package masterdata

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// SKU 与条码服务（business-flow §1.2；backend-m1-plan §6.2 skus/barcodes 表）。
//
// 三开关约束（inventory-rules §6–§8，供 M2 入库/库存/出库分配分支消费）：
//   - is_expiry_managed=true 必须 is_batch_managed=true——效期是批次属性
//     （batches.expiry_date），无批次维度的 SKU 无处承载效期（backend-m1-plan §8.1）；
//   - is_serial_managed 独立（一物一码，serial_numbers 表逐件追踪）。
//
// 条码规则：一码一 SKU 全局唯一（uk_barcodes_barcode）；更新为全量替换（同事务
// 先删后插）；SKU 软删后其条码仍占用命名空间——条码印在实物上，回收复用会造成
// 扫码追溯一码两 SKU（models.go Barcode 注释同口径）。

// maxBarcodesPerSKU 单 SKU 条码上限（防御性业务上限，防误传大数组）。
const maxBarcodesPerSKU = 50

// ---- 视图 DTO ----

// BarcodeView 条码视图。
type BarcodeView struct {
	ID        database.ID `json:"id"`
	Barcode   string      `json:"barcode"`
	CodeType  string      `json:"code_type"`
	IsPrimary bool        `json:"is_primary"`
}

// SKUView SKU 视图。
type SKUView struct {
	ID              database.ID       `json:"id"`
	Code            string            `json:"code"`
	ProductID       database.ID       `json:"product_id"`
	ProductCode     string            `json:"product_code,omitempty"` // 详情装配
	ProductName     string            `json:"product_name,omitempty"` // 详情装配
	SpecAttrs       AttrMap           `json:"spec_attrs"`
	CostPrice       Number            `json:"cost_price"`
	SalePrice       Number            `json:"sale_price"`
	SafetyStock     Number            `json:"safety_stock"`
	MaxStock        Number            `json:"max_stock"`
	MinReplenishQty Number            `json:"min_replenish_qty"`
	IsBatchManaged  bool              `json:"is_batch_managed"`
	IsExpiryManaged bool              `json:"is_expiry_managed"`
	IsSerialManaged bool              `json:"is_serial_managed"`
	IsEnabled       bool              `json:"is_enabled"`
	Barcodes        []BarcodeView     `json:"barcodes"`
	CreatedAt       database.JSONTime `json:"created_at"`
	UpdatedAt       database.JSONTime `json:"updated_at"`
}

