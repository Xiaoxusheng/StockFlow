package warehouse

// 测试替身：内存版 Repository（实现 Service 依赖的窄接口；行为模拟 000004 的
// 唯一索引与软删除语义，见各方法注释）。不依赖 PostgreSQL。

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

type fakeRepo struct {
	mu         sync.Mutex
	warehouses map[int64]*Warehouse
	zones      map[int64]*Zone
	shelves    map[int64]*Shelf
	bins       map[int64]*Bin
	whDeleted  map[int64]bool // 软删仓库（部分索引：编码不占用）
	binDeleted map[int64]bool // 软删库位（普通唯一索引：编码永久占用）
	nextID     int64
	db         *gorm.DB // database.Tx 需要的句柄（假方言器，事务 no-op 可运行）
}

func newFakeRepo() *fakeRepo {
	g, err := openTestGorm()
	if err != nil {
		panic(err)
	}
	return &fakeRepo{
		warehouses: map[int64]*Warehouse{},
		zones:      map[int64]*Zone{},
		shelves:    map[int64]*Shelf{},
		bins:       map[int64]*Bin{},
		whDeleted:  map[int64]bool{},
		binDeleted: map[int64]bool{},
		nextID:     100,
		db:         g,
	}
}

func (r *fakeRepo) DB() *gorm.DB { return r.db }

func (r *fakeRepo) id() int64 {
	r.nextID++
	return r.nextID
}

// stamp 填充通用字段（真实 GORM 由 autoCreateTime/钩子完成）。
func stamp(by int64) (database.JSONTime, database.ID, database.ID) {
	now := database.Now()
	return now, database.ID(by), database.ID(by)
}

// uniqueViolation 模拟 PostgreSQL 23505（与 gormRepo.translateUnique 同型错误）。
func uniqueViolation(c response.Code) error {
	return response.NewError(c, nil)
}

// —— warehouses ——

func (r *fakeRepo) FindWarehouseByID(_ context.Context, id int64) (*Warehouse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.whDeleted[id] {
		return nil, nil
	}
	if w, ok := r.warehouses[id]; ok {
		c := *w
		return &c, nil
	}
	return nil, nil
}

