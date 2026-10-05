package devices

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// devices/scanner 域错误码（api.md §1 M3 段 DEVICE_*/SCANNER_*；scanner 识别类错误码
// 按 scanner.md §6.1 字面冻结——UNKNOWN_BARCODE/SKU_NOT_FOUND/BIN_NOT_FOUND/
// ORDER_NOT_FOUND/TASK_NOT_FOUND，不带域前缀，backend-m3-plan §8.3 条 6）。
// 统一经 internal/response 注册，禁止 handler 直接 c.JSON（plan §4.2 判据 2）。
var (
	// ErrDeviceNotFound 设备不存在（或不在当前用户数据权限范围内）。
	ErrDeviceNotFound = response.Register("DEVICE_NOT_FOUND", "设备不存在", http.StatusNotFound)
	// ErrDeviceCodeInvalid 设备编码非法（空/超长/含非法字符；管理端命名如 SF-SCAN-001）。
	ErrDeviceCodeInvalid = response.Register("DEVICE_CODE_INVALID", "设备编码非法", http.StatusBadRequest)
	// ErrDeviceCodeConflict 设备编码已存在（uk_devices_code）。
	ErrDeviceCodeConflict = response.Register("DEVICE_CODE_CONFLICT", "设备编码已存在", http.StatusConflict)
	// ErrDeviceTypeInvalid 设备类型非法（pc/pad/pda/scanner/printer）。
	ErrDeviceTypeInvalid = response.Register("DEVICE_TYPE_INVALID", "设备类型非法", http.StatusBadRequest)
	// ErrWarehouseNotFound 绑定仓库不存在或未启用（warehouse_id 裸 ID + Service 校验，
	// plan §5 000013 注；校验经 auth.WarehouseChecker 窄接口）。
	ErrWarehouseNotFound = response.Register("DEVICE_WAREHOUSE_NOT_FOUND", "绑定的仓库不存在或未启用", http.StatusBadRequest)
	// ErrDeviceStatusConflict 设备状态冲突（守卫 UPDATE 影响行数 0——已停用/并发操作）。
	ErrDeviceStatusConflict = response.Register("DEVICE_STATUS_CONFLICT", "设备状态冲突", http.StatusConflict)
	// ErrDeviceActivationInvalid 激活失败：激活码无效、已过期或已消费（一次性语义——
	// 三种原因统一返回，不向设备端区分，plan §8.2 防重放裁决）。
	ErrDeviceActivationInvalid = response.Register("DEVICE_ACTIVATION_INVALID", "激活码无效或已过期", http.StatusUnauthorized)
	// ErrDeviceTokenInvalid 设备令牌无效（签名/过期/停用/未激活/token_version 不匹配——
	// DeviceAuthRequired 拒绝）。
	ErrDeviceTokenInvalid = response.Register("DEVICE_TOKEN_INVALID", "设备令牌无效或已失效", http.StatusUnauthorized)
	// ErrBatteryInvalid 心跳电量非法（DDL CHECK 0–100）。
	ErrBatteryInvalid = response.Register("DEVICE_BATTERY_INVALID", "电量必须为 0-100 的整数", http.StatusBadRequest)
	// ErrConfigInvalid 配置下发载荷非法（非 JSON 对象/未知键/值类型错误——devices.md §7.3 冻结项白名单）。
	ErrConfigInvalid = response.Register("DEVICE_CONFIG_INVALID", "设备配置项非法", http.StatusBadRequest)
	// ErrAppVersionNotFound 无已发布的 App 版本（plan §15：latest 查询无已发布行返回 404，
	// 错误码字面冻结于 plan §15/§8.2）。
	ErrAppVersionNotFound = response.Register("DEVICE_APP_VERSION_NOT_FOUND", "暂无已发布的 App 版本", http.StatusNotFound)
	// ErrPlatformInvalid App 平台非法（DDL CHECK 仅 android——M3 值域）。
	ErrPlatformInvalid = response.Register("DEVICE_PLATFORM_INVALID", "App 平台非法", http.StatusBadRequest)
	// ErrDeviceLogBatchInvalid 设备日志批量上报非法（超 100 条/level 值域外/occurred_at 缺失）。
	ErrDeviceLogBatchInvalid = response.Register("DEVICE_LOG_BATCH_INVALID", "设备日志上报载荷非法", http.StatusBadRequest)

	// ---- scanner 识别类（scanner.md §6.1 字面冻结）----

	// ErrUnknownBarcode 无法识别的条码（管线全未命中——scanner.md §6.1）。
	ErrUnknownBarcode = response.Register("UNKNOWN_BARCODE", "无法识别的条码", http.StatusNotFound)
	// ErrSKUNotFound SKU 不存在/已停用（条码命中但对象不可用——scanner.md §6.1）。
	ErrSKUNotFound = response.Register("SKU_NOT_FOUND", "SKU 不存在", http.StatusNotFound)
	// ErrBinNotFound 库位不存在/已停用。
	ErrBinNotFound = response.Register("BIN_NOT_FOUND", "库位不存在", http.StatusNotFound)
	// ErrOrderNotFound 单据不存在（单据类前缀命中但对象不存在）。
	ErrOrderNotFound = response.Register("ORDER_NOT_FOUND", "单据不存在", http.StatusNotFound)
	// ErrTaskNotFound 任务不存在（任务类前缀 PW/PK/CH 命中但对象不存在）。
	ErrTaskNotFound = response.Register("TASK_NOT_FOUND", "任务不存在", http.StatusNotFound)
	// ErrCodeInvalid 扫码解析请求的 code 非法（空/超长——raw_code varchar(255)）。
	ErrCodeInvalid = response.Register("SCANNER_CODE_INVALID", "条码内容非法", http.StatusBadRequest)

	// ---- SFQR 协议识别类（docs/qr-code.md §5 字面冻结，均 400；解析唯一点 sfqr.go）----

	// ErrSfqrInvalid SFQR 载荷格式非法（段数错误 / payload 空或超 64 字符——§4 向量 11-14）。
	ErrSfqrInvalid = response.Register("SFQR_INVALID", "SFQR 载荷格式非法", http.StatusBadRequest)
	// ErrSfqrVersionUnsupported SFQR 协议版本不支持（version ≠ "1"——明确拒绝不降级，
	// qr-code.md §3.1 版本策略）。
	ErrSfqrVersionUnsupported = response.Register("SFQR_VERSION_UNSUPPORTED", "SFQR 协议版本不支持", http.StatusBadRequest)
	// ErrSfqrTypeUnsupported SFQR 类型已预留未实现（BIN/BOX/PALLET 及未知 type，
	// 大小写敏感——qr-code.md §2.2 段表）。
	ErrSfqrTypeUnsupported = response.Register("SFQR_TYPE_UNSUPPORTED", "SFQR 类型已预留未实现", http.StatusBadRequest)

	// ErrCheckerRequired 仓库存在性校验器未注入（plan §3.1 规则① fail-closed：
	// router 装配缺位拒绝执行，杜绝静默跳过 warehouse_id 校验）。
	ErrCheckerRequired = response.Register("DEVICE_CHECKER_MISSING", "仓库校验器未装配", http.StatusInternalServerError)
	// ErrReaderRequired 解析窄接口未注入（plan §3.1 规则①：匹配器缺位启动期 panic，
	// 运行期兜底拒绝——不静默降级为"全部未识别"）。
	ErrReaderRequired = response.Register("DEVICE_READER_MISSING", "解析读接口未装配", http.StatusInternalServerError)
)
