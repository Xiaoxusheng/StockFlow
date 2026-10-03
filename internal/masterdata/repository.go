package masterdata

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// Repository 消费方窄接口（backend-m1-plan §4.3 精神）：Service 只依赖本接口，
// 单元测试以内存替身实现（ask 约束：不依赖 PostgreSQL）。GORM 实现在本文件。
// 约束：repository 无业务判断（architecture.md §1），只做数据访问。
//
// 软删除语义：products/skus/suppliers/customers 模型含 gorm.DeletedAt，GORM 默认
// 作用域自动追加 deleted_at IS NULL（database.md §5.1）；分类/单位/条码无软删除。
type Repository interface {
	DB() *gorm.DB

	// —— 商品分类 ——
	FindCategoryByID(ctx context.Context, id int64) (*ProductCategory, error) // 未命中 (nil, nil)
	FindCategoryByCode(ctx context.Context, code string) (*ProductCategory, error)
	ListCategories(ctx context.Context, f CategoryListFilter) ([]*ProductCategory, int64, error)
	InsertCategory(ctx context.Context, tx *gorm.DB, c *ProductCategory) error
	UpdateCategoryCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	CountEnabledChildCategories(ctx context.Context, parentID int64) (int64, error)

	// —— 计量单位 ——
	FindUnitByID(ctx context.Context, id int64) (*Unit, error)
	FindUnitByCode(ctx context.Context, code string) (*Unit, error)
	ListUnits(ctx context.Context, f UnitListFilter) ([]*Unit, int64, error)
	InsertUnit(ctx context.Context, tx *gorm.DB, u *Unit) error
	UpdateUnitCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error

	// —— 商品 / SKU 引用计数（停用/删除被引用校验的数据面）——
	CountProductsByCategory(ctx context.Context, categoryID int64, onlyEnabled bool) (int64, error)
	CountProductsByUnit(ctx context.Context, unitID int64) (int64, error)
	CountSkusByProduct(ctx context.Context, productID int64) (int64, error)

	// —— 商品 ——
	FindProductByID(ctx context.Context, id int64) (*Product, error)
	FindProductByCode(ctx context.Context, code string) (*Product, error)
	ListProducts(ctx context.Context, f ProductListFilter) ([]*Product, int64, error)
	InsertProduct(ctx context.Context, tx *gorm.DB, p *Product) error
	UpdateProductCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteProduct(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error
	DisableSkusOfProduct(ctx context.Context, tx *gorm.DB, productID int64, updatedBy int64) (int64, error)

	// —— SKU 与条码 ——
	FindSKUByID(ctx context.Context, id int64) (*SKU, error)
	FindSKUByCode(ctx context.Context, code string) (*SKU, error)
	ListSkus(ctx context.Context, f SKUListFilter) ([]*SKU, int64, error)
	InsertSKU(ctx context.Context, tx *gorm.DB, s *SKU) error
	UpdateSKUCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteSKU(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error
	ListBarcodesBySKUs(ctx context.Context, skuIDs []int64) ([]*Barcode, error) // 批量装配，防 N+1（architecture.md §7）
	FindBarcode(ctx context.Context, barcode string) (*Barcode, error)
	ReplaceBarcodes(ctx context.Context, tx *gorm.DB, skuID int64, bcs []*Barcode, createdBy int64) error // 全量替换：先删后插

	// —— 供应商 ——
	FindSupplierByID(ctx context.Context, id int64) (*Supplier, error)
	FindSupplierByCode(ctx context.Context, code string) (*Supplier, error)
	ListSuppliers(ctx context.Context, f PartnerListFilter) ([]*Supplier, int64, error)
	InsertSupplier(ctx context.Context, tx *gorm.DB, s *Supplier) error
	UpdateSupplierCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteSupplier(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error

	// —— 客户 ——
	FindCustomerByID(ctx context.Context, id int64) (*Customer, error)
	FindCustomerByCode(ctx context.Context, code string) (*Customer, error)
	ListCustomers(ctx context.Context, f PartnerListFilter) ([]*Customer, int64, error)
	InsertCustomer(ctx context.Context, tx *gorm.DB, s *Customer) error
	UpdateCustomerCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteCustomer(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error
}

// repo GORM 实现。
type repo struct {
	db *gorm.DB
}

// NewRepository 构建 GORM Repository。
func NewRepository(db *gorm.DB) Repository { return &repo{db: db} }

func (r *repo) DB() *gorm.DB { return r.db }

// withCtx 统一挂载 context（超时/取消贯穿，go-dev-standard api-and-data 规范）。
func withCtx(ctx context.Context, q *gorm.DB) *gorm.DB { return q.WithContext(ctx) }

// likeEscape 转义 ILIKE 通配符，防止用户输入 %/_ 扰动匹配范围。
func likeEscape(kw string) string {
	kw = strings.TrimSpace(kw)
	kw = strings.ReplaceAll(kw, "\\", "\\\\")
	kw = strings.ReplaceAll(kw, "%", "\\%")
	kw = strings.ReplaceAll(kw, "_", "\\_")
	return kw
}

// isUniqueViolation 识别 PostgreSQL 唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// paginate 追加稳定排序与分页（architecture.md §7：禁无条件全量查询，列表强制分页）。
func paginate[T any](q *gorm.DB, page, pageSize int) ([]*T, int64, error) {
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*T, 0, pageSize)
	err := q.Order("id ASC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error
	return items, total, err
}

// ---- 商品分类 ----

func (r *repo) FindCategoryByID(ctx context.Context, id int64) (*ProductCategory, error) {
	var c ProductCategory
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&c).Error
	return firstOrNil(&c, err, "查询商品分类")
}

func (r *repo) FindCategoryByCode(ctx context.Context, code string) (*ProductCategory, error) {
	var c ProductCategory
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&c).Error
	return firstOrNil(&c, err, "按编码查询商品分类")
}

// CategoryListFilter 分类列表筛选（分页强制）。
type CategoryListFilter struct {
	Keyword  string // code/name 模糊匹配
	ParentID *int64 // 指定上级
	RootOnly bool   // 仅顶级（parent_id IS NULL）
	Status   string // ENABLED/DISABLED，空为全部
	Page     int
	PageSize int
}

func (r *repo) ListCategories(ctx context.Context, f CategoryListFilter) ([]*ProductCategory, int64, error) {
	q := withCtx(ctx, r.db).Model(&ProductCategory{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.RootOnly {
		q = q.Where("parent_id IS NULL")
	} else if f.ParentID != nil {
		q = q.Where("parent_id = ?", *f.ParentID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	items, total, err := paginate[ProductCategory](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询商品分类列表")
}

func (r *repo) InsertCategory(ctx context.Context, tx *gorm.DB, c *ProductCategory) error {
	if err := withCtx(ctx, tx).Create(c).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrCategoryCodeExists, map[string]any{"code": c.Code})
		}
		return fmt.Errorf("创建商品分类失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateCategoryCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&ProductCategory{}), id, cols, "更新商品分类")
}

func (r *repo) CountEnabledChildCategories(ctx context.Context, parentID int64) (int64, error) {
	return count(withCtx(ctx, r.db).Model(&ProductCategory{}).Where("parent_id = ? AND status = ?", parentID, StatusEnabled),
		"统计启用中的子分类")
}

// ---- 计量单位 ----

func (r *repo) FindUnitByID(ctx context.Context, id int64) (*Unit, error) {
	var u Unit
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&u).Error
	return firstOrNil(&u, err, "查询计量单位")
}

func (r *repo) FindUnitByCode(ctx context.Context, code string) (*Unit, error) {
	var u Unit
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&u).Error
	return firstOrNil(&u, err, "按编码查询计量单位")
}

// UnitListFilter 单位列表筛选。
type UnitListFilter struct {
	Keyword  string // code/name 模糊匹配
	Status   string
	Page     int
	PageSize int
}

func (r *repo) ListUnits(ctx context.Context, f UnitListFilter) ([]*Unit, int64, error) {
	q := withCtx(ctx, r.db).Model(&Unit{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	items, total, err := paginate[Unit](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询计量单位列表")
}

func (r *repo) InsertUnit(ctx context.Context, tx *gorm.DB, u *Unit) error {
	if err := withCtx(ctx, tx).Create(u).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrUnitCodeExists, map[string]any{"code": u.Code})
		}
		return fmt.Errorf("创建计量单位失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateUnitCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&Unit{}), id, cols, "更新计量单位")
}

// ---- 引用计数 ----

func (r *repo) CountProductsByCategory(ctx context.Context, categoryID int64, onlyEnabled bool) (int64, error) {
	q := withCtx(ctx, r.db).Model(&Product{}).Where("category_id = ?", categoryID)
	if onlyEnabled {
		q = q.Where("status = ?", StatusEnabled)
	}
	return count(q, "统计分类被商品引用")
}

func (r *repo) CountProductsByUnit(ctx context.Context, unitID int64) (int64, error) {
	return count(withCtx(ctx, r.db).Model(&Product{}).Where("unit_id = ?", unitID), "统计单位被商品引用")
}

func (r *repo) CountSkusByProduct(ctx context.Context, productID int64) (int64, error) {
	return count(withCtx(ctx, r.db).Model(&SKU{}).Where("product_id = ?", productID), "统计商品下 SKU")
}

// ---- 商品 ----

func (r *repo) FindProductByID(ctx context.Context, id int64) (*Product, error) {
	var p Product
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&p).Error
	return firstOrNil(&p, err, "查询商品")
}

func (r *repo) FindProductByCode(ctx context.Context, code string) (*Product, error) {
	var p Product
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&p).Error
	return firstOrNil(&p, err, "按编码查询商品")
}

// ProductListFilter 商品列表筛选。
type ProductListFilter struct {
	Keyword    string // code/name/short_name 模糊匹配
	CategoryID int64  // 0 为全部
	Status     string
	Page       int
	PageSize   int
}

func (r *repo) ListProducts(ctx context.Context, f ProductListFilter) ([]*Product, int64, error) {
	q := withCtx(ctx, r.db).Model(&Product{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ? OR short_name ILIKE ?", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
	}
	if f.CategoryID > 0 {
		q = q.Where("category_id = ?", f.CategoryID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	items, total, err := paginate[Product](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询商品列表")
}

func (r *repo) InsertProduct(ctx context.Context, tx *gorm.DB, p *Product) error {
	if err := withCtx(ctx, tx).Create(p).Error; err != nil {
		if isUniqueViolation(err) {
			// uk_products_code 部分唯一索引（WHERE deleted_at IS NULL）兜底；
			// 竞态窗口内的重复插入在此显式转为业务冲突。
			return response.NewError(ErrProductCodeExists, map[string]any{"code": p.Code})
		}
		return fmt.Errorf("创建商品失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateProductCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&Product{}), id, cols, "更新商品")
}

// SoftDeleteProduct 软删除（database.md §5.1）：deleted_at + updated_by 同语句落定。
func (r *repo) SoftDeleteProduct(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return softDelete(withCtx(ctx, tx).Model(&Product{}), id, updatedBy, "删除商品")
}

// DisableSkusOfProduct 商品停用级联停用其启用中的 SKU（同事务，architecture.md §4）。
// 返回受影响行数供审计记录级联数量。
func (r *repo) DisableSkusOfProduct(ctx context.Context, tx *gorm.DB, productID int64, updatedBy int64) (int64, error) {
	q := withCtx(ctx, tx).Model(&SKU{}).
		Where("product_id = ? AND is_enabled = ?", productID, true).
		Updates(map[string]any{"is_enabled": false, "updated_by": database.ID(updatedBy)})
	if q.Error != nil {
		return 0, fmt.Errorf("级联停用商品 SKU 失败: %w", q.Error)
	}
	return q.RowsAffected, nil
}

// ---- SKU 与条码 ----

func (r *repo) FindSKUByID(ctx context.Context, id int64) (*SKU, error) {
	var s SKU
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&s).Error
	return firstOrNil(&s, err, "查询 SKU")
}

func (r *repo) FindSKUByCode(ctx context.Context, code string) (*SKU, error) {
	var s SKU
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&s).Error
	return firstOrNil(&s, err, "按编码查询 SKU")
}

// SKUListFilter SKU 列表筛选。
type SKUListFilter struct {
	Keyword   string // code 模糊匹配
	ProductID int64  // 0 为全部
	Enabled   *bool  // nil 为全部
	Page      int
	PageSize  int
}

func (r *repo) ListSkus(ctx context.Context, f SKUListFilter) ([]*SKU, int64, error) {
	q := withCtx(ctx, r.db).Model(&SKU{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ?", "%"+kw+"%")
	}
	if f.ProductID > 0 {
		q = q.Where("product_id = ?", f.ProductID)
	}
	if f.Enabled != nil {
		q = q.Where("is_enabled = ?", *f.Enabled)
	}
	items, total, err := paginate[SKU](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询 SKU 列表")
}

func (r *repo) InsertSKU(ctx context.Context, tx *gorm.DB, s *SKU) error {
	if err := withCtx(ctx, tx).Create(s).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrSKUCodeExists, map[string]any{"code": s.Code})
		}
		return fmt.Errorf("创建 SKU 失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateSKUCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&SKU{}), id, cols, "更新 SKU")
}

func (r *repo) SoftDeleteSKU(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return softDelete(withCtx(ctx, tx).Model(&SKU{}), id, updatedBy, "删除 SKU")
}

func (r *repo) ListBarcodesBySKUs(ctx context.Context, skuIDs []int64) ([]*Barcode, error) {
	if len(skuIDs) == 0 {
		return nil, nil
	}
	var bcs []*Barcode
	err := withCtx(ctx, r.db).Where("sku_id IN ?", skuIDs).Order("id ASC").Find(&bcs).Error
	if err != nil {
		return nil, fmt.Errorf("查询 SKU 条码失败: %w", err)
	}
	return bcs, nil
}

func (r *repo) FindBarcode(ctx context.Context, barcode string) (*Barcode, error) {
	var b Barcode
	err := withCtx(ctx, r.db).Where("barcode = ?", barcode).First(&b).Error
	return firstOrNil(&b, err, "查询条码")
}

// ReplaceBarcodes 全量替换 SKU 条码（同事务先删后插；barcodes 无软删除，解绑即物理移除）。
// uk_barcodes_barcode 兜底：竞态窗口内的重复插入显式转为业务冲突。
func (r *repo) ReplaceBarcodes(ctx context.Context, tx *gorm.DB, skuID int64, bcs []*Barcode, createdBy int64) error {
	if err := withCtx(ctx, tx).Where("sku_id = ?", skuID).Delete(&Barcode{}).Error; err != nil {
		return fmt.Errorf("清理 SKU %d 原条码失败: %w", skuID, err)
	}
	if len(bcs) == 0 {
		return nil
	}
	for _, b := range bcs {
		b.SKUID = database.ID(skuID)
		b.CreatedBy = database.ID(createdBy)
		b.UpdatedBy = database.ID(createdBy)
	}
	if err := withCtx(ctx, tx).Create(&bcs).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrBarcodeExists, nil)
		}
		return fmt.Errorf("写入 SKU %d 条码失败: %w", skuID, err)
	}
	return nil
}

// ---- 供应商 ----

func (r *repo) FindSupplierByID(ctx context.Context, id int64) (*Supplier, error) {
	var s Supplier
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&s).Error
	return firstOrNil(&s, err, "查询供应商")
}

func (r *repo) FindSupplierByCode(ctx context.Context, code string) (*Supplier, error) {
	var s Supplier
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&s).Error
	return firstOrNil(&s, err, "按编码查询供应商")
}

// PartnerListFilter 供应商/客户共用列表筛选。
type PartnerListFilter struct {
	Keyword  string // code/name 模糊匹配
	Status   string
	Page     int
	PageSize int
}

func (r *repo) ListSuppliers(ctx context.Context, f PartnerListFilter) ([]*Supplier, int64, error) {
	q := withCtx(ctx, r.db).Model(&Supplier{})
	q = applyPartnerFilter(q, f.Keyword, f.Status)
	items, total, err := paginate[Supplier](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询供应商列表")
}

func (r *repo) InsertSupplier(ctx context.Context, tx *gorm.DB, s *Supplier) error {
	if err := withCtx(ctx, tx).Create(s).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrSupplierCodeExists, map[string]any{"code": s.Code})
		}
		return fmt.Errorf("创建供应商失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateSupplierCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&Supplier{}), id, cols, "更新供应商")
}

func (r *repo) SoftDeleteSupplier(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return softDelete(withCtx(ctx, tx).Model(&Supplier{}), id, updatedBy, "删除供应商")
}

// ---- 客户 ----

func (r *repo) FindCustomerByID(ctx context.Context, id int64) (*Customer, error) {
	var s Customer
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&s).Error
	return firstOrNil(&s, err, "查询客户")
}

func (r *repo) FindCustomerByCode(ctx context.Context, code string) (*Customer, error) {
	var s Customer
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&s).Error
	return firstOrNil(&s, err, "按编码查询客户")
}

func (r *repo) ListCustomers(ctx context.Context, f PartnerListFilter) ([]*Customer, int64, error) {
	q := withCtx(ctx, r.db).Model(&Customer{})
	q = applyPartnerFilter(q, f.Keyword, f.Status)
	items, total, err := paginate[Customer](q, f.Page, f.PageSize)
	return items, total, wrapList(err, "查询客户列表")
}

func (r *repo) InsertCustomer(ctx context.Context, tx *gorm.DB, s *Customer) error {
	if err := withCtx(ctx, tx).Create(s).Error; err != nil {
		if isUniqueViolation(err) {
			return response.NewError(ErrCustomerCodeExists, map[string]any{"code": s.Code})
		}
		return fmt.Errorf("创建客户失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateCustomerCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return updateCols(withCtx(ctx, tx).Model(&Customer{}), id, cols, "更新客户")
}

func (r *repo) SoftDeleteCustomer(ctx context.Context, tx *gorm.DB, id int64, updatedBy int64) error {
	return softDelete(withCtx(ctx, tx).Model(&Customer{}), id, updatedBy, "删除客户")
}

// ---- 通用小件 ----

// applyPartnerFilter 供应商/客户共用条件（列结构同名）。
func applyPartnerFilter(q *gorm.DB, keyword, status string) *gorm.DB {
	if kw := likeEscape(keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	return q
}

func count(q *gorm.DB, what string) (int64, error) {
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("%s失败: %w", what, err)
	}
	return n, nil
}

func updateCols(q *gorm.DB, id int64, cols map[string]any, what string) error {
	if len(cols) == 0 {
		return nil
	}
	if err := q.Where("id = ?", id).Updates(cols).Error; err != nil {
		return fmt.Errorf("%s %d 失败: %w", what, id, err)
	}
	return nil
}

// softDelete 软删除统一形态：deleted_at + updated_by 同语句（数据库.md §5）。
func softDelete(q *gorm.DB, id int64, updatedBy int64, what string) error {
	err := q.Where("id = ?", id).Updates(map[string]any{
		"deleted_at": database.Now(),
		"updated_by": database.ID(updatedBy),
	}).Error
	if err != nil {
		return fmt.Errorf("%s %d 失败: %w", what, id, err)
	}
	return nil
}

// firstOrNil ErrRecordNotFound → (nil, nil)，其余错误包装上下文。
func firstOrNil[T any](row *T, err error, what string) (*T, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s失败: %w", what, err)
	}
	return row, nil
}

func wrapList(err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s失败: %w", what, err)
	}
	return nil
}