func viewSKU(s *SKU) *SKUView {
	v := &SKUView{
		ID:              s.ID,
		Code:            s.Code,
		ProductID:       s.ProductID,
		SpecAttrs:       s.SpecAttrs.normalize(),
		CostPrice:       s.CostPrice,
		SalePrice:       s.SalePrice,
		SafetyStock:     s.SafetyStock,
		MaxStock:        s.MaxStock,
		MinReplenishQty: s.MinReplenishQty,
		IsBatchManaged:  s.IsBatchManaged,
		IsExpiryManaged: s.IsExpiryManaged,
		IsSerialManaged: s.IsSerialManaged,
		IsEnabled:       s.IsEnabled,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
	return v
}

// withBarcodes 填充条码视图。
func (v *SKUView) withBarcodes(bcs []*Barcode) {
	v.Barcodes = make([]BarcodeView, 0, len(bcs))
	for _, b := range bcs {
		v.Barcodes = append(v.Barcodes, BarcodeView{
			ID: b.ID, Barcode: b.Barcode, CodeType: b.CodeType, IsPrimary: b.IsPrimary,
		})
	}
}

// assembleSKUViews 批量装配条码（一次 IN 查询，防 N+1，architecture.md §7）。
func (s *Service) assembleSKUViews(ctx context.Context, skus []*SKU, withProduct bool) ([]*SKUView, error) {
	views := make([]*SKUView, 0, len(skus))
	ids := make([]int64, 0, len(skus))
	for _, sku := range skus {
		ids = append(ids, sku.ID.Int64())
	}
	bySku := map[int64][]*Barcode{}
	if len(ids) > 0 {
		bcs, err := s.repo.ListBarcodesBySKUs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, b := range bcs {
			bySku[b.SKUID.Int64()] = append(bySku[b.SKUID.Int64()], b)
		}
	}
	for _, sku := range skus {
		v := viewSKU(sku)
		v.withBarcodes(bySku[sku.ID.Int64()])
		views = append(views, v)
	}
	if withProduct {
		byProduct := map[int64]*Product{}
		for _, sku := range skus {
			pid := sku.ProductID.Int64()
			if _, ok := byProduct[pid]; ok {
				continue
			}
			p, err := s.repo.FindProductByID(ctx, pid)
			if err != nil {
				return nil, err
			}
			byProduct[pid] = p
		}
		for _, v := range views {
			if p := byProduct[v.ProductID.Int64()]; p != nil {
				v.ProductCode = p.Code
				v.ProductName = p.Name
			}
		}
	}
	return views, nil
}

// ---- 输入 DTO ----

// BarcodeInput 条码入参。
type BarcodeInput struct {
	Barcode   string `json:"barcode"`
	CodeType  string `json:"code_type"`
	IsPrimary bool   `json:"is_primary"`
}

// SKUCreateInput 创建 SKU 入参（编码唯一；状态由 is_enabled 控制，缺省启用）。
type SKUCreateInput struct {
	Code            string         `json:"code"`
	ProductID       int64          `json:"product_id"`
	SpecAttrs       AttrMap        `json:"spec_attrs"`
	CostPrice       Number         `json:"cost_price"`
	SalePrice       Number         `json:"sale_price"`
	SafetyStock     Number         `json:"safety_stock"`
	MaxStock        Number         `json:"max_stock"`
	MinReplenishQty Number         `json:"min_replenish_qty"`
	IsBatchManaged  bool           `json:"is_batch_managed"`
	IsExpiryManaged bool           `json:"is_expiry_managed"`
	IsSerialManaged bool           `json:"is_serial_managed"`
	IsEnabled       *bool          `json:"is_enabled"`
	Barcodes        []BarcodeInput `json:"barcodes"`
}

// SKUUpdateInput 更新 SKU 入参（编码不可改；指针/切片三态：nil 不修改；空切片=清空）。
type SKUUpdateInput struct {
	ProductID       *int64          `json:"product_id"`
	SpecAttrs       map[string]any  `json:"spec_attrs"`
	CostPrice       *Number         `json:"cost_price"`
	SalePrice       *Number         `json:"sale_price"`
	SafetyStock     *Number         `json:"safety_stock"`
	MaxStock        *Number         `json:"max_stock"`
	MinReplenishQty *Number         `json:"min_replenish_qty"`
	IsBatchManaged  *bool           `json:"is_batch_managed"`
	IsExpiryManaged *bool           `json:"is_expiry_managed"`
	IsSerialManaged *bool           `json:"is_serial_managed"`
	IsEnabled       *bool           `json:"is_enabled"`
	Barcodes        *[]BarcodeInput `json:"barcodes"`
}

// ---- 校验 ----

// hasControlChar 拒绝控制字符（扫描设备上传的条码值应为可见字符）。
func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// buildBarcodes 条码入参校验与构建：非空 ≤128、无控制字符、类型缺省 CODE128、
// 列表内不重复、主条码至多一个（一码一 SKU，uk_barcodes_barcode）。
func buildBarcodes(in []BarcodeInput) ([]*Barcode, error) {
	if len(in) > maxBarcodesPerSKU {
		return nil, invalidParam("barcodes", "最多 "+itoa(maxBarcodesPerSKU)+" 个")
	}
	seen := map[string]bool{}
	primaryCount := 0
	out := make([]*Barcode, 0, len(in))
	for i, b := range in {
		code := strings.TrimSpace(b.Barcode)
		at := "第 " + itoa(i+1) + " 项"
		if code == "" {
			return nil, invalidParam("barcodes", at+"条码为空")
		}
		if len(code) > 128 {
			return nil, invalidParam("barcodes", at+"条码超过 128 字符")
		}
		if hasControlChar(code) {
			return nil, invalidParam("barcodes", at+"条码含非法控制字符")
		}
		if seen[code] {
			return nil, invalidParam("barcodes", at+"条码重复: "+code)
		}
		seen[code] = true
		ct := strings.ToUpper(strings.TrimSpace(b.CodeType))
		if ct == "" {
			ct = CodeTypeDefault
		}
		if !barcodeTypeRe.MatchString(ct) {
			return nil, invalidParam("barcodes", at+"条码类型格式不正确（1-32 位大写字母/数字/连字符）")
		}
		if b.IsPrimary {
			primaryCount++
		}
		out = append(out, &Barcode{Barcode: code, CodeType: ct, IsPrimary: b.IsPrimary})
	}
	if primaryCount > 1 {
		return nil, invalidParam("barcodes", "主条码最多一个")
	}
	return out, nil
}

// assertBarcodesAvailable 条码全局占用校验：已被其他 SKU 占用即冲突
// （api.md §4 唯一性校验；uk_barcodes_barcode 为竞态兜底）。
func (s *Service) assertBarcodesAvailable(ctx context.Context, bcs []*Barcode, ownerSKU int64) error {
	for _, b := range bcs {
		exist, err := s.repo.FindBarcode(ctx, b.Barcode)
		if err != nil {
			return err
		}
		if exist != nil && exist.SKUID.Int64() != ownerSKU {
			return response.NewError(ErrBarcodeExists, map[string]any{
				"barcode": b.Barcode, "occupied_by_sku_id": exist.SKUID,
			})
		}
	}
	return nil
}

// assertFlagsBatchExpiry 三开关一致性：效期管理必须先启用批次管理。
func assertFlagsBatchExpiry(batch, expiry bool) error {
	if expiry && !batch {
		return invalidParam("is_expiry_managed", "效期管理必须先启用批次管理（效期为批次属性，inventory-rules §6/§7）")
	}
	return nil
}

// ---- 业务方法 ----

// CreateSKU 创建 SKU（含条码）。
func (s *Service) CreateSKU(ctx context.Context, actor Actor, in SKUCreateInput) (*SKUView, error) {
	if err := validateCode("code", in.Code, false); err != nil {
		return nil, err
	}
	if in.ProductID <= 0 {
		return nil, invalidParam("product_id", "必须为正整数")
	}
	if in.SpecAttrs != nil {
		if _, ok := AttrMap(in.SpecAttrs).Value(); ok != nil {
			return nil, invalidParam("spec_attrs", "必须为 JSON 对象")
		}
	}
	for field, n := range map[string]Number{
		"cost_price": in.CostPrice, "sale_price": in.SalePrice,
		"safety_stock": in.SafetyStock, "max_stock": in.MaxStock,
		"min_replenish_qty": in.MinReplenishQty,
	} {
		if err := validateNonNegative(field, n); err != nil {
			return nil, err
		}
	}
	if err := validateStockRange(in.SafetyStock, in.MaxStock); err != nil {
		return nil, err
	}
	if err := assertFlagsBatchExpiry(in.IsBatchManaged, in.IsExpiryManaged); err != nil {
		return nil, err
	}
	bcs, err := buildBarcodes(in.Barcodes)
	if err != nil {
		return nil, err
	}
	// 业务关系校验（api.md §4）：商品存在 + 启用（停用商品不允许挂新 SKU）。
	p, err := s.repo.FindProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, response.NewError(ErrProductNotFound, map[string]any{"product_id": in.ProductID})
	}
	if p.Status != StatusEnabled {
		return nil, response.NewError(ErrProductDisabled, map[string]any{"product_id": in.ProductID})
	}
	if exist, err := s.repo.FindSKUByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrSKUCodeExists, map[string]any{"code": in.Code})
	}
	if err := s.assertBarcodesAvailable(ctx, bcs, 0); err != nil {
		return nil, err
	}

	enabled := true
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}
	sku := &SKU{
		Code:            in.Code,
		ProductID:       database.ID(in.ProductID),
		SpecAttrs:       in.SpecAttrs.normalize(),
		CostPrice:       in.CostPrice,
		SalePrice:       in.SalePrice,
		SafetyStock:     in.SafetyStock,
		MaxStock:        in.MaxStock,
		MinReplenishQty: in.MinReplenishQty,
		IsBatchManaged:  in.IsBatchManaged,
		IsExpiryManaged: in.IsExpiryManaged,
		IsSerialManaged: in.IsSerialManaged,
		IsEnabled:       enabled,
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertSKU(ctx, tx, sku); err != nil {
			return err
		}
		if err := s.repo.ReplaceBarcodes(ctx, tx, sku.ID.Int64(), bcs, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("sku", sku.ID.Int64(), "create")
		e.Success = true
		after := viewSKU(sku)
		after.withBarcodes(bcs)
		e.After = after
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewSKU(sku)
	v.withBarcodes(bcs)
	return v, nil
}

// UpdateSKU 更新 SKU（含条码全量替换；跨字段以"更新后的最终值"校验）。
func (s *Service) UpdateSKU(ctx context.Context, actor Actor, id int64, in SKUUpdateInput) (*SKUView, error) {
	sku, err := s.repo.FindSKUByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, response.NewError(ErrSKUNotFound, map[string]any{"id": id})
	}
	before, err := s.assembleSKUViews(ctx, []*SKU{sku}, false)
	if err != nil {
		return nil, err
	}

	// —— 商品换挂（存在 + 启用）——
	if in.ProductID != nil {
		if *in.ProductID <= 0 {
			return nil, invalidParam("product_id", "必须为正整数")
		}
		if *in.ProductID != sku.ProductID.Int64() {
			p, err := s.repo.FindProductByID(ctx, *in.ProductID)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, response.NewError(ErrProductNotFound, map[string]any{"product_id": *in.ProductID})
			}
			if p.Status != StatusEnabled {
				return nil, response.NewError(ErrProductDisabled, map[string]any{"product_id": *in.ProductID})
			}
		}
	}

	// —— 三开关一致性（最终值）——
	batch, expiry := sku.IsBatchManaged, sku.IsExpiryManaged
	if in.IsBatchManaged != nil {
		batch = *in.IsBatchManaged
	}
	if in.IsExpiryManaged != nil {
		expiry = *in.IsExpiryManaged
	}
	if err := assertFlagsBatchExpiry(batch, expiry); err != nil {
		return nil, err
	}

	// —— 数量/金额（最终值非负 + 库存范围）——
	safety, max := sku.SafetyStock, sku.MaxStock
	if in.SafetyStock != nil {
		safety = *in.SafetyStock
	}
	if in.MaxStock != nil {
		max = *in.MaxStock
	}
	for field, n := range map[string]Number{
		"cost_price": sku.CostPrice, "sale_price": sku.SalePrice,
		"safety_stock": safety, "max_stock": max,
		"min_replenish_qty": sku.MinReplenishQty,
	} {
		if err := validateNonNegative(field, n); err != nil {
			return nil, err
		}
	}
	if err := validateStockRange(safety, max); err != nil {
		return nil, err
	}

	// —— 条码（nil 不修改；提供即全量替换）——
	var bcs []*Barcode
	if in.Barcodes != nil {
		bcs, err = buildBarcodes(*in.Barcodes)
		if err != nil {
			return nil, err
		}
		if err := s.assertBarcodesAvailable(ctx, bcs, id); err != nil {
			return nil, err
		}
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.ProductID != nil {
		sku.ProductID = database.ID(*in.ProductID)
		cols["product_id"] = sku.ProductID
	}
	if in.SpecAttrs != nil {
		sku.SpecAttrs = AttrMap(in.SpecAttrs).normalize()
		cols["spec_attrs"] = sku.SpecAttrs
	}
	if in.CostPrice != nil {
		sku.CostPrice = *in.CostPrice
		cols["cost_price"] = sku.CostPrice
	}
	if in.SalePrice != nil {
		sku.SalePrice = *in.SalePrice
		cols["sale_price"] = sku.SalePrice
	}
	if in.SafetyStock != nil {
		sku.SafetyStock = *in.SafetyStock
		cols["safety_stock"] = sku.SafetyStock
	}
	if in.MaxStock != nil {
		sku.MaxStock = *in.MaxStock
		cols["max_stock"] = sku.MaxStock
	}
	if in.MinReplenishQty != nil {
		sku.MinReplenishQty = *in.MinReplenishQty
		cols["min_replenish_qty"] = sku.MinReplenishQty
	}
	if in.IsBatchManaged != nil {
		sku.IsBatchManaged = *in.IsBatchManaged
		cols["is_batch_managed"] = sku.IsBatchManaged
	}
	if in.IsExpiryManaged != nil {
		sku.IsExpiryManaged = *in.IsExpiryManaged
		cols["is_expiry_managed"] = sku.IsExpiryManaged
	}
	if in.IsSerialManaged != nil {
		sku.IsSerialManaged = *in.IsSerialManaged
		cols["is_serial_managed"] = sku.IsSerialManaged
	}
	if in.IsEnabled != nil {
		sku.IsEnabled = *in.IsEnabled
		cols["is_enabled"] = sku.IsEnabled
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateSKUCols(ctx, tx, id, cols); err != nil {
			return err
		}
		if bcs != nil {
			if err := s.repo.ReplaceBarcodes(ctx, tx, id, bcs, actor.UserID); err != nil {
				return err
			}
		}
		e := actor.auditEntry("sku", id, "update")
		e.Success = true
		e.Before = before[0]
		after := viewSKU(sku)
		if bcs != nil {
			after.withBarcodes(bcs)
		} else {
			after.withBarcodes(bySKUID(before[0].Barcodes))
		}
		e.After = after
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewSKU(sku)
	if bcs != nil {
		v.withBarcodes(bcs)
	} else {
		exist, err := s.repo.ListBarcodesBySKUs(ctx, []int64{id})
		if err != nil {
			return nil, err
		}
		v.withBarcodes(exist)
	}
	return v, nil
}

// bySKUID 视图条码 → 模型条码（审计 After 快照复用）。
func bySKUID(views []BarcodeView) []*Barcode {
	out := make([]*Barcode, 0, len(views))
	for _, v := range views {
		out = append(out, &Barcode{Barcode: v.Barcode, CodeType: v.CodeType, IsPrimary: v.IsPrimary})
	}
	return out
}

// GetSKU SKU 详情（含条码与商品信息）。
func (s *Service) GetSKU(ctx context.Context, id int64) (*SKUView, error) {
	sku, err := s.repo.FindSKUByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, response.NewError(ErrSKUNotFound, map[string]any{"id": id})
	}
	views, err := s.assembleSKUViews(ctx, []*SKU{sku}, true)
	if err != nil {
		return nil, err
	}
	return views[0], nil
}

