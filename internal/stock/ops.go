package stock

import (
	"github.com/stockflow/server/internal/database"
)

// 九个库存原语入参 Op 结构（自 internal/inventory/service.go 原样搬迁，
// backend-m2-plan §3 冻结形状——Putaway/Lock/ReleaseLock/Deduct/MoveBin/InspectResult/
// Adjust/Batch/Serial；字段名、类型、json tag 一字未改）。
//
// 唯一有意的包级例外：BatchOp 的三个时间字段沿用平台值类型 internal/database.JSONTime
// （批次生产/入库/效期日期），以保证 inventory 侧别名承接（type BatchOp = stock.BatchOp）
// 后类型恒等、M1 既有代码零改动——本包因而不依赖任何业务域包，仅依赖平台数据库值类型。

// PutawayOp 上架增加入参。
//
// 语义（plan §8.2"上架增加：pending_inspect→available（或免检直达 available），total 增"
// 的展开）：本原语负责"增加"——
//   - RequireInspect=false：免检直达 available（total、available 同增）；
//   - RequireInspect=true：进入待检（total、pending_inspect 同增，change_type=INBOUND、
//     status_to=pending_inspect），质检后经 InspectResult 转合格（pending→available）或
//     不良（pending→defective）——两者组合即完整表达"pending_inspect→available"路径。
type PutawayOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	RequireInspect bool   `json:"require_inspect"`
	SerialNo       string `json:"serial_no"` // 序列号 SKU 单件操作携带（流水 serial_no，plan §8.3 条 1）
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// LockOp 预占/冻结入参（inventory-rules §4 五类锁定）。
type LockOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	LockType       string `json:"lock_type"`
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// ReleaseLockOp 释放锁定入参。
//
// 签名说明：plan §8.2 草图为 ReleaseLock(ctx, tx, lockID, qty)；为满足 §4.4
// "inventory Service 全部变更方法写操作日志"与 §8.5 幂等键，收拢为 op 结构
// （与"op 结构均携带可选 IdempotencyKey"的全局约定一致），lockID/qty 语义不变。
type ReleaseLockOp struct {
	LockID         int64  `json:"lock_id"`
	Qty            Qty    `json:"qty"`
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// DeductOp 出库扣减入参。
type DeductOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	LockID         int64  `json:"lock_id"`   // 可选：核销指定锁定记录（business-flow §8.5 发货核销）
	SerialNo       string `json:"serial_no"` // 序列号 SKU 单件操作携带（流水 serial_no，plan §8.3 条 1）
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// MoveBinOp 仓内移库入参（同仓同 SKU 同批次跨库位；M1 仅移动可用库存，
// 锁定/冻结库存的移库随 M2 调拨域的专用原语交付）。
type MoveBinOp struct {
	From           RowKey `json:"from"`
	To             RowKey `json:"to"`
	Qty            Qty    `json:"qty"`
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// InspectResultOp 待检处理结果入参。
type InspectResultOp struct {
	Key            RowKey `json:"key"`
	Qty            Qty    `json:"qty"`
	Pass           bool   `json:"pass"`      // true=合格转 available（INSPECT_PASS）；false=不良转 defective
	SerialNo       string `json:"serial_no"` // 序列号 SKU 单件操作携带（流水 serial_no，plan §8.3 条 1）
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
	Remark         string `json:"remark"`
}

// AdjustOp 库存调整入参（business-flow §11.1：申请必填原因 → 审核 → 执行 → 生成流水；
// M1 无审批流，执行即落账 status=EXECUTED 并留 executed_by/at）。
type AdjustOp struct {
	Key        RowKey `json:"key"`
	AdjustType string `json:"adjust_type"` // 盘盈/盘亏/损耗/报废/其他
	Qty        Qty    `json:"qty"`         // 恒为正；方向由 AdjustType 决定
	Reason     string `json:"reason"`      // 必填（business-flow §11.1）
	// ExistingAdjustmentID 既有调整单 ID（backend-m2-plan §8.3 条 3）：>0 时不再
	// insertAdjustment，而按 ID 将既有调整单（APPROVED）守卫更新为 EXECUTED（调整审批流
	// 复用）；≤0 行为与 M1 完全一致（执行即落账，insertAdjustment）。
	ExistingAdjustmentID int64  `json:"existing_adjustment_id,omitempty"`
	SerialNo             string `json:"serial_no"` // 序列号件联动调整的流水 serial_no（plan §8.3 条 1）
	Source               Source `json:"source"`
	Actor                Actor  `json:"actor"`
	IdempotencyKey       string `json:"idempotency_key"`
	Remark               string `json:"remark"`
}

// BatchOp 批次建立入参（批次台账，inventory-rules §6）。
type BatchOp struct {
	SKUID          int64             `json:"sku_id"`
	BatchNo        string            `json:"batch_no"`
	SupplierID     int64             `json:"supplier_id"`
	ProductionDate database.JSONTime `json:"production_date"`
	InboundDate    database.JSONTime `json:"inbound_date"`
	ExpiryDate     database.JSONTime `json:"expiry_date"`
	CostPrice      Qty               `json:"cost_price"`
	Actor          Actor             `json:"actor"`
	Remark         string            `json:"remark"`
}

// SerialOp 序列号状态变化入参（inventory-rules §8：每次变化记录来源单据/位置/操作人/时间）。
type SerialOp struct {
	SerialNo       string `json:"serial_no"`
	SKUID          int64  `json:"sku_id"`
	BatchID        int64  `json:"batch_id"`
	WarehouseID    int64  `json:"warehouse_id"` // 0=不在库
	BinID          int64  `json:"bin_id"`       // 0=不在库
	Status         string `json:"status"`       // IN_STOCK/LOCKED/OUTBOUND/RETURNED/FROZEN
	Source         Source `json:"source"`
	Actor          Actor  `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"` // 预留：序列号事件经唯一键天然幂等
	Remark         string `json:"remark"`
}
