package sales

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/response"
)

// 库存分配引擎（business-flow §8.1、plan §6.4/§7）。
//
// 流程（每明细行）：SKU 开关分支 → 批次 SKU 用 internal/inventory.AllocateFEFO/AllocateFIFO
// 纯函数命中批次（plan §6.4 字面口径）→ 逐批次读库位可用行、按 bin_id 升序确定消耗顺序 →
// 逐 (批次, 库位) 调用 StockGateway.Lock 预占（ORDER_HOLD，锁定量 = 分配量）→
// 生成 allocation_records（含分配理由 jsonb：策略命中原因 + 可用量快照，§8.1 展示义务）。
//
// 策略映射（效期管理必须先启用批次管理，masterdata assertFlagsBatchExpiry）：
//   - 批次 + 效期  → FEFO（先到期先出，inventory-rules §7.2）；
//   - 批次 + 非效期 → FIFO（先入先出，inventory-rules §6）；
//   - 非批次       → 库位升序直接消耗，策略落 FIFO（无批次维度退化为库位顺序，
//     reason 中注明"非批次 SKU：按库位升序消耗"）。
//
// 并发正确性：候选读取（只读）与逐 bin Lock 之间存在窗口，但 Lock 原语自带
// "业务层校验 + WHERE available_qty >= n 数据层守卫"（inventory-rules §9.3 双重校验），
// 守卫失败即整体回滚——分配永不超卖（宁可失败回滚，不静默部分成功）。
//
// 对 internal/inventory 的 import 限定说明：本文件是全包唯一引用点，且仅引用其
// 无 IO 的分配纯函数与 AllocationShortageError 值类型（plan §6.4 指定实现），
// 不触碰其 GORM 模型 / Service / SQL；internal/stock 承接纯函数后此 import 即可移除。

// allocResult 单行分配结果：落库记录 + 汇总量。
type allocResult struct {
	Records []AllocationRecord
}

// allocateLine 为一条订单明细执行默认策略分配并逐行预占（在调用方事务内）。
//
// 入参：warehouseID 出货仓；line 行信息；src 预占来源（销售订单，Lock source 与
// allocation_records.outbound_no 用出库单号、锁来源用销售单号——锁的来源单据是订单，
// business-flow §4.1"锁定必须记录来源单据"）。
func (s *Service) allocateLine(ctx context.Context, tx *gorm.DB, actor Actor,
	warehouseID int64, soNo, outboundNo string, line SalesOrderItem, flags SKUFlags) (allocResult, error) {
	return s.allocateLineWithKey(ctx, tx, actor, warehouseID, soNo, outboundNo, line, flags,
		// 首次审核预占幂等键（plan §7 冻结构成）：
		// {lock}:{so_no}:{line_no}:{bin_id}:{sku_id}:{batch_id}
		func(binID, batchID int64) string {
			return "lock:" + soNo + ":" + strconv.FormatInt(line.LineNo, 10) + ":" +
				strconv.FormatInt(binID, 10) + ":" + strconv.FormatInt(line.SKUID, 10) + ":" +
				strconv.FormatInt(batchID, 10)
		})
}

