package database_test

// M3 权限点收编探针（backend-m3-plan §11 收编模式；MT6 集成工程师落位）。
//
// 断言 internal/auth 权限常量与 internal/database 种子清单同源：
//   - 全量 auth.Perm* 常量（M1/M2/M3 段）必须都在种子权限点中——常量收编漏种/值漂移即失败；
//   - M3 段 41 个动作点逐字双向核对（常量值 == 种子编码，清单外编码由 seed_test.go
//     字面清单负责排除，本文件负责"常量侧不漂移"）。
//
// 依赖说明：探针须同时引用 auth 常量与 database 种子数据，而 auth import database
// （bootstrap 委托），故落位为 database 的外部测试包（database_test）——in-package
// 测试引用 auth 会构成测试导入环。零数据库依赖（种子清单为纯函数）。

import (
	"testing"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
)

// m3PermConstants M3 段权限常量 → 冻结清单字面值（backend-m3-plan §11.1）。
// 字面值与 auth 常量分别独立书写：常量被误改时本表立即暴露。
var m3PermConstants = map[string]string{
	auth.PermImportList:    "datax:import:list",
	auth.PermImportRead:    "datax:import:read",
	auth.PermImportCreate:  "datax:import:create",
	auth.PermImportExecute: "datax:import:execute",
	auth.PermExportList:    "datax:export:list",
	auth.PermExportRead:    "datax:export:read",
	auth.PermExportCreate:  "datax:export:create",
	auth.PermFileList:      "datax:file:list",
	auth.PermFileRead:      "datax:file:read",
	auth.PermFileCreate:    "datax:file:create",
	auth.PermFileDelete:    "datax:file:delete",

	auth.PermPrintTemplateList:   "printing:template:list",
	auth.PermPrintTemplateRead:   "printing:template:read",
	auth.PermPrintTemplateCreate: "printing:template:create",
	auth.PermPrintTemplateUpdate: "printing:template:update",
	auth.PermPrintTemplateStatus: "printing:template:status",
	auth.PermPrintTaskList:       "printing:task:list",
	auth.PermPrintTaskRead:       "printing:task:read",
	auth.PermPrintTaskCreate:     "printing:task:create",
	auth.PermPrintTaskExecute:    "printing:task:execute",

	auth.PermDeviceList:         "devices:device:list",
	auth.PermDeviceRead:         "devices:device:read",
	auth.PermDeviceCreate:       "devices:device:create",
	auth.PermDeviceUpdate:       "devices:device:update",
	auth.PermDeviceStatus:       "devices:device:status",
	auth.PermScanLogList:        "devices:scanlog:list",
	auth.PermScanLogRead:        "devices:scanlog:read",
	auth.PermScannerResolveList: "scanner:resolve:list",

	auth.PermReportList: "reports:report:list",
	auth.PermReportRead: "reports:report:read",

	auth.PermSystemLogList:      "system:log:list",
	auth.PermSystemLogRead:      "system:log:read",
	auth.PermSystemJobList:      "system:job:list",
	auth.PermSystemJobRead:      "system:job:read",
	auth.PermSystemJobStatus:    "system:job:status",
	auth.PermSystemConfigList:   "system:config:list",
	auth.PermSystemConfigUpdate: "system:config:update",
	auth.PermSystemMonitorList:  "system:monitor:list",

	auth.PermSystemBackupList:   "system:backup:list",
	auth.PermSystemBackupRead:   "system:backup:read",
	auth.PermSystemBackupCreate: "system:backup:create",
}

