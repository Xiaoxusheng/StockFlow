package devices

// 测试替身（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
// 模式与 internal/masterdata/fakedb_test.go、internal/returns/fakes_test.go 同源
// （同一项目约定，非第二套机制）：
//   - fake gorm 驱动：事务可运行（database.Tx 路径）、审计写入经回调探针捕获——
//     驱动层全部 no-op 成功，业务数据由 fakeRepo 内存承载；
//   - fakeRepo：Repository 接口的内存实现（语义对齐 GORM 实现的守卫 UPDATE 行数判定
//     与激活一次性消费语义）；
//   - fakeChecker：WarehouseChecker 内存实现；
//   - 时钟经 Service.setNow 注入（激活时效/去重窗口/在线判定的确定性断言）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
)

// ---- database/sql 假驱动（形态对齐 masterdata fakedb_test.go）----

var (
	drvMu  sync.Mutex
	drvSeq int
)

func registerFakeDriver() string {
	drvMu.Lock()
	defer drvMu.Unlock()
	drvSeq++
	name := "sfake-devices-" + strconv.Itoa(drvSeq)
	sql.Register(name, fakeDriver{})
	return name
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 } // 不校验参数个数
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
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

var (
	_ driver.Conn = (*fakeConn)(nil)
	_ driver.Stmt = (*fakeStmt)(nil)
	_ driver.Tx   = fakeTx{}
	_ driver.Rows = (*fakeRows)(nil)
)

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests DummyDialector + 可用 ConnPool）----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake-devices" }

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

// ---- fakeRepo：Repository 内存实现 ----

type fakeRepo struct {
	mu          sync.Mutex
	db          *gorm.DB
	seq         int64
	devices     map[int64]*Device
	byCode      map[string]int64
	configs     map[int64]*DeviceConfig
	scanLogs    []*ScanLog
	deviceLogs  map[int64][]*DeviceLog
	appVersions []*AppVersion
	scanLogFail bool // scan_logs 写入失败注入（降级放行断言）
}

func newFakeRepo(db *gorm.DB) *fakeRepo {
	return &fakeRepo{
		db:         db,
		devices:    map[int64]*Device{},
		byCode:     map[string]int64{},
		configs:    map[int64]*DeviceConfig{},
		deviceLogs: map[int64][]*DeviceLog{},
	}
}

func (r *fakeRepo) DB() *gorm.DB { return r.db }

func (r *fakeRepo) InsertDevice(tx *gorm.DB, d *Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byCode[d.Code]; ok {
		return fmt.Errorf("uk_devices_code 冲突: %s", d.Code)
	}
	r.seq++
	d.ID = database.ID(r.seq)
	cp := *d
	r.devices[int64(d.ID)] = &cp
	r.byCode[d.Code] = int64(d.ID)
	return nil
}

func (r *fakeRepo) snapshot(id int64) *Device {
	cp := *r.devices[id]
	return &cp
}

func (r *fakeRepo) FindDevice(_ context.Context, id int64) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.devices[id]
	if !ok {
		return nil, ErrNotFound
	}
	return r.snapshot(id), nil
}

func (r *fakeRepo) FindDeviceByCode(_ context.Context, code string) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byCode[code]
	if !ok {
		return nil, ErrNotFound
	}
	return r.snapshot(id), nil
}

