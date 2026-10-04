package stockops

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// stockops 域错误码（architecture.md §2 模块命名空间 STOCKOPS_*；api.md §4 校验失败
// 必须带 details）。统一经 internal/response 注册与输出，禁止 handler 直接 c.JSON。
var (
	// ErrTransferNotFound 调拨单不存在（或不在当前用户数据权限范围内——fail-closed）。
	ErrTransferNotFound = response.Register("STOCKOPS_TRANSFER_NOT_FOUND", "调拨单不存在", http.StatusNotFound)
	// ErrCountNotFound 盘点单不存在（或不在当前用户数据权限范围内）。
	ErrCountNotFound = response.Register("STOCKOPS_COUNT_NOT_FOUND", "盘点单不存在", http.StatusNotFound)
	// ErrStatusConflict 单据状态冲突：当前状态不允许该操作（business-flow §13.2 状态机守卫；
	// 方案 §6"影响行数 0 = 状态冲突，各域注册域内码"）。
	ErrStatusConflict = response.Register("STOCKOPS_STATUS_CONFLICT", "单据状态不允许该操作", http.StatusConflict)
	// ErrTransferLineInvalid 调拨明细非法（行号重复/数量非正/维度缺失/两端仓库与单据不符等）。
	ErrTransferLineInvalid = response.Register("STOCKOPS_TRANSFER_LINE_INVALID", "调拨明细非法", http.StatusBadRequest)
	// ErrTransferCancelForbidden 调拨出库后禁止直接取消（business-flow §13.3：只能反向单据冲正）。
	ErrTransferCancelForbidden = response.Register("STOCKOPS_TRANSFER_CANCEL_FORBIDDEN", "调拨已出库，禁止直接取消（请创建反向调拨单冲正）", http.StatusConflict)
	// ErrLockMissing 调拨预占锁缺失或已核销（出库前锁定记录必须就位）。
	ErrLockMissing = response.Register("STOCKOPS_LOCK_MISSING", "调拨预占锁定记录缺失或已完结", http.StatusConflict)
	// ErrSerialQtyInvalid 序列号管理 SKU 的数量必须为正整数（逐件作业，inventory-rules §8.2）。
	ErrSerialQtyInvalid = response.Register("STOCKOPS_SERIAL_QTY_INVALID", "序列号管理 SKU 数量必须为正整数", http.StatusBadRequest)
	// ErrSerialShortage 序列号件数不足（源库位在库件数少于出库需求 / 到货回件数与出库不一致）。
	ErrSerialShortage = response.Register("STOCKOPS_SERIAL_SHORTAGE", "序列号件数不足或不一致", http.StatusConflict)
	// ErrSerialLineDup 序列号管理 SKU 同单多行（逐件追溯要求单 SKU 单行）。
	ErrSerialLineDup = response.Register("STOCKOPS_SERIAL_LINE_DUP", "序列号管理 SKU 在单据中必须仅占一行", http.StatusBadRequest)
	// ErrScopeEmpty 盘点范围为空（DRAFT→COUNTING 守卫：范围内无库存行，方案 §6.7）。
	ErrScopeEmpty = response.Register("STOCKOPS_SCOPE_EMPTY", "盘点范围内没有库存行", http.StatusBadRequest)
	// ErrScopeInvalid 盘点范围声明非法（mode 值域/ID 非正/重复）。
	ErrScopeInvalid = response.Register("STOCKOPS_SCOPE_INVALID", "盘点范围声明非法", http.StatusBadRequest)
	// ErrScopeFrozen 盘点范围已被其他盘点单冻结（architecture §5：多人同范围盘点互斥）。
	ErrScopeFrozen = response.Register("STOCKOPS_SCOPE_FROZEN", "盘点范围已被其他盘点单冻结", http.StatusConflict)
	// ErrCountingIncomplete 实盘未完成（存在未登记明细，禁止生成差异——登记 0 亦是显式登记）。
	ErrCountingIncomplete = response.Register("STOCKOPS_COUNTING_INCOMPLETE", "存在未登记的盘点明细（实盘为 0 也必须显式登记）", http.StatusBadRequest)
	// ErrCountItemForeign 登记行不属于该盘点单范围（防止越范围改数）。
	ErrCountItemForeign = response.Register("STOCKOPS_COUNT_ITEM_FOREIGN", "盘点明细不属于该盘点单", http.StatusBadRequest)
	// ErrCountQtyInvalid 实盘登记数量非法（序列号行仅 0/1；普通行 >=0）。
	ErrCountQtyInvalid = response.Register("STOCKOPS_COUNT_QTY_INVALID", "实盘登记数量非法", http.StatusBadRequest)
	// ErrSerialSKUMismatch 登记的序列号已存在且不属于该 SKU（建档校验前置到登记时点）。
	ErrSerialSKUMismatch = response.Register("STOCKOPS_SERIAL_SKU_MISMATCH", "序列号已存在且不属于该 SKU", http.StatusConflict)
	// ErrReaderMissing 跨域只读/校验服务未装配（plan §4.3 规则①：fail-closed，
	// 杜绝静默跳过 SKU 序列号开关等业务分支）。
	ErrReaderMissing = response.Register("STOCKOPS_READER_MISSING", "跨域校验/读取服务未装配（SKU 开关读取缺位）", http.StatusInternalServerError)
	// ErrMoveKeyInvalid 仓内移库五维键非法（POST /api/inventory/moves，plan §8.3 条 5）。
	ErrMoveKeyInvalid = response.Register("STOCKOPS_MOVE_KEY_INVALID", "移库 from/to 定位键非法", http.StatusBadRequest)
	// ErrMoveQtyInvalid 仓内移库数量非法。
	ErrMoveQtyInvalid = response.Register("STOCKOPS_MOVE_QTY_INVALID", "移库数量非法（numeric(18,4) 正数）", http.StatusBadRequest)
	// ErrMoveSourceRequired 仓内移库缺作业依据号（inventory-rules §5 来源追溯）。
	ErrMoveSourceRequired = response.Register("STOCKOPS_MOVE_SOURCE_REQUIRED", "移库必须携带作业依据号", http.StatusBadRequest)
	// ErrMoveFailed 仓内移库执行失败（原语透传的包装入口；实际错误码经原语上抛）。
	ErrMoveFailed = response.Register("STOCKOPS_MOVE_FAILED", "仓内移库执行失败", http.StatusConflict)
)
