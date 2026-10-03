package inventory

import (
	"fmt"
	"sort"
	"time"
)

// 批次出库分配策略纯函数（inventory-rules §6/§7.2）：
// 给定候选批次列表 → 出库顺序（每批取多少）。纯函数、无 IO、无数据库依赖，
// 策略命中由 M2 出库分配域调用；分配只允许使用可用库存（inventory-rules §4）。

// AllocationCandidate 出库分配候选批次。
type AllocationCandidate struct {
	BatchID      int64
	BatchNo      string
	ExpiryDate   *time.Time // 无效期管理 SKU 为 nil（FEFO 排序末位：不会到期，最后消耗）
	InboundDate  *time.Time // 入库日期（FIFO 排序键；nil 视为入库时间未知，排序末位）
	AvailableQty Qty        // 候选可用量（≤0 的候选不参与分配）
}

// AllocationStep 分配步骤：从指定批次取多少。
type AllocationStep struct {
	BatchID int64  `json:"batch_id"`
	BatchNo string `json:"batch_no"`
	Take    Qty    `json:"take"`
}

// AllocationShortageError 分配缺口：候选可用量之和不足以满足需求
// （api.md §4：错误 details 携带需求量与实际可得量）。
type AllocationShortageError struct {
	Needed  Qty // 需求量
	Offered Qty // 候选可用量合计
}

func (e *AllocationShortageError) Error() string {
	return fmt.Sprintf("批次可用量不足: 需求 %s，候选合计 %s", e.Needed.String(), e.Offered.String())
}

// allocate 按 less 给定的全序做贪心分配（FEFO/FIFO 共用骨架）。
// less 返回 true 表示 a 先于 b 出库；排序为稳定全序（并列时按 BatchID 升序兜底，
// 保证同一输入永远得到同一分配顺序，杜绝分配抖动）。
func allocate(candidates []AllocationCandidate, need Qty, less func(a, b AllocationCandidate) bool) ([]AllocationStep, error) {
	if !need.IsPositive() {
		return nil, fmt.Errorf("分配需求量必须为正数（got %s）", need.String())
	}
	for _, c := range candidates {
		if c.AvailableQty.IsNegative() {
			return nil, fmt.Errorf("候选批次 %d 可用量为负数（%s），输入非法", c.BatchID, c.AvailableQty.String())
		}
	}
	// 拷贝后排序：不修改调用方切片（纯函数无副作用）。
	// 比较器：策略序（less）优先，策略上真正并列（less 双向皆否）时按 BatchID
	// 升序兜底——保持严格弱序一致性，同一输入永远得到同一分配顺序。
	sorted := make([]AllocationCandidate, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		si, sj := sorted[i], sorted[j]
		if less(si, sj) {
			return true
		}
		if less(sj, si) {
			return false
		}
		return si.BatchID < sj.BatchID
	})

	steps := make([]AllocationStep, 0, len(sorted))
	remain := need
	var offered Qty
	for _, c := range sorted {
		if remain.IsZero() {
			break
		}
		offered = offered.Add(c.AvailableQty)
		if c.AvailableQty.IsZero() {
			continue
		}
		take := c.AvailableQty
		if take.Sub(remain).IsPositive() {
			take = remain
		}
		steps = append(steps, AllocationStep{BatchID: c.BatchID, BatchNo: c.BatchNo, Take: take})
		remain = remain.Sub(take)
	}
	if !remain.IsZero() {
		return nil, &AllocationShortageError{Needed: need, Offered: offered}
	}
	return steps, nil
}

// earlier 排序辅助：时间早者在前，nil（未知）在末位。
func earlier(a, b *time.Time) (aFirst, bFirst bool) {
	switch {
	case a == nil && b == nil:
		return false, false
	case a == nil:
		return false, true
	case b == nil:
		return true, false
	default:
		return a.Before(*b), b.Before(*a)
	}
}

// AllocateFEFO 先到期先出（inventory-rules §7.2 FEFO 出库分配策略）：
// 有效期升序（先到期先出），无有效期批次排末位；同效期按入库日期升序；
// 再按 BatchID 升序兜底（确定性全序）。
func AllocateFEFO(candidates []AllocationCandidate, need Qty) ([]AllocationStep, error) {
	return allocate(candidates, need, func(a, b AllocationCandidate) bool {
		if aFirst, bFirst := earlier(a.ExpiryDate, b.ExpiryDate); aFirst || bFirst {
			return aFirst
		}
		if aFirst, bFirst := earlier(a.InboundDate, b.InboundDate); aFirst || bFirst {
			return aFirst
		}
		return false
	})
}

// AllocateFIFO 先进先出（inventory-rules §6：批次在出库分配环节被策略命中）：
// 入库日期升序，入库日期未知批次排末位；再按 BatchID 升序兜底。
func AllocateFIFO(candidates []AllocationCandidate, need Qty) ([]AllocationStep, error) {
	return allocate(candidates, need, func(a, b AllocationCandidate) bool {
		if aFirst, bFirst := earlier(a.InboundDate, b.InboundDate); aFirst || bFirst {
			return aFirst
		}
		return false
	})
}
