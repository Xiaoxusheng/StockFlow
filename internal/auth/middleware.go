package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// 认证与授权中间件（plan §5.1 冻结契约；permission.md §2 API 层强制校验）。
//
// AuthRequired：Bearer JWT 校验（签名/过期）+ Redis 会话校验（被踢/过期 → 401
// SESSION_INVALID，plan §7.2 双轨），注入 UserContext 与会话快照；首登强制改密门禁
// （plan §7.3：must_change_password=true 时除白名单外一律 403 PASSWORD_CHANGE_REQUIRED）。
//
// RequirePermission：RBAC 权限点校验，超级管理员直通；权限集走 Redis 缓存
//（未命中回源 DB），缓存/DB 故障时 fail-closed 拒绝（go-dev-standard 缓存故障策略：
// 安全功能不能"没有缓存 = 有权限"）。

// gin 上下文键（本包内读写；跨包只经 CurrentUser/WarehouseScope 取值）。
const (
	ctxUserKey    = "sf_auth_user"
	ctxSessionKey = "sf_auth_session"
)

// WarehouseChecker 跨域消费接口（plan §4.3）：用户绑定仓库的存在性校验。
// 实现由 warehouse 域提供（warehouse.NewChecker(db)），router 装配注入；
// 结构化满足本接口即可（方法名 ExistsActive）。
type WarehouseChecker interface {
	ExistsActive(ctx context.Context, warehouseID int64) (bool, error)
}

// AuthRequired 认证中间件：三业务域唯一的鉴权入口（plan §5.1，签名冻结）。
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		_, store, _, _, _, _, wired := snapshotWired()
		if !wired || store == nil {
			response.Err(c, response.NewError(response.CodeInternalError, map[string]any{
				"reason": "auth 中间件未装配（router 必须先调用 RegisterPublicRoutes/RegisterProtectedRoutes）",
			}))
			c.Abort()
			return
		}

		raw := bearerToken(c.GetHeader("Authorization"))
		uid, sid, err := parseAccessToken(cfgOf(), raw)
		if err != nil {
			response.Err(c, err)
			c.Abort()
			return
		}

		sess, err := store.Get(c.Request.Context(), sid)
		if err != nil {
			// Redis 故障：无法确认会话有效 → fail-closed（503，拒绝而非放行）。
			response.Err(c, response.NewError(response.CodeServiceUnavailable, map[string]any{
				"reason": "会话存储不可用",
			}))
			c.Abort()
			return
		}
		if sess == nil || sess.UserID != uid {
			// 被踢/过期/重放（uid 与会话不匹配）→ 401 SESSION_INVALID（plan §7.2）。
			response.Err(c, response.NewError(ErrSessionInvalid, nil))
			c.Abort()
			return
		}

		// 首登强制改密门禁（plan §7.3）：仅放行改密/登出/me。
		if sess.MustChangePassword && !passwordGateAllowed(c.Request.Method, c.FullPath()) {
			response.Err(c, response.NewError(ErrPasswordChangeRequired, nil))
			c.Abort()
			return
		}

		// 注入用户上下文与会话快照（数据范围快照来源，plan §7.4）。
		c.Set(ctxUserKey, UserContext{
			UserID:       sess.UserID,
			Username:     sess.Username,
			IsSuper:      sess.IsSuper,
			DataScope:    sess.DataScope,
			WarehouseIDs: sess.WarehouseIDs,
			DeptID:       sess.DeptID,
			SessionID:    sess.SID,
		})
		c.Set(ctxSessionKey, sess)

		// 滑动续期（plan §7.2 滑动 TTL）：失败不影响本次请求。
		store.Touch(c.Request.Context(), sess)
		c.Next()
	}
}

// passwordGateAllowed 强制改密白名单：PUT /api/auth/password、POST /api/auth/logout、
// GET /api/auth/me（改密页需要 me 取权限集渲染；登出走未认证清理路径）。
// 计划原文仅点名 password 接口，本域放宽此两个只读/清理入口并在交付中披露。
func passwordGateAllowed(method, fullPath string) bool {
	switch {
	case method == http.MethodPut && strings.HasSuffix(fullPath, "/auth/password"),
		method == http.MethodPost && strings.HasSuffix(fullPath, "/auth/logout"),
		method == http.MethodGet && strings.HasSuffix(fullPath, "/auth/me"):
		return true
	}
	return false
}

// RequirePermission RBAC 权限点校验（plan §5.1 冻结签名）。
// 失败 403 COMMON_PERMISSION_DENIED（api.md §6.2：接口声明权限点，中间件统一校验）。
func RequirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uc, ok := CurrentUser(c)
		if !ok {
			// AuthRequired 未挂载/未通过的编程错误。
			response.Err(c, response.NewError(response.CodeUnauthorized, nil))
			c.Abort()
			return
		}
		// 超级管理员直通（permission.md §1；数据权限仍按 data_scope）。
		if uc.IsSuper {
			c.Next()
			return
		}
		_, store, _, repo, svc, _, wired := snapshotWired()
		if !wired || svc == nil {
			response.Err(c, response.NewError(response.CodeInternalError, map[string]any{
				"reason": "auth 中间件未装配",
			}))
			c.Abort()
			return
		}

		permCodes, err := loadPermissionCodes(c, svc, repo, store, uc.UserID)
		if err != nil {
			// 缓存与回源均失败 → fail-closed（go-dev-standard 缓存故障策略）。
			response.Err(c, response.NewError(response.CodeServiceUnavailable, map[string]any{
				"reason": "权限数据不可用",
			}))
			c.Abort()
			return
		}
		if !hasPermission(permCodes, perm) {
			response.Err(c, response.NewError(response.CodePermissionDenied, map[string]any{
				"permission": perm,
			}))
			c.Abort()
			return
		}
		c.Next()
	}
}

