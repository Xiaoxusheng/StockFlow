package reports

// 工作台优先处理统计端点（2026-10-06 工作台『我现在该做什么』改版二期——web/src/views/
// workbench/WorkbenchPage.tsx 消费；docs/api.md §9 契约由集成轮补录）：
//
//	GET /api/workbench/priorities?limit=5
//
// 响应四组（workbenchPrioritiesDTO，每组 count + items 尾 N 条）：
//   - overdue_receipts  超时收货：inbound_orders 状态 RECEIVING 且 created_at 早于截止线
//     （截止线 = now − task.timeout.receive_hours 小时；该键与 task.timeout.putaway_hours/
//     pick_hours 同族同缺省 4 小时——sysops/configs.go seed 已注册该键（集成轮补行），
//     行缺失时 taskTimeoutHours 仍走缺省 4 兜底）。
//   - bin_exceptions    库位异常：exceptions 已定位到库位（bin_id>0）且未闭环
//     （NOT IN ('RESOLVED','CLOSED')）；exceptions 无仓库列（000010 DDL），
//     scopeOf/warehouse 过滤不生效——与 summary.exception_count、nextTask exception
//     分支同口径（api.md 披露先例）。
//   - near_expiry_stock 临期库存：inventory 按仓/SKU/批次聚合现存量为正，批次效期在
//     （今天, 今天+窗口] 内——窗口 = inventory.alert.expiry_days 阈值最大档（缺省 30 天，
//     DashboardSummary 同源口径）；已过期批次不入本组（expired 是预警页独立 level，
//     组口径与点击直达 /inventory/alerts?level=near_expiry 列表 total 同构）。
//   - pending_checks    待复核订单：存在 PENDING 复核任务的出库单（按 outbound_no 去重
//     计数，明细按单分组）——单据维度口径与 Dashboard tasks 块「待复核」（outbound
//     PICKED）的差异：序列号 SKU 一件一行复核任务（sales/service_outbound.go
//     createCheckTasksForPick），按任务单号去重即「待复核订单」的真实集合，且与
//     点击直达的 /checking?status=PENDING 任务列表同源。
//
// 排序：四组明细一律 created_at/到期日 ASC（先来先办 / 最先到期优先），真实 SQL
// ORDER BY，严禁前端推算（/api/tasks/next 同红线）。
// 只读约束：全部参数化 SELECT 零写语句（guard-readonly 红线，同 workbench.go）。
// 跨域零 import：SQL 直连单据/任务/异常/库存表（平台包口径，workbench.go:29 同）。
// 软删一致性：inbound_orders 内嵌 gorm.DeletedAt（迁移 000016）显式过滤；check_tasks/
// exceptions/batches/inventory 无软删列（000016 注）不加过滤。
// 数据范围：Scope 会话仓库快照（scopeOf），禁收前端范围参数；fail-closed 空集 count=0
// 明细空（exceptions 组无仓库列不受 scope 约束，见上）。
// 权限：auth.PermInventoryList（GET /api/workbench/summary 同码先例——计数版已在同码
// 暴露同一信息面，明细 TopN 不抬高敏感面；routes.go 接线由集成轮完成）。

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// cfgTaskTimeoutReceiveHours 收货超时阈值配置键（task.timeout.* 族，与 workbench.go
// cfgTaskTimeoutPickHours/PutawayHours 同源命名；seed 注册由集成轮决定，行缺失走缺省）。
const cfgTaskTimeoutReceiveHours = "task.timeout.receive_hours"

