package sysops

import (
	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// auditActor 操作者归因（审计写入与 created_by 落值的统一载体；
// 形状对齐 stock.Actor 先例——内建类型字段，Service 层不感知 gin）。
type auditActor struct {
	UserID    int64
	Name      string
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// actorOf 从 gin 上下文构造操作者（handler 层唯一入口；未认证场景兜底零值——
// 路由全部挂 auth.AuthRequired，零值仅出现在单测直调路径）。
func actorOf(c *gin.Context) auditActor {
	uc, _ := auth.CurrentUser(c)
	return auditActor{
		UserID:    uc.UserID,
		Name:      uc.Username,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
	}
}

// auditEntry 构造审计条目（module=system 域统一动作审计入口）。
func auditEntry(actor auditActor, action, objectType string, objectID int64, before, after, request any) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "system",
		ObjectType:   objectType,
		ObjectID:     objectID,
		Action:       action,
		OperatorID:   actor.UserID,
		OperatorName: actor.Name,
		RequestID:    actor.RequestID,
		IP:           actor.IP,
		UserAgent:    actor.UserAgent,
		Method:       actor.Method,
		Path:         actor.Path,
		Success:      true,
		Before:       before,
		After:        after,
		Request:      request,
	}
}
