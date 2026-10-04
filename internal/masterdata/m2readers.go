package masterdata

// M2 跨域只读读取器（backend-m2-plan §3.1 消费方窄接口的实现侧导出——本域读自己的表，
// router 装配经 Option 注入 purchase/sales/stockops 各自的窄接口；实现方对消费方无感知，
// M1 先例 internal/masterdata/masterdata.go NewSKUChecker / internal/warehouse NewBinChecker）。
//
// 收编说明（MT5）：§3.1 冻结的三项 masterdata 侧导出——
//   - NewSKUFlagReader：SKU 启用态与批次/效期/序列号三开关（business-flow §1.2，
//     收货/上架/拣货/盘点序列号分支依据）；
//   - NewSupplierChecker / NewCustomerChecker：供应商/客户存在且启用（api.md §4 业务关系校验，
//     采购/销售订单创建校验）。
//
// 全部只读 SELECT，无任何写通路；接口签名只含内建类型与本域值类型（Flags 结构体），
// 消费方域类型（purchase.SKUFlags/sales.SKUFlags/stockops.SKUFlags）由 router 桥接
// 逐字段构造——域包之间禁止 import（plan §2.3 判据 2）。

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// SKUFlags SKU 启用态与三开关（business-flow §1.2）。内建类型字段；
// 约束与 skus 表一致：效期管理必须先启用批次管理（ExpiryManaged ⇒ BatchManaged）。
type SKUFlags struct {
	Enabled       bool
	BatchManaged  bool
	ExpiryManaged bool
	SerialManaged bool
}

// SKUFlagService SKU 开关读取实现（读本域 skus 表；GORM 软删作用域自动排除已删行）。
type SKUFlagService struct {
	db *gorm.DB
}

// NewSKUFlagReader 构建 SKU 开关读取器（router 装配：purchase/sales/stockops 注入）。
func NewSKUFlagReader(db *gorm.DB) *SKUFlagService { return &SKUFlagService{db: db} }

// GetFlags 读取 SKU 启用态与三开关。found=false 表示 SKU 不存在（未命中含已停用判定
// 交由消费方按 Enabled 分支拒绝）；db 故障时返回错误（fail-closed 由消费方决定拒绝路径）。
func (s *SKUFlagService) GetFlags(ctx context.Context, skuID int64) (SKUFlags, bool, error) {
	var row struct {
		IsEnabled       bool
		IsBatchManaged  bool
		IsExpiryManaged bool
		IsSerialManaged bool
	}
	err := s.db.WithContext(ctx).Model(&SKU{}).
		Select("is_enabled, is_batch_managed, is_expiry_managed, is_serial_managed").
		Where("id = ?", skuID).
		Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return SKUFlags{}, false, nil
		}
		return SKUFlags{}, false, fmt.Errorf("读取 SKU %d 开关失败: %w", skuID, err)
	}
	return SKUFlags{
		Enabled:       row.IsEnabled,
		BatchManaged:  row.IsBatchManaged,
		ExpiryManaged: row.IsExpiryManaged,
		SerialManaged: row.IsSerialManaged,
	}, true, nil
}

// SupplierCheckService 供应商可用性校验实现（读本域 suppliers 表；软删自动排除）。
type SupplierCheckService struct {
	db *gorm.DB
}

// NewSupplierChecker 构建供应商可用性校验器（router 装配：purchase.WithSupplierChecker）。
func NewSupplierChecker(db *gorm.DB) *SupplierCheckService { return &SupplierCheckService{db: db} }

// ExistsActive 判断供应商存在且启用（api.md §4 业务关系校验；未命中返回 false, nil）。
func (s *SupplierCheckService) ExistsActive(ctx context.Context, supplierID int64) (bool, error) {
	return existsActiveByID(ctx, s.db, &Supplier{}, supplierID, "供应商")
}

// CustomerCheckService 客户可用性校验实现（读本域 customers 表；软删自动排除）。
type CustomerCheckService struct {
	db *gorm.DB
}

// NewCustomerChecker 构建客户可用性校验器（router 装配：sales.WithCustomerChecker）。
func NewCustomerChecker(db *gorm.DB) *CustomerCheckService { return &CustomerCheckService{db: db} }

// ExistsActive 判断客户存在且启用（api.md §4 业务关系校验；未命中返回 false, nil）。
func (s *CustomerCheckService) ExistsActive(ctx context.Context, customerID int64) (bool, error) {
	return existsActiveByID(ctx, s.db, &Customer{}, customerID, "客户")
}

// existsActiveByID 按 ID 判定业务资料存在且启用（软删行由 GORM 作用域自动排除；
// db 故障返回错误，消费方 fail-closed）。
func existsActiveByID(ctx context.Context, db *gorm.DB, model any, id int64, label string) (bool, error) {
	var n int64
	err := db.WithContext(ctx).Model(model).
		Where("id = ? AND status = ?", id, StatusEnabled).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("校验%s %d 可用性失败: %w", label, id, err)
	}
	return n > 0, nil
}
