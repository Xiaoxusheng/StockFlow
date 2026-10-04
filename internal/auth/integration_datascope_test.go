//go:build integration

// 数据权限过滤集成链路专项（testing.md §8 专项 6，清偿项 F7）：
// 两个不同仓库绑定的 SPECIFIED_WAREHOUSE 账号 + 双仓库存/单据数据，
// 断言 A 仓账号经「真实登录 → Redis 会话 → AuthRequired → WarehouseScope →
// 真实 PG 查询」全链路后只能看到 A 仓数据（B 仓行被数据权限过滤，空集语义）。
// 复用 integration_test.go 的 fixture（integrationEnv / setWiredForTest / testCfg）。

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
)

// TestIntegrationWarehouseDataScopeFiltering 越权访问他人仓库数据 → 数据权限过滤生效。
func TestIntegrationWarehouseDataScopeFiltering(t *testing.T) {
	db, rdb := integrationEnv(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := database.BootstrapIfEmpty(db, "SfAdmin2026")
		require.NoError(t, err)
	}
	resetWiredForTest()
	t.Cleanup(resetWiredForTest)
	cfg := testCfg()
	setWiredForTest(cfg, NewRepository(db), rdb)
	_, _, _, _, svc, _, ok := snapshotWired()
	require.True(t, ok)

	// 双仓：编码带纳秒后缀保证重复执行不撞唯一索引。
	uniq := time.Now().UnixNano()
	whA, whB := int64(0), int64(0)
	require.NoError(t, db.WithContext(ctx).Raw(fmt.Sprintf(
		`INSERT INTO warehouses (code, name, status, type) VALUES ('WH-DS-A-%d','数据权限A仓','ENABLED','NORMAL') RETURNING id`, uniq),
	).Row().Scan(&whA))
	require.NoError(t, db.WithContext(ctx).Raw(fmt.Sprintf(
		`INSERT INTO warehouses (code, name, status, type) VALUES ('WH-DS-B-%d','数据权限B仓','ENABLED','NORMAL') RETURNING id`, uniq),
	).Row().Scan(&whB))

	// 双账号：A 绑定 A 仓、B 绑定 B 仓（真实 user_warehouses 绑定行）。
	actor := Actor{UserID: 1, Username: "admin", IsSuper: true}
	ua, err := svc.CreateUser(ctx, actor, UserCreateInput{
		Username: fmt.Sprintf("wh_scope_a_%d", uniq), Password: "ScopeA2026!",
		RealName: "A仓用户", DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{whA},
	})
	require.NoError(t, err)
	ub, err := svc.CreateUser(ctx, actor, UserCreateInput{
		Username: fmt.Sprintf("wh_scope_b_%d", uniq), Password: "ScopeB2026!",
		RealName: "B仓用户", DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{whB},
	})
	require.NoError(t, err)

	// B 仓数据：库存一行 + 库存调整单一张（A 仓各放一行/一张做对照）。
	// 跨域不建外键（plan §5）：sku_id 裸值即可，不必先建 SKU。
	const sku = int64(990001)
	require.NoError(t, db.WithContext(ctx).Exec(
		`INSERT INTO inventory (warehouse_id, zone_id, shelf_id, bin_id, sku_id, total_qty, available_qty)
		 VALUES (?, 1, 1, 1, ?, 10, 10)`, whA, sku).Error)
	require.NoError(t, db.WithContext(ctx).Exec(
		`INSERT INTO inventory (warehouse_id, zone_id, shelf_id, bin_id, sku_id, total_qty, available_qty)
		 VALUES (?, 1, 1, 1, ?, 20, 20)`, whB, sku).Error)
	require.NoError(t, db.WithContext(ctx).Exec(fmt.Sprintf(
		`INSERT INTO inventory_adjustments (adjustment_no, warehouse_id, sku_id, bin_id, adjust_type, qty, reason)
		 VALUES ('ADJ-DS-A-%d', ?, ?, 1, '盘盈', 1, '集成测试')`, uniq), whA, sku).Error)
	require.NoError(t, db.WithContext(ctx).Exec(fmt.Sprintf(
		`INSERT INTO inventory_adjustments (adjustment_no, warehouse_id, sku_id, bin_id, adjust_type, qty, reason)
		 VALUES ('ADJ-DS-B-%d', ?, ?, 1, '盘亏', 1, '集成测试')`, uniq), whB, sku).Error)

	// 探针路由：AuthRequired 会话链 + ApplyWarehouseScope 数据权限过滤，
	// 回显过滤后可见的仓库集（库存/单据各一计数）——等价业务域 handler 的过滤接线；
	// ?warehouse_id= 叠加显式过滤（对齐 inventory 列表「范围注入 AND 参数过滤」语义）。
	r := gin.New()
	r.GET("/__probe_scope", AuthRequired(), func(c *gin.Context) {
		inv := ApplyWarehouseScope(c, db.WithContext(c.Request.Context()), "warehouse_id")
		adj := ApplyWarehouseScope(c, db.WithContext(c.Request.Context()), "warehouse_id")
		if raw := c.Query("warehouse_id"); raw != "" {
			inv = inv.Where("warehouse_id = ?", raw)
		}
		var invWh, adjWh []int64
		require.NoError(t, inv.Raw(`SELECT DISTINCT warehouse_id FROM inventory`).Scan(&invWh).Error)
		require.NoError(t, adj.Raw(`SELECT DISTINCT warehouse_id FROM inventory_adjustments`).Scan(&adjWh).Error)
		c.JSON(http.StatusOK, gin.H{"inventory": invWh, "adjustments": adjWh})
	})

	// 真实登录拿 Access Token。
	login := func(username, password string) string {
		res, err := svc.Login(ctx, LoginInput{Username: username, Password: password, IP: "127.0.0.1", UserAgent: "integration"})
		require.NoError(t, err)
		return res.AccessToken
	}
	tokenA := login(ua.Username, "ScopeA2026!")
	tokenB := login(ub.Username, "ScopeB2026!")

	get := func(token, query string) map[string][]int64 {
		req := httptest.NewRequest(http.MethodGet, "/__probe_scope"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		var body map[string][]int64
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body
	}

	// A 仓账号：只见 A 仓库存/单据（B 仓行被过滤）。
	seenA := get(tokenA, "")
	require.Equal(t, []int64{whA}, seenA["inventory"], "A 仓账号库存可见集应仅 A 仓")
	require.Equal(t, []int64{whA}, seenA["adjustments"], "A 仓账号单据可见集应仅 A 仓")

	// B 仓账号：只见 B 仓（对称验证过滤随账号绑定走）。
	seenB := get(tokenB, "")
	require.Equal(t, []int64{whB}, seenB["inventory"], "B 仓账号库存可见集应仅 B 仓")
	require.Equal(t, []int64{whB}, seenB["adjustments"], "B 仓账号单据可见集应仅 B 仓")

	// 用户 ID 归属复核（会话快照确为两独立账号，非同一人双仓）。
	require.NotEqual(t, ua.ID, ub.ID)

	// 越权直查 B 仓：A 账号显式指名 B 仓（warehouse_id=whB 参数），范围过滤叠加后为空集——
	// 越权请求得不到任何 B 仓行（testing.md §8 专项 6 的 fail-closed 语义）。
	cross := get(tokenA, fmt.Sprintf("?warehouse_id=%d", whB))
	require.Empty(t, cross["inventory"], "A 仓账号指名 B 仓查询必须为空集")
	require.Empty(t, cross["adjustments"], "A 仓账号指名 B 仓查询单据必须为空集")
}
