package devices

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。
//
// 双轨认证（plan §8.2/§12.1）：
//   - 管理端（protected 组，用户 JWT）：全部挂 auth.RequirePermission（权限点常量
//     permissions.go——plan §11.1 冻结清单）；数据权限按 auth.WarehouseScope 仓库范围
//     过滤（permission.md §4）。
//   - 设备端（deviceAPI 组，设备令牌）：DeviceAuthRequired（验签 + DB 行复查）；
//     activate 免中间件——一次性激活码即凭证。
//   - /api/scanner/resolve 双轨：设备令牌（iss=stockflow-devices）或用户链
//     （AuthRequired + RequirePermission(scanner:resolve:list)）。
//
// 审计（plan §13.8）：设备创建/绑定/解绑/停用/配置下发/重新生成激活码经
// middleware.Audit 同事务审计（Service 层落位）；设备端动作不写 operation_logs。

// handler 设备域 HTTP handler。
type handler struct {
	svc *Service
}

// actorOf 从 gin 上下文提取操作者归因（审计用；auth.CurrentUser 冻结契约 plan §5.1）。
func actorOf(c *gin.Context) Actor {
	uc, _ := auth.CurrentUser(c)
	return Actor{
		UserID:    uc.UserID,
		Username:  uc.Username,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
	}
}

// pathID 解析路径 :id（ID 字符串化约定，backend-m1-plan §1）。
func pathID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "id", "reason": "必须为正整数字符串"}))
		return 0, false
	}
	return id, true
}

// requestBaseURL 从请求推导 API 基址（激活二维码 server_url 缺省来源；
// 装配经 WithServerURL 固定时优先固定值——Service.activationPayload 解析优先级）。
func requestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request != nil && c.Request.TLS != nil {
		scheme = "https"
	}
	host := ""
	if c.Request != nil {
		host = c.Request.Host
	}
	return scheme + "://" + host
}

