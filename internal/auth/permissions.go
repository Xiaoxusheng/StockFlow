package auth

// 权限点常量清单——backend-m1-plan §5.4.1 全量冻结清单（T2–T5 并行的唯一权威清单），
// 与 internal/database/seed.go 的权限种子同源（禁止发明清单外动作词）。
//
// 命名：`域:资源:动作`，域按 api.md §1 领域划分（auth / masterdata / warehouse / inventory）。
// 动作枚举：list / read / create / update / delete / status 六个基础动作，
// 外加各资源的特有动作（assign-role / assign-permission / reset-password / unlock / kick）。
// 说明：ask 示例中的 "product:list"、"inventory:adjust" 为示意写法，实际以本冻结清单为准
// （商品 = masterdata:product:*；M1 库存 HTTP 面只读，无 adjust 权限点，plan §8.7）。

// —— auth 域（认证与权限，api.md §1）——
const (
	PermUserList          = "auth:user:list"
	PermUserRead          = "auth:user:read"
	PermUserCreate        = "auth:user:create"
	PermUserUpdate        = "auth:user:update"
	PermUserStatus        = "auth:user:status"
	PermUserAssignRole    = "auth:user:assign-role"
	PermUserResetPassword = "auth:user:reset-password"
	PermUserUnlock        = "auth:user:unlock"

	PermRoleList             = "auth:role:list"
	PermRoleRead             = "auth:role:read"
	PermRoleCreate           = "auth:role:create"
	PermRoleUpdate           = "auth:role:update"
	PermRoleStatus           = "auth:role:status"
	PermRoleAssignPermission = "auth:role:assign-permission"

	PermPermissionList = "auth:permission:list"

	PermDeptList   = "auth:dept:list"
	PermDeptRead   = "auth:dept:read"
	PermDeptCreate = "auth:dept:create"
	PermDeptUpdate = "auth:dept:update"
	PermDeptStatus = "auth:dept:status"

	PermSessionList = "auth:session:list"
	PermSessionKick = "auth:session:kick"
)

// —— masterdata 域（基础资料，api.md §1；供 scope D 路由挂载使用）——
const (
	PermProductList   = "masterdata:product:list"
	PermProductRead   = "masterdata:product:read"
	PermProductCreate = "masterdata:product:create"
	PermProductUpdate = "masterdata:product:update"
	PermProductDelete = "masterdata:product:delete"
	PermProductStatus = "masterdata:product:status"

	PermSKUList   = "masterdata:sku:list"
	PermSKURead   = "masterdata:sku:read"
	PermSKUCreate = "masterdata:sku:create"
	PermSKUUpdate = "masterdata:sku:update"
	PermSKUDelete = "masterdata:sku:delete"
	PermSKUStatus = "masterdata:sku:status"

	PermCategoryList   = "masterdata:category:list"
	PermCategoryRead   = "masterdata:category:read"
	PermCategoryCreate = "masterdata:category:create"
	PermCategoryUpdate = "masterdata:category:update"
	PermCategoryStatus = "masterdata:category:status"

	PermUnitList   = "masterdata:unit:list"
	PermUnitRead   = "masterdata:unit:read"
	PermUnitCreate = "masterdata:unit:create"
	PermUnitUpdate = "masterdata:unit:update"
	PermUnitStatus = "masterdata:unit:status"

	PermSupplierList   = "masterdata:supplier:list"
	PermSupplierRead   = "masterdata:supplier:read"
	PermSupplierCreate = "masterdata:supplier:create"
	PermSupplierUpdate = "masterdata:supplier:update"
	PermSupplierDelete = "masterdata:supplier:delete"
	PermSupplierStatus = "masterdata:supplier:status"

	PermCustomerList   = "masterdata:customer:list"
	PermCustomerRead   = "masterdata:customer:read"
	PermCustomerCreate = "masterdata:customer:create"
	PermCustomerUpdate = "masterdata:customer:update"
	PermCustomerDelete = "masterdata:customer:delete"
	PermCustomerStatus = "masterdata:customer:status"
)

// —— warehouse 域（仓库空间，api.md §1；供 scope E 路由挂载使用）——
const (
	PermWarehouseList   = "warehouse:warehouse:list"
	PermWarehouseRead   = "warehouse:warehouse:read"
	PermWarehouseCreate = "warehouse:warehouse:create"
	PermWarehouseUpdate = "warehouse:warehouse:update"
	PermWarehouseDelete = "warehouse:warehouse:delete"
	PermWarehouseStatus = "warehouse:warehouse:status"

	PermZoneList   = "warehouse:zone:list"
	PermZoneRead   = "warehouse:zone:read"
	PermZoneCreate = "warehouse:zone:create"
	PermZoneUpdate = "warehouse:zone:update"
	PermZoneStatus = "warehouse:zone:status"

	PermShelfList   = "warehouse:shelf:list"
	PermShelfRead   = "warehouse:shelf:read"
	PermShelfCreate = "warehouse:shelf:create"
	PermShelfUpdate = "warehouse:shelf:update"
	PermShelfStatus = "warehouse:shelf:status"

	PermBinList   = "warehouse:bin:list"
	PermBinRead   = "warehouse:bin:read"
	PermBinCreate = "warehouse:bin:create"
	PermBinUpdate = "warehouse:bin:update"
	PermBinDelete = "warehouse:bin:delete"
	PermBinStatus = "warehouse:bin:status"
)

// —— inventory 域（库存，api.md §1；供 scope F 路由挂载使用；M1 只读，plan §8.7）——
const (
	PermInventoryList = "inventory:inventory:list"
	PermLedgerList    = "inventory:ledger:list"
	PermBatchList     = "inventory:batch:list"
	PermSerialList    = "inventory:serial:list"
)

// SuperAdminRoleCode 超级管理员角色编码（seed 内置角色）：绕过 RBAC 判定但仅此一项，
// 数据权限仍按 users.data_scope（恒 ALL）。
const SuperAdminRoleCode = "super_admin"
