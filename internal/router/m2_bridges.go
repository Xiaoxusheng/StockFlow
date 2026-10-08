package router

// M2 跨域窄接口桥接（backend-m2-plan §3/§3.1 唯一跨域机制：接口定义在消费方域包，
// 实现由被消费域提供，router 装配注入——本文件是全部 M2 桥接的唯一落点，plan §9.4）。
//
// 桥接形态说明（M1 先例 internal/router/router.go binOccupancyBridge）：
//   - 库存原语网关：四个单据域以 internal/stock 值类型定义 StockGateway（plan §3 冻结
//     机制），inventory.Service 的别名承接（plan §8.3 条 4 aliases.go）尚未落地——
//     *inventory.Service 与 stock 值类型结构同形但类型不同名，按各域 ports.go 预留的
//     "收编前形态"以闭包/适配器逐字段桥接；字段清单 = plan §3 冻结形状（原样搬迁），
//     漂移将由域侧编译错暴露（字段删除）或在评审发现（字段新增），不静默吞字段。
//   - SKU 开关/供应商/客户校验：masterdata 侧导出实现（m2readers.go，plan §3.1），
//     签名内建类型者直接结构化满足；消费方携带域类型 Flags 的（purchase/sales/
//     stockops）逐字段桥接。
//   - 销售单/采购单可退读取：returns.SalesOrderReader/PurchaseOrderReader 携带 returns
//     域类型 ReturnableLine，域包之间禁止 import（判据 2），sales/purchase 无法直接
//     实现——按 plan §3.1 冻结签名（内建类型字段）在 router 侧桥接：只读 SELECT 经
//     域包导出的 GORM 模型（sales.SalesOrder/SalesOrderItem、purchase.PurchaseOrder/
//     PurchaseOrderItem），无写通路、无第二套数据源。
//   - 退货质检：purchase.QCCreatorService.CreateQC 为 plan §3.1 冻结实现方，签名含
//     operator 归因而 returns.QCCreator 冻结契约不携带操作者——按"系统级调用
//     OperatorID=0"口径记账（inventory.Actor 注释同口径），触发动作本身的审计由
//     returns 域业务事务内经 middleware.Audit 落 operation_logs。
//   - 追溯读：returns.LedgerReader/StockStateReader 由 inventory 只读查询
//     （QueryLedgers/QueryInventory/QuerySerials，plan §2.3 判据 7 无旁路 SELECT）桥接，
//     流水模型到 Trace 投影的转换全部经 inventory 导出模型字段。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/masterdata"
	"github.com/stockflow/server/internal/purchase"
	"github.com/stockflow/server/internal/returns"
	"github.com/stockflow/server/internal/sales"
	"github.com/stockflow/server/internal/stock"
	"github.com/stockflow/server/internal/stockops"
	"github.com/stockflow/server/internal/warehouse"
)

// m2QtyScale stock.Qty 冻结标度（numeric(18,4)，1 单位 Qty = 0.0001；stock/qty.go 同值）。
const m2QtyScale int64 = 10000

// ---- stock 值类型 → inventory 值类型 ----
//
// inventory 已按 backend-m2-plan §8.3 条 4 以类型别名承接 internal/stock
// （internal/inventory/aliases.go：type Qty = stock.Qty 等）——两侧类型恒等，
// 逐字段搬运桥接（M1 收编前形态）整体退役：原语 op/结果直接透传，字段漂移由
// 编译器在消费方接口断言处暴露（见文件尾编译期契约自检），不再存在"桥接层
// 静默吞字段"的窗口。

// ---- 库存原语网关桥接（purchase/sales/returns 各自的 StockGateway 消费接口）----

// purchaseStockGateway 实现 purchase.StockGateway（本域子集：Putaway/InspectResult/
// EnsureBatch/SerialEvent，plan §3.1 按域裁剪）。
type purchaseStockGateway struct{ svc *inventory.Service }

func (g purchaseStockGateway) Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	return g.svc.Putaway(ctx, tx, op)
}

func (g purchaseStockGateway) InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	return g.svc.InspectResult(ctx, tx, op)
}

