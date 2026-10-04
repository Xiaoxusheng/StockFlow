package inventory

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。

// 权限点（plan §5.4.1 权限点全量冻结）：全部复用 auth 域导出的冻结常量
// （internal/auth/permissions.go，单一事实来源；inventory:batch / inventory:serial
// 已随 auth 种子与 plan §5.4.1 同步补录）。

// handler 库存域 HTTP handler（RegisterRoutes 装配时构造，依赖经构造注入）。
type handler struct {
	svc *Service
}

// ---- 响应视图（ID 序列化为字符串：backend-m1-plan §1；数量为 numeric(18,4) 裸数字）----

// InventoryView 实时库存视图（五维定位 + 六状态数量）。
type InventoryView struct {
	ID          database.ID `json:"id"`
	WarehouseID database.ID `json:"warehouse_id"`
	ZoneID      database.ID `json:"zone_id"`
	ShelfID     database.ID `json:"shelf_id"`
	BinID       database.ID `json:"bin_id"`
	SKUID       database.ID `json:"sku_id"`
	BatchID     database.ID `json:"batch_id"` // 0=非批次 SKU
	StockState
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
}

func newInventoryView(m *Inventory) InventoryView {
	return InventoryView{
		ID: m.ID, WarehouseID: database.ID(m.WarehouseID), ZoneID: database.ID(m.ZoneID),
		ShelfID: database.ID(m.ShelfID), BinID: database.ID(m.BinID),
		SKUID: database.ID(m.SKUID), BatchID: database.ID(m.BatchID),
		StockState: m.State(),
		CreatedAt:  m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// LedgerView 库存流水视图（inventory-rules §5 字段清单；append-only 只读）。
type LedgerView struct {
	ID             database.ID       `json:"id"`
	LedgerNo       string            `json:"ledger_no"`
	SKUID          database.ID       `json:"sku_id"`
	WarehouseID    database.ID       `json:"warehouse_id"`
	ZoneID         *database.ID      `json:"zone_id"`
	ShelfID        *database.ID      `json:"shelf_id"`
	BinID          database.ID       `json:"bin_id"`
	BatchID        database.ID       `json:"batch_id"`
	SerialNo       string            `json:"serial_no"`
	ChangeType     string            `json:"change_type"`
	BusinessType   string            `json:"business_type"`
	BusinessNo     string            `json:"business_no"`
	StatusFrom     string            `json:"status_from"`
	StatusTo       string            `json:"status_to"`
	QtyBefore      Qty               `json:"qty_before"`
	QtyChange      Qty               `json:"qty_change"`
	QtyAfter       Qty               `json:"qty_after"`
	IdempotencyKey string            `json:"idempotency_key"`
	OperatorID     database.ID       `json:"operator_id"`
	OperatorName   string            `json:"operator_name"`
	RequestID      string            `json:"request_id"`
	Remark         string            `json:"remark"`
	CreatedAt      database.JSONTime `json:"created_at"`
}

func newLedgerView(m *InventoryLedger) LedgerView {
	v := LedgerView{
		ID: m.ID, LedgerNo: m.LedgerNo,
		SKUID: database.ID(m.SKUID), WarehouseID: database.ID(m.WarehouseID),
		BinID: database.ID(m.BinID), BatchID: database.ID(m.BatchID),
		SerialNo: m.SerialNo, ChangeType: m.ChangeType,
		BusinessType: m.BusinessType, BusinessNo: m.BusinessNo,
		StatusFrom: m.StatusFrom, StatusTo: m.StatusTo,
		QtyBefore: m.QtyBefore, QtyChange: m.QtyChange, QtyAfter: m.QtyAfter,
		OperatorID: database.ID(m.OperatorID), OperatorName: m.OperatorName,
		RequestID: m.RequestID, Remark: m.Remark, CreatedAt: m.CreatedAt,
	}
	if m.ZoneID != nil {
		z := database.ID(*m.ZoneID)
		v.ZoneID = &z
	}
	if m.ShelfID != nil {
		s := database.ID(*m.ShelfID)
		v.ShelfID = &s
	}
	if m.IdempotencyKey != nil {
		v.IdempotencyKey = *m.IdempotencyKey
	}
	return v
}

// BatchView 批次视图（inventory-rules §6 字段清单）。
type BatchView struct {
	ID             database.ID       `json:"id"`
	SKUID          database.ID       `json:"sku_id"`
	BatchNo        string            `json:"batch_no"`
	SupplierID     database.ID       `json:"supplier_id"`
	ProductionDate database.JSONTime `json:"production_date"`
	InboundDate    database.JSONTime `json:"inbound_date"`
	ExpiryDate     database.JSONTime `json:"expiry_date"`
	CostPrice      Qty               `json:"cost_price"`
	Remark         string            `json:"remark"`
	CreatedAt      database.JSONTime `json:"created_at"`
	UpdatedAt      database.JSONTime `json:"updated_at"`
}

func newBatchView(m *Batch) BatchView {
	return BatchView{
		ID: m.ID, SKUID: database.ID(m.SKUID), BatchNo: m.BatchNo,
		SupplierID:     database.ID(m.SupplierID),
		ProductionDate: m.ProductionDate, InboundDate: m.InboundDate, ExpiryDate: m.ExpiryDate,
		CostPrice: m.CostPrice, Remark: m.Remark, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// SerialView 序列号视图（inventory-rules §8：一物一行 + 最近状态变化追溯指针）。
type SerialView struct {
	ID             database.ID       `json:"id"`
	SerialNo       string            `json:"serial_no"`
	SKUID          database.ID       `json:"sku_id"`
	BatchID        database.ID       `json:"batch_id"`
	WarehouseID    database.ID       `json:"warehouse_id"` // 0=不在库
	BinID          database.ID       `json:"bin_id"`       // 0=不在库
	Status         string            `json:"status"`
	LastSourceType string            `json:"last_source_type"`
	LastSourceNo   string            `json:"last_source_no"`
	LastEventAt    database.JSONTime `json:"last_event_at"`
	CreatedAt      database.JSONTime `json:"created_at"`
	UpdatedAt      database.JSONTime `json:"updated_at"`
}

func newSerialView(m *SerialNumber) SerialView {
	return SerialView{
		ID: m.ID, SerialNo: m.SerialNo, SKUID: database.ID(m.SKUID),
		BatchID: database.ID(m.BatchID), WarehouseID: database.ID(m.WarehouseID),
		BinID: database.ID(m.BinID), Status: m.Status,
		LastSourceType: m.LastSourceType, LastSourceNo: m.LastSourceNo, LastEventAt: m.LastEventAt,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// ---- 参数解析（api.md §4：后端完整校验，非法即 COMMON_INVALID_PARAM + details）----

// timeLayouts 接受的时间格式（api.md §2 统一格式 + 纯日期）。
var timeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02"}

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

// parseIDQuery 解析可选的正整数查询参数（0=未提供）。parse 失败返回 ok=false（已写响应）。
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

func parseBatchIDQuery(c *gin.Context) (*int64, bool) {
	v, ok := parseIDQuery(c, "batch_id")
	if !ok {
		return nil, false
	}
	return &v, true
}

// parseTimeParam 解析时间范围端点（from=含下界；to=含上界，纯日期补全为当日 23:59:59）。
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

// scopeOf 当前用户的数据权限仓库范围（auth.WarehouseScope 快照，plan §7.4）。
func scopeOf(c *gin.Context) Scope {
	all, ids := auth.WarehouseScope(c)
	return Scope{AllWarehouses: all, WarehouseIDs: ids}
}

// ---- GET /api/inventory（列表）----

// @Summary GET /api/inventory
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory [get]
func (h *handler) listInventory(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := InventoryQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "zone_id": &q.ZoneID, "shelf_id": &q.ShelfID,
		"bin_id": &q.BinID, "sku_id": &q.SKUID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	batchID, ok := parseBatchIDQuery(c)
	if !ok {
		return
	}
	q.BatchID = batchID
	rows, total, err := h.svc.QueryInventory(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]InventoryView, 0, len(rows))
	for i := range rows {
		items = append(items, newInventoryView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- GET /api/inventory/{id}（详情）----

// @Summary GET /api/inventory/:id
// @Tags 库存
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/{id} [get]
func (h *handler) getInventory(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	row, err := h.svc.GetInventoryDetail(c.Request.Context(), id, scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	if row == nil {
		response.Err(c, response.NewError(response.CodeNotFound, map[string]any{"id": id}))
		return
	}
	response.OK(c, newInventoryView(row))
}

// ---- GET /api/inventory-ledgers（流水，只读）----

// @Summary GET /api/inventory-ledgers
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory-ledgers [get]
func (h *handler) listLedgers(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := LedgerQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	var ok bool
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "sku_id": &q.SKUID, "bin_id": &q.BinID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	if q.BatchID, ok = parseBatchIDQuery(c); !ok {
		return
	}
	q.ChangeType = strings.TrimSpace(c.Query("change_type"))
	if q.ChangeType != "" && !changeTypes[q.ChangeType] {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "change_type", "reason": "非法流水类型",
			"allowed": []string{
				"INBOUND", "OUTBOUND", "TRANSFER_OUT", "TRANSFER_IN", "LOCK",
				"RELEASE", "MOVE", "INSPECT_PASS", "INSPECT_DEFECTIVE", "ADJUST",
			},
		}))
		return
	}
	q.BusinessNo = strings.TrimSpace(c.Query("business_no"))
	q.SerialNo = strings.TrimSpace(c.Query("serial_no"))
	if q.CreatedFrom, ok = parseTimeParam(c, "created_from", false); !ok {
		return
	}
	if q.CreatedTo, ok = parseTimeParam(c, "created_to", true); !ok {
		return
	}
	if q.CreatedFrom != nil && q.CreatedTo != nil && q.CreatedTo.Before(*q.CreatedFrom) {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "created_to", "reason": "必须不早于 created_from",
		}))
		return
	}
	rows, total, err := h.svc.QueryLedgers(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]LedgerView, 0, len(rows))
	for i := range rows {
		items = append(items, newLedgerView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- GET /api/batches（批次台账）----

// @Summary GET /api/batches
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/batches [get]
func (h *handler) listBatches(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := BatchQuery{Page: page, PageSize: pageSize}
	var ok bool
	for field, dst := range map[string]*int64{
		"sku_id": &q.SKUID, "supplier_id": &q.SupplierID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	q.BatchNo = strings.TrimSpace(c.Query("batch_no"))
	if q.ExpiryFrom, ok = parseTimeParam(c, "expiry_from", false); !ok {
		return
	}
	if q.ExpiryTo, ok = parseTimeParam(c, "expiry_to", true); !ok {
		return
	}
	q.ExpiryFirst = c.Query("order") == "expiry" // 按效期升序（FEFO 审阅视图）
	rows, total, err := h.svc.QueryBatches(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]BatchView, 0, len(rows))
	for i := range rows {
		items = append(items, newBatchView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- GET /api/serials（序列号）----

// @Summary GET /api/serials
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/serials [get]
func (h *handler) listSerials(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := SerialQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	var ok bool
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "bin_id": &q.BinID, "sku_id": &q.SKUID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	if q.BatchID, ok = parseBatchIDQuery(c); !ok {
		return
	}
	q.SerialNo = strings.TrimSpace(c.Query("serial_no"))
	q.Status = strings.TrimSpace(c.Query("status"))
	if q.Status != "" && !serialStatuses[q.Status] {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "status", "reason": "非法序列号状态",
			"allowed": []string{"IN_STOCK", "LOCKED", "OUTBOUND", "RETURNED", "FROZEN"},
		}))
		return
	}
	rows, total, err := h.svc.QuerySerials(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]SerialView, 0, len(rows))
	for i := range rows {
		items = append(items, newSerialView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- 锁定记录 / 调整单列表（GET /api/inventory/locks、/adjustments，plan §8.3 条 5）----

// LockView 库存锁定记录视图（inventory-rules §4 字段清单）。
type LockView struct {
	ID          database.ID       `json:"id"`
	WarehouseID database.ID       `json:"warehouse_id"`
	BinID       database.ID       `json:"bin_id"`
	SKUID       database.ID       `json:"sku_id"`
	BatchID     database.ID       `json:"batch_id"`
	LockType    string            `json:"lock_type"`
	SourceType  string            `json:"source_type"`
	SourceNo    string            `json:"source_no"`
	Qty         Qty               `json:"qty"`
	Status      string            `json:"status"`
	ReleasedAt  database.JSONTime `json:"released_at"`
	ReleasedBy  database.ID       `json:"released_by"`
	Remark      string            `json:"remark"`
	CreatedAt   database.JSONTime `json:"created_at"`
	UpdatedAt   database.JSONTime `json:"updated_at"`
}

func newLockView(m *InventoryLock) LockView {
	return LockView{
		ID: m.ID, WarehouseID: database.ID(m.WarehouseID), BinID: database.ID(m.BinID),
		SKUID: database.ID(m.SKUID), BatchID: database.ID(m.BatchID),
		LockType: m.LockType, SourceType: m.SourceType, SourceNo: m.SourceNo,
		Qty: m.Qty, Status: m.Status, ReleasedAt: m.ReleasedAt,
		ReleasedBy: database.ID(m.ReleasedBy), Remark: m.Remark,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// AdjustmentView 库存调整单视图（business-flow §11.1 字段清单）。
type AdjustmentView struct {
	ID           database.ID       `json:"id"`
	AdjustmentNo string            `json:"adjustment_no"`
	WarehouseID  database.ID       `json:"warehouse_id"`
	SKUID        database.ID       `json:"sku_id"`
	BinID        database.ID       `json:"bin_id"`
	BatchID      database.ID       `json:"batch_id"`
	AdjustType   string            `json:"adjust_type"`
	Qty          Qty               `json:"qty"`
	Reason       string            `json:"reason"`
	Status       string            `json:"status"`
	ExecutedBy   database.ID       `json:"executed_by"`
	ExecutedAt   database.JSONTime `json:"executed_at"`
	CreatedAt    database.JSONTime `json:"created_at"`
	UpdatedAt    database.JSONTime `json:"updated_at"`
}

func newAdjustmentView(m *InventoryAdjustment) AdjustmentView {
	return AdjustmentView{
		ID: m.ID, AdjustmentNo: m.AdjustmentNo,
		WarehouseID: database.ID(m.WarehouseID), SKUID: database.ID(m.SKUID),
		BinID: database.ID(m.BinID), BatchID: database.ID(m.BatchID),
		AdjustType: m.AdjustType, Qty: m.Qty, Reason: m.Reason, Status: m.Status,
		ExecutedBy: database.ID(m.ExecutedBy), ExecutedAt: m.ExecutedAt,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// @Summary GET /api/inventory/locks
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/locks [get]
func (h *handler) listLocks(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := LockQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "sku_id": &q.SKUID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	q.LockType = strings.TrimSpace(c.Query("lock_type"))
	if q.LockType != "" && !lockTypes[q.LockType] {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "lock_type", "reason": "非法锁定类型",
			"allowed": []string{"ORDER_HOLD", "COUNT_FREEZE", "QC_FREEZE", "MANUAL_FREEZE", "EXCEPTION_FREEZE"},
		}))
		return
	}
	q.Status = strings.TrimSpace(c.Query("status"))
	if q.Status != "" && q.Status != "ACTIVE" && q.Status != "RELEASED" && q.Status != "CONSUMED" {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "status", "reason": "非法锁定状态", "allowed": []string{"ACTIVE", "RELEASED", "CONSUMED"},
		}))
		return
	}
	q.SourceType = strings.TrimSpace(c.Query("source_type"))
	q.SourceNo = strings.TrimSpace(c.Query("source_no"))
	rows, total, err := h.svc.QueryLocks(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]LockView, 0, len(rows))
	for i := range rows {
		items = append(items, newLockView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/inventory/adjustments
// @Tags 库存
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/adjustments [get]
func (h *handler) listAdjustments(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	q := AdjustmentQuery{Scope: scopeOf(c), Page: page, PageSize: pageSize}
	for field, dst := range map[string]*int64{
		"warehouse_id": &q.WarehouseID, "sku_id": &q.SKUID,
	} {
		v, ok := parseIDQuery(c, field)
		if !ok {
			return
		}
		*dst = v
	}
	q.AdjustType = strings.TrimSpace(c.Query("adjust_type"))
	if q.AdjustType != "" && !adjustTypes[q.AdjustType] {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "adjust_type", "reason": "非法调整类型",
			"allowed": []string{"盘盈", "盘亏", "损耗", "报废", "其他"},
		}))
		return
	}
	q.Status = strings.TrimSpace(c.Query("status"))
	rows, total, err := h.svc.QueryAdjustments(c.Request.Context(), q)
	if err != nil {
		response.Err(c, err)
		return
	}
	items := make([]AdjustmentView, 0, len(rows))
	for i := range rows {
		items = append(items, newAdjustmentView(&rows[i]))
	}
	response.OKPage(c, items, page, pageSize, total)
}
