package router

// 测试替身：假 gorm 方言器（database/sql 假驱动支撑）。
// 目的：M1 全量装配（backend-m1-plan §5.3）下 db==nil 属装配错误（各域启动期
// fail-fast），路由装配测试改以假驱动 gorm 句柄装配——单测不依赖 PostgreSQL/Redis/
// 网络（与 internal/auth、internal/masterdata、internal/warehouse 的 fakedb_test.go
// 同一项目约定，非第二套机制）。
//
// 与业务域假驱动的差异：本包用例不执行任何 SQL（保护组请求在 AuthRequired 即被拒），
// 假连接 Prepare/Begin/Ping 一律返回错误——其中 Ping 必败是刻意语义：health.Readiness
// 经 sqlDB.PingContext 探测数据库，据此判 DOWN，支撑 TestReadyFailsWithoutDependencies
// 的"依赖不可达 → 503"断言。若有用例意外触达 SQL，将得到响亮的 500 而非静默假数据。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// ---- database/sql 假驱动 ----

const fakeDriverName = "sfake-router-test"

var drvOnce sync.Once

func registerFakeDriver() {
	drvOnce.Do(func() {
		sql.Register(fakeDriverName, fakeDriver{})
	})
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("router 测试假驱动不支持语句执行（用例不应触达 SQL）")
}

func (*fakeConn) Close() error { return nil }

func (*fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("router 测试假驱动不支持事务")
}

// Ping 必败：/ready 探测（health.Readiness）据此判 database DOWN。
func (*fakeConn) Ping(context.Context) error {
	return errors.New("router 测试假驱动: 数据库依赖不可达")
}

// 编译期接口断言：fakeConn 必须同时是可连接与可探测的。
var (
	_ driver.Conn   = (*fakeConn)(nil)
	_ driver.Pinger = (*fakeConn)(nil)
)

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests 的 DummyDialector + 可用 ConnPool）----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake-router" }

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

// openFakeGorm 打开假 gorm 句柄（不落任何真实存储）。
// DisableAutomaticPing 必须开启：假连接 Ping 必败，否则 gorm.Open 自身的
// 自动探测即失败，句柄都拿不到。
func openFakeGorm() (*gorm.DB, error) {
	registerFakeDriver()
	return gorm.Open(fakeDialector{}, &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
}
