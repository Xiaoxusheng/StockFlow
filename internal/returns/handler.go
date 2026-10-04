package returns

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。
//
// 权限点：本包 permissions.go 常量（plan §9.2 冻结清单逐字同源；集成工程师收编
// internal/auth 后可直接替换引用，字符串不变）。全部业务路由挂 auth.RequirePermission
// （permission.md §2）。
//
// 数据权限（permission.md §4）：退货单列表/详情按 auth.WarehouseScope 仓库范围过滤；
// 详情越界按不存在处理（fail-closed，inventory.GetInventoryDetail 同口径）。
// 异常单为跨域工单（迁移 000010 无 warehouse_id 列），不按仓库过滤——列入 DomainResult
// 已知边界；追溯的数据权限在 Service.Trace 内按 scope 收窄（非全量范围必须显式指定
// 范围内仓库）。

// handler 退货域 HTTP handler。
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
		response.Err(c, paramError("id", "必须为正整数字符串"))
		return 0, false
	}
	return id, true
}

// returnScope 组装退货单列表数据权限（禁止接受前端仓库范围参数决定可见性，permission.md §4）。
func returnScope(c *gin.Context, f *ReturnOrderFilter) {
	all, ids := auth.WarehouseScope(c)
	f.AllWarehouses = all
	if !all {
		wids := make([]int64, len(ids))
		copy(wids, ids)
		f.WarehouseIDs = wids
	}
}

// ---- RegisterRoutes ----