func (g purchaseStockGateway) EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	return g.svc.EnsureBatch(ctx, tx, op)
}

func (g purchaseStockGateway) SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	return g.svc.SerialEvent(ctx, tx, op)
}

// salesStockGateway 实现 sales.StockGateway（本域子集：Lock/ReleaseLock/Deduct/SerialEvent；
// sales 域 op 类型为 stock 冻结形状的别名，sales/ports.go——转换与本桥接同构）。
type salesStockGateway struct{ svc *inventory.Service }

func (g salesStockGateway) Lock(ctx context.Context, tx *gorm.DB, op sales.LockOp) (sales.MutationResult, error) {
	return g.svc.Lock(ctx, tx, op)
}

func (g salesStockGateway) ReleaseLock(ctx context.Context, tx *gorm.DB, op sales.ReleaseLockOp) (sales.MutationResult, error) {
	return g.svc.ReleaseLock(ctx, tx, op)
}

func (g salesStockGateway) Deduct(ctx context.Context, tx *gorm.DB, op sales.DeductOp) (sales.MutationResult, error) {
	return g.svc.Deduct(ctx, tx, op)
}

func (g salesStockGateway) SerialEvent(ctx context.Context, tx *gorm.DB, op sales.SerialOp) (int64, bool, error) {
	return g.svc.SerialEvent(ctx, tx, op)
}

// returnsStockGateway 实现 returns.StockGateway（本域子集七原语，returns/ports.go）。
type returnsStockGateway struct{ svc *inventory.Service }

func (g returnsStockGateway) Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	return g.svc.Putaway(ctx, tx, op)
}

func (g returnsStockGateway) Deduct(ctx context.Context, tx *gorm.DB, op stock.DeductOp) (stock.MutationResult, error) {
	return g.svc.Deduct(ctx, tx, op)
}

func (g returnsStockGateway) Lock(ctx context.Context, tx *gorm.DB, op stock.LockOp) (stock.MutationResult, error) {
	return g.svc.Lock(ctx, tx, op)
}

func (g returnsStockGateway) ReleaseLock(ctx context.Context, tx *gorm.DB, op stock.ReleaseLockOp) (stock.MutationResult, error) {
	return g.svc.ReleaseLock(ctx, tx, op)
}

func (g returnsStockGateway) InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	return g.svc.InspectResult(ctx, tx, op)
}

func (g returnsStockGateway) EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	return g.svc.EnsureBatch(ctx, tx, op)
}

func (g returnsStockGateway) SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	return g.svc.SerialEvent(ctx, tx, op)
}

// SerialStates 实现 returns.StockGateway（M2 修复轮：采购退货出库序列号逐件校验
// 数据源；事务内读，inventory.Service.SerialStates）。
func (g returnsStockGateway) SerialStates(ctx context.Context, tx *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error) {
	return g.svc.SerialStates(ctx, tx, skuID, serials)
}

// stockops 网关无需桥接：其 StockGateway 消费接口以 stock 值类型定义
// （plan §3 冻结机制；inventory.Service 别名承接后类型恒等，结构化满足），router 直传。

// ---- SKU 开关桥接（masterdata.SKUFlags → 各域 Flags）----

// purchaseSKUFlagsBridge 实现 purchase.SKUAttrReader（GetFlags 带 found 位）。
type purchaseSKUFlagsBridge struct{ r *masterdata.SKUFlagService }

func (b purchaseSKUFlagsBridge) GetFlags(ctx context.Context, skuID int64) (purchase.SKUFlags, bool, error) {
	f, found, err := b.r.GetFlags(ctx, skuID)
	return purchase.SKUFlags{
		Enabled: f.Enabled, BatchManaged: f.BatchManaged,
		ExpiryManaged: f.ExpiryManaged, SerialManaged: f.SerialManaged,
	}, found, err
}

// salesSKUFlagsBridge 实现 sales.SKUAttrReader（签名无 found 位）：
// SKU 不存在返回全零 Flags（Enabled=false），由消费方以"SKU 未启用"拒绝——
// fail-closed 语义不变，不虚构开关状态。
type salesSKUFlagsBridge struct{ r *masterdata.SKUFlagService }

