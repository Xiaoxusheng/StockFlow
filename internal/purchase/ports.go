package purchase

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/stock"
)

// 跨域消费接口与装配端口（backend-m2-plan §3/§3.1 唯一跨域机制：消费方定义最小接口，
// 实现由被消费域提供、router 装配经 Option 注入；域包之间禁止互相 import——
// 合法 import 面仅 internal/stock + internal/auth）。
//
// 规则（plan §3.1）：除 StockGateway 签名引用 stock 值类型外，其余接口签名一律内建类型，
// 实现方无需 import 本包即可经 router 闭包适配器结构化满足（M1 先例
// internal/router/router.go:96 binOccupancyBridge）。缺省 fail-closed：必需服务未注入时
// 业务拒绝（ErrCheckerMissing/ErrStockGatewayMissing/ErrExceptionServiceMissing），
// 杜绝静默跳过业务校验（plan §4.3 规则①）。

// StockGateway 库存原语消费接口（本域所需子集，plan §3 示例形态）：
// Putaway（上架落账）、InspectResult（质检结果 pending→available/defective）、
// EnsureBatch（收货批次采集）、SerialEvent（收货/上架序列号事件）。
// 实现由 inventory.NewService(db, rdb) 结构化满足——签名携带 stock 值类型，
// inventory 侧类型别名承接后（plan §8.3 条 4）即为恒等类型，router 可直接传参
// 或以闭包适配器逐字段桥接（收编前形态）。
type StockGateway interface {
	Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error)
	InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error)
	EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (batchID int64, created bool, err error)
	SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (serialID int64, created bool, err error)
}

// SupplierChecker 跨域消费接口：供应商存在且启用（api.md §4 业务关系校验；
// masterdata 侧实现，router 注入——plan §3.1）。
type SupplierChecker interface {
	ExistsActive(ctx context.Context, supplierID int64) (bool, error)
}

// WarehouseChecker 跨域消费接口：仓库存在且启用（收货仓校验；
// warehouse.NewChecker(db) 既有导出实现可直接注入）。
type WarehouseChecker interface {
	ExistsActive(ctx context.Context, warehouseID int64) (bool, error)
}

// SKUFlags SKU 三开关分支标记（business-flow §1.2：批次/效期/序列号开关决定
// 收货/质检/上架的业务分支；Enabled 供单据明细 SKU 启用校验）。
// 内建类型字段，router 桥接 masterdata 实现时逐字段构造（plan §3.1 SKUAttrReader）。
type SKUFlags struct {
	Enabled       bool
	BatchManaged  bool
	ExpiryManaged bool
	SerialManaged bool
}

// SKUAttrReader 跨域消费接口：读取 SKU 启用态与三开关（plan §3.1）。
// found=false 表示 SKU 不存在（业务关系校验失败）；err 为查询故障（fail-closed 拒绝）。
type SKUAttrReader interface {
	GetFlags(ctx context.Context, skuID int64) (flags SKUFlags, found bool, err error)
}

// BinChecker 跨域消费接口：库位存在、归属指定仓库且启用（上架目标库位校验；
// warehouse.NewBinChecker(db) 既有导出实现可直接注入——plan §3.1）。
type BinChecker interface {
	ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error)
}

// BinSuggestion 推荐库位候选（business-flow §5.3 基础规则：同 SKU 集中 + 剩余容量 +
// 库位启用；完整算法属阶段 18，plan §12 分级交付）。内建类型字段。
type BinSuggestion struct {
	BinID   int64   `json:"bin_id"`
	ZoneID  int64   `json:"zone_id"`
	ShelfID int64   `json:"shelf_id"`
	Score   float64 `json:"score"`  // 推荐序（降序）
	Reason  string  `json:"reason"` // 分配理由展示（§8.1 展示义务同源）
}

// BinRecommender 跨域消费接口：推荐库位（business-flow §5.3）。
// 数据面（库位容量/同 SKU 分布）属 warehouse/inventory 域，本域无权直读——
// 实现由 router 桥接两域数据后注入；未注入时上架任务必须显式指定目标库位
// （fail-closed，ErrBinRequired），不造假推荐。
type BinRecommender interface {
	Recommend(ctx context.Context, warehouseID, skuID int64, qty float64) ([]BinSuggestion, error)
}

// ExceptionCreator 跨域消费接口：异常登记统一进异常中心（business-flow §3.4/§11.2、
// plan §3.1；returns 域实现，router 注入）。签名内建类型：
// excType 为异常中心九类之一（收货场景固定"收货异常"），detail 为 JSON 文本
// （{"sub_type","sku_id","serial_no","description"...}——§3.4 七类异常子型与定位信息）。
// tx 形参：plan §10 要求异常联动与业务同事务（收货单据回滚则异常登记一并回滚）。
type ExceptionCreator interface {
	Create(ctx context.Context, tx *gorm.DB, excType, sourceType, sourceNo, detail string) (exceptionNo string, err error)
}

// Option RegisterRoutes 的可选注入项：仅用于跨域消费接口注入（plan §2.2/§5.2），
// 不得携带业务配置。
type Option func(*options)

type options struct {
	stock       StockGateway
	suppliers   SupplierChecker
	warehouses  WarehouseChecker
	skuAttrs    SKUAttrReader
	bins        BinChecker
	recommender BinRecommender
	exceptions  ExceptionCreator
}

// WithStock 注入库存原语网关（router 装配：inventory.Service 适配，收货/质检/上架必需）。
func WithStock(g StockGateway) Option { return func(o *options) { o.stock = g } }

// WithSupplierChecker 注入供应商校验（masterdata，采购订单创建必需）。
func WithSupplierChecker(c SupplierChecker) Option { return func(o *options) { o.suppliers = c } }

// WithWarehouseChecker 注入仓库校验（warehouse.NewChecker(db)，采购/入库创建必需）。
func WithWarehouseChecker(c WarehouseChecker) Option { return func(o *options) { o.warehouses = c } }

// WithSKUAttrReader 注入 SKU 三开关读取（masterdata，收货采集分支必需）。
func WithSKUAttrReader(r SKUAttrReader) Option { return func(o *options) { o.skuAttrs = r } }

// WithBinChecker 注入库位校验（warehouse.NewBinChecker(db)，上架确认必需）。
func WithBinChecker(c BinChecker) Option { return func(o *options) { o.bins = c } }

// WithBinRecommender 注入推荐库位（可选：未注入时上架任务须显式指定库位）。
func WithBinRecommender(r BinRecommender) Option { return func(o *options) { o.recommender = r } }

// WithExceptionCreator 注入异常中心创建（returns 域；异常收货必需）。
func WithExceptionCreator(c ExceptionCreator) Option { return func(o *options) { o.exceptions = c } }
