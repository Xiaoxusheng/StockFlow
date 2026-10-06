package inventory

// 查询侧 8 条 HTTP 端点的数据驱动表测试（ask：全部 GET 端点补齐含真实业务数据的
// 参数绑定/校验失败/成功路径/数据权限用例；不依赖 PostgreSQL/Redis/网络）。
//
// 测试通路：gin.CreateTestContext 直接驱动 handler 方法（沿用 handler_test.go 模式，
// 不经 auth.RequirePermission——权限点挂载集合由 TestRegisterRoutesAllEndpointsRequirePermission
// 锁定，401/403 鉴权行为属 auth 域中间件职责，integration_test.go:8 同口径注释）；
// 数据访问经 fakedb_test.go 的可编程假驱动按 SQL 子串路由返回配置行，
// 断言面覆盖：过滤参数 → SQL 绑定参数透传（IN 切片展开/时间端点补全/batch_id=0 显式过滤）、
// 行扫描 → 视图转换（ID 字符串化/Qty 裸数字/可空列 null 语义）、统一分页信封。
//
// 数据权限（permission.md §4）：经 auth.CurrentUser 的 gin 键注入用户上下文——
// auth.CurrentUser 只认 "sf_auth_user"（internal/auth/middleware.go:26 ctxUserKey，
// 包内私有常量）；本文件以同字面量镜像注入（ctxUserKeyMirror，先例
// internal/masterdata/handler_http_test.go:43）；若 auth 改键名，越权断言将响亮失败。

import (
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// ctxUserKeyMirror 见文件头注（镜像 internal/auth/middleware.go:26 ctxUserKey）。
const ctxUserKeyMirror = "sf_auth_user"

// reqQTestID 注入每个请求的 request_id（真实路径：middleware → response 信封回显）。
const reqQTestID = "req-query-test"

// ---- 用例支撑 ----

// superUser 全仓数据权限用户（auth.scopeOf → (true, nil)，middleware.go:206-208）。
func superUser() *auth.UserContext {
	return &auth.UserContext{UserID: 9, Username: "query-user", IsSuper: true, DataScope: auth.DataScopeAll}
}

// whUser 指定仓库集数据权限用户（SPECIFIED_WAREHOUSE，middleware.go:209-211）。
func whUser(ids ...int64) *auth.UserContext {
	return &auth.UserContext{UserID: 9, Username: "query-user",
		DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids}
}

// makeQueryCtx 构造带用户上下文与 request_id 的测试上下文（未注入路径参数——
// gin.CreateTestContext 不经路由树，c.Param 需显式注入，见 callPathParamQuery）。
func makeQueryCtx(t *testing.T, target string, uc *auth.UserContext) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set(response.RequestIDKey, reqQTestID)
	if uc != nil {
		c.Set(ctxUserKeyMirror, *uc)
	}
	return rec, c
}

// callQuery 以指定用户上下文驱动一个查询 handler 并解码统一信封。
func callQuery(t *testing.T, call func(*gin.Context), target string, uc *auth.UserContext) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec, c := makeQueryCtx(t, target, uc)
	call(c)
	return rec, decodeEnvelope(t, rec)
}

// callPathParamQuery 驱动带 :id 路径参数的 handler（getInventory/getStockDistribution）；
// 路由匹配本身由 TestRegisterRoutesWiring 的装配测试锁定，此处按参数名注入。
func callPathParamQuery(t *testing.T, call func(*gin.Context), target, id string, uc *auth.UserContext) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec, c := makeQueryCtx(t, target, uc)
	c.Params = gin.Params{{Key: "id", Value: id}}
	call(c)
	return rec, decodeEnvelope(t, rec)
}

// envData 取信封 data 对象（列表信封 {page,pageSize,total,items}）。
func envData(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	require.Equal(t, float64(0), env["code"], "成功用例信封 code 必须为 0: %v", env)
	d, ok := env["data"].(map[string]any)
	require.True(t, ok, "data 必须为对象: %v", env["data"])
	return d
}

// envItems 取列表信封 items 数组（断言空列表为 [] 而非 null——api.md §2.1）。
func envItems(t *testing.T, env map[string]any) []any {
	t.Helper()
	d := envData(t, env)
	items, ok := d["items"].([]any)
	require.True(t, ok, "items 必须为数组（空列表序列化为 [] 而非 null）: %v", d["items"])
	return items
}

// item 取第 i 条视图（map 形态）。
func item(t *testing.T, env map[string]any, i int) map[string]any {
	t.Helper()
	items := envItems(t, env)
	require.Greater(t, len(items), i, "items 长度不足")
	return items[i].(map[string]any)
}

// distNode 取分布树第 i 个节点（data 为数组）。
func distNode(t *testing.T, env map[string]any, i int) map[string]any {
	t.Helper()
	require.Equal(t, float64(0), env["code"], "成功用例信封 code 必须为 0: %v", env)
	d, ok := env["data"].([]any)
	require.True(t, ok, "distribution data 必须为数组: %v", env["data"])
	require.Greater(t, len(d), i, "分布树长度不足")
	return d[i].(map[string]any)
}

// assertArgs 逐位断言捕获的 SQL 绑定参数（gorm IN 切片展开后为展平 int64，
// gorm v1.31.2 statement.go:249-261）。
func assertArgs(t *testing.T, got []driver.Value, want ...driver.Value) {
	t.Helper()
	require.Len(t, got, len(want), "绑定参数个数: %v", got)
	for i := range want {
		require.Equal(t, want[i], got[i], "arg[%d]", i)
	}
}

// queryCall 取第 idx 次捕获（越界即 Fatal）。
func queryCall(t *testing.T, idx int) fakeSQLCall {
	t.Helper()
	calls := fakeCalls()
	require.Greater(t, len(calls), idx, "捕获查询次数不足: %d", len(calls))
	return calls[idx]
}