func (b salesSKUFlagsBridge) GetFlags(ctx context.Context, skuID int64) (sales.SKUFlags, error) {
	f, found, err := b.r.GetFlags(ctx, skuID)
	if err != nil {
		return sales.SKUFlags{}, err
	}
	if !found {
		return sales.SKUFlags{}, nil
	}
	return sales.SKUFlags{
		Enabled: f.Enabled, BatchManaged: f.BatchManaged,
		ExpiryManaged: f.ExpiryManaged, SerialManaged: f.SerialManaged,
	}, nil
}

// stockopsSKUFlagsBridge 实现 stockops.SKUFlagReader（缺省 fail-closed 同上）。
type stockopsSKUFlagsBridge struct{ r *masterdata.SKUFlagService }

func (b stockopsSKUFlagsBridge) GetFlags(ctx context.Context, skuID int64) (stockops.SKUFlags, error) {
	f, found, err := b.r.GetFlags(ctx, skuID)
	if err != nil {
		return stockops.SKUFlags{}, err
	}
	if !found {
		return stockops.SKUFlags{}, nil
	}
	return stockops.SKUFlags{
		Enabled: f.Enabled, BatchManaged: f.BatchManaged,
		ExpiryManaged: f.ExpiryManaged, SerialManaged: f.SerialManaged,
	}, nil
}

// returnsSKUFlagsBridge 实现 returns.SKUFlagReader（缺省 fail-closed 同上；
// M2 修复轮：采购退货出库序列号逐件核销的开关判定）。
type returnsSKUFlagsBridge struct{ r *masterdata.SKUFlagService }

func (b returnsSKUFlagsBridge) GetFlags(ctx context.Context, skuID int64) (returns.SKUFlags, error) {
	f, found, err := b.r.GetFlags(ctx, skuID)
	if err != nil {
		return returns.SKUFlags{}, err
	}
	if !found {
		return returns.SKUFlags{}, nil
	}
	return returns.SKUFlags{
		Enabled: f.Enabled, BatchManaged: f.BatchManaged,
		ExpiryManaged: f.ExpiryManaged, SerialManaged: f.SerialManaged,
	}, nil
}

// ---- 异常中心桥接（returns.Service.Create 为 plan §3.1 ExceptionCreator 导出实现）----
//
// purchase.ExceptionCreator 签名与 returns.Service.Create 逐字一致（内建类型），
// router 直接注入，无需适配器；sales.ExceptionCreator 携带域类型 detail，
// 以 JSON 文本（键与 returns.exceptionDetailPayload 对齐——sales.ExceptionDetail
// 的 json tag 即该键集）经闭包桥接。

// salesExceptionBridge 实现 sales.ExceptionCreator。
type salesExceptionBridge struct{ svc *returns.Service }

func (b salesExceptionBridge) Create(ctx context.Context, tx *gorm.DB, excType, sourceType, sourceNo string, detail sales.ExceptionDetail) (string, error) {
	payload, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("序列化异常定位信息失败: %w", err)
	}
	return b.svc.Create(ctx, tx, excType, sourceType, sourceNo, string(payload))
}

// ---- 退货质检桥接（purchase.QCCreatorService → returns.QCCreator）----

// qcCreatorBridge 实现 returns.QCCreator。operator 归因按系统级调用 OperatorID=0
// （触发动作的审计由 returns 域业务事务内经 middleware.Audit 落 operation_logs，
// 见文件头"退货质检"节）。warehouseID 由 returns 域在来源单据上守卫
// （purchase.QCCreatorService 契约注释同口径），桥接层不重复校验。
type qcCreatorBridge struct{ qc *purchase.QCCreatorService }

func (b qcCreatorBridge) CreateQC(ctx context.Context, sourceType, sourceNo, qcType string, warehouseID int64, lines []returns.QCLine) (string, error) {
	_ = warehouseID
	ls := make([]purchase.QCCreateLine, 0, len(lines))
	for _, l := range lines {
		ls = append(ls, purchase.QCCreateLine{
			LineNo: int(l.LineNo), SKUID: l.SKUID, QtyInspected: l.QtyInspected,
		})
	}
	return b.qc.CreateQC(ctx, 0, "", sourceType, sourceNo, qcType, ls)
}

