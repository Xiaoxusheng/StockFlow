package sales

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// sales 域错误码（architecture.md §2 模块命名空间 SALES_*；api.md §4 校验失败必须带
// details）。统一经 internal/response 注册，禁止 handler 直接 c.JSON（plan §2.3 判据 2）。
// 库存不足不在此重复注册：审核预占/重新分配的缺口一律透出 inventory 原语的
// INVENTORY_NOT_ENOUGH（plan §6.4"返回 INVENTORY_NOT_ENOUGH"），本域只补充行级 details。
var (
	// ErrOrderNotFound 销售订单不存在（或不在当前用户数据权限范围内）。
	ErrOrderNotFound = response.Register("SALES_ORDER_NOT_FOUND", "销售订单不存在", http.StatusNotFound)
	// ErrOutboundNotFound 出库单不存在（或不在当前用户数据权限范围内）。
	ErrOutboundNotFound = response.Register("SALES_OUTBOUND_NOT_FOUND", "出库单不存在", http.StatusNotFound)
	// ErrTaskNotFound 作业任务（拣货/复核）不存在。
	ErrTaskNotFound = response.Register("SALES_TASK_NOT_FOUND", "作业任务不存在", http.StatusNotFound)

	// ErrCustomerNotFound 客户不存在或已停用（api.md §4 业务关系校验）。
	ErrCustomerNotFound = response.Register("SALES_CUSTOMER_NOT_FOUND", "客户不存在或已停用", http.StatusBadRequest)
	// ErrSKUNotFound SKU 不存在或已停用（api.md §4 业务关系校验）。
	ErrSKUNotFound = response.Register("SALES_SKU_NOT_FOUND", "SKU 不存在或已停用", http.StatusBadRequest)
	// ErrItemInvalid 订单明细非法（数量/单价/行号重复等）。
	ErrItemInvalid = response.Register("SALES_ITEM_INVALID", "销售订单明细非法", http.StatusBadRequest)
	// ErrCloseReasonRequired 差额关闭必须填写原因（business-flow §13.3 关闭留痕）。
	ErrCloseReasonRequired = response.Register("SALES_CLOSE_REASON_REQUIRED", "差额关闭必须填写原因", http.StatusBadRequest)

	// ErrStateConflict 单据状态机冲突（plan §6 统一实现形态：守卫 UPDATE 影响 0 行
	// = COMMON_CONFLICT 语义的域内码）。
	ErrStateConflict = response.Register("SALES_STATE_CONFLICT", "单据状态冲突，请刷新后重试", http.StatusConflict)
	// ErrClaimConflict 任务领取/指派冲突（architecture §5.2：原子抢占 0 行 = 并发冲突）。
	ErrClaimConflict = response.Register("SALES_CLAIM_CONFLICT", "任务已被他人领取", http.StatusConflict)

	// ErrNothingToShip 无可发货明细（发货量必须来自已打包未发货数量）。
	ErrNothingToShip = response.Register("SALES_NOTHING_TO_SHIP", "没有可发货的明细", http.StatusConflict)
	// ErrShipQtyInvalid 发货明细非法（行号不存在 / 超出可发数量 / 重复行）。
	ErrShipQtyInvalid = response.Register("SALES_SHIP_QTY_INVALID", "发货明细非法", http.StatusBadRequest)
	// ErrSerialStateInvalid 序列号不可用（不存在 / SKU 不符 / 不在可出库状态）。
	ErrSerialStateInvalid = response.Register("SALES_SERIAL_STATE_INVALID", "序列号不在可出库状态", http.StatusConflict)
	// ErrSerialsMissing 序列号 SKU 发货时序列号件数与发货数量不符。
	ErrSerialsMissing = response.Register("SALES_SERIALS_MISSING", "序列号数量与发货数量不符", http.StatusConflict)

	// ErrPackExceed 打包数量超出已复核（可打包）数量。
	ErrPackExceed = response.Register("SALES_PACK_EXCEED", "打包数量超出可打包数量", http.StatusBadRequest)
	// ErrPickExceed 拣货确认数量超出任务量。
	ErrPickExceed = response.Register("SALES_PICK_EXCEED", "拣货数量超出任务数量", http.StatusBadRequest)

	// ErrReallocForbidden 重新分配被拒绝（已拣货/已发货行不可重分配）。
	ErrReallocForbidden = response.Register("SALES_REALLOC_FORBIDDEN", "该行已拣货或已发货，不可重新分配", http.StatusConflict)
	// ErrCheckerMissing 跨域校验/库存网关未装配（plan §3.1 规则①：fail-closed，
	// 杜绝静默跳过业务校验或库存动作缺位）。
	ErrCheckerMissing = response.Register("SALES_CHECKER_MISSING", "销售域跨域服务未装配（库存网关/客户/SKU 开关校验缺位）", http.StatusInternalServerError)
	// ErrExceptionsNotWired 异常中心创建接口未装配（returns 域 MT4 交付后由 router 注入；
	// fail-closed：异常上报在装配缺位时拒绝，不静默吞掉异常）。
	ErrExceptionsNotWired = response.Register("SALES_EXCEPTIONS_NOT_WIRED", "异常中心服务未装配，无法登记异常", http.StatusServiceUnavailable)
)
