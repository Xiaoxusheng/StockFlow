package inventory

// 测试替身：可编程返回数据行的假 gorm 方言器（database/sql 假驱动支撑）。
// 目的：查询侧 8 条 HTTP 端点（handler.go：listInventory/getInventory/
// getStockDistribution/listLocks/listAdjustments/listLedgers/listBatches/listSerials）
// 的数据路径在无 PostgreSQL 环境可测——repository.go 查询为原生 SQL（db.Raw().Scan），
// repo 是具体类型 *repository 非接口，内存 fakeRepo 无法拦截；故经驱动层按 SQL 文本
// 子串路由返回配置行，使"过滤参数透传 → SQL 绑定参数 → 行扫描 → 视图转换 → 响应信封"
// 全链路在单测可断言（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
// 形态与 internal/masterdata、internal/auth、internal/purchase 的 fakedb_test.go 同族
// （同一项目约定，非第二套机制）。与 masterdata 版夹具的两点差异见 fakeQueryStub 注释。
//
// 并发口径：同包测试串行执行（Go 默认，无 t.Parallel），包级 stub/捕获状态安全
// （与 internal/auth fakedb_test.go 同一约定）；fakeReset 以 t.Cleanup 收尾防用例间串扰。

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// ---- database/sql 假驱动 ----

const fakeDriverName = "sfake-inventory-test"

var (
	fakeDriverOnce sync.Once
	fakeMu         sync.Mutex
	fakeStubs      []fakeQueryStub
	fakeExecLog    []fakeSQLCall
)

func registerFakeDriver() {
	fakeDriverOnce.Do(func() {
		sql.Register(fakeDriverName, fakeDriver{})
	})
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return &fakeStmt{query: query}, nil }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{ query string }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 } // 不校验参数个数

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	// 查询侧测试不写库：任何 Exec 都是用例越界，响亮失败（对齐 internal/router 的语义）。
	return nil, fmt.Errorf("inventory fakedb: 测试不预期写库执行: %s", s.query)
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeExecLog = append(fakeExecLog, fakeSQLCall{
		query: s.query,
		args:  append([]driver.Value(nil), args...),
	})
	for _, st := range fakeStubs {
		if stubMatches(st.subs, s.query) {
			if st.err != nil {
				return nil, st.err
			}
			return &fakeQueryRows{cols: st.cols, rows: st.rows}, nil
		}
	}
	// 未命中夹具即报错（不静默回退假数据）：查询侧用例必须显式声明每条 SQL 的响应，
	// 触达未预期查询 = 用例与实现漂移，应响亮失败而非伪装成功。
	return nil, fmt.Errorf("inventory fakedb: 未注册匹配夹具的 SQL: %s", s.query)
}

// stubMatches 子串全命中判定（len(subs)==0 恒不命中，防误配）。
func stubMatches(subs []string, query string) bool {
	if len(subs) == 0 {
		return false
	}
	for _, s := range subs {
		if !strings.Contains(query, s) {
			return false
		}
	}
	return true
}

// ---- 夹具注册与 SQL 捕获（查询侧测试的配置面与断言面）----

// fakeQueryStub 一条"SQL 子串匹配 → 返回配置行/错误"的路由（先注册先匹配——
// 更精确的子串（如 "WHERE id = ?"）先注册即可压过同表宽匹配）。
type fakeQueryStub struct {
	subs []string // query 必须包含的全部子串
	cols []string
	rows [][]driver.Value
	err  error // 非 nil 时该查询返回此错误（repo 错误传播分支用）
}

// fakeSQLCall 一次已执行的查询（gorm IN 切片展开后的最终 SQL 文本 + 展平绑定参数）。
type fakeSQLCall struct {
	query string
	args  []driver.Value
}

// fakeReset 清空夹具与捕获，并注册用例结束后的兜底清理（每个用例开头调用）。
func fakeReset(t *testing.T) {
	t.Helper()
	fakeMu.Lock()
	fakeStubs = nil
	fakeExecLog = nil
	fakeMu.Unlock()
	t.Cleanup(func() {
		fakeMu.Lock()
		defer fakeMu.Unlock()
		fakeStubs = nil
		fakeExecLog = nil
	})
}

