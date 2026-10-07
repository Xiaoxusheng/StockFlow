package purchase

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库——plan §2.3 判据 6；禁止绕过 internal/response 直接 c.JSON）。
// handler 均为纯函数 (c, svc)，由 RegisterRoutes 以闭包挂接（无包级可变状态）。

// actorOf 从 gin 上下文提取操作者归因（审计用；auth.CurrentUser 冻结契约 plan §5.1）。
// 同时注入数据权限仓库范围快照（f16：按 ID 直取的写动作在 Service 层
// fail-closed 校验仓库范围，permission.md §4）。
func actorOf(c *gin.Context) Actor {
	uc, _ := auth.CurrentUser(c)
	all, ids := auth.WarehouseScope(c)
	return Actor{
		UserID:    uc.UserID,
		Username:  uc.Username,
		IsSuper:   uc.IsSuper,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
		Scope:     &stock.WhScope{All: all, WarehouseIDs: ids},
	}
}

// pathID 解析路径 ID（api.md §2：ID 字符串形态，正整数）。
func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return id, true
}

// bindJSON 统一请求体绑定。
func bindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return false
	}
	return true
}

// pageOf 统一分页解析（api.md §2.1；response.ParsePage 全站唯一入口）。
func pageOf(c *gin.Context) (int, int, bool) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return 0, 0, false
	}
	return page, pageSize, true
}

// scopeOf 数据权限仓库范围（auth.WarehouseScope 冻结契约，permission.md §4）。
func scopeOf(c *gin.Context) WarehouseScope {
	all, ids := auth.WarehouseScope(c)
	return WarehouseScope{All: all, IDs: ids}
}

// queryInt64 可选正整数查询参数（空→0）。
func queryInt64(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为非负整数",
		}))
		return 0, false
	}
	return n, true
}

// ---- 采购订单（purchase:purchase:*）----

