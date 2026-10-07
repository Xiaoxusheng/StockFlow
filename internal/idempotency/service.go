package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"fmt"

	"github.com/stockflow/server/internal/response"
	"go.uber.org/zap"
)

// 服务层：键校验、占用仲裁、快照收尾（doc.go 语义冻结）。
// 调用方两形态：
//   - 中间件（middleware.go）：高风险端点零改动接入；
//   - 手动辅助（Acquire/Complete/Release 导出）：端点需在业务事务边界精确控制
//     占用/收尾时点时使用（与中间件共用同一 Service 语义，不设第二套）。

const (
	// maxSnapshotBytes 响应快照字节上限（统一信封端点响应为 KB 量级；128KB 留
	// 百倍余量。超限响应不缓存——Release 释放占用，重试语义退化为重执行，日志披露）。
	maxSnapshotBytes = 128 << 10

	// defaultLeaseTTL 占用租约 TTL（PROCESSING 滞留惰性回收判定窗口）：命中
	// PROCESSING 且 created_at 早于 now-TTL 视为持有者失联（收尾失败/硬崩溃，
	// Release 不可达），同键重试以原子 UPDATE 抢占接管、按全新请求处理。
	// 取 3h——必须大于最长业务任务执行时长：异步任务入队显式超时为 2h
	//（internal/asynqx DefaultTimeout，防 asynq 库默认 30 分钟误杀超大导入/导出），
	// TTL 须覆盖其上，否则仍在途的长任务（如 200MB 级导出重生成）会在执行中被
	// 并发同键重试误接管，破坏「恰一执行」。
	// 语义代价（doc.go 滞留回收）：TTL 后同键重放视为新请求，可能重复执行——
	// 风险口径与无幂等防护的既有基线一致，库存原语另受
	// inventory_ledgers.idempotency_key 唯一索引兜底（api.md §7）。
	defaultLeaseTTL = 3 * time.Hour
)

// keyRegexp 幂等键值域（与 000024 CHECK 同式：UUID 与常见客户端生成器兼容）。
var keyRegexp = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// Service 幂等业务服务。
type Service struct {
	repo Repo
	log  *zap.Logger
}

// NewService 装配（RegisterRoutes/router 装配唯一调用点；log nil = 静默）。
func NewService(repo Repo, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, log: log}
}

// HashRequest 请求体 SHA-256（hex 64；空体为确定性哈希——同键空体提交互为重放）。
func HashRequest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Lease 一次幂等占用结果。
type Lease struct {
	id      int64
	userID  int64
	leaseID bool // 占用成功
	replay  *Snapshot
}

// Acquired 是否占用成功（true = 调用方应真实执行，收尾必调 Complete/Release）。
func (l Lease) Acquired() bool { return l.leaseID }

// Replay 非 nil = 键命中既有快照，调用方应原样回放、不执行业务。
func (l Lease) Replay() *Snapshot { return l.replay }

// LeaseID 占用行 id（手动模式 Complete/Release 定位用；中间件模式无需感知）。
func (l Lease) LeaseID() int64 { return l.id }

// Acquire 占用执行权：
//   - 键非法 → ErrKeyInvalid；
//   - 占用成功 → Lease{Acquired: true}；
//   - 命中 COMPLETED → Lease{Replay: 快照}（快照损坏自愈：释放脏行后按 IN_PROGRESS
//     拒绝，日志披露，同键重试恢复全新执行）；
//   - 命中 PROCESSING → ErrInProgress（并发同键第二请求）；其中 created_at 超
//     defaultLeaseTTL 的滞留行除外——原子抢占接管该键，按全新请求处理（TTL 后
//     同键重放视为新请求，doc.go 滞留回收语义）；
//   - 同键换载荷 → ErrRequestMismatch（TTL 不削弱该防御：换载荷即使命中滞留行
//     也拒绝，接管仅面向同载荷的合法重试）。
func (s *Service) Acquire(ctx context.Context, userID int64, endpoint, key, requestHash string) (Lease, error) {
	if !keyRegexp.MatchString(key) {
		return Lease{}, response.NewError(ErrKeyInvalid, map[string]any{"field": "Idempotency-Key"})
	}
	k := &Key{
		Key:         key,
		UserID:      userID,
		Endpoint:    endpoint,
		RequestHash: requestHash,
		Status:      StatusProcessing,
		CreatedBy:   userID,
		UpdatedBy:   userID,
	}
	inserted, err := s.repo.TryInsert(ctx, k)
	if err != nil {
		return Lease{}, err
	}
	if inserted {
		return Lease{id: k.ID, userID: userID, leaseID: true}, nil
	}
	row, err := s.repo.Find(ctx, key, userID, endpoint)
	if err != nil {
		return Lease{}, err
	}
	if row == nil {
		// 唯一索引命中但读不到行：并发窗口内的极端竞态（如占用方刚释放）。按
		// IN_PROGRESS 保守拒绝——下一次提交可重新占用，不伪造执行权。
		s.log.Warn("idempotency: 唯一索引命中但占用行缺失",
			zap.String("endpoint", endpoint), zap.Int64("user_id", userID))
		return Lease{}, response.NewError(ErrInProgress, nil)
	}
	if row.RequestHash != requestHash {
		return Lease{}, response.NewError(ErrRequestMismatch, map[string]any{
			"endpoint": endpoint,
		})
	}
	if row.Status == StatusCompleted {
		snap, perr := parseSnapshot(row.ResponseSnapshot)
		if perr != nil {
			// 快照损坏（不该发生）：释放脏行自愈，同键重试恢复全新执行。
			s.log.Error("idempotency: 响应快照损坏，已释放占用行",
				zap.Int64("id", row.ID), zap.Error(perr))
			if _, rerr := s.repo.Release(ctx, row.ID); rerr != nil {
				s.log.Error("idempotency: 脏快照行释放失败", zap.Int64("id", row.ID), zap.Error(rerr))
			}
			return Lease{}, response.NewError(ErrInProgress, nil)
		}
		return Lease{id: row.ID, userID: userID, replay: snap}, nil
	}
	// PROCESSING：TTL 惰性回收——占用行租约超时视为持有者失联（快照落库失败/
	// 硬崩溃后 Release 不可达，行滞留），原子抢占接管、按全新请求处理，不再永久 409。
	cutoff := time.Now().Add(-defaultLeaseTTL)
	if row.CreatedAt.Time.Before(cutoff) {
		n, perr := s.repo.PreemptExpired(ctx, row.ID, userID, requestHash, cutoff)
		if perr != nil {
			return Lease{}, perr
		}
		if n > 0 {
			s.log.Warn("idempotency: 占用行滞留 PROCESSING 超租约 TTL，已原子抢占接管（同键重试按全新请求执行）",
				zap.Int64("id", row.ID), zap.String("endpoint", endpoint), zap.Int64("user_id", userID))
			return Lease{id: row.ID, userID: userID, leaseID: true}, nil
		}
		// 抢占失利（0 行）：原持有者恰在此窗口完成收尾（COMPLETED，下次提交可回放）
		// 或并发抢占方已刷新租约——保守按 IN_PROGRESS 拒绝，不伪造执行权。
		s.log.Warn("idempotency: 滞留占用行抢占失利（并发收尾或已被并发方接管）",
			zap.Int64("id", row.ID), zap.String("endpoint", endpoint), zap.Int64("user_id", userID))
	}
	return Lease{}, response.NewError(ErrInProgress, nil)
}

