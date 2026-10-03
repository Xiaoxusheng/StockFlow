package warehouse

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 库区/货架/库位 Service（business-flow §1.6 第二至四级）。
//
// 层级约束（ask："仓库-库区-货架-库位层级约束"）：
//   - 层级锚点（zone 的 warehouse_id、shelf 的 zone_id/warehouse_id、bin 的
//     shelf_id/zone_id/warehouse_id）创建后不可变更——变更层级等于移库业务，M1 不提供；
//   - 创建子级时上级必须存在且启用（api.md §4 业务关系 + 状态守卫）；
//   - 删除时子级校验：仓库删除要求零库区；库位软删除要求零占用容量；
//     库区/货架无删除通路（plan §5.4.1），停用时要求无启用中的子级。
//
// 编码唯一域（000004 唯一索引同源）：
//   - 仓库：全局（未删除行，部分索引，软删编码可复用）；
//   - 库区：仓库内；货架：库区内；库位：仓库内（普通唯一索引，软删行仍占用编码）。

// CreateZone 创建库区。
func (s *Service) CreateZone(ctx context.Context, actor Actor, in ZoneCreateInput) (*ZoneView, error) {
	code, err := validateCode("code", in.Code)
	if err != nil {
		return nil, err
	}
	name, err := validateName("name", in.Name)
	if err != nil {
		return nil, err
	}
	zoneType := ZoneTypeStorage
	if in.ZoneType != "" {
		if zoneType, err = validateTypeCode("zone_type", in.ZoneType); err != nil {
			return nil, err
		}
	}
	capacity := 0.0
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		capacity = *in.Capacity
	}

	wh, err := s.loadWarehouseForChild(ctx, actor, in.WarehouseID)
	if err != nil {
		return nil, err
	}
	if exist, err := s.repo.FindZoneByCode(ctx, in.WarehouseID, code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrZoneCodeExists, map[string]any{"code": code, "warehouse_id": in.WarehouseID})
	}

	z := &Zone{
		WarehouseID: wh.ID,
		Code:        code,
		Name:        name,
		ZoneType:    zoneType,
		Capacity:    capacity,
		Status:      StatusEnabled,
	}
	z.CreatedBy = database.ID(actor.UserID)
	z.UpdatedBy = database.ID(actor.UserID)

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertZone(ctx, tx, z); err != nil {
			return err
		}
		e := actor.auditEntry("zone", idOf(z.ID), "create")
		e.Success = true
		e.After = viewZone(z)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewZone(z)
	return &v, nil
}

// UpdateZone 更新库区（warehouse_id 不可变更）。
func (s *Service) UpdateZone(ctx context.Context, actor Actor, id int64, in ZoneUpdateInput) (*ZoneView, error) {
	z, err := s.repo.FindZoneByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, response.NewError(ErrZoneNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(z.WarehouseID)); err != nil {
		return nil, err
	}

	cols := map[string]any{}
	if in.Code != nil {
		code, err := validateCode("code", *in.Code)
		if err != nil {
			return nil, err
		}
		if exist, err := s.repo.FindZoneByCode(ctx, idOf(z.WarehouseID), code); err != nil {
			return nil, err
		} else if exist != nil && idOf(exist.ID) != id {
			return nil, response.NewError(ErrZoneCodeExists, map[string]any{"code": code})
		}
		cols["code"] = code
	}
	if in.Name != nil {
		name, err := validateName("name", *in.Name)
		if err != nil {
			return nil, err
		}
		cols["name"] = name
	}
	if in.ZoneType != nil {
		t, err := validateTypeCode("zone_type", *in.ZoneType)
		if err != nil {
			return nil, err
		}
		cols["zone_type"] = t
	}
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		cols["capacity"] = *in.Capacity
	}
	if len(cols) == 0 {
		return nil, invalidParam("body", "未提供任何可更新字段")
	}
	cols["updated_by"] = actor.UserID

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateZoneCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("zone", id, "update")
		e.Success = true
		e.Before = viewZone(z)
		e.After = viewZone(applyZoneCols(z, cols))
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.GetZone(ctx, actor.Scope, id)
}

// GetZone 库区详情。
func (s *Service) GetZone(ctx context.Context, scope Scope, id int64) (*ZoneView, error) {
	z, err := s.repo.FindZoneByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, response.NewError(ErrZoneNotFound, nil)
	}
	if err := checkScope(scope, idOf(z.WarehouseID)); err != nil {
		return nil, err
	}
	v := viewZone(z)
	return &v, nil
}

