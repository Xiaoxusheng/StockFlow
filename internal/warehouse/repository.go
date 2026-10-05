package warehouse

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// Repository 消费方窄接口：Service 只依赖本接口，单元测试以内存替身实现
// （ask 约束：不依赖 PostgreSQL）。GORM 实现在本文件。
// 约束（architecture.md §1）：repository 无业务判断，只做数据访问与参数化查询。
type Repository interface {
	DB() *gorm.DB

	// —— warehouses（软删除对象，读取自动排除 deleted_at 非空行）——
	FindWarehouseByID(ctx context.Context, id int64) (*Warehouse, error)      // 未命中 (nil, nil)
	FindWarehouseByCode(ctx context.Context, code string) (*Warehouse, error) // 未删除行内检索
	ListWarehouses(ctx context.Context, f WarehouseListFilter) ([]*Warehouse, int64, error)
	InsertWarehouse(ctx context.Context, tx *gorm.DB, w *Warehouse) error
	UpdateWarehouseCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteWarehouse(ctx context.Context, tx *gorm.DB, id int64) error
	CountZonesByWarehouse(ctx context.Context, warehouseID int64, onlyEnabled bool) (int64, error)

	// —— zones（无软删除，database.md §5.1）——
	FindZoneByID(ctx context.Context, id int64) (*Zone, error)
	FindZoneByCode(ctx context.Context, warehouseID int64, code string) (*Zone, error)
	ListZones(ctx context.Context, f ZoneListFilter) ([]*Zone, int64, error)
	ListZonesByWarehouse(ctx context.Context, warehouseID int64) ([]*Zone, error)
	InsertZone(ctx context.Context, tx *gorm.DB, z *Zone) error
	UpdateZoneCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	CountShelvesByZone(ctx context.Context, zoneID int64, onlyEnabled bool) (int64, error)

	// —— shelves（无软删除）——
	FindShelfByID(ctx context.Context, id int64) (*Shelf, error)
	FindShelfByCode(ctx context.Context, zoneID int64, code string) (*Shelf, error)
	ListShelves(ctx context.Context, f ShelfListFilter) ([]*Shelf, int64, error)
	ListShelvesByWarehouse(ctx context.Context, warehouseID int64) ([]*Shelf, error)
	InsertShelf(ctx context.Context, tx *gorm.DB, s *Shelf) error
	UpdateShelfCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	CountBinsByShelf(ctx context.Context, shelfID int64, onlyEnabled bool) (int64, error)

	// —— bins（软删除对象）——
	FindBinByID(ctx context.Context, id int64) (*Bin, error)
	// FindBinByCode 含软删除行（uk_bins_warehouse_code 为普通唯一索引、非部分索引，
	// 软删行仍占用编码——库位编码永久保留，避免历史库存/流水追溯混淆，000004 注释）。
	FindBinByCode(ctx context.Context, warehouseID int64, code string) (*Bin, error)
	ListBins(ctx context.Context, f BinListFilter) ([]*Bin, int64, error)
	// binStockAggregates / binStockFirstSkuNames 库位存量展示聚合（ListBins 装配；跨域 SQL 直连见实现注）
	binStockAggregates(ctx context.Context, binIDs []int64) (map[int64]binStockAggregateRow, error)
	binStockFirstSkuNames(ctx context.Context, binIDs []int64) (map[int64]string, error)
	ListBinsByWarehouse(ctx context.Context, warehouseID int64) ([]*Bin, error)
	InsertBin(ctx context.Context, tx *gorm.DB, b *Bin) error
	UpdateBinCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	SoftDeleteBin(ctx context.Context, tx *gorm.DB, id int64) error
}

// —— 列表过滤器（Service 层填充；scope 为数据权限仓库范围，permission.md §4）——

// Scope 数据权限仓库范围快照（handler 经 auth.WarehouseScope 取自用户会话，
// 禁止接受前端传参决定范围，permission.md §4）。
type Scope struct {
	All          bool
	WarehouseIDs []int64
}

// allows 判断某仓库是否在范围内（DEPARTMENT/SELF 等 M1 未落仓库行级的范围由
// auth.WarehouseScope 归一为空集，fail-closed 不可见）。
func (s Scope) allows(warehouseID int64) bool {
	if s.All {
		return true
	}
	for _, id := range s.WarehouseIDs {
		if id == warehouseID {
			return true
		}
	}
	return false
}

// WarehouseListFilter 仓库列表过滤。
type WarehouseListFilter struct {
	Keyword        string // code/name 模糊匹配
	Status         string // 空 = 全部
	Type           string // 空 = 全部
	Scope          Scope
	Page, PageSize int
}