// allocateLineWithKey 为一条订单明细执行默认策略分配并逐行预占（在调用方事务内）。
// keyFn 构造预占幂等键（入参当前分配步骤的 batchID 与 binID）。重新分配传
// "relock:{so_no}:{line_no}:{seq}"（plan §7 冻结构成），避免命中旧锁的 LOCK 流水幂等键。
func (s *Service) allocateLineWithKey(ctx context.Context, tx *gorm.DB, actor Actor,
	warehouseID int64, soNo, outboundNo string, line SalesOrderItem, flags SKUFlags,
	keyFn func(binID, batchID int64) string) (allocResult, error) {

	var res allocResult
	if !flags.Enabled {
		return res, response.NewError(ErrSKUNotFound, map[string]any{"sku_id": line.SKUID, "line_no": line.LineNo})
	}
	strategy := AllocStrategyFIFO
	batchReason := "非批次 SKU：按库位升序消耗"

	var steps []inventory.AllocationStep
	if flags.BatchManaged {
		if flags.ExpiryManaged {
			strategy = AllocStrategyFEFO
			batchReason = "效期管理 SKU：先到期先出（FEFO，inventory-rules §7.2）"
		} else {
			strategy = AllocStrategyFIFO
			batchReason = "批次管理 SKU：先入先出（FIFO，inventory-rules §6）"
		}
		cands, err := s.repo.ReadBatchCandidates(tx, warehouseID, line.SKUID)
		if err != nil {
			return res, err
		}
		pure := make([]inventory.AllocationCandidate, 0, len(cands))
		for _, c := range cands {
			pc := inventory.AllocationCandidate{
				BatchID:      c.BatchID,
				BatchNo:      c.BatchNo,
				AvailableQty: inventory.Qty(c.AvailableQty), // 同标度 int64 冻结形状，显式精确转换
			}
			if c.ExpiryDate != nil {
				t := c.ExpiryDate.Time
				pc.ExpiryDate = &t
			}
			if c.InboundDate != nil {
				t := c.InboundDate.Time
				pc.InboundDate = &t
			}
			pure = append(pure, pc)
		}
		need := inventory.Qty(line.Qty)
		var aerr error
		if strategy == AllocStrategyFEFO {
			steps, aerr = inventory.AllocateFEFO(pure, need)
		} else {
			steps, aerr = inventory.AllocateFIFO(pure, need)
		}
		if aerr != nil {
			return res, shortageErr(warehouseID, line, aerr)
		}
	}

	if len(steps) == 0 {
		// 非批次 SKU：直接按库位升序消耗（单"批次"步退化处理）。
		steps = []inventory.AllocationStep{{BatchID: 0, Take: inventory.Qty(line.Qty)}}
	}

	for _, step := range steps {
		bins, err := s.repo.ReadBinStock(tx, warehouseID, line.SKUID, step.BatchID)
		if err != nil {
			return res, err
		}
		remain := Qty(step.Take) // inventory.Qty → 本域 Qty（同标度精确转换）
		for _, b := range bins {
			if remain.IsZero() {
				break
			}
			take := b.AvailableQty
			if take.Sub(remain).IsPositive() {
				take = remain
			}
			mut, err := s.stock.Lock(ctx, tx, LockOp{
				Key: RowKey{
					WarehouseID: b.WarehouseID, ZoneID: b.ZoneID, ShelfID: b.ShelfID,
					BinID: b.BinID, SKUID: line.SKUID, BatchID: step.BatchID,
				},
				Qty:            take,
				LockType:       "ORDER_HOLD",
				Source:         Source{Type: "sales_order", No: soNo},
				Actor:          actor,
				IdempotencyKey: keyFn(b.BinID, step.BatchID),
				Remark:         "销售订单审核预占",
			})
			if err != nil {
				return res, err
			}
			res.Records = append(res.Records, AllocationRecord{
				OutboundNo:  outboundNo,
				LineNo:      line.LineNo,
				SKUID:       line.SKUID,
				BatchID:     step.BatchID,
				WarehouseID: b.WarehouseID,
				BinID:       b.BinID,
				Qty:         take,
				Strategy:    strategy,
				Reason: map[string]any{
					"strategy":      strategy,
					"batch_reason":  batchReason,
					"bin_reason":    "同批次按库位升序消耗（确定性分配，杜绝分配抖动）",
					"need":          line.Qty.String(),
					"bin_available": b.AvailableQty.String(),
					"take":          take.String(),
					"batch_no":      step.BatchNo,
				},
				LockID:    mut.LockID,
				CreatedBy: actor.ID,
				UpdatedBy: actor.ID,
			})
			remain = remain.Sub(take)
		}
		if !remain.IsZero() {
			return res, shortageErr(warehouseID, line,
				fmt.Errorf("批次 %d 库位可用量之和不足以满足分配", step.BatchID))
		}
	}
	return res, nil
}

// shortageErr 分配缺口统一错误：透出 inventory.ErrNotEnough（plan §6.4"返回
// INVENTORY_NOT_ENOUGH"），details 携带行级需求量与实际可得量（api.md §4）。
func shortageErr(warehouseID int64, line SalesOrderItem, cause error) error {
	offered := ""
	var se *inventory.AllocationShortageError
	if errors.As(cause, &se) {
		offered = se.Offered.String()
	} else {
		offered = "0.0000"
	}
	return response.NewError(inventory.ErrNotEnough, map[string]any{
		"reason":       "可用库存不足，订单无法进入出库流程（business-flow §6.2）",
		"warehouse_id": warehouseID,
		"line_no":      line.LineNo,
		"sku_id":       line.SKUID,
		"need":         line.Qty.String(),
		"offered":      offered,
		"cause":        cause.Error(),
	})
}