// ListZones 库区分页列表。
func (s *Service) ListZones(ctx context.Context, f ZoneListFilter) ([]*ZoneView, int64, error) {
	rows, total, err := s.repo.ListZones(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*ZoneView, 0, len(rows))
	for _, z := range rows {
		v := viewZone(z)
		views = append(views, &v)
	}
	return views, total, nil
}

// UpdateZoneStatus 启停库区。停用校验：不允许存在启用中的货架。
func (s *Service) UpdateZoneStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validStatus(status) {
		return invalidParam("status", "ENABLED/DISABLED")
	}
	z, err := s.repo.FindZoneByID(ctx, id)
	if err != nil {
		return err
	}
	if z == nil {
		return response.NewError(ErrZoneNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(z.WarehouseID)); err != nil {
		return err
	}

	if status == StatusDisabled {
		n, err := s.repo.CountShelvesByZone(ctx, id, true)
		if err != nil {
			return err
		}
		if n > 0 {
			return response.NewError(ErrZoneHasEnabledShelves, map[string]any{"enabled_shelves": n})
		}
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateZoneCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": actor.UserID,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("zone", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": z.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
}

// CreateShelf 创建货架。warehouse_id 可选提供作交叉校验；归属以 zone 为准。
func (s *Service) CreateShelf(ctx context.Context, actor Actor, in ShelfCreateInput) (*ShelfView, error) {
	code, err := validateCode("code", in.Code)
	if err != nil {
		return nil, err
	}
	layers, columns := 1, 1
	if in.Layers != nil {
		if err := validateGridPos("layers", *in.Layers); err != nil {
			return nil, err
		}
		layers = *in.Layers
	}
	if in.Columns != nil {
		if err := validateGridPos("columns", *in.Columns); err != nil {
			return nil, err
		}
		columns = *in.Columns
	}
	capacity := 0.0
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		capacity = *in.Capacity
	}

	zone, err := s.repo.FindZoneByID(ctx, in.ZoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, response.NewError(ErrZoneNotFound, map[string]any{"zone_id": in.ZoneID})
	}
	if err := checkScope(actor.Scope, idOf(zone.WarehouseID)); err != nil {
		return nil, err
	}
	if in.WarehouseID > 0 && in.WarehouseID != idOf(zone.WarehouseID) {
		return nil, response.NewError(ErrParentMismatch, map[string]any{
			"warehouse_id": in.WarehouseID, "zone_warehouse_id": idOf(zone.WarehouseID),
		})
	}
	if _, err := s.loadWarehouseForChild(ctx, actor, idOf(zone.WarehouseID)); err != nil {
		return nil, err
	}
	if zone.Status != StatusEnabled {
		return nil, response.NewError(ErrParentDisabled, map[string]any{"zone_id": in.ZoneID})
	}
	if exist, err := s.repo.FindShelfByCode(ctx, in.ZoneID, code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrShelfCodeExists, map[string]any{"code": code, "zone_id": in.ZoneID})
	}

	sh := &Shelf{
		WarehouseID: zone.WarehouseID,
		ZoneID:      zone.ID,
		Code:        code,
		Layers:      layers,
		Columns:     columns,
		Capacity:    capacity,
		Status:      StatusEnabled,
	}
	sh.CreatedBy = database.ID(actor.UserID)
	sh.UpdatedBy = database.ID(actor.UserID)

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertShelf(ctx, tx, sh); err != nil {
			return err
		}
		e := actor.auditEntry("shelf", idOf(sh.ID), "create")
		e.Success = true
		e.After = viewShelf(sh)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewShelf(sh)
	return &v, nil
}