// priorityItem 优先处理明细行（四组统一形状，前端单渲染器；snake_case 契约）。
type priorityItem struct {
	ID int64 `json:"id"`
	// Title 主标题（入库单号 / 异常单号 / SKU 编码 / 出库单号）。
	Title string `json:"title"`
	// Subtitle 辅助行（仓库名 / 异常类型+仓库名 / 商品名+仓库名）。
	Subtitle string `json:"subtitle"`
	// Detail 明细说明（后端组装的人读文本，含来源单号/批次/剩余天数/任务数）。
	Detail string `json:"detail"`
	// Qty 数量类明细（近效期现存量；其余组为 nil 不下发）。
	Qty *float64 `json:"qty,omitempty"`
	// Status 原态（仅异常组：OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW——前端经
	// EXCEPTION_STATUS_TAG 渲染；其余组空串）。
	Status string `json:"status,omitempty"`
	// Time 时间锚点（created_at / 组内最早创建时间；近效期行无自然时间锚点为 null）。
	Time *database.JSONTime `json:"time,omitempty"`
}

// workbenchPriorityGroup 单组：count 全量计数 + items 尾 N 条（count ≠ len(items)——
// items 是 TopN 截断，前端以「共 N 条」披露全量）。
type workbenchPriorityGroup struct {
	Count int64          `json:"count"`
	Items []priorityItem `json:"items"`
}

// workbenchPrioritiesDTO GET /api/workbench/priorities 响应（四组键名冻结）。
type workbenchPrioritiesDTO struct {
	OverdueReceipts workbenchPriorityGroup `json:"overdue_receipts"`
	BinExceptions   workbenchPriorityGroup `json:"bin_exceptions"`
	NearExpiryStock workbenchPriorityGroup `json:"near_expiry_stock"`
	PendingChecks   workbenchPriorityGroup `json:"pending_checks"`
}

// ---------- Repository（只读参数化 SELECT） ----------

// overdueReceiptRow 超时收货明细行。
type overdueReceiptRow struct {
	ID            int64             `gorm:"column:id"`
	InboundNo     string            `gorm:"column:inbound_no"`
	WarehouseName string            `gorm:"column:warehouse_name"`
	SourceNo      string            `gorm:"column:source_no"`
	CreatedAt     database.JSONTime `gorm:"column:created_at"`
}

// 超时收货计数 + 明细（%1=仓库范围片段；created_at/截止线/limit 占位符为字面 ?）。
const (
	overdueReceiptsCountBase = `
SELECT COUNT(*)
FROM inbound_orders io
WHERE io.status = 'RECEIVING' AND io.deleted_at IS NULL AND %s AND io.created_at <= ?`
	overdueReceiptsListBase = `
SELECT io.id, io.inbound_no, w.name AS warehouse_name, io.source_no, io.created_at
FROM inbound_orders io
JOIN warehouses w ON w.id = io.warehouse_id
WHERE io.status = 'RECEIVING' AND io.deleted_at IS NULL AND %s AND io.created_at <= ?
ORDER BY io.created_at, io.id
LIMIT ?`
)

func (r *repository) overdueReceipts(ctx context.Context, sc Scope, cutoff time.Time, limit int) (int64, []overdueReceiptRow, error) {
	scCond, scArgs := sc.cond("io.warehouse_id")
	var count int64
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(overdueReceiptsCountBase, scCond),
		append(append([]any{}, scArgs...), cutoff)...).Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 超时收货计数失败: %w", err)
	}
	rows := make([]overdueReceiptRow, 0)
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(overdueReceiptsListBase, scCond),
		append(append(append([]any{}, scArgs...), cutoff), limit)...).Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 超时收货明细查询失败: %w", err)
	}
	return count, rows, nil
}

// binExceptionRow 库位异常明细行。
type binExceptionRow struct {
	ID          int64             `gorm:"column:id"`
	ExceptionNo string            `gorm:"column:exception_no"`
	Type        string            `gorm:"column:type"`
	Status      string            `gorm:"column:status"`
	SourceNo    string            `gorm:"column:source_no"`
	CreatedAt   database.JSONTime `gorm:"column:created_at"`
}

// 库位异常计数 + 明细（exceptions 无仓库列：无 scope 片段——文件头口径）。
const (
	binExceptionsCountBase = `
SELECT COUNT(*)
FROM exceptions e
WHERE e.bin_id > 0 AND e.status NOT IN ('RESOLVED', 'CLOSED')`
	binExceptionsListBase = `
SELECT e.id, e.exception_no, e.type, e.status, e.source_no, e.created_at
FROM exceptions e
WHERE e.bin_id > 0 AND e.status NOT IN ('RESOLVED', 'CLOSED')
ORDER BY e.created_at, e.id
LIMIT ?`
)

