package sales

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/stock"
)

// 跨域契约端口（backend-m2-plan §3/§3.1：消费方在本包内定义最小窄接口，
// router 装配注入；域间合法 import 仅 internal/stock 与 internal/auth）。
//
// 库存原语消费形态（plan §3 冻结设计）：方法签名只引用 internal/stock 值类型——
// 叶子契约包（仅标准库 + database），本域唯一合法的库存值类型 import 面。
// 下方以类型别名承接 stock 冻结形状（Qty/Actor/Source/RowKey 与本域所需四个
// 原语 op/结果类型）；internal/inventory 完成同形别名承接（plan §8.3 条 4）后，
// *inventory.Service 即结构化满足本域 StockGateway，router 无需适配器。
//
// 说明：本域实现启动时 internal/stock 尚未交付，曾以本地镜像承载冻结形状；
// stock 落地后已按预留切换点改为别名（单文件切换，业务代码零改动）——
// 这正是 plan §3 "形状原样搬迁 + 别名承接"设计的执行路径。

// ---- 冻结值类型（别名承接 internal/stock，plan §3） ----

type (
	Qty            = stock.Qty
	Actor          = stock.Actor
	Source         = stock.Source
	RowKey         = stock.RowKey
	MutationResult = stock.MutationResult
	LedgerRef      = stock.LedgerRef
	LockOp         = stock.LockOp
	ReleaseLockOp  = stock.ReleaseLockOp
	DeductOp       = stock.DeductOp
	SerialOp       = stock.SerialOp
)

// qtyScale numeric(18,4) 标度（stock.Qty 内部标度同名约定；本域金额计算与
// 序列号件数换算使用，与 internal/stock/qty.go 的 qtyScale 同值 1e-4）。
const qtyScale int64 = 10000

// ---- 消费方窄接口（plan §3.1：接口定义在消费方、router 注入、缺省 fail-closed） ----

// StockGateway 库存原语窄接口（本域所需子集：Lock/ReleaseLock/Deduct/SerialEvent——
// plan §3.1 允许按域裁剪；销售域不产生入库，无需 Putaway/EnsureBatch/InspectResult）。
// 实现方 = inventory.NewService(db, rdb)（router 经适配器注入）。
type StockGateway interface {
	Lock(ctx context.Context, tx *gorm.DB, op LockOp) (MutationResult, error)
	ReleaseLock(ctx context.Context, tx *gorm.DB, op ReleaseLockOp) (MutationResult, error)
	Deduct(ctx context.Context, tx *gorm.DB, op DeductOp) (MutationResult, error)
	SerialEvent(ctx context.Context, tx *gorm.DB, op SerialOp) (serialID int64, created bool, err error)
}

// SKUFlags SKU 三开关 + 启用位（business-flow §1.2 批次/效期/序列号开关；收货/上架/
// 拣货的分支依据，plan §3.1 sales.SKUAttrReader）。约束：效期管理必须先启用批次管理
// （masterdata assertFlagsBatchExpiry），故 ExpiryManaged ⇒ BatchManaged。
type SKUFlags struct {
	Enabled       bool `json:"enabled"`
	BatchManaged  bool `json:"batch_managed"`
	ExpiryManaged bool `json:"expiry_managed"`
	SerialManaged bool `json:"serial_managed"`
}

// SKUAttrReader SKU 开关读取（plan §3.1：masterdata 导出 NewSKUFlagReader(db) 实现，
// router 注入；实现方读取自身 skus 表）。
type SKUAttrReader interface {
	GetFlags(ctx context.Context, skuID int64) (SKUFlags, error)
}

// CustomerChecker 客户存在且启用（plan §3.1：masterdata 实现注入；销售订单
// 业务关系校验，api.md §4）。
type CustomerChecker interface {
	ExistsActive(ctx context.Context, customerID int64) (bool, error)
}