// UpdateShelf 更新货架（zone_id/warehouse_id 不可变更）。
func (s *Service) UpdateShelf(ctx context.Context, actor Actor, id int64, in ShelfUpdateInput) (*ShelfView, error) {
	sh, err := s.repo.FindShelfByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sh == nil {
		return nil, response.NewError(ErrShelfNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(sh.WarehouseID)); err != nil {
		return nil, err
	}

	cols := map[string]any{}
	if in.Code != nil {
		code, err := validateCode("code", *in.Code)
		if err != nil {
			return nil, err
		}
		if exist, err := s.repo.FindShelfByCode(ctx, idOf(sh.ZoneID), code); err != nil {
			return nil, err
		} else if exist != nil && idOf(exist.ID) != id {
			return nil, response.NewError(ErrShelfCodeExists, map[string]any{"code": code})
		}
		cols["code"] = code
	}
	if in.Layers != nil {
		if err := validateGridPos("layers", *in.Layers); err != nil {
			return nil, err
		}
		cols["layers"] = *in.Layers
	}
	if in.Columns != nil {
		if err := validateGridPos("columns", *in.Columns); err != nil {
			return nil, err
		}
		cols["columns"] = *in.Columns
	}
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		cols["capacity"] = *in.Capacity
	}
	if len(cols) == 0 {
		return nil, invalidParam("body", "未提供任何可更新字段")
	}
	cols["updated_by"] = actor.UserID

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateShelfCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("shelf", id, "update")
		e.Success = true
		e.Before = viewShelf(sh)
		e.After = viewShelf(applyShelfCols(sh, cols))
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.GetShelf(ctx, actor.Scope, id)
}

// GetShelf 货架详情。
func (s *Service) GetShelf(ctx context.Context, scope Scope, id int64) (*ShelfView, error) {
	sh, err := s.repo.FindShelfByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sh == nil {
		return nil, response.NewError(ErrShelfNotFound, nil)
	}
	if err := checkScope(scope, idOf(sh.WarehouseID)); err != nil {
		return nil, err
	}
	v := viewShelf(sh)
	return &v, nil
}

// ListShelves 货架分页列表。
func (s *Service) ListShelves(ctx context.Context, f ShelfListFilter) ([]*ShelfView, int64, error) {
	rows, total, err := s.repo.ListShelves(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*ShelfView, 0, len(rows))
	for _, sh := range rows {
		v := viewShelf(sh)
		views = append(views, &v)
	}
	return views, total, nil
}

// UpdateShelfStatus 启停货架。停用校验：不允许存在启用中的库位。
func (s *Service) UpdateShelfStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validStatus(status) {
		return invalidParam("status", "ENABLED/DISABLED")
	}
	sh, err := s.repo.FindShelfByID(ctx, id)
	if err != nil {
		return err
	}
	if sh == nil {
		return response.NewError(ErrShelfNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(sh.WarehouseID)); err != nil {
		return err
	}

	if status == StatusDisabled {
		n, err := s.repo.CountBinsByShelf(ctx, id, true)
		if err != nil {
			return err
		}
		if n > 0 {
			return response.NewError(ErrShelfHasEnabledBins, map[string]any{"enabled_bins": n})
		}
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateShelfCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": actor.UserID,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("shelf", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": sh.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
}

// CreateBin 创建库位（business-flow §1.6 第四级属性：层/列/库位类型/最大容量；
// 当前容量由上架/移库维护，创建恒为 0）。编码在仓库内唯一（含软删行）。
func (s *Service) CreateBin(ctx context.Context, actor Actor, in BinCreateInput) (*BinView, error) {
	code, err := validateCode("code", in.Code)
	if err != nil {
		return nil, err
	}
	binType := BinTypePick
	if in.BinType != "" {
		if binType, err = validateTypeCode("bin_type", in.BinType); err != nil {
			return nil, err
		}
	}
	layer, columnNo := 1, 1
	if in.Layer != nil {
		if err := validateGridPos("layer", *in.Layer); err != nil {
			return nil, err
		}
		layer = *in.Layer
	}
	if in.ColumnNo != nil {
		if err := validateGridPos("column_no", *in.ColumnNo); err != nil {
			return nil, err
		}
		columnNo = *in.ColumnNo
	}
	maxCapacity := 0.0
	if in.MaxCapacity != nil {
		if err := validateNonNegative("max_capacity", *in.MaxCapacity); err != nil {
			return nil, err
		}
		maxCapacity = *in.MaxCapacity
	}

	shelf, err := s.repo.FindShelfByID(ctx, in.ShelfID)
	if err != nil {
		return nil, err
	}
	if shelf == nil {
		return nil, response.NewError(ErrShelfNotFound, map[string]any{"shelf_id": in.ShelfID})
	}
	if err := checkScope(actor.Scope, idOf(shelf.WarehouseID)); err != nil {
		return nil, err
	}
	if in.ZoneID > 0 && in.ZoneID != idOf(shelf.ZoneID) {
		return nil, response.NewError(ErrParentMismatch, map[string]any{
			"zone_id": in.ZoneID, "shelf_zone_id": idOf(shelf.ZoneID),
		})
	}
	if in.WarehouseID > 0 && in.WarehouseID != idOf(shelf.WarehouseID) {
		return nil, response.NewError(ErrParentMismatch, map[string]any{
			"warehouse_id": in.WarehouseID, "shelf_warehouse_id": idOf(shelf.WarehouseID),
		})
	}
	if _, err := s.loadWarehouseForChild(ctx, actor, idOf(shelf.WarehouseID)); err != nil {
		return nil, err
	}
	zone, err := s.repo.FindZoneByID(ctx, idOf(shelf.ZoneID))
	if err != nil {
		return nil, err
	}
	if zone == nil || zone.Status != StatusEnabled {
		return nil, response.NewError(ErrParentDisabled, map[string]any{"zone_id": idOf(shelf.ZoneID)})
	}
	if shelf.Status != StatusEnabled {
		return nil, response.NewError(ErrParentDisabled, map[string]any{"shelf_id": in.ShelfID})
	}
	if exist, err := s.repo.FindBinByCode(ctx, idOf(shelf.WarehouseID), code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrBinCodeExists, map[string]any{"code": code, "warehouse_id": idOf(shelf.WarehouseID)})
	}

	b := &Bin{
		WarehouseID: shelf.WarehouseID,
		ZoneID:      shelf.ZoneID,
		ShelfID:     shelf.ID,
		Layer:       layer,
		ColumnNo:    columnNo,
		Code:        code,
		BinType:     binType,
		MaxCapacity: maxCapacity,
		Status:      StatusEnabled,
	}
	b.CreatedBy = database.ID(actor.UserID)
	b.UpdatedBy = database.ID(actor.UserID)

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertBin(ctx, tx, b); err != nil {
			return err
		}
		e := actor.auditEntry("bin", idOf(b.ID), "create")
		e.Success = true
		e.After = viewBin(b)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewBin(b)
	return &v, nil
}

