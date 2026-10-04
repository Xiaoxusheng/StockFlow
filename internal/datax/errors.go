package datax

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// datax 域错误码（architecture.md §2 模块命名空间 DATAX_*；api.md §4 校验失败必须带
// details）。统一经 internal/response 注册，禁止 handler 直接 c.JSON（plan §2.3 判据 6）。
var (
	// ErrImportTypeInvalid 导入类型不在冻结九值清单（或该类型 Writer 未装配——装配缺位
	// fail-closed 暴露为 4xx 而非静默假成功，plan §3.1 规则①同族口径）。
	ErrImportTypeInvalid = response.Register("DATAX_IMPORT_TYPE_INVALID", "导入类型不存在或未就绪", http.StatusBadRequest)
	// ErrImportFileRequired 上传缺少文件部件。
	ErrImportFileRequired = response.Register("DATAX_FILE_REQUIRED", "请选择要上传的文件", http.StatusBadRequest)
	// ErrImportFileType 导入源文件不是 .xlsx（excelize v2 仅支持 xlsx；storage 白名单同口径）。
	ErrImportFileType = response.Register("DATAX_IMPORT_FILE_TYPE", "导入文件必须为 .xlsx 格式", http.StatusBadRequest)
	// ErrTemplateMismatch 表头与模板列定义不一致（结构层第一道闸：列名/顺序逐列比对）。
	ErrTemplateMismatch = response.Register("DATAX_TEMPLATE_MISMATCH", "表头与模板不一致，请下载最新模板后填写", http.StatusBadRequest)
	// ErrTooManyRows 解析行数超过 datax.import_max_rows（plan §6.2：上传即拒 4xx）。
	ErrTooManyRows = response.Register("DATAX_TOO_MANY_ROWS", "导入行数超出上限", http.StatusBadRequest)
	// ErrSheetEmpty 上传文件无数据行。
	ErrSheetEmpty = response.Register("DATAX_SHEET_EMPTY", "导入文件没有数据行", http.StatusBadRequest)
	// ErrImportFileSuspicious 导入文件解压放大守卫命中（zip 部件数/单部件/累计解压尺寸
	// 超出安全上限——api.md §5 上传安全清单的解压放大维度；≤20MB 上传内恶意工作簿
	// 可把内存放大数十倍，行数守卫只能约束迭代开始之后）。
	ErrImportFileSuspicious = response.Register("DATAX_FILE_SUSPICIOUS", "导入文件解压后超出安全上限，已拒绝", http.StatusBadRequest)
	// ErrTaskNotFound 导入/导出任务不存在（或不在可见范围）。
	ErrTaskNotFound = response.Register("DATAX_TASK_NOT_FOUND", "任务不存在", http.StatusNotFound)
	// ErrStatusConflict 任务状态冲突（向导状态机守卫 UPDATE 影响行数 0——重放/并发确认，
	// plan §13.2 状态守卫幂等）。
	ErrStatusConflict = response.Register("DATAX_STATUS_CONFLICT", "任务状态冲突，请刷新后重试", http.StatusConflict)
	// ErrRowsInvalid 存在校验失败行时禁止确认导入（excel §6.1 一致性约束 1 的前置条件）。
	ErrRowsInvalid = response.Register("DATAX_ROWS_INVALID", "存在校验失败的行，请先处理错误后重新上传", http.StatusConflict)
	// ErrConfirmRequired 高危导入（INITIAL_INVENTORY）未携带二次确认（excel §6.1：
	// 必须走审批或二次确认）。
	ErrConfirmRequired = response.Register("DATAX_CONFIRM_REQUIRED", "初始化库存导入为高危操作，需显式二次确认", http.StatusBadRequest)
	// ErrModuleInvalid 导出模块不在冻结十六值清单。
	ErrModuleInvalid = response.Register("DATAX_MODULE_INVALID", "导出模块不存在", http.StatusBadRequest)
	// ErrScopeInvalid 导出范围非法或缺必需参数（SELECTED 缺 ids/超 1000、TIME_RANGE 缺时间区间——plan §6.3）。
	ErrScopeInvalid = response.Register("DATAX_SCOPE_INVALID", "导出范围参数非法", http.StatusBadRequest)
	// ErrModuleNotAvailable 导出模块的行源未装配（REPORT 行源归 MT4；装配缺位 fail-closed）。
	ErrModuleNotAvailable = response.Register("DATAX_MODULE_NOT_AVAILABLE", "该模块导出尚未就绪", http.StatusConflict)
	// ErrExportNotFinished 导出任务尚未到终态，产物不可下载。
	ErrExportNotFinished = response.Register("DATAX_EXPORT_NOT_FINISHED", "导出尚未完成，请稍后下载", http.StatusConflict)
	// ErrFileNotFound files 登记行不存在。
	ErrFileNotFound = response.Register("DATAX_FILE_NOT_FOUND", "文件不存在", http.StatusNotFound)
	// ErrFileExpired 文件已过保留期（file_cleanup 清理前即拒绝访问，excel §5）。
	ErrFileExpired = response.Register("DATAX_FILE_EXPIRED", "文件已过期清理", http.StatusGone)
	// ErrFileDeleted 文件已删除（软删行拒绝下载）。
	ErrFileDeleted = response.Register("DATAX_FILE_DELETED", "文件已删除", http.StatusNotFound)
	// ErrPreviewUnsupported 预览仅支持图片类型（plan §6.4：不做缩略图/文档预览）。
	ErrPreviewUnsupported = response.Register("DATAX_PREVIEW_UNSUPPORTED", "该文件类型不支持预览", http.StatusBadRequest)
	// ErrModuleRequired 文件上传缺少 module 参数（excel §7：文件必须归属业务模块）。
	ErrModuleRequired = response.Register("DATAX_MODULE_REQUIRED", "缺少业务模块参数 module", http.StatusBadRequest)
	// ErrModuleParamInvalid module/business_no 参数字符集或长度非法（files 列宽防御，S7 同族）。
	ErrModuleParamInvalid = response.Register("DATAX_MODULE_PARAM_INVALID", "业务模块/单号参数非法", http.StatusBadRequest)
	// ErrPayloadInvalid 队列 payload 解析失败或必填字段缺失（handler 拒绝执行）。
	ErrPayloadInvalid = response.Register("DATAX_PAYLOAD_INVALID", "任务载荷非法", http.StatusBadRequest)
	// ErrWriterMissing 运行期 Writer/Source 缺位（装配缺位属于编程错误，任务终态 FAILED）。
	ErrWriterMissing = response.Register("DATAX_WRITER_MISSING", "导入写入器/导出行源未装配", http.StatusInternalServerError)
	// ErrEnqueueFailed 入队失败且状态回补失败（任务卡 EXECUTING/PROCESSING 的兜底披露）。
	ErrEnqueueFailed = response.Register("DATAX_ENQUEUE_FAILED", "任务入队失败，请稍后重试", http.StatusServiceUnavailable)
)