// CompleteQC 实现 returns.QCCreator：退货全量质检完成时把质检单收尾（问题 5 修复——
// 原先只建单不回写，质检模块留 PENDING 僵尸单）。归因透传操作者，便于质检模块
// 审计显示是谁做的退货质检。
func (b qcCreatorBridge) CompleteQC(ctx context.Context, operatorID int64, operatorName, qcNo string, lines []returns.QCResultLine) error {
	ls := make([]purchase.QCResultLine, 0, len(lines))
	for _, l := range lines {
		ls = append(ls, purchase.QCResultLine{
			LineNo: l.LineNo, QtyQualified: l.QtyQualified, QtyDefective: l.QtyDefective,
		})
	}
	return b.qc.CompleteQC(ctx, operatorID, operatorName, qcNo, ls)
}

// ---- 来源单可退读取桥接（plan §3.1 SalesOrderReader/PurchaseOrderReader 行）----
//
// 退量口径：plan §3.1 冻结签名的行数量为 int64（整数件）；来源单 qty 列为
// numeric(18,4)（stock.Qty，×10000 标度）。桥接按下取整折为整数件——小数件来源量
// 在冻结 int64 契约边界按保守口径收缩（只会少退不会多退，防超量方向安全）；
// returns 侧以 stock.ParseQty(fmt.Sprint(qty)) 还原为 numeric(18,4) 比对。

// qtyToUnits numeric(18,4) 标度量折为整数件（下取整，防超量保守口径）。
func qtyToUnits(q stock.Qty) int64 { return int64(q) / m2QtyScale }

// salesReturnOrderReader 实现 returns.SalesOrderReader（只读 sales 自有表经其导出模型）。
// found=false = 单号不存在或无任何已发货行（取消单发货量为 0，天然不可退）。
type salesReturnOrderReader struct{ db *gorm.DB }

func (r salesReturnOrderReader) FindReturnable(ctx context.Context, soNo string) (int64, int64, []returns.ReturnableLine, bool, error) {
	var so sales.SalesOrder
	err := r.db.WithContext(ctx).Where("so_no = ?", strings.TrimSpace(soNo)).Take(&so).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, nil, false, nil
	}
	if err != nil {
		return 0, 0, nil, false, fmt.Errorf("查询销售单 %s 失败: %w", soNo, err)
	}
	var items []sales.SalesOrderItem
	if err := r.db.WithContext(ctx).Where("so_id = ?", so.ID.Int64()).
		Order("line_no ASC").Find(&items).Error; err != nil {
		return 0, 0, nil, false, fmt.Errorf("查询销售单 %s 明细失败: %w", soNo, err)
	}
	lines := make([]returns.ReturnableLine, 0, len(items))
	for _, it := range items {
		if !it.QtyShipped.IsPositive() {
			continue
		}
		lines = append(lines, returns.ReturnableLine{
			LineNo: it.LineNo, SKUID: it.SKUID, Qty: qtyToUnits(it.QtyShipped),
		})
	}
	if len(lines) == 0 {
		return 0, 0, nil, false, nil // 无已发货行 = 不可退（契约：QtyShipped>0 才可退）
	}
	return so.ID.Int64(), so.WarehouseID, lines, true, nil
}

// purchaseReturnOrderReader 实现 returns.PurchaseOrderReader（只读 purchase 自有表；
// PurchaseOrder 挂 BaseModel 软删，GORM 作用域自动排除已删单）。
type purchaseReturnOrderReader struct{ db *gorm.DB }

