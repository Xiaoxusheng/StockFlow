package masterdata

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 商品服务（business-flow §1.1 全量字段；backend-m1-plan §6.2 products 表）。
// 商品-SKU 级联约束（api.md §4 业务关系/状态校验）：
//   - 商品删除前校验无 SKU 引用（软删对象，被引用即禁止删除，改用停用）；
//   - 商品停用级联停用其启用中的 SKU（同事务），避免"商品停用而 SKU 可分配"的
//     不一致态流入 M2 库存/出库；商品启用不自动反启 SKU（保持操作者意图最小化）。

// ---- 视图 DTO ----

// ProductView 商品视图。
type ProductView struct {
	ID           database.ID       `json:"id"`
	Code         string            `json:"code"`
	Name         string            `json:"name"`
	ShortName    string            `json:"short_name"`
	CategoryID   *database.ID      `json:"category_id"`
	CategoryName string            `json:"category_name,omitempty"` // 详情装配
	Brand        string            `json:"brand"`
	Model        string            `json:"model"`
	Spec         string            `json:"spec"`
	UnitID       *database.ID      `json:"unit_id"`
	UnitName     string            `json:"unit_name,omitempty"` // 详情装配
	Weight       Number            `json:"weight"`
	Length       Number            `json:"length"`
	Width        Number            `json:"width"`
	Height       Number            `json:"height"`
	Volume       Number            `json:"volume"`
	ImageURLs    StringList        `json:"image_urls"`
	Description  string            `json:"description"`
	Remark       string            `json:"remark"`
	Status       string            `json:"status"`
	CreatedAt    database.JSONTime `json:"created_at"`
	UpdatedAt    database.JSONTime `json:"updated_at"`
}

