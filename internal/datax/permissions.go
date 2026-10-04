package datax

import "github.com/stockflow/server/internal/auth"

// 权限点常量（backend-m3-plan §11.1 冻结清单，域:资源:动作；动作词沿用 M1/M2 冻结枚举，
// 清单外动作词禁止发明——plan §2.3 判据 9）。
//
// 收编落位（MT6，plan §2.2 Scope I/§11）：常量唯一来源已并入 internal/auth/permissions.go
// （M3 段，seed 种子同源，seed_test 字面清单 + 探针测试交叉核验）；本文件保留同名包内
// 别名——handler.go 路由挂载引用零改动，字符串值与冻结清单逐字一致
// （internal/returns/permissions.go 先例）。
//
// 动作语义（§11.1）：import create=上传+校验、execute=确认导入（高危二次确认与审计）；
// export create=创建导出任务（permission §6 敏感操作）、read=下载产物/错误文件；
// file 四动作 = 文件中心 list/read/create/delete。
const (
	// —— datax:import（导入中心）——
	PermImportList    = auth.PermImportList
	PermImportRead    = auth.PermImportRead    // 详情/预览/模板下载/错误 Excel 下载
	PermImportCreate  = auth.PermImportCreate  // 上传解析 + 发起校验
	PermImportExecute = auth.PermImportExecute // 确认导入（高危二次确认与审计）

	// —— datax:export（导出中心）——
	PermExportList   = auth.PermExportList
	PermExportRead   = auth.PermExportRead // 产物下载（permission §6 敏感操作）
	PermExportCreate = auth.PermExportCreate

	// —— datax:file（文件中心）——
	PermFileList   = auth.PermFileList
	PermFileRead   = auth.PermFileRead // 下载/预览
	PermFileCreate = auth.PermFileCreate
	PermFileDelete = auth.PermFileDelete
)
