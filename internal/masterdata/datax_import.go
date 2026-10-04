package masterdata

// masterdata 域 datax 接入（backend-m3-plan §2.2 冻结新增文件；§6.1 写入器）：
//   - 四个导入写入器：PRODUCT / SKU / SUPPLIER / CUSTOMER（写路径经本域既有 Service
//     通路 CreateProduct/CreateSKU/CreateSupplier/CreateCustomer——§2.2 规则①，
//     禁止旁路 SQL；"存在即更新"不适用：§6.1 明确"编码与库内重复"为校验错误行）；
//   - DataxCodeResolver：编码 → ID/启用的只读解析器（purchase/sales/inventory 各域
//     datax_import.go 的跨域依赖，router 桥接注入——plan §12.2 窄接口机制，
//     只读本域表；实现落本冻结文件）。
//
// Validate 只读（存在性/库内重复），Commit 逐行调用既有 Service 方法（行级失败返回
// RowError，不中断批次——excel §6.2；行级审计由域内 Service 同事务写入）。

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/response"
)

// DataxCodeResolver 基础资料编码解析器（只读）。
type DataxCodeResolver struct {
	repo Repository
}

// NewDataxCodeResolver 构建编码解析器（router 装配注入各域 datax_import.go）。
func NewDataxCodeResolver(db *gorm.DB) *DataxCodeResolver {
	return &DataxCodeResolver{repo: NewRepository(db)}
}

// CodeRef 编码解析结果（内建类型，跨域契约不带域类型）。
type CodeRef struct {
	ID      int64
	Enabled bool
	Found   bool
}

// ResolveProductCode 商品编码 → ID。
func (r *DataxCodeResolver) ResolveProductCode(ctx context.Context, code string) (CodeRef, error) {
	p, err := r.repo.FindProductByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if p == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: p.ID.Int64(), Enabled: p.Status == StatusEnabled, Found: true}, nil
}

// ResolveSKUCode SKU 编码 → ID。
func (r *DataxCodeResolver) ResolveSKUCode(ctx context.Context, code string) (CodeRef, error) {
	s, err := r.repo.FindSKUByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if s == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: s.ID.Int64(), Enabled: s.IsEnabled, Found: true}, nil
}

// ResolveSupplierCode 供应商编码 → ID。
func (r *DataxCodeResolver) ResolveSupplierCode(ctx context.Context, code string) (CodeRef, error) {
	s, err := r.repo.FindSupplierByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if s == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: s.ID.Int64(), Enabled: s.Status == StatusEnabled, Found: true}, nil
}

// ResolveCustomerCode 客户编码 → ID。
func (r *DataxCodeResolver) ResolveCustomerCode(ctx context.Context, code string) (CodeRef, error) {
	c, err := r.repo.FindCustomerByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if c == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: c.ID.Int64(), Enabled: c.Status == StatusEnabled, Found: true}, nil
}

// ---- 通用工具（本文件四个写入器共享）----

// cellText 读文本列。
func cellText(r datax.ImportRow, key string) string {
	return strings.TrimSpace(r.Cells[key].S)
}

// cellNumber 读数值列（float 形态，供比较/累计）。
func cellNumber(r datax.ImportRow, key string) float64 {
	return r.Cells[key].N
}

// cellNumberText 数值列原始文本（numeric(18,4) 载体禁 float——Number/Qty 一律以
// 十进制文本承载，精度无损，masterdata.Number 契约）。
func cellNumberText(r datax.ImportRow, key string) string {
	return strings.TrimSpace(r.Raw[key])
}

// boolText 是/否布尔列解析（模板说明统一口径；空值返回缺省）。
func boolText(v string, def bool) (bool, bool) {
	switch v {
	case "是", "Y", "y", "true", "TRUE", "1":
		return true, true
	case "否", "N", "n", "false", "FALSE", "0":
		return false, true
	case "":
		return def, true
	}
	return def, false
}

// rowErr 行错误快捷构造。
func rowErr(rowNo int, key, msg string) datax.RowError {
	return datax.RowError{Row: rowNo, Column: key, Message: msg}
}

// serviceErrMsg 领域错误 → 用户可读消息（api.md §4：校验失败信息可直接展示）。
func serviceErrMsg(err error) string {
	if re, ok := err.(*response.Error); ok {
		return re.Message()
	}
	return err.Error()
}

