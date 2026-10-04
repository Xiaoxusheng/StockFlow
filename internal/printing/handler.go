package printing

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一
// 响应封装；禁止直连数据库，plan §2.3 判据 6：禁止绕过 internal/response 直接 c.JSON）。
//
// 权限点：本包 permissions.go 常量（plan §11.1 冻结清单逐字同源；集成工程师收编
// internal/auth 后替换为引用，字符串不变）。全部业务路由挂 auth.RequirePermission
// （permission.md §2）。
//
// 数据权限（permission.md §4）：print_templates/print_tasks 无仓库维度列（000012
// 冻结 DDL），模板为全局资源；打印内容的仓库范围由使用侧承接——data_ids 取自各域
// 列表页（已按 auth.WarehouseScope 过滤），装配 reader 逐对象校验存在性。打印历史
// 与任务列表不按仓库过滤，列入 DomainResult 已知边界。
//
// 权限挂载说明（plan §11.1 值域内的口径选择，集成收编评审确认）：
//   - GET /api/prints/barcode → printing:task:read（预览渲染链路的最低公共权限；
//     供 Scan 端/调试/外部系统引用——plan §7.2）；
//   - GET /api/prints/history → printing:task:list（print_tasks 已确认子集）；
//   - POST /api/prints/templates/{id}/copy → printing:template:create（plan §11.1
//     "copy 复用 create"）。

// handler 打印域 HTTP handler。
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

// renderRegisterOnce asynqx handler 全局注册防线（注册表为进程级——重复注册 panic，
// asynqx.RegisterHandler 同款；生产路径 RegisterRoutes 仅被 router 调用一次）。
var renderRegisterOnce sync.Once

