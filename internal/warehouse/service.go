package warehouse

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 仓库空间域业务服务：事务边界与业务判断（architecture.md §1）、
// 数据权限过滤（permission.md §4）、操作日志（architecture.md §8）。
// 只依赖 Repository 窄接口——单元测试以内存替身实现，不依赖 PostgreSQL。
type Service struct {
	repo Repository
	occ  BinOccupancyReader // 可选：注入后库位地图携带库存占用（plan §4.3）
}

// ServiceOption Service 构造选项。
type ServiceOption func(*Service)

// WithOccupancyReader 注入库位占用读取实现（inventory 域提供，router 装配）。
func WithOccupancyReader(r BinOccupancyReader) ServiceOption {
	return func(s *Service) { s.occ = r }
}

// NewService 构造业务服务。
func NewService(repo Repository, opts ...ServiceOption) *Service {
	s := &Service{repo: repo}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Actor 操作者归因（handler 自 gin 上下文经 auth.CurrentUser 提取；审计字段来源）。
// Scope 为数据权限仓库范围快照，由 handler 经 auth.WarehouseScope 填入
// （超管在 auth 侧已归一为 All，permission.md §4），Service 不再触碰 gin 上下文。
type Actor struct {
	UserID    int64
	Username  string
	IsSuper   bool // 信息性字段；数据权限一律以 Scope 为准
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
	Scope     Scope
}

// auditEntry 构造 warehouse 域审计条目骨架（module=warehouse；快照由调用方补充）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "warehouse",
		ObjectType:   objectType,
		Action:       action,
		ObjectID:     objectID,
		OperatorID:   a.UserID,
		OperatorName: a.Username,
		RequestID:    a.RequestID,
		IP:           a.IP,
		UserAgent:    a.UserAgent,
		Method:       a.Method,
		Path:         a.Path,
	}
}

// —— 校验规则（api.md §4：后端完整校验，禁止只靠前端 required）——

var (
	codeRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	typeRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
	phoneRe   = regexp.MustCompile(`^[0-9+()\- ]*$`)
	controlRe = regexp.MustCompile(`[[:cntrl:]]`)
)

// 层/列取值上限（货架层数与列数的工程合理上界；防整型滥用，非业务规则）。
const maxGrid = 10000

// invalidParam 构造带字段定位的参数错误（api.md §4：details 指明字段）。
func invalidParam(field, reason string) error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

// validateCode 编码：去空白后 1-64 位，字母/数字开头，可含 _ . -。
func validateCode(field, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", invalidParam(field, "不能为空")
	}
	if !codeRe.MatchString(code) {
		return "", invalidParam(field, "1-64 位，字母或数字开头，仅可含字母/数字/_/./-")
	}
	return code, nil
}

// validateName 名称：去空白后 1-255 字符（按字符数，非字节）。
func validateName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalidParam(field, "不能为空")
	}
	if utf8.RuneCountInString(name) > 255 {
		return "", invalidParam(field, "长度不能超过 255 字符")
	}
	if controlRe.MatchString(name) {
		return "", invalidParam(field, "不能包含控制字符")
	}
	return name, nil
}

// validateTypeCode 类型值格式校验（值域开放：随业务扩展由应用层校验，000004 注释）。
func validateTypeCode(field, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", invalidParam(field, "不能为空")
	}
	if !typeRe.MatchString(v) {
		return "", invalidParam(field, "1-32 位大写字母/数字/下划线，字母开头")
	}
	return v, nil
}

// validateContactInfo 联系人与联系方式（长度 + 电话字符白名单；空串合法 = 未填写）。
func validateContactInfo(contact, phone string) (string, string, error) {
	contact = strings.TrimSpace(contact)
	phone = strings.TrimSpace(phone)
	if utf8.RuneCountInString(contact) > 64 {
		return "", "", invalidParam("contact", "长度不能超过 64 字符")
	}
	if len(phone) > 32 {
		return "", "", invalidParam("phone", "长度不能超过 32 字符")
	}
	if phone != "" && !phoneRe.MatchString(phone) {
		return "", "", invalidParam("phone", "仅可含数字、+、-、()、空格")
	}
	return contact, phone, nil
}

// validateAddress 地址长度（空串合法）。
func validateAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if utf8.RuneCountInString(addr) > 512 {
		return "", invalidParam("address", "长度不能超过 512 字符")
	}
	return addr, nil
}

// validateNonNegative 非负数量（面积/容量；api.md §4 范围校验）。
func validateNonNegative(field string, v float64) error {
	if v < 0 {
		return invalidParam(field, "不能为负数")
	}
	return nil
}

// validateGridPos 层/列位置：1..10000（0/负数与异常大值拒绝）。
func validateGridPos(field string, v int) error {
	if v < 1 || v > maxGrid {
		return invalidParam(field, "必须为 1-"+strconv.Itoa(maxGrid)+" 范围内的整数")
	}
	return nil
}

// validStatus 状态值域（000004 CHECK 同源）。
func validStatus(s string) bool { return s == StatusEnabled || s == StatusDisabled }

// checkScope 范围外数据统一按不存在处理（fail-closed，不泄漏存在性；
// permission.md §4：数据权限过滤在 Service 层）。
func checkScope(scope Scope, warehouseID int64) error {
	if !scope.allows(warehouseID) {
		return response.NewError(response.CodeNotFound, nil)
	}
	return nil
}

// idOf database.ID → int64（Service/Repository 传参约定）。
func idOf(v database.ID) int64 { return v.Int64() }
