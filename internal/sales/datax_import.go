package sales

// sales 域 datax 接入（backend-m3-plan §2.2 冻结新增文件；§6.1）：
//   - SALES_ORDER 导入写入器：写路径经本域既有 CreateSalesOrder 通路落 DRAFT
//     （§2.2 规则①禁止旁路 SQL；连续且（客户编码, 仓库编码）相同的行归并为一单）；
//   - DataxCodeResolver 声明的消费接口由 router 桥接 masterdata/warehouse 实现
//     （plan §12.2 窄接口机制，本域不得直查他域表）。

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// —— 跨域编码解析接口（消费方定义，router 注入实现）——

// CodeResolver 基础资料编码解析（masterdata.DataxCodeResolver 经 router 闭包桥接）。
type CodeResolver interface {
	ResolveCustomerCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
	ResolveSKUCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
}

// WarehouseResolver 仓库编码解析（warehouse.DataxWarehouseResolver 经 router 闭包桥接）。
type WarehouseResolver interface {
	ResolveWarehouseCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
}

// SOImportDeps 导入写入器跨域依赖（router 装配注入；nil 即 fail-closed 拒绝执行）。
type SOImportDeps struct {
	Codes      CodeResolver
	Warehouses WarehouseResolver
}

// SOImportWriter 销售订单导入写入器（plan §6.1：客户存在、SKU 存在启用、数量/价格合法）。
type SOImportWriter struct {
	svc  *Service
	deps SOImportDeps
}

// NewSOImportWriter 构建销售订单导入写入器（router 装配）。
func NewSOImportWriter(svc *Service, deps SOImportDeps) datax.ImportWriter {
	return &SOImportWriter{svc: svc, deps: deps}
}

// Template 模板规格。
func (w *SOImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportSalesOrder,
		Name:        "销售订单导入",
		FileName:    "销售订单导入模板.xlsx",
		Description: "批量导入销售订单（落草稿）；连续且客户+仓库相同的行归并为一张订单。",
		Sheet:       "销售订单导入",
		Columns: []datax.Column{
			{Key: "customer_code", Title: "客户编码", Required: true, Example: "C-0001", Note: "须为已启用客户", Width: 14},
			{Key: "warehouse_code", Title: "仓库编码", Required: true, Example: "WH-A", Note: "须为已启用仓库", Width: 12},
			{Key: "sku_code", Title: "SKU编码", Required: true, Example: "SKU-0001", Note: "须为已启用 SKU", Width: 14},
			{Key: "qty", Title: "数量", Required: true, Type: datax.CellNumber, Example: "20", Note: "> 0，最多 4 位小数", Width: 10},
			{Key: "price", Title: "单价", Type: datax.CellMoney, Example: "19.90", Note: ">= 0", Width: 10},
			{Key: "shipping_address", Title: "收货地址", Width: 26},
			{Key: "remark", Title: "备注", Width: 20},
		},
		SampleRows: [][]string{
			{"C-0001", "WH-A", "SKU-0001", "20", "19.90", "示例市收货路 2 号", "示例行，导入前请删除"},
		},
		Notes: []string{
			"客户/仓库/SKU 编码必须存在且启用",
			"数量必须 > 0；单价 >= 0（可空=0）",
			"相邻行客户编码与仓库编码均相同则归并为一张订单，否则开启新订单",
			"导入的订单全部为草稿状态，提交与审核走销售订单既有流程",
		},
	}
}

func (w *SOImportWriter) depsReady() error {
	if w.deps.Codes == nil || w.deps.Warehouses == nil {
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "销售订单导入跨域解析器未注入（router 装配 NewSOImportWriter deps）",
		})
	}
	return nil
}