// RegisterRoutes 打印域路由（三参 + Option 冻结形态，returns/purchase 同款）。
//
// 装配要求（plan §3.1 规则① fail-closed）：
//
//	WithContentReader(objectType, 各域 printing_content.go 实现)   7 类业务对象装配（缺一启动失败）
//	WithQueue(asynqx.Runtime.Queue)                                render 入队（缺位启动失败）
//	WithLogger(logger 基座)                                        域内日志（缺位降级 nop）
//
// 路由与权限点（plan §7.1/§7.2/§11.1 冻结清单）：
//
//	/api/prints/templates        GET 列表 / POST 新建 / GET/:id 详情 / PUT/:id 修改
//	                             / POST/:id copy / PUT/:id/status 启停
//	/api/prints/tasks            GET 列表 / POST 创建 / GET/:id 详情（渲染数据包）
//	                             / POST/:id/execute 执行确认
//	/api/prints/history          GET 打印历史（print_tasks 已确认子集）
//	/api/prints/barcode          GET 条码/二维码 PNG（printing.md §4.1 全量码制）
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	if db == nil {
		panic("printing 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	_ = rdb // 预留参数位（与 returns/inventory RegisterRoutes 同形；打印域无 Redis 直用）
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	// fail-closed：7 类业务对象 reader 全量校验（CARTON_CODE/PALLET_CODE 为内置承接，
	// plan §7.2；缺任一外部 reader = 域包接入文件缺位，启动失败——plan §3.1 规则①）。
	var missing []string
	for _, ot := range objectTypes {
		if IsBuiltinObjectType(ot) {
			continue
		}
		if o.readers == nil || o.readers[ot] == nil {
			missing = append(missing, ot)
		}
	}
	if len(missing) > 0 {
		panic("printing 装配失败: 装配 reader 未注入（router 必须按 plan §12.2 注入各域 printing_content.go 实现，WithContentReader）——缺失对象类型: " + strings.Join(missing, ","))
	}
	if o.queue == nil {
		panic("printing 装配失败: 异步队列未注入（router 必须传 WithQueue(asynqx.Runtime.Queue)，plan §3.1 规则①）")
	}

	repo := NewGormRepository(db)
	svc := NewService(repo, opts...)
	h := &handler{svc: svc}

	// render handler + 失败终态回调注册（进程级一次；Server.Start 前完成——RegisterRoutes
	// 在 router 装配期调用，先于 cmd/server main 启动 asynq Server，plan §4.2）。
	renderRegisterOnce.Do(func() {
		asynqx.RegisterHandler(asynqx.TaskTypePrintRender, svc.HandleRenderTask)
		asynqx.RegisterFailureFinalizer(asynqx.TaskTypePrintRender, svc.FinalizeRenderFailure)
	})

	p := rg.Group("/prints")

	// —— 打印模板（plan §7.1）——
	p.GET("/templates", auth.RequirePermission(PermTemplateList), h.listTemplates)
	p.GET("/templates/:id", auth.RequirePermission(PermTemplateRead), h.getTemplate)
	p.POST("/templates", auth.RequirePermission(PermTemplateCreate), h.createTemplate)
	p.PUT("/templates/:id", auth.RequirePermission(PermTemplateUpdate), h.updateTemplate)
	p.POST("/templates/:id/copy", auth.RequirePermission(PermTemplateCreate), h.copyTemplate)
	p.PUT("/templates/:id/status", auth.RequirePermission(PermTemplateStatus), h.setTemplateStatus)

	// —— 打印任务（plan §7.2）——
	p.GET("/tasks", auth.RequirePermission(PermTaskList), h.listTasks)
	p.GET("/tasks/:id", auth.RequirePermission(PermTaskRead), h.getTask)
	p.POST("/tasks", auth.RequirePermission(PermTaskCreate), h.createTask)
	p.POST("/tasks/:id/execute", auth.RequirePermission(PermTaskExecute), h.executeTask)

	// —— 打印历史（printing.md §1.2 五字段；task 已确认子集）——
	p.GET("/history", auth.RequirePermission(PermTaskList), h.listHistory)

	// —— 条码/二维码 PNG（printing.md §4.1；plan §7.2）——
	p.GET("/barcode", auth.RequirePermission(PermTaskRead), h.barcode)
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// ---- 模板 ----

// @Summary GET /api/prints/templates
// @Tags 打印中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates [get]
func (h *handler) listTemplates(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.svc.ListTemplates(c.Request.Context(), TemplateFilter{
		Keyword:    c.Query("keyword"),
		ObjectType: c.Query("object_type"),
		Status:     c.Query("status"),
		Page:       page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/prints/templates/:id
// @Tags 打印中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates/{id} [get]
func (h *handler) getTemplate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetTemplate(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/prints/templates
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param body body TemplateSaveInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates [post]
func (h *handler) createTemplate(c *gin.Context) {
	var in TemplateSaveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.CreateTemplate(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary PUT /api/prints/templates/:id
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param body body TemplateSaveInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates/{id} [put]
func (h *handler) updateTemplate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in TemplateSaveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.UpdateTemplate(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/prints/templates/:id/copy
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates/{id}/copy [post]
func (h *handler) copyTemplate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.CopyTemplate(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary PUT /api/prints/templates/:id/status
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param body body object true "请求体（匿名结构体，字段见 handler 定义）"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/templates/{id}/status [put]
func (h *handler) setTemplateStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.SetTemplateStatus(c.Request.Context(), actorOf(c), id, in.Status)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// ---- 任务 ----

// @Summary GET /api/prints/tasks
// @Tags 打印中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/tasks [get]
func (h *handler) listTasks(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	f := TaskFilter{
		Keyword:    c.Query("keyword"),
		ObjectType: c.Query("object_type"),
		Status:     c.Query("status"),
		Page:       page, PageSize: pageSize,
	}
	if v := c.Query("id"); v != "" {
		// id 精确过滤（前端预览页回对取单条——plan §7.2；非正整数字符串按参数错误）。
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.Err(c, paramError("id", "必须为正整数字符串"))
			return
		}
		f.ID = id
	}
	items, total, err := h.svc.ListTasks(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// @Summary GET /api/prints/tasks/:id
// @Tags 打印中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/tasks/{id} [get]
func (h *handler) getTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetTask(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/prints/tasks
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param body body TaskCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/tasks [post]
func (h *handler) createTask(c *gin.Context) {
	var in TaskCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.CreateTask(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// @Summary POST /api/prints/tasks/:id/execute
// @Tags 打印中心
// @Accept json
// @Produce json
// @Param body body ExecuteInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/tasks/{id}/execute [post]
func (h *handler) executeTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ExecuteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := h.svc.ExecuteTask(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// ---- 历史 ----

// @Summary GET /api/prints/history
// @Tags 打印中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/history [get]
func (h *handler) listHistory(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.svc.ListHistory(c.Request.Context(), TaskFilter{
		Keyword:    c.Query("keyword"),
		ObjectType: c.Query("object_type"),
		Status:     "", // 历史不看 render 态
		Result:     resultFilter(c),
		Page:       page, PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// resultFilter 历史 result 筛选（SUCCESS/FAILED；空=全部；非法值忽略——筛选键宽容，
// 域内值由 Service 校验）。
func resultFilter(c *gin.Context) string {
	return c.Query("result")
}

// ---- 条码 ----

// barcode GET /api/prints/barcode?text=&symbology=&width=&height=
// 输出 image/png（printing.md §4.1 全量码制：CODE128 默认推荐/CODE39/EAN13/EAN8/
// UPC/QR/DATAMATRIX；plan §7.2 参数上限防滥用）。
// @Summary GET /api/prints/barcode?text=&symbology=&width=&height=
// @Tags 打印中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/prints/barcode [get]
func (h *handler) barcode(c *gin.Context) {
	text := c.Query("text")
	if text == "" {
		response.Err(c, response.NewError(ErrBarcodeTextInvalid, map[string]any{"reason": "text 不能为空"}))
		return
	}
	symbology := c.Query("symbology")
	if symbology == "" {
		symbology = SymbologyCode128 // Code128 默认推荐（printing.md §4.1）
	}
	width, err := parseDimension(c, "width")
	if err != nil {
		response.Err(c, err)
		return
	}
	height, err := parseDimension(c, "height")
	if err != nil {
		response.Err(c, err)
		return
	}
	img, err := h.svc.RenderBarcodePNG(text, symbology, width, height)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Data(http.StatusOK, "image/png", img)
}

// parseDimension 解析可选整型尺寸参数（非数字 → 4xx 参数错误）。
func parseDimension(c *gin.Context, name string) (int, error) {
	raw := c.Query(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, paramError(name, "必须为整数")
	}
	return n, nil
}
