package masterdata

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 供应商与客户服务（business-flow §1.4/§1.5；backend-m1-plan §6.2 suppliers/customers 表）。
//
// 生命周期：软删除（database.md §5.1）+ 启停。供应商删除的业务规则——"不能删除
// 已经产生业务记录的供应商，应使用停用"（business-flow §1.4）——的引用校验依赖
// 采购/入库单据表，随 M2 交付（backend-m1-plan §6.2 迁移注释同口径）；M1 删除
// 仅落软删标记，历史追溯由 operation_logs 审计链兜底。

// ---- 视图 DTO ----

// SupplierView 供应商视图。
type SupplierView struct {
	ID        database.ID       `json:"id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Contact   string            `json:"contact"`
	Phone     string            `json:"phone"`
	Email     string            `json:"email"`
	Address   string            `json:"address"`
	Status    string            `json:"status"`
	Remark    string            `json:"remark"`
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
}

func viewSupplier(s *Supplier) *SupplierView {
	return &SupplierView{
		ID:        s.ID,
		Code:      s.Code,
		Name:      s.Name,
		Contact:   s.Contact,
		Phone:     s.Phone,
		Email:     s.Email,
		Address:   s.Address,
		Status:    s.Status,
		Remark:    s.Remark,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// CustomerView 客户视图。
type CustomerView struct {
	ID              database.ID       `json:"id"`
	Code            string            `json:"code"`
	Name            string            `json:"name"`
	Contact         string            `json:"contact"`
	Phone           string            `json:"phone"`
	Email           string            `json:"email"`
	Address         string            `json:"address"`
	ShippingAddress string            `json:"shipping_address"`
	Status          string            `json:"status"`
	CreatedAt       database.JSONTime `json:"created_at"`
	UpdatedAt       database.JSONTime `json:"updated_at"`
}

func viewCustomer(s *Customer) *CustomerView {
	return &CustomerView{
		ID:              s.ID,
		Code:            s.Code,
		Name:            s.Name,
		Contact:         s.Contact,
		Phone:           s.Phone,
		Email:           s.Email,
		Address:         s.Address,
		ShippingAddress: s.ShippingAddress,
		Status:          s.Status,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
}

// ---- 输入 DTO ----

// SupplierCreateInput 创建供应商入参。
type SupplierCreateInput struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
	Address string `json:"address"`
	Remark  string `json:"remark"`
}

// SupplierUpdateInput 更新供应商入参（编码不可改；指针三态）。
type SupplierUpdateInput struct {
	Name    *string `json:"name"`
	Contact *string `json:"contact"`
	Phone   *string `json:"phone"`
	Email   *string `json:"email"`
	Address *string `json:"address"`
	Remark  *string `json:"remark"`
}

// CustomerCreateInput 创建客户入参。
type CustomerCreateInput struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Contact         string `json:"contact"`
	Phone           string `json:"phone"`
	Email           string `json:"email"`
	Address         string `json:"address"`
	ShippingAddress string `json:"shipping_address"`
}

// CustomerUpdateInput 更新客户入参（编码不可改；指针三态）。
type CustomerUpdateInput struct {
	Name            *string `json:"name"`
	Contact         *string `json:"contact"`
	Phone           *string `json:"phone"`
	Email           *string `json:"email"`
	Address         *string `json:"address"`
	ShippingAddress *string `json:"shipping_address"`
}

// ---- 共用校验 ----

// validatePartnerContact 联系人/电话/邮箱/地址校验（business-flow §1.4/§1.5 字段）。
func validatePartnerContact(contact, phone, email, address, addressField string) error {
	if err := validateOptionalText("contact", contact, 64); err != nil {
		return err
	}
	if err := validatePhoneEmail("phone", phone); err != nil {
		return err
	}
	if err := validatePhoneEmail("email", email); err != nil {
		return err
	}
	return validateOptionalText(addressField, address, 512)
}

// ---- 供应商 ----

// CreateSupplier 创建供应商。
func (s *Service) CreateSupplier(ctx context.Context, actor Actor, in SupplierCreateInput) (*SupplierView, error) {
	if err := validateCode("code", in.Code, false); err != nil {
		return nil, err
	}
	if err := validateName("name", in.Name, 255); err != nil {
		return nil, err
	}
	if err := validatePartnerContact(in.Contact, in.Phone, in.Email, in.Address, "address"); err != nil {
		return nil, err
	}
	if err := validateOptionalText("remark", in.Remark, 2000); err != nil {
		return nil, err
	}
	if exist, err := s.repo.FindSupplierByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrSupplierCodeExists, map[string]any{"code": in.Code})
	}

	sup := &Supplier{
		Code:    in.Code,
		Name:    in.Name,
		Contact: in.Contact,
		Phone:   in.Phone,
		Email:   in.Email,
		Address: in.Address,
		Status:  StatusEnabled,
		Remark:  in.Remark,
	}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertSupplier(ctx, tx, sup); err != nil {
			return err
		}
		e := actor.auditEntry("supplier", sup.ID.Int64(), "create")
		e.Success = true
		e.After = viewSupplier(sup)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewSupplier(sup), nil
}

// UpdateSupplier 更新供应商。
func (s *Service) UpdateSupplier(ctx context.Context, actor Actor, id int64, in SupplierUpdateInput) (*SupplierView, error) {
	sup, err := s.repo.FindSupplierByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, response.NewError(ErrSupplierNotFound, map[string]any{"id": id})
	}
	before := viewSupplier(sup)

	contact, phone, email, addr := sup.Contact, sup.Phone, sup.Email, sup.Address
	if in.Name != nil {
		if err := validateName("name", *in.Name, 255); err != nil {
			return nil, err
		}
		sup.Name = *in.Name
	}
	if in.Contact != nil {
		contact = *in.Contact
	}
	if in.Phone != nil {
		phone = *in.Phone
	}
	if in.Email != nil {
		email = *in.Email
	}
	if in.Address != nil {
		addr = *in.Address
	}
	if err := validatePartnerContact(contact, phone, email, addr, "address"); err != nil {
		return nil, err
	}
	if in.Remark != nil {
		if err := validateOptionalText("remark", *in.Remark, 2000); err != nil {
			return nil, err
		}
		sup.Remark = *in.Remark
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.Name != nil {
		cols["name"] = sup.Name
	}
	if in.Contact != nil {
		sup.Contact = contact
		cols["contact"] = contact
	}
	if in.Phone != nil {
		sup.Phone = phone
		cols["phone"] = phone
	}
	if in.Email != nil {
		sup.Email = email
		cols["email"] = email
	}
	if in.Address != nil {
		sup.Address = addr
		cols["address"] = addr
	}
	if in.Remark != nil {
		cols["remark"] = sup.Remark
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateSupplierCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("supplier", id, "update")
		e.Success = true
		e.Before = before
		e.After = viewSupplier(sup)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewSupplier(sup), nil
}

// GetSupplier 供应商详情。
func (s *Service) GetSupplier(ctx context.Context, id int64) (*SupplierView, error) {
	sup, err := s.repo.FindSupplierByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, response.NewError(ErrSupplierNotFound, map[string]any{"id": id})
	}
	return viewSupplier(sup), nil
}

// ListSuppliers 供应商分页列表。
func (s *Service) ListSuppliers(ctx context.Context, f PartnerListFilter) ([]*SupplierView, int64, error) {
	items, total, err := s.repo.ListSuppliers(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*SupplierView, 0, len(items))
	for _, it := range items {
		views = append(views, viewSupplier(it))
	}
	return views, total, nil
}

// UpdateSupplierStatus 启用/停用供应商（停用强制审计，backend-m1-plan §4.4）。
func (s *Service) UpdateSupplierStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return invalidParam("status", "必须为 ENABLED 或 DISABLED")
	}
	sup, err := s.repo.FindSupplierByID(ctx, id)
	if err != nil {
		return err
	}
	if sup == nil {
		return response.NewError(ErrSupplierNotFound, map[string]any{"id": id})
	}
	before := viewSupplier(sup)
	sup.Status = status

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateSupplierCols(ctx, tx, id, map[string]any{
			"status":     status,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("supplier", id, "status")
		e.Success = true
		e.Before = before
		e.After = viewSupplier(sup)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}

// DeleteSupplier 软删除供应商。
// business-flow §1.4"已产生业务记录不可删"的引用校验依赖 M2 采购单据表（本域
// 禁止跨域直查，backend-m1-plan §4.2 判据 9）；M1 由审计链追溯，见文件头注释。
func (s *Service) DeleteSupplier(ctx context.Context, actor Actor, id int64) error {
	sup, err := s.repo.FindSupplierByID(ctx, id)
	if err != nil {
		return err
	}
	if sup == nil {
		return response.NewError(ErrSupplierNotFound, map[string]any{"id": id})
	}
	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteSupplier(ctx, tx, id, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("supplier", id, "delete")
		e.Success = true
		e.Before = viewSupplier(sup)
		return middleware.Audit(tx, e)
	})
}

// ---- 客户 ----

// CreateCustomer 创建客户。
func (s *Service) CreateCustomer(ctx context.Context, actor Actor, in CustomerCreateInput) (*CustomerView, error) {
	if err := validateCode("code", in.Code, false); err != nil {
		return nil, err
	}
	if err := validateName("name", in.Name, 255); err != nil {
		return nil, err
	}
	if err := validatePartnerContact(in.Contact, in.Phone, in.Email, in.Address, "address"); err != nil {
		return nil, err
	}
	if err := validateOptionalText("shipping_address", in.ShippingAddress, 512); err != nil {
		return nil, err
	}
	if exist, err := s.repo.FindCustomerByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrCustomerCodeExists, map[string]any{"code": in.Code})
	}

	cus := &Customer{
		Code:            in.Code,
		Name:            in.Name,
		Contact:         in.Contact,
		Phone:           in.Phone,
		Email:           in.Email,
		Address:         in.Address,
		ShippingAddress: in.ShippingAddress,
		Status:          StatusEnabled,
	}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertCustomer(ctx, tx, cus); err != nil {
			return err
		}
		e := actor.auditEntry("customer", cus.ID.Int64(), "create")
		e.Success = true
		e.After = viewCustomer(cus)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewCustomer(cus), nil
}

// UpdateCustomer 更新客户。
func (s *Service) UpdateCustomer(ctx context.Context, actor Actor, id int64, in CustomerUpdateInput) (*CustomerView, error) {
	cus, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cus == nil {
		return nil, response.NewError(ErrCustomerNotFound, map[string]any{"id": id})
	}
	before := viewCustomer(cus)

	contact, phone, email, addr := cus.Contact, cus.Phone, cus.Email, cus.Address
	shipping := cus.ShippingAddress
	if in.Name != nil {
		if err := validateName("name", *in.Name, 255); err != nil {
			return nil, err
		}
		cus.Name = *in.Name
	}
	if in.Contact != nil {
		contact = *in.Contact
	}
	if in.Phone != nil {
		phone = *in.Phone
	}
	if in.Email != nil {
		email = *in.Email
	}
	if in.Address != nil {
		addr = *in.Address
	}
	if in.ShippingAddress != nil {
		shipping = *in.ShippingAddress
	}
	if err := validatePartnerContact(contact, phone, email, addr, "address"); err != nil {
		return nil, err
	}
	if err := validateOptionalText("shipping_address", shipping, 512); err != nil {
		return nil, err
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.Name != nil {
		cols["name"] = cus.Name
	}
	if in.Contact != nil {
		cus.Contact = contact
		cols["contact"] = contact
	}
	if in.Phone != nil {
		cus.Phone = phone
		cols["phone"] = phone
	}
	if in.Email != nil {
		cus.Email = email
		cols["email"] = email
	}
	if in.Address != nil {
		cus.Address = addr
		cols["address"] = addr
	}
	if in.ShippingAddress != nil {
		cus.ShippingAddress = shipping
		cols["shipping_address"] = shipping
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateCustomerCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("customer", id, "update")
		e.Success = true
		e.Before = before
		e.After = viewCustomer(cus)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewCustomer(cus), nil
}

// GetCustomer 客户详情。
func (s *Service) GetCustomer(ctx context.Context, id int64) (*CustomerView, error) {
	cus, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cus == nil {
		return nil, response.NewError(ErrCustomerNotFound, map[string]any{"id": id})
	}
	return viewCustomer(cus), nil
}

// ListCustomers 客户分页列表。
func (s *Service) ListCustomers(ctx context.Context, f PartnerListFilter) ([]*CustomerView, int64, error) {
	items, total, err := s.repo.ListCustomers(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*CustomerView, 0, len(items))
	for _, it := range items {
		views = append(views, viewCustomer(it))
	}
	return views, total, nil
}

// UpdateCustomerStatus 启用/停用客户（停用强制审计，backend-m1-plan §4.4）。
func (s *Service) UpdateCustomerStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return invalidParam("status", "必须为 ENABLED 或 DISABLED")
	}
	cus, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return err
	}
	if cus == nil {
		return response.NewError(ErrCustomerNotFound, map[string]any{"id": id})
	}
	before := viewCustomer(cus)
	cus.Status = status

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateCustomerCols(ctx, tx, id, map[string]any{
			"status":     status,
			"updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("customer", id, "status")
		e.Success = true
		e.Before = before
		e.After = viewCustomer(cus)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}

// DeleteCustomer 软删除客户（销售单据引用校验随 M2，理由同 DeleteSupplier）。
func (s *Service) DeleteCustomer(ctx context.Context, actor Actor, id int64) error {
	cus, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return err
	}
	if cus == nil {
		return response.NewError(ErrCustomerNotFound, map[string]any{"id": id})
	}
	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.SoftDeleteCustomer(ctx, tx, id, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("customer", id, "delete")
		e.Success = true
		e.Before = viewCustomer(cus)
		return middleware.Audit(tx, e)
	})
}