func (r purchaseReturnOrderReader) FindReturnable(ctx context.Context, poNo string) (int64, int64, []returns.ReturnableLine, bool, error) {
	var po purchase.PurchaseOrder
	err := r.db.WithContext(ctx).Where("po_no = ?", strings.TrimSpace(poNo)).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, nil, false, nil
	}
	if err != nil {
		return 0, 0, nil, false, fmt.Errorf("查询采购单 %s 失败: %w", poNo, err)
	}
	var items []purchase.PurchaseOrderItem
	if err := r.db.WithContext(ctx).Where("po_id = ?", po.ID.Int64()).
		Order("line_no ASC").Find(&items).Error; err != nil {
		return 0, 0, nil, false, fmt.Errorf("查询采购单 %s 明细失败: %w", poNo, err)
	}
	lines := make([]returns.ReturnableLine, 0, len(items))
	for _, it := range items {
		if !it.QtyReceived.IsPositive() {
			continue
		}
		lines = append(lines, returns.ReturnableLine{
			LineNo: int64(it.LineNo), SKUID: it.SKUID, Qty: qtyToUnits(it.QtyReceived),
		})
	}
	if len(lines) == 0 {
		return 0, 0, nil, false, nil // 无已收货行 = 不可退（契约：QtyReceived>0 才可退）
	}
	return po.ID.Int64(), po.WarehouseID, lines, true, nil
}

// ---- 追溯读桥接（inventory 只读查询 → returns.LedgerReader/StockStateReader）----

// traceReaders 实现 returns.LedgerReader 与 returns.StockStateReader（inventory-rules §10：
// 追溯数据源 = 库存流水 + 库存/序列号台账；经 inventory 导出查询编排，不建追溯中间表）。
// 数据权限：returns.Trace 服务已按 auth.WarehouseScope 收窄并只放行确定仓库（或 ALL 全量），
// 本桥接按其判定的 warehouseID 过滤（0 = 已确认 ALL 范围），不在桥内二次裁决。
type traceReaders struct{ svc *inventory.Service }

