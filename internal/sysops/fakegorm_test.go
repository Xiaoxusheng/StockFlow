package sysops

// 测试替身：可按「SQL 文本子串（+ 可选二重子串）」路由回放行数据的假 gorm 方言器
// （database/sql 假驱动支撑）。目的：/api/logs /api/system /api/notifications 17 条
// 端点（handler→service→runtimeRepo 的 Raw SQL 链路）在无 PostgreSQL 环境下可测。
//
// 与 internal/reports/fakedb_test.go 的夹具约定同源（非第二套机制），按本域写路径
// 扩展三点（reports 为纯只读域，无此三项）：
//  1. ExecContext 路由回放写影响行数——PUT configs / 任务启停 / 通知已读的写路径；
//  2. Begin/Commit/Rollback 事务计数——saveConfigs 逐项审计同事务、registerBackup
//     登记同事务的"批内原子"断言（configs.go saveConfigs / backups.go registerBackup）；
//  3. Ping 可编程——monitor 的 database.healthy 两分支（monitor.go sqlDB.PingContext）。
//
// 写语句（UPDATE/审计 INSERT）一律经捕获日志断言真实下发；未命中夹具 fail-loud
// 报错而非静默空结果——漏配夹具应得到响亮的失败而非 0 值假通过。
// 同包测试串行执行（全局夹具，reports 同注），禁用 t.Parallel。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// ---- 全局夹具（同包测试串行复用） ----

var (
	sysFixtureMu sync.Mutex
	sysQueryFn   func(query string, args []driver.NamedValue) (cols []string, rows [][]driver.Value)
	sysQueryErr  func(query string, args []driver.NamedValue) error
	sysExecFn    func(query string, args []driver.NamedValue) (int64, error)
	sysExecErr   func(query string, args []driver.NamedValue) error
	sysPingOK    bool
)

// sysFixture 一次装配：查询/写语句路由 + Ping 语义（nil 字段 = 该面 fail-loud）。
// queryErr/execErr 优先于路由命中：命中即回放错误（唯一索引冲突等驱动层错误注入）。
type sysFixture struct {
	queries  func(query string, args []driver.NamedValue) ([]string, [][]driver.Value)
	queryErr func(query string, args []driver.NamedValue) error
	execs    func(query string, args []driver.NamedValue) (int64, error)
	execErr  func(query string, args []driver.NamedValue) error
	pingOK   bool
}

// useSQLFixture 装配全局夹具（nil=卸载）；用例以 defer useSQLFixture(nil) 或 t.Cleanup 收尾。
func useSQLFixture(f *sysFixture) {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	sysQueryFn, sysQueryErr, sysExecFn, sysExecErr, sysPingOK = nil, nil, nil, nil, false
	sysCapture.reset()
	if f == nil {
		return
	}
	sysQueryFn, sysQueryErr, sysExecFn, sysExecErr, sysPingOK = f.queries, f.queryErr, f.execs, f.execErr, f.pingOK
}

func currentQueryErr() func(string, []driver.NamedValue) error {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	return sysQueryErr
}

func currentExecErr() func(string, []driver.NamedValue) error {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	return sysExecErr
}

func currentQueryFn() func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	return sysQueryFn
}

func currentExecFn() func(string, []driver.NamedValue) (int64, error) {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	return sysExecFn
}

func currentPingOK() bool {
	sysFixtureMu.Lock()
	defer sysFixtureMu.Unlock()
	return sysPingOK
}

// ---- database/sql 假驱动 ----

const sysFakeDriverName = "sfake-sysops-test"

var sysDrvOnce sync.Once

func registerSysFakeDriver() {
	sysDrvOnce.Do(func() {
		sql.Register(sysFakeDriverName, sysFakeDriver{})
	})
}

type sysFakeDriver struct{}

func (sysFakeDriver) Open(string) (driver.Conn, error) { return &sysFakeConn{}, nil }

type sysFakeConn struct{}

func (*sysFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("sysops 测试假驱动不支持 Prepare（Raw/Exec 应走 QueryerContext/ExecerContext）")
}

func (*sysFakeConn) Close() error { return nil }

func (*sysFakeConn) Begin() (driver.Tx, error) {
	sysCapture.recordTx("begin")
	return sysFakeTx{}, nil
}

// Ping 可编程：monitor 的 database.healthy、health 语义由 sysPingOK 驱动。
func (*sysFakeConn) Ping(context.Context) error {
	if currentPingOK() {
		return nil
	}
	return errors.New("sysops 测试假驱动: 数据库不可达（夹具未开启 pingOK）")
}

