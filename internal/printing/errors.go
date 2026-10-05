package printing

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// printing 域错误码（architecture.md §2 模块命名空间 PRINT_*，backend-m3-plan §1
// 新域错误码清单；api.md §4 校验失败必须带 details）。统一经 internal/response 注册，
// 禁止 handler 直接 c.JSON（plan §2.3 判据 6）。
var (
	// ErrTemplateNotFound 模板不存在。
	ErrTemplateNotFound = response.Register("PRINT_TEMPLATE_NOT_FOUND", "打印模板不存在", http.StatusNotFound)
	// ErrTemplateDisabled 模板已停用（printing.md §2：停用即不可被新任务选用）。
	ErrTemplateDisabled = response.Register("PRINT_TEMPLATE_DISABLED", "打印模板已停用", http.StatusConflict)
	// ErrTemplateStatusConflict 模板状态冲突（启停守卫 UPDATE 影响行数 0——并发变更）。
	ErrTemplateStatusConflict = response.Register("PRINT_TEMPLATE_STATUS_CONFLICT", "打印模板状态冲突", http.StatusConflict)

	// ErrObjectTypeInvalid 对象类型非法（九值域外）。
	ErrObjectTypeInvalid = response.Register("PRINT_OBJECT_TYPE_INVALID", "打印对象类型非法", http.StatusBadRequest)
	// ErrPaperInvalid 纸张规格非法（五值域外）。
	ErrPaperInvalid = response.Register("PRINT_PAPER_INVALID", "纸张规格非法", http.StatusBadRequest)
	// ErrSymbologyInvalid 条码码制非法（模板五值域外 / 标签类缺失 / 单据类多余）。
	ErrSymbologyInvalid = response.Register("PRINT_SYMBOLOGY_INVALID", "条码码制非法", http.StatusBadRequest)
	// ErrFieldsInvalid 字段绑定非法（绑定键不在 object_type 预设内——plan §7.1 预设键子集）。
	ErrFieldsInvalid = response.Register("PRINT_FIELDS_INVALID", "模板字段绑定非法", http.StatusBadRequest)

	// ErrTaskNotFound 打印任务不存在。
	ErrTaskNotFound = response.Register("PRINT_TASK_NOT_FOUND", "打印任务不存在", http.StatusNotFound)
	// ErrTaskStatusConflict 打印任务状态冲突（render 状态机守卫 0 行——已被处理或并发变更）。
	ErrTaskStatusConflict = response.Register("PRINT_TASK_STATUS_CONFLICT", "打印任务状态冲突", http.StatusConflict)
	// ErrAlreadyConfirmed 执行确认结果已回填（plan §13.2：SUCCESS/FAILED 回填一次，重复确认 409）。
	ErrAlreadyConfirmed = response.Register("PRINT_ALREADY_CONFIRMED", "打印结果已确认，不能重复回填", http.StatusConflict)
	// ErrResultInvalid 执行确认结果值非法（SUCCESS/FAILED 之外）。
	ErrResultInvalid = response.Register("PRINT_RESULT_INVALID", "打印结果值非法", http.StatusBadRequest)

	// ErrTooManyDataIDs 打印对象数超上限（单任务 data_ids ≤500，Orchestrator 裁决④，
	// plan §7.2/§18.4；details 携带 limit 与 actual）。
	ErrTooManyDataIDs = response.Register("PRINT_TOO_MANY_DATA_IDS", "打印对象数超出单任务上限", http.StatusBadRequest)
	// ErrCopiesInvalid 打印份数非法（1–999；上限为防御性边界，go-dev-standard 规则 5）。
	ErrCopiesInvalid = response.Register("PRINT_COPIES_INVALID", "打印份数非法", http.StatusBadRequest)
	// ErrDataNotFound 打印数据不存在（ContentReader 装配时逐对象校验，details 携带缺失 ID）。
	ErrDataNotFound = response.Register("PRINT_DATA_NOT_FOUND", "打印数据不存在", http.StatusBadRequest)
	// ErrDataDisabled 打印对象中存在已停用 SKU（qr-code.md §9 不可打印校验——约束 10
	// 「商品已停用」；409 语义对齐 ErrTemplateDisabled 先例；details 携 disabled_ids）。
	ErrDataDisabled = response.Register("PRINT_SKU_DISABLED", "打印对象中存在已停用 SKU", http.StatusConflict)
	// ErrDataIDsInvalid 打印对象标识非法（空串/超出长度等）。
	ErrDataIDsInvalid = response.Register("PRINT_DATA_IDS_INVALID", "打印对象标识非法", http.StatusBadRequest)

	// ErrReaderRequired 装配 reader 未注入（plan §3.1 规则① fail-closed：缺位拒绝执行）。
	ErrReaderRequired = response.Register("PRINT_READER_MISSING", "打印装配接口未装配", http.StatusInternalServerError)
	// ErrQueueRequired 队列未注入（plan §3.1 规则①：render 入队缺位拒绝）。
	ErrQueueRequired = response.Register("PRINT_QUEUE_MISSING", "异步队列未装配", http.StatusInternalServerError)

	// ErrBarcodeTextInvalid 条码内容非法（空/超长）。
	ErrBarcodeTextInvalid = response.Register("PRINT_BARCODE_TEXT_INVALID", "条码内容非法", http.StatusBadRequest)
	// ErrBarcodeContentInvalid 条码内容与码制不符（如 EAN13 非数字或位数错误）。
	ErrBarcodeContentInvalid = response.Register("PRINT_BARCODE_CONTENT_INVALID", "条码内容与码制不符", http.StatusBadRequest)
	// ErrBarcodeSizeInvalid 条码尺寸参数非法（越界或小于码制最小模块尺寸）。
	ErrBarcodeSizeInvalid = response.Register("PRINT_BARCODE_SIZE_INVALID", "条码尺寸参数非法", http.StatusBadRequest)
)

// NewDataNotFoundError 构造装配缺失错误（details.missing_ids = 缺失对象标识；
// 各域 printing_content.go 经本函数返回同一域内错误码，避免跨包借用或重复注册）。
func NewDataNotFoundError(missing []string) *response.Error {
	return response.NewError(ErrDataNotFound, map[string]any{"missing_ids": missing})
}

// NewDataDisabledError 构造停用拒绝错误（details.disabled_ids = 不可打印 SKU 编码
// 列表——qr-code.md §9 逐条「{code}：商品已停用」；形态镜像 NewDataNotFoundError，
// 各域 reader 经本函数返回同一域内错误码）。
func NewDataDisabledError(disabled []string) *response.Error {
	return response.NewError(ErrDataDisabled, map[string]any{"disabled_ids": disabled})
}
