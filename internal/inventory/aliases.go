package inventory

import (
	"github.com/stockflow/server/internal/stock"
)

// 类型别名承接 internal/stock 契约值类型（backend-m2-plan §3/§8.3 条 4，MT0 一次性改动；
// 此后本包既有文件冻结）。
//
// 设计（plan §3）：internal/stock 是叶子契约包（不依赖任何内部业务包），承载 M1 冻结的
// 库存原语值形状；本包以类型别名承接后类型恒等——M1 既有代码与测试零逻辑改动，
// 四个 M2 单据域的消费接口（StockGateway 等）以 stock 值类型定义签名，
// *inventory.Service 结构化满足，router 无需逐字段桥接（plan §3.1）。
//
// 约束（plan §3）：stock 包禁止出现任何 SQL/IO；库存族六表写路径仍仅限本包
// （plan §2.3 判据 3）；本包领域模型（Inventory/InventoryLedger 等，models.go）
// 仍仅可被本包命名（判据 3 类型级防线）。

type (
	// Qty numeric(18,4) 精确十进制数量（M1 冻结形状，stock/qty.go）。
	Qty = stock.Qty
	// Actor 操作者归因。
	Actor = stock.Actor
	// RowKey 五维定位键。
	RowKey = stock.RowKey
	// Source 来源单据。
	Source = stock.Source
	// MutationResult 变更结果（Adjust 携带 AdjustmentNo，plan §8.3 条 3）。
	MutationResult = stock.MutationResult
	// LedgerRef 流水引用。
	LedgerRef = stock.LedgerRef

	// 九个库存原语入参（stock/ops.go、stock/transfer_ops.go；字段与 M1 定义一字未改，
	// M2 增量：PutawayOp/DeductOp/InspectResultOp/AdjustOp.SerialNo、
	// AdjustOp.ExistingAdjustmentID——plan §8.3 条 1/条 3）。
	PutawayOp       = stock.PutawayOp
	LockOp          = stock.LockOp
	ReleaseLockOp   = stock.ReleaseLockOp
	DeductOp        = stock.DeductOp
	MoveBinOp       = stock.MoveBinOp
	InspectResultOp = stock.InspectResultOp
	AdjustOp        = stock.AdjustOp
	BatchOp         = stock.BatchOp
	SerialOp        = stock.SerialOp
	TransferOutOp   = stock.TransferOutOp
	TransferInOp    = stock.TransferInOp
)

// ParseQty 十进制字面量解析（M1 导出面保持；实现承接 stock.ParseQty）。
func ParseQty(s string) (Qty, error) { return stock.ParseQty(s) }

// qtyScale numeric(18,4) 标度（与 stock/qty.go 同值；包内测试与既有代码引用）。
const qtyScale int64 = 10000
