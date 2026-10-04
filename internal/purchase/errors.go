package purchase

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// purchase 域错误码（architecture.md §2 模块命名空间 PURCHASE_*；api.md §4 校验失败
// 必须带 details，如超量收货 details 携带行号/订单量/已收量/本次量——plan §6.1）。
// 统一经 internal/response 注册与输出，禁止 handler 直接 c.JSON（plan §2.3 判据 6）。
// 约定：字段格式/必填问题用 COMMON_INVALID_PARAM + details{field,reason}；
// 业务冲突（状态机、数量约束、单据关系）用本域错误码。
var (
	// —— 采购订单 ——
	ErrPONotFound = response.Register("PURCHASE_ORDER_NOT_FOUND", "采购订单不存在", http.StatusNotFound)
	// ErrPOStatusNotAllowed 状态机守卫拒绝（business-flow §13.2：所有状态变化必须经过
	// 业务方法，迁移目标不受当前状态允许）。
	ErrPOStatusNotAllowed = response.Register("PURCHASE_ORDER_STATUS_NOT_ALLOWED", "采购订单当前状态不允许该操作", http.StatusConflict)
	// ErrPOHasReceipts 存在收货记录，禁止取消（§13.3：已影响业务的单据只能关闭/冲正）。
	ErrPOHasReceipts = response.Register("PURCHASE_ORDER_HAS_RECEIPTS", "采购订单已产生收货，禁止取消；请使用差额关闭", http.StatusConflict)
	// ErrPOLinesRequired 采购订单必须至少一行明细。
	ErrPOLinesRequired = response.Register("PURCHASE_ORDER_LINES_REQUIRED", "采购订单必须至少包含一行明细", http.StatusBadRequest)
	// ErrPOLineDuplicated 明细行号或 SKU 重复。
	ErrPOLineDuplicated = response.Register("PURCHASE_ORDER_LINE_DUPLICATED", "采购订单明细行号或 SKU 重复", http.StatusBadRequest)

	// —— 数量约束（business-flow §2.3：累计收货 ≤ 原始数量，防超量收货）——
	// ErrOverReceipt 超量收货（本次 + 累计超出原始数量；details 携带行号/上限/已收/本次）。
	ErrOverReceipt = response.Register("PURCHASE_OVER_RECEIPT", "收货数量超出采购订单原始数量", http.StatusBadRequest)

	// —— 入库单 / 收货 ——
	ErrInboundNotFound         = response.Register("PURCHASE_INBOUND_NOT_FOUND", "入库单不存在", http.StatusNotFound)
	ErrInboundStatusNotAllowed = response.Register("PURCHASE_INBOUND_STATUS_NOT_ALLOWED", "入库单当前状态不允许该操作", http.StatusConflict)
	ErrInboundHasReceipts      = response.Register("PURCHASE_INBOUND_HAS_RECEIPTS", "入库单已产生收货，禁止取消", http.StatusConflict)
	ErrInboundSourceInvalid    = response.Register("PURCHASE_INBOUND_SOURCE_INVALID", "入库单来源单据不存在或状态不允许", http.StatusBadRequest)
	ErrReceiptNotFound         = response.Register("PURCHASE_RECEIPT_NOT_FOUND", "收货记录不存在", http.StatusNotFound)
	// ErrReceiptQtyInvalid 收货行数量非法（合格+拒收必须为正）。
	ErrReceiptQtyInvalid = response.Register("PURCHASE_RECEIPT_QTY_INVALID", "收货数量非法：合格数量与拒收数量之和必须为正数", http.StatusBadRequest)
	// ErrReceiptInboundLineUnknown 收货 SKU 不在该入库单明细中。
	ErrReceiptInboundLineUnknown = response.Register("PURCHASE_RECEIPT_LINE_UNKNOWN", "收货 SKU 不在该入库单明细中", http.StatusBadRequest)
	// ErrBatchRequired 批次管理 SKU 收货必须采集批次号（inventory-rules §6）。
	ErrBatchRequired = response.Register("PURCHASE_BATCH_REQUIRED", "批次管理 SKU 收货必须采集批次号", http.StatusBadRequest)
	// ErrExpiryRequired 效期管理 SKU 收货必须采集效期（inventory-rules §6）。
	ErrExpiryRequired = response.Register("PURCHASE_EXPIRY_REQUIRED", "效期管理 SKU 收货必须采集效期日期", http.StatusBadRequest)
	// ErrSerialRequired 序列号管理 SKU 收货必须逐件采集序列号（inventory-rules §8）。
	ErrSerialRequired = response.Register("PURCHASE_SERIAL_REQUIRED", "序列号管理 SKU 收货必须逐件采集序列号", http.StatusBadRequest)
	// ErrSerialQtyMismatch 序列号件数与合格数量不一致（序列号 SKU 按件收货）。
	ErrSerialQtyMismatch = response.Register("PURCHASE_SERIAL_QTY_MISMATCH", "序列号数量与合格收货数量不一致", http.StatusBadRequest)
	// ErrReceiptQtyMismatch 收货数量与序列号/明细分账不一致等数量守卫失败。
	ErrReceiptQtyMismatch = response.Register("PURCHASE_RECEIPT_QTY_MISMATCH", "收货数量守卫未生效（并发或重复提交）", http.StatusConflict)

	// —— 跨域服务缺位（fail-closed，plan §2.3 判据 6/§4.3 规则①：杜绝静默跳过校验）——
	ErrCheckerMissing = response.Register("PURCHASE_CHECKER_MISSING", "采购域跨域校验服务未装配（供应商/仓库/SKU 校验缺位）", http.StatusInternalServerError)
	// ErrStockGatewayMissing 库存原语网关未装配（router 必须 WithStock 注入 inventory.Service）。
	ErrStockGatewayMissing = response.Register("PURCHASE_STOCK_GATEWAY_MISSING", "库存原语网关未装配，无法执行收货/质检/上架落账", http.StatusInternalServerError)
	// ErrExceptionServiceMissing 异常中心创建服务未装配（异常收货必须登记异常中心，§3.4/§11.2）。
	ErrExceptionServiceMissing = response.Register("PURCHASE_EXCEPTION_SERVICE_MISSING", "异常中心服务未装配，无法登记收货异常", http.StatusInternalServerError)
	// ErrBinRequired 未指定目标库位且推荐库位服务不可用（business-flow §5.2）。
	ErrBinRequired = response.Register("PURCHASE_BIN_REQUIRED", "未指定目标库位且推荐库位服务不可用", http.StatusBadRequest)

	// —— 质检 ——
	ErrQCNotFound = response.Register("PURCHASE_QC_NOT_FOUND", "质检单不存在", http.StatusNotFound)
	// ErrQCStatusNotAllowed 质检单状态机守卫（plan §6.3：PENDING→INSPECTING→COMPLETED）。
	ErrQCStatusNotAllowed = response.Register("PURCHASE_QC_STATUS_NOT_ALLOWED", "质检单当前状态不允许该操作", http.StatusConflict)
	// ErrQCQtyInvalid 质检数量非法（合格+不合格 必须为正且等于检验数量）。
	ErrQCQtyInvalid = response.Register("PURCHASE_QC_QTY_INVALID", "质检数量非法：合格+不合格必须等于检验数量且为正数", http.StatusBadRequest)
	// ErrQCResultInvalid 处理结果不在 business-flow §4.3 九类值域。
	ErrQCResultInvalid = response.Register("PURCHASE_QC_RESULT_INVALID", "质检处理结果非法", http.StatusBadRequest)
	// ErrQCInspectionTypeInvalid 检验方式不在 §4.1 三类值域。
	ErrQCInspectionTypeInvalid = response.Register("PURCHASE_QC_INSPECTION_TYPE_INVALID", "检验方式非法（免检/抽检/全检）", http.StatusBadRequest)
	// ErrQCQtyExceedsReceived 质检数量超出该入库单已收货量。
	ErrQCQtyExceedsReceived = response.Register("PURCHASE_QC_QTY_EXCEEDS_RECEIVED", "质检数量超出入库单已收货数量", http.StatusConflict)
	// ErrPendingNotPutaway 存在未上架（未入待检区）的收货量，暂不能提交质检结果——
	// InspectResult 的前置状态 pending_inspect 必须先经上架任务落账产生。
	ErrPendingNotPutaway = response.Register("PURCHASE_PENDING_NOT_PUTAWAY", "存在未上架进入待检区的收货数量，请先完成上架再提交质检结果", http.StatusConflict)

	// —— 上架任务 ——
	ErrPutawayTaskNotFound = response.Register("PURCHASE_PUTAWAY_TASK_NOT_FOUND", "上架任务不存在", http.StatusNotFound)
	// ErrPutawayStatusNotAllowed 上架任务状态机守卫（§5.1：待上架→上架中→已完成）。
	ErrPutawayStatusNotAllowed = response.Register("PURCHASE_PUTAWAY_STATUS_NOT_ALLOWED", "上架任务当前状态不允许该操作", http.StatusConflict)
	// ErrPutawayClaimConflict 原子抢占失败：任务已被其他作业员领取（architecture §5.2，0 行=领取冲突）。
	ErrPutawayClaimConflict = response.Register("PURCHASE_PUTAWAY_CLAIM_CONFLICT", "上架任务已被领取", http.StatusConflict)
	// ErrPutawayNotClaimant 仅任务领取人本人可执行上架确认。
	ErrPutawayNotClaimant = response.Register("PURCHASE_PUTAWAY_NOT_CLAIMANT", "仅任务领取人可执行该上架任务", http.StatusForbidden)
	// ErrPutawayBinInvalid 目标库位不存在、已停用或不属于该仓库（经 BinChecker 校验）。
	ErrPutawayBinInvalid = response.Register("PURCHASE_PUTAWAY_BIN_INVALID", "目标库位不存在、已停用或不属于该仓库", http.StatusBadRequest)
	// ErrInboundHasActiveTasks 存在进行中的上架任务，禁止关闭入库单（先完成或取消任务）。
	ErrInboundHasActiveTasks = response.Register("PURCHASE_INBOUND_HAS_ACTIVE_TASKS", "存在进行中的上架任务，不能关闭入库单", http.StatusConflict)

	// —— 通用 ——
	// ErrStatusConflict 状态守卫条件更新未命中（并发迁移冲突，plan §6 统一实现形态）。
	ErrStatusConflict = response.Register("PURCHASE_STATUS_CONFLICT", "单据状态已变化，请刷新后重试", http.StatusConflict)
	// ErrNumberConflict 单号生成冲突（docnum 重试耗尽兜底）。
	ErrNumberConflict = response.Register("PURCHASE_NUMBER_CONFLICT", "单据编号生成冲突，请重试", http.StatusConflict)
)
