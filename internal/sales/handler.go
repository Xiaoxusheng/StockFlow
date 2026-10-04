package sales

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §2.3 判据 2/6：禁止绕过 internal/response 直接 c.JSON、
// 事务边界不出现在 handler）。权限点常量见 permissions.go（plan §9.2 冻结命名）。

// handler 销售域 HTTP handler（RegisterRoutes 装配时构造，依赖经构造注入）。
type handler struct {
	svc *Service
}

// actorOf 当前用户上下文 → 操作者归因（库存流水与审计 operator 冗余字段）。
func actorOf(c *gin.Context) Actor {
	uc, ok := auth.CurrentUser(c)
	a := Actor{Name: uc.Username, RequestID: c.GetString(response.RequestIDKey),
		IP: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		Method: c.Request.Method, Path: c.Request.URL.Path}
	if ok {
		a.ID = uc.UserID
	}
	return a
}

// scopeOf 当前用户的数据权限仓库范围（auth.WarehouseScope 快照，permission.md §4）。
func scopeOf(c *gin.Context) Scope {
	all, ids := auth.WarehouseScope(c)
	return Scope{All: all, WarehouseIDs: ids}
}

// ---- 参数解析 ----

func parseIDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return id, true
}

// parseIDQuery 解析可选的正整数查询参数（0=未提供）。
func parseIDQuery(c *gin.Context, name string) (int64, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为 >=0 的整数",
		}))
		return 0, false
	}
	return v, true
}

var timeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02"}

// parseTimeParam 解析时间范围端点（to 纯日期补全为当日末秒）。
func parseTimeParam(c *gin.Context, name string, endOfDay bool) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			if endOfDay && layout == "2006-01-02" {
				t = t.Add(24*time.Hour - time.Second)
			}
			return &t, true
		}
	}
	response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
		"field": name, "reason": "时间格式必须为 YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD",
	}))
	return nil, false
}

func fail(c *gin.Context, err error) { response.Err(c, err) }

// idemKeyOf HTTP Idempotency-Key 头（architecture §3.2；覆盖 packing/shipments
// 单据表幂等并透传给库存原语）。
func idemKeyOf(c *gin.Context) string { return strings.TrimSpace(c.GetHeader("Idempotency-Key")) }

// ---- 销售订单 /api/sales ----