// assertInvalidParam 断言 400 校验失败信封（COMMON_INVALID_PARAM + details，api.md §4）。
func assertInvalidParam(t *testing.T, rec *httptest.ResponseRecorder, env map[string]any, field string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", env["code"])
	require.NotNil(t, env["details"], "校验失败必须带 details（api.md §4）")
	if field != "" {
		require.Equal(t, field, env["details"].(map[string]any)["field"])
	}
}

// ---- GET /api/inventory（listInventory）----

// inventoryCols 对齐 repository.go:707-711 行查询列序（getInventory 单行清单 :720-726 同）。
var inventoryCols = []string{
	"id", "warehouse_id", "zone_id", "shelf_id", "bin_id", "sku_id", "batch_id",
	"total_qty", "available_qty", "locked_qty", "frozen_qty", "pending_inspect_qty", "defective_qty",
	"created_at", "updated_at", "created_by", "updated_by",
}

// inventoryRow 组一行库存（17 列，时间固定便于断言）。
func inventoryRow(id, wh, zone, shelf, bin, sku, batch int64, total, avail, locked, frozen, pending, defective string) []driver.Value {
	return []driver.Value{
		id, wh, zone, shelf, bin, sku, batch,
		qv(total), qv(avail), qv(locked), qv(frozen), qv(pending), qv(defective),
		tv("2026-10-01 08:30:00"), tv("2026-10-02 09:40:00"), int64(99), int64(99),
	}
}

// stubInventoryList 注册列表两查（COUNT + 行，先注册先匹配）。
func stubInventoryList(total int64, rows [][]driver.Value) {
	fakeStubCount("inventory", total)
	fakeStubQuery([]string{"FROM inventory WHERE", "ORDER BY id"}, inventoryCols, rows)
}

