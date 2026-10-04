package devices

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// 重复事件去重窗口（scanner.md §6.6）：
// 扫码设备可能因配置问题重复发送（如 `SKU001 Enter` 连续两次）。极短时间内完全相同的
// 事件（同码 + 同页面 + 同任务上下文）按重复事件识别；正常连续扫码（如同 SKU 连续扫
// 10 次计数收货）允许重复。
//
// 落位口径（plan §8.3 与 ask 交付的收敛，DomainResult 偏离记录）：
//   - resolve 的识别结果恒完整（不因窗口抑制识别——识别与业务执行分离，业务幂等准绳
//     仍在各域业务 API 的幂等体系）；
//   - 窗口命中仅在响应标记 duplicate=true 供前端/作业端提示，同时 scan_logs 照常落库
//     （append-only 审计，不丢事件）；
//   - 进程内内存实现（惰性清扫，零后台 goroutine——go-dev-standard 规则 3），单实例语义；
//     多副本部署下窗口各自独立（属 M4 部署议题，plan §18.7 同口径）。

// DefaultDedupTTL 默认去重窗口（"极短时间"冻结默认值：连续扫码的最小人工间隔远大于 2s）。
const DefaultDedupTTL = 2 * time.Second

// dedupMaxKeys 窗口键容量上限（防无界内存增长——go-dev-standard 规则：不让数据无限增长；
// 超限立即全量清扫，仍超限按最旧丢弃由 sweep 完成）。
const dedupMaxKeys = 4096

// DedupWindow 重复事件去重窗口。
type DedupWindow struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
	last time.Time // 上次清扫时刻（惰性清扫节流）
	now  func() time.Time
}

// NewDedupWindow 构造去重窗口（ttl<=0 取默认 2s）。
func NewDedupWindow(ttl time.Duration) *DedupWindow {
	if ttl <= 0 {
		ttl = DefaultDedupTTL
	}
	return &DedupWindow{
		ttl:  ttl,
		seen: make(map[string]time.Time),
		now:  time.Now,
	}
}

// DedupKey 组装去重键：来源（设备编码或用户 ID）+ 页面 + 条码 + 业务上下文
// （scanner.md §6.6：同码 + 同页面 + 同任务上下文）。
func DedupKey(source, page, code, context string) string {
	sum := sha256.Sum256([]byte(source + "\x1f" + page + "\x1f" + code + "\x1f" + context))
	return hex.EncodeToString(sum[:])
}

// Mark 记录一次事件：窗口内已存在同键事件返回 false（重复事件），否则登记并返回
// true（首次事件）。重复事件刷新时间戳（滑动窗口语义）。
func (w *DedupWindow) Mark(key string) bool {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sweepLocked(now)
	if t, ok := w.seen[key]; ok && now.Sub(t) < w.ttl {
		w.seen[key] = now
		return false
	}
	w.seen[key] = now
	return true
}

// sweepLocked 惰性清扫：到期键删除；节流条件（30s 或容量超限）不满足时跳过全量扫描。
func (w *DedupWindow) sweepLocked(now time.Time) {
	if len(w.seen) < dedupMaxKeys && now.Sub(w.last) < 30*time.Second {
		return
	}
	for k, t := range w.seen {
		if now.Sub(t) >= w.ttl {
			delete(w.seen, k)
		}
	}
	w.last = now
}

// Len 当前窗口键数（测试/监控用）。
func (w *DedupWindow) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.seen)
}
