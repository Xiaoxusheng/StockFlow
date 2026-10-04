package purchase

// 供应商业务记录引用读取（masterdata.SupplierRefReader 窄接口的实现侧导出——
// 本域只读自己的 purchase_orders 表，router 装配注入 masterdata.DeleteSupplier
// 引用校验；backend-m1-plan §4.3 消费方窄接口机制，先例 internal/masterdata/m2readers.go）。
//
// 业务依据：business-flow §1.4"不能删除已经产生业务记录的供应商，应使用停用"。
// 判定口径：任何未软删采购单（含 CANCELLED——取消单仍属历史业务证据）即视为已产生
// 业务记录；GORM 软删作用域自动排除已删行（000016 起 purchase_orders 带 deleted_at）。

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// SupplierPurchaseRefService 供应商采购业务记录读取实现。
type SupplierPurchaseRefService struct {
	db *gorm.DB
}

// NewSupplierRefReader 构建（router 装配：masterdata.WithSupplierRefReader(purchase.NewSupplierRefReader(db))）。
func NewSupplierRefReader(db *gorm.DB) *SupplierPurchaseRefService {
	return &SupplierPurchaseRefService{db: db}
}

// HasBusinessRecord 实现 masterdata.SupplierRefReader。db 故障返回错误（消费方 fail-closed）。
func (s *SupplierPurchaseRefService) HasBusinessRecord(ctx context.Context, supplierID int64) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&PurchaseOrder{}).
		Where("supplier_id = ?", supplierID).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("校验供应商 %d 采购业务记录失败: %w", supplierID, err)
	}
	return n > 0, nil
}
