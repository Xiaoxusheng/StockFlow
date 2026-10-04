package purchase

// purchase 域 datax 接入——导入写入器（backend-m3-plan §2.2 冻结新增文件；§6.1
// PURCHASE_ORDER 导入）。写路径经本域既有 CreatePO 通路（§2.2 规则①禁止旁路 SQL），
// 落 DRAFT（§6.1：导入单据落草稿，提交/审核走既有审批流）。
//
// 分组语义：模板每行为一条订单明细，连续且（供应商编码, 仓库编码）相同的行归并为
// 一张订单，行号由 CreatePO 通路连续编号（§6.1"行号连续"由既有通路保证）。
// 行级结果：某张订单创建失败 → 该组全部行标记失败并携带同一原因（excel §6.2 行级
// 失败明细）。
//
// 跨域依赖（plan §12.2 窄接口机制）：供应商/仓库/SKU 编码解析经消费方接口定义、
// router 桥接注入（masterdata.DataxCodeResolver / warehouse.DataxWarehouseResolver
// 承载实现），本域不得直查他域表。

import (
	"context"
	"strings"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// —— 跨域编码解析接口（消费方定义，router 注入实现）——

// CodeResolver 基础资料编码解析（masterdata.DataxCodeResolver 结构化满足）。
type CodeResolver interface {
	ResolveSupplierCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
	ResolveSKUCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
}

// WarehouseResolver 仓库编码解析（warehouse.DataxWarehouseResolver 结构化满足）。
type WarehouseResolver interface {
	ResolveWarehouseCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
}

// POImportDeps 导入写入器跨域依赖（router 装配注入；nil 即 fail-closed 拒绝执行）。
type POImportDeps struct {
	Codes      CodeResolver
	Warehouses WarehouseResolver
}

// POImportWriter 采购订单导入写入器（plan §6.1：供应商存在、SKU 存在启用、数量>0、
// 单价≥0、行号连续）。
type POImportWriter struct {
	svc  *Service
	deps POImportDeps
}

// NewPOImportWriter 构建采购订单导入写入器（router 装配）。
func NewPOImportWriter(svc *Service, deps POImportDeps) datax.ImportWriter {
	return &POImportWriter{svc: svc, deps: deps}
}

// Template 模板规格。
func (w *POImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportPurchaseOrder,
		Name:        "采购订单导入",
		FileName:    "采购订单导入模板.xlsx",
		Description: "批量导入采购订单（落草稿）；连续且供应商+仓库相同的行归并为一张订单。",
		Sheet:       "采购订单导入",
		Columns: []datax.Column{
			{Key: "supplier_code", Title: "供应商编码", Required: true, Example: "S-0001", Note: "须为已启用供应商", Width: 14},
			{Key: "warehouse_code", Title: "仓库编码", Required: true, Example: "WH-A", Note: "须为已启用仓库", Width: 12},
			{Key: "sku_code", Title: "SKU编码", Required: true, Example: "SKU-0001", Note: "须为已启用 SKU", Width: 14},
			{Key: "qty", Title: "数量", Required: true, Type: datax.CellNumber, Example: "100", Note: "> 0，最多 4 位小数", Width: 10},
			{Key: "price", Title: "单价", Type: datax.CellMoney, Example: "10.50", Note: ">= 0", Width: 10},
			{Key: "remark", Title: "备注", Width: 20},
		},
		SampleRows: [][]string{
			{"S-0001", "WH-A", "SKU-0001", "100", "10.50", "示例行，导入前请删除"},
			{"S-0001", "WH-A", "SKU-0002", "50", "20.00", "与上行同供应商同仓库，归并为一张订单"},
		},
		Notes: []string{
			"供应商/仓库/SKU 编码必须存在且启用",
			"数量必须 > 0；单价 >= 0（可空=0）",
			"相邻行供应商编码与仓库编码均相同则归并为一张订单，否则开启新订单",
			"导入的订单全部为草稿状态，提交与审核走采购订单既有流程",
		},
	}
}

// poGroup 连续行归组（同供应商+同仓库 = 一张订单）。
type poGroup struct {
	supplierID, warehouseID int64
	firstRow, lastRow       int
	items                   []POItemInput
}

// Validate 业务层校验（逐行存在性/启用/数量/价格）。
func (w *POImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		if err := w.validateRow(ctx, r); err != nil {
			return nil, err
		}
		if e := w.checkRow(ctx, r); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs, nil
}

func (w *POImportWriter) depsReady() error {
	if w.deps.Codes == nil || w.deps.Warehouses == nil {
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "采购订单导入跨域解析器未注入（router 装配 NewPOImportWriter deps）",
		})
	}
	return nil
}

// validateRow 依赖缺位检查（fail-closed）。
func (w *POImportWriter) validateRow(ctx context.Context, r datax.ImportRow) error {
	return w.depsReady()
}