// UpdateBin 更新库位（zone_id/shelf_id/warehouse_id/current_capacity 不可变更；
// current_capacity 由上架/移库业务维护，000004 列注释）。
func (s *Service) UpdateBin(ctx context.Context, actor Actor, id int64, in BinUpdateInput) (*BinView, error) {
	b, err := s.repo.FindBinByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, response.NewError(ErrBinNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(b.WarehouseID)); err != nil {
		return nil, err
	}

	cols := map[string]any{}
	if in.Code != nil {
		code, err := validateCode("code", *in.Code)
		if err != nil {
			return nil, err
		}
		if exist, err := s.repo.FindBinByCode(ctx, idOf(b.WarehouseID), code); err != nil {
			return nil, err
		} else if exist != nil && idOf(exist.ID) != id {
			return nil, response.NewError(ErrBinCodeExists, map[string]any{"code": code})
		}
		cols["code"] = code
	}
	if in.BinType != nil {
		t, err := validateTypeCode("bin_type", *in.BinType)
		if err != nil {
			return nil, err
		}
		cols["bin_type"] = t
	}
	if in.Layer != nil {
		if err := validateGridPos("layer", *in.Layer); err != nil {
			return nil, err
		}
		cols["layer"] = *in.Layer
	}
	if in.ColumnNo != nil {
		if err := validateGridPos("column_no", *in.ColumnNo); err != nil {
			return nil, err
		}
		cols["column_no"] = *in.ColumnNo
	}
	if in.MaxCapacity != nil {
		if err := validateNonNegative("max_capacity", *in.MaxCapacity); err != nil {
			return nil, err
		}
		cols["max_capacity"] = *in.MaxCapacity
	}
	if len(cols) == 0 {
		return nil, invalidParam("body", "未提供任何可更新字段")
	}
	cols["updated_by"] = actor.UserID

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateBinCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("bin", id, "update")
		e.Success = true
		e.Before = viewBin(b)
		e.After = viewBin(applyBinCols(b, cols))
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.GetBin(ctx, actor.Scope, id)
}

