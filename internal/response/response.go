package response

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// envelope 统一响应信封（api.md §2.2）。成功 code=0（数字），失败为字符串错误码；
// details 仅失败时出现（omitempty，plan §1：成功 code=0 且无 details）。
type envelope struct {
	Code      any    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details,omitempty"`
}

var errLogger *zap.Logger

// SetErrorLogger 注入错误日志（main 装配时调用一次）：Err 遇到非业务错误时
// 记录完整错误用于排查，响应只给通用文案，防止内部细节泄漏（architecture.md §6）。
func SetErrorLogger(l *zap.Logger) { errLogger = l }

func requestID(c *gin.Context) string { return c.GetString(RequestIDKey) }

// OK 成功响应：HTTP 200，code=0（数字，前端 client.ts 以 number 类型判定信封）。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{Code: 0, Message: "ok", Data: data, RequestID: requestID(c)})
}

// Page 统一分页结构（api.md §2.1：{page,pageSize,total,items}）。
type Page struct {
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
	Items    any   `json:"items"`
}

// 分页参数边界：列表接口强制分页（architecture.md §7，禁无条件全量查询）。
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// ParsePage 解析并校验 page/pageSize。缺省 1/20；越界返回 COMMON_INVALID_PARAM 并带 details。
// 所有列表 handler 必须经此取分页参数，禁止各页自造。
func ParsePage(c *gin.Context) (page, pageSize int, err error) {
	page = DefaultPage
	if raw := c.Query("page"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 {
			return 0, 0, NewError(CodeInvalidParam, gin.H{"field": "page", "reason": "必须为 >=1 的整数"})
		}
		page = n
	}
	pageSize = DefaultPageSize
	if raw := c.Query("pageSize"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > MaxPageSize {
			return 0, 0, NewError(CodeInvalidParam, gin.H{
				"field": "pageSize", "reason": "必须为 1-" + strconv.Itoa(MaxPageSize) + " 的整数",
			})
		}
		pageSize = n
	}
	return page, pageSize, nil
}

// OKPage 分页成功响应（出参 {page,pageSize,total,items}）。
func OKPage(c *gin.Context, items any, page, pageSize int, total int64) {
	OK(c, Page{Page: page, PageSize: pageSize, Total: total, Items: items})
}

// Err 失败响应：业务错误（*Error）按注册的 HTTP 状态、文案与 details 写出；
// 请求体超限（http.MaxBytesReader 的 *MaxBytesError，安全审查 S6）归一为
// 413 COMMON_PAYLOAD_TOO_LARGE——客户端错误，不入内部错误日志；
// 其他 error 一律 500——完整错误仅入日志，响应不暴露内部细节。
func Err(c *gin.Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			e = NewError(CodePayloadTooLarge, gin.H{"limit_bytes": mbe.Limit})
		} else {
			logInternal(c, err)
			e = NewError(CodeInternalError, nil)
		}
	}
	c.JSON(e.code.HTTPStatus, envelope{
		Code:      e.code.Code,
		Message:   e.Message(),
		Data:      nil,
		RequestID: requestID(c),
		Details:   e.Details,
	})
}

// AbortInternal 供中间件（Recovery）使用：写 500 统一信封并终止后续 handler。
func AbortInternal(c *gin.Context) {
	c.AbortWithStatusJSON(CodeInternalError.HTTPStatus, envelope{
		Code:      CodeInternalError.Code,
		Message:   CodeInternalError.Message,
		RequestID: requestID(c),
	})
}

func logInternal(c *gin.Context, err error) {
	if errLogger == nil {
		return // logger 未装配（单测场景）：响应仍正确，日志缺位不阻断
	}
	errLogger.Error("未归类内部错误",
		zap.Error(err),
		zap.String("request_id", requestID(c)),
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path),
	)
}