// checkRow 单行业务校验。
func (w *POImportWriter) checkRow(ctx context.Context, r datax.ImportRow) *datax.RowError {
	if sup := text(r, "supplier_code"); sup != "" {
		_, enabled, found, err := w.deps.Codes.ResolveSupplierCode(ctx, sup)
		if err != nil {
			return rowErrP(r.RowNo, "supplier_code", "供应商校验失败: "+err.Error())
		}
		if !found {
			return rowErrP(r.RowNo, "supplier_code", "供应商编码不存在")
		}
		if !enabled {
			return rowErrP(r.RowNo, "supplier_code", "供应商已停用")
		}
	}
	if wh := text(r, "warehouse_code"); wh != "" {
		_, enabled, found, err := w.deps.Warehouses.ResolveWarehouseCode(ctx, wh)
		if err != nil {
			return rowErrP(r.RowNo, "warehouse_code", "仓库校验失败: "+err.Error())
		}
		if !found {
			return rowErrP(r.RowNo, "warehouse_code", "仓库编码不存在")
		}
		if !enabled {
			return rowErrP(r.RowNo, "warehouse_code", "仓库已停用")
		}
	}
	if sku := text(r, "sku_code"); sku != "" {
		_, enabled, found, err := w.deps.Codes.ResolveSKUCode(ctx, sku)
		if err != nil {
			return rowErrP(r.RowNo, "sku_code", "SKU 校验失败: "+err.Error())
		}
		if !found {
			return rowErrP(r.RowNo, "sku_code", "SKU 编码不存在")
		}
		if !enabled {
			return rowErrP(r.RowNo, "sku_code", "SKU 已停用")
		}
	}
	if q := text(r, "qty"); q != "" {
		qty, err := stock.ParseQty(q)
		if err != nil || !qty.IsPositive() {
			return rowErrP(r.RowNo, "qty", "数量必须大于 0")
		}
	}
	if p := text(r, "price"); p != "" {
		price, err := stock.ParseQty(p)
		if err != nil || price.IsNegative() {
			return rowErrP(r.RowNo, "price", "单价必须 >= 0")
		}
	}
	return nil
}

// Commit 按连续分组逐单经既有 CreatePO 通路写入（行级失败不中断批次）。
func (w *POImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	if err := w.depsReady(); err != nil {
		return res, err
	}
	rowErrs := make(map[int]datax.RowError)
	var current *poGroup
	flush := func() {
		if current == nil {
			return
		}
		if _, err := w.svc.CreatePO(ctx, purchaseActor(actor), POCreateInput{
			SupplierID:  current.supplierID,
			WarehouseID: current.warehouseID,
			Items:       current.items,
		}); err != nil {
			msg := serviceErrMsg(err)
			for row := current.firstRow; row <= current.lastRow; row++ {
				rowErrs[row] = datax.RowError{Row: row, Column: "supplier_code", Message: msg}
			}
		}
		current = nil
	}
	for _, r := range rows {
		supID, supEnabled, supFound, err := w.deps.Codes.ResolveSupplierCode(ctx, text(r, "supplier_code"))
		if err != nil {
			return res, err
		}
		whID, _, whFound, err := w.deps.Warehouses.ResolveWarehouseCode(ctx, text(r, "warehouse_code"))
		if err != nil {
			return res, err
		}
		skuID, _, skuFound, err := w.deps.Codes.ResolveSKUCode(ctx, text(r, "sku_code"))
		if err != nil {
			return res, err
		}
		// 提交期解析失败/被并发停用：行级失败（与校验期同语义）。
		if !supFound || !supEnabled || !whFound || !skuFound {
			rowErrs[r.RowNo] = datax.RowError{Row: r.RowNo, Column: "supplier_code", Message: "供应商/仓库/SKU 已不存在或停用"}
			current = nil
			continue
		}
		if current == nil || current.supplierID != supID || current.warehouseID != whID {
			flush()
			current = &poGroup{supplierID: supID, warehouseID: whID, firstRow: r.RowNo, lastRow: r.RowNo}
		}
		current.lastRow = r.RowNo
		qty, _ := stock.ParseQty(text(r, "qty"))
		price, _ := stock.ParseQty(text(r, "price"))
		current.items = append(current.items, POItemInput{SKUID: skuID, Qty: qty, Price: price, Remark: text(r, "remark")})
	}
	flush()

	for _, r := range rows {
		if e, bad := rowErrs[r.RowNo]; bad {
			res.FailedRows++
			res.Errors = append(res.Errors, e)
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// purchaseActor datax.Actor → 本域 Actor。
func purchaseActor(a datax.Actor) Actor {
	return Actor{
		UserID: a.UserID, Username: a.Username,
		RequestID: a.RequestID, IP: a.IP, UserAgent: a.UserAgent,
		Method: a.Method, Path: a.Path,
	}
}

// text 读文本列。
func text(r datax.ImportRow, key string) string { return strings.TrimSpace(r.Raw[key]) }

// rowErrP 行错误指针快捷构造。
func rowErrP(rowNo int, key, msg string) *datax.RowError {
	e := datax.RowError{Row: rowNo, Column: key, Message: msg}
	return &e
}

// serviceErrMsg 领域错误 → 用户可读消息。
func serviceErrMsg(err error) string {
	if re, ok := err.(*response.Error); ok {
		return re.Message()
	}
	return err.Error()
}
