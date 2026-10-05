package masterdata

// 测试替身：可运行事务与 INSERT 的假 gorm 方言器（database/sql 假驱动支撑）。
// 目的：Service 的事务路径（database.Tx + middleware.Audit）在无 PostgreSQL 环境下
// 可执行——驱动层全部 no-op 成功，业务数据由 fakeRepo 内存承载（ask 约束：单测
// 不依赖 PostgreSQL/Redis/网络）。模式与 internal/auth 的同款测试支撑一致（同一
// 项目约定，非第二套机制）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/middleware"
)

// ---- database/sql 假驱动 ----

var (
	drvOnce sync.Once
	drvMu   sync.Mutex
	drvN    int
)

func registerFakeDriver() string {
	drvOnce.Do(func() {
		sql.Register("sfake-masterdata-test", fakeDriver{})
	})
	drvMu.Lock()
	defer drvMu.Unlock()
	drvN++
	return "sfake-masterdata-test"
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
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	// 可编程查询夹具（见 fakeQueryFixture）：按 SQL 文本路由返回配置行；
	// 未命中夹具回退默认单行 id=1 行为（既有 Create/RETURNING 路径不受影响）。
	if fakeQueryFixture != nil {
		if cols, rows := fakeQueryFixture(s.query); cols != nil {
			return &fakeQueryRows{cols: cols, rows: rows}, nil
		}
	}
	return &fakeRows{}, nil
}

// fakeQueryFixture 可编程查询夹具（nil=默认单行 id=1 行为）：devices_resolve.go /
// printing_content.go 的 reader 直接走 Raw SELECT（不经 Repository 接口，内存替身
// 无法拦截）——经本钩子按 SQL 文本路由返回配置行，使 Raw 装配路径在无 PostgreSQL
// 环境可测（ask 约束：单测不依赖真实数据库）。同包测试串行执行，无并发竞争。
var fakeQueryFixture func(query string) (cols []string, rows [][]driver.Value)

// fakeQueryRows 多列配置行（Raw SELECT 装配路径的投影承载）。
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
	copy(dest, r.rows[r.i])
	r.i++
	return nil
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

// ---- 审计探针（经 gorm Create 回调捕获 middleware.Audit 落库的 operation_logs 行）----

// auditSpy 捕获同事务写入的审计行（真路径：middleware.Audit → tx.Create）。
type auditSpy struct {
	mu      sync.Mutex
	entries []middleware.OperationLog
}

func (s *auditSpy) record(e middleware.OperationLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

func (s *auditSpy) all() []middleware.OperationLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]middleware.OperationLog(nil), s.entries...)
}

// openTestGorm 打开假 gorm 句柄（事务/INSERT 可运行，数据不落任何真实存储），
// 并挂载审计捕获回调。
func openTestGorm(spy *auditSpy) (*gorm.DB, error) {
	db, err := gorm.Open(fakeDialector{}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if spy != nil {
		if err := db.Callback().Create().Before("gorm:create").Register("sf_audit_spy", func(tx *gorm.DB) {
			if dest, ok := tx.Statement.Dest.(*middleware.OperationLog); ok {
				spy.record(*dest)
			}
		}); err != nil {
			return nil, err
		}
	}
	return db, nil
}