// BinChecker 库位存在且启用（plan §3.1：warehouse.NewBinChecker(db) 注入；
// 分配候选的库位必须在出库仓内有效）。
type BinChecker interface {
	ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error)
}

// ExceptionDetail 异常登记定位信息（plan §3.1 sales.ExceptionCreator 的 detail 载荷，
// 全部内建类型，不携带域类型）。
type ExceptionDetail struct {
	SKUID    int64          `json:"sku_id,omitempty"`
	BinID    int64          `json:"bin_id,omitempty"`
	BatchID  int64          `json:"batch_id,omitempty"`
	SerialNo string         `json:"serial_no,omitempty"`
	LineNo   int64          `json:"line_no,omitempty"`
	Reason   string         `json:"reason"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// ExceptionCreator 异常中心创建窄接口（plan §3.1：returns 域实现注入；拣货缺货/少货
// 上报、复核五类异常统一进异常中心，business-flow §8.2/§8.3、§11.2）。
// 返回异常单号；tx 非 nil 时异常登记与业务同事务（整体回滚）。
type ExceptionCreator interface {
	Create(ctx context.Context, tx *gorm.DB, excType, sourceType, sourceNo string, detail ExceptionDetail) (exceptionNo string, err error)
}

// AuditFunc 审计写入函数缝（签名 = middleware.Audit）。Service 构造时默认绑定
// middleware.Audit（业务事务内写 operation_logs，plan §2.3 判据 6——生产路径恒经
// middleware.Audit，不存在第二套审计写入）；仅单测注入内存探针以摆脱 gorm 依赖。
type AuditFunc func(tx *gorm.DB, e middleware.AuditEntry) error

// NumberIssuer 单据编号发放函数（生产绑定 internal/docnum——plan §2.3 判据 5 禁止
// 绕过编号引擎；单测注入内存计数器。tx 为当前业务事务，发放必须与单据创建同事务，
// business-flow §13.1）。
type NumberIssuer func(ctx context.Context, tx *gorm.DB, prefix string) (string, error)

// ---- Option 注入（plan §4.3/§5.2：仅用于跨域消费接口注入，不得携带业务配置） ----

// Option RegisterRoutes/NewService 的可选注入项。
type Option func(*options)

type options struct {
	stock      StockGateway
	skus       SKUAttrReader
	customers  CustomerChecker
	bins       BinChecker
	exceptions ExceptionCreator
	audit      AuditFunc
	nextNo     NumberIssuer
}

// WithStock 注入库存原语网关（router 装配：inventory.NewService 适配器）。
func WithStock(g StockGateway) Option { return func(o *options) { o.stock = g } }

// WithSKUAttr 注入 SKU 开关读取（router 装配：masterdata.NewSKUFlagReader 适配器）。
func WithSKUAttr(r SKUAttrReader) Option { return func(o *options) { o.skus = r } }

// WithCustomerChecker 注入客户校验（router 装配：masterdata 实现适配器）。
func WithCustomerChecker(c CustomerChecker) Option { return func(o *options) { o.customers = c } }

// WithBinChecker 注入库位校验（router 装配：warehouse.NewBinChecker 适配器）。
func WithBinChecker(c BinChecker) Option { return func(o *options) { o.bins = c } }

// WithExceptions 注入异常中心创建（router 装配：returns 域实现适配器；
// 可选注入——缺位时异常上报 fail-closed 拒绝，见 ErrExceptionsNotWired）。
func WithExceptions(e ExceptionCreator) Option { return func(o *options) { o.exceptions = e } }

// WithAudit 覆盖审计写入（生产留空 = 默认 middleware.Audit；仅测试注入探针）。
func WithAudit(f AuditFunc) Option { return func(o *options) { o.audit = f } }

// WithNumbers 覆盖单号发放（生产留空 = 默认 internal/docnum；仅测试注入计数器）。
func WithNumbers(f NumberIssuer) Option { return func(o *options) { o.nextNo = f } }