// bearerOf 提取 Bearer 凭证（大小写不敏感 scheme；对齐 auth.bearerToken 形态）。
func bearerOf(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// mustDevice 取设备上下文（DeviceAuthRequired 之后可用；缺位属装配/编程错误）。
func mustDevice(c *gin.Context) (DeviceContext, bool) {
	dev, ok := DeviceFrom(c)
	if !ok {
		response.Err(c, response.NewError(ErrDeviceTokenInvalid, nil))
		c.Abort()
	}
	return dev, ok
}

// resolveIdentity resolve 调用方归因：设备端（DeviceAuthRequired）携带设备上下文，
// Web HID（用户链）以 user_id/username/ip 归因——plan §8.3 双轨口径。
func resolveIdentity(c *gin.Context) ResolveIdentity {
	ident := ResolveIdentity{IP: c.ClientIP()}
	if dev, ok := DeviceFrom(c); ok {
		ident.DeviceID = dev.ID
		ident.DeviceCode = dev.Code
		ident.WarehouseID = dev.WarehouseID
		return ident
	}
	if uc, ok := auth.CurrentUser(c); ok {
		ident.UserID = uc.UserID
		ident.Username = uc.Username
	}
	return ident
}

// deviceScope 组装设备列表数据权限（禁止接受前端仓库范围参数决定可见性，permission.md §4）。
func deviceScope(c *gin.Context, f *DeviceFilter) {
	all, ids := auth.WarehouseScope(c)
	f.AllWarehouses = all
	if !all {
		wids := make([]int64, len(ids))
		copy(wids, ids)
		f.WarehouseIDs = wids
	}
}

// deviceScopeOf 单点端点数据权限（permission.md §4：详情/激活/绑定/停用/配置/日志沿
// 设备仓库归属做可见性校验，Service 层 fail-closed——非 ALL 且 ID 不在集合 = 不存在）。
func deviceScopeOf(c *gin.Context) WarehouseScope {
	all, ids := auth.WarehouseScope(c)
	return WarehouseScope{All: all, IDs: ids}
}

// ---- RegisterRoutes ----

// RegisterRoutes 设备/扫码域路由（protected 三参 + Option 冻结形态）。
//
// 装配要求（plan §3.1 规则① fail-closed，缺任一项启动期 panic）：
//
//	WithWarehouseChecker(warehouse.NewChecker(db))   仓库存在性校验（创建/绑定校验）
//	WithSKUBarcodes/WithBins/WithSerials/WithBatches resolve 匹配器 2–5（各域 devices_resolve.go）
//	WithSfqrSkus                                     匹配器 0（SFQR 载荷 SKU 读取，qr-code.md §6）
//	WithPurchaseDocs/WithSalesDocs/WithStockopsDocs/WithReturnsDocs 匹配器 1（前缀分派）
//	WithDeviceAPI(gin 路由组)                         设备端挂载组——必须不含 auth.AuthRequired
//	                                                  （设备令牌与用户 JWT 并行，plan §8.2）
//
// 路由与权限点（plan §8.1/§11.1/§12.1）：
//
//	/api/devices                 GET 列表 / POST 创建（返回激活二维码 payload）
//	/api/devices/:id             GET 详情 / PUT config / POST bind|unbind|disable
//	/api/devices/:id/activation  GET 激活状态 / PUT 重新生成激活码
//	/api/devices/:id/logs        GET 设备日志分页
//	/api/scanner/logs            GET 扫码日志分页（devices:scanlog:list）
//	-- 设备端（deviceAPI 组，设备令牌；activate 免中间件）--
//	/api/devices/activate        POST 激活（一次性激活码）
//	/api/devices/heartbeat       POST 心跳
//	/api/devices/self/config     GET 配置拉取
//	/api/devices/self/logs       POST 设备日志批量上报
//	/api/devices/app/versions/latest GET 最新 App 版本
//	/api/scanner/resolve         POST 统一解析（设备令牌或用户链双轨）
func RegisterRoutes(protected *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	if db == nil {
		panic("devices 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	_ = rdb // 预留参数位（M2 域包同款；设备域正确性不依赖 Redis）
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	if o.whChecker == nil {
		panic("devices 装配失败: 仓库校验器未注入（router 必须传 WithWarehouseChecker(warehouse.NewChecker(db))，plan §3.1 规则①）")
	}
	if o.skuBarcodes == nil || o.bins == nil || o.serials == nil || o.batches == nil || o.sfqrSkus == nil ||
		o.purchaseDocs == nil || o.salesDocs == nil || o.stockopsDocs == nil || o.returnsDocs == nil {
		panic("devices 装配失败: resolve 匹配器窄接口未注入（router 必须传 WithSKUBarcodes/WithBins/WithSerials/WithBatches/WithSfqrSkus/WithPurchaseDocs/WithSalesDocs/WithStockopsDocs/WithReturnsDocs，plan §3.1 规则①）")
	}
	if o.deviceAPI == nil {
		panic("devices 装配失败: 设备端挂载组未注入（router 必须传 WithDeviceAPI(不含 AuthRequired 的 /api 组)，plan §8.2 设备令牌与用户 JWT 并行）")
	}

	repo := NewGormRepository(db)
	svc := NewService(repo, opts...)
	h := &handler{svc: svc}

	// —— 管理端 /api/devices（用户 JWT + RequirePermission，plan §8.1）——
	dv := protected.Group("/devices")
	dv.GET("", auth.RequirePermission(PermDeviceList), h.listDevices)
	dv.POST("", auth.RequirePermission(PermDeviceCreate), h.createDevice)
	dv.GET("/:id", auth.RequirePermission(PermDeviceRead), h.getDevice)
	dv.GET("/:id/activation", auth.RequirePermission(PermDeviceRead), h.getActivation)
	dv.PUT("/:id/activation", auth.RequirePermission(PermDeviceUpdate), h.regenActivation)
	dv.POST("/:id/bind", auth.RequirePermission(PermDeviceUpdate), h.bindDevice)
	dv.POST("/:id/unbind", auth.RequirePermission(PermDeviceUpdate), h.unbindDevice)
	dv.POST("/:id/disable", auth.RequirePermission(PermDeviceStatus), h.disableDevice)
	dv.PUT("/:id/config", auth.RequirePermission(PermDeviceUpdate), h.putConfig)
	dv.GET("/:id/logs", auth.RequirePermission(PermDeviceRead), h.listDeviceLogs)

	// —— 扫码日志 /api/scanner/logs（devices:scanlog:list，devices.md §7.1）——
	protected.GET("/scanner/logs", auth.RequirePermission(PermScanLogList), h.listScanLogs)

	// —— 设备端（设备令牌；activate 免中间件——激活码即凭证，plan §8.2）——
	g := o.deviceAPI
	g.POST("/devices/activate", h.activate)
	devAuth := svc.DeviceAuthRequired()
	g.POST("/devices/heartbeat", devAuth, h.heartbeat)
	g.GET("/devices/self/config", devAuth, h.selfConfig)
	g.POST("/devices/self/logs", devAuth, h.selfUploadLogs)
	g.GET("/devices/app/versions/latest", devAuth, h.latestAppVersion)

	// —— 统一解析 /api/scanner/resolve（双轨：设备令牌或用户链——plan §8.3）——
	g.POST("/scanner/resolve", svc.resolveAuth(), h.resolve)
}

// ---- 中间件 ----

// DeviceAuthRequired 设备令牌认证中间件（plan §8.2 冻结：验签 + devices 行
// status=ENABLED AND activated AND token_version 匹配，每次请求查 DB；
// 失败 401 DEVICE_TOKEN_INVALID）。导出供集成侧需要时复用。
func (s *Service) DeviceAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerOf(c.GetHeader("Authorization"))
		if raw == "" {
			response.Err(c, response.NewError(ErrDeviceTokenInvalid, nil))
			c.Abort()
			return
		}
		dev, err := s.AuthenticateDevice(c.Request.Context(), raw)
		if err != nil {
			response.Err(c, err)
			c.Abort()
			return
		}
		setDeviceContext(c, dev)
		c.Next()
	}
}

// resolveAuth resolve 双轨认证中间件：设备令牌（iss=stockflow-devices）走设备验证；
// 其余走用户链（AuthRequired + RequirePermission(scanner:resolve:list)）。组合为单
// 中间件——gin 中间件无法跳过同组已注册的其他中间件（plan §8.2 双轨语义的唯一实现位）。
func (s *Service) resolveAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerOf(c.GetHeader("Authorization"))
		// 无验签 iss 探测仅用于路由判定（不信任内容；设备路径全量验签在 AuthenticateDevice）。
		if raw != "" && tokenIssuerProbe(raw) == TokenIssuer {
			dev, err := s.AuthenticateDevice(c.Request.Context(), raw)
			if err != nil {
				response.Err(c, err)
				c.Abort()
				return
			}
			setDeviceContext(c, dev)
			c.Next()
			return
		}
		auth.AuthRequired()(c)
		if c.IsAborted() {
			return
		}
		auth.RequirePermission(PermScannerResolveList)(c)
		if c.IsAborted() {
			return
		}
		c.Next()
	}
}

