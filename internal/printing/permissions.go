package printing

import "github.com/stockflow/server/internal/auth"

// 权限点常量（backend-m3-plan §11.1 冻结清单，域:资源:动作；清单外动作词一律不发明，
// plan §2.3 判据 9）。
//
// 收编落位（MT6，plan §2.1/§11）：常量唯一来源已并入 internal/auth/permissions.go
// （M3 段，seed 种子同源，seed_test 交叉核验）；本文件保留同名包内别名引用 auth 同名
// 常量（internal/returns/permissions.go 先例），字符串值不变、路由挂载零改动。
const (
	// —— printing:template（plan §11.1：copy 复用 create）——
	PermTemplateList   = auth.PermPrintTemplateList
	PermTemplateRead   = auth.PermPrintTemplateRead
	PermTemplateCreate = auth.PermPrintTemplateCreate
	PermTemplateUpdate = auth.PermPrintTemplateUpdate
	PermTemplateStatus = auth.PermPrintTemplateStatus

	// —— printing:task（plan §11.1：execute=打印执行确认）——
	PermTaskList    = auth.PermPrintTaskList
	PermTaskRead    = auth.PermPrintTaskRead
	PermTaskCreate  = auth.PermPrintTaskCreate
	PermTaskExecute = auth.PermPrintTaskExecute
)