func TestListInventoryData(t *testing.T) {
	t.Run("成功路径_批次行与非批次行_视图与信封", func(t *testing.T) {
		fakeReset(t)
		stubInventoryList(2, [][]driver.Value{
			inventoryRow(1, 1, 11, 111, 101, 5001, 0, "50.0000", "30.0000", "10.0000", "5.0000", "3.0000", "2.0000"),
			inventoryRow(2, 2, 21, 211, 201, 5002, 7, "12.5000", "12.5000", "0", "0", "0", "0"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listInventory, "/api/inventory", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, reqQTestID, env["request_id"], "统一信封 request_id 回显")
		d := envData(t, env)
		require.Equal(t, float64(1), d["page"])
		require.Equal(t, float64(20), d["pageSize"], "缺省分页 1/20（response.go:48-49）")
		require.Equal(t, float64(2), d["total"])

		// 非批次行：六状态数量逐列断言（InventoryView，handler.go:30-41）。
		first := item(t, env, 0)
		require.Equal(t, "1", first["id"], "ID 序列化为字符串（backend-m1-plan §1）")
		require.Equal(t, "1", first["warehouse_id"])
		require.Equal(t, "5001", first["sku_id"])
		require.Equal(t, "0", first["batch_id"], "0=非批次 SKU")
		require.Equal(t, 50.0, first["total_qty"], "Qty 裸数字（stock/qty.go:72）")
		require.Equal(t, 30.0, first["available_qty"])
		require.Equal(t, 10.0, first["locked_qty"])
		require.Equal(t, 5.0, first["frozen_qty"])
		require.Equal(t, 3.0, first["pending_inspect_qty"])
		require.Equal(t, 2.0, first["defective_qty"])
		require.Equal(t, "2026-10-01 08:30:00", first["created_at"], "时间格式 YYYY-MM-DD HH:mm:ss（api.md §2）")

		// 批次行：六状态各分量如实透出。
		second := item(t, env, 1)
		require.Equal(t, "7", second["batch_id"])
		require.Equal(t, 12.5, second["total_qty"])
	})

	t.Run("五维过滤与分页_参数透传SQL", func(t *testing.T) {
		fakeReset(t)
		stubInventoryList(1, [][]driver.Value{
			inventoryRow(2, 2, 21, 211, 201, 5002, 7, "12.5000", "12.5000", "0", "0", "0", "0"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, env := callQuery(t, h.listInventory,
			"/api/inventory?warehouse_id=2&zone_id=3&shelf_id=4&bin_id=5&sku_id=6&batch_id=0&page=2&pageSize=1", superUser())

		require.Equal(t, float64(2), envData(t, env)["page"])
		require.Equal(t, float64(1), envData(t, env)["pageSize"])
		// where() 拼接顺序（repository.go:662-696）：warehouse→zone→shelf→bin→sku→batch。
		assertArgs(t, queryCall(t, 0).args, int64(2), int64(3), int64(4), int64(5), int64(6), int64(0))
		// batch_id=0 为显式过滤（只看非批次行，api.md §9 2026-10-05 联调轮修复）：
		// parseBatchIDQuery（handler.go:192-201）返回 &0，仓储拼 AND batch_id = 0。
		// 行查询参数 = 过滤参数 + [pageSize, offset]（repository.go:715）。
		assertArgs(t, queryCall(t, 1).args, int64(2), int64(3), int64(4), int64(5), int64(6), int64(0), int64(1), int64(1))
		require.Len(t, envItems(t, env), 1)
	})

	t.Run("batch_id缺省不过滤_可见全部含批次行", func(t *testing.T) {
		fakeReset(t)
		stubInventoryList(2, [][]driver.Value{
			inventoryRow(1, 1, 11, 111, 101, 5001, 0, "50.0000", "50.0000", "0", "0", "0", "0"),
			inventoryRow(2, 1, 11, 111, 101, 5001, 7, "9.0000", "9.0000", "0", "0", "0", "0"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, env := callQuery(t, h.listInventory, "/api/inventory", superUser())

		// 不提供 batch_id → parseBatchIDQuery 返回 nil（handler.go:193-195）→ 不拼 batch 条件。
		assertArgs(t, queryCall(t, 0).args)
		require.Len(t, envItems(t, env), 2, "缺省列表必须可见全部库存行，含批次行")
	})

	t.Run("数据权限_指定仓库集收敛与空集fail-closed", func(t *testing.T) {
		fakeReset(t)
		stubInventoryList(1, [][]driver.Value{
			inventoryRow(2, 2, 21, 211, 201, 5002, 7, "12.5000", "12.5000", "0", "0", "0", "0"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		// SPECIFIED_WAREHOUSE ids=[2]：仅可见 wh2（SQL 注入仓库集，非前端传参）。
		_, env := callQuery(t, h.listInventory, "/api/inventory", whUser(2))
		require.Contains(t, queryCall(t, 0).query, "warehouse_id IN (?)",
			"Scope 仓库集强制收敛为 SQL 条件（permission.md §4）")
		assertArgs(t, queryCall(t, 0).args, int64(2))
		require.Len(t, envItems(t, env), 1)
		require.Equal(t, "2", item(t, env, 0)["warehouse_id"])

		// 指定仓库但空集 → where 直接 "1 = 0"（repository.go:666-668 fail-closed）。
		fakeReset(t)
		stubInventoryList(0, nil)
		_, env = callQuery(t, h.listInventory, "/api/inventory", whUser())
		require.Contains(t, queryCall(t, 0).query, "WHERE 1 = 0", "空仓库集必须 fail-closed")
		assertArgs(t, queryCall(t, 0).args)
		require.Equal(t, float64(0), envData(t, env)["total"])
		require.Len(t, envItems(t, env), 0)

		// 未认证直调（无用户上下文）→ WarehouseScope (false,nil)（middleware.go:198-200）→ 同 fail-closed。
		fakeReset(t)
		stubInventoryList(0, nil)
		_, env = callQuery(t, h.listInventory, "/api/inventory", nil)
		require.Contains(t, queryCall(t, 0).query, "WHERE 1 = 0")
		require.Len(t, envItems(t, env), 0)
	})

	t.Run("参数校验失败表驱动_400不触达数据层", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}
		cases := []struct {
			name   string
			target string
			field  string
		}{
			{"非法warehouse_id", "/api/inventory?warehouse_id=abc", "warehouse_id"},
			{"负数bin_id", "/api/inventory?bin_id=-1", "bin_id"},
			{"非法page", "/api/inventory?page=0", "page"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec, env := callQuery(t, h.listInventory, tc.target, superUser())
				assertInvalidParam(t, rec, env, tc.field)
				require.Len(t, fakeCalls(), 0, "校验失败不得触达数据层")
			})
		}
	})

	t.Run("repo错误_500统一信封", func(t *testing.T) {
		fakeReset(t)
		fakeStubError([]string{"SELECT COUNT(*) FROM inventory"}, errors.New("boom"))
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listInventory, "/api/inventory", superUser())

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Equal(t, "COMMON_INTERNAL_ERROR", env["code"], "未归类错误归一 500（response.go:83-98）")
	})
}

// ---- GET /api/inventory/:id（getInventory）----

func TestGetInventoryData(t *testing.T) {
	// 单行查询 stub（getInventory 行 SQL 无 ORDER BY/LIMIT，repository.go:719-734）。
	stubGetInventory := func(rows [][]driver.Value) {
		fakeStubQuery([]string{"FROM inventory WHERE id = ?"}, inventoryCols, rows)
	}

	t.Run("成功路径_单行视图", func(t *testing.T) {
		fakeReset(t)
		stubGetInventory([][]driver.Value{
			inventoryRow(5, 1, 11, 111, 101, 5001, 7, "120.0000", "90.0000", "20.0000", "6.0000", "3.0000", "1.0000"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getInventory, "/api/inventory/5", "5", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		d, ok := env["data"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "5", d["id"])
		require.Equal(t, "101", d["bin_id"])
		require.Equal(t, "7", d["batch_id"])
		require.Equal(t, 120.0, d["total_qty"])
		require.Equal(t, 90.0, d["available_qty"])
		require.Equal(t, 20.0, d["locked_qty"])
		require.Equal(t, "2026-10-02 09:40:00", d["updated_at"])
		// 单行查询按路径 id 绑定参数。
		assertArgs(t, queryCall(t, 0).args, int64(5))
	})

	t.Run("行不存在_404", func(t *testing.T) {
		fakeReset(t)
		stubGetInventory(nil) // 0 行 → row.ID==0 → repo 返回 nil（repository.go:730-733）
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getInventory, "/api/inventory/404", "404", superUser())

		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "COMMON_NOT_FOUND", env["code"])
	})

	t.Run("行仓库越权_fail-closed按不存在", func(t *testing.T) {
		fakeReset(t)
		stubGetInventory([][]driver.Value{
			inventoryRow(5, 1, 11, 111, 101, 5001, 0, "10.0000", "10.0000", "0", "0", "0", "0"),
		})
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		// 用户仅可见 wh2，行在 wh1 → GetInventoryDetail 返回 nil（query.go:56-58）→ 404。
		rec, env := callPathParamQuery(t, h.getInventory, "/api/inventory/5", "5", whUser(2))

		require.Equal(t, http.StatusNotFound, rec.Code, "越权按不存在处理（query.go:47 注释）")
		require.Equal(t, "COMMON_NOT_FOUND", env["code"])
	})

	t.Run("非法路径id_表驱动400", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}
		for _, id := range []string{"abc", "0", "-1"} {
			rec, env := callPathParamQuery(t, h.getInventory, "/api/inventory/"+id, id, superUser())
			assertInvalidParam(t, rec, env, "id")
			require.Len(t, fakeCalls(), 0, "id=%s 校验失败不得触达数据层", id)
		}
	})
}

// ---- GET /api/inventory/:id/distribution（getStockDistribution）----

// stockDistCols 对齐 repository.go:759-763 分布联表列序。
var stockDistCols = []string{
	"warehouse_id", "warehouse_code", "warehouse_name",
	"zone_id", "zone_code", "bin_id", "bin_code", "total_qty", "available_qty",
}

func TestGetStockDistributionData(t *testing.T) {
	// stubDistPair 注册入口行查询 + 分布联表查询两夹具。
	stubDistPair := func(entryRows [][]driver.Value, distRows [][]driver.Value) {
		fakeStubQuery([]string{"FROM inventory WHERE id = ?"}, inventoryCols, entryRows)
		fakeStubQuery([]string{"LEFT JOIN warehouses"}, stockDistCols, distRows)
	}
	// 入口行：id=5，SKU 5001，仓库 1。
	entry := inventoryRow(5, 1, 11, 111, 101, 5001, 7, "7.0000", "7.0000", "0", "0", "0", "0")
	// 分布扁平行：两仓三行（WH-B 同库位两批行各一行——分布维度不含批次，叶聚合）。
	dist := [][]driver.Value{
		{int64(1), sv("WH-A"), sv("甲仓"), int64(11), sv("Z1"), int64(101), sv("B1"), qv("7.0000"), qv("7.0000")},
		{int64(2), sv("WH-B"), sv("乙仓"), int64(22), sv("Z2"), int64(202), sv("B2"), qv("100.0000"), qv("60.0000")},
		{int64(2), sv("WH-B"), sv("乙仓"), int64(22), sv("Z2"), int64(202), sv("B2"), qv("50.0000"), qv("50.0000")},
	}

	t.Run("成功路径_两仓三层树_同库位多批聚合", func(t *testing.T) {
		fakeReset(t)
		stubDistPair([][]driver.Value{entry}, dist)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getStockDistribution, "/api/inventory/5/distribution", "5", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		// 分布查询按入口行 SKU 绑定（repository.go:769 args=[skuID]）。
		assertArgs(t, queryCall(t, 1).args, int64(5001))

		require.Len(t, env["data"].([]any), 2, "两个仓库根节点")

		a := distNode(t, env, 0)
		require.Equal(t, "WH-A", a["warehouse_code"])
		require.Equal(t, "甲仓", a["warehouse_name"])
		require.Equal(t, 7.0, a["total_qty"])
		require.Equal(t, 7.0, a["available_qty"])
		require.NotContains(t, a, "zone_code", "仓库层无库区编码（层级标记，query.go:64）")
		aChildren := a["children"].([]any)
		require.Len(t, aChildren, 1)
		aZone := aChildren[0].(map[string]any)
		require.Equal(t, "Z1", aZone["zone_code"])
		require.Equal(t, "WH-A", aZone["warehouse_code"], "中间层回填 warehouse_code（api.md §2026-10-06）")
		aBin := aZone["children"].([]any)[0].(map[string]any)
		require.Equal(t, "B1", aBin["bin_code"])
		require.NotContains(t, aBin, "children", "末层无 children（query.go:73 omitempty）")

		b := distNode(t, env, 1)
		require.Equal(t, 150.0, b["total_qty"], "仓库总量=子树求和（同库位两批行聚合 150）")
		require.Equal(t, 110.0, b["available_qty"])
		bChildren := b["children"].([]any)
		require.Len(t, bChildren, 1)
		bZone := bChildren[0].(map[string]any)
		require.Equal(t, 150.0, bZone["total_qty"])
		bBin := bZone["children"].([]any)[0].(map[string]any)
		require.Equal(t, "B2", bBin["bin_code"])
		require.Equal(t, 150.0, bBin["total_qty"], "同库位多批次行在库位叶聚合（query.go:100 注释）")
	})

	t.Run("入口行不存在_404且不触达分布查询", func(t *testing.T) {
		fakeReset(t)
		stubDistPair(nil, nil)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getStockDistribution, "/api/inventory/9/distribution", "9", superUser())

		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "COMMON_NOT_FOUND", env["code"])
		require.Len(t, fakeCalls(), 1, "入口行不存在即短路，不得触达分布联表查询")
	})

	t.Run("入口行越权_404且不触达分布查询", func(t *testing.T) {
		fakeReset(t)
		stubDistPair([][]driver.Value{entry}, dist)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getStockDistribution, "/api/inventory/5/distribution", "5", whUser(2))

		require.Equal(t, http.StatusNotFound, rec.Code, "行仓库越权 fail-closed（query.go:89-91）")
		require.Equal(t, "COMMON_NOT_FOUND", env["code"])
		require.Len(t, fakeCalls(), 1)
	})

	t.Run("SKU无正数库存行_空数组空态", func(t *testing.T) {
		fakeReset(t)
		stubDistPair([][]driver.Value{entry}, nil) // 分布查询 0 行
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callPathParamQuery(t, h.getStockDistribution, "/api/inventory/5/distribution", "5", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, []any{}, env["data"], "空态为 []（api.md §2026-10-06：前端呈空态）")
	})

	t.Run("非法路径id_400", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}
		rec, env := callPathParamQuery(t, h.getStockDistribution, "/api/inventory/xyz/distribution", "xyz", superUser())
		assertInvalidParam(t, rec, env, "id")
	})
}

// ---- GET /api/inventory/locks（listLocks）----

// lockCols 对齐 repository.go:497-501 行查询列序。
var lockCols = []string{
	"id", "warehouse_id", "bin_id", "sku_id", "batch_id", "lock_type",
	"source_type", "source_no", "qty", "status", "released_at", "released_by", "remark",
	"created_at", "updated_at", "created_by", "updated_by",
}

func TestListLocksData(t *testing.T) {
	// 两行：ACTIVE 订单占用（released_at NULL）+ RELEASED 质检冻结（released_at 有值）。
	lockRows := [][]driver.Value{
		{int64(1), int64(1), int64(101), int64(5001), int64(0), sv("ORDER_HOLD"),
			sv("inventory_order"), sv("SO-1001"), qv("5.0000"), sv("ACTIVE"),
			nil, int64(0), sv("订单预占"), tv("2026-10-03 10:00:00"), tv("2026-10-03 10:00:00"), int64(99), int64(99)},
		{int64(2), int64(1), int64(101), int64(5001), int64(0), sv("QC_FREEZE"),
			sv("inventory_qc"), sv("QC-9"), qv("8.0000"), sv("RELEASED"),
			tv("2026-10-05 12:00:00"), int64(7), sv("质检解冻"), tv("2026-10-04 10:00:00"), tv("2026-10-05 12:00:00"), int64(99), int64(7)},
	}
	stubLocks := func(total int64, rows [][]driver.Value) {
		fakeStubCount("inventory_locks", total)
		fakeStubQuery([]string{"FROM inventory_locks", "ORDER BY id DESC"}, lockCols, rows)
	}

	t.Run("成功路径_ACTIVE与RELEASED两态视图", func(t *testing.T) {
		fakeReset(t)
		stubLocks(2, lockRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listLocks, "/api/inventory/locks", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, float64(2), envData(t, env)["total"])
		first := item(t, env, 0)
		require.Equal(t, "1", first["id"])
		require.Equal(t, "ORDER_HOLD", first["lock_type"])
		require.Equal(t, "inventory_order", first["source_type"])
		require.Equal(t, "SO-1001", first["source_no"])
		require.Equal(t, 5.0, first["qty"])
		require.Equal(t, "ACTIVE", first["status"])
		require.Nil(t, first["released_at"], "未释放记录 released_at 为 null")
		require.Equal(t, "0", first["released_by"])

		second := item(t, env, 1)
		require.Equal(t, "QC_FREEZE", second["lock_type"], "冻结类锁定进 frozen 列（service.go:88-90）")
		require.Equal(t, "RELEASED", second["status"])
		require.Equal(t, "2026-10-05 12:00:00", second["released_at"])
		require.Equal(t, "7", second["released_by"])
	})

	t.Run("组合过滤_参数按where顺序透传", func(t *testing.T) {
		fakeReset(t)
		stubLocks(1, lockRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listLocks,
			"/api/inventory/locks?warehouse_id=1&sku_id=5001&lock_type=ORDER_HOLD&status=ACTIVE&source_type=inventory_order&source_no=SO-1001", superUser())

		// where() 顺序（repository.go:452-487）：warehouse→sku→lock_type→status→source_type→source_no。
		assertArgs(t, queryCall(t, 0).args,
			int64(1), int64(5001), "ORDER_HOLD", "ACTIVE", "inventory_order", "SO-1001")
	})

	t.Run("数据权限_仓库集收敛", func(t *testing.T) {
		fakeReset(t)
		stubLocks(1, lockRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listLocks, "/api/inventory/locks", whUser(3, 4))

		require.Contains(t, queryCall(t, 0).query, "warehouse_id IN (?,?)",
			"锁定记录按仓库集收敛（LockQuery 注释 query.go:271-272）")
		assertArgs(t, queryCall(t, 0).args, int64(3), int64(4))
	})

	t.Run("校验失败表驱动_400", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}
		cases := []struct {
			name    string
			target  string
			field   string
			allowed []any
		}{
			{"非法锁定类型", "/api/inventory/locks?lock_type=HACK", "lock_type",
				[]any{"ORDER_HOLD", "COUNT_FREEZE", "QC_FREEZE", "MANUAL_FREEZE", "EXCEPTION_FREEZE"}},
			{"非法锁定状态", "/api/inventory/locks?status=NOPE", "status",
				[]any{"ACTIVE", "RELEASED", "CONSUMED"}},
			{"非法warehouse_id", "/api/inventory/locks?warehouse_id=1.5", "warehouse_id", nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec, env := callQuery(t, h.listLocks, tc.target, superUser())
				assertInvalidParam(t, rec, env, tc.field)
				if tc.allowed != nil {
					require.ElementsMatch(t, tc.allowed,
						env["details"].(map[string]any)["allowed"].([]any))
				}
				require.Len(t, fakeCalls(), 0, "校验失败不得触达数据层")
			})
		}
	})
}