func viewProduct(p *Product) *ProductView {
	return &ProductView{
		ID:          p.ID,
		Code:        p.Code,
		Name:        p.Name,
		ShortName:   p.ShortName,
		CategoryID:  p.CategoryID,
		Brand:       p.Brand,
		Model:       p.Model,
		Spec:        p.Spec,
		UnitID:      p.UnitID,
		Weight:      p.Weight,
		Length:      p.Length,
		Width:       p.Width,
		Height:      p.Height,
		Volume:      p.Volume,
		ImageURLs:   p.ImageURLs.normalize(),
		Description: p.Description,
		Remark:      p.Remark,
		Status:      p.Status,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// assembleProductView 详情装配：分类/单位名称（域内联查，architecture.md §7 批量装配）。
func (s *Service) assembleProductView(ctx context.Context, p *Product) (*ProductView, error) {
	v := viewProduct(p)
	if p.CategoryID != nil {
		c, err := s.repo.FindCategoryByID(ctx, p.CategoryID.Int64())
		if err != nil {
			return nil, err
		}
		if c != nil {
			v.CategoryName = c.Name
		}
	}
	if p.UnitID != nil {
		u, err := s.repo.FindUnitByID(ctx, p.UnitID.Int64())
		if err != nil {
			return nil, err
		}
		if u != nil {
			v.UnitName = u.Name
		}
	}
	return v, nil
}

// ---- 输入 DTO ----

// ProductCreateInput 创建商品入参（状态恒为 ENABLED 创建；下线走启停接口）。
type ProductCreateInput struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	ShortName   string   `json:"short_name"`
	CategoryID  *int64   `json:"category_id"`
	Brand       string   `json:"brand"`
	Model       string   `json:"model"`
	Spec        string   `json:"spec"`
	UnitID      *int64   `json:"unit_id"`
	Weight      Number   `json:"weight"`
	Length      Number   `json:"length"`
	Width       Number   `json:"width"`
	Height      Number   `json:"height"`
	Volume      Number   `json:"volume"`
	ImageURLs   []string `json:"image_urls"`
	Description string   `json:"description"`
	Remark      string   `json:"remark"`
}

// ProductUpdateInput 更新商品入参（编码/状态不可经本接口修改；指针三态：nil 不修改）。
// 分类/单位 ID 三态语义（对齐 service_category.go CategoryUpdateInput.ParentID 的 0 哨兵
// 先例，前端 allowClear 显式清空不再与缺省同义）：
//   - nil（缺省）不修改；
//   - 0 显式清空（置 NULL）；
//   - >0 换绑（存在 + 启用校验）；负数非法。
type ProductUpdateInput struct {
	Name        *string  `json:"name"`
	ShortName   *string  `json:"short_name"`
	CategoryID  *int64   `json:"category_id"`
	Brand       *string  `json:"brand"`
	Model       *string  `json:"model"`
	Spec        *string  `json:"spec"`
	UnitID      *int64   `json:"unit_id"`
	Weight      *Number  `json:"weight"`
	Length      *Number  `json:"length"`
	Width       *Number  `json:"width"`
	Height      *Number  `json:"height"`
	Volume      *Number  `json:"volume"`
	ImageURLs   []string `json:"image_urls"`
	Description *string  `json:"description"`
	Remark      *string  `json:"remark"`
}

// ---- 业务方法 ----

// validateProductRefs 校验分类/单位引用（api.md §4 业务关系：存在 + 启用）。
func (s *Service) validateProductRefs(ctx context.Context, in ProductCreateInput) error {
	if err := optionalID("category_id", in.CategoryID); err != nil {
		return err
	}
	if err := optionalID("unit_id", in.UnitID); err != nil {
		return err
	}
	if in.CategoryID != nil {
		c, err := s.repo.FindCategoryByID(ctx, *in.CategoryID)
		if err != nil {
			return err
		}
		if c == nil {
			return response.NewError(ErrCategoryNotFound, map[string]any{"category_id": *in.CategoryID})
		}
		if c.Status != StatusEnabled {
			return response.NewError(ErrCategoryDisabled, map[string]any{"category_id": *in.CategoryID})
		}
	}
	if in.UnitID != nil {
		u, err := s.repo.FindUnitByID(ctx, *in.UnitID)
		if err != nil {
			return err
		}
		if u == nil {
			return response.NewError(ErrUnitNotFound, map[string]any{"unit_id": *in.UnitID})
		}
		if u.Status != StatusEnabled {
			return response.NewError(ErrUnitDisabled, map[string]any{"unit_id": *in.UnitID})
		}
	}
	return nil
}

// validateProductNumbers 数量字段非负 + 库存范围校验。
func validateProductNumbers(in ProductCreateInput) error {
	for field, n := range map[string]Number{
		"weight": in.Weight, "length": in.Length, "width": in.Width,
		"height": in.Height, "volume": in.Volume,
	} {
		if err := validateNonNegative(field, n); err != nil {
			return err
		}
	}
	return nil
}

// CreateProduct 创建商品。
func (s *Service) CreateProduct(ctx context.Context, actor Actor, in ProductCreateInput) (*ProductView, error) {
	if err := validateCode("code", in.Code, false); err != nil {
		return nil, err
	}
	if err := validateName("name", in.Name, 255); err != nil {
		return nil, err
	}
	if err := validateOptionalText("short_name", in.ShortName, 128); err != nil {
		return nil, err
	}
	if err := validateOptionalText("brand", in.Brand, 128); err != nil {
		return nil, err
	}
	if err := validateOptionalText("model", in.Model, 128); err != nil {
		return nil, err
	}
	if err := validateOptionalText("spec", in.Spec, 255); err != nil {
		return nil, err
	}
	if err := validateImageURLs(in.ImageURLs); err != nil {
		return nil, err
	}
	if err := validateOptionalText("description", in.Description, 10000); err != nil {
		return nil, err
	}
	if err := validateOptionalText("remark", in.Remark, 2000); err != nil {
		return nil, err
	}
	if err := s.validateProductRefs(ctx, in); err != nil {
		return nil, err
	}
	if err := validateProductNumbers(in); err != nil {
		return nil, err
	}
	if exist, err := s.repo.FindProductByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrProductCodeExists, map[string]any{"code": in.Code})
	}

	p := &Product{
		Code:        in.Code,
		Name:        in.Name,
		ShortName:   in.ShortName,
		Brand:       in.Brand,
		Model:       in.Model,
		Spec:        in.Spec,
		Weight:      in.Weight,
		Length:      in.Length,
		Width:       in.Width,
		Height:      in.Height,
		Volume:      in.Volume,
		ImageURLs:   StringList(in.ImageURLs).normalize(),
		Description: in.Description,
		Remark:      in.Remark,
		Status:      StatusEnabled,
	}
	if in.CategoryID != nil {
		id := database.ID(*in.CategoryID)
		p.CategoryID = &id
	}
	if in.UnitID != nil {
		id := database.ID(*in.UnitID)
		p.UnitID = &id
	}

	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertProduct(ctx, tx, p); err != nil {
			return err
		}
		e := actor.auditEntry("product", p.ID.Int64(), "create")
		e.Success = true
		e.After = viewProduct(p)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewProduct(p), nil
}