// Complete 占用收尾：落响应快照并置 COMPLETED。body 必须为合法 JSON 且
// ≤ maxSnapshotBytes（统一信封端点恒满足）；不满足时释放占用并记日志——该响应
// 不参与回放（诚实降级，不伪造快照），日志披露。
func (s *Service) Complete(ctx context.Context, lease Lease, status int, body []byte) {
	if !lease.leaseID {
		return
	}
	if len(body) == 0 || len(body) > maxSnapshotBytes || !json.Valid(body) {
		s.log.Warn("idempotency: 响应不可缓存（空/超限/非 JSON），释放占用",
			zap.Int64("id", lease.id), zap.Int("status", status), zap.Int("bytes", len(body)))
		s.releaseQuietly(ctx, lease)
		return
	}
	raw, err := json.Marshal(Snapshot{Status: status, Body: json.RawMessage(body)})
	if err != nil {
		s.log.Error("idempotency: 快照序列化失败", zap.Int64("id", lease.id), zap.Error(err))
		s.releaseQuietly(ctx, lease)
		return
	}
	if err := s.repo.Complete(ctx, lease.id, lease.userID, raw); err != nil {
		// 收尾失败：行滞留 PROCESSING——同键重试 409 直至租约超时（defaultLeaseTTL）
		// 由后续同键同载荷重试惰性抢占回收，不再依赖定时清理作业——如实记录。
		s.log.Error("idempotency: 快照落库失败（占用行滞留 PROCESSING，超 TTL 后经同键重试惰性回收）",
			zap.Int64("id", lease.id), zap.Error(err))
	}
}

// Release 释放占用（执行中断：panic / 响应不可缓存）。仅 PROCESSING 行受影响。
func (s *Service) Release(ctx context.Context, lease Lease) {
	if !lease.leaseID {
		return
	}
	s.releaseQuietly(ctx, lease)
}

func (s *Service) releaseQuietly(ctx context.Context, lease Lease) {
	if _, err := s.repo.Release(ctx, lease.id); err != nil {
		s.log.Error("idempotency: 占用行释放失败", zap.Int64("id", lease.id), zap.Error(err))
	}
}

// parseSnapshot jsonb → Snapshot（库内形态契约校验：status 必须为合法 HTTP 状态码、
// body 必须为合法 JSON——仅脏数据路径触发，调用方按损坏自愈处理）。
func parseSnapshot(raw jsonb) (*Snapshot, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("idempotency: 响应快照为空")
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("idempotency: 响应快照解析失败: %w", err)
	}
	if snap.Status < 200 || snap.Status > 599 || len(snap.Body) == 0 || !json.Valid(snap.Body) {
		return nil, fmt.Errorf("idempotency: 响应快照形态非法（status=%d bytes=%d）", snap.Status, len(snap.Body))
	}
	return &snap, nil
}

// EndpointOf 端点维度字符串（method + 路由模式；中间件与手动模式共用同一口径，
// 防两处拼法漂移导致同端点键空间分裂）。
func EndpointOf(method, routePattern string) string {
	return method + " " + routePattern
}