// ZoneListFilter 库区列表过滤。
type ZoneListFilter struct {
	WarehouseID    int64 // 0 = 不限（仍受 Scope 过滤）
	Keyword        string
	ZoneType       string
	Status         string
	Scope          Scope
	Page, PageSize int
}

// ShelfListFilter 货架列表过滤。
type ShelfListFilter struct {
	WarehouseID    int64
	ZoneID         int64
	Keyword        string
	Status         string
	Scope          Scope
	Page, PageSize int
}

// BinListFilter 库位列表过滤。
type BinListFilter struct {
	WarehouseID    int64
	ZoneID         int64
	ShelfID        int64
	Keyword        string
	BinType        string
	Status         string
	Scope          Scope
	Page, PageSize int
}

// —— GORM 实现 ——

type gormRepo struct{ db *gorm.DB }

// newGormRepository 构造 GORM 仓储实现（RegisterRoutes 装配用）。
func newGormRepository(db *gorm.DB) Repository { return &gormRepo{db: db} }

// DB 返回句柄（Service 用 database.Tx 开事务）。
func (r *gormRepo) DB() *gorm.DB { return r.db }

func (r *gormRepo) FindWarehouseByID(ctx context.Context, id int64) (*Warehouse, error) {
	var w Warehouse
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&w).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &w, nil
}

// FindWarehouseByCode 未删除行内检索（uk_warehouses_code 为部分索引 WHERE deleted_at
// IS NULL，软删行不占用编码，可复用）。
func (r *gormRepo) FindWarehouseByCode(ctx context.Context, code string) (*Warehouse, error) {
	var w Warehouse
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&w).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &w, nil
}

