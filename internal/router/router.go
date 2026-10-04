// Package router 全局路由装配（backend-m1-plan §5.3）。
// 唯一允许静态 import 全部域包的包（plan §2/§3）；M2/M3 新增域只改本文件；
// 跨域消费接口的装配注入也只在此处（plan §4.3；M3 桥接见 m3_bridges.go）。
package router

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/devices"
	"github.com/stockflow/server/internal/health"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/masterdata"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/printing"
	"github.com/stockflow/server/internal/purchase"
	"github.com/stockflow/server/internal/reports"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/returns"
	"github.com/stockflow/server/internal/sales"
	"github.com/stockflow/server/internal/stockops"
	"github.com/stockflow/server/internal/storage"
	"github.com/stockflow/server/internal/sysops"
	"github.com/stockflow/server/internal/warehouse"
)

// New 装配 gin.Engine：受信代理 → 全局中间件 → 探针（免认证，api.md §6.1）→ /api 装配。
// rt 为 asynqx 队列运行时（backend-m3-plan §12.1：Queue 注入 datax/printing，Inspector
// 桥接 sysops 监控；main 必须先于本函数创建，Server.Start 在装配完成——handler 注册齐——
// 之后由 main 执行）。
func New(cfg *config.Config, db *gorm.DB, rdb *redis.Client, rt *asynqx.Runtime) *gin.Engine {
	if rt == nil || rt.Queue == nil {
		panic("router 装配失败: asynqx Runtime 未注入（cmd/server main 必须传 asynqx.NewRuntime 产物，plan §12.1）")
	}
	r := gin.New()
	// 受信代理（安全审查 S5）：显式按配置设置——空列表 = 不信任任何代理，ClientIP
	// 一律取直连 RemoteAddr（gin 默认信任全部代理，会放行 X-Forwarded-For 伪造）。
	// 网段非法属启动期配置错误，config.Validate 已先行拦截，此处 fail-fast 双保险
	// （与 auth 装配 panic 同一模式，deployment.md §3 禁止带病启动）。
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		panic("router 装配失败: 设置受信代理失败: " + err.Error())
	}
	// 上传路由 body 上限局部放宽清单（plan §12.1：storage.upload_max_bytes 覆盖全局 1MB），
	// 装配完成后 verifyRelaxedRoutes 核验路由存在。
	uploadLimits := uploadRelaxedRoutes(cfg.Storage.UploadMaxBytes)
	// 全局中间件链（plan §5.3 冻结形态）：Recovery → RequestID → MaxBody → AccessLog → CORS。
	// MaxBody（安全审查 S6）置于 Recovery 之后、RequestID 之后——413 统一信封携带
	// request_id，且超限请求同样留下访问日志；M3 起为 route-aware（m3_bridges.go），
	// 非上传路由维持全局上限，命中上传路由放宽至 storage.upload_max_bytes。
	r.Use(middleware.Recovery(), middleware.RequestID(), bodyLimit(cfg.Server.MaxBodyBytes, uploadLimits),
		middleware.AccessLog(cfg), middleware.CORS(cfg.CORS.AllowedOrigins))

	// 探针（免认证：api.md §6.1 豁免名单）；/ready 含文件中心存储根可写探测
	//（backend-m3-plan §6.4，M1 §12 挂账销项）。
	r.GET("/health", health.Liveness())
	r.GET("/ready", health.Readiness(db, rdb, cfg.Storage.Root))

	// 未匹配路由 / 未允许方法也走统一信封（前端 client.ts 依赖 message/request_id）
	r.NoRoute(func(c *gin.Context) {
		response.Err(c, response.NewError(response.CodeNotFound, nil))
	})
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		// 405 语义（REST 方法不匹配），区别于路径不存在的 404；信封结构不变。
		response.Err(c, response.NewError(response.CodeMethodNotAllowed, gin.H{"reason": "method not allowed"}))
	})

	api := r.Group("/api")

	// API 指标采样（backend-m3-plan §12.1：sysops.MetricsMiddleware 挂 /api 组——
	// 进程内分钟级环形采样，重启清零，/api/system/monitor 数据源）。
	api.Use(sysops.MetricsMiddleware())

	// 设备端挂载组：不含 auth.AuthRequired——设备令牌（DeviceAuthRequired）与用户 JWT
	// 并行，activate 以一次性激活码即凭证（plan §8.2；设备域对缺位启动期 fail-fast）。
	deviceAPI := api.Group("")

	// 公开组：认证登录/刷新（api.md §6.1 豁免；plan §5.3）。
	// IP 维度组级滑动窗口限流（安全审查 S4，auth.rate_limit_ip_per_minute 默认 30）：
	// Redis 故障 fail-open 并记 error 日志；用户名维度的严格限流在 auth 域 guard 内
	// （契约分工见各自清单）。
	authPublic := api.Group("/auth")
	authPublic.Use(middleware.AuthIPRateLimit(int64(cfg.Auth.RateLimitIPPerMinute), rdb))
	auth.RegisterPublicRoutes(authPublic, db, rdb)

	// 保护组：全部业务接口；跨域消费接口在本处按 plan §4.3/§5.3 注入（唯一装配点，
	// 各域对"必需 Checker 未注入"启动期 fail-fast，plan §4.3 规则①；db==nil 属装配
	// 错误，域内同样 fail-fast，deployment.md §3 禁止带病启动）。
	protected := api.Group("")
	protected.Use(auth.AuthRequired())
	auth.RegisterProtectedRoutes(protected, db, rdb, auth.WithWarehouseChecker(warehouse.NewChecker(db)))
	masterdata.RegisterRoutes(protected, db, rdb)
	warehouse.RegisterRoutes(protected, db, rdb,
		warehouse.WithBinOccupancy(binOccupancyBridge(inventory.NewBinOccupancy(db))))
	inventory.RegisterRoutes(protected, db, rdb,
		inventory.WithSKUChecker(masterdata.NewSKUChecker(db)),
		inventory.WithBinChecker(warehouse.NewBinChecker(db)))

	// ---- M2 四域装配（backend-m2-plan §9.4，桥接实现见 m2_bridges.go）----
	// 库存原语网关：plan §3 冻结口径——router 为各消费点各自构造 inventory.NewService
	// 注入，多实例无共享状态（Service 仅持 db/rdb/checker），不构成第二套库存入口；
	// 各实例同样注入 SKU/库位校验（M1 注入方式复用，行创建类原语的业务关系校验齐备）。
	newInvService := func() *inventory.Service {
		return newInventoryService(db, rdb)
	}
	// 异常中心与退货质检的服务实例：仅承载跨域导出动作（returns.Create 异常登记、
	// purchase.QCCreatorService 退货质检创建），无路由；returns.RegisterRoutes 内部
	// 以完整依赖另行构造自己的 Service（与 inventory 同一多实例口径）。
	returnsSvc := returns.NewService(returns.NewGormRepository(db))
	purchaseSvc := purchase.NewService(purchase.NewRepository(db))

	// 采购入库域：/api/purchases、/api/inbounds、/api/receipts、/api/quality、/api/putaway。
	// BinRecommender（推荐库位）不注入——plan §3.1 冻结清单未收录该接口，域内缺省
	// fail-closed（上架必须显式指定目标库位）。
	purchase.RegisterRoutes(protected, db, rdb,
		purchase.WithStock(purchaseStockGateway{svc: newInvService()}),
		purchase.WithSupplierChecker(masterdata.NewSupplierChecker(db)),
		purchase.WithWarehouseChecker(warehouse.NewChecker(db)),
		purchase.WithSKUAttrReader(purchaseSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)}),
		purchase.WithBinChecker(warehouse.NewBinChecker(db)),
		purchase.WithExceptionCreator(returnsSvc),
	)

	// 销售出库域：/api/sales、/api/outbounds、/api/allocations、/api/picks、/api/checks、
	// /api/packing、/api/shipments（域内对必需跨域依赖缺位启动期 fail-fast）。
	sales.RegisterRoutes(protected, db, rdb,
		sales.WithStock(salesStockGateway{svc: newInvService()}),
		sales.WithSKUAttr(salesSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)}),
		sales.WithCustomerChecker(masterdata.NewCustomerChecker(db)),
		sales.WithBinChecker(warehouse.NewBinChecker(db)),
		sales.WithExceptions(salesExceptionBridge{svc: returnsSvc}),
	)

	// 库存作业域：/api/transfers、/api/counts。StockGateway 消费接口以 inventory 类型
	// 定义（stockops/ports.go），*inventory.Service 结构化满足，直传。
	stockops.RegisterRoutes(protected, db, rdb,
		stockops.WithGateway(newInvService()),
		stockops.WithBinChecker(warehouse.NewBinChecker(db)),
		stockops.WithSKUChecker(masterdata.NewSKUChecker(db)),
		stockops.WithSKUFlagReader(stockopsSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)}),
	)

	// 退货域：/api/returns、/api/purchase-returns、/api/exceptions 与
	// GET /api/inventory/trace（plan §8.3 条 5：returns 实现、inventory 前缀挂载）。
	returns.RegisterRoutes(protected, db, rdb,
		returns.WithStock(returnsStockGateway{svc: newInvService()}),
		returns.WithSKUFlags(returnsSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)}),
		returns.WithSalesOrders(salesReturnOrderReader{db: db}),
		returns.WithPurchaseOrders(purchaseReturnOrderReader{db: db}),
		returns.WithQCCreator(qcCreatorBridge{qc: purchase.NewQCCreator(purchaseSvc)}),
		returns.WithLedgers(traceReaders{svc: newInvService()}),
		returns.WithStockState(traceReaders{svc: newInvService()}),
	)

	// ---- M3 五域装配（backend-m3-plan §12.1，桥接实现见 m3_bridges.go）----
	// 数据中心：/api/imports、/api/exports、/api/files、/api/data-tasks。
	// ① 导入写入器 ×9（§6.1：写路径经各域既有 Service 通路，期初库存经库存原语）；
	// ② 导出 16 模块行源（§6.1/§6.3）——11 个已交付行源装配，PRODUCT/SKU/SUPPLIER/
	//    CUSTOMER（masterdata datax_export.go）与 REPORT（reports 行源）未交付，
	//    运行期按 datax fail-closed 设计对未挂模块创建任务返回 409 DATAX_MODULE_NOT_AVAILABLE；
	// ③ asynqx handler（import:commit/export:run）先于 Server.Start 注册（§4.2）。
	dataxSvc := newDataxService(cfg, db, rdb, rt.Queue)
	registerDataxQueueHandlers(dataxSvc)
	datax.RegisterRoutes(protected, dataxSvc)

	// 打印中心：/api/prints（模板/任务/历史/条码）。ContentReader ×7（§7.2 装配窄接口，
	// CARTON_CODE/PALLET_CODE 为 printing 内置承接）+ 队列注入；render handler 在
	// RegisterRoutes 内进程级一次注册。
	printing.RegisterRoutes(protected, db, rdb,
		printing.WithContentReader(printing.ObjectSKULabel, masterdata.NewSKUContentReader(db)),
		printing.WithContentReader(printing.ObjectBinLabel, warehouse.NewBinContentReader(db)),
		printing.WithContentReader(printing.ObjectInboundOrder, purchase.NewInboundContentReader(db)),
		printing.WithContentReader(printing.ObjectOutboundOrder, sales.NewOutboundContentReader(db)),
		printing.WithContentReader(printing.ObjectPickOrder, sales.NewPickContentReader(db)),
		printing.WithContentReader(printing.ObjectShipmentOrder, sales.NewShipmentContentReader(db)),
		printing.WithContentReader(printing.ObjectCountOrder, stockops.NewCountContentReader(db)),
		printing.WithQueue(rt.Queue),
	)

	// 设备与扫码：/api/devices（管理端，用户 JWT）+ /api/scanner/logs；
	// 设备端 /api/devices/activate|heartbeat|self/*、/api/devices/app/versions/latest、
	// /api/scanner/resolve 挂 deviceAPI 组（设备令牌，plan §8.2/§8.3）。
	// resolve 匹配器 1–5 窄接口（§12.2）：单据号分派 4 域 DocFinder（docnum 冻结前缀映射）、
	// SKU 条码/库位码/序列号/批次码各域 devices_resolve.go。
	devices.RegisterRoutes(protected, db, rdb,
		devices.WithWarehouseChecker(warehouse.NewChecker(db)),
		devices.WithSKUBarcodes(masterdata.NewSKUBarcodeReader(db)),
		devices.WithBins(warehouse.NewBinCodeReader(db)),
		devices.WithSerials(inventory.NewSerialReader(db)),
		devices.WithBatches(inventory.NewBatchReader(db)),
		devices.WithPurchaseDocs(purchase.NewDocResolveFinder(db)),
		devices.WithSalesDocs(sales.NewDocResolveFinder(db)),
		devices.WithStockopsDocs(stockops.NewDocResolveFinder(db)),
		devices.WithReturnsDocs(returns.NewDocResolveFinder(db)),
		devices.WithDeviceAPI(deviceAPI),
	)

	// 报表与智能能力：/api/reports/* + /api/inventory/summary|alerts（§9.1：reports 实现、
	// inventory 前缀挂载）。补货参数/预警阈值经 WithReplenishmentParams/WithAlertThresholds
	// 注入点接线 system_configs（sysops 只读桥，行缺失回退冻结缺省 7/3、30,15,7,3、
	// 30,60,90——plan §10.2 seed 同源键，/api/reports 与预警扫描两套口径不再漂移）。
	reports.RegisterRoutes(protected, db, rdb,
		reports.WithReplenishmentParams(func(ctx context.Context) (int, int, error) {
			lead, err := sysops.ReadConfigInt(ctx, db, sysops.CfgKeyReplenishmentLeadDays, 7)
			if err != nil {
				return 0, 0, err
			}
			buffer, err := sysops.ReadConfigInt(ctx, db, sysops.CfgKeyReplenishmentBufferDays, 3)
			if err != nil {
				return 0, 0, err
			}
			return lead, buffer, nil
		}),
		reports.WithAlertThresholds(func(ctx context.Context) ([]int, []int, error) {
			expiry, err := sysops.ReadConfigIntList(ctx, db, sysops.CfgKeyExpiryDays, []int{30, 15, 7, 3})
			if err != nil {
				return nil, nil, err
			}
			stagnant, err := sysops.ReadConfigIntList(ctx, db, sysops.CfgKeyStagnantDays, []int{30, 60, 90})
			if err != nil {
				return nil, nil, err
			}
			return expiry, stagnant, nil
		}),
	)

	// 平台运维面：/api/logs、/api/system、/api/notifications（§10）。Redis 连通探测经
	// WithRedisPinger（RegisterRoutes 内部适配 *redis.Client）；队列积压统计仅在
	// asynq 模式注入（inline 无 Inspector，monitor 省略 queued_tasks）；存储根/备份
	// 保留期随配置注入（file_cleanup 与备份下载路径一致性，§10.5）。
	sysopsOpts := []sysops.ServiceOption{
		sysops.WithStorageRoot(cfg.Storage.Root),
		sysops.WithBackupRetention(cfg.Sysops.BackupRetentionDays),
	}
	if rt.Inspector != nil {
		sysopsOpts = append(sysopsOpts, sysops.WithQueueStats(inspectorQueueStats{insp: rt.Inspector}))
	}
	sysops.RegisterRoutes(protected, db, rdb, sysopsOpts...)

	// 上传放宽路由核验（启动期 fail-fast，防 datax 路由漂移致 body 上限守卫失效）。
	verifyRelaxedRoutes(r, uploadLimits)

	return r
}