// UpdateProduct 更新商品。
func (s *Service) UpdateProduct(ctx context.Context, actor Actor, id int64, in ProductUpdateInput) (*ProductView, error) {
	p, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, response.NewError(ErrProductNotFound, map[string]any{"id": id})
	}
	before := viewProduct(p)

	if in.Name != nil {
		if err := validateName("name", *in.Name, 255); err != nil {
			return nil, err
		}
	}
	if in.ShortName != nil {
		if err := validateOptionalText("short_name", *in.ShortName, 128); err != nil {
			return nil, err
		}
	}
	if in.Brand != nil {
		if err := validateOptionalText("brand", *in.Brand, 128); err != nil {
			return nil, err
		}
	}
	if in.Model != nil {
		if err := validateOptionalText("model", *in.Model, 128); err != nil {
			return nil, err
		}
	}
	if in.Spec != nil {
		if err := validateOptionalText("spec", *in.Spec, 255); err != nil {
			return nil, err
		}
	}
	if in.ImageURLs != nil {
		if err := validateImageURLs(in.ImageURLs); err != nil {
			return nil, err
		}
	}
	if in.Description != nil {
		if err := validateOptionalText("description", *in.Description, 10000); err != nil {
			return nil, err
		}
	}
	if in.Remark != nil {
		if err := validateOptionalText("remark", *in.Remark, 2000); err != nil {
			return nil, err
		}
	}
	// 引用校验（换分类/单位：存在 + 启用；0=显式清空跳过引用校验，负数非法）。
	refs := ProductCreateInput{}
	for field, v := range map[string]*int64{"category_id": in.CategoryID, "unit_id": in.UnitID} {
		if v == nil {
			continue
		}
		if *v < 0 {
			return nil, invalidParam(field, "必须为正整数或 0（显式清空）")
		}
		if *v == 0 {
			continue
		}
		if field == "category_id" {
			refs.CategoryID = v
		} else {
			refs.UnitID = v
		}
	}
	if err := s.validateProductRefs(ctx, refs); err != nil {
		return nil, err
	}
	// 数量校验：以"更新后的最终值"校验（未提供的沿用现值）。
	nums := ProductCreateInput{Weight: p.Weight, Length: p.Length, Width: p.Width, Height: p.Height, Volume: p.Volume}
	if in.Weight != nil {
		nums.Weight = *in.Weight
	}
	if in.Length != nil {
		nums.Length = *in.Length
	}
	if in.Width != nil {
		nums.Width = *in.Width
	}
	if in.Height != nil {
		nums.Height = *in.Height
	}
	if in.Volume != nil {
		nums.Volume = *in.Volume
	}
	if err := validateProductNumbers(nums); err != nil {
		return nil, err
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.Name != nil {
		p.Name = *in.Name
		cols["name"] = p.Name
	}
	if in.ShortName != nil {
		p.ShortName = *in.ShortName
		cols["short_name"] = p.ShortName
	}
	if in.CategoryID != nil {
		// 三态：0=显式清空（置 NULL）/ >0=换绑（nil 缺省不修改，见 ProductUpdateInput 注）。
		if *in.CategoryID > 0 {
			cid := database.ID(*in.CategoryID)
			p.CategoryID = &cid
			cols["category_id"] = p.CategoryID
		} else {
			p.CategoryID = nil
			cols["category_id"] = nil
		}
	}
	if in.Brand != nil {
		p.Brand = *in.Brand
		cols["brand"] = p.Brand
	}
	if in.Model != nil {
		p.Model = *in.Model
		cols["model"] = p.Model
	}
	if in.Spec != nil {
		p.Spec = *in.Spec
		cols["spec"] = p.Spec
	}
	if in.UnitID != nil {
		// 三态：0=显式清空（置 NULL）/ >0=换绑（nil 缺省不修改，见 ProductUpdateInput 注）。
		if *in.UnitID > 0 {
			uid := database.ID(*in.UnitID)
			p.UnitID = &uid
			cols["unit_id"] = p.UnitID
		} else {
			p.UnitID = nil
			cols["unit_id"] = nil
		}
	}
	if in.Weight != nil {
		p.Weight = *in.Weight
		cols["weight"] = p.Weight
	}
	if in.Length != nil {
		p.Length = *in.Length
		cols["length"] = p.Length
	}
	if in.Width != nil {
		p.Width = *in.Width
		cols["width"] = p.Width
	}
	if in.Height != nil {
		p.Height = *in.Height
		cols["height"] = p.Height
	}
	if in.Volume != nil {
		p.Volume = *in.Volume
		cols["volume"] = p.Volume
	}
	if in.ImageURLs != nil {
		p.ImageURLs = StringList(in.ImageURLs).normalize()
		cols["image_urls"] = p.ImageURLs
	}
	if in.Description != nil {
		p.Description = *in.Description
		cols["description"] = p.Description
	}
	if in.Remark != nil {
		p.Remark = *in.Remark
		cols["remark"] = p.Remark
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateProductCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("product", id, "update")
		e.Success = true
		e.Before = before
		e.After = viewProduct(p)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewProduct(p), nil
}

// GetProduct 商品详情（装配分类/单位名称）。
func (s *Service) GetProduct(ctx context.Context, id int64) (*ProductView, error) {
	p, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, response.NewError(ErrProductNotFound, map[string]any{"id": id})
	}
	return s.assembleProductView(ctx, p)
}

