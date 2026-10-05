package devices

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/auth"
)

// 跨域窄接口与装配端口（backend-m3-plan §12.2 唯一跨域机制：消费方在包内定义最小接口，
// 实现由被消费域的 devices_resolve.go 冻结清单文件提供、router 装配经 Option 注入；
// 域包之间禁止 import——签名一律内建类型或本包值类型，不携带域类型）。
//
// WarehouseChecker 复用既有 auth.WarehouseChecker 载体（auth/middleware.go 冻结契约，
// warehouse.NewChecker 实现）：devices.warehouse_id 为裸 ID 引用 + Service 层校验
// （plan §5 000013 注），不新造跨域接口（plan §12.2：新增跨域需求必须先进窄接口清单评审）。

// WarehouseChecker 仓库存在性校验（auth.WarehouseChecker 别名承接，装配同形）。
type WarehouseChecker = auth.WarehouseChecker

// Hit 解析命中对象（匹配器返回形态；全部内建类型）。
type Hit struct {
	// ID 对象主键（库内 bigserial）。
	ID int64
	// Code 对象编码：单据号 / SKU 编码 / 库位编码 / 序列号 / 批次号。
	Code string
	// Name 展示名：商品名 / 单据类型+单号 / 库位全码 / 批次描述。
	Name string
	// WarehouseID 归属仓库 ID（0=不适用或未定位）。
	WarehouseID int64
	// Status 对象状态（SKU 启用位/库位状态/序列号台账状态/单据状态；""=不适用）。
	Status string
	// DocKind 单据业务前缀（仅 DocFinder 命中携带，如 PO/TR）。
	DocKind string
}

// SKUBarcodeReader SKU 条码读取（匹配器 2——scanner.md §5.2；实现 masterdata/devices_resolve.go：
// barcodes 表唯一索引精确命中，uk_barcodes_barcode 一码一 SKU）。
type SKUBarcodeReader interface {
	// FindByBarcode 按条码取 SKU 命中；found=false 表示未命中（管线继续后续匹配器）。
	FindByBarcode(ctx context.Context, code string) (Hit, bool, error)
}

// BinCodeReader 库位码读取（匹配器 3；实现 warehouse/devices_resolve.go）。
type BinCodeReader interface {
	// FindByCode 按库位编码取命中列表——库位编码仓内唯一（uk_bins_warehouse_code），
	// 跨仓同码返回多命中，由前端选择（plan §8.3 歧义消解）。空列表 = 未命中。
	FindByCode(ctx context.Context, code string) ([]Hit, error)
}

// DocFinder 单据号读取（匹配器 1；实现 purchase/sales/stockops/returns 各自 devices_resolve.go，
// 按 docnum frozenRules 业务前缀 15 值分派——前缀→实现映射见 docPrefixOwners 单一冻结表）。
type DocFinder interface {
	// FindByNo 按单号取存在性与摘要；found=false 表示单号不存在。
	FindByNo(ctx context.Context, docNo string) (Hit, bool, error)
}

// SerialReader 序列号读取（匹配器 4；实现 inventory/devices_resolve.go：
// serial_numbers 全局唯一，uk_serial_numbers_serial_no）。
type SerialReader interface {
	// FindSerial 按序列号取台账命中；found=false 表示不存在。
	FindSerial(ctx context.Context, sn string) (Hit, bool, error)
}

// BatchReader 批次码读取（匹配器 5；实现 inventory/devices_resolve.go）。
type BatchReader interface {
	// FindBatch 按批次号取命中列表——批次号 SKU 内唯一（uk_batches_sku_batch_no），
	// 跨 SKU 同批次号返回多命中（plan §8.3）。空列表 = 未命中。
	FindBatch(ctx context.Context, batchNo string) ([]Hit, error)
}

// SfqrSkuReader SFQR 载荷 SKU 读取（resolve 管线第 0 段——docs/qr-code.md §6；
// 实现 masterdata/devices_resolve.go：skus.code 精确命中 JOIN products，双
// deleted_at IS NULL 对齐 FindByBarcode 口径。沿 plan §12.2「新增匹配 = 接口 +
// 实现 + Option + 装配」四点模式）。
type SfqrSkuReader interface {
	// FindBySkuCode 按 SKU 编码取命中；found=false 表示未命中/软删
	// （Hit.Status 携带 SKU 启用位，停用由消费方转 SKU_NOT_FOUND）。
	FindBySkuCode(ctx context.Context, code string) (Hit, bool, error)
}

// docPrefixOwners 单据前缀 → DocFinder 槽位单一冻结映射（plan §8.3 条 1/§12.2：
// purchase=IN/PO/QC/RC/PW、sales=SO/OUT/PK/CH/BP/SH、stockops=TR/CK、returns=RT/EX；
// LED/ADJ 不可扫（M1 存量前缀承接），IMP/EXP/PT 为任务号不可扫——均不入本表，
// 落到后续匹配器，最终 UNKNOWN_BARCODE）。
var docPrefixOwners = map[string]string{
	"IN":  "purchase",
	"PO":  "purchase",
	"QC":  "purchase",
	"RC":  "purchase",
	"PW":  "purchase",
	"SO":  "sales",
	"OUT": "sales",
	"PK":  "sales",
	"CH":  "sales",
	"BP":  "sales", // 箱码 → sales 域 packing_records（复用 M2 包裹，plan §8.3）
	"SH":  "sales",
	"TR":  "stockops",
	"CK":  "stockops",
	"RT":  "returns",
	"EX":  "returns",
}