func (r *repository) binExceptions(ctx context.Context, limit int) (int64, []binExceptionRow, error) {
	var count int64
	if err := r.db.WithContext(ctx).Raw(binExceptionsCountBase).Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 库位异常计数失败: %w", err)
	}
	rows := make([]binExceptionRow, 0)
	if err := r.db.WithContext(ctx).Raw(binExceptionsListBase, limit).Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 库位异常明细查询失败: %w", err)
	}
	return count, rows, nil
}

// nearExpiryRow 临期库存明细行（days_left = 效期 − 今天，SQL 计算——禁前端推算）。
type nearExpiryRow struct {
	ID            int64   `gorm:"column:id"`
	SKUCode       string  `gorm:"column:sku_code"`
	SKUName       string  `gorm:"column:sku_name"`
	WarehouseName string  `gorm:"column:warehouse_name"`
	BatchNo       string  `gorm:"column:batch_no"`
	DaysLeft      int64   `gorm:"column:days_left"`
	Qty           float64 `gorm:"column:qty"`
}

// 临期库存计数 + 明细（%1=仓库范围片段作用于 inventory 聚合子查询；窗口参数 ?::interval
// 与 alerts expiry 分支同形——repository_alerts.go buildAlertBranch）。聚合行无自然主键，
// 明细 id 取组内 MIN(i.id)（每条 inventory 行恰属一个 (仓,SKU,批次) 组，MIN 必组间唯一）；
// 派生表 b 不含 expiry_date 列，days_left 由子查询内计算后透出。
const (
	nearExpiryCountBase = `
SELECT COUNT(*)
FROM (
    SELECT i.warehouse_id, i.sku_id, i.batch_id
    FROM inventory i
    WHERE %s
    GROUP BY 1, 2, 3
    HAVING SUM(i.total_qty) > 0
) t
JOIN batches b ON b.id = t.batch_id AND b.sku_id = t.sku_id
WHERE b.expiry_date > CURRENT_DATE AND b.expiry_date <= (CURRENT_DATE + ?::interval)`
	nearExpiryListBase = `
SELECT t.id, t.warehouse_id, s.code AS sku_code, p.name AS sku_name, w.name AS warehouse_name,
       b.batch_no, b.days_left, t.qty::float8 AS qty
FROM (
    SELECT i.warehouse_id, i.sku_id, i.batch_id, SUM(i.total_qty) AS qty, MIN(i.id) AS id
    FROM inventory i
    WHERE %s
    GROUP BY 1, 2, 3
    HAVING SUM(i.total_qty) > 0
) t
JOIN (
    SELECT b.id, b.batch_no, b.sku_id, (b.expiry_date - CURRENT_DATE)::int AS days_left
    FROM batches b
    WHERE b.expiry_date > CURRENT_DATE AND b.expiry_date <= (CURRENT_DATE + ?::interval)
) b ON b.id = t.batch_id AND b.sku_id = t.sku_id
JOIN skus s ON s.id = t.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = t.warehouse_id
ORDER BY days_left, s.code, t.warehouse_id
LIMIT ?`
)

func (r *repository) nearExpiryStock(ctx context.Context, sc Scope, expiryDays int, limit int) (int64, []nearExpiryRow, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	window := fmt.Sprintf("%d days", expiryDays)
	var count int64
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(nearExpiryCountBase, scCond),
		append(append([]any{}, scArgs...), window)...).Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 临期库存计数失败: %w", err)
	}
	rows := make([]nearExpiryRow, 0)
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(nearExpiryListBase, scCond),
		append(append(append([]any{}, scArgs...), window), limit)...).Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 临期库存明细查询失败: %w", err)
	}
	return count, rows, nil
}

