package sysops

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// /api/logs /api/system /api/notifications HTTP 层（backend-m3-plan §10/§12.1）。
// 全部挂 auth.RequirePermission（notifications 个人收件箱除外——认证即可用，plan §10.6）；
// 禁止直连数据库（architecture §1）；统一信封经 internal/response。

// handler sysops HTTP 层。
type handler struct {
	svc     *Service
	rdb     redisPinger
	queues  QueueStatsReader
	version string
}

// ServiceOption 装配注入项（router 唯一装配点）。
type ServiceOption func(*handler)

// WithRedisPinger 注入 Redis 连通探测（*redis.Client 经 router 桥接适配）。
func WithRedisPinger(p redisPinger) ServiceOption { return func(h *handler) { h.rdb = p } }

// WithQueueStats 注入队列积压统计（asynqx.Inspector 桥接；未注入省略 queued_tasks）。
func WithQueueStats(q QueueStatsReader) ServiceOption { return func(h *handler) { h.queues = q } }

// WithVersion 注入应用版本（构建注入；缺省 "dev"）。
func WithVersion(v string) ServiceOption { return func(h *handler) { h.version = v } }

// WithStorageRoot/WithBackupRetention 注入运行时参数（转发 Configure——裁决②备份
// 文件与 files 清理的物理根；main 不感知，router 装配传入 cfg 值）。
func WithStorageRoot(root string) ServiceOption {
	return func(*handler) {
		runtimeCfgValue.StorageRoot = root
	}
}

func WithBackupRetention(days int) ServiceOption {
	return func(*handler) {
		if days > 0 {
			runtimeCfgValue.BackupRetentionDays = days
		}
	}
}

// RegisterRoutes 平台运维域路由（三参冻结形态 + 可选 Option；plan §12.1 由 router 挂 protected 组）。
//
//	GET  /api/logs/operations              system:log:list      操作日志分页
//	GET  /api/logs/operations/:id          system:log:read      操作日志详情（三快照）
//	GET  /api/logs/logins                  system:log:list      登录日志分页
//	GET  /api/system/configs               system:config:list   配置全量
//	PUT  /api/system/configs               system:config:update 配置批量保存（逐项审计）
//	GET  /api/system/jobs                  system:job:list      任务列表
//	PUT  /api/system/jobs/:id/status       system:job:status    任务启停（热更新）
//	GET  /api/system/jobs/:id/run-logs     system:job:read      执行日志分页
//	GET  /api/system/monitor               system:monitor:list  系统监控
//	GET  /api/system/backups               system:backup:list   备份记录列表
//	POST /api/system/backups               system:backup:create 备份登记（REQUESTED）
//	GET  /api/system/backups/:id/download  system:backup:read   备份下载（审计）
//	GET  /api/system/backups/pg-dump-template system:backup:read pg_dump 命令模板（裁决②）
//	GET  /api/notifications/unread-count   （认证即可）          未读数
//	GET  /api/notifications                （认证即可）          收件箱分页
//	POST /api/notifications/:id/read       （认证即可）          标记已读
//	POST /api/notifications/read-all       （认证即可）          全部已读
//
// fail-fast：db 为 nil 即 panic（plan §3.1 规则①）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...ServiceOption) {
	if db == nil {
		panic("sysops 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	h := &handler{svc: NewService(db), version: "dev"}
	for _, opt := range opts {
		opt(h)
	}
	if rdb != nil {
		h.rdb = redisPingAdapter{rdb}
	}

	// —— 审计日志（只读，fail-closed 权限）——
	rg.GET("/logs/operations", RequirePerm(PermSystemLogList), h.listOperationLogs)
	rg.GET("/logs/operations/:id", RequirePerm(PermSystemLogRead), h.getOperationLog)
	rg.GET("/logs/logins", RequirePerm(PermSystemLogList), h.listLoginLogs)

	// —— 系统配置 ——
	rg.GET("/system/configs", RequirePerm(PermSystemConfigList), h.listConfigs)
	rg.PUT("/system/configs", RequirePerm(PermSystemConfigUpdate), h.saveConfigs)

	// —— 定时任务 ——
	rg.GET("/system/jobs", RequirePerm(PermSystemJobList), h.listJobs)
	rg.PUT("/system/jobs/:id/status", RequirePerm(PermSystemJobStatus), h.setJobStatus)
	rg.GET("/system/jobs/:id/run-logs", RequirePerm(PermSystemJobRead), h.listJobRunLogs)

	// —— 系统监控 ——
	rg.GET("/system/monitor", RequirePerm(PermSystemMonitorList), h.monitor)

	// —— 备份（裁决②混合模式）——
	rg.GET("/system/backups/pg-dump-template", RequirePerm(PermSystemBackupRead), h.pgDumpTemplate)
	rg.GET("/system/backups", RequirePerm(PermSystemBackupList), h.listBackups)
	rg.POST("/system/backups", RequirePerm(PermSystemBackupCreate), h.registerBackup)
	rg.GET("/system/backups/:id/download", RequirePerm(PermSystemBackupRead), h.downloadBackup)

	// —— 通知个人收件箱（认证即可用，无权限点——个人数据自见原则，plan §10.6）——
	rg.GET("/notifications/unread-count", h.unreadCount)
	rg.GET("/notifications", h.listNotifications)
	rg.POST("/notifications/read-all", h.markAllNotificationsRead)
	rg.POST("/notifications/:id/read", h.markNotificationRead)
}

// RequirePerm auth.RequirePermission 的域内别名。
func RequirePerm(perm string) gin.HandlerFunc { return auth.RequirePermission(perm) }

// redisPingAdapter *redis.Client → redisPinger 适配（Ping 返回 StatusCmd，接口要求 error）。
type redisPingAdapter struct{ client *redis.Client }

func (a redisPingAdapter) Ping(ctx context.Context) error {
	_, err := a.client.Ping(ctx).Result()
	return err
}

// parseID 路径参数 ID 解析（非法 400）。
func parseID(c *gin.Context, key string) (int64, bool) {
	v, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || v <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": key, "reason": "必须为正整数"}))
		return 0, false
	}
	return v, true
}

