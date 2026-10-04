package sales

// 客户业务记录引用读取（masterdata.CustomerRefReader 窄接口的实现侧导出——
// 本域只读自己的 sales_orders 表，router 装配注入 masterdata.DeleteCustomer
// 引用校验；backend-m1-plan §4.3 消费方窄接口机制，先例 internal/masterdata/m2readers.go）。
//
// 业务依据：business-flow §1.5 客户"历史订单、历史出库"能力 + "已影响库存的单据不能
// 直接删除"总则（§15），与供应商删除校验（§1.4）同口径——后端裁决 2026-10-04 补齐。
// 判定口径：任何未软删销售单（含 CANCELLED——取消单仍属历史业务证据）即视为已产生
// 业务记录。

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// CustomerOrderRefService 客户销售业务记录读取实现。
type CustomerOrderRefService struct {
	db *gorm.DB
}

// NewCustomerRefReader 构建（router 装配：masterdata.WithCustomerRefReader(sales.NewCustomerRefReader(db))）。
func NewCustomerRefReader(db *gorm.DB) *CustomerOrderRefService {
	return &CustomerOrderRefService{db: db}
}

// HasBusinessRecord 实现 masterdata.CustomerRefReader。db 故障返回错误（消费方 fail-closed）。
func (s *CustomerOrderRefService) HasBusinessRecord(ctx context.Context, customerID int64) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&SalesOrder{}).
		Where("customer_id = ?", customerID).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("校验客户 %d 销售业务记录失败: %w", customerID, err)
	}
	return n > 0, nil
}