func (r *fakeRepo) ListDevices(_ context.Context, f DeviceFilter) ([]*Device, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Device
	for id := range r.devices {
		d := r.snapshot(id)
		if f.Type != "" && d.Type != f.Type {
			continue
		}
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		if f.WarehouseID > 0 && d.WarehouseID != f.WarehouseID {
			continue
		}
		if f.Keyword != "" && !strings.Contains(d.Code, f.Keyword) && !strings.Contains(d.Name, f.Keyword) {
			continue
		}
		if f.Online != nil {
			online := deviceOnline(d.LastOnlineAt, f.OnlineCutoff.Add(onlineWindow))
			if online != *f.Online {
				continue
			}
		}
		if !f.AllWarehouses {
			found := false
			for _, wid := range f.WarehouseIDs {
				if d.WarehouseID == wid {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	total := int64(len(out))
	if f.PageSize > 0 && f.Page > 0 {
		lo := (f.Page - 1) * f.PageSize
		if lo >= len(out) {
			out = nil
		} else {
			hi := lo + f.PageSize
			if hi > len(out) {
				hi = len(out)
			}
			out = out[lo:hi]
		}
	}
	return out, total, nil
}

func (r *fakeRepo) UpdateDeviceStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok || d.Status != from {
		return 0, nil
	}
	d.Status = to
	d.UpdatedBy = by
	d.UpdatedAt = database.JSONTime{Time: time.Now()}
	return 1, nil
}

func (r *fakeRepo) BumpTokenVersion(tx *gorm.DB, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.TokenVersion++
	return nil
}

func (r *fakeRepo) UpdateDeviceBinding(tx *gorm.DB, id, userID, by int64, bumpTokenVersion bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return ErrNotFound
	}
	if bumpTokenVersion && d.Status != StatusEnabled {
		return ErrStatusConflict
	}
	d.BoundUserID = userID
	d.UpdatedBy = by
	if bumpTokenVersion {
		d.TokenVersion++
	}
	return nil
}

func (r *fakeRepo) RegenerateActivation(tx *gorm.DB, id int64, tokenHash string, expiresAt time.Time, by int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok || d.Status != StatusEnabled {
		return 0, nil
	}
	d.ActivationStatus = ActivationPending
	d.ActivationTokenHash = tokenHash
	d.ActivationExpiresAt = database.JSONTime{Time: expiresAt}
	d.ActivatedAt = database.JSONTime{}
	d.ActivatedBy = 0
	d.TokenVersion++
	d.UpdatedBy = by
	return 1, nil
}

func (r *fakeRepo) ConsumeActivation(tx *gorm.DB, code, tokenHash string, info DeviceSelfInfo, now time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byCode[code]
	if !ok {
		return 0, nil
	}
	d := r.devices[id]
	// 一次性消费语义（对齐 GORM 实现守卫 UPDATE）：PENDING + 哈希匹配 + 未过期。
	if d.ActivationStatus != ActivationPending || d.ActivationTokenHash != tokenHash ||
		d.ActivationExpiresAt.IsZero() || !d.ActivationExpiresAt.After(now) {
		return 0, nil
	}
	d.ActivationStatus = ActivationActivated
	d.ActivationTokenHash = ""
	d.ActivationExpiresAt = database.JSONTime{}
	d.ActivatedAt = database.JSONTime{Time: now}
	d.Brand, d.Model, d.OS, d.AppVersion, d.IP = info.Brand, info.Model, info.OS, info.AppVersion, info.IP
	return 1, nil
}

func (r *fakeRepo) UpdateDeviceHeartbeat(tx *gorm.DB, id int64, cols map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return ErrNotFound
	}
	for k, v := range cols {
		switch k {
		case "last_online_at":
			d.LastOnlineAt = asJSONTime(v)
		case "battery_level":
			if v == nil {
				d.BatteryLevel = nil
			} else {
				b := v.(int)
				d.BatteryLevel = &b
			}
		case "app_version":
			d.AppVersion = v.(string)
		case "ip":
			d.IP = v.(string)
		case "updated_at":
			d.UpdatedAt = asJSONTime(v)
		}
	}
	return nil
}

// asJSONTime Service 传入的时钟值为 time.Time（GORM 实现直写 time 列），统一转换。
func asJSONTime(v any) database.JSONTime {
	switch t := v.(type) {
	case database.JSONTime:
		return t
	case time.Time:
		return database.JSONTime{Time: t}
	}
	panic(fmt.Sprintf("非法时钟值类型 %T", v))
}

func (r *fakeRepo) UpdateDeviceLastScan(_ context.Context, id int64, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.LastScanAt = database.JSONTime{Time: at}
	return nil
}

func (r *fakeRepo) UpsertDeviceConfig(tx *gorm.DB, deviceID int64, cfg jsonb, by int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.configs[deviceID]
	if !ok {
		r.configs[deviceID] = &DeviceConfig{DeviceID: deviceID, Config: cfg, Version: 1, CreatedBy: by}
		return 1, nil
	}
	c.Config = cfg
	c.Version++
	c.UpdatedBy = by
	return c.Version, nil
}

func (r *fakeRepo) FindDeviceConfig(_ context.Context, deviceID int64) (*DeviceConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.configs[deviceID]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) InsertScanLog(tx *gorm.DB, l *ScanLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scanLogFail {
		return errors.New("注入的 scan_logs 写失败")
	}
	r.seq++
	l.ID = database.ID(r.seq)
	cp := *l
	r.scanLogs = append(r.scanLogs, &cp)
	return nil
}

func (r *fakeRepo) ListScanLogs(_ context.Context, f ScanLogFilter) ([]*ScanLog, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*ScanLog
	for _, l := range r.scanLogs {
		if f.DeviceID > 0 && (l.DeviceID == nil || *l.DeviceID != f.DeviceID) {
			continue
		}
		if f.UserID > 0 && l.UserID != f.UserID {
			continue
		}
		if f.WarehouseID > 0 && l.WarehouseID != f.WarehouseID {
			continue
		}
		if f.Success != nil && l.Success != *f.Success {
			continue
		}
		out = append(out, l)
	}
	total := int64(len(out))
	return out, total, nil
}

func (r *fakeRepo) CountScanLogs(_ context.Context, deviceID int64, since time.Time) (total, today int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.scanLogs {
		if l.DeviceID == nil || *l.DeviceID != deviceID {
			continue
		}
		total++
		if l.CreatedAt.After(since) {
			today++
		}
	}
	return total, today, nil
}

func (r *fakeRepo) InsertDeviceLogs(tx *gorm.DB, logs []*DeviceLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range logs {
		r.seq++
		l.ID = database.ID(r.seq)
		cp := *l
		r.deviceLogs[l.DeviceID] = append(r.deviceLogs[l.DeviceID], &cp)
		_ = i
	}
	return nil
}

func (r *fakeRepo) ListDeviceLogs(_ context.Context, f DeviceLogFilter) ([]*DeviceLog, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*DeviceLog
	for _, l := range r.deviceLogs[f.DeviceID] {
		if f.Level != "" && l.Level != f.Level {
			continue
		}
		out = append(out, l)
	}
	total := int64(len(out))
	return out, total, nil
}

func (r *fakeRepo) FindLatestAppVersion(_ context.Context, platform string) (*AppVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *AppVersion
	for i := range r.appVersions {
		v := r.appVersions[i]
		if v.Platform != platform || v.Status != AppVersionPublished {
			continue
		}
		if best == nil || v.VersionCode > best.VersionCode {
			best = v
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	cp := *best
	return &cp, nil
}

// seedAppVersion 测试种子：App 版本行。
func (r *fakeRepo) seedAppVersion(v *AppVersion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	v.ID = database.ID(r.seq)
	r.appVersions = append(r.appVersions, v)
}

// ---- fakeChecker：WarehouseChecker 内存实现 ----

type fakeChecker struct {
	exists map[int64]bool
}

func (c *fakeChecker) ExistsActive(_ context.Context, warehouseID int64) (bool, error) {
	return c.exists[warehouseID], nil
}

// ---- fakeSfqrSkus：SfqrSkuReader 内存实现（qr-code.md §6 匹配器 0 替身；
// 命中/停用/未命中/错误注入四态，仿 fakeSKUBarcodes 表驱动）----

type fakeSfqrSkus struct {
	hits map[string]Hit
	err  error // 注入读失败（fail-closed 断言）
}

func (f *fakeSfqrSkus) FindBySkuCode(_ context.Context, code string) (Hit, bool, error) {
	if f.err != nil {
		return Hit{}, false, f.err
	}
	h, ok := f.hits[code]
	return h, ok, nil
}

// ---- 测试环境 ----

type testEnv struct {
	repo    *fakeRepo
	checker *fakeChecker
	spy     *auditSpy
	svc     *Service
	now     time.Time
}

// newTestEnv 构建全套内存环境（时钟从 2026-10-03 10:00:00 起步，测试可推进）。
func newTestEnv(t *testing.T, opts ...Option) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode) // 非 release：令牌密钥走进程内随机降级（单测无需环境变量）
	spy := &auditSpy{}
	db, err := openTestGorm(spy)
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	repo := newFakeRepo(db)
	env := &testEnv{
		repo:    repo,
		checker: &fakeChecker{exists: map[int64]bool{1: true, 2: true}},
		spy:     spy,
		now:     time.Date(2026, 10, 3, 10, 0, 0, 0, time.Local),
	}
	env.svc = NewService(repo, append([]Option{
		WithWarehouseChecker(env.checker),
	}, opts...)...)
	env.svc.setNow(func() time.Time { return env.now })
	return env
}

// advance 推进测试时钟。
func (e *testEnv) advance(d time.Duration) { e.now = e.now.Add(d) }

// seedDevice 直接落一台已激活设备（绕过激活流程的用例用）。
func (e *testEnv) seedDevice(code string, warehouseID int64) *Device {
	d := &Device{
		Code:                code,
		Name:                "设备-" + code,
		Type:                TypePDA,
		WarehouseID:         warehouseID,
		Status:              StatusEnabled,
		ActivationStatus:    ActivationActivated,
		ActivatedAt:         database.JSONTime{Time: e.now},
		TokenVersion:        1,
		CreatedAt:           database.JSONTime{Time: e.now},
		UpdatedAt:           database.JSONTime{Time: e.now},
		ActivationExpiresAt: database.JSONTime{},
	}
	if err := e.repo.InsertDevice(nil, d); err != nil {
		panic(err)
	}
	return e.mustDevice(code)
}

func (e *testEnv) mustDevice(code string) *Device {
	d, err := e.repo.FindDeviceByCode(context.Background(), code)
	if err != nil {
		panic(fmt.Sprintf("seed device %s: %v", code, err))
	}
	return d
}
