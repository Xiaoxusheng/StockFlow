package stockops

// 仓内移库 HTTP 化（backend-m2-plan §8.3 条 5：POST /api/inventory/moves，本包注册，
// 权限点 stockops:move:execute）。业务动作 = inventory.MoveBin 原语（同仓同 SKU 同批次
// 跨库位，M1 仅移动可用库存）；本域做入参校验、来源归因与审计，不触碰库存族表。
//
// 来源单据（inventory-rules §5：任何库存变化必须可追溯来源单据）：移库为人工仓内作业，
// 无单据承载——source_type 固定 "stockops_move"，source_no 由请求携带的作业依据号
// （作业工单/异常单/审批记录号等）必填落账，流水经 business_no 追溯。

import (
	"context"
	"strings"

	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// responseError 域错误快捷构造（统一经 response 注册码输出，plan §2.3 判据 6）。
func responseError(code response.Code, details map[string]any) error {
	return response.NewError(code, details)
}

// MoveKeyInput 移库端五维键。
type MoveKeyInput struct {
	WarehouseID int64 `json:"warehouse_id"`
	ZoneID      int64 `json:"zone_id"`
	ShelfID     int64 `json:"shelf_id"`
	BinID       int64 `json:"bin_id"`
	SKUID       int64 `json:"sku_id"`
	BatchID     int64 `json:"batch_id"`
}

// MoveInput 仓内移库入参。
type MoveInput struct {
	From     MoveKeyInput `json:"from"`
	To       MoveKeyInput `json:"to"`
	Qty      string       `json:"qty"`       // numeric(18,4) 文本
	SourceNo string       `json:"source_no"` // 作业依据号（必填，流水追溯）
	Remark   string       `json:"remark"`
}

// MoveBin 仓内移库（可用库存，inventory.MoveBin 原语语义：源行 available/total 同减、
// 目标行同增、两行各一条 MOVE 流水）。
func (s *Service) MoveBin(ctx context.Context, actor stock.Actor, in MoveInput) (stock.MutationResult, error) {
	if s.gateway == nil {
		return stock.MutationResult{}, responseError(ErrReaderMissing, map[string]any{
			"reason": "库存原语网关未注入（router 装配 WithGateway，plan §4.3 规则①）",
		})
	}
	if in.From.WarehouseID <= 0 || in.From.BinID <= 0 || in.From.SKUID <= 0 {
		return stock.MutationResult{}, responseError(ErrMoveKeyInvalid, map[string]any{"field": "from"})
	}
	if in.To.WarehouseID <= 0 || in.To.BinID <= 0 || in.To.SKUID <= 0 {
		return stock.MutationResult{}, responseError(ErrMoveKeyInvalid, map[string]any{"field": "to"})
	}
	qty, err := stock.ParseQty(in.Qty)
	if err != nil || !qty.IsPositive() {
		return stock.MutationResult{}, responseError(ErrMoveQtyInvalid, map[string]any{
			"field": "qty", "value": strings.TrimSpace(in.Qty),
		})
	}
	if strings.TrimSpace(in.SourceNo) == "" {
		return stock.MutationResult{}, responseError(ErrMoveSourceRequired, map[string]any{
			"field": "source_no", "reason": "移库必须携带作业依据号（inventory-rules §5 来源追溯）",
		})
	}
	return s.gateway.MoveBin(ctx, nil, stock.MoveBinOp{
		From: stock.RowKey{
			WarehouseID: in.From.WarehouseID, ZoneID: in.From.ZoneID, ShelfID: in.From.ShelfID,
			BinID: in.From.BinID, SKUID: in.From.SKUID, BatchID: in.From.BatchID,
		},
		To: stock.RowKey{
			WarehouseID: in.To.WarehouseID, ZoneID: in.To.ZoneID, ShelfID: in.To.ShelfID,
			BinID: in.To.BinID, SKUID: in.To.SKUID, BatchID: in.To.BatchID,
		},
		Qty:    qty,
		Source: stock.Source{Type: "stockops_move", No: strings.TrimSpace(in.SourceNo)},
		Actor:  actor,
		Remark: strings.TrimSpace(in.Remark),
	})
}