// @Summary GET /api/sales
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales [get]
func (h *handler) listSalesOrders(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := SalesOrderQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.Status = strings.TrimSpace(c.Query("status"))
	if q.CustomerID, err = optID(c, "customer_id"); err != nil {
		return
	}
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	q.SoNo = strings.TrimSpace(c.Query("so_no"))
	if from, ok := parseTimeParam(c, "created_from", false); !ok {
		return
	} else {
		q.CreatedFrom = from
	}
	if to, ok := parseTimeParam(c, "created_to", true); !ok {
		return
	} else {
		q.CreatedTo = to
	}
	rows, total, err := h.svc.ListSalesOrders(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// optID 可选正整数查询参数解析（供 handler 组装查询）。
func optID(c *gin.Context, name string) (int64, error) {
	v, ok := parseIDQuery(c, name)
	if !ok {
		return 0, response.NewError(response.CodeInvalidParam, map[string]any{"field": name})
	}
	return v, nil
}

// @Summary POST /api/sales
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CreateOrderInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales [post]
func (h *handler) createSalesOrder(c *gin.Context) {
	var in CreateOrderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	o, err := h.svc.CreateSalesOrder(c.Request.Context(), actorOf(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// @Summary GET /api/sales/:id
// @Tags 销售出库
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id} [get]
func (h *handler) getSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	o, items, err := h.svc.GetSalesOrderDetail(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"order": o, "items": items})
}

// @Summary PUT /api/sales/:id
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CreateOrderInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id} [put]
func (h *handler) updateSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in CreateOrderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	o, err := h.svc.UpdateSalesOrderDraft(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// @Summary PUT /api/sales/:id/submit
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id}/submit [put]
func (h *handler) submitSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	o, err := h.svc.SubmitSalesOrder(c.Request.Context(), actorOf(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// @Summary PUT /api/sales/:id/approve
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body ApproveInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id}/approve [put]
func (h *handler) approveSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in ApproveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := h.svc.ApproveSalesOrder(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, res)
}

// @Summary PUT /api/sales/:id/cancel
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CancelInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id}/cancel [put]
func (h *handler) cancelSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in CancelInput
	_ = c.ShouldBindJSON(&in) // 取消原因可选（空体允许）
	o, err := h.svc.CancelSalesOrder(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// @Summary PUT /api/sales/:id/close
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CloseInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/{id}/close [put]
func (h *handler) closeSalesOrder(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in CloseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	o, err := h.svc.CloseSalesOrder(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// ---- 出库单 /api/outbounds ----

// @Summary GET /api/outbounds
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds [get]
func (h *handler) listOutbounds(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := OutboundQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.Status = strings.TrimSpace(c.Query("status"))
	q.SoNo = strings.TrimSpace(c.Query("so_no"))
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListOutboundOrders(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary GET /api/outbounds/:no
// @Tags 销售出库
// @Produce json
// @Param no path int true "路径参数 no"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/{no} [get]
func (h *handler) getOutbound(c *gin.Context) {
	no := strings.TrimSpace(c.Param("no"))
	o, items, allocs, picks, checks, packages, ships, err := h.svc.GetOutboundDetail(c.Request.Context(), no, scopeOf(c))
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"outbound": o, "items": items, "allocations": allocs, "picks": picks,
		"checks": checks, "packages": packages, "shipments": ships,
	})
}

// releaseToPick 生成拣货任务（ALLOCATED→PICKING）。
// @Summary 生成拣货任务（ALLOCATED→PICKING）
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param no path int true "路径参数 no"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/{no}/picks [post]
func (h *handler) releaseToPick(c *gin.Context) {
	no := strings.TrimSpace(c.Param("no"))
	o, tasks, err := h.svc.GeneratePickTasks(c.Request.Context(), actorOf(c), no)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"outbound": o, "picks": tasks})
}

// @Summary PUT /api/outbounds/:no/cancel
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CancelInput true "请求体"
// @Param no path int true "路径参数 no"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/{no}/cancel [put]
func (h *handler) cancelOutbound(c *gin.Context) {
	no := strings.TrimSpace(c.Param("no"))
	var in CancelInput
	_ = c.ShouldBindJSON(&in)
	o, err := h.svc.CancelOutbound(c.Request.Context(), actorOf(c), no, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// @Summary PUT /api/outbounds/:no/close
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CloseInput true "请求体"
// @Param no path int true "路径参数 no"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/{no}/close [put]
func (h *handler) closeOutbound(c *gin.Context) {
	no := strings.TrimSpace(c.Param("no"))
	var in CloseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	o, err := h.svc.CloseOutbound(c.Request.Context(), actorOf(c), no, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, o)
}

// ---- 分配 /api/allocations ----

// @Summary GET /api/allocations
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/allocations [get]
func (h *handler) listAllocations(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := AllocationQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	q.Strategy = strings.TrimSpace(c.Query("strategy"))
	if q.SKUID, err = optID(c, "sku_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListAllocations(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// reallocate 重新分配（释放旧锁 + 重分配 + 记录替换，同事务）。
// @Summary 重新分配（释放旧锁 + 重分配 + 记录替换，同事务）
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body ReallocateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/allocations [post]
func (h *handler) reallocate(c *gin.Context) {
	var in ReallocateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	recs, err := h.svc.Reallocate(c.Request.Context(), actorOf(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"allocations": recs})
}

// ---- 拣货 /api/picks ----

// @Summary GET /api/picks
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/picks [get]
func (h *handler) listPicks(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := PickQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.Status = strings.TrimSpace(c.Query("status"))
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	if q.AssigneeID, err = optID(c, "assignee_id"); err != nil {
		return
	}
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListPickTasks(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary PUT /api/picks/:id/claim
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/picks/{id}/claim [put]
func (h *handler) claimPick(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	t, err := h.svc.ClaimPickTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// @Summary PUT /api/picks/:id/confirm
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body PickConfirmInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/picks/{id}/confirm [put]
func (h *handler) confirmPick(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in PickConfirmInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	t, err := h.svc.ConfirmPick(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// @Summary PUT /api/picks/:id/exception
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body object true "请求体（匿名结构体，字段见 handler 定义）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/picks/{id}/exception [put]
func (h *handler) reportPickException(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	t, err := h.svc.ReportPickException(c.Request.Context(), actorOf(c), id, in.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// ---- 复核 /api/checks ----

// @Summary GET /api/checks
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/checks [get]
func (h *handler) listChecks(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := CheckQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.Status = strings.TrimSpace(c.Query("status"))
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	if q.AssigneeID, err = optID(c, "assignee_id"); err != nil {
		return
	}
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListCheckTasks(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary PUT /api/checks/:id/claim
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/checks/{id}/claim [put]
func (h *handler) claimCheck(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	t, err := h.svc.ClaimCheckTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// @Summary PUT /api/checks/:id/confirm
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body CheckConfirmInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/checks/{id}/confirm [put]
func (h *handler) confirmCheck(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in CheckConfirmInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	t, err := h.svc.ConfirmCheck(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// reopenCheck 复核异常重开（EXCEPTION→PENDING；PUT /api/checks/{id}/reopen）。
// @Summary 复核异常重开（EXCEPTION→PENDING；PUT /api/checks/{id}/reopen）
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/checks/{id}/reopen [put]
func (h *handler) reopenCheck(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	t, err := h.svc.ReopenCheckTask(c.Request.Context(), actorOf(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, t)
}

// ---- 打包 /api/packing ----

// @Summary GET /api/packing
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/packing [get]
func (h *handler) listPacking(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := PackingQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListPackages(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary POST /api/packing
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body PackInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/packing [post]
func (h *handler) pack(c *gin.Context) {
	var in PackInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if k := idemKeyOf(c); k != "" && in.IdempotencyKey == "" {
		in.IdempotencyKey = k
	}
	res, err := h.svc.Pack(c.Request.Context(), actorOf(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, res)
}

// ---- 发货 /api/shipments ----

// @Summary GET /api/shipments
// @Tags 销售出库
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shipments [get]
func (h *handler) listShipments(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		fail(c, err)
		return
	}
	q := ShipmentQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	q.Status = strings.TrimSpace(c.Query("status"))
	q.OutboundNo = strings.TrimSpace(c.Query("outbound_no"))
	if q.WarehouseID, err = optID(c, "warehouse_id"); err != nil {
		return
	}
	rows, total, err := h.svc.ListShipments(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary POST /api/shipments
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body ShipInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shipments [post]
func (h *handler) ship(c *gin.Context) {
	var in ShipInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if k := idemKeyOf(c); k != "" && in.IdempotencyKey == "" {
		in.IdempotencyKey = k
	}
	res, err := h.svc.Ship(c.Request.Context(), actorOf(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, res)
}

// @Summary PUT /api/shipments/:id/status
// @Tags 销售出库
// @Accept json
// @Produce json
// @Param body body ShipStatusInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shipments/{id}/status [put]
func (h *handler) updateShipmentStatus(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in ShipStatusInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	sh, err := h.svc.UpdateShipmentStatus(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, sh)
}
