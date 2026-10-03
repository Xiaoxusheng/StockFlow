package auth

// 测试替身：可运行事务与 INSERT 的假 gorm 方言器（database/sql 假驱动支撑）。
// 目的：Service 的事务路径（database.Tx + middleware.WriteLoginLog/Audit）在无 PostgreSQL
// 环境下可执行——驱动层全部 no-op 成功，业务数据由 fakeRepo 内存承载（ask 约束：
// 单测不依赖 PostgreSQL）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// ---- database/sql 假驱动 ----

var (
	drvOnce sync.Once
	drvMu   sync.Mutex
	drvN    int
)

func registerFakeDriver() string {
	drvOnce.Do(func() {
		sql.Register("sfake-auth-test", fakeDriver{})
	})
	drvMu.Lock()
	defer drvMu.Unlock()
	drvN++
	return "sfake-auth-test"
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (c *fakeConn) Prepare(q string) (driver.Stmt, error) { return &fakeStmt{sql: q}, nil }
func (c *fakeConn) Close() error                          { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)             { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{ sql string }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 } // 不校验参数个数

// ---- SQL 捕获（断言 login_logs 写入行为用；包级共享，Go 测试串行执行）----

var (
	capMu  sync.Mutex
	capLog []capEntry
)

type capEntry struct {
	sql  string
	args string
}

func recordSQL(sql string, args []driver.Value) {
	capMu.Lock()
	defer capMu.Unlock()
	anyArgs := make([]any, 0, len(args))
	for _, a := range args {
		anyArgs = append(anyArgs, a)
	}
	capLog = append(capLog, capEntry{sql: sql, args: fmt.Sprint(anyArgs...)})
}

// resetSQLCapture 清空捕获（断言前调用，避免用例间串扰）。
func resetSQLCapture() {
	capMu.Lock()
	defer capMu.Unlock()
	capLog = nil
}

// sqlCaptured 是否捕获到"SQL 含 sqlSub 且绑定参数含 argSub"的执行
// （login_logs 为纯 INSERT，语句文本含表名、fail_reason 在绑定参数中）。
func sqlCaptured(sqlSub, argSub string) bool {
	capMu.Lock()
	defer capMu.Unlock()
	for _, e := range capLog {
		if strings.Contains(e.sql, sqlSub) && strings.Contains(e.args, argSub) {
			return true
		}
	}
	return false
}

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	recordSQL(s.sql, args)
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	recordSQL(s.sql, args) // gorm 带 RETURNING 的 Create 走 Query 路径
	return &fakeRows{}, nil
}

// fakeRows 单行单列（id=1）：支撑 RETURNING 的 Create 路径。
type fakeRows struct{ done bool }

func (r *fakeRows) Columns() []string { return []string{"id"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

// 独立的 context 变体（database/sql 优先走 Context 接口，缺省回退 legacy 即可）。
var (
	_ driver.Conn     = (*fakeConn)(nil)
	_ driver.Stmt     = (*fakeStmt)(nil)
	_ driver.Tx       = fakeTx{}
	_ driver.Rows     = (*fakeRows)(nil)
	_ context.Context = context.Background()
)

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests 的 DummyDialector + 可用 ConnPool）----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake" }

func (fakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	sqlDB, err := sql.Open(registerFakeDriver(), "")
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

// openTestGorm 打开假 gorm 句柄（事务/INSERT 可运行，数据不落任何真实存储）。
func openTestGorm() (*gorm.DB, error) {
	return gorm.Open(fakeDialector{}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}
