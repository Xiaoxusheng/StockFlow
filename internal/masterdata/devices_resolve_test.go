package masterdata

// devices_resolve.go 的 SFQR 载荷查证测试（qr-code.md §6）：FindBySkuCode 命中/
// 软删未命中/停用三态。Raw SELECT 路径经 fakedb_test.go 可编程查询夹具承载
// （按 SQL 文本路由配置行——ask 约束：单测不依赖 PostgreSQL）。

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// withQueryFixture 设置可编程查询夹具（用例结束恢复，避免跨用例泄漏）。
func withQueryFixture(t *testing.T, fn func(query string) (cols []string, rows [][]driver.Value)) {
	t.Helper()
	saved := fakeQueryFixture
	fakeQueryFixture = fn
	t.Cleanup(func() { fakeQueryFixture = saved })
}

func TestFindBySkuCode(t *testing.T) {
	db, err := openTestGorm(nil)
	require.NoError(t, err)
	svc := NewSKUBarcodeReader(db)
	ctx := context.Background()

	// 命中启用 SKU：Hit 形态（ID/Code=sku_code/Name=product_name/Status）。
	withQueryFixture(t, func(query string) ([]string, [][]driver.Value) {
		if strings.Contains(query, "skus.id AS id") {
			return []string{"id", "sku_code", "product_name", "is_enabled"},
				[][]driver.Value{{int64(7), "SKU-001", "商品一", true}}
		}
		return nil, nil
	})
	hit, found, err := svc.FindBySkuCode(ctx, "SKU-001")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(7), hit.ID)
	require.Equal(t, "SKU-001", hit.Code)
	require.Equal(t, "商品一", hit.Name)
	require.Equal(t, "ENABLED", hit.Status)

	// 命中停用 SKU：Status=DISABLED（停用转 SKU_NOT_FOUND 由消费方 devices 侧负责）。
	withQueryFixture(t, func(query string) ([]string, [][]driver.Value) {
		if strings.Contains(query, "skus.id AS id") {
			return []string{"id", "sku_code", "product_name", "is_enabled"},
				[][]driver.Value{{int64(8), "SKU-OFF", "商品二", false}}
		}
		return nil, nil
	})
	hit, found, err = svc.FindBySkuCode(ctx, "SKU-OFF")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "DISABLED", hit.Status)

	// 软删未命中（skus.deleted_at IS NULL 过滤后 0 行）→ found=false。
	withQueryFixture(t, func(query string) ([]string, [][]driver.Value) {
		if strings.Contains(query, "skus.id AS id") {
			return []string{"id", "sku_code", "product_name", "is_enabled"}, [][]driver.Value{}
		}
		return nil, nil
	})
	hit, found, err = svc.FindBySkuCode(ctx, "SKU-DELETED")
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, hit.ID)
}