// Validate 业务层校验（逐行存在性/启用/数量/价格）。
func (w *SOImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	if err := w.depsReady(); err != nil {
		return nil, err
	}
	var errs []datax.RowError
	for _, r := range rows {
		if e := w.checkRow(ctx, r); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs, nil
}

// checkRow 单行业务校验。
func (w *SOImportWriter) checkRow(ctx context.Context, r datax.ImportRow) *datax.RowError {
	if cus := cellRaw(r, "customer_code"); cus != "" {
		_, enabled, found, err := w.deps.Codes.ResolveCustomerCode(ctx, cus)
		if err != nil {
			return rowErrP(r.RowNo, "customer_code", "客户校验失败: "+err.Error())
		}
		if !found {
			return rowErrP(r.RowNo, "customer_code", "客户编码不存在")
		}
		if !enabled {
			return rowErrP(r.RowNo, "customer_code", "客户已停用")
		}
	}
	if wh := cellRaw(r, "warehouse_code"); wh != "" {
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
	if sku := cellRaw(r, "sku_code"); sku != "" {
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
	if q := cellRaw(r, "qty"); q != "" {
		qty, err := stock.ParseQty(q)
		if err != nil || !qty.IsPositive() {
			return rowErrP(r.RowNo, "qty", "数量必须大于 0")
		}
	}
	if p := cellRaw(r, "price"); p != "" {
		price, err := stock.ParseQty(p)
		if err != nil || price.IsNegative() {
			return rowErrP(r.RowNo, "price", "单价必须 >= 0")
		}
	}
	return nil
}

// soGroup 连续行归组。
type soGroup struct {
	customerID, warehouseID int64
	shippingAddress         string
	firstRow, lastRow       int
	items                   []OrderItemInput
}

// Commit 按连续分组逐单经既有 CreateSalesOrder 通路写入。
func (w *SOImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	if err := w.depsReady(); err != nil {
		return res, err
	}
	rowErrs := make(map[int]datax.RowError)
	var current *soGroup
	flush := func() {
		if current == nil {
			return
		}
		if _, err := w.svc.CreateSalesOrder(ctx, salesActor(actor), CreateOrderInput{
			CustomerID:      current.customerID,
			WarehouseID:     current.warehouseID,
			ShippingAddress: current.shippingAddress,
			Items:           current.items,
		}); err != nil {
			msg := serviceErrMsg(err)
			for row := current.firstRow; row <= current.lastRow; row++ {
				rowErrs[row] = datax.RowError{Row: row, Column: "customer_code", Message: msg}
			}
		}
		current = nil
	}
	for _, r := range rows {
		cusID, cusEnabled, cusFound, err := w.deps.Codes.ResolveCustomerCode(ctx, cellRaw(r, "customer_code"))
		if err != nil {
			return res, err
		}
		whID, _, whFound, err := w.deps.Warehouses.ResolveWarehouseCode(ctx, cellRaw(r, "warehouse_code"))
		if err != nil {
			return res, err
		}
		skuID, _, skuFound, err := w.deps.Codes.ResolveSKUCode(ctx, cellRaw(r, "sku_code"))
		if err != nil {
			return res, err
		}
		if !cusFound || !cusEnabled || !whFound || !skuFound {
			rowErrs[r.RowNo] = datax.RowError{Row: r.RowNo, Column: "customer_code", Message: "客户/仓库/SKU 已不存在或停用"}
			current = nil
			continue
		}
		if current == nil || current.customerID != cusID || current.warehouseID != whID {
			flush()
			current = &soGroup{customerID: cusID, warehouseID: whID,
				shippingAddress: cellRaw(r, "shipping_address"), firstRow: r.RowNo, lastRow: r.RowNo}
		}
		current.lastRow = r.RowNo
		qty, _ := stock.ParseQty(cellRaw(r, "qty"))
		price, _ := stock.ParseQty(cellRaw(r, "price"))
		current.items = append(current.items, OrderItemInput{SKUID: skuID, Qty: qty, Price: price, Remark: cellRaw(r, "remark")})
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

// salesActor datax.Actor → 本域 Actor。
func salesActor(a datax.Actor) Actor {
	return Actor{
		ID: a.UserID, Name: a.Username,
		RequestID: a.RequestID, IP: a.IP, UserAgent: a.UserAgent,
		Method: a.Method, Path: a.Path,
	}
}

// cellRaw 读原始文本列（numeric(18,4) 十进制文本直传，禁 float 转换损耗）。
func cellRaw(r datax.ImportRow, key string) string { return strings.TrimSpace(r.Raw[key]) }

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

// NewSOImportService 构建销售订单导入写入器的承载 Service（router 装配注入跨域校验器）。
//
// MT6 集成最小改动（backend-m3-plan §2.2 规则③一次性最小改动清单，唯一执行人=集成工程师）：
// 本域 repo 构造器（newGormRepository）未导出，路由装配无法按 RegisterRoutes 同款依赖
// 构造 *Service，SALES_ORDER 导入写入器不可接线——按本导出构造器修复（纯新增，零既有改动）。
// 依赖与 sales.RegisterRoutes 完全同源（CreateSalesOrder 的 depsReady 要求四依赖齐备），
// 导入创建通路与手工创建同一校验面（plan §6.1 写路径红线）。
func NewSOImportService(db *gorm.DB, stock StockGateway, skus SKUAttrReader, customers CustomerChecker, bins BinChecker) *Service {
	return NewService(newGormRepository(db),
		WithStock(stock), WithSKUAttr(skus), WithCustomerChecker(customers), WithBinChecker(bins))
}