// ---- GET /api/inventory/adjustments（listAdjustments）----

// adjustmentCols 对齐 repository.go:555-559 行查询列序。
var adjustmentCols = []string{
	"id", "adjustment_no", "warehouse_id", "sku_id", "bin_id", "batch_id", "adjust_type", "qty",
	"reason", "status", "approved_by", "approved_at", "executed_by", "executed_at",
	"created_at", "updated_at", "created_by", "updated_by",
}

func TestListAdjustmentsData(t *testing.T) {
	adjRows := [][]driver.Value{
		{int64(1), sv("ADJ202610010001"), int64(1), int64(5001), int64(101), int64(0),
			sv("盘亏"), qv("-3.0000"), sv("盘点短缺"), sv("EXECUTED"),
			int64(0), nil, int64(99), tv("2026-10-01 15:00:00"),
			tv("2026-10-01 15:00:00"), tv("2026-10-01 15:00:00"), int64(99), int64(99)},
		{int64(2), sv("ADJ202610020002"), int64(1), int64(5001), int64(101), int64(0),
			sv("盘盈"), qv("2.0000"), sv("盘点多出"), sv("EXECUTED"),
			int64(0), nil, int64(99), tv("2026-10-02 15:30:00"),
			tv("2026-10-02 15:30:00"), tv("2026-10-02 15:30:00"), int64(99), int64(99)},
	}
	stubAdjustments := func(total int64, rows [][]driver.Value) {
		fakeStubCount("inventory_adjustments", total)
		fakeStubQuery([]string{"FROM inventory_adjustments"}, adjustmentCols, rows)
	}

	t.Run("成功路径_盘亏负量与盘盈正量", func(t *testing.T) {
		fakeReset(t)
		stubAdjustments(2, adjRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listAdjustments, "/api/inventory/adjustments", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		first := item(t, env, 0)
		require.Equal(t, "ADJ202610010001", first["adjustment_no"])
		require.Equal(t, "盘亏", first["adjust_type"], "调整类型中文值域（service.go:101）")
		require.Equal(t, -3.0, first["qty"], "盘亏为减向负量")
		require.Equal(t, "盘点短缺", first["reason"])
		require.Equal(t, "EXECUTED", first["status"], "M1 执行即落账（repository.go:353 注释）")
		require.Equal(t, "99", first["executed_by"])
		require.Equal(t, "2026-10-01 15:00:00", first["executed_at"])
		require.Nil(t, first["approved_at"], "未审批为 null（JSONTime 零值序列化）")

		require.Equal(t, "盘盈", item(t, env, 1)["adjust_type"])
		require.Equal(t, 2.0, item(t, env, 1)["qty"])
	})

	t.Run("过滤参数透传_含中文值", func(t *testing.T) {
		fakeReset(t)
		stubAdjustments(1, adjRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listAdjustments,
			"/api/inventory/adjustments?warehouse_id=1&sku_id=5001&adjust_type=%E7%9B%98%E4%BA%8F&status=EXECUTED", superUser())

		// where() 顺序（repository.go:518-545）：warehouse→sku→adjust_type→status。
		assertArgs(t, queryCall(t, 0).args, int64(1), int64(5001), "盘亏", "EXECUTED")
	})

	t.Run("非法调整类型_400带中文值域", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listAdjustments, "/api/inventory/adjustments?adjust_type=NOPE", superUser())

		assertInvalidParam(t, rec, env, "adjust_type")
		require.ElementsMatch(t,
			[]any{"盘盈", "盘亏", "损耗", "报废", "其他"},
			env["details"].(map[string]any)["allowed"].([]any),
			"值域与 business-flow §11.1 同源（service.go:101）")
		require.Len(t, fakeCalls(), 0)
	})
}

