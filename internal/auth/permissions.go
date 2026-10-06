package auth

// 权限点常量清单——backend-m1-plan §5.4.1（M1 段）+ backend-m2-plan §9.2（M2 段）
// + backend-m3-plan §11.1（M3 段）全量冻结清单，与 internal/database/seed.go 的权限种子
// 同源（禁止发明清单外动作词）。
//
// 命名：`域:资源:动作`，域按 api.md §1 领域划分（auth / masterdata / warehouse /
// inventory / purchase / sales / stockops / returns + M3 平台域 datax / printing /
// devices / scanner / reports / system）。
// 动作枚举：M1 基础六动作 list/read/create/update/delete/status + M1 特有动作
// （assign-role/assign-permission/reset-password/unlock/kick）+ M2 作业/审批动作
// （backend-m2-plan §9.1：submit/approve/cancel/close/execute/claim/assign）。
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

// —— M2 权限点段（backend-m2-plan §9.2 全量冻结清单，21 资源 / 106 动作点）——
//
// 归属：plan §9.2 规定 M2 权限点常量由集成工程师一次性收编至本文件（单一来源，
// seed 种子同源，seed_test.go 字面清单交叉核验）；四个单据域包内同名常量自 MT5
// 起改为引用本段别名（字符串值不变，路由挂载零改动）。
// 动作词为 plan §9.1 冻结扩展：submit/approve/cancel/close/execute/claim/assign
// （清单外动作词禁止发明，§2.3 判据 9）。
const (
	// —— purchase:purchase（采购订单）——
	PermPurchaseList    = "purchase:purchase:list"
	PermPurchaseRead    = "purchase:purchase:read"
	PermPurchaseCreate  = "purchase:purchase:create"
	PermPurchaseUpdate  = "purchase:purchase:update"
	PermPurchaseSubmit  = "purchase:purchase:submit"
	PermPurchaseApprove = "purchase:purchase:approve"
	PermPurchaseCancel  = "purchase:purchase:cancel"
	PermPurchaseClose   = "purchase:purchase:close"

	// —— purchase:inbound（入库单）——
	PermInboundList   = "purchase:inbound:list"
	PermInboundRead   = "purchase:inbound:read"
	PermInboundCreate = "purchase:inbound:create"
	PermInboundUpdate = "purchase:inbound:update"
	PermInboundCancel = "purchase:inbound:cancel"
	PermInboundClose  = "purchase:inbound:close"

	// —— purchase:receipt（收货，execute=收货确认）——
	PermReceiptList    = "purchase:receipt:list"
	PermReceiptRead    = "purchase:receipt:read"
	PermReceiptExecute = "purchase:receipt:execute"

	// —— purchase:putaway（上架任务，claim=领取 / execute=上架确认）——
	PermPutawayList    = "purchase:putaway:list"
	PermPutawayRead    = "purchase:putaway:read"
	PermPutawayClaim   = "purchase:putaway:claim"
	PermPutawayExecute = "purchase:putaway:execute"
	PermPutawayAssign  = "purchase:putaway:assign" // 任务优先级设置（效率层一期）

	// —— purchase:quality（质检单，execute=质检结果提交）——
	PermQualityList    = "purchase:quality:list"
	PermQualityRead    = "purchase:quality:read"
	PermQualityCreate  = "purchase:quality:create"
	PermQualityExecute = "purchase:quality:execute"

	// —— sales:sales（销售订单）——
	PermSalesList    = "sales:sales:list"
	PermSalesRead    = "sales:sales:read"
	PermSalesCreate  = "sales:sales:create"
	PermSalesUpdate  = "sales:sales:update"
	PermSalesSubmit  = "sales:sales:submit"
	PermSalesApprove = "sales:sales:approve"
	PermSalesCancel  = "sales:sales:cancel"
	PermSalesClose   = "sales:sales:close"

	// —— sales:outbound（出库单；create=生成拣货任务）——
	PermOutboundList   = "sales:outbound:list"
	PermOutboundRead   = "sales:outbound:read"
	PermOutboundCreate = "sales:outbound:create"
	PermOutboundCancel = "sales:outbound:cancel"
	PermOutboundClose  = "sales:outbound:close"

	// —— sales:allocation（库存分配，execute=重新分配）——
	PermAllocationList    = "sales:allocation:list"
	PermAllocationRead    = "sales:allocation:read"
	PermAllocationCreate  = "sales:allocation:create"
	PermAllocationExecute = "sales:allocation:execute"

	// —— sales:pick（拣货任务）——
	PermPickList    = "sales:pick:list"
	PermPickRead    = "sales:pick:read"
	PermPickClaim   = "sales:pick:claim"
	PermPickExecute = "sales:pick:execute"
	PermPickAssign  = "sales:pick:assign" // 任务优先级设置（效率层一期）

	// —— sales:check（复核任务）——
	PermCheckList    = "sales:check:list"
	PermCheckRead    = "sales:check:read"
	PermCheckClaim   = "sales:check:claim"
	PermCheckExecute = "sales:check:execute"
	PermCheckAssign  = "sales:check:assign" // 任务优先级设置（效率层一期）

	// —— sales:packing（打包）——
	PermPackingList    = "sales:packing:list"
	PermPackingRead    = "sales:packing:read"
	PermPackingExecute = "sales:packing:execute"

	// —— sales:shipment（发货）——
	PermShipmentList    = "sales:shipment:list"
	PermShipmentRead    = "sales:shipment:read"
	PermShipmentExecute = "sales:shipment:execute"

	// —— stockops:transfer（调拨；close 为清单内动作词，M2 无差额关闭态、无路由挂载）——
	PermTransferList    = "stockops:transfer:list"
	PermTransferRead    = "stockops:transfer:read"
	PermTransferCreate  = "stockops:transfer:create"
	PermTransferUpdate  = "stockops:transfer:update"
	PermTransferSubmit  = "stockops:transfer:submit"
	PermTransferApprove = "stockops:transfer:approve"
	PermTransferExecute = "stockops:transfer:execute"
	PermTransferCancel  = "stockops:transfer:cancel"
	PermTransferClose   = "stockops:transfer:close"

	// —— stockops:count（盘点；approve=差异审核通过/驳回共用资源点）——
	PermCountList    = "stockops:count:list"
	PermCountRead    = "stockops:count:read"
	PermCountCreate  = "stockops:count:create"
	PermCountExecute = "stockops:count:execute"
	PermCountApprove = "stockops:count:approve"
	PermCountCancel  = "stockops:count:cancel"
	PermCountClose   = "stockops:count:close"

	// —— stockops:adjustment（库存调整审批，§8.3 条 5 查询面随平台项交付）——
	PermAdjustmentList    = "stockops:adjustment:list"
	PermAdjustmentRead    = "stockops:adjustment:read"
	PermAdjustmentCreate  = "stockops:adjustment:create"
	PermAdjustmentApprove = "stockops:adjustment:approve"
	PermAdjustmentExecute = "stockops:adjustment:execute"
	PermAdjustmentCancel  = "stockops:adjustment:cancel"

	// —— stockops:move（仓内移库 POST /api/inventory/moves）——
	PermMoveList    = "stockops:move:list"
	PermMoveExecute = "stockops:move:execute"

	// —— inventory:lock（锁定记录查询；锁的创建/释放随产生它的业务动作授权）——
	PermLockList = "inventory:lock:list"

	// —— returns:salesreturn（销售退货）——
	PermSalesReturnList    = "returns:salesreturn:list"
	PermSalesReturnRead    = "returns:salesreturn:read"
	PermSalesReturnCreate  = "returns:salesreturn:create"
	PermSalesReturnUpdate  = "returns:salesreturn:update"
	PermSalesReturnSubmit  = "returns:salesreturn:submit"
	PermSalesReturnApprove = "returns:salesreturn:approve"
	PermSalesReturnExecute = "returns:salesreturn:execute"
	PermSalesReturnCancel  = "returns:salesreturn:cancel"
	PermSalesReturnClose   = "returns:salesreturn:close"

	// —— returns:purchasereturn（采购退货）——
	PermPurchaseReturnList    = "returns:purchasereturn:list"
	PermPurchaseReturnRead    = "returns:purchasereturn:read"
	PermPurchaseReturnCreate  = "returns:purchasereturn:create"
	PermPurchaseReturnUpdate  = "returns:purchasereturn:update"
	PermPurchaseReturnSubmit  = "returns:purchasereturn:submit"
	PermPurchaseReturnApprove = "returns:purchasereturn:approve"
	PermPurchaseReturnExecute = "returns:purchasereturn:execute"
	PermPurchaseReturnCancel  = "returns:purchasereturn:cancel"
	PermPurchaseReturnClose   = "returns:purchasereturn:close"

	// —— returns:exception（异常中心，assign=分派）——
	PermExceptionList    = "returns:exception:list"
	PermExceptionRead    = "returns:exception:read"
	PermExceptionCreate  = "returns:exception:create"
	PermExceptionAssign  = "returns:exception:assign"
	PermExceptionExecute = "returns:exception:execute"
	PermExceptionClose   = "returns:exception:close"

	// —— returns:trace（库存追溯 GET /api/inventory/trace）——
	PermTraceList = "returns:trace:list"
)