// pendingCheckRow 待复核订单明细行（按出库单分组：task_count=该单 PENDING 复核任务数，
// first_created_at=组内最早创建时间）。
type pendingCheckRow struct {
	ID             int64             `gorm:"column:id"`
	OutboundNo     string            `gorm:"column:outbound_no"`
	WarehouseName  string            `gorm:"column:warehouse_name"`
	TaskCount      int64             `gorm:"column:task_count"`
	FirstCreatedAt database.JSONTime `gorm:"column:first_created_at"`
}

// 待复核订单计数（按出库单去重）+ 明细（%1=仓库范围片段；排序最早创建优先）。
const (
	pendingChecksCountBase = `
SELECT COUNT(DISTINCT ck.outbound_no)
FROM check_tasks ck
WHERE ck.status = 'PENDING' AND %s`
	pendingChecksListBase = `
SELECT MIN(ck.id) AS id, ck.outbound_no, w.name AS warehouse_name,
       COUNT(*)::bigint AS task_count, MIN(ck.created_at) AS first_created_at
FROM check_tasks ck
JOIN warehouses w ON w.id = ck.warehouse_id
WHERE ck.status = 'PENDING' AND %s
GROUP BY ck.outbound_no, w.name
ORDER BY first_created_at, ck.outbound_no
LIMIT ?`
)

func (r *repository) pendingCheckOrders(ctx context.Context, sc Scope, limit int) (int64, []pendingCheckRow, error) {
	scCond, scArgs := sc.cond("ck.warehouse_id")
	var count int64
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(pendingChecksCountBase, scCond),
		scArgs...).Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 待复核订单计数失败: %w", err)
	}
	rows := make([]pendingCheckRow, 0)
	if err := r.db.WithContext(ctx).Raw(fmt.Sprintf(pendingChecksListBase, scCond),
		append(append([]any{}, scArgs...), limit)...).Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("reports: 待复核订单明细查询失败: %w", err)
	}
	return count, rows, nil
}

// ---------- Service ----------

// WorkbenchPriorities 优先处理四组统计（count 全量 + 明细尾 limit 条）。
func (s *Service) WorkbenchPriorities(ctx context.Context, sc Scope, limit int) (*workbenchPrioritiesDTO, error) {
	now := s.now()
	// 超时收货截止线：task.timeout.receive_hours（族缺省 4 小时，行缺失走缺省——
	// taskTimeoutHours 与 workbench.go 超时阈值同读法；值非法 fail-closed 上抛）。
	receiveHours, err := s.repo.taskTimeoutHours(ctx, cfgTaskTimeoutReceiveHours)
	if err != nil {
		return nil, err
	}
	receiveCutoff := now.Add(-time.Duration(receiveHours) * time.Hour)
	// 临期窗口：inventory.alert.expiry_days 阈值最大档（降序首位），缺省 30 天——
	// DashboardSummary（service.go DashboardSummary）同源口径；读取失败上抛不静默降级。
	expiryList, _, err := s.alertThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("reports: 临期窗口阈值读取失败: %w", err)
	}
	expiryWindow := defaultExpiryWindow
	if len(expiryList) > 0 && expiryList[0] > 0 {
		expiryWindow = expiryList[0]
	}

	out := &workbenchPrioritiesDTO{
		OverdueReceipts: workbenchPriorityGroup{Items: make([]priorityItem, 0)},
		BinExceptions:   workbenchPriorityGroup{Items: make([]priorityItem, 0)},
		NearExpiryStock: workbenchPriorityGroup{Items: make([]priorityItem, 0)},
		PendingChecks:   workbenchPriorityGroup{Items: make([]priorityItem, 0)},
	}

	// 超时收货（count 全量 + 明细尾 N 条一次取回）。
	receiptCount, receiptRows, err := s.repo.overdueReceipts(ctx, sc, receiveCutoff, limit)
	if err != nil {
		return nil, err
	}
	out.OverdueReceipts.Count = receiptCount
	for _, r := range receiptRows {
		out.OverdueReceipts.Items = append(out.OverdueReceipts.Items, priorityItem{
			ID: r.ID, Title: r.InboundNo, Subtitle: r.WarehouseName,
			Detail: sourceDetail(r.SourceNo), Time: jsonTimePtr(r.CreatedAt),
		})
	}
	// 库位异常（无仓库列，scope 不生效）。
	count, rows, err := s.repo.binExceptions(ctx, limit)
	if err != nil {
		return nil, err
	}
	out.BinExceptions.Count = count
	for _, r := range rows {
		out.BinExceptions.Items = append(out.BinExceptions.Items, priorityItem{
			ID: r.ID, Title: r.ExceptionNo, Subtitle: r.Type,
			Detail: sourceDetail(r.SourceNo), Status: r.Status, Time: jsonTimePtr(r.CreatedAt),
		})
	}
	// 临期库存。
	count, expiryRows, err := s.repo.nearExpiryStock(ctx, sc, expiryWindow, limit)
	if err != nil {
		return nil, err
	}
	out.NearExpiryStock.Count = count
	for _, r := range expiryRows {
		qty := roundQty(r.Qty)
		out.NearExpiryStock.Items = append(out.NearExpiryStock.Items, priorityItem{
			ID: r.ID, Title: r.SKUCode, Subtitle: r.SKUName + " · " + r.WarehouseName,
			Detail: fmt.Sprintf("批次 %s · 剩余 %d 天", r.BatchNo, r.DaysLeft),
			Qty:    &qty,
		})
	}
	// 待复核订单。
	count, checkRows, err := s.repo.pendingCheckOrders(ctx, sc, limit)
	if err != nil {
		return nil, err
	}
	out.PendingChecks.Count = count
	for _, r := range checkRows {
		out.PendingChecks.Items = append(out.PendingChecks.Items, priorityItem{
			ID: r.ID, Title: r.OutboundNo, Subtitle: r.WarehouseName,
			Detail: fmt.Sprintf("%d 条待复核任务", r.TaskCount), Time: jsonTimePtr(r.FirstCreatedAt),
		})
	}
	return out, nil
}