// newDataxService 构建数据中心 Service（router 唯一装配点，plan §6/§12.2）：
// 存储根/队列/导入写入器 ×9/导出行源 ×11。REPORT 行源与 masterdata 四行源未交付
// （工程师 X/MT4 剩余项），不注入——datax 对未挂模块 fail-closed（409 拒绝创建任务）。
func newDataxService(cfg *config.Config, db *gorm.DB, rdb *redis.Client, queue asynqx.Queue) *datax.Service {
	store, err := storage.NewStore(cfg.Storage.Root)
	if err != nil {
		panic("router 装配失败: 文件中心存储初始化失败: " + err.Error())
	}
	// 导入写入器承载 Service（与各域 RegisterRoutes 同款依赖，导入创建通路 = 手工创建
	// 同一校验面；sales/warehouse 承载构造器为 §2.2 规则③最小改动清单产物）。
	salesSvc := sales.NewSOImportService(db,
		salesStockGateway{svc: newInventoryService(db, rdb)},
		salesSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)},
		masterdata.NewCustomerChecker(db),
		warehouse.NewBinChecker(db))
	whSvc := warehouse.NewWarehouseImportService(db)
	poSvc := purchase.NewService(purchase.NewRepository(db),
		purchase.WithSupplierChecker(masterdata.NewSupplierChecker(db)),
		purchase.WithWarehouseChecker(warehouse.NewChecker(db)),
		purchase.WithSKUAttrReader(purchaseSKUFlagsBridge{r: masterdata.NewSKUFlagReader(db)}))
	mdCodes := masterdata.NewDataxCodeResolver(db)
	whCodes := warehouse.NewDataxWarehouseResolver(db)

	return datax.NewService(datax.NewGormRepository(db), store, queue, datax.Config{
		ImportMaxRows:     cfg.Datax.ImportMaxRows,
		BatchSize:         cfg.Datax.BatchSize,
		ExportBatchSize:   cfg.Datax.ExportBatchSize,
		FileRetentionDays: cfg.Datax.FileRetentionDays,
		UploadMaxBytes:    cfg.Storage.UploadMaxBytes,
	}, nil,
		// —— 导入写入器 ×9（§6.1）——
		datax.WithImportWriter(datax.ImportProduct, masterdata.NewProductImportWriter(masterdata.NewService(masterdata.NewRepository(db)))),
		datax.WithImportWriter(datax.ImportSKU, masterdata.NewSKUImportWriter(masterdata.NewService(masterdata.NewRepository(db)))),
		datax.WithImportWriter(datax.ImportSupplier, masterdata.NewSupplierImportWriter(masterdata.NewService(masterdata.NewRepository(db)))),
		datax.WithImportWriter(datax.ImportCustomer, masterdata.NewCustomerImportWriter(masterdata.NewService(masterdata.NewRepository(db)))),
		datax.WithImportWriter(datax.ImportWarehouse, warehouse.NewWarehouseImportWriter(whSvc)),
		datax.WithImportWriter(datax.ImportLocation, warehouse.NewBinImportWriter(whSvc)),
		datax.WithImportWriter(datax.ImportPurchaseOrder, purchase.NewPOImportWriter(poSvc, purchase.POImportDeps{
			Codes:      purchaseDataxCodeBridge{r: mdCodes},
			Warehouses: dataxWarehouseCodeBridge{r: whCodes},
		})),
		datax.WithImportWriter(datax.ImportSalesOrder, sales.NewSOImportWriter(salesSvc, sales.SOImportDeps{
			Codes:      salesDataxCodeBridge{r: mdCodes},
			Warehouses: dataxWarehouseCodeBridge{r: whCodes},
		})),
		datax.WithImportWriter(datax.ImportInitialInventory, inventory.NewInitialInventoryWriter(
			newInventoryService(db, rdb),
			inventory.InitialInventoryDeps{
				SKUs:       inventoryDataxSKUBridge{codes: mdCodes, flags: masterdata.NewSKUFlagReader(db)},
				Warehouses: inventoryDataxWarehouseBridge{r: whCodes},
			})),
		// —— 导出行源（§6.1 导出 16 模块；已交付 11 源）——
		datax.WithExportSource(datax.ModuleWarehouse, warehouse.NewWarehouseExportSource(db)),
		datax.WithExportSource(datax.ModuleLocation, warehouse.NewBinExportSource(db)),
		datax.WithExportSource(datax.ModulePurchaseOrder, purchase.NewPOExportSource(db)),
		datax.WithExportSource(datax.ModulePurchaseInbound, purchase.NewInboundExportSource(db)),
		datax.WithExportSource(datax.ModuleQuality, purchase.NewQualityExportSource(db)),
		datax.WithExportSource(datax.ModuleSalesOutbound, sales.NewOutboundExportSource(db)),
		datax.WithExportSource(datax.ModuleInventory, inventory.NewInventoryExportSource(db)),
		datax.WithExportSource(datax.ModuleInventoryLedger, inventory.NewLedgerExportSource(db)),
		datax.WithExportSource(datax.ModuleTransfer, stockops.NewTransferExportSource(db)),
		datax.WithExportSource(datax.ModuleCount, stockops.NewCountExportSource(db)),
		datax.WithExportSource(datax.ModuleException, returns.NewExceptionExportSource(db)),
	)
}