// hasPermission 权限判定（纯函数，供单测）。
func hasPermission(codes []string, perm string) bool {
	for _, c := range codes {
		if c == perm {
			return true
		}
	}
	return false
}

// loadPermissionCodes 权限点集读取：缓存优先 → 回源 DB（回源成功即回填缓存）。
func loadPermissionCodes(c *gin.Context, svc *Service, _ Repository, _ *sessionStore, uid int64) ([]string, error) {
	return svc.PermissionCodes(c.Request.Context(), uid)
}

// CurrentUser 取当前用户上下文（AuthRequired 之后可用；plan §5.1 冻结签名）。
func CurrentUser(c *gin.Context) (UserContext, bool) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return UserContext{}, false
	}
	uc, ok := v.(UserContext)
	return uc, ok
}

// CurrentSession 取当前会话快照（登出/改密需要会话级信息；本包 handler 使用）。
func CurrentSession(c *gin.Context) (*sessionData, bool) {
	v, ok := c.Get(ctxSessionKey)
	if !ok {
		return nil, false
	}
	sess, ok := v.(*sessionData)
	return sess, ok
}

// WarehouseScope 数据权限仓库范围快照（plan §5.1 冻结签名；permission.md §4）：
//   - ALL / 超级管理员      → (true, nil)   全部仓库
//   - SPECIFIED_WAREHOUSE   → (false, ids)  绑定仓库集（可为空集 = 不可见任何仓库）
//   - DEPARTMENT / SELF / SELF_IN_CHARGE → (false, nil)
//     M1 只落模型与快照，仓库级行过滤随 M2 单据域（plan §7.4）；M1 仓库维度查询
//     对这三种范围返回空集（不可见），不静默放大为"全部"。
func WarehouseScope(c *gin.Context) (all bool, ids []int64) {
	uc, ok := CurrentUser(c)
	if !ok {
		return false, nil
	}
	return scopeOf(uc)
}

// scopeOf 范围判定（纯函数，供单测与 ApplyWarehouseScope 复用）。
func scopeOf(uc UserContext) (all bool, ids []int64) {
	if uc.IsSuper || uc.DataScope == DataScopeAll {
		return true, nil
	}
	if uc.DataScope == DataScopeSpecifiedWh {
		return false, uc.WarehouseIDs
	}
	return false, nil
}

// ApplyWarehouseScope 数据权限过滤助手（供三个业务域直接使用）：
// 按当前用户仓库范围给查询注入过滤条件（column 为仓库 ID 列名，默认 "warehouse_id"）。
// 禁止接受前端传入的仓库范围参数决定数据可见性（permission.md §4 实现位置约定）。
func ApplyWarehouseScope(c *gin.Context, db *gorm.DB, column string) *gorm.DB {
	all, ids := WarehouseScope(c)
	if column == "" {
		column = "warehouse_id"
	}
	if all {
		return db
	}
	if len(ids) == 0 {
		// 指定仓库范围但无绑定 / M1 未落行级的范围 → 不可见任何行（fail-closed）。
		return db.Where("1 = 0")
	}
	return db.Where(column+" IN ?", ids)
}

// SelfScope 数据权限过滤助手（本人维度）：creator/operator 列 = 当前用户。
// M1 行级使用方为 M2 单据域（plan §7.4），助手先行交付保持接入面稳定。
func SelfScope(c *gin.Context, db *gorm.DB, column string) *gorm.DB {
	uc, ok := CurrentUser(c)
	if !ok {
		return db.Where("1 = 0")
	}
	if column == "" {
		column = "created_by"
	}
	return db.Where(column+" = ?", uc.UserID)
}

// DepartmentScope 数据权限过滤助手（部门维度）：DEPARTMENT 范围返回用户部门 ID。
// M1 只落快照（plan §7.4），部门级行过滤随 M2；返回 ok=false 表示该范围不适用。
func DepartmentScope(c *gin.Context) (deptID int64, ok bool) {
	uc, has := CurrentUser(c)
	if !has || uc.DataScope != DataScopeDepartment || uc.DeptID <= 0 {
		return 0, false
	}
	return uc.DeptID, true
}

// bearerToken 提取 Bearer 凭证（大小写不敏感的 scheme，多余空白容忍）。
func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// cfgOf 当前运行配置快照（中间件路径使用；未装配返回零值，由调用方 wired 判定兜底）。
func cfgOf() runtimeConfig {
	cfg, _, _, _, _, _, _ := snapshotWired()
	return cfg
}
