package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/stockflow/server/internal/cache"
)

// redisStore 窄接口：仅声明本域用到的命令；*redis.Client 天然满足。
// 单元测试以内存替身实现本接口（ask 约束：单测不依赖 Redis/网络，用接口替身）。
type redisStore interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Incr(ctx context.Context, key string) *redis.IntCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
}

// 会话键规范 sf:{module}:{key}（cache 包规范；plan §7.2/§7.3）。
// 失败计数双维度（安全审查 S4）：sf:loginfail:{username}（用户名维度）与
// sf:loginfail:ip:{ip}（IP 维度，防跨用户名喷洒）。用户名规则（usernameRe）不含冒号，
// "ip:" 前缀不会与用户名维度键冲突。
func sessionKey(sid string) string   { return cache.Key("session", sid) }
func refreshKey(tok string) string   { return cache.Key("refresh", tok) }
func failKey(username string) string { return cache.Key("loginfail", username) }
func failIPDim(ip string) string     { return "ip:" + ip }
func permsKey(uid int64) string      { return cache.Key("perms", strconv.FormatInt(uid, 10)) }

// sessionData 会话快照（plan §7.2：存储用户与数据范围快照）。
// 注意：权限点集不进会话（独立缓存键 permsKey，RBAC 变更可定向失效）；
// 数据范围快照进会话——变更后最长 2h+7d 内不刷新，靠管理员强制下线兜底（plan §13.3 已知限制）。
type sessionData struct {
	SID                string    `json:"sid"`
	UserID             int64     `json:"user_id"`
	Username           string    `json:"username"`
	IsSuper            bool      `json:"is_super"`
	DataScope          string    `json:"data_scope"`
	WarehouseIDs       []int64   `json:"warehouse_ids"`
	DeptID             int64     `json:"dept_id"`
	MustChangePassword bool      `json:"must_change_password"`
	RefreshToken       string    `json:"refresh_token"` // 不透明随机串（plan §7.2）
	IP                 string    `json:"ip"`
	UserAgent          string    `json:"user_agent"`
	LoginAt            time.Time `json:"login_at"`
	LastActiveAt       time.Time `json:"last_active_at"`
}

// sessionStore Redis 会话存储：登录/刷新/登出/踢下线/会话列表。
type sessionStore struct {
	rdb redisStore
	cfg runtimeConfig
}

func newSessionStore(rdb redisStore, cfg runtimeConfig) *sessionStore {
	return &sessionStore{rdb: rdb, cfg: cfg}
}

// newOpaqueToken 生成 256bit 不透明随机串（Refresh Token / 会话 ID）。
func newOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成随机凭证失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Create 登记会话：session 键 + refresh 索引键，TTL 均为 refreshTTL。
func (s *sessionStore) Create(ctx context.Context, d *sessionData) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	if err := s.rdb.Set(ctx, sessionKey(d.SID), raw, s.cfg.refreshTTL).Err(); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	if err := s.rdb.Set(ctx, refreshKey(d.RefreshToken), d.SID, s.cfg.refreshTTL).Err(); err != nil {
		return fmt.Errorf("写入刷新凭证索引失败: %w", err)
	}
	return nil
}

// Get 读取会话；不存在返回 (nil, nil)（被踢/过期，上层转 401 SESSION_INVALID）。
func (s *sessionStore) Get(ctx context.Context, sid string) (*sessionData, error) {
	raw, err := s.rdb.Get(ctx, sessionKey(sid)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var d sessionData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("解析会话失败: %w", err)
	}
	return &d, nil
}

// Touch 滑动续期（plan §7.2 滑动 TTL）：每次认证请求延长会话与刷新索引。
func (s *sessionStore) Touch(ctx context.Context, d *sessionData) {
	// 滑动续期失败不影响本次请求（会话仍有效）；下次请求重试。
	_ = s.rdb.Expire(ctx, sessionKey(d.SID), s.cfg.refreshTTL).Err()
	_ = s.rdb.Expire(ctx, refreshKey(d.RefreshToken), s.cfg.refreshTTL).Err()
}

