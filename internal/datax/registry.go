package datax

// 模块注册表（M3 冻结全量，backend-m3-plan §6.1：导入 9 类 + 导出 16 模块；
// 值集与 db/migrations/000011_create_datax_tables.up.sql 的 CHECK 约束同源，
// 与 web/src/api/data.ts IMPORT_TYPE_OPTIONS / EXPORT_MODULE_OPTIONS 先行契约逐字对齐）。

// 导入类型九值（excel.md §1.1 全量）。
const (
	ImportProduct          = "PRODUCT"
	ImportSKU              = "SKU"
	ImportSupplier         = "SUPPLIER"
	ImportCustomer         = "CUSTOMER"
	ImportWarehouse        = "WAREHOUSE"
	ImportLocation         = "LOCATION"
	ImportPurchaseOrder    = "PURCHASE_ORDER"
	ImportSalesOrder       = "SALES_ORDER"
	ImportInitialInventory = "INITIAL_INVENTORY"
)

// 导出模块十六值（excel.md §2.1 全量；REPORT 行源归 MT4 报表域）。
const (
	ModuleProduct         = "PRODUCT"
	ModuleSKU             = "SKU"
	ModuleSupplier        = "SUPPLIER"
	ModuleCustomer        = "CUSTOMER"
	ModuleWarehouse       = "WAREHOUSE"
	ModuleLocation        = "LOCATION"
	ModulePurchaseOrder   = "PURCHASE_ORDER"
	ModulePurchaseInbound = "PURCHASE_INBOUND"
	ModuleQuality         = "QUALITY"
	ModuleSalesOutbound   = "SALES_OUTBOUND"
	ModuleInventory       = "INVENTORY"
	ModuleInventoryLedger = "INVENTORY_LEDGER"
	ModuleTransfer        = "TRANSFER"
	ModuleCount           = "COUNT"
	ModuleException       = "EXCEPTION"
	ModuleReport          = "REPORT"
)

// importTypes 冻结顺序（模板清单/前端选项顺序同源）。
var importTypes = []string{
	ImportProduct, ImportSKU, ImportSupplier, ImportCustomer,
	ImportWarehouse, ImportLocation, ImportPurchaseOrder, ImportSalesOrder, ImportInitialInventory,
}

// exportModules 冻结顺序。
var exportModules = []string{
	ModuleProduct, ModuleSKU, ModuleSupplier, ModuleCustomer,
	ModuleWarehouse, ModuleLocation, ModulePurchaseOrder, ModulePurchaseInbound,
	ModuleQuality, ModuleSalesOutbound, ModuleInventory, ModuleInventoryLedger,
	ModuleTransfer, ModuleCount, ModuleException, ModuleReport,
}

// importLabels 导入类型展示名（任务列表 moduleName 下发；与前端 data.ts 文案一致）。
var importLabels = map[string]string{
	ImportProduct:          "商品导入",
	ImportSKU:              "SKU 导入",
	ImportSupplier:         "供应商导入",
	ImportCustomer:         "客户导入",
	ImportWarehouse:        "仓库导入",
	ImportLocation:         "库位导入",
	ImportPurchaseOrder:    "采购订单导入",
	ImportSalesOrder:       "销售订单导入",
	ImportInitialInventory: "初始化库存导入",
}

// exportLabels 导出模块展示名。
var exportLabels = map[string]string{
	ModuleProduct:         "商品",
	ModuleSKU:             "SKU",
	ModuleSupplier:        "供应商",
	ModuleCustomer:        "客户",
	ModuleWarehouse:       "仓库",
	ModuleLocation:        "库位",
	ModulePurchaseOrder:   "采购订单",
	ModulePurchaseInbound: "入库单",
	ModuleQuality:         "质检单",
	ModuleSalesOutbound:   "出库单",
	ModuleInventory:       "库存",
	ModuleInventoryLedger: "库存流水",
	ModuleTransfer:        "调拨单",
	ModuleCount:           "盘点单",
	ModuleException:       "异常单",
	ModuleReport:          "报表",
}

// ImportTypes 返回冻结导入类型清单（只读副本，防调用方改写注册表）。
func ImportTypes() []string {
	out := make([]string, len(importTypes))
	copy(out, importTypes)
	return out
}

// ExportModules 返回冻结导出模块清单（只读副本）。
func ExportModules() []string {
	out := make([]string, len(exportModules))
	copy(out, exportModules)
	return out
}

// IsValidImportType 值域校验（000011 CHECK 同源）。
func IsValidImportType(t string) bool {
	for _, v := range importTypes {
		if v == t {
			return true
		}
	}
	return false
}

// IsValidExportModule 值域校验（000011 CHECK 同源）。
func IsValidExportModule(m string) bool {
	for _, v := range exportModules {
		if v == m {
			return true
		}
	}
	return false
}

// ImportTypeLabel 导入类型展示名（导入任务 moduleName 专用——PRODUCT 等编码在
// 导入/导出两侧展示名不同："商品导入" vs "商品"）。
func ImportTypeLabel(t string) string {
	if l, ok := importLabels[t]; ok {
		return l
	}
	return t
}

// ExportModuleLabel 导出模块展示名（导出任务/产物文件名专用）。
func ExportModuleLabel(m string) string {
	if l, ok := exportLabels[m]; ok {
		return l
	}
	return m
}

// ModuleLabel 模块展示名（通用回退：导出侧优先——文件中心 module 展示；
// 未注册值原样返回，任务表历史值不做二次加工）。
func ModuleLabel(m string) string {
	if l, ok := exportLabels[m]; ok {
		return l
	}
	if l, ok := importLabels[m]; ok {
		return l
	}
	return m
}

// IsHighRiskImport 初始化库存导入为高危操作（excel §6.1：必须二次确认；与前端
// data.ts isHighRiskImport 同一判定，单一语义两端口径一致）。
func IsHighRiskImport(importType string) bool {
	return importType == ImportInitialInventory
}
