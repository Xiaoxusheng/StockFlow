package inventory

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// inventory 域错误码（architecture.md §2 模块命名空间 INVENTORY_*；api.md §4 校验失败
// 必须带 details，如 INVENTORY_NOT_ENOUGH 的 details 携带具体 SKU/库位/需求量与可用量，
// plan §8.3）。统一经 internal/response 注册与输出，禁止 handler 直接 c.JSON
// （plan §4.2 判据 2）。
var (
	// ErrNotEnough 库存不足（可用/锁定/待检等受影响列数量不满足本次变更）。
	ErrNotEnough = response.Register("INVENTORY_NOT_ENOUGH", "库存不足", http.StatusConflict)
	// ErrRecordNotFound 库存行不存在（五维键无匹配行）。
	ErrRecordNotFound = response.Register("INVENTORY_RECORD_NOT_FOUND", "库存记录不存在", http.StatusNotFound)
	// ErrLockNotFound 锁定记录不存在或已释放/核销。
	ErrLockNotFound = response.Register("INVENTORY_LOCK_NOT_FOUND", "库存锁定记录不存在或已完结", http.StatusNotFound)
	// ErrBatchNotFound 批次不存在。
	ErrBatchNotFound = response.Register("INVENTORY_BATCH_NOT_FOUND", "批次不存在", http.StatusNotFound)
	// ErrSerialNotFound 序列号不存在。
	ErrSerialNotFound = response.Register("INVENTORY_SERIAL_NOT_FOUND", "序列号不存在", http.StatusNotFound)
	// ErrSerialSKUMismatch 序列号已存在但归属 SKU 不一致（序列号全局唯一，inventory-rules §8）。
	ErrSerialSKUMismatch = response.Register("INVENTORY_SERIAL_SKU_MISMATCH", "序列号已存在且不属于该 SKU", http.StatusConflict)

	// ErrQtyInvalid 数量非法（≤0 / 小数位超限等）。
	ErrQtyInvalid = response.Register("INVENTORY_QTY_INVALID", "库存数量非法", http.StatusBadRequest)
	// ErrSourceRequired 来源单据缺失（inventory-rules §5：流水必须追溯来源单据）。
	ErrSourceRequired = response.Register("INVENTORY_SOURCE_REQUIRED", "缺少来源单据类型或单号", http.StatusBadRequest)
	// ErrAdjustTypeInvalid 调整类型非法（business-flow §11.1 值域：盘盈/盘亏/损耗/报废/其他）。
	ErrAdjustTypeInvalid = response.Register("INVENTORY_ADJUST_TYPE_INVALID", "库存调整类型非法", http.StatusBadRequest)
	// ErrAdjustReasonRequired 调整原因必填（business-flow §11.1）。
	ErrAdjustReasonRequired = response.Register("INVENTORY_ADJUST_REASON_REQUIRED", "库存调整必须填写原因", http.StatusBadRequest)
	// ErrLockTypeInvalid 锁定类型非法（inventory-rules §4 五类）。
	ErrLockTypeInvalid = response.Register("INVENTORY_LOCK_TYPE_INVALID", "库存锁定类型非法", http.StatusBadRequest)
	// ErrSerialStatusInvalid 序列号状态非法（迁移 chk_serial_numbers_status 值域）。
	ErrSerialStatusInvalid = response.Register("INVENTORY_SERIAL_STATUS_INVALID", "序列号状态非法", http.StatusBadRequest)
	// ErrMoveCrossWarehouse 仓内移库不允许跨仓（跨仓属调拨域，M2）。
	ErrMoveCrossWarehouse = response.Register("INVENTORY_MOVE_CROSS_WAREHOUSE", "仓内移库不允许跨仓库或变更 SKU/批次", http.StatusBadRequest)
	// ErrMoveSameLocation 移库源库位与目标库位相同。
	ErrMoveSameLocation = response.Register("INVENTORY_MOVE_SAME_LOCATION", "移库源库位与目标库位相同", http.StatusBadRequest)
	// ErrCheckerMissing 跨域校验服务未装配（plan §4.3 规则①：杜绝静默跳过业务校验——
	// fail-closed：行创建类变更在 Checker 缺位时拒绝执行，而不是放行）。
	ErrCheckerMissing = response.Register("INVENTORY_CHECKER_MISSING", "库存跨域校验服务未装配（SKU/库位存在性校验缺位）", http.StatusInternalServerError)
	// ErrSKUNotFound SKU 不存在或已停用（api.md §4 业务关系校验，经 §4.3 注入的 SKUChecker）。
	ErrSKUNotFound = response.Register("INVENTORY_SKU_NOT_FOUND", "SKU 不存在或已停用", http.StatusBadRequest)
	// ErrBinNotFound 库位不存在/已停用/不属于该仓库（经 §4.3 注入的 BinChecker）。
	ErrBinNotFound = response.Register("INVENTORY_BIN_NOT_FOUND", "库位不存在、已停用或不属于该仓库", http.StatusBadRequest)
	// ErrLedgerNumberConflict 流水号生成冲突（重试后仍碰撞，理论上仅随机后缀碰撞时出现）。
	ErrLedgerNumberConflict = response.Register("INVENTORY_LEDGER_NUMBER_CONFLICT", "流水号生成冲突，请重试", http.StatusConflict)
	// ErrRowCountFrozen 库存行处于盘点冻结期（存在 ACTIVE COUNT_FREEZE）：改变行 total
	// 的原语（上架/出库扣减/调整/移库）拒绝执行——冻结期间行总量必须恒定，盘点差异
	// 才能以冻结快照为准（business-flow §10.2"冻结范围"、plan §6.7；M2 修复轮补齐）。
	ErrRowCountFrozen = response.Register("INVENTORY_ROW_COUNT_FROZEN", "库存行盘点冻结中，暂不可执行该库存变更", http.StatusConflict)
	// ErrWarehouseScopeDenied 目标仓库不在操作者数据权限范围内（permission.md §4
	// fail-closed；期初库存导入写入器按 Actor 仓库范围快照拒绝跨仓行——plan §13.6）。
	ErrWarehouseScopeDenied = response.Register("INVENTORY_WAREHOUSE_SCOPE_DENIED", "目标仓库不在数据权限范围内", http.StatusForbidden)
)