// ---- 管理端 handler ----

// listDevices GET /api/devices（type/status/warehouse_id/online/keyword + 分页）。
// @Summary GET /api/devices（type/status/warehouse_id/online/keyword + 分页）
// @Tags 设备中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices [get]
func (h *handler) listDevices(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := DeviceFilter{
		Type:     c.Query("type"),
		Status:   c.Query("status"),
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: pageSize,
	}
	if v := c.Query("warehouse_id"); v != "" {
		id, e := strconv.ParseInt(v, 10, 64)
		if e != nil || id < 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "warehouse_id"}))
			return
		}
		f.WarehouseID = id
	}
	if v := c.Query("online"); v != "" {
		b := v == "true" || v == "1"
		f.Online = &b
	}
	deviceScope(c, &f)
	items, total, err := h.svc.ListDevices(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createDevice POST /api/devices → 激活二维码 payload（devices.md §6.2）。
// @Summary POST /api/devices → 激活二维码 payload（devices.md §6.2）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body DeviceCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices [post]
func (h *handler) createDevice(c *gin.Context) {
	var in DeviceCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	payload, err := h.svc.CreateDevice(c.Request.Context(), actorOf(c), in, requestBaseURL(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, payload)
}

// getDevice GET /api/devices/:id。
// @Summary GET /api/devices/:id
// @Tags 设备中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id} [get]
func (h *handler) getDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	v, err := h.svc.GetDevice(c.Request.Context(), id, deviceScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// getActivation GET /api/devices/:id/activation（前端待激活轮询——device.ts:176）。
// @Summary GET /api/devices/:id/activation（前端待激活轮询——device.ts:176）
// @Tags 设备中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/activation [get]
func (h *handler) getActivation(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	payload, err := h.svc.GetActivation(c.Request.Context(), id, requestBaseURL(c), deviceScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, payload)
}

// regenActivation PUT /api/devices/:id/activation（旧 token 作废，token_version+1）。
// @Summary PUT /api/devices/:id/activation（旧 token 作废，token_version+1）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/activation [put]
func (h *handler) regenActivation(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	payload, err := h.svc.RegenerateActivation(c.Request.Context(), actorOf(c), id, requestBaseURL(c), deviceScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, payload)
}

// bindDevice POST /api/devices/:id/bind。
// @Summary POST /api/devices/:id/bind
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body object true "请求体（匿名结构体，字段见 handler 定义）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/bind [post]
func (h *handler) bindDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := h.svc.BindDevice(c.Request.Context(), actorOf(c), id, in.UserID, deviceScopeOf(c)); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"bound_user_id": in.UserID})
}