// ---- GET /api/inventory-ledgers（listLedgers）----

// ledgerCols 对齐 repository.go:856-861 行查询列序。
var ledgerCols = []string{
	"id", "ledger_no", "sku_id", "warehouse_id", "zone_id", "shelf_id", "bin_id", "batch_id",
	"serial_no", "change_type", "business_type", "business_no",
	"status_from", "status_to", "qty_before", "qty_change", "qty_after",
	"idempotency_key", "operator_id", "operator_name", "request_id", "remark", "created_at",
}

func TestListLedgersData(t *testing.T) {
	// 两笔真实口径流水（integration_test.go TestLedgerPairing 同形态）：
	// INBOUND（total 变化型，zone/shelf/幂等键有值）+ LOCK（状态迁移型，zone/shelf/幂等键 NULL）。
	ledgerRows := [][]driver.Value{
		{int64(2), sv("LED20261002000002"), int64(5001), int64(1), nil, nil, int64(101), int64(0),
			sv(""), sv("LOCK"), sv("inventory_order"), sv("SO-1"),
			sv("available"), sv("locked"), qv("30.0000"), qv("-10.0000"), qv("20.0000"),
			nil, int64(99), sv("张三"), sv("req-lock-1"), sv(""), tv("2026-10-02 11:00:00")},
		{int64(1), sv("LED20261001000001"), int64(5001), int64(1), int64(11), int64(111), int64(101), int64(0),
			sv("SN-9"), sv("INBOUND"), sv("inventory_inbound"), sv("IN-1"),
			sv("available"), sv("available"), qv("0.0000"), qv("30.0000"), qv("30.0000"),
			sv("IDEM-IN-1"), int64(99), sv("张三"), sv("req-in-1"), sv("首单入库"), tv("2026-10-01 10:00:00")},
	}
	stubLedgers := func(total int64, rows [][]driver.Value) {
		fakeStubCount("inventory_ledgers", total)
		fakeStubQuery([]string{"FROM inventory_ledgers", "ORDER BY created_at DESC"}, ledgerCols, rows)
	}

	t.Run("成功路径_状态迁移型与total变化型流水_可空列语义", func(t *testing.T) {
		fakeReset(t)
		stubLedgers(2, ledgerRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listLedgers, "/api/inventory-ledgers", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		lockLed := item(t, env, 0)
		require.Equal(t, "LED20261002000002", lockLed["ledger_no"])
		require.Nil(t, lockLed["zone_id"], "状态迁移型流水 zone/shelf 可空（LedgerView *ID）")
		require.Nil(t, lockLed["shelf_id"])
		require.Equal(t, "", lockLed["idempotency_key"], "无幂等键序列化为空串（handler.go:100-102）")
		require.Equal(t, "LOCK", lockLed["change_type"])
		require.Equal(t, "available", lockLed["status_from"])
		require.Equal(t, "locked", lockLed["status_to"])
		require.Equal(t, 30.0, lockLed["qty_before"], "三态为 status_from 所指列（available）")
		require.Equal(t, -10.0, lockLed["qty_change"])
		require.Equal(t, 20.0, lockLed["qty_after"])
		require.Equal(t, "张三", lockLed["operator_name"])

		inLed := item(t, env, 1)
		require.Equal(t, "11", inLed["zone_id"], "有值 zone_id 序列化为 ID 字符串")
		require.Equal(t, "IDEM-IN-1", inLed["idempotency_key"])
		require.Equal(t, "SN-9", inLed["serial_no"], "M2 序列号追溯接入点（service.go:296 注释）")
		require.Equal(t, 30.0, inLed["qty_after"])
	})

	t.Run("时间范围过滤_from含下界_to补全当日末秒", func(t *testing.T) {
		fakeReset(t)
		stubLedgers(2, ledgerRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listLedgers,
			"/api/inventory-ledgers?created_from=2026-01-01&created_to=2026-01-02", superUser())

		// where() 时间端点在最后（repository.go:837-844）。
		args := queryCall(t, 0).args
		require.Len(t, args, 2)
		from, ok := args[0].(time.Time)
		require.True(t, ok, "created_from 必须为 time.Time: %T", args[0])
		require.Equal(t, "2026-01-01 00:00:00", from.Format("2006-01-02 15:04:05"))
		to, ok := args[1].(time.Time)
		require.True(t, ok)
		require.Equal(t, "2026-01-02 23:59:59", to.Format("2006-01-02 15:04:05"),
			"纯日期 to 端点补全为当日 23:59:59（handler.go:203-214）")
	})

	t.Run("业务过滤透传_changeType与单号与序列号", func(t *testing.T) {
		fakeReset(t)
		stubLedgers(1, ledgerRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listLedgers,
			"/api/inventory-ledgers?warehouse_id=1&sku_id=5001&bin_id=101&change_type=LOCK&business_no=SO-1&serial_no=SN-9", superUser())

		// where() 顺序（repository.go:799-845）：warehouse→sku→bin→change_type→business_no→serial_no。
		assertArgs(t, queryCall(t, 0).args,
			int64(1), int64(5001), int64(101), "LOCK", "SO-1", "SN-9")
	})

	t.Run("数据权限_仓库集收敛", func(t *testing.T) {
		fakeReset(t)
		stubLedgers(0, nil)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, env := callQuery(t, h.listLedgers, "/api/inventory-ledgers", whUser(5))

		require.Contains(t, queryCall(t, 0).query, "warehouse_id IN (?)")
		assertArgs(t, queryCall(t, 0).args, int64(5))
		require.Len(t, envItems(t, env), 0)
	})
}

// ---- GET /api/batches（listBatches）----

// batchCols 对齐 repository.go:916-919 行查询列序。
var batchCols = []string{
	"id", "sku_id", "batch_no", "supplier_id", "production_date", "inbound_date",
	"expiry_date", "cost_price", "remark", "created_at", "updated_at", "created_by", "updated_by",
}

func TestListBatchesData(t *testing.T) {
	batchRows := [][]driver.Value{
		{int64(1), int64(5001), sv("B2026A"), int64(3),
			tv("2026-01-15 00:00:00"), tv("2026-02-01 00:00:00"), tv("2026-12-31 00:00:00"),
			qv("12.5000"), sv("首批"), tv("2026-02-01 00:00:00"), tv("2026-02-01 00:00:00"), int64(99), int64(99)},
		{int64(2), int64(5001), sv("B2026B"), int64(0),
			nil, tv("2026-03-01 00:00:00"), nil,
			qv("0.0000"), sv(""), tv("2026-03-01 00:00:00"), tv("2026-03-01 00:00:00"), int64(99), int64(99)},
	}
	stubBatches := func(total int64, rows [][]driver.Value) {
		fakeStubCount("batches", total)
		fakeStubQuery([]string{"FROM batches", "LIMIT ? OFFSET ?"}, batchCols, rows)
	}

	t.Run("成功路径_有期与无期批次_视图", func(t *testing.T) {
		fakeReset(t)
		stubBatches(2, batchRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listBatches, "/api/batches", nil) // 批次为 SKU 维度台账，仓库无关（query.go:215-216）

		require.Equal(t, http.StatusOK, rec.Code)
		first := item(t, env, 0)
		require.Equal(t, "1", first["id"])
		require.Equal(t, "B2026A", first["batch_no"])
		require.Equal(t, "3", first["supplier_id"])
		require.Equal(t, 12.5, first["cost_price"])
		require.Equal(t, "2026-12-31 00:00:00", first["expiry_date"])

		second := item(t, env, 1)
		require.Equal(t, "B2026B", second["batch_no"])
		require.Nil(t, second["production_date"], "可空时间列 null（JSONTime 零值）")
		require.Nil(t, second["expiry_date"], "无效期批次 expiry_date 为 null（NULLS LAST 语义的来源）")
	})

	t.Run("order=expiry按效期升序_FEF0审阅视图排序子句", func(t *testing.T) {
		fakeReset(t)
		stubBatches(2, batchRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listBatches, "/api/batches?order=expiry", superUser())

		require.Contains(t, queryCall(t, 1).query, "ORDER BY expiry_date ASC NULLS LAST, id",
			"FEFO 审阅视图排序（repository.go:875/913）")
		// 缺省（不含 order）按 id。
		fakeReset(t)
		stubBatches(2, batchRows)
		_, _ = callQuery(t, h.listBatches, "/api/batches", superUser())
		require.Contains(t, queryCall(t, 1).query, "ORDER BY id")
		require.NotContains(t, queryCall(t, 1).query, "NULLS LAST")
	})

	t.Run("过滤参数透传_效期端点补全", func(t *testing.T) {
		fakeReset(t)
		stubBatches(1, batchRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listBatches,
			"/api/batches?sku_id=5001&supplier_id=3&batch_no=B2026A&expiry_from=2026-06-01&expiry_to=2026-12-31", superUser())

		// where() 顺序（repository.go:878-902）：sku→batch_no→supplier→expiry_from→expiry_to。
		args := queryCall(t, 0).args
		require.Len(t, args, 5)
		require.Equal(t, int64(5001), args[0])
		require.Equal(t, "B2026A", args[1])
		require.Equal(t, int64(3), args[2])
		from, ok := args[3].(time.Time)
		require.True(t, ok)
		require.Equal(t, "2026-06-01 00:00:00", from.Format("2006-01-02 15:04:05"))
		to, ok := args[4].(time.Time)
		require.True(t, ok)
		require.Equal(t, "2026-12-31 23:59:59", to.Format("2006-01-02 15:04:05"), "expiry_to 补全当日末秒")
	})

	t.Run("校验失败表驱动_400", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}
		cases := []struct {
			name   string
			target string
			field  string
		}{
			{"非法效期下界", "/api/batches?expiry_from=2026/06/01", "expiry_from"},
			{"非法sku_id", "/api/batches?sku_id=abc", "sku_id"},
			{"非法supplier_id", "/api/batches?supplier_id=-2", "supplier_id"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec, env := callQuery(t, h.listBatches, tc.target, superUser())
				assertInvalidParam(t, rec, env, tc.field)
				require.Len(t, fakeCalls(), 0)
			})
		}
	})
}

