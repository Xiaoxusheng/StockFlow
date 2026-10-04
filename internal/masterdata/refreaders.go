package masterdata

import "context"

// 往来单位删除引用校验的消费侧窄接口（backend-m1-plan §4.3 唯一跨域机制：本域定义
// 最小消费接口，实现由被消费域提供——purchase/sales 各自只读自己的单据表，router
// 装配经 Option 注入；域包之间禁止 import，本域无"只读 SELECT 他人表"旁路，
// backend-m1-plan §4.2 判据 9）。
//
// 业务依据：business-flow §1.4"不能删除已经产生业务记录的供应商，应使用停用"
// （DeleteSupplier）；§1.5 客户列"历史订单、历史出库"并受"已影响库存的单据不能直接
// 删除"总则约束（DeleteCustomer 同口径补齐，后端裁决 2026-10-04）。
//
// 未注入时（RegisterRoutes 无对应 Option）删除引用校验跳过——fail-open；router 唯一
// 装配点恒注入（2026-10-04 补齐），缺注入属装配回归，由装配评审把关。选择 fail-open
// 而非启动 panic 的原因：RegisterRoutes 三参冻结签名被多处测试装配复用（无业务 DB
// 场景），panic 会扩大冻结契约的破坏面；删除为软删+审计可恢复动作，非安全边界。

// SupplierRefReader 供应商业务记录引用判定接口（实现方：purchase 域，读自己的采购单表）。
type SupplierRefReader interface {
	// HasBusinessRecord 供应商存在任何（未软删）采购单即视为已产生业务记录，
	// 含 CANCELLED——取消单仍属历史业务证据。db 故障返回错误（调用方原样上抛，
	// 拒绝删除路径静默放行）。
	HasBusinessRecord(ctx context.Context, supplierID int64) (bool, error)
}

// CustomerRefReader 客户业务记录引用判定接口（实现方：sales 域，读自己的销售单表）。
type CustomerRefReader interface {
	// HasBusinessRecord 客户存在任何（未软删）销售单即视为已产生业务记录（语义同上）。
	HasBusinessRecord(ctx context.Context, customerID int64) (bool, error)
}

// WithSupplierRefReader 注入供应商业务记录引用读取（router 装配：purchase.NewSupplierRefReader）。
func WithSupplierRefReader(r SupplierRefReader) Option {
	return func(o *options) { o.supplierRefs = r }
}

// WithCustomerRefReader 注入客户业务记录引用读取（router 装配：sales.NewCustomerRefReader）。
func WithCustomerRefReader(r CustomerRefReader) Option {
	return func(o *options) { o.customerRefs = r }
}
