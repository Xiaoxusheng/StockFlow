package warehouse

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 仓库实体 Service（business-flow §1.6 第一级）。
// 事务边界：全部写操作（含审计写入）在同一事务内（architecture.md §4）。
// 数据权限：读/写均先做仓库范围校验（permission.md §4）；范围外按不存在处理。

// CreateWarehouse 创建仓库（编码全局唯一——未删除行内，软删仓库编码可复用，
// uk_warehouses_code 部分索引同源）。
func (s *Service) CreateWarehouse(ctx context.Context, actor Actor, in WarehouseCreateInput) (*WarehouseView, error) {
	code, err := validateCode("code", in.Code)
	if err != nil {
		return nil, err
	}
	name, err := validateName("name", in.Name)
	if err != nil {
		return nil, err
	}
	addr, err := validateAddress(in.Address)
	if err != nil {
		return nil, err
	}
	contact, phone, err := validateContactInfo(in.Contact, in.Phone)
	if err != nil {
		return nil, err
	}
	whType := WarehouseTypeNormal
	if in.Type != "" {
		if whType, err = validateTypeCode("type", in.Type); err != nil {
			return nil, err
		}
	}
	area, capacity := 0.0, 0.0
	if in.Area != nil {
		if err := validateNonNegative("area", *in.Area); err != nil {
			return nil, err
		}
		area = *in.Area
	}
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		capacity = *in.Capacity
	}
	if in.ManagerUserID < 0 {
		return nil, invalidParam("manager_user_id", "不能为负数")
	}

	if exist, err := s.repo.FindWarehouseByCode(ctx, code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrWarehouseCodeExists, map[string]any{"code": code})
	}

	w := &Warehouse{
		Code:          code,
		Name:          name,
		Address:       addr,
		Contact:       contact,
		Phone:         phone,
		Area:          area,
		Capacity:      capacity,
		Type:          whType,
		Status:        StatusEnabled,
		ManagerUserID: database.ID(in.ManagerUserID),
	}
	w.CreatedBy = database.ID(actor.UserID)
	w.UpdatedBy = database.ID(actor.UserID)

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertWarehouse(ctx, tx, w); err != nil {
			return err
		}
		e := actor.auditEntry("warehouse", idOf(w.ID), "create")
		e.Success = true
		e.After = viewWarehouse(w)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	v := viewWarehouse(w)
	return &v, nil // 事务提交后 ID/时间已回填
}