// RegisterRoutes 退货域路由（三参 + Option 冻结形态，purchase/sales/stockops 同款）。
//
// 装配要求（plan §3.1 规则① fail-closed：必需跨域服务未注入即启动失败，杜绝静默跳过
// 库存变更/来源校验——与 inventory.RegisterRoutes 对 Checker 的启动期 panic 同约定）：
//
//	WithStock(inventory.NewService(db, rdb) 适配)                      库存原语（收货/质检/出库/冻结必需）
//	WithSalesOrders(sales 域实现)                                       销售退货来源校验 + 追溯富化
//	WithPurchaseOrders(purchase 域实现)                                 采购退货来源校验 + 追溯富化
//	WithQCCreator(purchase 域实现)                                      退货质检单创建
//	WithLedgers(inventory 只读查询适配)                                 追溯流水主轴
//	WithStockState(inventory 只读查询适配)                              追溯当前状态
//
// 路由与权限点（plan §2.2 R 交付、§9.2 冻结清单）：
//
//	/api/returns                 GET 列表 / POST 创建（销售退货）
//	/api/returns/:id             GET 详情 / PUT 提交 / POST approve/receive/submit-qc/quality/cancel
//	/api/purchase-returns        GET 列表 / POST 创建（采购退货）
//	/api/purchase-returns/:id    GET 详情 / PUT 提交 / POST approve/ship/complete/cancel
//	/api/exceptions              GET 列表 / POST 创建
//	/api/exceptions/:id          GET 详情 / POST assign/start/review/resolve/close
//	/api/inventory/trace         GET 追溯（plan §8.3 条 5：returns 实现、inventory 前缀挂载）
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	if db == nil {
		panic("returns 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	_ = rdb // 预留：与 inventory.Service 一致的热点缓存/幂等快路径参数位（M2 正确性不依赖）
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	if o.stock == nil {
		panic("returns 装配失败: 库存原语网关未注入（router 必须传 WithStock(inventory.NewService(...))，plan §3.1 规则①）")
	}
	if o.salesOrders == nil || o.purchaseOrders == nil || o.qc == nil {
		panic("returns 装配失败: 来源单/质检跨域接口未注入（router 必须传 WithSalesOrders/WithPurchaseOrders/WithQCCreator，plan §3.1 规则①）")
	}
	if o.ledgers == nil || o.stockState == nil {
		panic("returns 装配失败: 追溯读接口未注入（router 必须传 WithLedgers/WithStockState，plan §3.1 规则①）")
	}
	repo := NewGormRepository(db)
	svc := NewService(repo, opts...)
	h := &handler{svc: svc}

	// —— 销售退货 /api/returns ——
	ret := rg.Group("/returns")
	ret.GET("", auth.RequirePermission(PermSalesReturnList), h.listSalesReturns)
	ret.POST("", auth.RequirePermission(PermSalesReturnCreate), h.createSalesReturn)
	ret.GET("/:id", auth.RequirePermission(PermSalesReturnRead), h.getReturn)
	ret.POST("/:id/submit", auth.RequirePermission(PermSalesReturnSubmit), h.submitReturn)
	ret.POST("/:id/approve", auth.RequirePermission(PermSalesReturnApprove), h.approveReturn)
	ret.POST("/:id/receive", auth.RequirePermission(PermSalesReturnExecute), h.receiveSalesReturn)
	ret.POST("/:id/submit-qc", auth.RequirePermission(PermSalesReturnExecute), h.submitSalesQC)
	ret.POST("/:id/quality", auth.RequirePermission(PermSalesReturnExecute), h.applySalesQC)
	ret.POST("/:id/cancel", auth.RequirePermission(PermSalesReturnCancel), h.cancelReturn)

	// —— 采购退货 /api/purchase-returns（plan §2.2 R：api.md §1 缺项回写补录）——
	pr := rg.Group("/purchase-returns")
	pr.GET("", auth.RequirePermission(PermPurchaseReturnList), h.listPurchaseReturns)
	pr.POST("", auth.RequirePermission(PermPurchaseReturnCreate), h.createPurchaseReturn)
	pr.GET("/:id", auth.RequirePermission(PermPurchaseReturnRead), h.getReturn)
	pr.POST("/:id/submit", auth.RequirePermission(PermPurchaseReturnSubmit), h.submitReturn)
	pr.POST("/:id/approve", auth.RequirePermission(PermPurchaseReturnApprove), h.approveReturn)
	pr.POST("/:id/ship", auth.RequirePermission(PermPurchaseReturnExecute), h.shipPurchaseReturn)
	pr.POST("/:id/complete", auth.RequirePermission(PermPurchaseReturnExecute), h.completePurchaseReturn)
	pr.POST("/:id/cancel", auth.RequirePermission(PermPurchaseReturnCancel), h.cancelReturn)

	// —— 异常中心 /api/exceptions（api.md §1 既有资源）——
	ex := rg.Group("/exceptions")
	ex.GET("", auth.RequirePermission(PermExceptionList), h.listExceptions)
	ex.POST("", auth.RequirePermission(PermExceptionCreate), h.createException)
	ex.GET("/:id", auth.RequirePermission(PermExceptionRead), h.getException)
	ex.POST("/:id/assign", auth.RequirePermission(PermExceptionAssign), h.assignException)
	ex.POST("/:id/images", auth.RequirePermission(PermExceptionExecute), h.attachExceptionImages)
	ex.POST("/:id/start", auth.RequirePermission(PermExceptionExecute), h.startException)
	ex.POST("/:id/review", auth.RequirePermission(PermExceptionExecute), h.reviewException)
	ex.POST("/:id/resolve", auth.RequirePermission(PermExceptionExecute), h.resolveException)
	ex.POST("/:id/close", auth.RequirePermission(PermExceptionClose), h.closeException)

	// —— 库存追溯（plan §8.3 条 5：/api/inventory/trace，returns 包实现、inventory 前缀挂载）——
	rg.GET("/inventory/trace", auth.RequirePermission(PermTraceList), h.trace)
}

// ---- 销售退货 ----

// @Summary GET /api/returns
// @Tags 退货与异常
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/returns [get]
func (h *handler) listSalesReturns(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := ReturnOrderFilter{
		Type: ReturnTypeSales, Status: c.Query("status"), SourceNo: c.Query("source_no"),
		Page: page, PageSize: pageSize,
	}
	if v := c.Query("warehouse_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.Err(c, paramError("warehouse_id", "必须为正整数"))
			return
		}
		f.WarehouseID = id
	}
	returnScope(c, &f)
	items, total, err := h.svc.ListReturns(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary POST /api/returns
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body SalesReturnCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/returns [post]
func (h *handler) createSalesReturn(c *gin.Context) {
	var in SalesReturnCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.CreateSalesReturn(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary GET /api/purchase-returns/:id
// @Tags 退货与异常
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id} [get]
// @Router /api/returns/{id} [get]
func (h *handler) getReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetReturn(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	// 详情数据权限：越界按不存在处理（fail-closed，testing.md §8）。
	all, ids := auth.WarehouseScope(c)
	if !all && !containsID(ids, view.WarehouseID) {
		response.Err(c, response.NewError(ErrReturnNotFound, map[string]any{"return_id": id}))
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/purchase-returns/:id/submit
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id}/submit [post]
// @Router /api/returns/{id}/submit [post]
func (h *handler) submitReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.SubmitReturn(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/purchase-returns/:id/approve
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ApproveInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id}/approve [post]
// @Router /api/returns/{id}/approve [post]
func (h *handler) approveReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ApproveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.ApproveReturn(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/returns/:id/receive
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ReceiveInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/returns/{id}/receive [post]
func (h *handler) receiveSalesReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ReceiveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	// 幂等键头透传（plan §5：事件型收货透传给原语；最终准绳 inventory_ledgers 部分唯一索引）。
	in.IdempotencyKey = c.GetHeader("Idempotency-Key")
	view, err := h.svc.ReceiveSalesReturn(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/returns/:id/submit-qc
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body SubmitQCInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/returns/{id}/submit-qc [post]
func (h *handler) submitSalesQC(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in SubmitQCInput
	_ = c.ShouldBindJSON(&in) // 空请求体允许（remark 可选）
	view, qcNo, err := h.svc.SubmitSalesQC(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"order": view, "qc_no": qcNo})
}

// @Summary POST /api/returns/:id/quality
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body QCResultInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/returns/{id}/quality [post]
func (h *handler) applySalesQC(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in QCResultInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.ApplySalesQCResult(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/purchase-returns/:id/cancel
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body CancelInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id}/cancel [post]
// @Router /api/returns/{id}/cancel [post]
func (h *handler) cancelReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in CancelInput
	_ = c.ShouldBindJSON(&in) // 空请求体允许（reason 可选）
	view, err := h.svc.CancelReturn(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// ---- 采购退货 ----

// @Summary GET /api/purchase-returns
// @Tags 退货与异常
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns [get]
func (h *handler) listPurchaseReturns(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := ReturnOrderFilter{
		Type: ReturnTypePurchase, Status: c.Query("status"), SourceNo: c.Query("source_no"),
		Page: page, PageSize: pageSize,
	}
	if v := c.Query("warehouse_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.Err(c, paramError("warehouse_id", "必须为正整数"))
			return
		}
		f.WarehouseID = id
	}
	returnScope(c, &f)
	items, total, err := h.svc.ListReturns(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary POST /api/purchase-returns
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body PurchaseReturnCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns [post]
func (h *handler) createPurchaseReturn(c *gin.Context) {
	var in PurchaseReturnCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.CreatePurchaseReturn(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/purchase-returns/:id/ship
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ShipInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id}/ship [post]
func (h *handler) shipPurchaseReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ShipInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.ShipPurchaseReturn(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/purchase-returns/:id/complete
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchase-returns/{id}/complete [post]
func (h *handler) completePurchaseReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.CompletePurchaseReturn(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// ---- 异常中心 ----

// @Summary GET /api/exceptions
// @Tags 退货与异常
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions [get]
func (h *handler) listExceptions(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := ExceptionFilter{
		Type: c.Query("type"), Status: c.Query("status"),
		SourceType: c.Query("source_type"), SourceNo: c.Query("source_no"),
		Page: page, PageSize: pageSize,
	}
	items, total, err := h.svc.ListExceptions(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary POST /api/exceptions
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ExceptionCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions [post]
func (h *handler) createException(c *gin.Context) {
	var in ExceptionCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	actor := actorOf(c)
	no, err := h.svc.CreateException(c.Request.Context(), nil, CreateExceptionOp{
		Type: in.Type, SourceType: in.SourceType, SourceNo: in.SourceNo,
		Detail: in.Detail, SKUID: in.SKUID, BinID: in.BinID, SerialNo: in.SerialNo,
		Freeze:            in.FreezeEnabled,
		FreezeWarehouseID: in.FreezeWarehouseID,
		FreezeBatchID:     in.FreezeBatchID,
		FreezeQty:         in.FreezeQty,
		OwnerID:           in.OwnerID,
		OwnerName:         in.OwnerName,
		Actor:             actor,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"exception_no": no})
}

// @Summary GET /api/exceptions/:id
// @Tags 退货与异常
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id} [get]
func (h *handler) getException(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetException(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/exceptions/:id/images（挂接图片取证；file_ids 为文件中心上传产物）
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ExceptionImageInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/images [post]
func (h *handler) attachExceptionImages(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ExceptionImageInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.AttachExceptionImages(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/exceptions/:id/assign
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param body body ExceptionAssignInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/assign [post]
func (h *handler) assignException(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ExceptionAssignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.AssignException(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/exceptions/:id/start
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/start [post]
func (h *handler) startException(c *gin.Context) {
	h.exceptionNoteAction(c, h.svc.StartException)
}

// @Summary POST /api/exceptions/:id/review
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/review [post]
func (h *handler) reviewException(c *gin.Context) {
	h.exceptionNoteAction(c, h.svc.ReviewException)
}

// @Summary POST /api/exceptions/:id/resolve
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/resolve [post]
func (h *handler) resolveException(c *gin.Context) {
	h.exceptionNoteAction(c, h.svc.ResolveException)
}

// @Summary POST /api/exceptions/:id/close
// @Tags 退货与异常
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exceptions/{id}/close [post]
func (h *handler) closeException(c *gin.Context) {
	h.exceptionNoteAction(c, h.svc.CloseException)
}

// exceptionNoteAction 通用备注动作（开始处理/提交复核/解决/关闭）。
func (h *handler) exceptionNoteAction(c *gin.Context, fn func(ctx context.Context, actor Actor, id int64, in ExceptionNoteInput) (*ExceptionView, error)) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ExceptionNoteInput
	_ = c.ShouldBindJSON(&in) // 空请求体允许（note 可选）
	view, err := fn(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// ---- 追溯 ----

// @Summary GET /api/inventory/trace
// @Tags 退货与异常
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/trace [get]
func (h *handler) trace(c *gin.Context) {
	q := TraceQuery{SerialNo: c.Query("serial_no")}
	if v := c.Query("sku_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.Err(c, paramError("sku_id", "必须为正整数"))
			return
		}
		q.SKUID = id
	}
	if v := c.Query("warehouse_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.Err(c, paramError("warehouse_id", "必须为正整数"))
			return
		}
		q.WarehouseID = id
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			response.Err(c, paramError("limit", "必须为正整数"))
			return
		}
		q.Limit = n
	}
	all, ids := auth.WarehouseScope(c)
	scope := TraceScope{AllWarehouses: all, WarehouseIDs: ids}
	res, err := h.svc.Trace(c.Request.Context(), q, scope)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}
