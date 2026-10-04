package stock

// MutationResult / LedgerRef——库存原语变更结果值类型
// （自 internal/inventory/service.go:99-110 原样搬迁，backend-m2-plan §3 冻结形状）。

// MutationResult 变更结果。流水是库存变更的最小审计单元；Lock 类另带锁记录 ID。
// AdjustmentNo 为 M2 增量字段（backend-m2-plan §8.3 条 3）：Adjust 原语落账的
// inventory_adjustments.adjustment_no（其余原语恒为空串）——调用方（盘点差异执行）
// 以原语返回值回写差异行，替代"按 qty+type 精确匹配回查"的脆弱实现。
type MutationResult struct {
	Replay       bool      `json:"replay"` // true=幂等重放（未再次变更库存，plan §8.5）
	Ledger       LedgerRef `json:"ledger"` // 本次（或既有）流水
	LockID       int64     `json:"lock_id,omitempty"`
	AdjustmentNo string    `json:"adjustment_no,omitempty"` // Adjust：调整单单号（plan §8.3 条 3）
}

// LedgerRef 流水引用。
type LedgerRef struct {
	ID       int64  `json:"id"`
	LedgerNo string `json:"ledger_no"`
}
