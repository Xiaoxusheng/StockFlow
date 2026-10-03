package masterdata

// Number（numeric(18,4) 载体，backend-m1-plan §1 全局约定：禁 float）编解码与校验测试。

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumberJSONRoundTrip(t *testing.T) {
	// 裸数字与字符串入参 → 原样保存；出参为裸数字
	var n Number
	require.NoError(t, n.UnmarshalJSON([]byte(`12.3400`)))
	require.Equal(t, Number("12.3400"), n)
	out, err := n.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `12.3400`, string(out))

	require.NoError(t, n.UnmarshalJSON([]byte(`"5.5"`)))
	require.Equal(t, Number("5.5"), n)

	// null / 空 → 0
	require.NoError(t, n.UnmarshalJSON([]byte(`null`)))
	require.Equal(t, Number("0"), n)
	out, err = n.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `0`, string(out))

	// 非法字面量：指数记法 / 超标度
	require.Error(t, n.UnmarshalJSON([]byte(`1e5`)))
	require.Error(t, n.UnmarshalJSON([]byte(`"1.12345"`))) // 小数位 >4
	require.Error(t, n.UnmarshalJSON([]byte(`"12345678901234567"`)))
}

func TestNumberValueScan(t *testing.T) {
	var n Number
	require.NoError(t, n.Scan("3.1400"))
	v, err := n.Value()
	require.NoError(t, err)
	require.Equal(t, "3.1400", v)

	// 零值写 0（列 NOT NULL DEFAULT 0，禁 NULL）
	var zero Number
	v, err = zero.Value()
	require.NoError(t, err)
	require.Equal(t, "0", v)

	// nil → "0"；[]byte/float64 兼容路径
	require.NoError(t, n.Scan(nil))
	require.Equal(t, Number("0"), n)
	require.NoError(t, n.Scan([]byte("2.5")))
	require.Equal(t, Number("2.5"), n)
	require.NoError(t, n.Scan(float64(7)))
	require.Equal(t, Number("7"), n)
	require.Error(t, n.Scan(true)) // 不支持的类型（bool）
}

func TestNumberDecScaled(t *testing.T) {
	v, ok := decScaled("12.3456")
	require.True(t, ok)
	require.EqualValues(t, 123456, v)

	v, ok = decScaled("-0.5")
	require.True(t, ok)
	require.EqualValues(t, -5000, v)

	v, ok = decScaled("")
	require.True(t, ok)
	require.EqualValues(t, 0, v)

	_, ok = decScaled("1e5")
	require.False(t, ok)
}