// LedgersForTrace 实现 returns.LedgerReader：最新 limit 条流水，按时间正序返回。
func (t traceReaders) LedgersForTrace(ctx context.Context, skuID int64, serialNo string, warehouseID int64, limit int) ([]returns.TraceLedger, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, _, err := t.svc.QueryLedgers(ctx, inventory.LedgerQuery{
		Scope:       inventory.Scope{AllWarehouses: true},
		WarehouseID: warehouseID, SKUID: skuID, SerialNo: serialNo,
		Page: 1, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("追溯查询 SKU %d 流水失败: %w", skuID, err)
	}
	out := make([]returns.TraceLedger, 0, len(rows))
	for _, l := range rows {
		out = append(out, returns.TraceLedger{
			ID: l.ID.Int64(), LedgerNo: l.LedgerNo, SKUID: l.SKUID,
			WarehouseID: l.WarehouseID, BinID: l.BinID, BatchID: l.BatchID,
			SerialNo: l.SerialNo, ChangeType: l.ChangeType,
			BusinessType: l.BusinessType, BusinessNo: l.BusinessNo,
			StatusFrom: l.StatusFrom, StatusTo: l.StatusTo,
			QtyBefore: l.QtyBefore.String(), QtyChange: l.QtyChange.String(), QtyAfter: l.QtyAfter.String(),
			OperatorName: l.OperatorName, RequestID: l.RequestID, Remark: l.Remark,
			CreatedAt: l.CreatedAt.Time,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// StockRowsBySKU 实现 returns.StockStateReader：当前库存行（六状态 numeric(18,4) 文本）。
func (t traceReaders) StockRowsBySKU(ctx context.Context, skuID, warehouseID int64, limit int) ([]returns.TraceStockRow, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, _, err := t.svc.QueryInventory(ctx, inventory.InventoryQuery{
		Scope:       inventory.Scope{AllWarehouses: true},
		WarehouseID: warehouseID, SKUID: skuID,
		Page: 1, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("追溯查询 SKU %d 库存行失败: %w", skuID, err)
	}
	out := make([]returns.TraceStockRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, returns.TraceStockRow{
			WarehouseID: r.WarehouseID, ZoneID: r.ZoneID, ShelfID: r.ShelfID, BinID: r.BinID,
			SKUID: r.SKUID, BatchID: r.BatchID,
			Total: r.TotalQty.String(), Available: r.AvailableQty.String(),
			Locked: r.LockedQty.String(), Frozen: r.FrozenQty.String(),
			PendingInspect: r.PendingInspectQty.String(), Defective: r.DefectiveQty.String(),
			UpdatedAt: r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// SerialByNo 实现 returns.StockStateReader：序列号当前台账（found=false 不存在）。
func (t traceReaders) SerialByNo(ctx context.Context, serialNo string) (returns.TraceSerialRow, bool, error) {
	rows, _, err := t.svc.QuerySerials(ctx, inventory.SerialQuery{
		Scope: inventory.Scope{AllWarehouses: true}, SerialNo: strings.TrimSpace(serialNo),
		Page: 1, PageSize: 1,
	})
	if err != nil {
		return returns.TraceSerialRow{}, false, fmt.Errorf("追溯查询序列号 %s 失败: %w", serialNo, err)
	}
	if len(rows) == 0 {
		return returns.TraceSerialRow{}, false, nil
	}
	r := rows[0]
	return returns.TraceSerialRow{
		SerialNo: r.SerialNo, SKUID: r.SKUID, BatchID: r.BatchID,
		WarehouseID: r.WarehouseID, BinID: r.BinID, Status: r.Status,
		LastSourceType: r.LastSourceType, LastSourceNo: r.LastSourceNo,
		LastEventAt: r.LastEventAt.Time,
	}, true, nil
}

// ---- 推荐库位桥接（warehouse.BinRecommenderService → purchase.BinRecommender）----
//
// business-flow §5.3 基础规则（同 SKU 集中 + 剩余容量 + 库位启用）的实现数据面在
// warehouse 域：库位静态属性直读本域 bins 表，库存动态占用经 BinOccupancyReader/
// SKUBinReader 窄接口由 inventory 提供；域类型 warehouse.BinSuggestion 逐字段桥接为
// purchase.BinSuggestion（判据 2 域包禁 import）。2026-10-04 补齐装配——此前
// BinRecommender 未注入，GET /api/putaway/recommend 运行期 fail-closed。

// warehouseBinRecommenderBridge 实现 purchase.BinRecommender。
type warehouseBinRecommenderBridge struct {
	r *warehouse.BinRecommenderService
}

// Recommend 实现 purchase.BinRecommender（逐字段搬运，评分/理由语义由 warehouse 侧定义）。
func (b warehouseBinRecommenderBridge) Recommend(ctx context.Context, warehouseID, skuID int64, qty float64) ([]purchase.BinSuggestion, error) {
	sugs, err := b.r.Recommend(ctx, warehouseID, skuID, qty)
	if err != nil {
		return nil, err
	}
	out := make([]purchase.BinSuggestion, 0, len(sugs))
	for _, s := range sugs {
		out = append(out, purchase.BinSuggestion{
			BinID: s.BinID, ZoneID: s.ZoneID, ShelfID: s.ShelfID,
			Score: s.Score, Reason: s.Reason,
		})
	}
	return out, nil
}

// ---- 编译期契约自检：桥接实现必须与消费方冻结接口逐字匹配 ----

var (
	_ purchase.StockGateway       = purchaseStockGateway{}
	_ sales.StockGateway          = salesStockGateway{}
	_ returns.StockGateway        = returnsStockGateway{}
	_ purchase.SKUAttrReader      = purchaseSKUFlagsBridge{}
	_ sales.SKUAttrReader         = salesSKUFlagsBridge{}
	_ stockops.SKUFlagReader      = stockopsSKUFlagsBridge{}
	_ returns.SKUFlagReader       = returnsSKUFlagsBridge{}
	_ sales.ExceptionCreator      = salesExceptionBridge{}
	_ returns.QCCreator           = qcCreatorBridge{}
	_ returns.SalesOrderReader    = salesReturnOrderReader{}
	_ returns.PurchaseOrderReader = purchaseReturnOrderReader{}
	_ returns.LedgerReader        = traceReaders{}
	_ returns.StockStateReader    = traceReaders{}
	_ purchase.BinRecommender     = warehouseBinRecommenderBridge{}
	_ warehouse.SKUBinReader      = inventory.NewBinOccupancy(nil)
)
