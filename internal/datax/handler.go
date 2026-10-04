package datax

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §2.3 判据 6：禁止绕过 internal/response 直接 c.JSON）。
//
// 权限点：本包 permissions.go 常量（plan §11.1 冻结清单逐字同源；集成工程师收编
// internal/auth M3 段后可替换引用，字符串不变）。全部业务路由挂 auth.RequirePermission。
//
// 路由与权限（plan §6.2/§6.3/§6.4）：
//
//	/api/imports                        GET 列表 / POST 上传解析
//	/api/imports/templates              GET 模板清单 / GET /:type 模板下载
//	/api/imports/:id/validate           POST 校验（create=上传+校验，plan §11.1）
//	/api/imports/:id/preview            GET 预览
//	/api/imports/:id/confirm            POST 确认导入（execute=高危二次确认与审计）
//	/api/imports/:id/error-file         GET 错误 Excel 下载
//	/api/exports                        GET 列表 / POST 创建（敏感操作，审计）
//	/api/exports/:id/file               GET 产物下载
//	/api/files                          GET 列表 / POST 上传 / DELETE /:id 删除
//	/api/files/:id/download|preview     GET 下载/预览（下载记 operation_logs）
//
// 上传路由组（/api/imports POST、/api/files POST）的请求体上限由 router 挂
// storage.upload_max_bytes 局部中间件覆盖全局 1MB（plan §12.1）。

// handler 数据中心 HTTP handler。
type handler struct {
	svc *Service
}

// actorOf 从 gin 上下文提取操作者归因（审计用；auth.CurrentUser 冻结契约 plan §5.1）。
func actorOf(c *gin.Context) Actor {
	uc, _ := auth.CurrentUser(c)
	all, ids := auth.WarehouseScope(c)
	return Actor{
		UserID:        uc.UserID,
		Username:      uc.Username,
		RequestID:     c.GetString(response.RequestIDKey),
		IP:            c.ClientIP(),
		UserAgent:     c.Request.UserAgent(),
		Method:        c.Request.Method,
		Path:          c.FullPath(),
		AllWarehouses: all,
		WarehouseIDs:  ids,
	}
}

// fileScopeOf 文件中心数据权限（plan §6.4：沿用仓库范围 Service 层过滤，permission.md §4；
// 与 actorOf 的仓库快照同源 auth.WarehouseScope）。
func fileScopeOf(c *gin.Context) *FileScope {
	uc, _ := auth.CurrentUser(c)
	all, ids := auth.WarehouseScope(c)
	return &FileScope{All: all, WarehouseIDs: ids, UserID: uc.UserID}
}

// pathID 解析路径 :id（ID 字符串化约定，backend-m1-plan §1）。
func pathID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "id", "reason": "必须为正整数字符串",
		}))
		return 0, false
	}
	return id, true
}

// RegisterRoutes 数据中心路由（protected 组挂载；db/存储/队列装配校验启动期 fail-fast，
// plan §3.1 规则①）。
//
// 装配要求（集成工程师 MT1/MT6，plan §2.2/§12.1）：
//
//	store：storage.NewStore(cfg.Storage.Root)（/ready 文件系统检查同一实例）
//	queue：asynqx Runtime.Queue（redis.enabled=false 时 inline 降级）
//	writers：WithImportWriter ×9（masterdata/warehouse/purchase/sales/inventory 各域
//	         datax_import.go 构造）
//	sources：WithExportSource ×15 + REPORT（MT4 交付后补挂；未挂模块创建任务 409 拒绝）
//	queue handlers：RegisterQueueHandlers(svc) 必须先于 asynq Server.Start / inline 入队
func RegisterRoutes(rg *gin.RouterGroup, svc *Service) {
	if svc == nil {
		panic("datax 装配失败: Service 为 nil")
	}
	if svc.store == nil {
		panic("datax 装配失败: storage.Store 未注入（router 必须传 storage.NewStore(...) 实例）")
	}
	if svc.queue == nil {
		panic("datax 装配失败: asynqx.Queue 未注入（router 必须传 asynqx Runtime.Queue）")
	}
	if svc.repo == nil || svc.repo.DB() == nil {
		panic("datax 装配失败: Repository/db 为 nil")
	}
	h := &handler{svc: svc}

	// —— 导入中心 /api/imports ——
	imp := rg.Group("/imports")
	imp.GET("", auth.RequirePermission(PermImportList), h.listImports)
	imp.GET("/templates", auth.RequirePermission(PermImportList), h.listTemplates)
	imp.GET("/templates/:type", auth.RequirePermission(PermImportRead), h.downloadTemplate)
	imp.POST("", auth.RequirePermission(PermImportCreate), h.uploadImport)
	imp.POST("/:id/validate", auth.RequirePermission(PermImportCreate), h.validateImport)
	imp.GET("/:id/preview", auth.RequirePermission(PermImportRead), h.previewImport)
	imp.POST("/:id/confirm", auth.RequirePermission(PermImportExecute), h.confirmImport)
	imp.GET("/:id/error-file", auth.RequirePermission(PermImportRead), h.downloadErrorFile)

	// —— 导出中心 /api/exports ——
	exp := rg.Group("/exports")
	exp.GET("", auth.RequirePermission(PermExportList), h.listExports)
	exp.POST("", auth.RequirePermission(PermExportCreate), h.createExport)
	exp.GET("/:id/file", auth.RequirePermission(PermExportRead), h.downloadExportFile)

	// —— 文件中心 /api/files ——
	files := rg.Group("/files")
	files.GET("", auth.RequirePermission(PermFileList), h.listFiles)
	files.POST("", auth.RequirePermission(PermFileCreate), h.uploadFile)
	files.GET("/:id/download", auth.RequirePermission(PermFileRead), h.downloadFile)
	files.GET("/:id/preview", auth.RequirePermission(PermFileRead), h.previewFile)
	files.DELETE("/:id", auth.RequirePermission(PermFileDelete), h.deleteFile)

	// —— 任务详情（数据中心任务卡；导入/导出各自权限点，防跨资源权限借用）——
	rg.GET("/data-tasks/import/:id", auth.RequirePermission(PermImportList), h.getImportTask)
	rg.GET("/data-tasks/export/:id", auth.RequirePermission(PermExportList), h.getExportTask)
}

