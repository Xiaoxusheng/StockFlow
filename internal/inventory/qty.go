package inventory

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Qty 库存数量：numeric(18,4) 的精确十进制载体（backend-m1-plan §1 全局约定：禁 float，
// 数量恒等式比较因此无浮点误差）。内部为 int64，单位 0.0001（即值 ×10000），
// 加减比较全部整数运算、无精度损失。
//
// 线格式（与 internal/masterdata.Number 的域内实现保持同一对外约定；域间禁止 import，
// 各域按 plan §1 统一约定各自实现载体）：
//   - JSON 出：裸数字（如 12.3400，解析端得到 number）；
//   - JSON 进：接受裸数字与字符串两种字面量；
//   - DB 进/出：driver.Valuer/Scanner 承载数值文本，由 PostgreSQL numeric 编解码，
//     列 typmod(18,4) 兜底范围与标度。
//
// 约束：整数位 ≤14、小数位 ≤4（numeric(18,4) 全量可表示）。负号语法上可解析
// （流水 qty_change 为负），语义上由 Service 按操作校验（业务入参量恒为正）。
type Qty int64

// qtyScale 精确十进制标度：1 单位 Qty 表示 0.0001。
const qtyScale int64 = 10000

// qtyRe 十进制字面量（纯小数记法，拒绝指数、前导符号/空白等）。
var qtyRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// ParseQty 解析十进制字面量为 Qty（×10000 取整，拒绝超出 4 位小数或 14 位整数）。
func ParseQty(s string) (Qty, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("数量不能为空")
	}
	if !qtyRe.MatchString(s) {
		return 0, fmt.Errorf("数量必须为十进制数字（如 12.5）: %q", s)
	}
	intPart, fracPart, _ := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	if len(strings.TrimLeft(intPart, "0")) > 14 {
		return 0, fmt.Errorf("数量整数位超限（numeric(18,4) 最多 14 位）: %q", s)
	}
	if len(fracPart) > 4 {
		return 0, fmt.Errorf("数量小数位超限（最多 4 位）: %q", s)
	}
	digits := strings.Replace(s, ".", "", 1) + strings.Repeat("0", 4-len(fracPart))
	v, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("数量超出 numeric(18,4) 表示范围: %q", s)
	}
	return Qty(v), nil
}

// String 输出 4 位小数的十进制文本（如 "12.3400"、"-5.0000"）。
func (q Qty) String() string {
	neg := q < 0
	v := int64(q)
	if neg {
		v = -v
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%04d", sign, v/qtyScale, v%qtyScale)
}

// MarshalJSON 输出裸数字（线格式与基础资料域一致）。
func (q Qty) MarshalJSON() ([]byte, error) {
	return []byte(q.String()), nil
}

// UnmarshalJSON 接受裸数字与字符串两种字面量。
func (q *Qty) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*q = 0
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	v, err := ParseQty(s)
	if err != nil {
		return err
	}
	*q = v
	return nil
}

// Value 实现 driver.Valuer：以十进制文本交由 pgx 的 numeric 编解码。
func (q Qty) Value() (driver.Value, error) { return q.String(), nil }

// Scan 支持 pgx 返回的 string / []byte / int64 / float64 / nil。
// float64 仅作兜底（驱动不会对 numeric 返回 float，防御性接受并按标度就近取整）。
func (q *Qty) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*q = 0
	case string:
		parsed, err := ParseQty(v)
		if err != nil {
			return err
		}
		*q = parsed
	case []byte:
		parsed, err := ParseQty(string(v))
		if err != nil {
			return err
		}
		*q = parsed
	case int64:
		*q = Qty(v * qtyScale)
	case float64:
		if v < 0 {
			*q = Qty(int64(v*float64(qtyScale) - 0.5))
		} else {
			*q = Qty(int64(v*float64(qtyScale) + 0.5))
		}
	default:
		return fmt.Errorf("Qty.Scan 不支持类型 %T", src)
	}
	return nil
}

// Add / Sub 加减（整数运算，恒等式推导的基础）。
func (q Qty) Add(o Qty) Qty { return q + o }
func (q Qty) Sub(o Qty) Qty { return q - o }

// Neg 取相反数（流水 qty_change 的方向翻转）。
func (q Qty) Neg() Qty { return -q }

// IsZero / IsPositive / IsNegative 符号判定。
func (q Qty) IsZero() bool     { return q == 0 }
func (q Qty) IsPositive() bool { return q > 0 }
func (q Qty) IsNegative() bool { return q < 0 }