// GetBin 库位详情。
func (s *Service) GetBin(ctx context.Context, scope Scope, id int64) (*BinView, error) {
	b, err := s.repo.FindBinByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, response.NewError(ErrBinNotFound, nil)
	}
	if err := checkScope(scope, idOf(b.WarehouseID)); err != nil {
		return nil, err
	}
	v := viewBin(b)
	return &v, nil
}

// ListBins 库位分页列表。
func (s *Service) ListBins(ctx context.Context, f BinListFilter) ([]*BinView, int64, error) {
	rows, total, err := s.repo.ListBins(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*BinView, 0, len(rows))
	for _, b := range rows {
		v := viewBin(b)
		views = append(views, &v)
	}
	return views, total, nil
}

// UpdateBinStatus 启停库位（叶子节点，无子级校验；停用即挡新上架，存量处理走库存域）。
func (s *Service) UpdateBinStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validStatus(status) {
		return invalidParam("status", "ENABLED/DISABLED")
	}
	b, err := s.repo.FindBinByID(ctx, id)
	if err != nil {
		return err
	}
	if b == nil {
		return response.NewError(ErrBinNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(b.WarehouseID)); err != nil {
		return err
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateBinCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": actor.UserID,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("bin", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": b.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
}

// DeleteBin 软删除库位（database.md §5.1）。删除校验：当前占用容量必须为 0
// （current_capacity 由上架/移库维护——删除时子级/占用校验）。
func (s *Service) DeleteBin(ctx context.Context, actor Actor, id int64) error {
	b, err := s.repo.FindBinByID(ctx, id)
	if err != nil {
		return err
	}
	if b == nil {
		return response.NewError(ErrBinNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(b.WarehouseID)); err != nil {
		return err
	}

	if b.CurrentCapacity > 0 {
		return response.NewError(ErrBinOccupied, map[string]any{"current_capacity": b.CurrentCapacity})
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteBin(ctx, tx, id); err != nil {
			return err
		}
		e := actor.auditEntry("bin", id, "delete")
		e.Success = true
		e.Before = viewBin(b)
		return middleware.Audit(tx, e)
	})
}

// loadWarehouseForChild 创建子级时的上级仓库校验：存在、范围内、启用
// （api.md §4 业务关系 + 状态守卫）。返回仓库行供锚点取值。
func (s *Service) loadWarehouseForChild(ctx context.Context, actor Actor, warehouseID int64) (*Warehouse, error) {
	wh, err := s.repo.FindWarehouseByID(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	if wh == nil {
		return nil, response.NewError(ErrWarehouseNotFound, map[string]any{"warehouse_id": warehouseID})
	}
	if err := checkScope(actor.Scope, idOf(wh.ID)); err != nil {
		return nil, err
	}
	if wh.Status != StatusEnabled {
		return nil, response.NewError(ErrParentDisabled, map[string]any{"warehouse_id": warehouseID})
	}
	return wh, nil
}

// applyZoneCols/applyShelfCols/applyBinCols 更新列叠加到模型（审计 after 快照）。
func applyZoneCols(z *Zone, cols map[string]any) *Zone {
	c := *z
	if v, ok := cols["code"].(string); ok {
		c.Code = v
	}
	if v, ok := cols["name"].(string); ok {
		c.Name = v
	}
	if v, ok := cols["zone_type"].(string); ok {
		c.ZoneType = v
	}
	if v, ok := cols["capacity"].(float64); ok {
		c.Capacity = v
	}
	return &c
}

func applyShelfCols(sh *Shelf, cols map[string]any) *Shelf {
	c := *sh
	if v, ok := cols["code"].(string); ok {
		c.Code = v
	}
	if v, ok := cols["layers"].(int); ok {
		c.Layers = v
	}
	if v, ok := cols["columns"].(int); ok {
		c.Columns = v
	}
	if v, ok := cols["capacity"].(float64); ok {
		c.Capacity = v
	}
	return &c
}

func applyBinCols(b *Bin, cols map[string]any) *Bin {
	c := *b
	if v, ok := cols["code"].(string); ok {
		c.Code = v
	}
	if v, ok := cols["bin_type"].(string); ok {
		c.BinType = v
	}
	if v, ok := cols["layer"].(int); ok {
		c.Layer = v
	}
	if v, ok := cols["column_no"].(int); ok {
		c.ColumnNo = v
	}
	if v, ok := cols["max_capacity"].(float64); ok {
		c.MaxCapacity = v
	}
	return &c
}