// unbindDevice POST /api/devices/:id/unbind。
// @Summary POST /api/devices/:id/unbind
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/unbind [post]
func (h *handler) unbindDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.UnbindDevice(c.Request.Context(), actorOf(c), id, deviceScopeOf(c)); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"bound_user_id": 0})
}

// disableDevice POST /api/devices/:id/disable。
// @Summary POST /api/devices/:id/disable
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/disable [post]
func (h *handler) disableDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DisableDevice(c.Request.Context(), actorOf(c), id, deviceScopeOf(c)); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": StatusDisabled})
}

// putConfig PUT /api/devices/:id/config（devices.md §7.3 下发项白名单校验）。
// @Summary PUT /api/devices/:id/config（devices.md §7.3 下发项白名单校验）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body object true "请求体（设备上报原始载荷）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/config [put]
func (h *handler) putConfig(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var raw json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	version, err := h.svc.PutDeviceConfig(c.Request.Context(), actorOf(c), id, raw, deviceScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"version": version})
}

// listDeviceLogs GET /api/devices/:id/logs（level 筛选 + 分页）。
// @Summary GET /api/devices/:id/logs（level 筛选 + 分页）
// @Tags 设备中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/{id}/logs [get]
func (h *handler) listDeviceLogs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := DeviceLogFilter{DeviceID: id, Level: c.Query("level"), Page: page, PageSize: pageSize}
	items, total, err := h.svc.ListDeviceLogs(c.Request.Context(), f, deviceScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// listScanLogs GET /api/scanner/logs（devices:scanlog:list；仓库范围数据权限）。
// @Summary GET /api/scanner/logs（devices:scanlog:list；仓库范围数据权限）
// @Tags 设备中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/scanner/logs [get]
func (h *handler) listScanLogs(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := ScanLogFilter{
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: pageSize,
	}
	if v := c.Query("device_id"); v != "" {
		id, e := strconv.ParseInt(v, 10, 64)
		if e != nil || id <= 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "device_id"}))
			return
		}
		f.DeviceID = id
	}
	if v := c.Query("user_id"); v != "" {
		id, e := strconv.ParseInt(v, 10, 64)
		if e != nil || id <= 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "user_id"}))
			return
		}
		f.UserID = id
	}
	if v := c.Query("warehouse_id"); v != "" {
		id, e := strconv.ParseInt(v, 10, 64)
		if e != nil || id < 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "warehouse_id"}))
			return
		}
		f.WarehouseID = id
	}
	if v := c.Query("success"); v != "" {
		b := v == "true" || v == "1"
		f.Success = &b
	}
	// 数据权限（permission.md §4）：scan_logs 按仓库范围过滤，fail-closed。
	all, ids := auth.WarehouseScope(c)
	f.AllWarehouses = all
	if !all {
		wids := make([]int64, len(ids))
		copy(wids, ids)
		f.WarehouseIDs = wids
	}
	items, total, err := h.svc.ListScanLogs(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// DeviceFilterScope scan_logs 过滤的仓库范围字段组（ScanLogFilter 内嵌形态；
// handler 侧按 auth.WarehouseScope 组装——独立方法避免 DeviceFilter 字段泄漏）。
func (f *ScanLogFilter) DeviceFilterScope() deviceScopeFields {
	return deviceScopeFields{AllWarehouses: f.AllWarehouses, WarehouseIDs: f.WarehouseIDs}
}

// deviceScopeFields 数据权限范围公共字段（ScanLogFilter 内嵌；DeviceFilter 独立声明同形字段）。
type deviceScopeFields struct {
	WarehouseIDs  []int64
	AllWarehouses bool
}

// ---- 设备端 handler ----

// activate POST /api/devices/activate（一次性激活码；plan §8.2）。
// @Summary POST /api/devices/activate（一次性激活码；plan §8.2）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body ActivateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/activate [post]
func (h *handler) activate(c *gin.Context) {
	var in ActivateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := h.svc.Activate(c.Request.Context(), in, c.ClientIP())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// heartbeat POST /api/devices/heartbeat。
// @Summary POST /api/devices/heartbeat
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body HeartbeatInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/heartbeat [post]
func (h *handler) heartbeat(c *gin.Context) {
	dev, ok := mustDevice(c)
	if !ok {
		return
	}
	var in HeartbeatInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := h.svc.Heartbeat(c.Request.Context(), dev, in, c.ClientIP())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// selfConfig GET /api/devices/self/config。
// @Summary GET /api/devices/self/config
// @Tags 设备中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/self/config [get]
func (h *handler) selfConfig(c *gin.Context) {
	dev, ok := mustDevice(c)
	if !ok {
		return
	}
	res, err := h.svc.GetSelfConfig(c.Request.Context(), dev)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// selfUploadLogs POST /api/devices/self/logs（≤100 条/次——plan §8.2）。
// @Summary POST /api/devices/self/logs（≤100 条/次——plan §8.2）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body object true "请求体（匿名结构体，字段见 handler 定义）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/self/logs [post]
func (h *handler) selfUploadLogs(c *gin.Context) {
	dev, ok := mustDevice(c)
	if !ok {
		return
	}
	var in struct {
		Logs []DeviceLogInput `json:"logs"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := h.svc.UploadDeviceLogs(c.Request.Context(), dev, in.Logs); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"count": len(in.Logs)})
}

// latestAppVersion GET /api/devices/app/versions/latest?platform=android。
// @Summary GET /api/devices/app/versions/latest?platform=android
// @Tags 设备中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/devices/app/versions/latest [get]
func (h *handler) latestAppVersion(c *gin.Context) {
	if _, ok := mustDevice(c); !ok {
		return
	}
	v, err := h.svc.LatestAppVersion(c.Request.Context(), c.Query("platform"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// resolve POST /api/scanner/resolve（只识别不执行业务——scanner.md §5.3）。
// @Summary POST /api/scanner/resolve（只识别不执行业务——scanner.md §5.3）
// @Tags 设备中心
// @Accept json
// @Produce json
// @Param body body ResolveInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/scanner/resolve [post]
func (h *handler) resolve(c *gin.Context) {
	var in ResolveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := h.svc.Resolve(c.Request.Context(), resolveIdentity(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}
