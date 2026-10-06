package userpref

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一信封）。
//
// 路由全部挂 protected 组（auth.AuthRequired 之后），认证即可用、无权限点
//（notifications 个人收件箱同口径，计划 §2.2/§2.3）；不挂 Audit 中间件
//（recent_visits 高频写会刷屏审计日志——计划 §2.3，api.md 披露）。
// 未认证场景兜底 401（直调/单测路径防御，sysops notifications 同款）。

// handler userpref HTTP 层。
type handler struct{ svc *Service }

// Option 装配注入项（router 唯一装配点；单测经 WithService 注入 fake 仓储服务）。
type Option func(*handler)

// WithService 直接注入 Service（仅测试使用；生产装配传 db 由包内自建）。
func WithService(s *Service) Option { return func(h *handler) { h.svc = s } }

// RegisterRoutes 用户态个性化路由（计划 §4.1 B1 段冻结端点集；由集成波次挂
// protected 组——router.go 装配先例 sysops.RegisterRoutes(protected, db, ...)）。
//
//	GET    /api/user/views?page_key=   （认证即可）  同页签视图列表
//	POST   /api/user/views             （认证即可）  新建视图（重名 409）
//	PUT    /api/user/views/:id         （认证即可）  改名/改内容/is_default（非本人 404）
//	DELETE /api/user/views/:id         （认证即可）  删除视图（非本人 404）
//	GET    /api/user/preferences       （认证即可）  偏好读取（?keys= 逗号分隔可选，缺省全量）
//	PUT    /api/user/preferences/:key  （认证即可）  偏好写入（白名单外 400，值 ≤16KB）
//
// fail-fast：未注入 Service 且 db 为 nil 即 panic（plan §3.1 规则①，sysops 同款）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, opts ...Option) {
	h := &handler{}
	for _, opt := range opts {
		opt(h)
	}
	if h.svc == nil {
		if db == nil {
			panic("userpref 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
		}
		h.svc = NewService(NewViewRepo(db), NewPrefRepo(db))
	}

	ug := rg.Group("/user")
	ug.GET("/views", h.listViews)
	ug.POST("/views", h.createView)
	ug.PUT("/views/:id", h.updateView)
	ug.DELETE("/views/:id", h.deleteView)
	ug.GET("/preferences", h.listPreferences)
	ug.PUT("/preferences/:key", h.putPreference)
}

// currentUser 当前用户上下文（缺失为未认证——AuthRequired 未挂载的直调路径）。
func currentUser(c *gin.Context) (int64, bool) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return 0, false
	}
	return uc.UserID, true
}

// pathID 解析路径 :id（ID 字符串化约定，backend-m1-plan §1）。
func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "id", "reason": "必须为正整数字符串",
		}))
		return 0, false
	}
	return id, true
}

// bindJSON 请求体绑定（统一经 BindErrorDetails 出 details）。
func bindJSON(c *gin.Context, in any) bool {
	if err := c.ShouldBindJSON(in); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return false
	}
	return true
}

// viewBody 视图创建/更新请求体（指针字段缺省 = PUT 不修改；json 列为原样 JSON）。
type viewBody struct {
	PageKey     string          `json:"page_key"`
	Name        *string         `json:"name"`
	FiltersJSON json.RawMessage `json:"filters_json"`
	SortJSON    json.RawMessage `json:"sort_json"`
	ColumnsJSON json.RawMessage `json:"columns_json"`
	PageSize    *int            `json:"page_size"`
	IsDefault   *bool           `json:"is_default"`
}

// listViews GET /api/user/views?page_key=（page_key 必填）。
//
//	@Summary GET /api/user/views
//	@Tags 用户态
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 400 {object} response.Envelope "请求参数错误"
//	@Router /api/user/views [get]
func (h *handler) listViews(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	pageKey := c.Query("page_key")
	if pageKey == "" {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "page_key", "reason": "必填",
		}))
		return
	}
	rows, err := h.svc.ListViews(c.Request.Context(), uid, pageKey)
	if err != nil {
		response.Err(c, err)
		return
	}
	if rows == nil {
		rows = []SavedView{}
	}
	response.OK(c, rows)
}

// createView POST /api/user/views。
//
//	@Summary POST /api/user/views
//	@Tags 用户态
//	@Accept json
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 400 {object} response.Envelope "请求参数错误"
//	@Failure 409 {object} response.Envelope "同名视图已存在"
//	@Router /api/user/views [post]
func (h *handler) createView(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	var body viewBody
	if !bindJSON(c, &body) {
		return
	}
	v, err := h.svc.CreateView(c.Request.Context(), uid, ViewInput{
		PageKey:     body.PageKey,
		Name:        body.Name,
		FiltersJSON: body.FiltersJSON,
		SortJSON:    body.SortJSON,
		ColumnsJSON: body.ColumnsJSON,
		PageSize:    body.PageSize,
		IsDefault:   body.IsDefault,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateView PUT /api/user/views/:id。
//
//	@Summary PUT /api/user/views/:id
//	@Tags 用户态
//	@Accept json
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 400 {object} response.Envelope "请求参数错误"
//	@Failure 404 {object} response.Envelope "视图不存在（含非本人）"
//	@Failure 409 {object} response.Envelope "同名视图已存在"
//	@Router /api/user/views/{id} [put]
func (h *handler) updateView(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var body viewBody
	if !bindJSON(c, &body) {
		return
	}
	v, err := h.svc.UpdateView(c.Request.Context(), uid, id, ViewInput{
		Name:        body.Name,
		FiltersJSON: body.FiltersJSON,
		SortJSON:    body.SortJSON,
		ColumnsJSON: body.ColumnsJSON,
		PageSize:    body.PageSize,
		IsDefault:   body.IsDefault,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// deleteView DELETE /api/user/views/:id。
//
//	@Summary DELETE /api/user/views/:id
//	@Tags 用户态
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 404 {object} response.Envelope "视图不存在（含非本人）"
//	@Router /api/user/views/{id} [delete]
func (h *handler) deleteView(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteView(c.Request.Context(), uid, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// listPreferences GET /api/user/preferences?keys=a,b（缺省白名单全量）。
//
//	@Summary GET /api/user/preferences
//	@Tags 用户态
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 400 {object} response.Envelope "请求参数错误"
//	@Router /api/user/preferences [get]
func (h *handler) listPreferences(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	var keys []string
	if raw := c.Query("keys"); raw != "" {
		keys = []string{raw}
	}
	rows, err := h.svc.ListPreferences(c.Request.Context(), uid, keys)
	if err != nil {
		response.Err(c, err)
		return
	}
	if rows == nil {
		rows = []Preference{}
	}
	response.OK(c, rows)
}

// putPreference PUT /api/user/preferences/:key。
//
//	@Summary PUT /api/user/preferences/:key
//	@Tags 用户态
//	@Accept json
//	@Produce json
//	@Success 200 {object} response.Envelope "统一响应信封"
//	@Failure 400 {object} response.Envelope "key 白名单外 / 值非法或超 16KB"
//	@Router /api/user/preferences/{key} [put]
func (h *handler) putPreference(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	key := c.Param("key")
	raw, err := c.GetRawData()
	if err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "value", "reason": "请求体读取失败",
		}))
		return
	}
	p, err := h.svc.PutPreference(c.Request.Context(), uid, key, raw)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, p)
}