func (r *gormRepo) ListWarehouses(ctx context.Context, f WarehouseListFilter) ([]*Warehouse, int64, error) {
	q := r.db.WithContext(ctx).Model(&Warehouse{})
	if f.Keyword != "" {
		like := likePattern(f.Keyword)
		q = q.Where("code ILIKE ? OR name ILIKE ?", like, like)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	q = applyScope(q, f.Scope, "id")
	total, err := countOf(q)
	if err != nil {
		return nil, 0, err
	}
	var rows []*Warehouse
	if err := q.Order("id ASC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *gormRepo) InsertWarehouse(ctx context.Context, tx *gorm.DB, w *Warehouse) error {
	return translateUnique(tx.WithContext(ctx).Create(w).Error)
}

func (r *gormRepo) UpdateWarehouseCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return tx.WithContext(ctx).Model(&Warehouse{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepo) SoftDeleteWarehouse(ctx context.Context, tx *gorm.DB, id int64) error {
	return tx.WithContext(ctx).Delete(&Warehouse{}, id).Error
}

func (r *gormRepo) CountZonesByWarehouse(ctx context.Context, warehouseID int64, onlyEnabled bool) (int64, error) {
	q := r.db.WithContext(ctx).Model(&Zone{}).Where("warehouse_id = ?", warehouseID)
	if onlyEnabled {
		q = q.Where("status = ?", StatusEnabled)
	}
	return countOf(q)
}

func (r *gormRepo) FindZoneByID(ctx context.Context, id int64) (*Zone, error) {
	var z Zone
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&z).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &z, nil
}

func (r *gormRepo) FindZoneByCode(ctx context.Context, warehouseID int64, code string) (*Zone, error) {
	var z Zone
	if err := r.db.WithContext(ctx).
		Where("warehouse_id = ? AND code = ?", warehouseID, code).
		First(&z).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &z, nil
}

func (r *gormRepo) ListZones(ctx context.Context, f ZoneListFilter) ([]*Zone, int64, error) {
	q := r.db.WithContext(ctx).Model(&Zone{})
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.Keyword != "" {
		like := likePattern(f.Keyword)
		q = q.Where("code ILIKE ? OR name ILIKE ?", like, like)
	}
	if f.ZoneType != "" {
		q = q.Where("zone_type = ?", f.ZoneType)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	q = applyScope(q, f.Scope, "warehouse_id")
	total, err := countOf(q)
	if err != nil {
		return nil, 0, err
	}
	var rows []*Zone
	if err := q.Order("warehouse_id ASC, code ASC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *gormRepo) ListZonesByWarehouse(ctx context.Context, warehouseID int64) ([]*Zone, error) {
	var rows []*Zone
	if err := r.db.WithContext(ctx).
		Where("warehouse_id = ?", warehouseID).
		Order("code ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *gormRepo) InsertZone(ctx context.Context, tx *gorm.DB, z *Zone) error {
	return translateUnique(tx.WithContext(ctx).Create(z).Error)
}

func (r *gormRepo) UpdateZoneCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return tx.WithContext(ctx).Model(&Zone{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepo) CountShelvesByZone(ctx context.Context, zoneID int64, onlyEnabled bool) (int64, error) {
	q := r.db.WithContext(ctx).Model(&Shelf{}).Where("zone_id = ?", zoneID)
	if onlyEnabled {
		q = q.Where("status = ?", StatusEnabled)
	}
	return countOf(q)
}

func (r *gormRepo) FindShelfByID(ctx context.Context, id int64) (*Shelf, error) {
	var s Shelf
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&s).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &s, nil
}

func (r *gormRepo) FindShelfByCode(ctx context.Context, zoneID int64, code string) (*Shelf, error) {
	var s Shelf
	if err := r.db.WithContext(ctx).
		Where("zone_id = ? AND code = ?", zoneID, code).
		First(&s).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &s, nil
}

func (r *gormRepo) ListShelves(ctx context.Context, f ShelfListFilter) ([]*Shelf, int64, error) {
	q := r.db.WithContext(ctx).Model(&Shelf{})
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.ZoneID > 0 {
		q = q.Where("zone_id = ?", f.ZoneID)
	}
	if f.Keyword != "" {
		// 货架无名称字段（business-flow §1.6），仅编码匹配
		q = q.Where("code ILIKE ?", likePattern(f.Keyword))
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	q = applyScope(q, f.Scope, "warehouse_id")
	total, err := countOf(q)
	if err != nil {
		return nil, 0, err
	}
	var rows []*Shelf
	if err := q.Order("zone_id ASC, code ASC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *gormRepo) ListShelvesByWarehouse(ctx context.Context, warehouseID int64) ([]*Shelf, error) {
	var rows []*Shelf
	if err := r.db.WithContext(ctx).
		Where("warehouse_id = ?", warehouseID).
		Order("code ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *gormRepo) InsertShelf(ctx context.Context, tx *gorm.DB, s *Shelf) error {
	return translateUnique(tx.WithContext(ctx).Create(s).Error)
}

func (r *gormRepo) UpdateShelfCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return tx.WithContext(ctx).Model(&Shelf{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepo) CountBinsByShelf(ctx context.Context, shelfID int64, onlyEnabled bool) (int64, error) {
	q := r.db.WithContext(ctx).Model(&Bin{}).Where("shelf_id = ?", shelfID)
	if onlyEnabled {
		q = q.Where("status = ?", StatusEnabled)
	}
	return countOf(q)
}

func (r *gormRepo) FindBinByID(ctx context.Context, id int64) (*Bin, error) {
	var b Bin
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&b).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &b, nil
}

// FindBinByCode 含软删除行（编码永久占用，见 Repository 接口注释）。
func (r *gormRepo) FindBinByCode(ctx context.Context, warehouseID int64, code string) (*Bin, error) {
	var b Bin
	if err := r.db.WithContext(ctx).Unscoped().
		Where("warehouse_id = ? AND code = ?", warehouseID, code).
		First(&b).Error; err != nil {
		return nil, ignoreNotFound(err)
	}
	return &b, nil
}

func (r *gormRepo) ListBins(ctx context.Context, f BinListFilter) ([]*Bin, int64, error) {
	q := r.db.WithContext(ctx).Model(&Bin{})
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.ZoneID > 0 {
		q = q.Where("zone_id = ?", f.ZoneID)
	}
	if f.ShelfID > 0 {
		q = q.Where("shelf_id = ?", f.ShelfID)
	}
	if f.Keyword != "" {
		like := likePattern(f.Keyword)
		q = q.Where("code ILIKE ?", like)
	}
	if f.BinType != "" {
		q = q.Where("bin_type = ?", f.BinType)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	q = applyScope(q, f.Scope, "warehouse_id")
	total, err := countOf(q)
	if err != nil {
		return nil, 0, err
	}
	var rows []*Bin
	if err := q.Order("warehouse_id ASC, zone_id ASC, shelf_id ASC, layer ASC, column_no ASC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// binStockAggregateRow 库位存量聚合行（ListBins 列表装配用）。
type binStockAggregateRow struct {
	BinID    int64   `gorm:"column:bin_id"`
	SkuCount int64   `gorm:"column:sku_count"`
	TotalQty float64 `gorm:"column:total_qty"`
}

// binStockAggregates 库位存量聚合（跨域零 import：SQL 直连 inventory/skus 表——
// reports/workbench.go 同口径；软删一致性：skus 内嵌 DeletedAt（迁移 000016）显式过滤，
// inventory 为 M1 表无软删。仅供列表页展示，不做业务判定）。
func (r *gormRepo) binStockAggregates(ctx context.Context, binIDs []int64) (map[int64]binStockAggregateRow, error) {
	if len(binIDs) == 0 {
		return map[int64]binStockAggregateRow{}, nil
	}
	var rows []binStockAggregateRow
	if err := r.db.WithContext(ctx).Raw(`
SELECT i.bin_id,
       COUNT(DISTINCT i.sku_id)::bigint AS sku_count,
       COALESCE(SUM(i.total_qty), 0)::float8 AS total_qty
FROM inventory i
WHERE i.bin_id IN ? AND i.total_qty > 0
GROUP BY i.bin_id`, binIDs).Scan(&rows).Error; err != nil {
		return nil, translateUnique(err)
	}
	result := make(map[int64]binStockAggregateRow, len(rows))
	for _, row := range rows {
		result[row.BinID] = row
	}
	return result, nil
}

// binStockFirstSkuNames 每库位存量最大的一个 SKU 名（display 用，DISTINCT ON 取行）。
func (r *gormRepo) binStockFirstSkuNames(ctx context.Context, binIDs []int64) (map[int64]string, error) {
	if len(binIDs) == 0 {
		return map[int64]string{}, nil
	}
	var rows []struct {
		BinID   int64  `gorm:"column:bin_id"`
		SkuName string `gorm:"column:sku_name"`
	}
	if err := r.db.WithContext(ctx).Raw(`
SELECT DISTINCT ON (i.bin_id) i.bin_id, COALESCE(p.name, s.code) AS sku_name
FROM inventory i
JOIN skus s ON s.id = i.sku_id AND s.deleted_at IS NULL
LEFT JOIN products p ON p.id = s.product_id AND p.deleted_at IS NULL
WHERE i.bin_id IN ? AND i.total_qty > 0
ORDER BY i.bin_id, i.total_qty DESC`, binIDs).Scan(&rows).Error; err != nil {
		return nil, translateUnique(err)
	}
	result := make(map[int64]string, len(rows))
	for _, row := range rows {
		result[row.BinID] = row.SkuName
	}
	return result, nil
}

func (r *gormRepo) ListBinsByWarehouse(ctx context.Context, warehouseID int64) ([]*Bin, error) {
	var rows []*Bin
	if err := r.db.WithContext(ctx).
		Where("warehouse_id = ?", warehouseID).
		Order("shelf_id ASC, layer ASC, column_no ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *gormRepo) InsertBin(ctx context.Context, tx *gorm.DB, b *Bin) error {
	return translateUnique(tx.WithContext(ctx).Create(b).Error)
}

func (r *gormRepo) UpdateBinCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return tx.WithContext(ctx).Model(&Bin{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepo) SoftDeleteBin(ctx context.Context, tx *gorm.DB, id int64) error {
	return tx.WithContext(ctx).Delete(&Bin{}, id).Error
}

// —— 查询助手 ——

// applyScope 注入数据权限仓库范围（permission.md §4：Service/Repository 层过滤，
// 语义与 auth.ApplyWarehouseScope 一致；column 为仓库 ID 列）。
func applyScope(q *gorm.DB, s Scope, column string) *gorm.DB {
	if s.All {
		return q
	}
	if len(s.WarehouseIDs) == 0 {
		return q.Where("1 = 0") // fail-closed：空范围不可见任何行
	}
	return q.Where(column+" IN ?", s.WarehouseIDs)
}

// countOf 计数（架构约束：分页必先取 total）。
func countOf(q *gorm.DB) (int64, error) {
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// likePattern 关键字模糊匹配模式（转义 LIKE 通配符，参数化查询之外的通配符注入防护）。
func likePattern(kw string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(kw) + "%"
}

// ignoreNotFound 未命中归一为 (nil, nil)（接口注释约定）。
func ignoreNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// translateUnique 唯一约束冲突（23505）翻译为域错误码（并发插入的兜底，竞态下
// Service 的先查后插存在窗口，最终以约束为准）。
func translateUnique(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "uk_warehouses_code":
			return response.NewError(ErrWarehouseCodeExists, nil)
		case "uk_zones_warehouse_code":
			return response.NewError(ErrZoneCodeExists, nil)
		case "uk_shelves_zone_code":
			return response.NewError(ErrShelfCodeExists, nil)
		case "uk_bins_warehouse_code":
			return response.NewError(ErrBinCodeExists, nil)
		default:
			return response.NewError(response.CodeConflict, nil)
		}
	}
	return err
}