// masterdataActor datax.Actor → 本域 Actor（字段一一对应；IsSuper 不经导入放权，
// 恒 false——RBAC 在路由层已判定）。
func masterdataActor(a datax.Actor) Actor {
	return Actor{
		UserID: a.UserID, Username: a.Username, IsSuper: false,
		RequestID: a.RequestID, IP: a.IP, UserAgent: a.UserAgent,
		Method: a.Method, Path: a.Path,
	}
}

// ---- 商品导入（PRODUCT）----

// ProductImportWriter 商品导入写入器（plan §6.1：编码与库内重复、分类/单位存在、名称必填）。
type ProductImportWriter struct {
	svc *Service
}

// NewProductImportWriter 构建商品导入写入器（router 装配）。
func NewProductImportWriter(svc *Service) datax.ImportWriter { return &ProductImportWriter{svc: svc} }

// Template 模板规格。
func (w *ProductImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportProduct,
		Name:        "商品导入",
		FileName:    "商品导入模板.xlsx",
		Description: "批量导入商品基础资料；编码已存在视为错误行（不做覆盖更新）。",
		Sheet:       "商品导入",
		Columns: []datax.Column{
			{Key: "code", Title: "商品编码", Required: true, UniqueInFile: true, Example: "P-0001", Note: "全局唯一，创建后不可修改", Width: 14},
			{Key: "name", Title: "商品名称", Required: true, Example: "示例商品", Width: 20},
			{Key: "short_name", Title: "商品简称", Example: "示例", Width: 14},
			{Key: "category_code", Title: "分类编码", Example: "CAT-01", Note: "须为已启用的商品分类编码", Width: 12},
			{Key: "brand", Title: "品牌", Width: 12},
			{Key: "model", Title: "型号", Width: 14},
			{Key: "spec", Title: "规格", Width: 14},
			{Key: "unit_code", Title: "单位编码", Example: "U-PCS", Note: "须为已启用的计量单位编码", Width: 12},
			{Key: "weight", Title: "重量(kg)", Type: datax.CellNumber, Width: 10},
			{Key: "length", Title: "长(cm)", Type: datax.CellNumber, Width: 10},
			{Key: "width", Title: "宽(cm)", Type: datax.CellNumber, Width: 10},
			{Key: "height", Title: "高(cm)", Type: datax.CellNumber, Width: 10},
			{Key: "volume", Title: "体积(L)", Type: datax.CellNumber, Width: 10},
			{Key: "remark", Title: "备注", Width: 20},
		},
		SampleRows: [][]string{
			{"P-0001", "示例商品", "示例", "CAT-01", "示例品牌", "M-100", "100ml", "U-PCS", "0.25", "10", "8", "18", "0.5", "示例行，导入前请删除"},
		},
		Notes: []string{
			"商品编码为必填项且全文件内不得重复、不得与系统已有编码重复",
			"分类编码/单位编码如填写，必须为系统已启用分类/单位的编码",
			"重量/长/宽/高/体积为数字列，须 >= 0",
			"商品创建后默认为启用状态；下线请使用商品启停功能",
		},
	}
}

