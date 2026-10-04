package masterdata

// 测试替身：内存版 Repository（ask 约束：单测不依赖 PostgreSQL，用接口替身）。
// Service 的全部数据访问经本替身承载；事务句柄来自 openTestGorm（no-op 驱动）。
// 语义对齐 GORM 实现：软删行（deleted_at 非零）对查询不可见；唯一编码/条码冲突
// 显式转为业务错误码（模拟部分唯一索引兜底路径）。

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

type fakeRepo struct {
	mu         sync.Mutex
	db         *gorm.DB
	seq        int64
	categories map[int64]*ProductCategory
	catByCode  map[string]int64
	units      map[int64]*Unit
	unitByCode map[string]int64
	products   map[int64]*Product
	prodByCode map[string]int64
	skus       map[int64]*SKU
	skuByCode  map[string]int64
	barcodeSeq int64
	barcodeIDs map[int64]*Barcode
	barcodes   map[string]int64 // 条码值 → 条码行 ID（一码一 SKU）
	suppliers  map[int64]*Supplier
	supByCode  map[string]int64
	customers  map[int64]*Customer
	cusByCode  map[string]int64
}

func newFakeRepo(spy *auditSpy) *fakeRepo {
	db, err := openTestGorm(spy)
	if err != nil {
		panic("打开假 gorm 失败: " + err.Error())
	}
	return &fakeRepo{
		db:         db,
		categories: map[int64]*ProductCategory{},
		catByCode:  map[string]int64{},
		units:      map[int64]*Unit{},
		unitByCode: map[string]int64{},
		products:   map[int64]*Product{},
		prodByCode: map[string]int64{},
		skus:       map[int64]*SKU{},
		skuByCode:  map[string]int64{},
		barcodeIDs: map[int64]*Barcode{},
		barcodes:   map[string]int64{},
		suppliers:  map[int64]*Supplier{},
		supByCode:  map[string]int64{},
		customers:  map[int64]*Customer{},
		cusByCode:  map[string]int64{},
	}
}

func (f *fakeRepo) DB() *gorm.DB { return f.db }

func (f *fakeRepo) nextID() int64 {
	f.seq++
	return f.seq
}

// deleted 软删判定（gorm.DeletedAt 置位即不可见）。
func deletedAtSet(d gorm.DeletedAt) bool { return d.Valid || !d.Time.IsZero() }

// ---- seed helpers（测试夹具）----

func (f *fakeRepo) seedCategory(code, status string, parentID int64) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	c := &ProductCategory{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		Code:     code, Name: "分类" + code, Status: status,
	}
	if parentID > 0 {
		pid := database.ID(parentID)
		c.ParentID = &pid
	}
	f.categories[id] = c
	f.catByCode[code] = id
	return id
}

func (f *fakeRepo) seedUnit(code, status string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.units[id] = &Unit{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		Code:     code, Name: "单位" + code, Status: status,
	}
	f.unitByCode[code] = id
	return id
}

func (f *fakeRepo) seedProduct(code, status string, mut func(*Product)) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	p := &Product{
		BaseModel: database.BaseModel{ID: database.ID(id)},
		Code:      code, Name: "商品" + code, Status: status,
	}
	if mut != nil {
		mut(p)
	}
	f.products[id] = p
	f.prodByCode[code] = id
	return id
}

func (f *fakeRepo) seedSKU(code string, productID int64, enabled bool, mut func(*SKU)) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	s := &SKU{
		BaseModel: database.BaseModel{ID: database.ID(id)},
		Code:      code, ProductID: database.ID(productID), IsEnabled: enabled,
	}
	if mut != nil {
		mut(s)
	}
	f.skus[id] = s
	f.skuByCode[code] = id
	return id
}

func (f *fakeRepo) seedBarcode(skuID int64, code string, primary bool) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.barcodeSeq++
	id := f.barcodeSeq
	f.barcodeIDs[id] = &Barcode{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		SKUID:    database.ID(skuID), Barcode: code, CodeType: CodeTypeDefault, IsPrimary: primary,
	}
	f.barcodes[code] = id
	return id
}

func (f *fakeRepo) seedSupplier(code, status string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.suppliers[id] = &Supplier{
		BaseModel: database.BaseModel{ID: database.ID(id)},
		Code:      code, Name: "供应商" + code, Status: status,
	}
	f.supByCode[code] = id
	return id
}