// UpdateWarehouse 更新仓库（部分更新：指针字段提供才更新；status 不在更新面）。
func (s *Service) UpdateWarehouse(ctx context.Context, actor Actor, id int64, in WarehouseUpdateInput) (*WarehouseView, error) {
	w, err := s.repo.FindWarehouseByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, response.NewError(ErrWarehouseNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(w.ID)); err != nil {
		return nil, err
	}

	cols := map[string]any{}
	if in.Code != nil {
		code, err := validateCode("code", *in.Code)
		if err != nil {
			return nil, err
		}
		if exist, err := s.repo.FindWarehouseByCode(ctx, code); err != nil {
			return nil, err
		} else if exist != nil && idOf(exist.ID) != id {
			return nil, response.NewError(ErrWarehouseCodeExists, map[string]any{"code": code})
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
	if in.Address != nil {
		addr, err := validateAddress(*in.Address)
		if err != nil {
			return nil, err
		}
		cols["address"] = addr
	}
	if in.Contact != nil || in.Phone != nil {
		contact, phone := w.Contact, w.Phone
		if in.Contact != nil {
			contact = *in.Contact
		}
		if in.Phone != nil {
			phone = *in.Phone
		}
		cVal, pVal, vErr := validateContactInfo(contact, phone)
		if vErr != nil {
			return nil, vErr
		}
		cols["contact"], cols["phone"] = cVal, pVal
	}
	if in.Area != nil {
		if err := validateNonNegative("area", *in.Area); err != nil {
			return nil, err
		}
		cols["area"] = *in.Area
	}
	if in.Capacity != nil {
		if err := validateNonNegative("capacity", *in.Capacity); err != nil {
			return nil, err
		}
		cols["capacity"] = *in.Capacity
	}
	if in.Type != nil {
		t, err := validateTypeCode("type", *in.Type)
		if err != nil {
			return nil, err
		}
		cols["type"] = t
	}
	if in.ManagerUserID != nil {
		if *in.ManagerUserID < 0 {
			return nil, invalidParam("manager_user_id", "不能为负数")
		}
		cols["manager_user_id"] = *in.ManagerUserID
	}
	if len(cols) == 0 {
		return nil, invalidParam("body", "未提供任何可更新字段")
	}
	cols["updated_by"] = actor.UserID

	before := viewWarehouse(w)
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateWarehouseCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("warehouse", id, "update")
		e.Success = true
		e.Before = before
		e.After = applyWarehouseCols(before, cols)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.GetWarehouse(ctx, actor.Scope, id)
}

// GetWarehouse 仓库详情。
func (s *Service) GetWarehouse(ctx context.Context, scope Scope, id int64) (*WarehouseView, error) {
	w, err := s.repo.FindWarehouseByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, response.NewError(ErrWarehouseNotFound, nil)
	}
	if err := checkScope(scope, idOf(w.ID)); err != nil {
		return nil, err
	}
	v := viewWarehouse(w)
	return &v, nil
}

// ListWarehouses 仓库分页列表（强制分页；数据权限范围在 Repository 注入）。
func (s *Service) ListWarehouses(ctx context.Context, f WarehouseListFilter) ([]*WarehouseView, int64, error) {
	rows, total, err := s.repo.ListWarehouses(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*WarehouseView, 0, len(rows))
	for _, w := range rows {
		v := viewWarehouse(w)
		views = append(views, &v)
	}
	return views, total, nil
}

// UpdateWarehouseStatus 启停仓库。停用校验：不允许存在启用中的库区
// （zones 无删除通路，停用即库区的离场路径——与 auth 部门停用同语义）。
func (s *Service) UpdateWarehouseStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validStatus(status) {
		return invalidParam("status", "ENABLED/DISABLED")
	}
	w, err := s.repo.FindWarehouseByID(ctx, id)
	if err != nil {
		return err
	}
	if w == nil {
		return response.NewError(ErrWarehouseNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(w.ID)); err != nil {
		return err
	}

	if status == StatusDisabled {
		n, err := s.repo.CountZonesByWarehouse(ctx, id, true)
		if err != nil {
			return err
		}
		if n > 0 {
			return response.NewError(ErrWarehouseHasEnabledZones, map[string]any{"enabled_zones": n})
		}
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateWarehouseCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": actor.UserID,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("warehouse", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": w.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
	return err
}

// DeleteWarehouse 软删除仓库（database.md §5.1）。删除校验：仓库下不允许存在任何库区
// （zones 无删除通路，存在库区即不可删——ask 的"删除时子级校验"）。
func (s *Service) DeleteWarehouse(ctx context.Context, actor Actor, id int64) error {
	w, err := s.repo.FindWarehouseByID(ctx, id)
	if err != nil {
		return err
	}
	if w == nil {
		return response.NewError(ErrWarehouseNotFound, nil)
	}
	if err := checkScope(actor.Scope, idOf(w.ID)); err != nil {
		return err
	}

	n, err := s.repo.CountZonesByWarehouse(ctx, id, false)
	if err != nil {
		return err
	}
	if n > 0 {
		return response.NewError(ErrWarehouseHasZones, map[string]any{"zones": n})
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteWarehouse(ctx, tx, id); err != nil {
			return err
		}
		e := actor.auditEntry("warehouse", id, "delete")
		e.Success = true
		e.Before = viewWarehouse(w)
		return middleware.Audit(tx, e)
	})
}

// applyWarehouseCols 将更新列叠加到 before 快照，得到审计 after 快照（与实际写入一致）。
func applyWarehouseCols(before WarehouseView, cols map[string]any) WarehouseView {
	after := before
	if v, ok := cols["code"].(string); ok {
		after.Code = v
	}
	if v, ok := cols["name"].(string); ok {
		after.Name = v
	}
	if v, ok := cols["address"].(string); ok {
		after.Address = v
	}
	if v, ok := cols["contact"].(string); ok {
		after.Contact = v
	}
	if v, ok := cols["phone"].(string); ok {
		after.Phone = v
	}
	if v, ok := cols["area"].(float64); ok {
		after.Area = v
	}
	if v, ok := cols["capacity"].(float64); ok {
		after.Capacity = v
	}
	if v, ok := cols["type"].(string); ok {
		after.Type = v
	}
	if v, ok := cols["manager_user_id"].(int64); ok {
		after.ManagerUserID = database.ID(v)
	}
	return after
}
