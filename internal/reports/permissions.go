package reports

import "github.com/stockflow/server/internal/auth"

// 报表域权限点常量（backend-m3-plan §11.1：reports:report 资源，动作 list/read）。
//
// 收编落位（MT6，plan §11）：常量唯一来源已并入 internal/auth/permissions.go（M3 段，
// seed 种子同源）；本文件保留 auth 同名常量别名（sales/permissions.go 收敛先例），
// 字符串值不变、路由挂载零改动。
const (
	// PermReportList 报表目录与报表查询（/api/reports 组）。
	PermReportList = auth.PermReportList
	// PermReportRead 报表明细/智能建议读取。
	PermReportRead = auth.PermReportRead
)