// fakeStubQuery 注册行集夹具（subs 全命中即响应；rows 行宽必须等于 cols 数，
// 不齐在 Next 处响亮失败）。
func fakeStubQuery(subs []string, cols []string, rows [][]driver.Value) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeStubs = append(fakeStubs, fakeQueryStub{subs: subs, cols: cols, rows: rows})
}

// fakeStubCount 为 "SELECT COUNT(*) FROM <table>" 注册 total
// （repository.go 各 list 方法的 total 预查形态固定）。
func fakeStubCount(table string, total int64) {
	fakeStubQuery([]string{"SELECT COUNT(*) FROM " + table},
		[]string{"count"}, [][]driver.Value{{total}})
}

// fakeStubError 注册一条返回错误的夹具（覆盖 handler 的 repo 错误 → 500 分支）。
func fakeStubError(subs []string, err error) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeStubs = append(fakeStubs, fakeQueryStub{subs: subs, err: err})
}

// fakeCalls 返回捕获的查询序列快照（calls[0]=COUNT、calls[1]=行查询为各 list 的固定次序）。
func fakeCalls() []fakeSQLCall {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	return append([]fakeSQLCall(nil), fakeExecLog...)
}

// fakeQueryRows 多列配置行（Raw SELECT 的投影承载，对齐 masterdata fakeQueryRows）。
type fakeQueryRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeQueryRows) Columns() []string { return r.cols }
func (r *fakeQueryRows) Close() error      { return nil }
func (r *fakeQueryRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	if len(r.rows[r.i]) != len(dest) {
		return fmt.Errorf("inventory fakedb: 夹具行宽度 %d 与列数 %d 不一致（第 %d 行）",
			len(r.rows[r.i]), len(dest), r.i)
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

var (
	_ driver.Conn = (*fakeConn)(nil)
	_ driver.Stmt = (*fakeStmt)(nil)
	_ driver.Tx   = fakeTx{}
	_ driver.Rows = (*fakeQueryRows)(nil)
)

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests 的 DummyDialector + 可用 ConnPool，
// 与 masterdata/auth/purchase 的 fakeDialector 同款）----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake-inventory" }

func (fakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	registerFakeDriver()
	sqlDB, err := sql.Open(fakeDriverName, "")
	if err != nil {
		return err
	}
	db.ConnPool = sqlDB
	return nil
}

func (fakeDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (fakeDialector) Migrator(*gorm.DB) gorm.Migrator { return nil }

func (fakeDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	writer.WriteByte('?')
}

func (fakeDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteByte('`')
	writer.WriteString(str)
	writer.WriteByte('`')
}

func (fakeDialector) Explain(sqlStr string, vars ...any) string {
	return logger.ExplainSQL(sqlStr, nil, `"`, vars...)
}

func (fakeDialector) DataTypeOf(*schema.Field) string { return "text" }

// openFakeGorm 打开假 gorm 句柄（查询经夹具承载，不落任何真实存储）。
func openFakeGorm(t *testing.T) *gorm.DB {
	t.Helper()
	registerFakeDriver()
	db, err := gorm.Open(fakeDialector{}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqldb, e := db.DB(); e == nil {
			_ = sqldb.Close()
		}
	})
	return db
}

// ---- 行值构造 helper（列序对齐 repository.go 各 SELECT 清单）----
// 约定：整型列给 int64（database/sql 反射直赋）；数量列给 numeric 文本（Qty.Scan
// 以 string 解析，pgx numeric 真实形态）；时间列给 time.Time（JSONTime.Scan 支持）；
// NULL 列给 nil（*int64/*string/JSONTime 的空值形态）。

func iv(n int64) driver.Value  { return n }
func sv(s string) driver.Value { return s }

// qv 数量列：numeric(18,4) 文本（真实链路经 pgx numeric → string）。
func qv(s string) driver.Value { return s }

// tv 时间列：统一 "YYYY-MM-DD HH:mm:ss" 解析为本地时区 time.Time。
func tv(s string) driver.Value {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		panic("inventory fakedb: 时间夹具格式错误: " + s)
	}
	return t
}
