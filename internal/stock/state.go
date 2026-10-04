package stock

import (
	"fmt"
)

// StockState / StateColumn / 锁定类型——库存状态值类型（自 internal/inventory/identity.go
// 与 service.go:120-138 原样搬迁，backend-m2-plan §3 冻结形状；
// identity.go 内服务原生 SQL 拼装使用的未导出 dbColumn/getOf 不属于值形状，不随迁）。

// StockState 库存六状态数量（backend-m1-plan §8.1 冻结六列；database.md §3 但书；
// inventory-rules §2 状态维度）。
//
// 一致性恒等式（inventory-rules §2，所有时刻、所有代码路径必须满足）：
//
//	total = available + locked + frozen + pending_inspect + defective
//
// 该恒等式由三道防线共同保证：
//  1. 构造保证：Service 每条 UPDATE 成对改列、和不变（plan §8.1）；
//  2. PostgreSQL CHECK 约束 chk_inventory_identity（迁移 000005）——被破坏的写库无法提交；
//  3. 本函数：测试与调用方显式断言（testing.md §4）。
type StockState struct {
	Total          Qty `json:"total_qty"`
	Available      Qty `json:"available_qty"`
	Locked         Qty `json:"locked_qty"`
	Frozen         Qty `json:"frozen_qty"`
	PendingInspect Qty `json:"pending_inspect_qty"`
	Defective      Qty `json:"defective_qty"`
}

// SumUnavailable 不可用量之和 = 锁定 + 冻结 + 待检 + 不良品（+ 其他不可用）。
func (s StockState) SumUnavailable() Qty {
	return s.Locked + s.Frozen + s.PendingInspect + s.Defective
}

// AvailableOf 按恒等式反推可用量（inventory-rules §2）：
//
//	可用 = 总库存 - 锁定 - 冻结 - 待检 - 不良品 - 其他不可用
//
// M1 schema 冻结六列、无"其他不可用"列（backend-m1-plan §8.1），该项恒为 0；
// M2 扩展状态列时必须同步扩展本函数、CHECK 约束与迁移（文档先行）。
// 注意：与状态列 Available 的差值即为恒等式偏差，ValidateIdentity 据此判定。
func (s StockState) AvailableOf() Qty {
	return s.Total.Sub(s.SumUnavailable())
}

// ValidateIdentity 校验恒等式与非负（inventory-rules §2）：
// 六列均 ≥ 0，且 Available == AvailableOf()。
func (s StockState) ValidateIdentity() error {
	for name, v := range map[string]Qty{
		"total": s.Total, "available": s.Available, "locked": s.Locked,
		"frozen": s.Frozen, "pending_inspect": s.PendingInspect, "defective": s.Defective,
	} {
		if v.IsNegative() {
			return fmt.Errorf("库存恒等式校验失败: %s 为负数（%s）", name, v.String())
		}
	}
	if s.Available != s.AvailableOf() {
		return fmt.Errorf("库存恒等式校验失败: total(%s) != available(%s)+locked(%s)+frozen(%s)+pending_inspect(%s)+defective(%s)",
			s.Total.String(), s.Available.String(), s.Locked.String(),
			s.Frozen.String(), s.PendingInspect.String(), s.Defective.String())
	}
	return nil
}

// StateColumn 库存状态列（inventory_ledgers.status_from/status_to 的值域，
// 迁移 CHECK 限定 available/locked/frozen/pending_inspect/defective 五值）。
// 列名只在 inventory 包白名单内取值，原生 SQL 不拼接任何外部输入（go-dev-standard 规则 8）。
type StateColumn int

const (
	ColAvailable StateColumn = iota
	ColLocked
	ColFrozen
	ColPendingInspect
	ColDefective
)

// String 流水 status_from/status_to 的值（迁移 chk_inventory_ledgers_status_from/to）。
func (c StateColumn) String() string {
	switch c {
	case ColAvailable:
		return "available"
	case ColLocked:
		return "locked"
	case ColFrozen:
		return "frozen"
	case ColPendingInspect:
		return "pending_inspect"
	case ColDefective:
		return "defective"
	}
	return "unknown"
}

// LockTypes 锁定类型（inventory-rules §4 五类；迁移 chk_inventory_locks_lock_type；
// 自 internal/inventory/service.go lockTypes 原样搬迁）。
var LockTypes = map[string]bool{
	"ORDER_HOLD": true, "COUNT_FREEZE": true, "QC_FREEZE": true,
	"MANUAL_FREEZE": true, "EXCEPTION_FREEZE": true,
}

// LockTargetState 锁定类型 → 目标状态列（自 internal/inventory/service.go lockTargetState
// 原样搬迁）：
//   - ORDER_HOLD（订单占用）→ locked（business-flow §7.2：审核预占，锁定库存）；
//   - COUNT_FREEZE/QC_FREEZE/MANUAL_FREEZE/EXCEPTION_FREEZE → frozen
//     （inventory-rules §2：质检冻结/人工冻结/异常冻结属"冻结库存"状态）。
func LockTargetState(lockType string) (StateColumn, bool) {
	switch lockType {
	case "ORDER_HOLD":
		return ColLocked, true
	case "COUNT_FREEZE", "QC_FREEZE", "MANUAL_FREEZE", "EXCEPTION_FREEZE":
		return ColFrozen, true
	}
	return ColAvailable, false
}
