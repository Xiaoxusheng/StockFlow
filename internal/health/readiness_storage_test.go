package health

// 就绪探针存储根探测分支补充测试（health.go probeWritableDir——backend-m3-plan §6.4
// 引入、M1 §12 挂账销项、deployment §3）。既有 health_test.go 覆盖 nil db 与 1 秒缓存
// 语义，未触达 storageRoots 探测——此处补齐：
//   - 存储根可写 → storage=UP 且探测文件清理干净（不遗留）；
//   - 存储根不可写（父路径为文件）→ storage=DOWN → 503；
//   - db DOWN + storage UP 组合：整体仍 503，details 同时携带两组件状态。
//
// 不依赖 PostgreSQL/Redis：假 gorm 驱动 Ping 可编程（UP/DOWN 驱动），rdb 恒 nil
// （redis=DISABLED 不阻塞就绪）。探针实例每次 Readiness() 独立缓存，无跨用例串扰。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// ---- database/sql 假驱动（Ping 可编程，其余语句 fail-loud）----

var (
	hdrvOnce sync.Once
	hdrvMu   sync.Mutex
	hdrvPing error
)

const hdrvName = "sfake-health-storage-test"

func hdrvRegister() {
	hdrvOnce.Do(func() { sql.Register(hdrvName, healthFakeDriver{}) })
}

func setHealthPingErr(err error) {
	hdrvRegister()
	hdrvMu.Lock()
	defer hdrvMu.Unlock()
	hdrvPing = err
}

type healthFakeDriver struct{}

func (healthFakeDriver) Open(string) (driver.Conn, error) { return &healthFakeConn{}, nil }

type healthFakeConn struct{}

func (*healthFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("health 测试假驱动不支持语句执行（探针只 Ping）")
}
func (*healthFakeConn) Close() error { return nil }
func (*healthFakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("health 测试假驱动不支持事务")
}

// Ping 可编程：/ready 的 database 组件据此判 UP/DOWN。
func (*healthFakeConn) Ping(context.Context) error {
	hdrvMu.Lock()
	defer hdrvMu.Unlock()
	if hdrvPing != nil {
		return hdrvPing
	}
	return nil
}

var (
	_ driver.Conn   = (*healthFakeConn)(nil)
	_ driver.Pinger = (*healthFakeConn)(nil)
)

// ---- gorm 方言器（DummyDialector 形态 + 可用 ConnPool）----

type healthFakeDialector struct{}

func (healthFakeDialector) Name() string { return "sfake-health-storage" }

func (healthFakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	hdrvRegister()
	sqlDB, err := sql.Open(hdrvName, "")
	if err != nil {
		return err
	}
	db.ConnPool = sqlDB
	return nil
}

func (healthFakeDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}
func (healthFakeDialector) Migrator(*gorm.DB) gorm.Migrator { return nil }
func (healthFakeDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	writer.WriteByte('?')
}
func (healthFakeDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteByte('`')
	writer.WriteString(str)
	writer.WriteByte('`')
}
func (healthFakeDialector) Explain(sqlStr string, vars ...any) string {
	return logger.ExplainSQL(sqlStr, nil, `"`, vars...)
}
func (healthFakeDialector) DataTypeOf(*schema.Field) string { return "text" }

// openHealthFakeGorm 打开假 gorm 句柄（探针只 Ping，无语句执行；Ping 语义由
// setHealthPingErr 独立驱动，本函数不改变其取值）。
func openHealthFakeGorm(t *testing.T) *gorm.DB {
	t.Helper()
	hdrvRegister()
	db, err := gorm.Open(healthFakeDialector{}, &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
	require.NoError(t, err)
	return db
}

// readyRequest 发起一次 /ready 请求并解析信封。
func readyRequest(t *testing.T, h gin.HandlerFunc) (int, map[string]any) {
	t.Helper()
	c, rec := newCtx(t)
	h(c)
	var env map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "body=%s", rec.Body.String())
	return rec.Code, env
}

func TestReadinessStorageRootWritableIsUP(t *testing.T) {
	setHealthPingErr(nil)
	root := t.TempDir()

	status, env := readyRequest(t, Readiness(openHealthFakeGorm(t), nil, root))
	require.Equal(t, http.StatusOK, status, "%v", env)
	data := env["data"].(map[string]any)
	require.Equal(t, "UP", data["database"])
	require.Equal(t, "DISABLED", data["redis"])
	require.Equal(t, "UP", data["storage"], "可写存储根应判 UP（backend-m3-plan §6.4）")
	// 探测文件不遗留（probeWritableDir 写入后必须清理）。
	_, err := os.Stat(filepath.Join(root, ".ready-probe"))
	require.True(t, os.IsNotExist(err), "探测文件应清理干净，禁止遗留根目录垃圾")
}

func TestReadinessStorageRootUnwritableIsDOWN(t *testing.T) {
	setHealthPingErr(nil)
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644)) // 以文件占位，MkdirAll 必败
	root := filepath.Join(blocker, "sub")

	status, env := readyRequest(t, Readiness(openHealthFakeGorm(t), nil, root))
	require.Equal(t, http.StatusServiceUnavailable, status, "%v", env)
	details := env["details"].(map[string]any)
	require.Equal(t, "UP", details["database"])
	require.Equal(t, "DOWN", details["storage"], "不可写存储根必须判 DOWN（deployment §3 禁止带病部署）")
}

func TestReadinessDBDownStorageUPCombination(t *testing.T) {
	setHealthPingErr(errors.New("pg 不可达"))
	root := t.TempDir()

	status, env := readyRequest(t, Readiness(openHealthFakeGorm(t), nil, root))
	require.Equal(t, http.StatusServiceUnavailable, status, "%v", env)
	details := env["details"].(map[string]any)
	require.Equal(t, "DOWN", details["database"])
	require.Equal(t, "UP", details["storage"], "db DOWN 不应掩盖 storage 组件真实状态")
	require.Equal(t, "DISABLED", details["redis"])
}
