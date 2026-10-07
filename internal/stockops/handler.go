package stockops

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，禁止绕过 internal/response 直接 c.JSON）。权限点见 permissions.go
// （backend-m2-plan §9.2 命名）；数据权限经 auth.WarehouseScope 注入 Service
// （permission.md §4，禁止接受前端传入范围参数）。

// handler 库存作业域 HTTP handler（RegisterRoutes 装配时构造，依赖经构造注入）。
type handler struct {
	svc *Service
}

// ---- 响应视图（ID 序列化为字符串：backend-m1-plan §1；数量为 numeric(18,4) 裸数字）----

type TransferOrderView struct {
	ID              database.ID       `json:"id"`
	TransferNo      string            `json:"transfer_no"`
	Type            string            `json:"type"`
	FromWarehouseID database.ID       `json:"from_warehouse_id"`
	ToWarehouseID   database.ID       `json:"to_warehouse_id"`
	Status          string            `json:"status"`
	ApprovedBy      database.ID       `json:"approved_by"`
	ApprovedAt      database.JSONTime `json:"approved_at"`
	OutboundAt      database.JSONTime `json:"outbound_at"`
	ReceivedAt      database.JSONTime `json:"received_at"`
	CancelledAt     database.JSONTime `json:"cancelled_at"`
	Remark          string            `json:"remark"`
	CreatedAt       database.JSONTime `json:"created_at"`
	UpdatedAt       database.JSONTime `json:"updated_at"`
}

