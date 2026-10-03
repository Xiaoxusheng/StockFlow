package masterdata

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 商品分类与计量单位服务：组织级下拉数据源（business-flow §1.1）。
// 生命周期：无删除接口（backend-m1-plan §5.4.1 权限清单无 delete），停用即终点；
// 停用做被引用校验（api.md §4：状态校验——启用中的子分类/商品引用禁止停用）。
// 编码为业务追溯键：创建后不可修改（business-flow §13.3 追溯要求）。

// maxCategoryDepth 上级链路遍历上限（环检测防御性上限；正常树深远小于此值）。
const maxCategoryDepth = 64

// ---- 视图 DTO ----

// CategoryView 分类视图。
type CategoryView struct {
	ID        database.ID       `json:"id"`
	ParentID  *database.ID      `json:"parent_id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Sort      int               `json:"sort"`
	Status    string            `json:"status"`
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
}

func viewCategory(c *ProductCategory) *CategoryView {
	return &CategoryView{
		ID:        c.ID,
		ParentID:  c.ParentID,
		Code:      c.Code,
		Name:      c.Name,
		Sort:      c.Sort,
		Status:    c.Status,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

// UnitView 单位视图。
type UnitView struct {
	ID        database.ID       `json:"id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
}

func viewUnit(u *Unit) *UnitView {
	return &UnitView{
		ID:        u.ID,
		Code:      u.Code,
		Name:      u.Name,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// ---- 商品分类 ----

// CategoryCreateInput 创建分类入参。
type CategoryCreateInput struct {
	ParentID *int64 `json:"parent_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Sort     int    `json:"sort"`
}

// CreateCategory 创建分类（编码唯一、上级存在且启用）。
func (s *Service) CreateCategory(ctx context.Context, actor Actor, in CategoryCreateInput) (*CategoryView, error) {
	if err := validateCode("code", in.Code, false); err != nil {
		return nil, err
	}
	if err := validateName("name", in.Name, 128); err != nil {
		return nil, err
	}
	if err := optionalID("parent_id", in.ParentID); err != nil {
		return nil, err
	}
	if in.ParentID != nil {
		parent, err := s.repo.FindCategoryByID(ctx, *in.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, response.NewError(ErrCategoryNotFound, map[string]any{"parent_id": *in.ParentID})
		}
		if parent.Status != StatusEnabled {
			return nil, response.NewError(ErrCategoryDisabled, map[string]any{"parent_id": *in.ParentID})
		}
	}
	if exist, err := s.repo.FindCategoryByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrCategoryCodeExists, map[string]any{"code": in.Code})
	}

	c := &ProductCategory{
		Code:   in.Code,
		Name:   in.Name,
		Sort:   in.Sort,
		Status: StatusEnabled, // 创建默认启用（deny-by-default 无"待生效"态）
	}
	if in.ParentID != nil {
		id := database.ID(*in.ParentID)
		c.ParentID = &id
	}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertCategory(ctx, tx, c); err != nil {
			return err
		}
		e := actor.auditEntry("category", c.ID.Int64(), "create")
		e.Success = true
		e.After = viewCategory(c)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewCategory(c), nil
}

// CategoryUpdateInput 更新分类入参（编码不可改；parent_id 三态：nil 不变 / 0 提升为顶级 / >0 换上级）。
type CategoryUpdateInput struct {
	ParentID *int64  `json:"parent_id"`
	Name     *string `json:"name"`
	Sort     *int    `json:"sort"`
}

// UpdateCategory 更新分类（换上级做环检测：新上级不能是自身或自身的下级）。
func (s *Service) UpdateCategory(ctx context.Context, actor Actor, id int64, in CategoryUpdateInput) (*CategoryView, error) {
	c, err := s.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, response.NewError(ErrCategoryNotFound, map[string]any{"id": id})
	}
	before := viewCategory(c)

	if in.Name != nil {
		if err := validateName("name", *in.Name, 128); err != nil {
			return nil, err
		}
	}
	if in.Sort != nil && *in.Sort < 0 {
		return nil, invalidParam("sort", "不能为负数")
	}
	if in.ParentID != nil {
		newParent := *in.ParentID
		// 三态语义：nil 不修改 / 0 提升为顶级 / >0 换上级（负数非法）。
		if newParent < 0 {
			return nil, invalidParam("parent_id", "必须为正整数或 0（顶级）")
		}
		if newParent == id {
			return nil, response.NewError(ErrCategoryCycle, map[string]any{"parent_id": newParent})
		}
		if newParent > 0 {
			parent, err := s.repo.FindCategoryByID(ctx, newParent)
			if err != nil {
				return nil, err
			}
			if parent == nil {
				return nil, response.NewError(ErrCategoryNotFound, map[string]any{"parent_id": newParent})
			}
			if parent.Status != StatusEnabled {
				return nil, response.NewError(ErrCategoryDisabled, map[string]any{"parent_id": newParent})
			}
			// 环检测：自新上级沿上级链上溯，命中自身即构成环（api.md §4 业务关系校验）。
			if err := s.assertNotDescendant(ctx, id, newParent); err != nil {
				return nil, err
			}
			pid := database.ID(newParent)
			c.ParentID = &pid
		} else {
			c.ParentID = nil
		}
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.ParentID != nil {
		cols["parent_id"] = c.ParentID // nil → 置顶级（PG 列可空）
	}
	if in.Name != nil {
		c.Name = *in.Name
		cols["name"] = c.Name
	}
	if in.Sort != nil {
		c.Sort = *in.Sort
		cols["sort"] = c.Sort
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateCategoryCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("category", id, "update")
		e.Success = true
		e.Before = before
		e.After = viewCategory(c)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewCategory(c), nil
}

// assertNotDescendant 校验 ancestorChain 不经过 self：newParent 是 self 的下级则构成环。
func (s *Service) assertNotDescendant(ctx context.Context, selfID, newParentID int64) error {
	cur := newParentID
	for depth := 0; cur > 0 && depth < maxCategoryDepth; depth++ {
		node, err := s.repo.FindCategoryByID(ctx, cur)
		if err != nil {
			return err
		}
		if node == nil {
			break
		}
		if node.ID.Int64() == selfID {
			return response.NewError(ErrCategoryCycle, map[string]any{"parent_id": newParentID})
		}
		if node.ParentID == nil {
			break
		}
		cur = node.ParentID.Int64()
	}
	return nil
}

// GetCategory 分类详情。
func (s *Service) GetCategory(ctx context.Context, id int64) (*CategoryView, error) {
	c, err := s.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, response.NewError(ErrCategoryNotFound, map[string]any{"id": id})
	}
	return viewCategory(c), nil
}

// ListCategories 分类分页列表。
func (s *Service) ListCategories(ctx context.Context, f CategoryListFilter) ([]*CategoryView, int64, error) {
	items, total, err := s.repo.ListCategories(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*CategoryView, 0, len(items))
	for _, c := range items {
		views = append(views, viewCategory(c))
	}
	return views, total, nil
}

// UpdateCategoryStatus 启用/停用分类（停用做被引用校验：启用中的子分类与启用中的商品）。
func (s *Service) UpdateCategoryStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return invalidParam("status", "必须为 ENABLED 或 DISABLED")
	}
	c, err := s.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return err
	}
	if c == nil {
		return response.NewError(ErrCategoryNotFound, map[string]any{"id": id})
	}
	before := viewCategory(c)

	if status == StatusDisabled {
		children, err := s.repo.CountEnabledChildCategories(ctx, id)
		if err != nil {
			return err
		}
		if children > 0 {
			return response.NewError(ErrCategoryHasChildren, map[string]any{"enabled_children": children})
		}
		products, err := s.repo.CountProductsByCategory(ctx, id, true)
		if err != nil {
			return err
		}
		if products > 0 {
			return response.NewError(ErrCategoryInUse, map[string]any{"enabled_products": products})
		}
	}

	c.Status = status
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateCategoryCols(ctx, tx, id, map[string]any{
			"status":     status,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("category", id, "status") // 停用/启用强制审计（backend-m1-plan §4.4）
		e.Success = true
		e.Before = before
		e.After = viewCategory(c)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}

// ---- 计量单位 ----

// UnitCreateInput 创建单位入参。
type UnitCreateInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// CreateUnit 创建计量单位（编码唯一）。
func (s *Service) CreateUnit(ctx context.Context, actor Actor, in UnitCreateInput) (*UnitView, error) {
	if err := validateCode("code", in.Code, true); err != nil {
		return nil, err
	}
	if err := validateName("name", in.Name, 64); err != nil {
		return nil, err
	}
	if exist, err := s.repo.FindUnitByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrUnitCodeExists, map[string]any{"code": in.Code})
	}

	u := &Unit{Code: in.Code, Name: in.Name, Status: StatusEnabled}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertUnit(ctx, tx, u); err != nil {
			return err
		}
		e := actor.auditEntry("unit", u.ID.Int64(), "create")
		e.Success = true
		e.After = viewUnit(u)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewUnit(u), nil
}

// UnitUpdateInput 更新单位入参（编码不可改）。
type UnitUpdateInput struct {
	Name *string `json:"name"`
}

// UpdateUnit 更新计量单位。
func (s *Service) UpdateUnit(ctx context.Context, actor Actor, id int64, in UnitUpdateInput) (*UnitView, error) {
	u, err := s.repo.FindUnitByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewError(ErrUnitNotFound, map[string]any{"id": id})
	}
	before := viewUnit(u)
	if in.Name != nil {
		if err := validateName("name", *in.Name, 64); err != nil {
			return nil, err
		}
		u.Name = *in.Name
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.Name != nil {
		cols["name"] = u.Name
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUnitCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("unit", id, "update")
		e.Success = true
		e.Before = before
		e.After = viewUnit(u)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewUnit(u), nil
}

// GetUnit 单位详情。
func (s *Service) GetUnit(ctx context.Context, id int64) (*UnitView, error) {
	u, err := s.repo.FindUnitByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewError(ErrUnitNotFound, map[string]any{"id": id})
	}
	return viewUnit(u), nil
}

// ListUnits 单位分页列表。
func (s *Service) ListUnits(ctx context.Context, f UnitListFilter) ([]*UnitView, int64, error) {
	items, total, err := s.repo.ListUnits(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*UnitView, 0, len(items))
	for _, u := range items {
		views = append(views, viewUnit(u))
	}
	return views, total, nil
}

// UpdateUnitStatus 启用/停用单位（停用被引用校验：任何未删商品引用即禁止停用——
// 停用中的商品仍展示其单位，引用完整性优先，api.md §4）。
func (s *Service) UpdateUnitStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return invalidParam("status", "必须为 ENABLED 或 DISABLED")
	}
	u, err := s.repo.FindUnitByID(ctx, id)
	if err != nil {
		return err
	}
	if u == nil {
		return response.NewError(ErrUnitNotFound, map[string]any{"id": id})
	}
	before := viewUnit(u)

	if status == StatusDisabled {
		ref, err := s.repo.CountProductsByUnit(ctx, id)
		if err != nil {
			return err
		}
		if ref > 0 {
			return response.NewError(ErrUnitInUse, map[string]any{"products": ref})
		}
	}

	u.Status = status
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUnitCols(ctx, tx, id, map[string]any{
			"status":     status,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("unit", id, "status")
		e.Success = true
		e.Before = before
		e.After = viewUnit(u)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}
