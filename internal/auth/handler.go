package auth

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应封装；
// 禁止直连数据库，plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。

// svcOf 取装配好的 Service（router 先 Register 才有请求可达，缺位属装配错误）。
func svcOf() (*Service, bool) {
	_, _, _, _, svc, _, wired := snapshotWired()
	return svc, wired && svc != nil
}

// abortUnwired 未装配的统一 500。
func abortUnwired(c *gin.Context) {
	response.Err(c, response.NewError(response.CodeInternalError, map[string]any{
		"reason": "auth 域未装配（router 未调用 Register*Routes）",
	}))
}

// actorOf 从 gin 上下文提取操作者归因（审计用）。
// DataScope/DeptID/WarehouseIDs 为会话数据范围快照（plan §7.4），
// 供授予侧特权边界（S1）与用户目录收敛（S9）在 Service 层判定。
func actorOf(c *gin.Context) Actor {
	uc, _ := CurrentUser(c)
	return Actor{
		UserID:       uc.UserID,
		Username:     uc.Username,
		IsSuper:      uc.IsSuper,
		DataScope:    uc.DataScope,
		DeptID:       uc.DeptID,
		WarehouseIDs: uc.WarehouseIDs,
		RequestID:    c.GetString(response.RequestIDKey),
		IP:           c.ClientIP(),
		UserAgent:    c.Request.UserAgent(),
		Method:       c.Request.Method,
		Path:         c.FullPath(),
	}
}

// pathID 解析路径 ID（api.md §2：ID 一律字符串形态，路由参数解析为 int64）。
func pathID(c *gin.Context, name string) (int64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return id, true
}

// queryID 可选整型查询参数（0 = 未提供；非法值 fail-fast，不静默归 0）。
func queryID(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return id, true
}

// ---- /api/auth 公开接口（api.md §6.1 豁免名单）----

// LoginRequest 登录请求体。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// handleLogin POST /api/auth/login。
// @Summary POST /api/auth/login
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body LoginRequest true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/login [post]
func handleLogin(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := svc.Login(c.Request.Context(), LoginInput{
		Username:  req.Username,
		Password:  req.Password,
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		RequestID: c.GetString(response.RequestIDKey),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// RefreshRequest 刷新请求体。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// handleRefresh POST /api/auth/refresh。
// @Summary POST /api/auth/refresh
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body RefreshRequest true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/refresh [post]
func handleRefresh(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	res, err := svc.Refresh(c.Request.Context(), RefreshInput{
		RefreshToken: req.RefreshToken,
		IP:           c.ClientIP(),
		UserAgent:    c.Request.UserAgent(),
		RequestID:    c.GetString(response.RequestIDKey),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// ---- /api/auth 受保护接口 ----

// handleLogout POST /api/auth/logout。
// @Summary POST /api/auth/logout
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/logout [post]
func handleLogout(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	sess, _ := CurrentSession(c)
	if err := svc.Logout(c.Request.Context(), sess, c.ClientIP(), c.Request.UserAgent()); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"logout": true})
}

// handleMe GET /api/auth/me。
// @Summary GET /api/auth/me
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/me [get]
func handleMe(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	uc, ok := CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	sess, _ := CurrentSession(c)
	res, err := svc.Me(c.Request.Context(), uc, sess)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}

// ChangePasswordRequest 修改密码请求体。
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// handlePassword PUT /api/auth/password。
// @Summary PUT /api/auth/password
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body ChangePasswordRequest true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/password [put]
func handlePassword(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	sess, ok := CurrentSession(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.ChangePassword(c.Request.Context(), sess, actorOf(c), req.OldPassword, req.NewPassword); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"changed": true})
}

// handleSessionList GET /api/auth/sessions（auth:session:list；内存分页满足强制分页）。
// @Summary GET /api/auth/sessions（auth:session:list；内存分页满足强制分页）
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/sessions [get]
func handleSessionList(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	all, err := svc.ListSessions(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start > int(total) {
		start = int(total)
	}
	end := start + pageSize
	if end > int(total) {
		end = int(total)
	}
	// 标记当前请求会话，便于前端展示"本机"。
	cur, _ := CurrentSession(c)
	items := all[start:end]
	for _, it := range items {
		if cur != nil && it.SessionID == cur.SID {
			it.Current = true
		}
	}
	response.OKPage(c, items, page, pageSize, total)
}

// handleSessionKick DELETE /api/auth/sessions/:id（auth:session:kick；强制审计）。
// @Summary DELETE /api/auth/sessions/:id（auth:session:kick；强制审计）
// @Tags 认证与用户
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/auth/sessions/{id} [delete]
func handleSessionKick(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	sid := c.Param("id")
	if strings.TrimSpace(sid) == "" {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{"field": "id", "reason": "必填"}))
		return
	}
	if err := svc.KickSession(c.Request.Context(), actorOf(c), sid); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"kicked": true})
}

// ---- /api/users（plan §5.4：CRUD + 启停/重置密码/解锁/绑定角色；M1 无删除接口）----