// ListProducts 商品分页列表。
func (s *Service) ListProducts(ctx context.Context, f ProductListFilter) ([]*ProductView, int64, error) {
	items, total, err := s.repo.ListProducts(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*ProductView, 0, len(items))
	for _, p := range items {
		views = append(views, viewProduct(p))
	}
	return views, total, nil
}

// UpdateProductStatus 启用/停用商品；停用级联停用其启用中的 SKU（同事务，级联数量入审计）。
func (s *Service) UpdateProductStatus(ctx context.Context, actor Actor, id int64, status string) (int64, error) {
	if !validOnOffStatus(status) {
		return 0, invalidParam("status", "必须为 ENABLED 或 DISABLED")
	}
	p, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return 0, err
	}
	if p == nil {
		return 0, response.NewError(ErrProductNotFound, map[string]any{"id": id})
	}
	before := viewProduct(p)

	var cascaded int64
	p.Status = status
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateProductCols(ctx, tx, id, map[string]any{
			"status":     status,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		if status == StatusDisabled {
			n, err := s.repo.DisableSkusOfProduct(ctx, tx, id, actor.UserID)
			if err != nil {
				return err
			}
			cascaded = n
		}
		e := actor.auditEntry("product", id, "status") // 停用/启用强制审计（backend-m1-plan §4.4）
		e.Success = true
		e.Before = before
		e.After = map[string]any{
			"status": status, "cascade_disabled_skus": cascaded,
		}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return 0, err
	}
	return cascaded, nil
}

// DeleteProduct 软删除商品（database.md §5.1）：被 SKU 引用即拒绝（api.md §4 删除被引用校验）。
func (s *Service) DeleteProduct(ctx context.Context, actor Actor, id int64) error {
	p, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return response.NewError(ErrProductNotFound, map[string]any{"id": id})
	}
	n, err := s.repo.CountSkusByProduct(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return response.NewError(ErrProductHasSKU, map[string]any{"sku_count": n})
	}

	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteProduct(ctx, tx, id, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("product", id, "delete") // 删除强制审计（backend-m1-plan §4.4）
		e.Success = true
		e.Before = viewProduct(p)
		return middleware.Audit(tx, e)
	})
}

// normalizeKeyword 列表关键字统一修剪（handler 传入前调用）。
func normalizeKeyword(kw string) string { return strings.TrimSpace(kw) }