// ---- 跨域接口桥接（plan §4.3：仅 router 装配处允许引用多域实现）----

// occupancyBridgeFunc 闭包适配器：将函数适配为 warehouse.BinOccupancyReader。
type occupancyBridgeFunc func(ctx context.Context, warehouseID int64) (quantity, locked map[int64]float64, err error)

// OccupancyByWarehouse 实现 warehouse.BinOccupancyReader。
func (f occupancyBridgeFunc) OccupancyByWarehouse(ctx context.Context, warehouseID int64) (map[int64]float64, map[int64]float64, error) {
	return f(ctx, warehouseID)
}

// binOccupancyBridge 将 inventory.BinOccupancy.SumByBin 桥接为 warehouse.BinOccupancyReader
// （warehouse/ports.go 装配约定，plan §4.3 ②/§5.3）：SumByBin 返回 inventory 域具名类型
// 切片（[]BinOccupancySummary），warehouse 包不可命名，故以闭包适配器桥接为内建类型
// 窄接口。locked 无独立数据源（SumByBin 仅聚合六状态总和），按契约返回 nil
// （缺省键 = 无占用信息，不造假数据，requirements.md §10）。
func binOccupancyBridge(o *inventory.BinOccupancy) warehouse.BinOccupancyReader {
	return occupancyBridgeFunc(func(ctx context.Context, warehouseID int64) (map[int64]float64, map[int64]float64, error) {
		rows, err := o.SumByBin(ctx, warehouseID)
		if err != nil {
			return nil, nil, fmt.Errorf("汇总仓库 %d 库位占用失败: %w", warehouseID, err)
		}
		quantity := make(map[int64]float64, len(rows))
		for _, r := range rows {
			// Qty 为 numeric(18,4) 精确十进制载体，String() 输出 4 位小数文本，
			// ParseFloat 取最接近表示；窄接口 float64 为跨域契约既定形态。
			q, perr := strconv.ParseFloat(r.TotalQty.String(), 64)
			if perr != nil {
				return nil, nil, fmt.Errorf("解析仓库 %d 库位 %d 占用数量失败: %w", warehouseID, r.BinID, perr)
			}
			quantity[r.BinID] = q
		}
		return quantity, nil, nil
	})
}

// newInventoryService 构建库存原语网关 Service（backend-m2-plan §9.4 冻结口径：多实例
// 无共享状态，不构成第二套库存入口；M3 datax 期初库存导入写入器复用同一构造——
// 写路径经库存原语，plan §6.1）。
func newInventoryService(db *gorm.DB, rdb *redis.Client) *inventory.Service {
	return inventory.NewService(db, rdb,
		inventory.WithSKUChecker(masterdata.NewSKUChecker(db)),
		inventory.WithBinChecker(warehouse.NewBinChecker(db)))
}
