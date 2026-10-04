package purchase

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/stockflow/server/internal/stock"
)

// 域内值类型与纯函数助手（数量/金额运算、jsonb 载体）。
// 数量载体统一用 stock.Qty（backend-m2-plan §3 契约包）；金额列同为 numeric(18,4)，
// 复用同一精确十进制载体，不另造第二套（判据：禁止另造第二套实现）。

// StringList jsonb 字符串数组载体（quality_orders.image_refs——文件中心阶段 14 前
// 仅占位列，不提供上传校验，plan §5/§12）。
type StringList []string

// Value 实现 driver.Valuer：nil → SQL NULL，其余为已编码 JSON 数组。
func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	b, err := json.Marshal([]string(l))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 支持 pgx 返回的 []byte/string/nil。
func (l *StringList) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*l = nil
	case []byte:
		return l.parse(string(v))
	case string:
		return l.parse(v)
	default:
		return fmt.Errorf("StringList.Scan 不支持类型 %T", src)
	}
	return nil
}

func (l *StringList) parse(s string) error {
	if strings.TrimSpace(s) == "" {
		*l = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return fmt.Errorf("StringList.Scan 解析失败: %w", err)
	}
	*l = out
	return nil
}

// mulAmount 金额计算：amount = qty × price（numeric(18,4)×numeric(18,4) 缩放，
// 经 big.Int 中转避免 int64 溢出；超出 numeric(18,4) 可表示范围返回 false）。
func mulAmount(qty, price stock.Qty) (stock.Qty, bool) {
	neg := false
	a, b := big.NewInt(int64(qty)), big.NewInt(int64(price))
	if a.Sign() < 0 {
		neg = !neg
		a.Neg(a)
	}
	if b.Sign() < 0 {
		neg = !neg
		b.Neg(b)
	}
	p := new(big.Int).Mul(a, b)
	p.Div(p, big.NewInt(10000)) // 消去一份 10^4 标度（qty×price 为 10^8 标度）
	if !p.IsInt64() {
		return 0, false
	}
	v := p.Int64()
	if neg {
		v = -v
	}
	return stock.Qty(v), true
}

// parseIntQty 校验数量为非负整数（序列号 SKU 按件作业，business-flow §5.2 逐件确认）。
func isIntegerQty(q stock.Qty) bool { return q%10000 == 0 }
