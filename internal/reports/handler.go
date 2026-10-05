package reports

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// handler 报表 HTTP 层（禁止直连数据库，architecture §1）：参数解析/校验、
// 数据权限快照注入、统一信封与分页（api.md §2）。
type handler struct {
	svc *Service
}

// scopeOf 当前用户仓库数据权限快照（permission.md §4：以会话快照为准，
// 禁止接受前端范围参数；M1 部门/个人范围在仓库维度不可见任何行——fail-closed）。
func scopeOf(c *gin.Context) Scope {
	all, ids := auth.WarehouseScope(c)
	return Scope{AllWarehouses: all, WarehouseIDs: ids}
}

// requireRange 解析并校验时间范围（time_from/time_to，缺省近 30 天；上限 366 天）。
// 失败已写响应并返回 false。
func (h *handler) requireRange(c *gin.Context) (time.Time, time.Time, bool) {
	now := time.Now()
	var fromPtr, toPtr *time.Time
	if raw := c.Query("time_from"); raw != "" {
		t, err := parseDay(raw)
		if err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "time_from", "reason": err.Error()}))
			return time.Time{}, time.Time{}, false
		}
		fromPtr = &t
	}
	if raw := c.Query("time_to"); raw != "" {
		t, err := parseDay(raw)
		if err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "time_to", "reason": err.Error()}))
			return time.Time{}, time.Time{}, false
		}
		// 按天语义：纯日期 time_to（YYYY-MM-DD）视为"含当天"——查询右界为排他
		// （created_at < to），须前进到次日零点，否则末整天的流水/单据被整体排除
		// （2026-10-05 实录：出库分析页 time_to=今天 → 当日落账全部不可见）。
		// 带时刻的 time_to（YYYY-MM-DD HH:mm:ss）维持精确边界不变。
		if len(strings.TrimSpace(raw)) == 10 {
			t = t.AddDate(0, 0, 1)
		}
		toPtr = &t
	}
	from, to, err := validateRange(fromPtr, toPtr, now)
	if err != nil {
		response.Err(c, response.NewError(ErrRangeTooLarge, gin.H{"reason": err.Error(), "max_days": maxRangeDays}))
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// listOut 输出统一分页信封。
func listOut(c *gin.Context, items any, page, pageSize int, total int64) {
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/reports
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports [get]
func (h *handler) catalog(c *gin.Context) {
	response.OK(c, h.svc.Catalog())
}

// @Summary GET /api/reports/inventory-summary
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/inventory-summary [get]
func (h *handler) inventorySummary(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	warehouseID, ok := parseInt64Query(c, "warehouse_id")
	if !ok {
		return
	}
	skUID, ok := parseInt64Query(c, "sku_id")
	if !ok {
		return
	}
	rows, total, err := h.svc.InventorySummary(c.Request.Context(), scopeOf(c), warehouseID, skUID, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, rows, page, pageSize, total)
}

// @Summary GET /api/reports/inbound-stats
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/inbound-stats [get]
func (h *handler) inboundStats(c *gin.Context) {
	h.flowStats(c, true)
}

// @Summary GET /api/reports/outbound-stats
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/outbound-stats [get]
func (h *handler) outboundStats(c *gin.Context) {
	h.flowStats(c, false)
}

func (h *handler) flowStats(c *gin.Context, inbound bool) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	var rows []FlowStatRow
	var total int64
	if inbound {
		rows, total, err = h.svc.InboundStats(c.Request.Context(), scopeOf(c), from, to, page, pageSize)
	} else {
		rows, total, err = h.svc.OutboundStats(c.Request.Context(), scopeOf(c), from, to, page, pageSize)
	}
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, gin.H{
		"items":     rows,
		"total":     total,
		"valuation": map[string]string{"basis": flowValuationBasis(inbound)},
	}, page, pageSize, total)
}

// flowValuationBasis 金额估值口径说明（现场聚合无订单金额列，按 SKU 单价估值——口径显式披露）。
func flowValuationBasis(inbound bool) string {
	if inbound {
		return "cost_price" // 入库金额 = Σ|INBOUND 流水量| × SKU 成本价
	}
	return "sale_price" // 出库金额 = Σ|OUTBOUND 流水量| × SKU 销售价
}

// @Summary GET /api/reports/inventory-turnover
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/inventory-turnover [get]
func (h *handler) turnover(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	rows, total, err := h.svc.InventoryTurnover(c.Request.Context(), scopeOf(c), from, to, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, rows, page, pageSize, total)
}

// @Summary GET /api/reports/stagnant-stock
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/stagnant-stock [get]
func (h *handler) stagnantStock(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	rows, total, err := h.svc.StagnantStock(c.Request.Context(), scopeOf(c), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, rows, page, pageSize, total)
}

// @Summary GET /api/reports/replenishment-suggestions
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/replenishment-suggestions [get]
func (h *handler) replenishment(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	onlyShortage := true
	if raw := c.Query("only_shortage"); raw == "false" {
		onlyShortage = false
	}
	rows, total, err := h.svc.ReplenishmentSuggestions(c.Request.Context(), scopeOf(c), onlyShortage, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, rows, page, pageSize, total)
}

// dashboardSummary GET /api/inventory/summary（reports 实现、inventory 前缀挂载）。
// @Summary GET /api/inventory/summary（reports 实现、inventory 前缀挂载）
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/summary [get]
func (h *handler) dashboardSummary(c *gin.Context) {
	row, err := h.svc.DashboardSummary(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, row)
}

// dashboardAlerts GET /api/inventory/alerts。
// @Summary GET /api/inventory/alerts
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/alerts [get]
func (h *handler) dashboardAlerts(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	level := c.Query("level")
	if level != "" && !validAlertLevel(level) {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "level", "reason": "应为 low_stock/overstock/near_expiry/expired/slow_moving 之一",
		}))
		return
	}
	rows, total, err := h.svc.DashboardAlerts(c.Request.Context(), scopeOf(c), level, c.Query("keyword"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, rows, page, pageSize, total)
}

func validAlertLevel(level string) bool {
	switch level {
	case "low_stock", "overstock", "near_expiry", "expired", "slow_moving":
		return true
	}
	return false
}

// parseInt64Query 可选整数查询参数；非法值写 400 响应并返回 ok=false（调用方必须终止）。
func parseInt64Query(c *gin.Context, key string) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": key, "reason": "必须为整数"}))
		return 0, false
	}
	return v, true
}