// Delete 删除会话（登出/踢下线）：session 键 + refresh 索引键一并清除，Token 立即失效。
func (s *sessionStore) Delete(ctx context.Context, d *sessionData) error {
	if err := s.rdb.Del(ctx, sessionKey(d.SID)).Err(); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	if d.RefreshToken != "" {
		if err := s.rdb.Del(ctx, refreshKey(d.RefreshToken)).Err(); err != nil {
			return fmt.Errorf("删除刷新凭证索引失败: %w", err)
		}
	}
	return nil
}

// List 汇总当前全部在线会话（SCAN sf:session:*）。
// Redis 为易失数据，本列表仅作运维视图（plan §7.2），登录记录以 login_logs 落库为准。
func (s *sessionStore) List(ctx context.Context) ([]*sessionData, error) {
	var out []*sessionData
	var cursor uint64 = 0
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, sessionKey("")+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("扫描会话失败: %w", err)
		}
		for _, k := range keys {
			raw, err := s.rdb.Get(ctx, k).Result()
			if err != nil {
				continue // 扫描间隙过期的会话跳过，不中断列表
			}
			var d sessionData
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				continue // 坏行跳过（不应发生）
			}
			out = append(out, &d)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

// FindByRefreshToken 经刷新凭证索引定位会话；不存在返回 (nil, nil)。
func (s *sessionStore) FindByRefreshToken(ctx context.Context, token string) (*sessionData, error) {
	sid, err := s.rdb.Get(ctx, refreshKey(token)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询刷新凭证索引失败: %w", err)
	}
	return s.Get(ctx, sid)
}

// Rotate 刷新轮换（plan §7.2）：新 sid + 新刷新凭证，旧会话与旧索引立即作废。
func (s *sessionStore) Rotate(ctx context.Context, old *sessionData) (*sessionData, error) {
	sid, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	token, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	fresh := *old
	fresh.SID = sid
	fresh.RefreshToken = token
	fresh.LastActiveAt = time.Now()
	if err := s.Create(ctx, &fresh); err != nil {
		return nil, err
	}
	if err := s.Delete(ctx, old); err != nil {
		return nil, err
	}
	return &fresh, nil
}

// loginGuard 失败计数保护（permission.md §3.2、plan §7.3；登录与改密保护共用骨架）：
// Redis 计数 sf:{prefix}:{dim}（TTL=窗口），锁定落库（账户行）由 Service 负责。
// prefix 为空时回退 loginfail（config.go 以零值构造默认登录保护，见 key()）。
type loginGuard struct {
	rdb         redisStore
	maxFailures int
	window      time.Duration // 失败计数窗口（=锁定时长，plan §7.3）
	prefix      string        // 键模块段：loginfail（登录）/ pwdfail（改密，S15）
}

// key 失败计数键：sf:{prefix}:{dim}；dim 为维度值（用户名 / "ip:"+IP / "username|ip"）。
func (g *loginGuard) key(dim string) string {
	p := g.prefix
	if p == "" {
		p = "loginfail"
	}
	return cache.Key(p, dim)
}

// Fail 记一次失败，返回累计次数。首个计数设置窗口 TTL。
func (g *loginGuard) Fail(ctx context.Context, dim string) (int64, error) {
	n, err := g.rdb.Incr(ctx, g.key(dim)).Result()
	if err != nil {
		return 0, fmt.Errorf("累加登录失败计数失败: %w", err)
	}
	if n == 1 {
		// TTL=锁定窗口（plan §7.3）：窗口内的连续失败累计，窗口自然滑过即清零。
		if err := g.rdb.Expire(ctx, g.key(dim), g.window).Err(); err != nil {
			return n, fmt.Errorf("设置失败计数窗口失败: %w", err)
		}
	}
	return n, nil
}

// Reset 清空失败计数（登录成功/管理员解锁）。
func (g *loginGuard) Reset(ctx context.Context, dim string) error {
	return g.rdb.Del(ctx, g.key(dim)).Err()
}

// Count 当前失败计数（无记录为 0）。
func (g *loginGuard) Count(ctx context.Context, dim string) (int64, error) {
	raw, err := g.rdb.Get(ctx, g.key(dim)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取登录失败计数失败: %w", err)
	}
	return strconv.ParseInt(raw, 10, 64)
}
