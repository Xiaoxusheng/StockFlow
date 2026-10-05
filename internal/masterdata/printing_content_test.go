package masterdata

// printing_content.go 的 SKU 标签装配测试（qr-code.md §9 不可打印校验 + §7.3
// values["sku_code"] 恒产出契约）。Raw SELECT 装配路径经 fakedb_test.go 可编程
// 查询夹具承载（按 SQL 文本路由配置行——ask 约束：单测不依赖 PostgreSQL）。

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/response"
)

// fixtureAssembleRows 装配两条 Raw 查询的配置行：主查询（FROM skus s）与条码查询
// （FROM barcodes）——列签名路由，对齐 Assemble 内两次 Raw 的 SELECT 文本特征。
func fixtureAssembleRows(skuRows [][]driver.Value, barcodeRows [][]driver.Value) func(string) ([]string, [][]driver.Value) {
	return func(query string) ([]string, [][]driver.Value) {
		switch {
		case strings.Contains(query, "s.id AS sku_id"):
			return []string{"sku_id", "sku_code", "product_name", "spec", "unit_name", "is_enabled"}, skuRows
		case strings.Contains(query, "FROM barcodes"):
			return []string{"sku_id", "barcode", "is_primary"}, barcodeRows
		}
		return nil, nil
	}
}

// TestAssembleSKU_DisabledRejected 命中停用 SKU → 整体拒绝 PRINT_SKU_DISABLED，
// details.disabled_ids 携停用 SKU 编码（qr-code.md §9；约束 10「商品已停用」）。
func TestAssembleSKU_DisabledRejected(t *testing.T) {
	db, err := openTestGorm(nil)
	require.NoError(t, err)
	r := NewSKUContentReader(db)

	withQueryFixture(t, fixtureAssembleRows(
		[][]driver.Value{
			{int64(1), "SKU-OK", "商品一", "500ml", "瓶", true},
			{int64(2), "SKU-OFF", "商品二", "500ml", "瓶", false},
		},
		[][]driver.Value{{int64(1), "6901234567890", true}},
	))
	_, err = r.Assemble(context.Background(), []string{"1", "2"}, []string{"sku_code", "product_name"})
	require.Equal(t, "PRINT_SKU_DISABLED", codeOf(t, err))
	var bizErr *response.Error
	require.ErrorAs(t, err, &bizErr)
	details, ok := bizErr.Details.(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"SKU-OFF"}, details["disabled_ids"], "disabled_ids 携停用 SKU 编码")
}

// TestAssembleSKU_MissingBeforeDisabled 数据完整性优先于可打印性：缺失与停用并存
// → 先报 PRINT_DATA_NOT_FOUND（装配契约：任一对象缺失整体拒绝，plan §7.2）。
func TestAssembleSKU_MissingBeforeDisabled(t *testing.T) {
	db, err := openTestGorm(nil)
	require.NoError(t, err)
	r := NewSKUContentReader(db)

	// id=3 缺行（不存在）、id=2 停用。
	withQueryFixture(t, fixtureAssembleRows(
		[][]driver.Value{
			{int64(1), "SKU-OK", "商品一", "500ml", "瓶", true},
			{int64(2), "SKU-OFF", "商品二", "500ml", "瓶", false},
		},
		[][]driver.Value{},
	))
	_, err = r.Assemble(context.Background(), []string{"1", "2", "3"}, nil)
	require.Equal(t, "PRINT_DATA_NOT_FOUND", codeOf(t, err))
}

// TestAssembleSKU_ValuesAlwaysContainSkuCode values["sku_code"] 恒产出（qr-code.md
// §7.3：SFQR 载荷构造依据，不受模板绑定影响）；其余键仍按绑定装配。
func TestAssembleSKU_ValuesAlwaysContainSkuCode(t *testing.T) {
	db, err := openTestGorm(nil)
	require.NoError(t, err)
	r := NewSKUContentReader(db)

	withQueryFixture(t, fixtureAssembleRows(
		[][]driver.Value{{int64(1), "SKU-OK", "商品一", "500ml", "瓶", true}},
		[][]driver.Value{{int64(1), "6901234567890", true}},
	))

	// 模板未绑定任何字段：sku_code 仍产出，未绑定的 product_name 不出现。
	rows, err := r.Assemble(context.Background(), []string{"1"}, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "SKU-OK", rows[0].Values["sku_code"])
	require.NotContains(t, rows[0].Values, "product_name")
	require.Equal(t, "6901234567890", rows[0].Code, "主码内容=主条码（printing.md §4.2）")

	// 绑定 product_name：sku_code 恒在 + 绑定键出现。
	rows, err = r.Assemble(context.Background(), []string{"1"}, []string{"product_name"})
	require.NoError(t, err)
	require.Equal(t, "SKU-OK", rows[0].Values["sku_code"])
	require.Equal(t, "商品一", rows[0].Values["product_name"])
}