// @Summary GET /api/purchases
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases [get]
func handlePOList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	supplierID, ok := queryInt64(c, "supplier_id")
	if !ok {
		return
	}
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok {
		return
	}
	items, total, err := svc.ListPO(c.Request.Context(), POListFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		Status:      strings.TrimSpace(c.Query("status")),
		SupplierID:  supplierID,
		WarehouseID: warehouseID,
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/purchases/:id
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id} [get]
func handlePODetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetPO(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/purchases
// @Tags 采购入库
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases [post]
func handlePOCreate(c *gin.Context, svc *Service) {
	var req POCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreatePO(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary PUT /api/purchases/:id
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id} [put]
func handlePOUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req POUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdatePO(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/purchases/:id/submit
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id}/submit [post]
func handlePOSubmit(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.SubmitPO(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/purchases/:id/approve
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id}/approve [post]
func handlePOApprove(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req POApproveInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.ApprovePO(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/purchases/:id/cancel
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param body body CancelInput false "请求体（reason 可选，落取消审批记录与审计）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id}/cancel [post]
func handlePOCancel(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req CancelInput
	_ = c.ShouldBindJSON(&req) // 取消原因可选（空体允许，对齐 sales 域 cancel 先例）
	v, err := svc.CancelPO(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/purchases/:id/close
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/{id}/close [post]
func handlePOClose(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req POCloseInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.ClosePO(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// ---- 入库单（purchase:inbound:*）----

// @Summary GET /api/inbounds
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds [get]
func handleInboundList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok {
		return
	}
	items, total, err := svc.ListInbound(c.Request.Context(), InboundListFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		Status:      strings.TrimSpace(c.Query("status")),
		SourceType:  strings.TrimSpace(c.Query("source_type")),
		SourceNo:    strings.TrimSpace(c.Query("source_no")),
		WarehouseID: warehouseID,
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/inbounds/:id
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds/{id} [get]
func handleInboundDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetInbound(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/inbounds
// @Tags 采购入库
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds [post]
func handleInboundCreate(c *gin.Context, svc *Service) {
	var req InboundCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateInbound(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary PUT /api/inbounds/:id
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds/{id} [put]
func handleInboundUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req InboundUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateInbound(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/inbounds/:id/cancel
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param body body CancelInput false "请求体（reason 可选，落审计快照）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds/{id}/cancel [post]
func handleInboundCancel(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req CancelInput
	_ = c.ShouldBindJSON(&req) // 取消原因可选（空体允许，对齐 sales 域 cancel 先例）
	v, err := svc.CancelInbound(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/inbounds/:id/close
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds/{id}/close [post]
func handleInboundClose(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req InboundCloseInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CloseInbound(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// ---- 收货（purchase:receipt:*）----

// @Summary GET /api/receipts
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/receipts [get]
func handleReceiptList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok {
		return
	}
	items, total, err := svc.ListReceipt(c.Request.Context(), ReceiptListFilter{
		InboundNo:   strings.TrimSpace(c.Query("inbound_no")),
		ReceiptNo:   strings.TrimSpace(c.Query("receipt_no")),
		PoNo:        strings.TrimSpace(c.Query("po_no")),
		WarehouseID: warehouseID,
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/receipts/no/:no
// @Tags 采购入库
// @Produce json
// @Param no path int true "路径参数 no"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/receipts/no/{no} [get]
// @Router /api/receipts/{id} [get]
func handleReceiptDetail(c *gin.Context, svc *Service) {
	if no := strings.TrimSpace(c.Param("no")); no != "" && c.FullPath() == "/api/receipts/no/:no" {
		v, err := svc.GetReceiptByNo(c.Request.Context(), no, scopeOf(c))
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OK(c, v)
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetReceiptByID(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// handleReceiptConfirm 收货确认（幂等：Idempotency-Key 头优先于请求体键——plan §7）。
// @Summary 收货确认（幂等：Idempotency-Key 头优先于请求体键——plan §7）
// @Tags 采购入库
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/receipts [post]
func handleReceiptConfirm(c *gin.Context, svc *Service) {
	var req ReceiptInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.ConfirmReceipt(c.Request.Context(), actorOf(c), req, c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// ---- 质检（purchase:quality:*）----

// @Summary GET /api/quality/trace（质量追溯：检验→处置记录链，quality_items 行粒度）
// @Tags 采购入库
// @Produce json
// @Param sku_code query string false "SKU 编码（等值）"
// @Param batch_no query string false "批次号（等值）"
// @Param biz_no query string false "来源单据号（等值）"
// @Param serial_no query string false "不支持（质检链未记录序列号，传值返回 400）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality/trace [get]
func handleQCTrace(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	items, total, err := svc.QualityTrace(c.Request.Context(), QualityTraceFilter{
		SKUCode:  strings.TrimSpace(c.Query("sku_code")),
		BatchNo:  strings.TrimSpace(c.Query("batch_no")),
		BizNo:    strings.TrimSpace(c.Query("biz_no")),
		SerialNo: strings.TrimSpace(c.Query("serial_no")),
		Scope:    scopeOf(c),
		Page:     page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/quality/nonconforming（不合格品记录列表）
// @Tags 采购入库
// @Produce json
// @Param keyword query string false "QC 单号/SKU 编码模糊"
// @Param disposition query string false "处置六值英文键（return_supplier/scrap/rework/downgrade/to_defective_warehouse/special_release）"
// @Param destination query string false "不支持（质检单未记录去向，传值返回 400）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality/nonconforming [get]
func handleQCNonconforming(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	items, total, err := svc.Nonconforming(c.Request.Context(), NonconformingFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		Disposition: strings.TrimSpace(c.Query("disposition")),
		Destination: strings.TrimSpace(c.Query("destination")),
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/quality
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality [get]
func handleQCList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok {
		return
	}
	items, total, err := svc.ListQC(c.Request.Context(), QCListFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		Status:      strings.TrimSpace(c.Query("status")),
		SourceType:  strings.TrimSpace(c.Query("source_type")),
		SourceNo:    strings.TrimSpace(c.Query("source_no")),
		WarehouseID: warehouseID,
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/quality/:id
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality/{id} [get]
func handleQCDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetQC(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/quality
// @Tags 采购入库
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality [post]
func handleQCCreate(c *gin.Context, svc *Service) {
	var req QCCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateQC(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/quality/:id/start
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality/{id}/start [post]
func handleQCStart(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.StartQC(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/quality/:id/execute
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/quality/{id}/execute [post]
func handleQCExecute(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req QCExecuteInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.ExecuteQC(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// ---- 上架任务（purchase:putaway:*）----

// @Summary GET /api/putaway
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway [get]
func handleTaskList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok {
		return
	}
	skuID, ok := queryInt64(c, "sku_id")
	if !ok {
		return
	}
	items, total, err := svc.ListTask(c.Request.Context(), TaskListFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		Status:      strings.TrimSpace(c.Query("status")),
		InboundNo:   strings.TrimSpace(c.Query("inbound_no")),
		SKUID:       skuID,
		FromState:   strings.TrimSpace(c.Query("from_state")),
		WarehouseID: warehouseID,
		Scope:       scopeOf(c),
		Page:        page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/putaway/:id
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/{id} [get]
func handleTaskDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetTask(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// handleTaskRecommend 推荐库位（§5.3 基础规则经 BinRecommender）。
// @Summary 推荐库位（§5.3 基础规则经 BinRecommender）
// @Tags 采购入库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/recommend [get]
func handleTaskRecommend(c *gin.Context, svc *Service) {
	warehouseID, ok := queryInt64(c, "warehouse_id")
	if !ok || warehouseID <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "warehouse_id", "reason": "必填且为正整数",
		}))
		return
	}
	skuID, ok := queryInt64(c, "sku_id")
	if !ok || skuID <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "sku_id", "reason": "必填且为正整数",
		}))
		return
	}
	qty := 1.0
	if raw := c.Query("qty"); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f <= 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "qty", "reason": "必须为正数",
			}))
			return
		}
		qty = f
	}
	v, err := svc.RecommendBins(c.Request.Context(), warehouseID, skuID, qty)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"suggestions": v})
}

// @Summary POST /api/putaway/:id/claim
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/{id}/claim [post]
func handleTaskClaim(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.ClaimPutawayTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/putaway/:id/pause（暂停任务，IN_PROGRESS→PAUSED；仅领取人/超管）
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/{id}/pause [post]
func handleTaskPause(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.PausePutawayTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/putaway/:id/resume（恢复任务，PAUSED→IN_PROGRESS；仅原领取人/超管）
// @Tags 采购入库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/{id}/resume [post]
func handleTaskResume(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.ResumePutawayTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary POST /api/putaway/:id/execute
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/{id}/execute [post]
func handleTaskExecute(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req PutawayExecuteInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.ExecutePutawayTask(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// ---- 批量领取与优先级（效率层一期 B3，docs/api.md §9 批量结果契约） ----

// @Summary POST /api/putaway/batch-claim
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param body body BatchClaimInput true "请求体（ids）"
// @Success 200 {object} response.Envelope "统一响应信封（批量结果契约）"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/putaway/batch-claim [post]
func handleBatchClaimPutaway(c *gin.Context, svc *Service) {
	var req BatchClaimInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.BatchClaimPutawayTasks(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// @Summary PUT /api/putaway/:id/priority（任务优先级，0-9；终态 409）
// @Tags 采购入库
// @Accept json
// @Produce json
// @Param body body TaskPriorityInput true "请求体（priority 0-9）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封（{id, priority}）"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Failure 409 {object} response.Envelope "终态任务拒绝"
// @Router /api/putaway/{id}/priority [put]
func handleTaskPriority(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req TaskPriorityInput
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.SetPutawayTaskPriority(c.Request.Context(), actorOf(c), id, req); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id, "priority": *req.Priority})
}