func (r *fakeRepo) FindWarehouseByCode(_ context.Context, code string) (*Warehouse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, w := range r.warehouses {
		if w.Code == code && !r.whDeleted[id] {
			c := *w
			return &c, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListWarehouses(_ context.Context, f WarehouseListFilter) ([]*Warehouse, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Warehouse
	for id, w := range r.warehouses {
		if r.whDeleted[id] {
			continue
		}
		if f.Keyword != "" && !strings.Contains(w.Code, f.Keyword) && !strings.Contains(w.Name, f.Keyword) {
			continue
		}
		if f.Status != "" && w.Status != f.Status {
			continue
		}
		if f.Type != "" && w.Type != f.Type {
			continue
		}
		if !f.Scope.allows(int64(w.ID)) {
			continue
		}
		c := *w
		rows = append(rows, &c)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return paginate(rows, f.Page, f.PageSize), int64(len(rows)), nil
}

func (r *fakeRepo) InsertWarehouse(_ context.Context, _ *gorm.DB, w *Warehouse) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, o := range r.warehouses {
		if o.Code == w.Code && !r.whDeleted[id] { // 部分索引：软删行不冲突
			return uniqueViolation(ErrWarehouseCodeExists)
		}
	}
	if w.ID == 0 {
		w.ID = database.ID(r.id())
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt, w.CreatedBy, w.UpdatedBy = stamp(int64(w.CreatedBy))
	}
	c := *w
	r.warehouses[int64(w.ID)] = &c
	return nil
}

func (r *fakeRepo) UpdateWarehouseCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.warehouses[id]
	if !ok {
		return fmt.Errorf("fake: warehouse %d not found", id)
	}
	applyCols(w, cols)
	return nil
}

func (r *fakeRepo) SoftDeleteWarehouse(_ context.Context, _ *gorm.DB, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.whDeleted[id] = true
	return nil
}

func (r *fakeRepo) CountZonesByWarehouse(_ context.Context, warehouseID int64, onlyEnabled bool) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, z := range r.zones {
		if int64(z.WarehouseID) == warehouseID && (!onlyEnabled || z.Status == StatusEnabled) {
			n++
		}
	}
	return n, nil
}

// —— zones ——

func (r *fakeRepo) FindZoneByID(_ context.Context, id int64) (*Zone, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if z, ok := r.zones[id]; ok {
		c := *z
		return &c, nil
	}
	return nil, nil
}

func (r *fakeRepo) FindZoneByCode(_ context.Context, warehouseID int64, code string) (*Zone, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, z := range r.zones {
		if int64(z.WarehouseID) == warehouseID && z.Code == code {
			c := *z
			return &c, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListZones(_ context.Context, f ZoneListFilter) ([]*Zone, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Zone
	for _, z := range r.zones {
		if f.WarehouseID > 0 && int64(z.WarehouseID) != f.WarehouseID {
			continue
		}
		if f.Keyword != "" && !strings.Contains(z.Code, f.Keyword) && !strings.Contains(z.Name, f.Keyword) {
			continue
		}
		if f.ZoneType != "" && z.ZoneType != f.ZoneType {
			continue
		}
		if f.Status != "" && z.Status != f.Status {
			continue
		}
		if !f.Scope.allows(int64(z.WarehouseID)) {
			continue
		}
		c := *z
		rows = append(rows, &c)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return paginate(rows, f.Page, f.PageSize), int64(len(rows)), nil
}

func (r *fakeRepo) ListZonesByWarehouse(_ context.Context, warehouseID int64) ([]*Zone, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Zone
	for _, z := range r.zones {
		if int64(z.WarehouseID) == warehouseID {
			c := *z
			rows = append(rows, &c)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
	return rows, nil
}

func (r *fakeRepo) InsertZone(_ context.Context, _ *gorm.DB, z *Zone) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.zones { // uk_zones_warehouse_code：全量索引（无软删）
		if int64(o.WarehouseID) == int64(z.WarehouseID) && o.Code == z.Code {
			return uniqueViolation(ErrZoneCodeExists)
		}
	}
	if z.ID == 0 {
		z.ID = database.ID(r.id())
	}
	if z.CreatedAt.IsZero() {
		z.CreatedAt, z.CreatedBy, z.UpdatedBy = stamp(int64(z.CreatedBy))
	}
	c := *z
	r.zones[int64(z.ID)] = &c
	return nil
}

func (r *fakeRepo) UpdateZoneCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	z, ok := r.zones[id]
	if !ok {
		return fmt.Errorf("fake: zone %d not found", id)
	}
	applyCols(z, cols)
	return nil
}

func (r *fakeRepo) CountShelvesByZone(_ context.Context, zoneID int64, onlyEnabled bool) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, s := range r.shelves {
		if int64(s.ZoneID) == zoneID && (!onlyEnabled || s.Status == StatusEnabled) {
			n++
		}
	}
	return n, nil
}

// —— shelves ——

func (r *fakeRepo) FindShelfByID(_ context.Context, id int64) (*Shelf, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.shelves[id]; ok {
		c := *s
		return &c, nil
	}
	return nil, nil
}

func (r *fakeRepo) FindShelfByCode(_ context.Context, zoneID int64, code string) (*Shelf, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.shelves {
		if int64(s.ZoneID) == zoneID && s.Code == code {
			c := *s
			return &c, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListShelves(_ context.Context, f ShelfListFilter) ([]*Shelf, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Shelf
	for _, s := range r.shelves {
		if f.WarehouseID > 0 && int64(s.WarehouseID) != f.WarehouseID {
			continue
		}
		if f.ZoneID > 0 && int64(s.ZoneID) != f.ZoneID {
			continue
		}
		if f.Keyword != "" && !strings.Contains(s.Code, f.Keyword) {
			continue
		}
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		if !f.Scope.allows(int64(s.WarehouseID)) {
			continue
		}
		c := *s
		rows = append(rows, &c)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return paginate(rows, f.Page, f.PageSize), int64(len(rows)), nil
}

func (r *fakeRepo) ListShelvesByWarehouse(_ context.Context, warehouseID int64) ([]*Shelf, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Shelf
	for _, s := range r.shelves {
		if int64(s.WarehouseID) == warehouseID {
			c := *s
			rows = append(rows, &c)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
	return rows, nil
}

func (r *fakeRepo) InsertShelf(_ context.Context, _ *gorm.DB, s *Shelf) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.shelves { // uk_shelves_zone_code：库区内唯一
		if int64(o.ZoneID) == int64(s.ZoneID) && o.Code == s.Code {
			return uniqueViolation(ErrShelfCodeExists)
		}
	}
	if s.ID == 0 {
		s.ID = database.ID(r.id())
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt, s.CreatedBy, s.UpdatedBy = stamp(int64(s.CreatedBy))
	}
	c := *s
	r.shelves[int64(s.ID)] = &c
	return nil
}

func (r *fakeRepo) UpdateShelfCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.shelves[id]
	if !ok {
		return fmt.Errorf("fake: shelf %d not found", id)
	}
	applyCols(s, cols)
	return nil
}

func (r *fakeRepo) CountBinsByShelf(_ context.Context, shelfID int64, onlyEnabled bool) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for id, b := range r.bins {
		if r.binDeleted[id] {
			continue
		}
		if int64(b.ShelfID) == shelfID && (!onlyEnabled || b.Status == StatusEnabled) {
			n++
		}
	}
	return n, nil
}

// —— bins ——

func (r *fakeRepo) FindBinByID(_ context.Context, id int64) (*Bin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binDeleted[id] {
		return nil, nil
	}
	if b, ok := r.bins[id]; ok {
		c := *b
		return &c, nil
	}
	return nil, nil
}

// FindBinByCode 含软删行（uk_bins_warehouse_code 普通唯一索引：编码永久占用）。
func (r *fakeRepo) FindBinByCode(_ context.Context, warehouseID int64, code string) (*Bin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.bins {
		if int64(b.WarehouseID) == warehouseID && b.Code == code {
			c := *b
			return &c, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListBins(_ context.Context, f BinListFilter) ([]*Bin, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Bin
	for id, b := range r.bins {
		if r.binDeleted[id] {
			continue
		}
		if f.WarehouseID > 0 && int64(b.WarehouseID) != f.WarehouseID {
			continue
		}
		if f.ZoneID > 0 && int64(b.ZoneID) != f.ZoneID {
			continue
		}
		if f.ShelfID > 0 && int64(b.ShelfID) != f.ShelfID {
			continue
		}
		if f.Keyword != "" && !strings.Contains(b.Code, f.Keyword) {
			continue
		}
		if f.BinType != "" && b.BinType != f.BinType {
			continue
		}
		if f.Status != "" && b.Status != f.Status {
			continue
		}
		if !f.Scope.allows(int64(b.WarehouseID)) {
			continue
		}
		c := *b
		rows = append(rows, &c)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return paginate(rows, f.Page, f.PageSize), int64(len(rows)), nil
}

func (r *fakeRepo) ListBinsByWarehouse(_ context.Context, warehouseID int64) ([]*Bin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*Bin
	for id, b := range r.bins {
		if r.binDeleted[id] {
			continue
		}
		if int64(b.WarehouseID) == warehouseID {
			c := *b
			rows = append(rows, &c)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Layer != rows[j].Layer {
			return rows[i].Layer < rows[j].Layer
		}
		return rows[i].ColumnNo < rows[j].ColumnNo
	})
	return rows, nil
}

func (r *fakeRepo) InsertBin(_ context.Context, _ *gorm.DB, b *Bin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.bins { // uk_bins_warehouse_code：全量索引（软删行仍占用）
		if int64(o.WarehouseID) == int64(b.WarehouseID) && o.Code == b.Code {
			return uniqueViolation(ErrBinCodeExists)
		}
	}
	if b.ID == 0 {
		b.ID = database.ID(r.id())
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt, b.CreatedBy, b.UpdatedBy = stamp(int64(b.CreatedBy))
	}
	c := *b
	r.bins[int64(b.ID)] = &c
	return nil
}

func (r *fakeRepo) UpdateBinCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.bins[id]
	if !ok {
		return fmt.Errorf("fake: bin %d not found", id)
	}
	applyCols(b, cols)
	return nil
}

func (r *fakeRepo) SoftDeleteBin(_ context.Context, _ *gorm.DB, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.binDeleted[id] = true
	return nil
}

// —— 测试辅助 ——

func paginate[T any](rows []T, page, pageSize int) []T {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = response.DefaultPageSize
	}
	start := (page - 1) * pageSize
	if start >= len(rows) {
		return []T{}
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

// applyCols 把列名 → 值映射应用到实体（反射实现，fake 专用；列名即 GORM 列名）。
func applyCols(target any, cols map[string]any) {
	applyColsReflect(reflect.ValueOf(target).Elem(), cols)
}

func applyColsReflect(v reflect.Value, cols map[string]any) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			applyColsReflect(v.Field(i), cols) // 嵌入通用字段（BaseModel/baseCols）
			continue
		}
		for col, val := range cols {
			if strings.EqualFold(f.Name, colFieldName(col)) && v.Field(i).CanSet() {
				setTyped(v.Field(i), val)
			}
		}
	}
}

// colFieldName 列名 → Go 字段名近似（max_capacity → MaxCapacity）。
func colFieldName(col string) string {
	name := ""
	for _, p := range strings.Split(col, "_") {
		name += strings.ToUpper(p[:1]) + p[1:]
	}
	return name
}

func setTyped(dst reflect.Value, val any) {
	if val == nil {
		return
	}
	rv := reflect.ValueOf(val)
	switch {
	case rv.Type().AssignableTo(dst.Type()):
		dst.Set(rv)
	case rv.Type().ConvertibleTo(dst.Type()):
		dst.Set(rv.Convert(dst.Type()))
	}
}

// binStockAggregates / binStockFirstSkuNames 测试桩：默认空聚合（存量展示列为零值，
// 不影响既有断言；需要断言存量展示时请在用例内替换实现）。
func (r *fakeRepo) binStockAggregates(ctx context.Context, binIDs []int64) (map[int64]binStockAggregateRow, error) {
	return map[int64]binStockAggregateRow{}, nil
}

func (r *fakeRepo) binStockFirstSkuNames(ctx context.Context, binIDs []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}