func newTransferOrderView(o *TransferOrder) TransferOrderView {
	return TransferOrderView{
		ID: o.ID, TransferNo: o.TransferNo, Type: o.Type,
		FromWarehouseID: database.ID(o.FromWarehouseID), ToWarehouseID: database.ID(o.ToWarehouseID),
		Status: o.Status, ApprovedBy: database.ID(o.ApprovedBy),
		ApprovedAt: o.ApprovedAt, OutboundAt: o.OutboundAt, ReceivedAt: o.ReceivedAt,
		CancelledAt: o.CancelledAt, Remark: o.Remark, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

type TransferItemView struct {
	ID              database.ID `json:"id"`
	LineNo          int         `json:"line_no"`
	SKUID           database.ID `json:"sku_id"`
	BatchID         database.ID `json:"batch_id"`
	FromWarehouseID database.ID `json:"from_warehouse_id"`
	FromZoneID      database.ID `json:"from_zone_id"`
	FromShelfID     database.ID `json:"from_shelf_id"`
	FromBinID       database.ID `json:"from_bin_id"`
	ToWarehouseID   database.ID `json:"to_warehouse_id"`
	ToZoneID        database.ID `json:"to_zone_id"`
	ToShelfID       database.ID `json:"to_shelf_id"`
	ToBinID         database.ID `json:"to_bin_id"`
	Qty             stock.Qty   `json:"qty"`
	QtyOut          stock.Qty   `json:"qty_out"`
	QtyIn           stock.Qty   `json:"qty_in"`
	InTransitQty    stock.Qty   `json:"in_transit_qty"` // 在途 = qty_out - qty_in（后端计算，前端禁止算库存）
	Remark          string      `json:"remark"`
}

func newTransferItemView(it *TransferItem) TransferItemView {
	return TransferItemView{
		ID: it.ID, LineNo: it.LineNo, SKUID: database.ID(it.SKUID), BatchID: database.ID(it.BatchID),
		FromWarehouseID: database.ID(it.FromWarehouseID), FromZoneID: database.ID(it.FromZoneID),
		FromShelfID: database.ID(it.FromShelfID), FromBinID: database.ID(it.FromBinID),
		ToWarehouseID: database.ID(it.ToWarehouseID), ToZoneID: database.ID(it.ToZoneID),
		ToShelfID: database.ID(it.ToShelfID), ToBinID: database.ID(it.ToBinID),
		Qty: it.Qty, QtyOut: it.QtyOut, QtyIn: it.QtyIn,
		InTransitQty: it.QtyOut.Sub(it.QtyIn), Remark: it.Remark,
	}
}

type TransferDetailView struct {
	Order TransferOrderView  `json:"order"`
	Items []TransferItemView `json:"items"`
}

type CountOrderView struct {
	ID          database.ID       `json:"id"`
	CountNo     string            `json:"count_no"`
	WarehouseID database.ID       `json:"warehouse_id"`
	Scope       CountScope        `json:"scope"`
	Status      string            `json:"status"`
	FrozenAt    database.JSONTime `json:"frozen_at"`
	ReviewedAt  database.JSONTime `json:"reviewed_at"`
	CompletedAt database.JSONTime `json:"completed_at"`
	CancelledAt database.JSONTime `json:"cancelled_at"`
	Remark      string            `json:"remark"`
	CreatedAt   database.JSONTime `json:"created_at"`
	UpdatedAt   database.JSONTime `json:"updated_at"`
}

func newCountOrderView(o *CountOrder) CountOrderView {
	return CountOrderView{
		ID: o.ID, CountNo: o.CountNo, WarehouseID: database.ID(o.WarehouseID),
		Scope: o.Scope, Status: o.Status,
		FrozenAt: o.FrozenAt, ReviewedAt: o.ReviewedAt, CompletedAt: o.CompletedAt,
		CancelledAt: o.CancelledAt, Remark: o.Remark, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

type CountItemView struct {
	ID             database.ID       `json:"id"`
	InventoryRowID database.ID       `json:"inventory_row_id"`
	SKUID          database.ID       `json:"sku_id"`
	WarehouseID    database.ID       `json:"warehouse_id"`
	ZoneID         database.ID       `json:"zone_id"`
	ShelfID        database.ID       `json:"shelf_id"`
	BinID          database.ID       `json:"bin_id"`
	QtySystem      stock.Qty         `json:"qty_system"`
	QtyCounted     *stock.Qty        `json:"qty_counted"` // null=未登记
	CountedBy      database.ID       `json:"counted_by"`
	CountedAt      database.JSONTime `json:"counted_at"`
	SerialNo       string            `json:"serial_no"`
}

func newCountItemView(it *CountItem) CountItemView {
	v := CountItemView{
		ID: it.ID, InventoryRowID: database.ID(it.InventoryRowID), SKUID: database.ID(it.SKUID),
		WarehouseID: database.ID(it.WarehouseID), ZoneID: database.ID(it.ZoneID),
		ShelfID: database.ID(it.ShelfID), BinID: database.ID(it.BinID),
		QtySystem: it.QtySystem, CountedBy: database.ID(it.CountedBy),
		CountedAt: it.CountedAt, SerialNo: it.SerialNo,
	}
	if it.QtyCounted.Valid {
		q := it.QtyCounted.Qty
		v.QtyCounted = &q
	}
	return v
}

type CountDifferenceView struct {
	ID          database.ID `json:"id"`
	LineNo      int         `json:"line_no"`
	SKUID       database.ID `json:"sku_id"`
	WarehouseID database.ID `json:"warehouse_id"`
	BinID       database.ID `json:"bin_id"`
	BatchID     database.ID `json:"batch_id"`
	QtySystem   stock.Qty   `json:"qty_system"`
	QtyCounted  stock.Qty   `json:"qty_counted"`
	DiffQty     stock.Qty   `json:"diff_qty"` // 盘盈为正
	AdjustNo    string      `json:"adjust_no"`
	Status      string      `json:"status"`
	Remark      string      `json:"remark"`
}

func newCountDifferenceView(d *CountDifference) CountDifferenceView {
	return CountDifferenceView{
		ID: d.ID, LineNo: d.LineNo, SKUID: database.ID(d.SKUID),
		WarehouseID: database.ID(d.WarehouseID), BinID: database.ID(d.BinID),
		BatchID:   database.ID(d.BatchID),
		QtySystem: d.QtySystem, QtyCounted: d.QtyCounted, DiffQty: d.DiffQty,
		AdjustNo: d.AdjustNo, Status: d.Status, Remark: d.Remark,
	}
}

type CountDetailView struct {
	Order       CountOrderView        `json:"order"`
	Items       []CountItemView       `json:"items"`
	Differences []CountDifferenceView `json:"differences"`
}

// ---- 参数解析（api.md §4；与 inventory 域 handler 同口径的本地实现）----

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

// scopeOf 当前用户的数据权限仓库范围（auth.WarehouseScope 快照，plan §7.4）。
func scopeOf(c *gin.Context) Scope {
	all, ids := auth.WarehouseScope(c)
	return Scope{AllWarehouses: all, WarehouseIDs: ids}
}

// actorOf 库存原语与审计的操作者归因（经 auth.CurrentUser 带出）。
// 同时注入数据权限仓库范围快照（f16：按 ID 直取的写动作在 Service 层
// fail-closed 校验仓库范围，permission.md §4）。
func actorOf(c *gin.Context) stock.Actor {
	uc, _ := auth.CurrentUser(c)
	all, ids := auth.WarehouseScope(c)
	return stock.Actor{
		ID:        uc.UserID,
		Name:      uc.Username,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
		Scope:     &stock.WhScope{All: all, WarehouseIDs: ids},
	}
}

// ---- 幂等键（效率层一期计划 §2.10；api.md §7「行级幂等键 × 头键合成规则」）----

// maxIdemHeaderLen Idempotency-Key 头长度上限（api.md §7 冻结 ≤32；列宽核验：最长
// 行级通式 trout:{TR-单号}:{行号}:{bin}:{sku}:{batch} 与 32 字符头键拼接在现实
// 主键量级下 ≤128——inventory_ledgers.idempotency_key varchar(128)，inventory 侧
// validateIdempotencyKey(128) 兜底 fail-closed）。
const maxIdemHeaderLen = 32

// idempotencyKeyOf 读取 Idempotency-Key 请求头（purchase handler.go handleReceiptConfirm
// 同款 c.GetHeader 读法）：存在且非空 → 参与行级键合成；缺省 → 空串（行级键与存量
// 通式形态完全一致，零行为变化）；超长 → 400 invalidParam。
func idempotencyKeyOf(c *gin.Context) (string, error) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(key) > maxIdemHeaderLen {
		return "", response.NewError(response.CodeInvalidParam, map[string]any{
			"field":  "Idempotency-Key",
			"reason": "长度不能超过 " + strconv.Itoa(maxIdemHeaderLen),
		})
	}
	return key, nil
}

// composeIdemKey 行级幂等键合成（键合成唯一实现点，调用点禁止复制粘贴）：
// 头键存在且非空 → "{头键}:{原行级通式}"——新命名空间，同头键重试=同行键，命中
// uk_inventory_ledgers_idempotency_key 部分唯一索引即原语重放既有结果，不重复扣加
// 库存/流水；头键缺省 → 原行级通式原样返回（零行为变化）。
func composeIdemKey(headerKey, rowKey string) string {
	if headerKey == "" {
		return rowKey
	}
	return headerKey + ":" + rowKey
}

func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return false
	}
	return true
}

// ---- 调拨 /api/transfers ----

// moveBin 仓内移库（POST /api/inventory/moves；stockops:move:execute）。
// @Summary 仓内移库
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param body body MoveInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/moves [post]
func (h *handler) moveBin(c *gin.Context) {
	var in MoveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	// 幂等键头透传（§2.10：仅产生 ledger 流水的写路径消费；缺省零行为变化）。
	idemKey, err := idempotencyKeyOf(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	res, err := h.svc.MoveBin(c.Request.Context(), actorOf(c), in, idemKey)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// @Summary POST /api/transfers
// @Tags 库存作业
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers [post]
func (h *handler) createTransfer(c *gin.Context) {
	var in TransferInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := h.svc.CreateTransfer(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newTransferDetailView(detail))
}

// @Summary PUT /api/transfers/:id
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id} [put]
func (h *handler) updateTransfer(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in TransferInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := h.svc.UpdateTransfer(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newTransferDetailView(detail))
}

// @Summary GET /api/transfers/:id
// @Tags 库存作业
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id} [get]
func (h *handler) getTransfer(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.GetTransferDetail(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newTransferDetailView(detail))
}

// @Summary GET /api/transfers
// @Tags 库存作业
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers [get]
func (h *handler) listTransfers(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	var q TransferFilter
	sc := scopeOf(c)
	q.AllWarehouses, q.WarehouseIDs = sc.AllWarehouses, sc.WarehouseIDs
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "to_warehouse_id": &q.ToWarehouseID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	q.Status = strings.TrimSpace(c.Query("status"))
	q.Type = strings.TrimSpace(c.Query("type"))
	q.TransferNo = strings.TrimSpace(c.Query("transfer_no"))
	rows, total, err := h.svc.ListTransfers(c.Request.Context(), q, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]TransferOrderView, 0, len(rows))
	for i := range rows {
		items = append(items, newTransferOrderView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/transfers/in-transit
// @Tags 库存作业
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/in-transit [get]
func (h *handler) listInTransit(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if !handlePageErr(c, err) {
		return
	}
	var f InTransitFilter
	sc := scopeOf(c)
	f.AllWarehouses, f.WarehouseIDs = sc.AllWarehouses, sc.WarehouseIDs
	whID, ok := parseIDQuery(c, "warehouse_id")
	if !ok {
		return
	}
	f.WarehouseID = whID
	if f.SKUID, ok = parseIDQuery(c, "sku_id"); !ok {
		return
	}
	rows, total, err := h.svc.ListInTransit(c.Request.Context(), f, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// submit / approve / outbound / arrive / receive / cancel 共用"迁移类动作"响应形态：
// 迁移后单据详情（replay 语义由 Service 层保证：重复提交同迁移返回既有状态）。
// @Summary / approve / outbound / arrive / receive / cancel 共用"迁移类动作"响应形态：
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/submit [post]
func (h *handler) submitTransfer(c *gin.Context) {
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.SubmitTransfer(ctx.Request.Context(), actorOf(ctx), id)
	}, "submit")
}

// @Summary POST /api/transfers/:id/approve
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/approve [post]
func (h *handler) approveTransfer(c *gin.Context) {
	var in ApproveTransferInput
	if !bindJSON(c, &in) {
		return
	}
	idemKey, err := idempotencyKeyOf(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.ApproveTransfer(ctx.Request.Context(), actorOf(ctx), id, in, idemKey)
	}, "approve")
}

// @Summary POST /api/transfers/:id/outbound
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/outbound [post]
func (h *handler) outboundTransfer(c *gin.Context) {
	idemKey, err := idempotencyKeyOf(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.OutboundTransfer(ctx.Request.Context(), actorOf(ctx), id, idemKey)
	}, "outbound")
}

// @Summary POST /api/transfers/:id/arrive
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/arrive [post]
func (h *handler) arriveTransfer(c *gin.Context) {
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.ArriveTransfer(ctx.Request.Context(), actorOf(ctx), id)
	}, "arrive")
}

