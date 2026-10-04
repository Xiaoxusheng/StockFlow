package stockops

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/stock"
)

// 跨域消费接口与装配端口（backend-m2-plan §3/§4.3 唯一跨域机制：消费方定义最小接口，
// 实现由被消费域提供、router 装配经 Option 注入）。
//
// 落位说明：方案 §3 规划 internal/stock 契约包承接 inventory 值类型（集成工程师 MT0
// 交付物）——截至本交付该包不存在（实测 internal/ 无 stock 目录），故本包直接引用
// internal/inventory 的值类型（Qty/RowKey/Actor/各 Op）定义消费接口；internal/stock
// 落地后改为别名承接（inventory 已规划 type Qty = stock.Qty 等零成本别名，机械改动）。

// StockGateway 库存原语消费接口（backend-m2-plan §7 消费映射的 stockops 子集：
// Lock/ReleaseLock/TransferOut/TransferIn/Adjust/SerialEvent）。*inventory.Service
// 结构化满足；router 注入 inventory.NewService(db, rdb, ...)。
type StockGateway interface {
	Lock(ctx context.Context, tx *gorm.DB, op stock.LockOp) (stock.MutationResult, error)
	ReleaseLock(ctx context.Context, tx *gorm.DB, op stock.ReleaseLockOp) (stock.MutationResult, error)
	TransferOut(ctx context.Context, tx *gorm.DB, op stock.TransferOutOp) (stock.MutationResult, error)
	TransferIn(ctx context.Context, tx *gorm.DB, op stock.TransferInOp) (stock.MutationResult, error)
	Adjust(ctx context.Context, tx *gorm.DB, op stock.AdjustOp) (stock.MutationResult, error)
	MoveBin(ctx context.Context, tx *gorm.DB, op stock.MoveBinOp) (stock.MutationResult, error)
	SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (serialID int64, created bool, err error)
}

// BinChecker 库位存在性校验（实现 warehouse.NewBinChecker(db)，router 注入；
// 实现方须校验库位归属指定仓库）。
type BinChecker interface {
	ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error)
}

// SKUChecker SKU 存在且启用（实现 masterdata.NewSKUChecker(db)，router 注入；
// api.md §4 业务关系校验）。
type SKUChecker interface {
	ExistsActive(ctx context.Context, skuID int64) (bool, error)
}

// SKUFlags SKU 三开关 + 启用位（business-flow §1.2：批次/效期/序列号开关决定业务分支）。
type SKUFlags struct {
	Enabled       bool
	BatchManaged  bool
	ExpiryManaged bool
	SerialManaged bool
}

// SKUFlagReader SKU 开关读取（backend-m2-plan §3.1 SKUAttrReader 同形窄接口的
// stockops 子集；盘点逐件冻结与调拨逐件出库需判定序列号管理 SKU——inventory-rules §8.2）。
// 实现方 masterdata（规划导出 NewSKUFlagReader）；未注入时依赖该开关的业务动作
// fail-closed 拒绝（ErrReaderMissing），不静默按非序列号 SKU 处理。
type SKUFlagReader interface {
	GetFlags(ctx context.Context, skuID int64) (SKUFlags, error)
}

// NumberIssuer 单据编号发放（business-flow §13.1 统一编号引擎 internal/docnum 的
// 消费窄接口；生产实现 docnumIssuer = docnum.NextWithRetry——取号与单据创建同事务，
// 撞唯一索引自动取下一号重试）。
type NumberIssuer interface {
	Issue(ctx context.Context, tx *gorm.DB, rule docnum.Rule, insert func(tx *gorm.DB, no string) error) (string, error)
}

// docnumIssuer 生产编号实现（内部经 docnum.NextWithRetry）。
type docnumIssuer struct{}

// Issue 实现 NumberIssuer。
func (docnumIssuer) Issue(ctx context.Context, tx *gorm.DB, rule docnum.Rule, insert func(tx *gorm.DB, no string) error) (string, error) {
	return docnum.NextWithRetry(ctx, tx, rule, insert)
}

// Option Service/RegisterRoutes 的可选注入项：仅用于跨域消费接口注入
// （plan §4.3），不得携带业务配置。
type Option func(*options)

type options struct {
	gateway StockGateway
	bins    BinChecker
	skus    SKUChecker
	flags   SKUFlagReader
	issuer  NumberIssuer
	store   Store
}

// WithGateway 注入库存原语网关（router：inventory.NewService(db, rdb, ...)）。必填。
func WithGateway(g StockGateway) Option { return func(o *options) { o.gateway = g } }

// WithBinChecker 注入库位存在性校验（router：warehouse.NewBinChecker(db)）。必填。
func WithBinChecker(c BinChecker) Option { return func(o *options) { o.bins = c } }

// WithSKUChecker 注入 SKU 存在性校验（router：masterdata.NewSKUChecker(db)）。必填。
func WithSKUChecker(c SKUChecker) Option { return func(o *options) { o.skus = c } }

// WithSKUFlagReader 注入 SKU 开关读取（router：masterdata 读取器，MT5 接线）。
// 缺省时序列号相关业务分支 fail-closed（ErrReaderMissing），启动不失败。
func WithSKUFlagReader(r SKUFlagReader) Option { return func(o *options) { o.flags = r } }

// WithNumberIssuer 注入编号发放实现（缺省 docnum.NextWithRetry；单测注入内存替身）。
func WithNumberIssuer(i NumberIssuer) Option { return func(o *options) { o.issuer = i } }

// WithStore 注入存储实现（缺省按 db 构造 gormStore；单测注入内存替身）。
func WithStore(s Store) Option { return func(o *options) { o.store = s } }

// Service 库存作业域服务（调拨单 + 盘点单；状态机与事务边界所在层，architecture §1）。
type Service struct {
	db      *gorm.DB
	store   Store
	gateway StockGateway
	bins    BinChecker
	skus    SKUChecker
	flags   SKUFlagReader
	issuer  NumberIssuer
}

// NewService 构造服务。store 缺省为 db 上的 gormStore；db 为 nil 时必须注入
// WithStore（单测形态），任何数据库访问方法将显式失败（不静默）。
func NewService(db *gorm.DB, opts ...Option) *Service {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	store := o.store
	if store == nil && db != nil {
		store = newGormStore(db)
	}
	issuer := o.issuer
	if issuer == nil {
		issuer = docnumIssuer{}
	}
	return &Service{
		db:      db,
		store:   store,
		gateway: o.gateway,
		bins:    o.bins,
		skus:    o.skus,
		flags:   o.flags,
		issuer:  issuer,
	}
}

// numbering rules（internal/docnum 冻结注册表取值，禁止散落字面量拼规则）。
var (
	transferNoRule = mustRule("TR")
	countNoRule    = mustRule("CK")
)

func mustRule(prefix string) docnum.Rule {
	r, ok := docnum.RuleFor(prefix)
	if !ok {
		panic("stockops: docnum 冻结注册表缺少前缀 " + prefix)
	}
	return r
}

// 来源单据类型（写入 inventory 流水 business_type / inventory_locks source_type /
// serial_numbers last_source_type 的统一口径，追溯经 business_no 串联）。
const (
	sourceTransfer = "transfer_order"
	sourceCount    = "count_order"
	// adjustSourceType 差异执行产生的调整单自身单据类型（序列号事件 last_source 用）。
	adjustSourceType = "inventory_adjustment"
)
