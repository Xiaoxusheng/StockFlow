// Package response 统一响应信封、分页与业务错误码注册表
// （api.md §2、architecture.md §2；backend-m1-plan §4.2 判据 2：任何 handler 禁止绕过本包直接 c.JSON）。
//
// 信封 {code,message,data,request_id,details}：
//   - 成功：code=0（数字），无 details 字段；
//   - 失败：code 为模块命名空间字符串错误码（COMMON_*/AUTH_*/MASTERDATA_*/WAREHOUSE_*/INVENTORY_*），
//     message 为用户可读信息，details 携带结构化补充（校验失败必须带，api.md §4）。
package response

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// RequestIDKey gin Context 键：middleware 写入，本包读取后放入信封（architecture.md §3.1）。
const RequestIDKey = "sf_request_id"

// Code 业务错误码：模块命名空间字符串 + 用户可读默认文案 + 对应 HTTP 状态。
type Code struct {
	Code       string
	Message    string
	HTTPStatus int
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Code{}
)

// Register 注册业务错误码并返回，供各域包以包级 var 声明：
//
//	var ErrProductNotFound = response.Register("MASTERDATA_PRODUCT_NOT_FOUND", "商品不存在", http.StatusNotFound)
//
// 重复注册不同定义视为编程错误，启动期（init）直接 panic 快速失败；
// 完全相同的重复注册幂等（允许跨包复用同一错误码常量的合法场景）。
func Register(code, message string, httpStatus int) Code {
	if code == "" || message == "" {
		panic("response.Register: code/message 不能为空")
	}
	if httpStatus < 400 || httpStatus > 599 {
		panic(fmt.Sprintf("response.Register: %s 的 HTTP 状态 %d 不在 400-599", code, httpStatus))
	}
	c := Code{Code: code, Message: message, HTTPStatus: httpStatus}
	registryMu.Lock()
	defer registryMu.Unlock()
	if prev, dup := registry[code]; dup && prev != c {
		panic("response.Register: 错误码重复注册且定义不一致: " + code)
	}
	registry[code] = c
	return c
}

// Lookup 查询已注册错误码（测试/文档工具用）。
func Lookup(code string) (Code, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[code]
	return c, ok
}

// 脚手架内置通用错误码（COMMON_*）。域错误码由各域包注册，禁止跨域借用。
var (
	CodeInvalidParam       = Register("COMMON_INVALID_PARAM", "请求参数错误", http.StatusBadRequest)
	CodeUnauthorized       = Register("COMMON_UNAUTHORIZED", "未认证或会话已失效", http.StatusUnauthorized)
	CodePermissionDenied   = Register("COMMON_PERMISSION_DENIED", "没有操作权限", http.StatusForbidden)
	CodeNotFound           = Register("COMMON_NOT_FOUND", "资源不存在", http.StatusNotFound)
	CodeMethodNotAllowed   = Register("COMMON_METHOD_NOT_ALLOWED", "请求方法不支持", http.StatusMethodNotAllowed)
	CodeConflict           = Register("COMMON_CONFLICT", "资源状态冲突", http.StatusConflict)
	CodePayloadTooLarge    = Register("COMMON_PAYLOAD_TOO_LARGE", "请求体超出大小限制", http.StatusRequestEntityTooLarge)
	CodeRateLimited        = Register("COMMON_RATE_LIMITED", "请求过于频繁，请稍后重试", http.StatusTooManyRequests)
	CodeInternalError      = Register("COMMON_INTERNAL_ERROR", "系统内部错误，请稍后重试", http.StatusInternalServerError)
	CodeServiceUnavailable = Register("COMMON_SERVICE_UNAVAILABLE", "服务暂不可用", http.StatusServiceUnavailable)
)

// Error 业务错误：Service 层构造并向上返回，handler 经 Err 写出统一信封。
type Error struct {
	code    Code
	msg     string // 为空时用 code.Message
	Details any    // 结构化补充（api.md §4：校验失败必须带 details）
}

// NewError 构造业务错误。
func NewError(c Code, details any) *Error {
	return &Error{code: c, Details: details}
}

// WithMessage 覆盖默认文案（需结合上下文时使用；机器可读码不变）。
func (e *Error) WithMessage(msg string) *Error {
	e.msg = msg
	return e
}

// Error 实现 error 接口（日志用，含错误码）。
func (e *Error) Error() string {
	return e.code.Code + ": " + e.Message()
}

// Message 用户可读信息。
func (e *Error) Message() string {
	if e.msg != "" {
		return e.msg
	}
	return e.code.Message
}

// AsError 将任意 error 归一为 *Error：非业务错误按 COMMON_INTERNAL_ERROR 处理。
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return NewError(CodeInternalError, nil)
}

// Envelope 统一响应信封的文档形态定义（api.md §2：{code,message,data,request_id,details}）。
// 仅供 swag 注解（@Success/@Failure）引用说明信封结构——实际写出经 Err/OK/OKPage，
// 本结构不参与运行时序列化路径，禁止绕过既有写出函数直接使用本类型。
type Envelope struct {
	Code      any    `json:"code"` // 成功 0（数字）；失败为模块命名空间错误码字符串（COMMON_* 等）
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details,omitempty"` // 校验失败必带（api.md §4）
}

// BindErrorReason 请求体绑定失败的固定对外文案（清偿项 F20）。
// 安全边界（go-dev-standard：用户输入不可信、内部错误不外泄）：ShouldBindJSON 的
// err.Error() 可能携带内部结构/类型信息，一律不得直出 details，统一收敛为本文案。
const BindErrorReason = "请求体格式错误"

// bindTagMessages validator tag → 字段级固定中文文案（不透传 validator 原始错误串）。
var bindTagMessages = map[string]string{
	"required": "必填",
	"min":      "长度或取值不足",
	"max":      "长度或取值超出上限",
	"len":      "长度不符",
	"oneof":    "取值不在允许范围",
	"email":    "格式不正确",
	"gte":      "不能小于下限",
	"gt":       "必须大于下限",
	"lte":      "不能大于上限",
	"lt":       "必须小于上限",
	"unique":   "存在重复项",
}

// BindErrorDetails ShouldBindJSON 错误 → 统一 details（api.md §4：校验失败必须带 details）。
//   - validator 校验失败 → {"reason": BindErrorReason, "fields": {字段名: 固定文案}}；
//   - JSON 语法错误/类型不符等其他失败 → {"reason": BindErrorReason}。
//
// 各域 bind 助手（masterdata/purchase bindJSON、warehouse bindInput 等）与直写
// ShouldBindJSON 的 handler 统一经此收敛，禁止再以 `"reason": err.Error()` 直出。
func BindErrorDetails(err error) gin.H {
	fields := map[string]string{}
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		for _, fe := range ve {
			msg, ok := bindTagMessages[fe.Tag()]
			if !ok {
				msg = "格式不正确"
			}
			fields[strings.ToLower(fe.Field())] = msg
		}
	}
	if len(fields) == 0 {
		return gin.H{"reason": BindErrorReason}
	}
	return gin.H{"reason": BindErrorReason, "fields": fields}
}