// @Summary POST /api/transfers/:id/receive
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/receive [post]
func (h *handler) receiveTransfer(c *gin.Context) {
	idemKey, err := idempotencyKeyOf(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.ReceiveTransfer(ctx.Request.Context(), actorOf(ctx), id, idemKey)
	}, "receive")
}

// @Summary POST /api/transfers/:id/cancel
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/transfers/{id}/cancel [post]
func (h *handler) cancelTransfer(c *gin.Context) {
	var in struct {
		Reason string `json:"reason"`
	}
	if c.Request.Body != nil {
		if !bindJSON(c, &in) {
			return
		}
	}
	h.transferAction(c, func(svc *Service, ctx *gin.Context, id int64) (*TransferDetail, bool, error) {
		return svc.CancelTransfer(ctx.Request.Context(), actorOf(ctx), id, in.Reason)
	}, "cancel")
}

func (h *handler) transferAction(c *gin.Context,
	fn func(*Service, *gin.Context, int64) (*TransferDetail, bool, error), _ string) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	detail, _, err := fn(h.svc, c, id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newTransferDetailView(detail))
}

func newTransferDetailView(d *TransferDetail) TransferDetailView {
	v := TransferDetailView{Order: newTransferOrderView(&d.Order), Items: make([]TransferItemView, 0, len(d.Items))}
	for i := range d.Items {
		v.Items = append(v.Items, newTransferItemView(&d.Items[i]))
	}
	return v
}