// ListSkus SKU 分页列表（含条码批量装配）。
func (s *Service) ListSkus(ctx context.Context, f SKUListFilter) ([]*SKUView, int64, error) {
	items, total, err := s.repo.ListSkus(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.assembleSKUViews(ctx, items, false)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// UpdateSKUStatus 启用/停用 SKU（is_enabled 布尔开关；停用即不可被出库分配预占，
// inventory-rules §4 分配只允许可用库存口径之外的启停门槛由 M2 消费）。
func (s *Service) UpdateSKUStatus(ctx context.Context, actor Actor, id int64, enabled bool) error {
	sku, err := s.repo.FindSKUByID(ctx, id)
	if err != nil {
		return err
	}
	if sku == nil {
		return response.NewError(ErrSKUNotFound, map[string]any{"id": id})
	}
	before, err := s.assembleSKUViews(ctx, []*SKU{sku}, false)
	if err != nil {
		return err
	}

	sku.IsEnabled = enabled
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateSKUCols(ctx, tx, id, map[string]any{
			"is_enabled": enabled,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("sku", id, "status") // 停用/启用强制审计（backend-m1-plan §4.4）
		e.Success = true
		e.Before = before[0]
		e.After = viewSKU(sku)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}

// DeleteSKU 软删除 SKU（database.md §5.1）：条码随 SKU 保留占用（扫码追溯安全，
// 见文件头注释）；库存/单据引用校验随 M2 经消费方窄接口补齐（backend-m1-plan §4.3）。
func (s *Service) DeleteSKU(ctx context.Context, actor Actor, id int64) error {
	sku, err := s.repo.FindSKUByID(ctx, id)
	if err != nil {
		return err
	}
	if sku == nil {
		return response.NewError(ErrSKUNotFound, map[string]any{"id": id})
	}
	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteSKU(ctx, tx, id, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("sku", id, "delete") // 删除强制审计（backend-m1-plan §4.4）
		e.Success = true
		e.Before = viewSKU(sku)
		return middleware.Audit(tx, e)
	})
}