// QueryContext 夹具路由入口：gorm Raw 经 clause.Expr.Build 展开占位符后经
// ConnPool.QueryContext 直达此处；gorm 带 RETURNING 的 Create（middleware.Audit）
// 亦走 Query 路径（gorm callbacks/create.go:91）。
func (*sysFakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	sysCapture.recordQuery(query, args)
	if fe := currentQueryErr(); fe != nil {
		if err := fe(query, args); err != nil {
			return nil, err
		}
	}
	if f := currentQueryFn(); f != nil {
		cols, rows := f(query, args)
		if cols != nil {
			return &sysFakeRows{cols: cols, rows: rows}, nil
		}
	}
	// fail-loud：漏配夹具必须响亮失败，禁止以空结果伪装查询数据。
	return nil, fmt.Errorf("sysops 测试假驱动: 查询未命中夹具: %.200s", query)
}

// ExecContext 写语句路由入口：UPDATE/DELETE 真实下发并回放影响行数。
func (*sysFakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	sysCapture.recordExec(query, args)
	if fe := currentExecErr(); fe != nil {
		if err := fe(query, args); err != nil {
			return nil, err
		}
	}
	if f := currentExecFn(); f != nil {
		n, err := f(query, args)
		if err == nil {
			return sysFakeResult{n: n}, nil
		}
		return nil, err
	}
	// fail-loud：漏配夹具必须响亮失败（写路径不应静默假成功）。
	return nil, fmt.Errorf("sysops 测试假驱动: 写语句未命中夹具: %.200s", query)
}

var (
	_ driver.Conn           = (*sysFakeConn)(nil)
	_ driver.Pinger         = (*sysFakeConn)(nil)
	_ driver.QueryerContext = (*sysFakeConn)(nil)
	_ driver.ExecerContext  = (*sysFakeConn)(nil)
)

type sysFakeTx struct{}

func (sysFakeTx) Commit() error   { sysCapture.recordTx("commit"); return nil }
func (sysFakeTx) Rollback() error { sysCapture.recordTx("rollback"); return nil }

type sysFakeResult struct{ n int64 }

func (r sysFakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r sysFakeResult) RowsAffected() (int64, error) { return r.n, nil }