// taskDocKinds 任务类单据前缀（命中但对象不存在 → TASK_NOT_FOUND；其余单据前缀
// → ORDER_NOT_FOUND——scanner.md §6.1 错误码字面）。
var taskDocKinds = map[string]bool{"PW": true, "PK": true, "CH": true}

// ---- Option 装配（plan §3.1：仅用于跨域消费接口注入与装配参数，不得携带业务配置）----

type options struct {
	skuBarcodes  SKUBarcodeReader
	bins         BinCodeReader
	serials      SerialReader
	batches      BatchReader
	sfqrSkus     SfqrSkuReader
	purchaseDocs DocFinder
	salesDocs    DocFinder
	stockopsDocs DocFinder
	returnsDocs  DocFinder
	whChecker    WarehouseChecker
	deviceAPI    *gin.RouterGroup // 设备端挂载组（必须不含 auth.AuthRequired，见 doc.go）
	serverURL    string           // 激活二维码 server_url（缺省取请求 Host 推导）
	dedupTTL     time.Duration    // §6.6 去重窗口时长（0=默认 2s）
	logger       *zap.Logger      // scan_logs 降级写失败日志（nil=跳过，单测场景）
}

// Option RegisterRoutes 的可选注入项。
type Option func(*options)

// WithSKUBarcodes 注入 SKU 条码读取实现（router 装配：masterdata.NewSKUBarcodeReader）。
func WithSKUBarcodes(r SKUBarcodeReader) Option { return func(o *options) { o.skuBarcodes = r } }

// WithBins 注入库位码读取实现（router 装配：warehouse.NewBinCodeReader）。
func WithBins(r BinCodeReader) Option { return func(o *options) { o.bins = r } }

// WithSerials 注入序列号读取实现（router 装配：inventory.NewSerialReader）。
func WithSerials(r SerialReader) Option { return func(o *options) { o.serials = r } }

// WithBatches 注入批次码读取实现（router 装配：inventory.NewBatchReader）。
func WithBatches(r BatchReader) Option { return func(o *options) { o.batches = r } }

// WithSfqrSkus 注入 SFQR 载荷 SKU 读取实现（router 装配：masterdata.NewSKUBarcodeReader——
// 同一实现双接口，qr-code.md §6；必需——缺位启动期 panic，plan §3.1 规则①）。
func WithSfqrSkus(r SfqrSkuReader) Option { return func(o *options) { o.sfqrSkus = r } }

// WithPurchaseDocs 注入采购/入库域单据读取（IN/PO/QC/RC/PW）。
func WithPurchaseDocs(r DocFinder) Option { return func(o *options) { o.purchaseDocs = r } }

// WithSalesDocs 注入销售/出库域单据读取（SO/OUT/PK/CH/BP/SH）。
func WithSalesDocs(r DocFinder) Option { return func(o *options) { o.salesDocs = r } }

// WithStockopsDocs 注入库内作业域单据读取（TR/CK）。
func WithStockopsDocs(r DocFinder) Option { return func(o *options) { o.stockopsDocs = r } }

// WithReturnsDocs 注入退货/异常域单据读取（RT/EX）。
func WithReturnsDocs(r DocFinder) Option { return func(o *options) { o.returnsDocs = r } }

// WithWarehouseChecker 注入仓库存在性校验（router 装配：warehouse.NewChecker(db)，
// auth.WarehouseChecker 载体复用）。必需——缺位启动期 panic（plan §3.1 规则①）。
func WithWarehouseChecker(c WarehouseChecker) Option { return func(o *options) { o.whChecker = c } }

// WithDeviceAPI 注入设备端路由挂载组：必须为不含 auth.AuthRequired 的组（如 /api 根组）——
// 设备令牌与用户 JWT 并行（plan §8.2），设备端点自带 DeviceAuthRequired；/api/scanner/resolve
// 双轨认证（设备令牌或用户链）同样挂本组。缺省未注入启动期 panic（规则①）。
func WithDeviceAPI(g *gin.RouterGroup) Option { return func(o *options) { o.deviceAPI = g } }

// WithServerURL 固定激活二维码 server_url（生产经反向代理/多域名部署时由装配注入；
// 缺省按请求 scheme://host 推导）。
func WithServerURL(u string) Option { return func(o *options) { o.serverURL = u } }

// WithDedupTTL 覆盖去重窗口时长（scanner.md §6.6"极短时间内"；默认 2s）。
func WithDedupTTL(d time.Duration) Option { return func(o *options) { o.dedupTTL = d } }

// WithLogger 注入 zap 错误日志（scan_logs 降级写失败记录；nil=静默跳过——单测场景）。
func WithLogger(l *zap.Logger) Option { return func(o *options) { o.logger = l } }
