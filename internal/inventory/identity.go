package inventory

import (
	"fmt"
)

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
// 列名只在本包白名单内取值，原生 SQL 不拼接任何外部输入（go-dev-standard 规则 8）。
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

// dbColumn 物理列名（白名单映射，供原生 SQL 构造）。
func (c StateColumn) dbColumn() string {
	switch c {
	case ColAvailable:
		return "available_qty"
	case ColLocked:
		return "locked_qty"
	case ColFrozen:
		return "frozen_qty"
	case ColPendingInspect:
		return "pending_inspect_qty"
	case ColDefective:
		return "defective_qty"
	}
	return ""
}

// getOf 读取状态列数量。
func (c StateColumn) getOf(s StockState) Qty {
	switch c {
	case ColAvailable:
		return s.Available
	case ColLocked:
		return s.Locked
	case ColFrozen:
		return s.Frozen
	case ColPendingInspect:
		return s.PendingInspect
	case ColDefective:
		return s.Defective
	}
	return 0
}