// sysFakeRows 多列配置行（夹具投影承载）。
type sysFakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *sysFakeRows) Columns() []string { return r.cols }
func (r *sysFakeRows) Close() error      { return nil }
func (r *sysFakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	if len(r.rows[r.i]) != len(dest) {
		return fmt.Errorf("sysops 测试夹具: 行值数 %d 与列数 %d 不一致", len(r.rows[r.i]), len(dest))
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests 的 DummyDialector + 可用 ConnPool）----

type sysFakeDialector struct{}

func (sysFakeDialector) Name() string { return "sfake-sysops" }

func (sysFakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	registerSysFakeDriver()
	sqlDB, err := sql.Open(sysFakeDriverName, "")
	if err != nil {
		return err
	}
	db.ConnPool = sqlDB
	return nil
}

func (sysFakeDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (sysFakeDialector) Migrator(*gorm.DB) gorm.Migrator { return nil }

func (sysFakeDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	writer.WriteByte('?')
}

func (sysFakeDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteByte('`')
	writer.WriteString(str)
	writer.WriteByte('`')
}

func (sysFakeDialector) Explain(sqlStr string, vars ...any) string {
	return logger.ExplainSQL(sqlStr, nil, `"`, vars...)
}

func (sysFakeDialector) DataTypeOf(*schema.Field) string { return "text" }

// openSysFakeGorm 打开假 gorm 句柄（不落任何真实存储）。
// DisableAutomaticPing 必须开启：假连接 Ping 默认必败，否则 gorm.Open 自身探测即失败。
func openSysFakeGorm() (*gorm.DB, error) {
	registerSysFakeDriver()
	return gorm.Open(sysFakeDialector{}, &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
}

// ---- 夹具构造助手 ----

// toDriverValue 夹具值归一为合法 driver.Value（行数据不经 database/sql 转换，必须自备合法类型）。
func toDriverValue(v any) driver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		return int64(x)
	case int64, float64, bool, string, []byte, time.Time:
		return x
	default:
		panic(fmt.Sprintf("sysops 测试夹具: 不支持的值类型 %T", v))
	}
}

// sysRoute 单条查询路由：SQL 含 match（必选）且含 count（可选二重子串）即命中。
// 二重子串用于区分 paged 的「SELECT COUNT(*) ...」计数与带 LIMIT/OFFSET 的明细查询。
type sysRoute struct {
	match string
	count string
	cols  []string
	rows  [][]driver.Value
}

// sysQRoute 行路由构造。
func sysQRoute(match, count string, cols []string, rows ...[]any) sysRoute {
	vals := make([][]driver.Value, 0, len(rows))
	for _, r := range rows {
		if len(r) != len(cols) {
			panic(fmt.Sprintf("sysops 测试夹具: 行值数 %d 与列数 %d 不一致", len(r), len(cols)))
		}
		row := make([]driver.Value, len(r))
		for i, v := range r {
			row[i] = toDriverValue(v)
		}
		vals = append(vals, row)
	}
	return sysRoute{match: match, count: count, cols: cols, rows: vals}
}

// sysQRoutes 子串路由夹具（先命中先返回；未命中由 QueryContext fail-loud）。
func sysQRoutes(routes ...sysRoute) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	return func(query string, _ []driver.NamedValue) ([]string, [][]driver.Value) {
		for _, rt := range routes {
			if strings.Contains(query, rt.match) && (rt.count == "" || strings.Contains(query, rt.count)) {
				return rt.cols, rt.rows
			}
		}
		return nil, nil
	}
}

// sysExecRoute 写语句路由：命中 match 回放影响行数；err 非 nil 回放错误。
type sysExecRoute struct {
	match    string
	affected int64
	err      error
}

func sysERoutes(routes ...sysExecRoute) func(string, []driver.NamedValue) (int64, error) {
	return func(query string, _ []driver.NamedValue) (int64, error) {
		for _, rt := range routes {
			if strings.Contains(query, rt.match) {
				return rt.affected, rt.err
			}
		}
		return 0, nil
	}
}

// ---- SQL 捕获（筛选参数下发、写语句、事务边界断言） ----

type sysCaptureLog struct {
	mu      sync.Mutex
	queries []string
	qargs   [][]driver.NamedValue
	execs   []string
	eargs   [][]driver.NamedValue
	tx      []string
}

var sysCapture sysCaptureLog

func (c *sysCaptureLog) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries, c.qargs, c.execs, c.eargs, c.tx = nil, nil, nil, nil, nil
}

func (c *sysCaptureLog) recordQuery(q string, args []driver.NamedValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	c.qargs = append(c.qargs, args)
}

func (c *sysCaptureLog) recordExec(q string, args []driver.NamedValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execs = append(c.execs, q)
	c.eargs = append(c.eargs, args)
}

func (c *sysCaptureLog) recordTx(ev string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tx = append(c.tx, ev)
}

// queryOf 返回第一条含 sub 的查询 SQL（未命中返回 ""）。
func (c *sysCaptureLog) queryOf(sub string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.queries {
		if strings.Contains(s, sub) {
			return s
		}
	}
	return ""
}

// argsOf 返回第一条含 sub 的 SQL 绑定参数（未命中返回 nil）。
func (c *sysCaptureLog) argsOf(sub string) []driver.NamedValue {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.queries {
		if strings.Contains(s, sub) {
			return c.qargs[i]
		}
	}
	return nil
}

// execCountOf 含 sub 的写语句条数。
func (c *sysCaptureLog) execCountOf(sub string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, s := range c.execs {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

// execArgsOf 返回第一条含 sub 的写语句绑定参数（未命中返回 nil）。
func (c *sysCaptureLog) execArgsOf(sub string) []driver.NamedValue {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.execs {
		if strings.Contains(s, sub) {
			return c.eargs[i]
		}
	}
	return nil
}

// txCountOf 事务事件计数（begin/commit/rollback）。
func (c *sysCaptureLog) txCountOf(ev string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, s := range c.tx {
		if s == ev {
			n++
		}
	}
	return n
}

// argString 绑定参数文本形态（database/sql 已归一：int→int64）。
func argString(args []driver.NamedValue) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a.Value))
	}
	return strings.Join(parts, "|")
}

// argContains 绑定参数中是否存在 fmt.Sprint 相等的值（bool/string/int 直判）。
func argContains(args []driver.NamedValue, want any) bool {
	for _, a := range args {
		if fmt.Sprint(a.Value) == fmt.Sprint(want) {
			return true
		}
	}
	return false
}
