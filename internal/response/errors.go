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
	"sync"
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