// ---- 盘点 /api/counts ----

// @Summary POST /api/counts
// @Tags 库存作业
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts [post]
func (h *handler) createCount(c *gin.Context) {
	var in CountInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := h.svc.CreateCount(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newCountDetailView(detail))
}

// @Summary GET /api/counts/:id
// @Tags 库存作业
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id} [get]
func (h *handler) getCount(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.GetCountDetail(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newCountDetailView(detail))
}

// @Summary GET /api/counts
// @Tags 库存作业
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts [get]
func (h *handler) listCounts(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if !handlePageErr(c, err) {
		return
	}
	var q CountFilter
	sc := scopeOf(c)
	q.AllWarehouses, q.WarehouseIDs = sc.AllWarehouses, sc.WarehouseIDs
	whID, ok := parseIDQuery(c, "warehouse_id")
	if !ok {
		return
	}
	q.WarehouseID = whID
	q.Status = strings.TrimSpace(c.Query("status"))
	q.CountNo = strings.TrimSpace(c.Query("count_no"))
	rows, total, err := h.svc.ListCounts(c.Request.Context(), q, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]CountOrderView, 0, len(rows))
	for i := range rows {
		items = append(items, newCountOrderView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary POST /api/counts/:id/start
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/start [post]
func (h *handler) startCount(c *gin.Context) {
	h.countAction(c, func(svc *Service, ctx *gin.Context, id int64) (*CountDetail, bool, error) {
		return svc.StartCount(ctx.Request.Context(), actorOf(ctx), id)
	})
}

// @Summary POST /api/counts/:id/finish
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/finish [post]
func (h *handler) finishCount(c *gin.Context) {
	h.countAction(c, func(svc *Service, ctx *gin.Context, id int64) (*CountDetail, bool, error) {
		return svc.FinishCount(ctx.Request.Context(), actorOf(ctx), id)
	})
}

// @Summary PUT /api/counts/:id/items
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/items [put]
func (h *handler) registerCount(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var in CountRegistrationInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := h.svc.RegisterCountings(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newCountDetailView(detail))
}

// @Summary POST /api/counts/:id/complete
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/complete [post]
func (h *handler) completeCount(c *gin.Context) {
	var in struct {
		Opinion string `json:"opinion"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if !bindJSON(c, &in) {
			return
		}
	}
	idemKey, err := idempotencyKeyOf(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.countAction(c, func(svc *Service, ctx *gin.Context, id int64) (*CountDetail, bool, error) {
		return svc.CompleteCount(ctx.Request.Context(), actorOf(ctx), id, in.Opinion, idemKey)
	})
}

// @Summary POST /api/counts/:id/reject
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/reject [post]
func (h *handler) rejectCount(c *gin.Context) {
	var in struct {
		Opinion string `json:"opinion"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if !bindJSON(c, &in) {
			return
		}
	}
	h.countAction(c, func(svc *Service, ctx *gin.Context, id int64) (*CountDetail, bool, error) {
		return svc.RejectCount(ctx.Request.Context(), actorOf(ctx), id, in.Opinion)
	})
}

// @Summary POST /api/counts/:id/cancel
// @Tags 库存作业
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/counts/{id}/cancel [post]
func (h *handler) cancelCount(c *gin.Context) {
	var in struct {
		Reason string `json:"reason"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if !bindJSON(c, &in) {
			return
		}
	}
	h.countAction(c, func(svc *Service, ctx *gin.Context, id int64) (*CountDetail, bool, error) {
		return svc.CancelCount(ctx.Request.Context(), actorOf(ctx), id, in.Reason)
	})
}

func (h *handler) countAction(c *gin.Context,
	fn func(*Service, *gin.Context, int64) (*CountDetail, bool, error)) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	detail, _, err := fn(h.svc, c, id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, newCountDetailView(detail))
}

func newCountDetailView(d *CountDetail) CountDetailView {
	v := CountDetailView{
		Order:       newCountOrderView(&d.Order),
		Items:       make([]CountItemView, 0, len(d.Items)),
		Differences: make([]CountDifferenceView, 0, len(d.Differences)),
	}
	for i := range d.Items {
		v.Items = append(v.Items, newCountItemView(&d.Items[i]))
	}
	for i := range d.Differences {
		v.Differences = append(v.Differences, newCountDifferenceView(&d.Differences[i]))
	}
	return v
}

func handlePageErr(c *gin.Context, err error) bool {
	if err != nil {
		response.Err(c, err)
		return false
	}
	return true
}
