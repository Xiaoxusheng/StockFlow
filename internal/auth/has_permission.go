package auth

import (
	"github.com/gin-gonic/gin"
)

// has_permission.go 编程式权限判定（efficiency-layer-phase1 §2.1：GET /api/search
// 端点级只挂认证，组内逐 type 过滤需要非中间件形态的判定入口）。
//
// 语义与 RequirePermission 完全同构（middleware.go：未认证拒绝 + 超管直通 +
// 权限快照含码），零语义新增；数据源同 loadPermissionCodes（Redis 缓存优先 →
// 回源 DB）。差异仅在失败形态：RequirePermission 写 403/503 响应并 Abort，
// 本函数返回 false 由调用方决定处置（搜索场景 = 该 type 不查询不出组；
// 缓存/回源故障 fail-closed 与中间件同一策略）。

// HasPermission 判定当前用户是否持有权限点 code（须在 AuthRequired 之后调用）。
// 未认证 / 中间件未装配 / 权限数据不可用一律 false（fail-closed，go-dev-standard
// 缓存故障策略：安全功能不能"没有数据 = 有权限"）。
func HasPermission(c *gin.Context, code string) bool {
	uc, ok := CurrentUser(c)
	if !ok {
		return false
	}
	// 超级管理员直通（permission.md §1；RequirePermission 同构语义——遗漏则
	// 超管在全局搜索看不到任何分组）。
	if uc.IsSuper {
		return true
	}
	_, store, _, repo, svc, _, wired := snapshotWired()
	if !wired || svc == nil {
		return false
	}
	permCodes, err := loadPermissionCodes(c, svc, repo, store, uc.UserID)
	if err != nil {
		return false
	}
	return hasPermission(permCodes, code)
}