// handleUserList GET /api/users。
// @Summary GET /api/users
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users [get]
func handleUserList(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	deptID, okDept := queryID(c, "department_id")
	if !okDept {
		return
	}
	items, total, err := svc.GetUsers(c.Request.Context(), actorOf(c), UserListFilter{
		Keyword:      c.Query("keyword"),
		Status:       c.Query("status"),
		DepartmentID: deptID,
		Page:         page,
		PageSize:     pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// handleUserDetail GET /api/users/:id。
// @Summary GET /api/users/:id
// @Tags 认证与用户
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id} [get]
func handleUserDetail(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	view, err := svc.GetUserDetail(c.Request.Context(), actorOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleUserCreate POST /api/users。
// @Summary POST /api/users
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body UserCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users [post]
func handleUserCreate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	var req UserCreateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.CreateUser(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleUserUpdate PUT /api/users/:id。
// @Summary PUT /api/users/:id
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body UserUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id} [put]
func handleUserUpdate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req UserUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.UpdateUser(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleUserStatus PUT /api/users/:id/status。
// @Summary PUT /api/users/:id/status
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body StatusRequest true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id}/status [put]
func handleUserStatus(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.UpdateUserStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

// handleUserResetPassword PUT /api/users/:id/reset-password。
// @Summary PUT /api/users/:id/reset-password
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body ResetPasswordInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id}/reset-password [put]
func handleUserResetPassword(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req ResetPasswordInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.ResetUserPassword(c.Request.Context(), actorOf(c), id, req); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"reset": true})
}

// handleUserUnlock PUT /api/users/:id/unlock。
// @Summary PUT /api/users/:id/unlock
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id}/unlock [put]
func handleUserUnlock(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	if err := svc.UnlockUser(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"unlocked": true})
}

// handleUserAssignRoles PUT /api/users/:id/roles。
// @Summary PUT /api/users/:id/roles
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body AssignRolesInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/users/{id}/roles [put]
func handleUserAssignRoles(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req AssignRolesInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.AssignRoles(c.Request.Context(), actorOf(c), id, req); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"assigned": true})
}

// ---- /api/roles（CRUD + 绑定权限；无删除，database.md §5.1 清单不含 roles）----

// handleRoleList GET /api/roles。
// @Summary GET /api/roles
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles [get]
func handleRoleList(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := svc.GetRoles(c.Request.Context(), RoleListFilter{
		Keyword:  c.Query("keyword"),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// handleRoleDetail GET /api/roles/:id。
// @Summary GET /api/roles/:id
// @Tags 认证与用户
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles/{id} [get]
func handleRoleDetail(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	view, err := svc.GetRoleDetail(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleRoleCreate POST /api/roles。
// @Summary POST /api/roles
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body RoleCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles [post]
func handleRoleCreate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	var req RoleCreateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.CreateRole(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleRoleUpdate PUT /api/roles/:id。
// @Summary PUT /api/roles/:id
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body RoleUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles/{id} [put]
func handleRoleUpdate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req RoleUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.UpdateRole(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// StatusRequest 通用启停请求体。
type StatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// handleRoleStatus PUT /api/roles/:id/status。
// @Summary PUT /api/roles/:id/status
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body StatusRequest true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles/{id}/status [put]
func handleRoleStatus(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.UpdateRoleStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

// handleRoleAssignPermissions PUT /api/roles/:id/permissions。
// @Summary PUT /api/roles/:id/permissions
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body AssignPermissionsInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/roles/{id}/permissions [put]
func handleRoleAssignPermissions(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req AssignPermissionsInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.AssignPermissions(c.Request.Context(), actorOf(c), id, req); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"assigned": true})
}

// ---- /api/permissions ----

// handlePermissionList GET /api/permissions（分页 + type/status/keyword 筛选）。
// @Summary GET /api/permissions（分页 + type/status/keyword 筛选）
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/permissions [get]
func handlePermissionList(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := svc.GetPermissions(c.Request.Context(), PermissionListFilter{
		Keyword:  c.Query("keyword"),
		Type:     c.Query("type"),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// ---- /api/departments（CRUD + 启停；无删除）----

// handleDeptList GET /api/departments。
// @Summary GET /api/departments
// @Tags 认证与用户
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/departments [get]
func handleDeptList(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := svc.GetDepts(c.Request.Context(), DeptListFilter{
		Keyword:  c.Query("keyword"),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// handleDeptDetail GET /api/departments/:id。
// @Summary GET /api/departments/:id
// @Tags 认证与用户
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/departments/{id} [get]
func handleDeptDetail(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	view, err := svc.GetDeptDetail(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleDeptCreate POST /api/departments。
// @Summary POST /api/departments
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body DeptCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/departments [post]
func handleDeptCreate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	var req DeptCreateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.CreateDept(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleDeptUpdate PUT /api/departments/:id。
// @Summary PUT /api/departments/:id
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body DeptUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/departments/{id} [put]
func handleDeptUpdate(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req DeptUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	view, err := svc.UpdateDept(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// handleDeptStatus PUT /api/departments/:id/status。
// @Summary PUT /api/departments/:id/status
// @Tags 认证与用户
// @Accept json
// @Produce json
// @Param body body StatusRequest true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/departments/{id}/status [put]
func handleDeptStatus(c *gin.Context) {
	svc, ok := svcOf()
	if !ok {
		abortUnwired(c)
		return
	}
	id, okID := pathID(c, "id")
	if !okID {
		return
	}
	var req StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return
	}
	if err := svc.UpdateDeptStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}
