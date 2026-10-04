package masterdata

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 基础资料域业务层（architecture.md §1：业务逻辑/状态机/事务边界/校验）。
// 依赖以窄接口注入（Repository），单元测试以接口替身替换（ask 约束：不依赖 PostgreSQL）。
//
// 数据权限说明（backend-m1-plan §7.4）：masterdata 为组织级数据（商品/SKU/分类/单位/
// 供应商/客户不分仓），不做仓库级行过滤；仓库/部门/本人范围在 M1 只落模型与快照。
type Service struct {
	repo Repository
	// supplierRefs/customerRefs 往来单位删除引用校验（refreaders.go 窄接口；router 注入，
	// nil 时删除引用校验跳过——fail-open 口径见 refreaders.go 文件注）。
	supplierRefs SupplierRefReader
	customerRefs CustomerRefReader
}

// NewService 构建业务层。
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因）。
type Actor struct {
	UserID    int64
	Username  string
	IsSuper   bool
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// auditEntry 构造 masterdata 域审计条目骨架（module=masterdata；快照由调用方补充）。
// 成功与否、错误码由调用方按结果填充（backend-m1-plan §4.4：审计与业务同事务）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "masterdata",
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

// ---- 输入校验（api.md §4：后端完整校验，禁止只靠前端 required）----

var (
	// codeRe 业务编码：1-64 位字母/数字开头，可含字母/数字/下划线/连字符
	// （products/skus/barcodes 对应列 varchar(64)，product_categories/suppliers/customers 同）。
	codeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// unitCodeRe 单位编码：列 varchar(32)。
	unitCodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	// barcodeTypeRe 条码类型：列 varchar(32)，大写字母/数字/连字符。
	barcodeTypeRe = regexp.MustCompile(`^[A-Z][A-Z0-9-]{0,31}$`)
	// emailRe 宽松邮箱格式（非空才校验）。
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	// phoneRe 电话：数字/加号/连字符/空格/括号，1-32（列 varchar(32)）。
	phoneRe = regexp.MustCompile(`^[0-9+()\-\s]{1,32}$`)
)

func invalidParam(field, reason string) *response.Error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

// validateCode 业务编码校验（unit=true 走 32 位上限）。
func validateCode(field, v string, unit bool) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return invalidParam(field, "必填")
	}
	if unit {
		if !unitCodeRe.MatchString(v) {
			return invalidParam(field, "1-32 位，字母或数字开头，可含字母/数字/下划线/连字符")
		}
		return nil
	}
	if !codeRe.MatchString(v) {
		return invalidParam(field, "1-64 位，字母或数字开头，可含字母/数字/下划线/连字符")
	}
	return nil
}

// validateName 名称校验（必填 + 长度上限对齐列宽）。
func validateName(field, v string, maxLen int) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return invalidParam(field, "必填")
	}
	if len([]rune(v)) > maxLen {
		return invalidParam(field, "长度不能超过 "+itoa(maxLen))
	}
	return nil
}

// validateOptionalText 可选文本长度校验（空串合法）。
func validateOptionalText(field, v string, maxLen int) error {
	if len([]rune(v)) > maxLen {
		return invalidParam(field, "长度不能超过 "+itoa(maxLen))
	}
	return nil
}

// validatePhoneEmail 联系电话/邮箱校验（空串合法；business-flow §1.4/§1.5 联系字段）。
func validatePhoneEmail(field, v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if field == "email" {
		if len(v) > 128 || !emailRe.MatchString(v) {
			return invalidParam(field, "邮箱格式不正确")
		}
		return nil
	}
	if !phoneRe.MatchString(v) {
		return invalidParam(field, "1-32 位，可含数字/加号/连字符/括号/空格")
	}
	return nil
}

// validateImageURLs 图片 URL 列表校验（非空项 ≤512 字符、≤20 张；M1 仅承载 URL
// 字段本身，上传接口随阶段 14 文件中心交付，backend-m1-plan §9）。
func validateImageURLs(urls []string) error {
	if len(urls) > 20 {
		return invalidParam("image_urls", "最多 20 张")
	}
	for i, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			return invalidParam("image_urls", "第 "+itoa(i+1)+" 项为空")
		}
		if len(u) > 512 {
			return invalidParam("image_urls", "第 "+itoa(i+1)+" 项超过 512 字符")
		}
	}
	return nil
}

// validateNonNegative 数量/金额非负校验（number.go：基础资料数量/金额字段全部 ≥0）。
func validateNonNegative(field string, n Number) error {
	v, ok := decScaled(n)
	if !ok {
		return invalidParam(field, "必须为数字且最多 14 位整数、4 位小数")
	}
	if v < 0 {
		return invalidParam(field, "不能为负数")
	}
	return nil
}

// validateStockRange 最大库存 ≥ 安全库存（api.md §4 范围校验：max_stock>0 时强制）。
func validateStockRange(safety, max Number) error {
	maxV, _ := decScaled(max)
	if maxV <= 0 {
		return nil
	}
	safetyV, _ := decScaled(safety)
	if safetyV > maxV {
		return invalidParam("max_stock", "最大库存不能小于安全库存")
	}
	return nil
}

// validOnOffStatus 通用启停枚举（chk_*_status CHECK 同枚举）。
func validOnOffStatus(v string) bool { return v == StatusEnabled || v == StatusDisabled }

// optionalID 可选外键入参：nil=不设置；≤0 视为非法。
func optionalID(field string, v *int64) error {
	if v != nil && *v <= 0 {
		return invalidParam(field, "必须为正整数")
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }
