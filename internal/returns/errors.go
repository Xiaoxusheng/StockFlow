package returns

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// returns 域错误码（architecture.md §2 模块命名空间 RETURNS_*；api.md §4 校验失败必须带
// details）。统一经 internal/response 注册，禁止 handler 直接 c.JSON（plan §4.2 判据 2）。
var (
	// ErrReturnNotFound 退货单不存在（或不在当前用户数据权限范围内）。
	ErrReturnNotFound = response.Register("RETURNS_RETURN_NOT_FOUND", "退货单不存在", http.StatusNotFound)
	// ErrStatusConflict 退货单状态冲突（状态机守卫 UPDATE 影响行数 0——COMMON_CONFLICT 域内码，plan §6）。
	ErrStatusConflict = response.Register("RETURNS_STATUS_CONFLICT", "退货单状态冲突", http.StatusConflict)
	// ErrLineNotFound 退货明细不存在（或与单据/来源单不匹配）。
	ErrLineNotFound = response.Register("RETURNS_LINE_NOT_FOUND", "退货明细不存在", http.StatusNotFound)
	// ErrQtyInvalid 退货数量非法（≤0 / 小数位超限等）。
	ErrQtyInvalid = response.Register("RETURNS_QTY_INVALID", "退货数量非法", http.StatusBadRequest)
	// ErrQtyExceeded 数量超限（超量收货/超量质检/超可退量创建——business-flow §2.3 同族约束，
	// details 携带行号、需求量与上限，plan §6.1 超量 4xx 口径）。
	ErrQtyExceeded = response.Register("RETURNS_QTY_EXCEEDED", "退货数量超出可退/可收范围", http.StatusConflict)
	// ErrSourceOrderNotFound 来源单据不存在、不可用或不属于该退货类型（api.md §4 业务关系校验，
	// 经 §3.1 SalesOrderReader/PurchaseOrderReader 窄接口校验）。
	ErrSourceOrderNotFound = response.Register("RETURNS_SOURCE_ORDER_NOT_FOUND", "来源单据不存在或不可退", http.StatusBadRequest)
	// ErrSourceMismatch 来源单据与请求不匹配（仓库/明细行/SKU 归属不一致）。
	ErrSourceMismatch = response.Register("RETURNS_SOURCE_MISMATCH", "来源单据与退货请求不匹配", http.StatusBadRequest)
	// ErrSerialMismatch 序列号采集与数量不匹配（逐件采集数 ≠ 数量）或序列号重复。
	ErrSerialMismatch = response.Register("RETURNS_SERIAL_MISMATCH", "序列号采集与数量不匹配", http.StatusBadRequest)
	// ErrSerialStateInvalid 序列号台账状态不允许出库核销（不存在 / 不属于该 SKU /
	// 非 IN_STOCK / 不在指定库位——inventory-rules §8.2 出库逐件校验，修复轮随
	// 采购退货序列号核销补齐）。
	ErrSerialStateInvalid = response.Register("RETURNS_SERIAL_STATE_INVALID", "序列号不在可出库状态", http.StatusConflict)
	// ErrIdempotencyInvalid 幂等键非法（超长等；最终准绳是 inventory_ledgers.idempotency_key，
	// plan §5 幂等口径）。
	ErrIdempotencyInvalid = response.Register("RETURNS_IDEMPOTENCY_INVALID", "幂等键非法", http.StatusBadRequest)
	// ErrPartialReplay 多行请求的部分行命中幂等重放（真实重试必然整单重放；部分重放视为
	// 矛盾请求，整体回滚——见 package doc 幂等小节）。
	ErrPartialReplay = response.Register("RETURNS_PARTIAL_REPLAY", "请求部分行已执行过，整单重试或核对后重新提交", http.StatusConflict)

	// ErrExceptionNotFound 异常单不存在。
	ErrExceptionNotFound = response.Register("RETURNS_EXCEPTION_NOT_FOUND", "异常单不存在", http.StatusNotFound)
	// ErrExceptionStatusConflict 异常单状态冲突（异常状态机守卫 0 行，plan §6.10）。
	ErrExceptionStatusConflict = response.Register("RETURNS_EXCEPTION_STATUS_CONFLICT", "异常单状态冲突", http.StatusConflict)
	// ErrExceptionTypeInvalid 异常类型非法（business-flow §11.2 九类值域）。
	ErrExceptionTypeInvalid = response.Register("RETURNS_EXCEPTION_TYPE_INVALID", "异常类型非法", http.StatusBadRequest)
	// ErrExceptionImagesInvalid 异常图片挂接入参非法（file_ids 空/超限/非图片/已过期——
	// business-flow §11.2 异常图片能力，2026-10-04 立项）。
	ErrExceptionImagesInvalid = response.Register("RETURNS_EXCEPTION_IMAGES_INVALID", "异常图片挂接参数非法", http.StatusBadRequest)
	// ErrExceptionImagesClosed 异常单已解决/关闭，生命周期终点不接受图片挂接。
	ErrExceptionImagesClosed = response.Register("RETURNS_EXCEPTION_IMAGES_CLOSED", "异常单已解决或关闭，不能挂接图片", http.StatusConflict)
	// ErrFreezeTargetRequired 请求异常冻结但定位信息不足（冻结需 仓库+库位+SKU 定位到库存行，
	// inventory-rules §4.1 锁定必须可定位）。
	ErrFreezeTargetRequired = response.Register("RETURNS_FREEZE_TARGET_REQUIRED", "异常冻结需要仓库/库位/SKU 定位信息", http.StatusBadRequest)

	// ErrGatewayRequired 库存原语网关未注入（plan §3.1 规则① fail-closed：router 装配缺位
	// 拒绝执行，杜绝静默跳过库存变更）。
	ErrGatewayRequired = response.Register("RETURNS_GATEWAY_MISSING", "库存原语网关未装配", http.StatusInternalServerError)
	// ErrReaderRequired 跨域读接口未注入（追溯/来源单校验缺位拒绝，plan §3.1 规则①）。
	ErrReaderRequired = response.Register("RETURNS_READER_MISSING", "跨域读接口未装配", http.StatusInternalServerError)

	// ErrTraceParamRequired 追溯查询缺少定位维度（sku_id 与 serial_no 至少其一，inventory-rules §10）。
	ErrTraceParamRequired = response.Register("RETURNS_TRACE_PARAM_REQUIRED", "追溯查询需要 sku_id 或 serial_no", http.StatusBadRequest)
	// ErrSerialNotFound 追溯的序列号不存在。
	ErrSerialNotFound = response.Register("RETURNS_SERIAL_NOT_FOUND", "序列号不存在", http.StatusNotFound)
)