// parseBoolQuery 布尔三态筛选（缺省 nil；非法值 400）。field 为 query 键名（错误提示回显）。
func parseBoolQuery(c *gin.Context, field string) (*bool, bool) {
	raw := c.Query(field)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": field, "reason": "必须为 true/false"}))
		return nil, false
	}
	return &v, true
}

// parseSuccessQuery success 三态筛选（缺省 nil；非法值 400）。
func parseSuccessQuery(c *gin.Context) (*bool, bool) {
	return parseBoolQuery(c, "success")
}

// parseTimeQuery 可选时间参数（YYYY-MM-DD [HH:mm:ss]）。
func parseTimeQuery(c *gin.Context, key string) (*time.Time, bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	t, err := parseDayParam(raw)
	if err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": key, "reason": err.Error()}))
		return nil, false
	}
	return &t, true
}

// parseDayParam 时间参数解析（api.md §2 格式）。
func parseDayParam(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("时间格式非法（应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss）")
}

// ---- 审计日志 ----

// @Summary GET /api/logs/operations
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/logs/operations [get]
func (h *handler) listOperationLogs(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	success, ok := parseSuccessQuery(c)
	if !ok {
		return
	}
	from, ok := parseTimeQuery(c, "time_from")
	if !ok {
		return
	}
	to, ok := parseTimeQuery(c, "time_to")
	if !ok {
		return
	}
	rows, total, err := h.svc.ListOperationLogs(c.Request.Context(), logFilter{
		Keyword: c.Query("keyword"), Module: c.Query("module"), Action: c.Query("action"),
		Success: success, From: from, To: to, Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary GET /api/logs/operations/:id
// @Tags 系统运维
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/logs/operations/{id} [get]
func (h *handler) getOperationLog(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	row, err := h.svc.GetOperationLog(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, row)
}

// @Summary GET /api/logs/logins
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/logs/logins [get]
func (h *handler) listLoginLogs(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	success, ok := parseSuccessQuery(c)
	if !ok {
		return
	}
	from, ok := parseTimeQuery(c, "time_from")
	if !ok {
		return
	}
	to, ok := parseTimeQuery(c, "time_to")
	if !ok {
		return
	}
	rows, total, err := h.svc.ListLoginLogs(c.Request.Context(), logFilter{
		Keyword: c.Query("keyword"), Success: success, From: from, To: to, Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// ---- 系统配置 ----

// @Summary GET /api/system/configs
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/configs [get]
func (h *handler) listConfigs(c *gin.Context) {
	rows, err := h.svc.ListConfigs(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

type saveConfigsPayload struct {
	Items []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"items"`
}

// @Summary PUT /api/system/configs
// @Tags 系统运维
// @Accept json
// @Produce json
// @Param body body saveConfigsPayload true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/configs [put]
func (h *handler) saveConfigs(c *gin.Context) {
	var payload saveConfigsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"reason": "请求体非法"}))
		return
	}
	items := make([]saveConfigItem, 0, len(payload.Items))
	for _, it := range payload.Items {
		if strings.TrimSpace(it.Key) == "" {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "items.key", "reason": "配置键不能为空"}))
			return
		}
		items = append(items, saveConfigItem{Key: it.Key, Value: it.Value})
	}
	saved, err := h.svc.SaveConfigs(c.Request.Context(), actorOf(c), items)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"saved": saved})
}

// ---- 定时任务 ----

// @Summary GET /api/system/jobs
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/jobs [get]
func (h *handler) listJobs(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	// enabled 三态筛选（前端 SystemJobQuery 发 enabled=true/false）——原经 parseSuccessQuery
	// 读 "success" 键致筛选被静默忽略，修复为按本端点契约键名取参。
	enabled, ok := parseBoolQuery(c, "enabled")
	if !ok {
		return
	}
	rows, total, err := h.svc.ListJobs(c.Request.Context(), jobFilter{
		Keyword: c.Query("keyword"), Enabled: enabled, Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

type jobStatusPayload struct {
	Enabled *bool `json:"enabled"`
}

// @Summary PUT /api/system/jobs/:id/status
// @Tags 系统运维
// @Accept json
// @Produce json
// @Param body body jobStatusPayload true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/jobs/{id}/status [put]
func (h *handler) setJobStatus(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var payload jobStatusPayload
	if err := c.ShouldBindJSON(&payload); err != nil || payload.Enabled == nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"reason": "enabled 必须为布尔值"}))
		return
	}
	job, err := h.svc.SetJobStatus(c.Request.Context(), id, *payload.Enabled)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, job)
}

// @Summary GET /api/system/jobs/:id/run-logs
// @Tags 系统运维
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/jobs/{id}/run-logs [get]
func (h *handler) listJobRunLogs(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	rows, total, err := h.svc.ListJobRunLogs(c.Request.Context(), id, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// ---- 系统监控 ----

// @Summary GET /api/system/monitor
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/monitor [get]
func (h *handler) monitor(c *gin.Context) {
	resp, err := h.svc.MonitorSnapshot(c.Request.Context(), h.rdb, h.queues, h.version)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, resp)
}

// ---- 备份（裁决②混合模式）----

// @Summary GET /api/system/backups
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/backups [get]
func (h *handler) listBackups(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	rows, total, err := h.svc.ListBackups(c.Request.Context(), c.Query("status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary POST /api/system/backups
// @Tags 系统运维
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/backups [post]
func (h *handler) registerBackup(c *gin.Context) {
	actor := actorOf(c)
	row, err := h.svc.RegisterBackup(c.Request.Context(), actor)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"backup":  row,
		"message": "已登记备份任务（REQUESTED），等待部署侧执行器拾取执行（Orchestrator 裁决②混合模式，deployment.md §4）",
	})
}

// @Summary GET /api/system/backups/:id/download
// @Tags 系统运维
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/backups/{id}/download [get]
func (h *handler) downloadBackup(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	path, fileName, err := h.svc.PrepareBackupDownload(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		response.Err(c, ErrBackupFileMissing)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		response.Err(c, ErrBackupFileMissing)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(fileName)+`"`)
	http.ServeContent(c.Writer, c.Request, sanitizeFilename(fileName), st.ModTime(), f)
}

// @Summary GET /api/system/backups/pg-dump-template
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/system/backups/pg-dump-template [get]
func (h *handler) pgDumpTemplate(c *gin.Context) {
	resp, err := h.svc.PGDumpTemplate(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, resp)
}

// ---- 通知个人收件箱 ----

// @Summary GET /api/notifications/unread-count
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/notifications/unread-count [get]
func (h *handler) unreadCount(c *gin.Context) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	n, err := h.svc.UnreadCount(c.Request.Context(), uc.UserID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, n)
}

// @Summary GET /api/notifications
// @Tags 系统运维
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/notifications [get]
func (h *handler) listNotifications(c *gin.Context) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	var readPtr *bool
	if raw := c.Query("read"); raw != "" {
		v, perr := strconv.ParseBool(raw)
		if perr != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "read", "reason": "必须为 true/false"}))
			return
		}
		readPtr = &v
	}
	rows, total, err := h.svc.ListInbox(c.Request.Context(), inboxFilter{
		UserID: uc.UserID, Read: readPtr, Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, rows, page, pageSize, total)
}

// @Summary POST /api/notifications/:id/read
// @Tags 系统运维
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/notifications/{id}/read [post]
func (h *handler) markNotificationRead(c *gin.Context) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	n, err := h.svc.MarkRead(c.Request.Context(), uc.UserID, id)
	if err != nil {
		response.Err(c, err)
		return
	}
	if n == 0 {
		response.Err(c, ErrNotificationNotFound)
		return
	}
	response.OK(c, gin.H{"marked": n})
}

// @Summary POST /api/notifications/read-all
// @Tags 系统运维
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/notifications/read-all [post]
func (h *handler) markAllNotificationsRead(c *gin.Context) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	n, err := h.svc.MarkAllRead(c.Request.Context(), uc.UserID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"marked": n})
}