// —— M3 权限点段（backend-m3-plan §11.1 全量冻结清单，14 资源 / 41 动作点）——
//
// 归属：plan §11/§2.2 Scope I 规定 M3 权限点常量由集成工程师一次性收编至本文件
// （单一来源，seed 种子同源，seed_test.go 字面清单 + 探针测试交叉核验）；五个 M3
// 域包（datax/printing/devices/reports/sysops）内同名常量收编后改为引用本段别名
// （internal/returns/permissions.go 先例，字符串值不变，路由挂载零改动）。
// 动作词沿用 M1/M2 冻结枚举，零新增动词（plan §11.1；清单外动作词禁止发明，§2.3 判据 9）。
//
// 端点语义（§11.1）：datax:import create=上传+校验、execute=确认导入（高危二次确认
// 与审计）；datax:export create=创建导出任务、read=下载产物/错误文件；
// printing:template copy 复用 create；devices:device bind/unbind/config 复用 update、
// disable 复用 status；scanner:resolve 映射全量角色授权（扫码是全角色基础输入）；
// devices:scanlog:read 冻结导出但 M3 无独立详情端点（不虚设路由）；notifications
// 个人收件箱认证即可用，无权限点（§10.6）。
const (
	// —— datax:import（导入中心）——
	PermImportList    = "datax:import:list"
	PermImportRead    = "datax:import:read"    // 详情/预览/模板下载/错误 Excel 下载
	PermImportCreate  = "datax:import:create"  // 上传解析 + 发起校验
	PermImportExecute = "datax:import:execute" // 确认导入（高危二次确认与审计）

	// —— datax:export（导出中心）——
	PermExportList   = "datax:export:list"
	PermExportRead   = "datax:export:read" // 产物下载（permission §6 敏感操作）
	PermExportCreate = "datax:export:create"

	// —— datax:file（文件中心）——
	PermFileList   = "datax:file:list"
	PermFileRead   = "datax:file:read" // 下载/预览
	PermFileCreate = "datax:file:create"
	PermFileDelete = "datax:file:delete"

	// —— printing:template（打印模板；copy 复用 create）——
	PermPrintTemplateList   = "printing:template:list"
	PermPrintTemplateRead   = "printing:template:read"
	PermPrintTemplateCreate = "printing:template:create"
	PermPrintTemplateUpdate = "printing:template:update"
	PermPrintTemplateStatus = "printing:template:status"

	// —— printing:task（打印任务；execute=打印执行确认）——
	PermPrintTaskList    = "printing:task:list"
	PermPrintTaskRead    = "printing:task:read"
	PermPrintTaskCreate  = "printing:task:create"
	PermPrintTaskExecute = "printing:task:execute"

	// —— devices:device（设备管理；bind/unbind/config 复用 update，disable 复用 status）——
	PermDeviceList   = "devices:device:list"
	PermDeviceRead   = "devices:device:read"
	PermDeviceCreate = "devices:device:create"
	PermDeviceUpdate = "devices:device:update"
	PermDeviceStatus = "devices:device:status"

	// —— devices:scanlog（扫码日志查询，设备管理后台 devices.md §7.1）——
	PermScanLogList = "devices:scanlog:list"
	PermScanLogRead = "devices:scanlog:read" // 冻结导出，M3 无独立详情端点

	// —— scanner:resolve（统一解析 POST /api/scanner/resolve，全量角色基础输入）——
	PermScannerResolveList = "scanner:resolve:list"

	// —— reports:report（报表与智能建议）——
	PermReportList = "reports:report:list"
	PermReportRead = "reports:report:read"

	// —— system:log（操作/登录日志查询，audit 只读）——
	PermSystemLogList = "system:log:list"
	PermSystemLogRead = "system:log:read"

	// —— system:job（定时任务管理；status=启停热更新）——
	PermSystemJobList   = "system:job:list"
	PermSystemJobRead   = "system:job:read"
	PermSystemJobStatus = "system:job:status"

	// —— system:config（系统配置；update=敏感操作逐项审计）——
	PermSystemConfigList   = "system:config:list"
	PermSystemConfigUpdate = "system:config:update"

	// —— system:monitor（系统监控，只读平台端点）——
	PermSystemMonitorList = "system:monitor:list"

	// —— system:backup（备份记录；create=手动触发登记 REQUESTED，裁决②混合模式）——
	PermSystemBackupList   = "system:backup:list"
	PermSystemBackupRead   = "system:backup:read"
	PermSystemBackupCreate = "system:backup:create"
)

// SuperAdminRoleCode 超级管理员角色编码（seed 内置角色）：绕过 RBAC 判定但仅此一项，
// 数据权限仍按 users.data_scope（恒 ALL）。
const SuperAdminRoleCode = "super_admin"