func (f *fakeRepo) seedCustomer(code, status string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.customers[id] = &Customer{
		BaseModel: database.BaseModel{ID: database.ID(id)},
		Code:      code, Name: "客户" + code, Status: status,
	}
	f.cusByCode[code] = id
	return id
}

// ---- 通用 ----

// idOf 提取实体 ID（pageItems 稳定排序用）。
func idOf(v any) database.ID {
	switch t := v.(type) {
	case *ProductCategory:
		return t.ID
	case *Unit:
		return t.ID
	case *Product:
		return t.ID
	case *SKU:
		return t.ID
	case *Supplier:
		return t.ID
	case *Customer:
		return t.ID
	}
	return 0
}

// pageItems 内存分页（id 升序，稳定排序）。
func pageItems[T any](items []*T, page, pageSize int) []*T {
	sort.Slice(items, func(i, j int) bool { return idOf(items[i]) < idOf(items[j]) })
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// ---- 商品分类 ----

func (f *fakeRepo) FindCategoryByID(_ context.Context, id int64) (*ProductCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.categories[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindCategoryByCode(_ context.Context, code string) (*ProductCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.catByCode[code]; ok {
		cp := *f.categories[id]
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListCategories(_ context.Context, fl CategoryListFilter) ([]*ProductCategory, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ProductCategory
	for _, c := range f.categories {
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(c.Code), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(c.Name), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.RootOnly && c.ParentID != nil {
			continue
		}
		if !fl.RootOnly && fl.ParentID != nil {
			if c.ParentID == nil || c.ParentID.Int64() != *fl.ParentID {
				continue
			}
		}
		if fl.Status != "" && c.Status != fl.Status {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertCategory(_ context.Context, _ *gorm.DB, c *ProductCategory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.catByCode[c.Code]; dup {
		return response.NewError(ErrCategoryCodeExists, map[string]any{"code": c.Code})
	}
	c.ID = database.ID(f.nextID())
	f.categories[c.ID.Int64()] = c
	f.catByCode[c.Code] = c.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateCategoryCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.categories[id]
	if !ok {
		return fmt.Errorf("分类 %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "parent_id":
			if v == nil {
				c.ParentID = nil
			} else {
				c.ParentID = v.(*database.ID)
			}
		case "name":
			c.Name = v.(string)
		case "sort":
			c.Sort = v.(int)
		case "status":
			c.Status = v.(string)
		case "updated_by":
			c.UpdatedBy = v.(database.ID)
		}
	}
	return nil
}

func (f *fakeRepo) CountEnabledChildCategories(_ context.Context, parentID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, c := range f.categories {
		if c.ParentID != nil && c.ParentID.Int64() == parentID && c.Status == StatusEnabled {
			n++
		}
	}
	return n, nil
}

// ---- 计量单位 ----

func (f *fakeRepo) FindUnitByID(_ context.Context, id int64) (*Unit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.units[id]; ok {
		up := *u
		return &up, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindUnitByCode(_ context.Context, code string) (*Unit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.unitByCode[code]; ok {
		up := *f.units[id]
		return &up, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListUnits(_ context.Context, fl UnitListFilter) ([]*Unit, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Unit
	for _, u := range f.units {
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(u.Code), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(u.Name), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.Status != "" && u.Status != fl.Status {
			continue
		}
		up := *u
		out = append(out, &up)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertUnit(_ context.Context, _ *gorm.DB, u *Unit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.unitByCode[u.Code]; dup {
		return response.NewError(ErrUnitCodeExists, map[string]any{"code": u.Code})
	}
	u.ID = database.ID(f.nextID())
	f.units[u.ID.Int64()] = u
	f.unitByCode[u.Code] = u.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateUnitCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.units[id]
	if !ok {
		return fmt.Errorf("单位 %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			u.Name = v.(string)
		case "status":
			u.Status = v.(string)
		case "updated_by":
			u.UpdatedBy = v.(database.ID)
		}
	}
	return nil
}

// ---- 引用计数 ----

func (f *fakeRepo) CountProductsByCategory(_ context.Context, categoryID int64, onlyEnabled bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, p := range f.products {
		if p.CategoryID != nil && p.CategoryID.Int64() == categoryID && !deletedAtSet(p.DeletedAt) {
			if onlyEnabled && p.Status != StatusEnabled {
				continue
			}
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) CountProductsByUnit(_ context.Context, unitID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, p := range f.products {
		if p.UnitID != nil && p.UnitID.Int64() == unitID && !deletedAtSet(p.DeletedAt) {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) CountSkusByProduct(_ context.Context, productID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, s := range f.skus {
		if s.ProductID.Int64() == productID && !deletedAtSet(s.DeletedAt) {
			n++
		}
	}
	return n, nil
}

// ---- 商品 ----

func (f *fakeRepo) FindProductByID(_ context.Context, id int64) (*Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.products[id]; ok && !deletedAtSet(p.DeletedAt) {
		pp := *p
		return &pp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindProductByCode(_ context.Context, code string) (*Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.prodByCode[code]; ok && !deletedAtSet(f.products[id].DeletedAt) {
		pp := *f.products[id]
		return &pp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListProducts(_ context.Context, fl ProductListFilter) ([]*Product, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Product
	for _, p := range f.products {
		if deletedAtSet(p.DeletedAt) {
			continue
		}
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(p.Code), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(p.Name), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(p.ShortName), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.CategoryID > 0 && (p.CategoryID == nil || p.CategoryID.Int64() != fl.CategoryID) {
			continue
		}
		if fl.Status != "" && p.Status != fl.Status {
			continue
		}
		pp := *p
		out = append(out, &pp)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertProduct(_ context.Context, _ *gorm.DB, p *Product) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, dup := f.prodByCode[p.Code]; dup && !deletedAtSet(f.products[id].DeletedAt) {
		return response.NewError(ErrProductCodeExists, map[string]any{"code": p.Code})
	}
	p.ID = database.ID(f.nextID())
	f.products[p.ID.Int64()] = p
	f.prodByCode[p.Code] = p.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateProductCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.products[id]
	if !ok {
		return fmt.Errorf("商品 %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			p.Name = v.(string)
		case "short_name":
			p.ShortName = v.(string)
		case "category_id":
			p.CategoryID = ptrIDOf(v)
		case "brand":
			p.Brand = v.(string)
		case "model":
			p.Model = v.(string)
		case "spec":
			p.Spec = v.(string)
		case "unit_id":
			p.UnitID = ptrIDOf(v)
		case "weight":
			p.Weight = v.(Number)
		case "length":
			p.Length = v.(Number)
		case "width":
			p.Width = v.(Number)
		case "height":
			p.Height = v.(Number)
		case "volume":
			p.Volume = v.(Number)
		case "image_urls":
			p.ImageURLs = v.(StringList)
		case "description":
			p.Description = v.(string)
		case "remark":
			p.Remark = v.(string)
		case "status":
			p.Status = v.(string)
		case "updated_by":
			p.UpdatedBy = v.(database.ID)
		case "deleted_at":
			p.DeletedAt = v.(gorm.DeletedAt)
		}
	}
	return nil
}

func (f *fakeRepo) SoftDeleteProduct(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return f.UpdateProductCols(ctx, tx, id, map[string]any{
		"deleted_at": gorm.DeletedAt{Time: database.Now().Time, Valid: true},
		"updated_by": database.ID(updatedBy),
	})
}

func (f *fakeRepo) DisableSkusOfProduct(_ context.Context, _ *gorm.DB, productID int64, updatedBy int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, s := range f.skus {
		if s.ProductID.Int64() == productID && s.IsEnabled && !deletedAtSet(s.DeletedAt) {
			s.IsEnabled = false
			s.UpdatedBy = database.ID(updatedBy)
			n++
		}
	}
	return n, nil
}

// ---- SKU 与条码 ----

func (f *fakeRepo) FindSKUByID(_ context.Context, id int64) (*SKU, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.skus[id]; ok && !deletedAtSet(s.DeletedAt) {
		sp := *s
		return &sp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindSKUByCode(_ context.Context, code string) (*SKU, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.skuByCode[code]; ok && !deletedAtSet(f.skus[id].DeletedAt) {
		sp := *f.skus[id]
		return &sp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListSkus(_ context.Context, fl SKUListFilter) ([]*SKU, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*SKU
	for _, s := range f.skus {
		if deletedAtSet(s.DeletedAt) {
			continue
		}
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(s.Code), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.ProductID > 0 && s.ProductID.Int64() != fl.ProductID {
			continue
		}
		if fl.Enabled != nil && s.IsEnabled != *fl.Enabled {
			continue
		}
		sp := *s
		out = append(out, &sp)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertSKU(_ context.Context, _ *gorm.DB, s *SKU) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, dup := f.skuByCode[s.Code]; dup && !deletedAtSet(f.skus[id].DeletedAt) {
		return response.NewError(ErrSKUCodeExists, map[string]any{"code": s.Code})
	}
	s.ID = database.ID(f.nextID())
	f.skus[s.ID.Int64()] = s
	f.skuByCode[s.Code] = s.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateSKUCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.skus[id]
	if !ok {
		return fmt.Errorf("SKU %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "product_id":
			s.ProductID = v.(database.ID)
		case "spec_attrs":
			s.SpecAttrs = v.(AttrMap)
		case "cost_price":
			s.CostPrice = v.(Number)
		case "sale_price":
			s.SalePrice = v.(Number)
		case "safety_stock":
			s.SafetyStock = v.(Number)
		case "max_stock":
			s.MaxStock = v.(Number)
		case "min_replenish_qty":
			s.MinReplenishQty = v.(Number)
		case "is_batch_managed":
			s.IsBatchManaged = v.(bool)
		case "is_expiry_managed":
			s.IsExpiryManaged = v.(bool)
		case "is_serial_managed":
			s.IsSerialManaged = v.(bool)
		case "is_enabled":
			s.IsEnabled = v.(bool)
		case "updated_by":
			s.UpdatedBy = v.(database.ID)
		case "deleted_at":
			s.DeletedAt = v.(gorm.DeletedAt)
		}
	}
	return nil
}

func (f *fakeRepo) SoftDeleteSKU(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return f.UpdateSKUCols(ctx, tx, id, map[string]any{
		"deleted_at": gorm.DeletedAt{Time: database.Now().Time, Valid: true},
		"updated_by": database.ID(updatedBy),
	})
}

func (f *fakeRepo) ListBarcodesBySKUs(_ context.Context, skuIDs []int64) ([]*Barcode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := map[int64]bool{}
	for _, id := range skuIDs {
		set[id] = true
	}
	var out []*Barcode
	for _, b := range f.barcodeIDs {
		if set[b.SKUID.Int64()] {
			bp := *b
			out = append(out, &bp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeRepo) FindBarcode(_ context.Context, code string) (*Barcode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.barcodes[code]; ok {
		bp := *f.barcodeIDs[id]
		return &bp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ReplaceBarcodes(_ context.Context, _ *gorm.DB, skuID int64, bcs []*Barcode, createdBy int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 先删（释放本 SKU 原占用）
	for id, b := range f.barcodeIDs {
		if b.SKUID.Int64() == skuID {
			delete(f.barcodes, b.Barcode)
			delete(f.barcodeIDs, id)
		}
	}
	// 后插（模拟 uk_barcodes_barcode 兜底：一码一 SKU）
	for _, b := range bcs {
		if otherID, dup := f.barcodes[b.Barcode]; dup && f.barcodeIDs[otherID].SKUID.Int64() != skuID {
			return response.NewError(ErrBarcodeExists, nil)
		}
		f.barcodeSeq++
		row := *b
		row.SKUID = database.ID(skuID)
		row.ID = database.ID(f.barcodeSeq)
		row.CreatedBy = database.ID(createdBy)
		row.UpdatedBy = database.ID(createdBy)
		f.barcodeIDs[row.ID.Int64()] = &row
		f.barcodes[row.Barcode] = row.ID.Int64()
	}
	return nil
}

// ---- 供应商 ----

func (f *fakeRepo) FindSupplierByID(_ context.Context, id int64) (*Supplier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.suppliers[id]; ok && !deletedAtSet(s.DeletedAt) {
		sp := *s
		return &sp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindSupplierByCode(_ context.Context, code string) (*Supplier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.supByCode[code]; ok && !deletedAtSet(f.suppliers[id].DeletedAt) {
		sp := *f.suppliers[id]
		return &sp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListSuppliers(_ context.Context, fl PartnerListFilter) ([]*Supplier, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Supplier
	for _, s := range f.suppliers {
		if deletedAtSet(s.DeletedAt) {
			continue
		}
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(s.Code), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(s.Name), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.Status != "" && s.Status != fl.Status {
			continue
		}
		sp := *s
		out = append(out, &sp)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertSupplier(_ context.Context, _ *gorm.DB, s *Supplier) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, dup := f.supByCode[s.Code]; dup && !deletedAtSet(f.suppliers[id].DeletedAt) {
		return response.NewError(ErrSupplierCodeExists, map[string]any{"code": s.Code})
	}
	s.ID = database.ID(f.nextID())
	f.suppliers[s.ID.Int64()] = s
	f.supByCode[s.Code] = s.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateSupplierCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.suppliers[id]
	if !ok {
		return fmt.Errorf("供应商 %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			s.Name = v.(string)
		case "contact":
			s.Contact = v.(string)
		case "phone":
			s.Phone = v.(string)
		case "email":
			s.Email = v.(string)
		case "address":
			s.Address = v.(string)
		case "status":
			s.Status = v.(string)
		case "remark":
			s.Remark = v.(string)
		case "updated_by":
			s.UpdatedBy = v.(database.ID)
		case "deleted_at":
			s.DeletedAt = v.(gorm.DeletedAt)
		}
	}
	return nil
}

func (f *fakeRepo) SoftDeleteSupplier(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return f.UpdateSupplierCols(ctx, tx, id, map[string]any{
		"deleted_at": gorm.DeletedAt{Time: database.Now().Time, Valid: true},
		"updated_by": database.ID(updatedBy),
	})
}

// ---- 客户 ----

func (f *fakeRepo) FindCustomerByID(_ context.Context, id int64) (*Customer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.customers[id]; ok && !deletedAtSet(s.DeletedAt) {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindCustomerByCode(_ context.Context, code string) (*Customer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.cusByCode[code]; ok && !deletedAtSet(f.customers[id].DeletedAt) {
		cp := *f.customers[id]
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListCustomers(_ context.Context, fl PartnerListFilter) ([]*Customer, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Customer
	for _, s := range f.customers {
		if deletedAtSet(s.DeletedAt) {
			continue
		}
		if fl.Keyword != "" && !strings.Contains(strings.ToLower(s.Code), strings.ToLower(fl.Keyword)) &&
			!strings.Contains(strings.ToLower(s.Name), strings.ToLower(fl.Keyword)) {
			continue
		}
		if fl.Status != "" && s.Status != fl.Status {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	total := int64(len(out))
	return pageItems(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertCustomer(_ context.Context, _ *gorm.DB, s *Customer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, dup := f.cusByCode[s.Code]; dup && !deletedAtSet(f.customers[id].DeletedAt) {
		return response.NewError(ErrCustomerCodeExists, map[string]any{"code": s.Code})
	}
	s.ID = database.ID(f.nextID())
	f.customers[s.ID.Int64()] = s
	f.cusByCode[s.Code] = s.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateCustomerCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.customers[id]
	if !ok {
		return fmt.Errorf("客户 %d 不存在", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			s.Name = v.(string)
		case "contact":
			s.Contact = v.(string)
		case "phone":
			s.Phone = v.(string)
		case "email":
			s.Email = v.(string)
		case "address":
			s.Address = v.(string)
		case "shipping_address":
			s.ShippingAddress = v.(string)
		case "status":
			s.Status = v.(string)
		case "updated_by":
			s.UpdatedBy = v.(database.ID)
		case "deleted_at":
			s.DeletedAt = v.(gorm.DeletedAt)
		}
	}
	return nil
}

func (f *fakeRepo) SoftDeleteCustomer(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return f.UpdateCustomerCols(ctx, tx, id, map[string]any{
		"deleted_at": gorm.DeletedAt{Time: database.Now().Time, Valid: true},
		"updated_by": database.ID(updatedBy),
	})
}

// ptrIDOf cols 值 → *database.ID（nil 合法：置空外键）。
func ptrIDOf(v any) *database.ID {
	if v == nil {
		return nil
	}
	if p, ok := v.(*database.ID); ok {
		cp := *p
		return &cp
	}
	return nil
}