// Validate 业务层校验（分类/单位存在 + 启用；编码与库内重复）。
func (w *ProductImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		if cat := cellText(r, "category_code"); cat != "" {
			ref, err := w.resolver().ResolveCategoryCode(ctx, cat)
			if err != nil {
				return nil, err
			}
			if !ref.Found {
				errs = append(errs, rowErr(r.RowNo, "category_code", "分类编码不存在"))
			} else if !ref.Enabled {
				errs = append(errs, rowErr(r.RowNo, "category_code", "分类已停用"))
			}
		}
		if unit := cellText(r, "unit_code"); unit != "" {
			ref, err := w.resolver().ResolveUnitCode(ctx, unit)
			if err != nil {
				return nil, err
			}
			if !ref.Found {
				errs = append(errs, rowErr(r.RowNo, "unit_code", "单位编码不存在"))
			} else if !ref.Enabled {
				errs = append(errs, rowErr(r.RowNo, "unit_code", "单位已停用"))
			}
		}
		if code := cellText(r, "code"); code != "" {
			ref, err := (&DataxCodeResolver{repo: w.svc.repo}).ResolveProductCode(ctx, code)
			if err != nil {
				return nil, err
			}
			if ref.Found {
				errs = append(errs, rowErr(r.RowNo, "code", "商品编码已存在（如需修改请使用商品编辑）"))
			}
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateProduct 通路写入。
func (w *ProductImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		in := ProductCreateInput{
			Code:      cellText(r, "code"),
			Name:      cellText(r, "name"),
			ShortName: cellText(r, "short_name"),
			Brand:     cellText(r, "brand"),
			Model:     cellText(r, "model"),
			Spec:      cellText(r, "spec"),
			Weight:    Number(cellNumberText(r, "weight")),
			Length:    Number(cellNumberText(r, "length")),
			Width:     Number(cellNumberText(r, "width")),
			Height:    Number(cellNumberText(r, "height")),
			Volume:    Number(cellNumberText(r, "volume")),
			Remark:    cellText(r, "remark"),
		}
		if cat := cellText(r, "category_code"); cat != "" {
			c, err := w.svc.repo.FindCategoryByCode(ctx, cat)
			if err != nil {
				return res, err
			}
			if c != nil {
				id := c.ID.Int64()
				in.CategoryID = &id
			}
		}
		if unit := cellText(r, "unit_code"); unit != "" {
			u, err := w.svc.repo.FindUnitByCode(ctx, unit)
			if err != nil {
				return res, err
			}
			if u != nil {
				id := u.ID.Int64()
				in.UnitID = &id
			}
		}
		if _, err := w.svc.CreateProduct(ctx, masterdataActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// resolver 分类/单位解析（复用 CodeResolver 内部实现；分类/单位不出跨域接口，
// 仅本域写入器使用——挂在 ProductImportWriter 私有方法上）。
func (w *ProductImportWriter) resolver() *categoryUnitResolver {
	return &categoryUnitResolver{repo: w.svc.repo}
}

// categoryUnitResolver 分类/单位只读解析（本域内部用）。
type categoryUnitResolver struct {
	repo Repository
}

func (r *categoryUnitResolver) ResolveCategoryCode(ctx context.Context, code string) (CodeRef, error) {
	c, err := r.repo.FindCategoryByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if c == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: c.ID.Int64(), Enabled: c.Status == StatusEnabled, Found: true}, nil
}

func (r *categoryUnitResolver) ResolveUnitCode(ctx context.Context, code string) (CodeRef, error) {
	u, err := r.repo.FindUnitByCode(ctx, code)
	if err != nil {
		return CodeRef{}, err
	}
	if u == nil {
		return CodeRef{}, nil
	}
	return CodeRef{ID: u.ID.Int64(), Enabled: u.Status == StatusEnabled, Found: true}, nil
}

// ---- SKU 导入（SKU）----

// SKUImportWriter SKU 导入写入器（plan §6.1：商品存在、条码唯一、三开关/安全库存合法）。
type SKUImportWriter struct {
	svc *Service
}

// NewSKUImportWriter 构建 SKU 导入写入器（router 装配）。
func NewSKUImportWriter(svc *Service) datax.ImportWriter { return &SKUImportWriter{svc: svc} }

// Template 模板规格。
func (w *SKUImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportSKU,
		Name:        "SKU 导入",
		FileName:    "SKU导入模板.xlsx",
		Description: "批量导入 SKU；批次管理为效期管理的前置开关（效期管理必须先开启批次管理）。",
		Sheet:       "SKU导入",
		Columns: []datax.Column{
			{Key: "code", Title: "SKU编码", Required: true, UniqueInFile: true, Example: "SKU-0001", Note: "全局唯一，创建后不可修改", Width: 14},
			{Key: "product_code", Title: "商品编码", Required: true, Example: "P-0001", Note: "须为已存在的商品编码", Width: 14},
			{Key: "spec_attrs", Title: "规格属性", Example: `{"颜色":"红","尺寸":"L"}`, Note: "JSON 对象文本，可为空", Width: 24},
			{Key: "barcodes", Title: "条码", Example: "6901000001;6901000002", Note: "分号分隔可多个；首个为主码；全局唯一", Width: 22},
			{Key: "cost_price", Title: "成本价", Type: datax.CellMoney, Width: 12},
			{Key: "sale_price", Title: "销售价", Type: datax.CellMoney, Width: 12},
			{Key: "safety_stock", Title: "安全库存", Type: datax.CellNumber, Width: 12},
			{Key: "max_stock", Title: "最大库存", Type: datax.CellNumber, Width: 12},
			{Key: "min_replenish_qty", Title: "最小补货量", Type: datax.CellNumber, Width: 12},
			{Key: "is_batch_managed", Title: "批次管理", Required: true, Example: "是", Note: "是/否", Width: 10},
			{Key: "is_expiry_managed", Title: "效期管理", Example: "否", Note: "是/否；开启必须先开启批次管理", Width: 10},
			{Key: "is_serial_managed", Title: "序列号管理", Example: "否", Note: "是/否", Width: 12},
			{Key: "is_enabled", Title: "是否启用", Example: "是", Note: "是/否，缺省是", Width: 10},
		},
		SampleRows: [][]string{
			{"SKU-0001", "P-0001", `{"颜色":"红"}`, "6901000001", "10.50", "19.90", "10", "1000", "5", "是", "否", "否", "是"},
		},
		Notes: []string{
			"SKU编码必填且全文件内不得重复、不得与系统已有编码重复",
			"商品编码必须为系统已存在商品",
			"条码如填写，不得与系统已有条码重复（一码一 SKU）",
			"效期管理为“是”时批次管理必须为“是”（效期是批次属性）",
			"价格列 >= 0；库存阈值列为数字",
		},
	}
}

// Validate 业务层校验。
func (w *SKUImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	resolver := &DataxCodeResolver{repo: w.svc.repo}
	for _, r := range rows {
		pc := cellText(r, "product_code")
		if pc != "" {
			ref, err := resolver.ResolveProductCode(ctx, pc)
			if err != nil {
				return nil, err
			}
			if !ref.Found {
				errs = append(errs, rowErr(r.RowNo, "product_code", "商品编码不存在"))
			}
		}
		if code := cellText(r, "code"); code != "" {
			ref, err := resolver.ResolveSKUCode(ctx, code)
			if err != nil {
				return nil, err
			}
			if ref.Found {
				errs = append(errs, rowErr(r.RowNo, "code", "SKU 编码已存在（如需修改请使用 SKU 编辑）"))
			}
		}
		// 三开关合法性（效期 ⇒ 批次）。
		batch, batchOK := boolText(cellText(r, "is_batch_managed"), false)
		expiry, expiryOK := boolText(cellText(r, "is_expiry_managed"), false)
		if batchOK && expiryOK && expiry && !batch {
			errs = append(errs, rowErr(r.RowNo, "is_expiry_managed", "效期管理必须先开启批次管理"))
		}
		// 条码库内唯一（uk_barcodes_barcode 语义前置校验）。
		if bcs := cellText(r, "barcodes"); bcs != "" {
			for _, b := range strings.Split(bcs, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				bc, err := w.svc.repo.FindBarcode(ctx, b)
				if err != nil {
					return nil, err
				}
				if bc != nil {
					errs = append(errs, rowErr(r.RowNo, "barcodes", "条码 "+b+" 已被其他 SKU 占用"))
					break
				}
			}
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateSKU 通路写入。
func (w *SKUImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		p, err := w.svc.repo.FindProductByCode(ctx, cellText(r, "product_code"))
		if err != nil {
			return res, err
		}
		if p == nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "product_code", "商品编码不存在"))
			continue
		}
		in := SKUCreateInput{
			Code:            cellText(r, "code"),
			ProductID:       p.ID.Int64(),
			CostPrice:       Number(cellNumberText(r, "cost_price")),
			SalePrice:       Number(cellNumberText(r, "sale_price")),
			SafetyStock:     Number(cellNumberText(r, "safety_stock")),
			MaxStock:        Number(cellNumberText(r, "max_stock")),
			MinReplenishQty: Number(cellNumberText(r, "min_replenish_qty")),
		}
		if raw := cellText(r, "spec_attrs"); raw != "" {
			attrs := AttrMap{}
			if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
				res.FailedRows++
				res.Errors = append(res.Errors, rowErr(r.RowNo, "spec_attrs", "规格属性必须是合法 JSON 对象"))
				continue
			}
			in.SpecAttrs = attrs
		}
		if bcs := cellText(r, "barcodes"); bcs != "" {
			for i, b := range strings.Split(bcs, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				in.Barcodes = append(in.Barcodes, BarcodeInput{Barcode: b, IsPrimary: i == 0})
			}
		}
		batch, _ := boolText(cellText(r, "is_batch_managed"), false)
		expiry, _ := boolText(cellText(r, "is_expiry_managed"), false)
		serial, _ := boolText(cellText(r, "is_serial_managed"), false)
		enabled, _ := boolText(cellText(r, "is_enabled"), true)
		in.IsBatchManaged = batch
		in.IsExpiryManaged = expiry
		in.IsSerialManaged = serial
		in.IsEnabled = &enabled
		if _, err := w.svc.CreateSKU(ctx, masterdataActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// ---- 供应商 / 客户导入 ----

// partnerTemplate 供应商/客户共用模板骨架。
func partnerTemplate(importType, name, sheet string, extra []datax.Column, notes []string) datax.TemplateSpec {
	cols := []datax.Column{
		{Key: "code", Title: "编码", Required: true, UniqueInFile: true, Example: "S-0001", Note: "全局唯一，创建后不可修改", Width: 14},
		{Key: "name", Title: "名称", Required: true, Example: "示例单位", Width: 20},
		{Key: "contact", Title: "联系人", Width: 12},
		{Key: "phone", Title: "电话", Example: "13800000000", Width: 14},
		{Key: "email", Title: "邮箱", Example: "a@example.com", Width: 18},
		{Key: "address", Title: "地址", Width: 26},
	}
	cols = append(cols, extra...)
	return datax.TemplateSpec{
		ImportType:  importType,
		Name:        name,
		FileName:    name + "模板.xlsx",
		Description: "批量导入" + name + "；编码已存在视为错误行。",
		Sheet:       sheet,
		Columns:     cols,
		Notes:       notes,
	}
}

var partnerSample = []string{"S-0001", "示例单位", "张三", "13800000000", "a@example.com", "示例市示例区示例路 1 号"}

// SupplierImportWriter 供应商导入写入器（plan §6.1：编码重复、联系人/电话格式）。
type SupplierImportWriter struct{ svc *Service }

// NewSupplierImportWriter 构建供应商导入写入器（router 装配）。
func NewSupplierImportWriter(svc *Service) datax.ImportWriter { return &SupplierImportWriter{svc: svc} }

// Template 模板规格。
func (w *SupplierImportWriter) Template() datax.TemplateSpec {
	spec := partnerTemplate(datax.ImportSupplier, "供应商导入", "供应商导入", []datax.Column{
		{Key: "remark", Title: "备注", Width: 20},
	}, []string{
		"编码必填且全文件内不得重复、不得与系统已有编码重复",
		"电话/邮箱格式由系统校验（可空）",
	})
	spec.SampleRows = [][]string{append(append([]string{}, partnerSample...), "示例行，导入前请删除")}
	return spec
}

// Validate 业务层校验（编码与库内重复）。
func (w *SupplierImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		ref, err := (&DataxCodeResolver{repo: w.svc.repo}).ResolveSupplierCode(ctx, cellText(r, "code"))
		if err != nil {
			return nil, err
		}
		if ref.Found {
			errs = append(errs, rowErr(r.RowNo, "code", "供应商编码已存在（如需修改请使用供应商编辑）"))
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateSupplier 通路写入。
func (w *SupplierImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		in := SupplierCreateInput{
			Code:    cellText(r, "code"),
			Name:    cellText(r, "name"),
			Contact: cellText(r, "contact"),
			Phone:   cellText(r, "phone"),
			Email:   cellText(r, "email"),
			Address: cellText(r, "address"),
			Remark:  cellText(r, "remark"),
		}
		if _, err := w.svc.CreateSupplier(ctx, masterdataActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// CustomerImportWriter 客户导入写入器。
type CustomerImportWriter struct{ svc *Service }

// NewCustomerImportWriter 构建客户导入写入器（router 装配）。
func NewCustomerImportWriter(svc *Service) datax.ImportWriter { return &CustomerImportWriter{svc: svc} }

// Template 模板规格。
func (w *CustomerImportWriter) Template() datax.TemplateSpec {
	spec := partnerTemplate(datax.ImportCustomer, "客户导入", "客户导入", []datax.Column{
		{Key: "shipping_address", Title: "收货地址", Width: 26},
	}, []string{
		"编码必填且全文件内不得重复、不得与系统已有编码重复",
		"电话/邮箱格式由系统校验（可空）",
	})
	sample := append(append([]string{}, partnerSample...), "示例市示例区收货路 2 号")
	spec.SampleRows = [][]string{sample}
	return spec
}

// Validate 业务层校验。
func (w *CustomerImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		ref, err := (&DataxCodeResolver{repo: w.svc.repo}).ResolveCustomerCode(ctx, cellText(r, "code"))
		if err != nil {
			return nil, err
		}
		if ref.Found {
			errs = append(errs, rowErr(r.RowNo, "code", "客户编码已存在（如需修改请使用客户编辑）"))
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateCustomer 通路写入。
func (w *CustomerImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		in := CustomerCreateInput{
			Code:            cellText(r, "code"),
			Name:            cellText(r, "name"),
			Contact:         cellText(r, "contact"),
			Phone:           cellText(r, "phone"),
			Email:           cellText(r, "email"),
			Address:         cellText(r, "address"),
			ShippingAddress: cellText(r, "shipping_address"),
		}
		if _, err := w.svc.CreateCustomer(ctx, masterdataActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}
