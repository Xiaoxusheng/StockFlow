package masterdata

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Number numeric(18,4) 的精确十进制载体（backend-m1-plan §1 全局约定：金额/数量
// numeric(18,4) 禁 float）。全链路均为十进制文本，精度无损：
//   - JSON 进：接受裸数字与字符串两种字面量（前端契约 number），校验后原样保存；
//   - JSON 出：输出裸数字（如 12.3400，JSON 解析端得到 number，与前端类型契约一致）；
//   - DB 进/出：driver.Valuer/Scanner 承载数值文本，由 PostgreSQL numeric 编解码，
//     列 typmod(18,4) 兜底范围与标度。
//
// 约束：整数位 ≤14、小数位 ≤4（numeric(18,4) 全量可表示）；负号语法上可解析，
// 语义上由 Service 按字段校验（基础资料的数量/金额字段全部 ≥0）。
type Number string

// decRe 十进制字面量（纯小数记法，拒绝指数/前导符号等）。
var decRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// maxIntDigits/maxFracDigits numeric(18,4)：14 位整数 + 4 位小数。
const (
	maxIntDigits  = 14
	maxFracDigits = 4
)

// validateDecimal 校验十进制字面量：整数位（去前导零后）≤14、小数位 ≤4。
func validateDecimal(s string) bool {
	if !decRe.MatchString(s) {
		return false
	}
	intPart, fracPart, hasFrac := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	if ip := strings.TrimLeft(intPart, "0"); len(ip) > maxIntDigits {
		return false
	}
	if hasFrac && len(fracPart) > maxFracDigits {
		return false
	}
	return true
}

// MarshalJSON 输出裸数字；零值（空串）输出 0。非法值报错而非静默改写。
func (n Number) MarshalJSON() ([]byte, error) {
	s := strings.TrimSpace(string(n))
	if s == "" {
		return []byte("0"), nil
	}
	if !validateDecimal(s) {
		return nil, fmt.Errorf("非法十进制值: %q", s)
	}
	return []byte(s), nil
}

// UnmarshalJSON 接受裸数字、数字字符串与 null（null/空 → "0"）。
func (n *Number) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*n = "0"
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	if s == "" {
		*n = "0"
		return nil
	}
	if !validateDecimal(s) {
		return fmt.Errorf("必须为数字且最多 %d 位整数、%d 位小数: %q", maxIntDigits, maxFracDigits, s)
	}
	*n = Number(s)
	return nil
}

// Value 零值写 0（列 NOT NULL DEFAULT 0，禁 NULL）。
func (n Number) Value() (driver.Value, error) {
	s := strings.TrimSpace(string(n))
	if s == "" {
		return "0", nil
	}
	return s, nil
}

// Scan 支持驱动返回的十进制文本（pgx 对 numeric 的 driver.Value 为 string），
// 兼容 []byte/float64 路径；nil → "0"。
func (n *Number) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*n = "0"
	case []byte:
		*n = Number(strings.TrimSpace(string(v)))
	case string:
		*n = Number(strings.TrimSpace(v))
	case float64:
		*n = Number(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return fmt.Errorf("Number.Scan 不支持类型 %T", src)
	}
	return nil
}

// decScaled 将已通过 validateDecimal 的文本无损换算为 ×1e4 的 int64（14+4=18 位
// 十进制在 int64 范围内），供 Service 做精确大小比较（禁 float 比较）。
func decScaled(n Number) (int64, bool) {
	s := strings.TrimSpace(string(n))
	if s == "" {
		return 0, true
	}
	if !validateDecimal(s) {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, fracPart, _ := strings.Cut(s, ".")
	fracPart = (fracPart + "0000")[:maxFracDigits]
	v, err := strconv.ParseInt(intPart+fracPart, 10, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}