// ---- GET /api/serials（listSerials）----

// serialCols 对齐 repository.go:984-988 行查询列序。
var serialCols = []string{
	"id", "serial_no", "sku_id", "batch_id", "warehouse_id", "bin_id", "status",
	"last_source_type", "last_source_no", "last_event_at",
	"created_at", "updated_at", "created_by", "updated_by",
}

func TestListSerialsData(t *testing.T) {
	// 两行：IN_STOCK 在库（wh1）+ OUTBOUND 不在库（wh0/bin0——退货/出库后）。
	serialRows := [][]driver.Value{
		{int64(1), sv("SN-001"), int64(5001), int64(7), int64(1), int64(101), sv("IN_STOCK"),
			sv("inventory_inbound"), sv("IN-1"), tv("2026-10-01 10:00:00"),
			tv("2026-10-01 10:00:00"), tv("2026-10-01 10:00:00"), int64(99), int64(99)},
		{int64(2), sv("SN-002"), int64(5001), int64(0), int64(0), int64(0), sv("OUTBOUND"),
			sv("inventory_shipment"), sv("SH-1"), tv("2026-10-05 16:00:00"),
			tv("2026-10-05 16:00:00"), tv("2026-10-05 16:00:00"), int64(99), int64(99)},
	}
	stubSerials := func(total int64, rows [][]driver.Value) {
		fakeStubCount("serial_numbers", total)
		fakeStubQuery([]string{"FROM serial_numbers"}, serialCols, rows)
	}

	t.Run("成功路径_在库与不在库两态视图", func(t *testing.T) {
		fakeReset(t)
		stubSerials(2, serialRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listSerials, "/api/serials", superUser())

		require.Equal(t, http.StatusOK, rec.Code)
		first := item(t, env, 0)
		require.Equal(t, "SN-001", first["serial_no"])
		require.Equal(t, "5001", first["sku_id"])
		require.Equal(t, "7", first["batch_id"])
		require.Equal(t, "1", first["warehouse_id"])
		require.Equal(t, "101", first["bin_id"])
		require.Equal(t, "IN_STOCK", first["status"])
		require.Equal(t, "inventory_inbound", first["last_source_type"], "最近状态变化追溯指针（SerialView，handler.go:130-144）")
		require.Equal(t, "IN-1", first["last_source_no"])
		require.Equal(t, "2026-10-01 10:00:00", first["last_event_at"])

		second := item(t, env, 1)
		require.Equal(t, "0", second["warehouse_id"], "0=不在库（inventory-rules §8）")
		require.Equal(t, "OUTBOUND", second["status"])
		require.Equal(t, "inventory_shipment", second["last_source_type"])
	})

	t.Run("数据权限_仓库集收敛且不在库序列号恒可见", func(t *testing.T) {
		fakeReset(t)
		stubSerials(2, serialRows)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, env := callQuery(t, h.listSerials, "/api/serials", whUser(1))

		// serialFilter.where()：不在库（warehouse_id=0）行恒可见（repository.go:945-947）。
		require.Contains(t, queryCall(t, 0).query, "(warehouse_id IN (?) OR warehouse_id = 0)")
		assertArgs(t, queryCall(t, 0).args, int64(1))
		require.Len(t, envItems(t, env), 2, "不在库序列号对任何仓库范围用户可见")
	})

	t.Run("过滤参数透传_单号与状态与维度", func(t *testing.T) {
		fakeReset(t)
		stubSerials(1, serialRows[:1])
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		_, _ = callQuery(t, h.listSerials,
			"/api/serials?warehouse_id=1&bin_id=101&sku_id=5001&batch_id=7&serial_no=SN-001&status=IN_STOCK", superUser())

		// where() 顺序（repository.go:938-973）：serial_no→sku→warehouse→bin→batch→status。
		assertArgs(t, queryCall(t, 0).args,
			"SN-001", int64(5001), int64(1), int64(101), int64(7), "IN_STOCK")
	})

	t.Run("非法warehouse_id_400", func(t *testing.T) {
		fakeReset(t)
		h := &handler{svc: NewService(openFakeGorm(t), nil)}

		rec, env := callQuery(t, h.listSerials, "/api/serials?warehouse_id=no", superUser())

		assertInvalidParam(t, rec, env, "warehouse_id")
		require.Len(t, fakeCalls(), 0)
	})
}