// sourceDetail 来源单号明细文本（空来源不拼「来源 」尾巴）。
func sourceDetail(sourceNo string) string {
	if sourceNo == "" {
		return ""
	}
	return "来源 " + sourceNo
}

// jsonTimePtr JSONTime 值转指针（统一 DTO Time *JSONTime 形态）。
func jsonTimePtr(v database.JSONTime) *database.JSONTime {
	return &v
}

// ---------- Handler ----------

// parsePriorityLimitQuery 明细截断 limit（缺省 5，1–20）；非法写 400 响应并返回 ok=false。
func parsePriorityLimitQuery(c *gin.Context) (int, bool) {
	const (
		defLimit = 5
		minLimit = 1
		maxLimit = 20
	)
	if c.Query("limit") == "" {
		return defLimit, true
	}
	n, ok := parseInt64Query(c, "limit")
	if !ok {
		return 0, false
	}
	if n < minLimit || n > maxLimit {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "limit", "reason": fmt.Sprintf("必须为 %d-%d 的整数", minLimit, maxLimit),
		}))
		return 0, false
	}
	return int(n), true
}

// workbenchPriorities GET /api/workbench/priorities（路由接线由集成轮在 routes.go 完成：
//
//	rg.GET("/workbench/priorities", RequirePerm(auth.PermInventoryList), h.workbenchPriorities)
//
// 与 /api/workbench/summary、/api/workbench/recent-operations 同组同码）。
//
// @Summary GET /api/workbench/priorities
// @Tags 报表
// @Produce json
// @Param limit query int false "每组明细条数（缺省 5，1–20）"
// @Success 200 {object} response.Envelope "统一响应信封（四组 count+items）"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/workbench/priorities [get]
func (h *handler) workbenchPriorities(c *gin.Context) {
	limit, ok := parsePriorityLimitQuery(c)
	if !ok {
		return
	}
	dto, err := h.svc.WorkbenchPriorities(c.Request.Context(), scopeOf(c), limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}