// TestM3PermissionConstantsMatchSeeds 探针：auth M3 常量值 == 种子编码（双向），
// 且全量 auth.Perm* 常量均已被种子收录。
func TestM3PermissionConstantsMatchSeeds(t *testing.T) {
	seedSet := map[string]bool{}
	for _, c := range database.FrozenSeedPermissionCodes() {
		seedSet[c] = true
	}

	// 常量 → 字面值：值漂移即失败（收编模式第一道防线）。
	for got, want := range m3PermConstants {
		if got != want {
			t.Fatalf("auth 权限常量值漂移：应为 %q，实际 %q", want, got)
		}
	}
	// 常量 → 种子：常量存在而种子缺失即失败（收编漏种防线）。
	for code := range m3PermConstants {
		if !seedSet[code] {
			t.Fatalf("auth 常量 %q 未被种子收录（seed.go permResources 缺失）", code)
		}
	}

	// 全量 auth 权限常量（M1/M2/M3 段）均须在种子中——防后续里程碑重演漏种。
	allConstants := []string{
		// M1（backend-m1-plan §5.4.1）
		auth.PermUserList, auth.PermUserRead, auth.PermUserCreate, auth.PermUserUpdate, auth.PermUserStatus,
		auth.PermUserAssignRole, auth.PermUserResetPassword, auth.PermUserUnlock,
		auth.PermRoleList, auth.PermRoleRead, auth.PermRoleCreate, auth.PermRoleUpdate, auth.PermRoleStatus,
		auth.PermRoleAssignPermission,
		auth.PermPermissionList,
		auth.PermDeptList, auth.PermDeptRead, auth.PermDeptCreate, auth.PermDeptUpdate, auth.PermDeptStatus,
		auth.PermSessionList, auth.PermSessionKick,
		auth.PermProductList, auth.PermProductRead, auth.PermProductCreate, auth.PermProductUpdate, auth.PermProductDelete, auth.PermProductStatus,
		auth.PermSKUList, auth.PermSKURead, auth.PermSKUCreate, auth.PermSKUUpdate, auth.PermSKUDelete, auth.PermSKUStatus,
		auth.PermCategoryList, auth.PermCategoryRead, auth.PermCategoryCreate, auth.PermCategoryUpdate, auth.PermCategoryStatus,
		auth.PermUnitList, auth.PermUnitRead, auth.PermUnitCreate, auth.PermUnitUpdate, auth.PermUnitStatus,
		auth.PermSupplierList, auth.PermSupplierRead, auth.PermSupplierCreate, auth.PermSupplierUpdate, auth.PermSupplierDelete, auth.PermSupplierStatus,
		auth.PermCustomerList, auth.PermCustomerRead, auth.PermCustomerCreate, auth.PermCustomerUpdate, auth.PermCustomerDelete, auth.PermCustomerStatus,
		auth.PermWarehouseList, auth.PermWarehouseRead, auth.PermWarehouseCreate, auth.PermWarehouseUpdate, auth.PermWarehouseDelete, auth.PermWarehouseStatus,
		auth.PermZoneList, auth.PermZoneRead, auth.PermZoneCreate, auth.PermZoneUpdate, auth.PermZoneStatus,
		auth.PermShelfList, auth.PermShelfRead, auth.PermShelfCreate, auth.PermShelfUpdate, auth.PermShelfStatus,
		auth.PermBinList, auth.PermBinRead, auth.PermBinCreate, auth.PermBinUpdate, auth.PermBinDelete, auth.PermBinStatus,
		auth.PermInventoryList, auth.PermLedgerList, auth.PermBatchList, auth.PermSerialList,
		// M2（backend-m2-plan §9.2）
		auth.PermPurchaseList, auth.PermPurchaseRead, auth.PermPurchaseCreate, auth.PermPurchaseUpdate,
		auth.PermPurchaseSubmit, auth.PermPurchaseApprove, auth.PermPurchaseCancel, auth.PermPurchaseClose,
		auth.PermInboundList, auth.PermInboundRead, auth.PermInboundCreate, auth.PermInboundUpdate,
		auth.PermInboundCancel, auth.PermInboundClose,
		auth.PermReceiptList, auth.PermReceiptRead, auth.PermReceiptExecute,
		auth.PermPutawayList, auth.PermPutawayRead, auth.PermPutawayClaim, auth.PermPutawayExecute,
		auth.PermQualityList, auth.PermQualityRead, auth.PermQualityCreate, auth.PermQualityExecute,
		auth.PermSalesList, auth.PermSalesRead, auth.PermSalesCreate, auth.PermSalesUpdate,
		auth.PermSalesSubmit, auth.PermSalesApprove, auth.PermSalesCancel, auth.PermSalesClose,
		auth.PermOutboundList, auth.PermOutboundRead, auth.PermOutboundCreate, auth.PermOutboundCancel, auth.PermOutboundClose,
		auth.PermAllocationList, auth.PermAllocationRead, auth.PermAllocationCreate, auth.PermAllocationExecute,
		auth.PermPickList, auth.PermPickRead, auth.PermPickClaim, auth.PermPickExecute,
		auth.PermCheckList, auth.PermCheckRead, auth.PermCheckClaim, auth.PermCheckExecute,
		auth.PermPackingList, auth.PermPackingRead, auth.PermPackingExecute,
		auth.PermShipmentList, auth.PermShipmentRead, auth.PermShipmentExecute,
		auth.PermTransferList, auth.PermTransferRead, auth.PermTransferCreate, auth.PermTransferUpdate,
		auth.PermTransferSubmit, auth.PermTransferApprove, auth.PermTransferExecute, auth.PermTransferCancel, auth.PermTransferClose,
		auth.PermCountList, auth.PermCountRead, auth.PermCountCreate, auth.PermCountExecute, auth.PermCountApprove,
		auth.PermCountCancel, auth.PermCountClose,
		auth.PermAdjustmentList, auth.PermAdjustmentRead, auth.PermAdjustmentCreate, auth.PermAdjustmentApprove,
		auth.PermAdjustmentExecute, auth.PermAdjustmentCancel,
		auth.PermMoveList, auth.PermMoveExecute,
		auth.PermLockList,
		auth.PermSalesReturnList, auth.PermSalesReturnRead, auth.PermSalesReturnCreate, auth.PermSalesReturnUpdate,
		auth.PermSalesReturnSubmit, auth.PermSalesReturnApprove, auth.PermSalesReturnExecute, auth.PermSalesReturnCancel, auth.PermSalesReturnClose,
		auth.PermPurchaseReturnList, auth.PermPurchaseReturnRead, auth.PermPurchaseReturnCreate, auth.PermPurchaseReturnUpdate,
		auth.PermPurchaseReturnSubmit, auth.PermPurchaseReturnApprove, auth.PermPurchaseReturnExecute, auth.PermPurchaseReturnCancel, auth.PermPurchaseReturnClose,
		auth.PermExceptionList, auth.PermExceptionRead, auth.PermExceptionCreate, auth.PermExceptionAssign,
		auth.PermExceptionExecute, auth.PermExceptionClose,
		auth.PermTraceList,
	}
	for _, c := range allConstants {
		if !seedSet[c] {
			t.Fatalf("auth 常量 %q 未被种子收录（seed.go permResources 缺失）", c)
		}
	}
}