// ---- 导入中心 ----

// listTemplates GET /api/imports/templates。
// @Summary GET /api/imports/templates
// @Tags 数据中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/templates [get]
func (h *handler) listTemplates(c *gin.Context) {
	response.OK(c, h.svc.ListTemplates())
}

// downloadTemplate GET /api/imports/templates/:type（xlsx 流式下发）。
// @Summary GET /api/imports/templates/:type（xlsx 流式下发）
// @Tags 数据中心
// @Produce json
// @Param type path int true "路径参数 type"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/templates/{type} [get]
func (h *handler) downloadTemplate(c *gin.Context) {
	name, content, err := h.svc.TemplateFile(c.Param("type"))
	if err != nil {
		response.Err(c, err)
		return
	}
	c.DataFromReader(http.StatusOK, int64(len(content)),
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		newBytesReader(content), map[string]string{
			"Content-Disposition": `attachment; filename="` + urlEscapeName(name) + `"`,
		})
}

// uploadImport POST /api/imports（multipart：import_type + file）。
// @Summary POST /api/imports（multipart：import_type + file）
// @Tags 数据中心
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports [post]
func (h *handler) uploadImport(c *gin.Context) {
	importType := c.PostForm("import_type")
	if importType == "" {
		// 前端先行字段名 importType 兼容（data.ts upload form.append('importType', ...)）。
		importType = c.PostForm("importType")
	}
	fh, err := c.FormFile("file")
	if err != nil {
		response.Err(c, response.NewError(ErrImportFileRequired, nil))
		return
	}
	res, err := h.svc.Upload(c.Request.Context(), actorOf(c), importType, fh)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// validateImport POST /api/imports/:id/validate。
// @Summary POST /api/imports/:id/validate
// @Tags 数据中心
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/{id}/validate [post]
func (h *handler) validateImport(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	res, err := h.svc.Validate(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// previewImport GET /api/imports/:id/preview。
// @Summary GET /api/imports/:id/preview
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/{id}/preview [get]
func (h *handler) previewImport(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	res, err := h.svc.Preview(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// confirmImport POST /api/imports/:id/confirm（高危类型请求体 confirmed=true 二次确认）。
// @Summary POST /api/imports/:id/confirm（高危类型请求体 confirmed=true 二次确认）
// @Tags 数据中心
// @Accept json
// @Produce json
// @Param body body ConfirmInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/{id}/confirm [post]
func (h *handler) confirmImport(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var in ConfirmInput
	// 请求体可选（前端先行契约 confirm 不携带 body；高危类型必须显式 confirmed=true）。
	if c.Request.Body != nil && c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&in); err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
				"reason": "请求体须为 {\"confirmed\": bool}",
			}))
			return
		}
	}
	res, err := h.svc.Confirm(c.Request.Context(), actorOf(c), id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// downloadErrorFile GET /api/imports/:id/error-file（错误 Excel 流式下发 + 下载审计）。
// @Summary GET /api/imports/:id/error-file（错误 Excel 流式下发 + 下载审计）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports/{id}/error-file [get]
func (h *handler) downloadErrorFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	sf, err := h.svc.GetImportErrorFile(c.Request.Context(), id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	defer func() { _ = sf.Body.Close() }()
	h.svc.AuditFileDownload(c.Request.Context(), actorOf(c), sf.ID, sf.FileName)
	http.ServeContent(c.Writer, c.Request, sf.FileName, sf.ModTime, sf.Body)
}

// listImports GET /api/imports。
// @Summary GET /api/imports
// @Tags 数据中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/imports [get]
func (h *handler) listImports(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.svc.ListImports(c.Request.Context(), TaskListFilter{
		Module: c.Query("module"), Status: c.Query("status"),
		Page: page, PageSize: pageSize,
	}, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- 导出中心 ----

// listExports GET /api/exports。
// @Summary GET /api/exports
// @Tags 数据中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exports [get]
func (h *handler) listExports(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.svc.ListExports(c.Request.Context(), TaskListFilter{
		Module: c.Query("module"), Status: c.Query("status"),
		Page: page, PageSize: pageSize,
	}, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createExport POST /api/exports（敏感操作：创建即审计，Service 同事务完成）。
// @Summary POST /api/exports（敏感操作：创建即审计，Service 同事务完成）
// @Tags 数据中心
// @Accept json
// @Produce json
// @Param body body ExportCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exports [post]
func (h *handler) createExport(c *gin.Context) {
	var in ExportCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"reason": "请求体须为导出创建参数",
		}))
		return
	}
	all, ids := auth.WarehouseScope(c)
	res, err := h.svc.CreateExport(c.Request.Context(), actorOf(c), in, all, ids)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// downloadExportFile GET /api/exports/:id/file（终态后可下载；流式 + 审计）。
// @Summary GET /api/exports/:id/file（终态后可下载；流式 + 审计）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/exports/{id}/file [get]
func (h *handler) downloadExportFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	sf, err := h.svc.GetExportFile(c.Request.Context(), id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	defer func() { _ = sf.Body.Close() }()
	h.svc.AuditFileDownload(c.Request.Context(), actorOf(c), sf.ID, sf.FileName)
	http.ServeContent(c.Writer, c.Request, sf.FileName, sf.ModTime, sf.Body)
}

// ---- 文件中心 ----

// listFiles GET /api/files。
// @Summary GET /api/files
// @Tags 数据中心
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/files [get]
func (h *handler) listFiles(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.svc.ListFiles(c.Request.Context(), FileListFilter{
		Module: c.Query("module"), BusinessNo: c.Query("business_no"), Keyword: c.Query("keyword"),
		Page: page, PageSize: pageSize, Scope: fileScopeOf(c),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// uploadFile POST /api/files（multipart：module 必填、business_no 选填、file）。
// @Summary POST /api/files（multipart：module 必填、business_no 选填、file）
// @Tags 数据中心
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/files [post]
func (h *handler) uploadFile(c *gin.Context) {
	in := FileUploadInput{
		Module:     c.PostForm("module"),
		BusinessNo: c.PostForm("business_no"), // 前端先行字段名同名（file.ts FileUploadPayload）
	}
	fh, err := c.FormFile("file")
	if err == nil {
		in.File = fh
	}
	res, err := h.svc.UploadFile(c.Request.Context(), actorOf(c), in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// downloadFile GET /api/files/:id/download（权限 + 过期/软删拒绝 + 流式 + 下载审计）。
// @Summary GET /api/files/:id/download（权限 + 过期/软删拒绝 + 流式 + 下载审计）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/files/{id}/download [get]
func (h *handler) downloadFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	sf, err := h.svc.GetFileDownload(c.Request.Context(), id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	defer func() { _ = sf.Body.Close() }()
	h.svc.AuditFileDownload(c.Request.Context(), actorOf(c), sf.ID, sf.FileName)
	http.ServeContent(c.Writer, c.Request, sf.FileName, sf.ModTime, sf.Body)
}

// previewFile GET /api/files/:id/preview（仅图片原样返回；plan §6.4）。
// @Summary GET /api/files/:id/preview（仅图片原样返回；plan §6.4）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/files/{id}/preview [get]
func (h *handler) previewFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	sf, err := h.svc.GetFilePreview(c.Request.Context(), id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	defer func() { _ = sf.Body.Close() }()
	http.ServeContent(c.Writer, c.Request, sf.FileName, sf.ModTime, sf.Body)
}

// deleteFile DELETE /api/files/:id（软删 + 审计）。
// @Summary DELETE /api/files/:id（软删 + 审计）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/files/{id} [delete]
func (h *handler) deleteFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFile(c.Request.Context(), actorOf(c), id, fileScopeOf(c)); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// getImportTask GET /api/data-tasks/import/:id（数据中心任务卡详情刷新）。
// @Summary GET /api/data-tasks/import/:id（数据中心任务卡详情刷新）
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/data-tasks/import/{id} [get]
func (h *handler) getImportTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	res, err := h.svc.GetTask(c.Request.Context(), "IMPORT", id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// getExportTask GET /api/data-tasks/export/:id。
// @Summary GET /api/data-tasks/export/:id
// @Tags 数据中心
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/data-tasks/export/{id} [get]
func (h *handler) getExportTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	res, err := h.svc.GetTask(c.Request.Context(), "EXPORT", id, fileScopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// ---- 响应工具 ----

// newBytesReader 模板下载的内存响应体（DataFromReader 用；stdlib bytes.Reader）。
func newBytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// newBytesReadSeeker 下载响应体：serveFile.Body 为 os.File（满足 io.ReadSeeker +
// Closer），http.ServeContent 流式响应、支持 Range，不整读内存。

// urlEscapeName Content-Disposition 文件名防御（中文文件名由前端 axios Blob 自行命名，
// 这里剥离引号/换行/控制字符防 header 注入）。
func urlEscapeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '"' || r == '\r' || r == '\n' || r < 0x20 {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
