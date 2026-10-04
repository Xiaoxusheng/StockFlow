package devices

import "github.com/stockflow/server/internal/auth"

// 权限点常量（backend-m3-plan §11.1 冻结清单，域:资源:动作；动作词沿用 M1/M2 冻结枚举）。
//
// 收编落位（MT6）：常量唯一来源已并入 internal/auth/permissions.go（M3 段，seed 种子
// 同源）；本文件保留同名包内别名（internal/returns/permissions.go 先例）——字符串值与
// 冻结清单逐字一致，handler.go 引用零改动。清单外动作词一律不发明（plan §2.3 判据 9）。
//
// 端点映射（plan §11.1）：bind/unbind/config 复用 update；disable 复用 status；
// scanner:resolve:list 映射全量角色授权（扫码是全角色基础输入），保留可关闭能力；
// devices:scanlog:read 冻结导出但 M3 无独立详情端点（scan_logs 无行详情形态，
// 与 M2"未开端点的冻结动作词"口径一致——常量随 seed 收编，不虚设路由）。
const (
	// —— devices:device（plan §11.1）——
	PermDeviceList   = auth.PermDeviceList
	PermDeviceRead   = auth.PermDeviceRead
	PermDeviceCreate = auth.PermDeviceCreate
	PermDeviceUpdate = auth.PermDeviceUpdate // bind/unbind/config/重新生成激活码
	PermDeviceStatus = auth.PermDeviceStatus // disable 停用

	// —— devices:scanlog（plan §11.1：扫码日志查询，设备管理后台 devices.md §7.1）——
	PermScanLogList = auth.PermScanLogList
	PermScanLogRead = auth.PermScanLogRead // 冻结导出，M3 无独立详情端点

	// —— scanner:resolve（plan §11.1：统一解析）——
	PermScannerResolveList = auth.PermScannerResolveList
)
