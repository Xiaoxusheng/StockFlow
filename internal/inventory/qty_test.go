package inventory

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Qty 精确十进制载体单元测试（不依赖 PG/Redis/网络）。

func TestParseQty(t *testing.T) {
	cases := []struct {
		in   string
		want Qty
		err  bool
	}{
		{"0", 0, false},
		{"12", 120000, false},
		{"12.5", 125000, false},
		{"0.0001", 1, false},
		{"-5.25", -52500, false},
		{"12.3400", 123400, false},
		{"99999999999999.9999", 999999999999999999, false}, // numeric(18,4) 上界
		{"", 0, true},
		{"abc", 0, true},
		{"1e3", 0, true},             // 拒绝指数
		{"+1.5", 0, true},            // 拒绝前导符号
		{"1.23456", 0, true},         // 小数位超限
		{"1 2", 0, true},             // 非法字符
		{"100000000000000", 0, true}, // 整数位超限（15 位）
	}
	for _, tc := range cases {
		got, err := ParseQty(tc.in)
		if tc.err {
			require.Error(t, err, "input %q 应失败", tc.in)
			continue
		}
		require.NoError(t, err, "input %q 不应失败", tc.in)
		require.Equal(t, tc.want, got, "input %q", tc.in)
	}
}

func TestQtyStringRoundTrip(t *testing.T) {
	for _, s := range []string{"0.0000", "12.3400", "-5.2500", "99999999999999.9999"} {
		q, err := ParseQty(s)
		require.NoError(t, err)
		require.Equal(t, s, q.String())
	}
}

func TestQtyJSON(t *testing.T) {
	// 出：裸数字（与基础资料域线格式一致）。
	b, err := json.Marshal(Qty(123400))
	require.NoError(t, err)
	require.Equal(t, "12.3400", string(b))

	// 进：裸数字与字符串两种字面量。
	var fromNumber Qty
	require.NoError(t, json.Unmarshal([]byte(`12.34`), &fromNumber))
	require.Equal(t, Qty(123400), fromNumber)

	var fromString Qty
	require.NoError(t, json.Unmarshal([]byte(`"12.34"`), &fromString))
	require.Equal(t, Qty(123400), fromString)

	var fromNull Qty
	require.NoError(t, json.Unmarshal([]byte(`null`), &fromNull))
	require.Equal(t, Qty(0), fromNull)

	var bad Qty
	require.Error(t, json.Unmarshal([]byte(`"1e3"`), &bad))
	require.Error(t, json.Unmarshal([]byte(`"1.23456"`), &bad))
}

func TestQtyArithmeticAndSigns(t *testing.T) {
	a, _ := ParseQty("10")
	b, _ := ParseQty("4")
	c, _ := ParseQty("0.0002")
	require.Equal(t, Qty(140002), a.Add(b).Add(c))
	require.Equal(t, Qty(60000), a.Sub(b))
	require.Equal(t, Qty(-60000), a.Sub(b).Neg())
	require.True(t, a.IsPositive())
	require.True(t, b.Sub(a).IsNegative())
	require.True(t, Qty(0).IsZero())
	require.False(t, Qty(-1).IsPositive())
}

func TestQtyScanValue(t *testing.T) {
	// Value：十进制文本交由 pgx numeric 编解码。
	q, _ := ParseQty("7.5")
	v, err := q.Value()
	require.NoError(t, err)
	require.Equal(t, "7.5000", v)

	// Scan：string / []byte / int64 / float64 / nil。
	var s Qty
	require.NoError(t, s.Scan("7.5"))
	require.Equal(t, Qty(75000), s)

	var b Qty
	require.NoError(t, b.Scan([]byte("0.0001")))
	require.Equal(t, Qty(1), b)

	var i Qty
	require.NoError(t, i.Scan(int64(3)))
	require.Equal(t, Qty(30000), i)

	var f Qty
	require.NoError(t, f.Scan(float64(2.5)))
	require.Equal(t, Qty(25000), f)

	var n Qty
	require.NoError(t, n.Scan(nil))
	require.Equal(t, Qty(0), n)

	var bad Qty
	require.Error(t, bad.Scan(struct{}{}))
}
