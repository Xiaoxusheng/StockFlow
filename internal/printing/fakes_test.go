package printing

// 测试替身（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
// 模式与 internal/returns、internal/masterdata 既有替身同源（同一项目约定，非第二套机制）：
//   - fake gorm 驱动：事务可运行、审计写入经回调探针捕获（returns fakes_test 同款），
//     Begin/Commit/Rollback 挂钩——回滚时恢复内存替身快照，使"任一步失败整体回滚"
//     （architecture.md §4）的断言在无库环境下仍然为真；
//   - fakeRepo：Repository 接口的内存实现（语义对齐 GORM 实现的状态守卫行数判定）；
//   - fakeQueue：asynqx.Queue 内存替身（捕获入队任务，可注入失败）；
//   - fakeReader：ContentReader 内存替身（表驱动数据，可注入缺失/错误）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// ---- 事务生命周期钩子 ----

type txHooks struct {
	mu         sync.Mutex
	onBegin    []func()
	onCommit   []func()
	onRollback []func()
}

// ---- database/sql 假驱动（每个 env 独立注册，钩子经 env 传递）----

var (
	drvMu  sync.Mutex
	drvSeq int
)

func registerFakeDriver(env *txHooks) string {
	drvMu.Lock()
	defer drvMu.Unlock()
	drvSeq++
	name := "sfake-printing-" + strconv.Itoa(drvSeq)
	sql.Register(name, fakeDriver{env: env})
	return name
}

type fakeDriver struct{ env *txHooks }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{env: d.env}, nil }

type fakeConn struct{ env *txHooks }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	c.env.mu.Lock()
	for _, f := range c.env.onBegin {
		f()
	}
	c.env.mu.Unlock()
	return fakeTx{env: c.env}, nil
}

type fakeTx struct{ env *txHooks }

func (t fakeTx) Commit() error {
	t.env.mu.Lock()
	for _, f := range t.env.onCommit {
		f()
	}
	t.env.mu.Unlock()
	return nil
}

func (t fakeTx) Rollback() error {
	t.env.mu.Lock()
	for _, f := range t.env.onRollback {
		f()
	}
	t.env.mu.Unlock()
	return nil
}

type fakeStmt struct{}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{}, nil
}

// fakeRows 单行单列（id=1）：支撑 RETURNING / docnum 计数器扫描路径。
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

type fakeDialector struct{ hooks *txHooks }

func (fakeDialector) Name() string { return "sfake-printing" }

func (d fakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	sqlDB, err := sql.Open(registerFakeDriver(d.hooks), "")
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

func (fakeDialector) DataTypeOf(*schema.Field) string { return "text" }

func (fakeDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteByte('`')
	writer.WriteString(str)
	writer.WriteByte('`')
}

func (fakeDialector) Explain(sqlStr string, vars ...any) string { return "" }

// ---- 审计探针（真路径：middleware.Audit → tx.Create）----

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

// openTestGorm 打开假 gorm 句柄（事务/INSERT 可运行，数据不落任何真实存储）。
func openTestGorm(t *testing.T, hooks *txHooks, spy *auditSpy) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(fakeDialector{hooks: hooks}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	if spy != nil {
		if err := db.Callback().Create().Before("gorm:create").Register("sf_printing_audit_spy", func(tx *gorm.DB) {
			if dest, ok := tx.Statement.Dest.(*middleware.OperationLog); ok {
				spy.record(*dest)
			}
		}); err != nil {
			t.Fatalf("注册审计探针失败: %v", err)
		}
	}
	return db
}

// ---- fakeRepo：Repository 内存实现 ----

type fakeRepo struct {
	mu        sync.Mutex
	db        *gorm.DB
	seq       int64
	templates map[int64]*PrintTemplate
	tasks     map[int64]*PrintTask
	rows      map[int64][]*PrintTaskRow // taskID → rows
	snap      *fakeRepoSnap
	// 注入故障（基础设施错误路径单测）
	failFindTaskByNo bool
}

type fakeRepoSnap struct {
	seq       int64
	templates map[int64]*PrintTemplate
	tasks     map[int64]*PrintTask
	rows      map[int64][]*PrintTaskRow
}

func newFakeRepo(db *gorm.DB, hooks *txHooks) *fakeRepo {
	f := &fakeRepo{
		db:        db,
		templates: map[int64]*PrintTemplate{},
		tasks:     map[int64]*PrintTask{},
		rows:      map[int64][]*PrintTaskRow{},
	}
	hooks.onBegin = append(hooks.onBegin, f.snapshot)
	hooks.onCommit = append(hooks.onCommit, f.dropSnap)
	hooks.onRollback = append(hooks.onRollback, f.restore)
	return f
}

func (f *fakeRepo) DB() *gorm.DB { return f.db }

func (f *fakeRepo) nextID() int64 {
	f.seq++
	return f.seq
}

func (f *fakeRepo) snapshot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	tpl := make(map[int64]*PrintTemplate, len(f.templates))
	for k, v := range f.templates {
		cp := *v
		tpl[k] = &cp
	}
	tk := make(map[int64]*PrintTask, len(f.tasks))
	for k, v := range f.tasks {
		cp := *v
		tk[k] = &cp
	}
	rows := make(map[int64][]*PrintTaskRow, len(f.rows))
	for k, vs := range f.rows {
		cp := make([]*PrintTaskRow, len(vs))
		for i, v := range vs {
			c := *v
			cp[i] = &c
		}
		rows[k] = cp
	}
	f.snap = &fakeRepoSnap{seq: f.seq, templates: tpl, tasks: tk, rows: rows}
}

func (f *fakeRepo) dropSnap() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = nil
}

func (f *fakeRepo) restore() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snap == nil {
		return
	}
	f.seq, f.templates, f.tasks, f.rows =
		f.snap.seq, f.snap.templates, f.snap.tasks, f.snap.rows
	f.snap = nil
}

func (f *fakeRepo) InsertTemplate(tx *gorm.DB, t *PrintTemplate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = database.ID(f.nextID())
	cp := *t
	f.templates[t.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) UpdateTemplate(tx *gorm.DB, t *PrintTemplate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.templates[t.ID.Int64()]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	cp := *t
	f.templates[t.ID.Int64()] = &cp
	_ = cur
	return nil
}

func (f *fakeRepo) FindTemplate(ctx context.Context, id int64) (*PrintTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.templates[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) ListTemplates(ctx context.Context, flt TemplateFilter) ([]*PrintTemplate, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PrintTemplate
	for _, t := range f.templates {
		if flt.ObjectType != "" && t.ObjectType != flt.ObjectType {
			continue
		}
		if flt.Status != "" && t.Status != flt.Status {
			continue
		}
		if flt.Keyword != "" && !containsFold(t.Name, flt.Keyword) && !containsFold(t.Remark, flt.Keyword) {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	total := int64(len(out))
	out = pageSlice(out, flt.Page, flt.PageSize)
	return out, total, nil
}

func (f *fakeRepo) UpdateTemplateStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok || t.Status != from {
		return 0, nil
	}
	t.Status = to
	t.UpdatedBy = by
	return 1, nil
}

func (f *fakeRepo) InsertTask(tx *gorm.DB, t *PrintTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = database.ID(f.nextID())
	cp := *t
	f.tasks[t.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) InsertTaskRows(tx *gorm.DB, rows []*PrintTaskRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	taskID := rows[0].TaskID
	f.rows[taskID] = nil
	for _, r := range rows {
		cp := *r
		cp.ID = database.ID(f.nextID())
		f.rows[taskID] = append(f.rows[taskID], &cp)
	}
	return nil
}

func (f *fakeRepo) FindTask(ctx context.Context, id int64) (*PrintTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindTaskByNo(ctx context.Context, printNo string) (*PrintTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFindTaskByNo {
		return nil, fmt.Errorf("injected db failure")
	}
	for _, t := range f.tasks {
		if t.PrintNo == printNo {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListTasks(ctx context.Context, flt TaskFilter) ([]*PrintTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PrintTask
	for _, t := range f.tasks {
		if flt.ID > 0 && t.ID.Int64() != flt.ID {
			continue
		}
		if flt.TemplateID > 0 && t.TemplateID != flt.TemplateID {
			continue
		}
		if flt.ObjectType != "" && t.ObjectType != flt.ObjectType {
			continue
		}
		if flt.Status != "" && t.Status != flt.Status {
			continue
		}
		if flt.ConfirmedOnly && t.Result == nil {
			continue
		}
		if flt.Result != "" && (t.Result == nil || *t.Result != flt.Result) {
			continue
		}
		if flt.Keyword != "" && !containsFold(t.PrintNo, flt.Keyword) {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	total := int64(len(out))
	out = pageSlice(out, flt.Page, flt.PageSize)
	return out, total, nil
}

func (f *fakeRepo) ListTaskRows(ctx context.Context, taskID int64) ([]*PrintTaskRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*PrintTaskRow, 0, len(f.rows[taskID]))
	for _, r := range f.rows[taskID] {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepo) UpdateTaskStatus(tx *gorm.DB, printNo string, from []string, to, errMsg string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tasks {
		if t.PrintNo != printNo {
			continue
		}
		for _, s := range from {
			if t.Status == s {
				t.Status = to
				if to == TaskStatusFailed {
					t.ErrorMessage = errMsg
				}
				return 1, nil
			}
		}
	}
	return 0, nil
}

func (f *fakeRepo) UpdateExecuteResult(tx *gorm.DB, id int64, result, message string, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || t.Result != nil {
		return 0, nil
	}
	t.Result = &result
	t.PrintedBy = by
	t.PrintedAt = database.Now()
	if result == ResultFailed && message != "" {
		t.ErrorMessage = message
	}
	return 1, nil
}

// ---- fakeQueue：asynqx.Queue 内存替身 ----

type fakeQueue struct {
	mu    sync.Mutex
	tasks []asynqx.Task
	err   error
}

func (q *fakeQueue) Enqueue(ctx context.Context, t asynqx.Task, opts ...asynqx.Option) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.tasks = append(q.tasks, t)
	return nil
}

func (q *fakeQueue) all() []asynqx.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]asynqx.Task(nil), q.tasks...)
}

// ---- fakeReader：ContentReader 内存替身 ----

type fakeReader struct {
	mu        sync.Mutex
	calls     int
	lastIDs   []string
	lastField []string
	rowsByID  map[string]ContentRow // 命中表
	missing   map[string]bool       // 注入缺失
	err       error                 // 注入基础设施错误
}

func (r *fakeReader) Assemble(ctx context.Context, ids []string, fields []string) ([]ContentRow, error) {
	r.mu.Lock()
	r.calls++
	r.lastIDs = append([]string(nil), ids...)
	r.lastField = append([]string(nil), fields...)
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	var missing []string
	out := make([]ContentRow, 0, len(ids))
	for _, id := range ids {
		if r.missing[id] {
			missing = append(missing, id)
			continue
		}
		row, ok := r.rowsByID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		out = append(out, row)
	}
	if err := FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 测试环境组装 ----

type testEnv struct {
	svc    *Service
	repo   *fakeRepo
	queue  *fakeQueue
	reader *fakeReader
	spy    *auditSpy
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	hooks := &txHooks{}
	spy := &auditSpy{}
	db := openTestGorm(t, hooks, spy)
	repo := newFakeRepo(db, hooks)
	reader := &fakeReader{rowsByID: map[string]ContentRow{}}
	queue := &fakeQueue{}
	svc := NewService(repo, WithQueue(queue))
	// 7 类外部对象全部注入同一替身（内置 CARTON/PALLET 无需注入）。
	for _, ot := range objectTypes {
		if IsBuiltinObjectType(ot) {
			continue
		}
		svc.opt.readers[ot] = reader
	}
	return &testEnv{svc: svc, repo: repo, queue: queue, reader: reader, spy: spy}
}

// seedTemplate 落一张模板（直写 repo，绕过 Service 校验——任务侧测试的前置）。
func (e *testEnv) seedTemplate(t *testing.T, objectType string, enabled bool) *PrintTemplate {
	t.Helper()
	tpl := &PrintTemplate{
		Name:          "模板-" + objectType,
		ObjectType:    objectType,
		Paper:         PaperA4,
		QRCodeEnabled: false,
		Fields:        jsonb("{}"),
		Status:        StatusEnabled,
	}
	if objectType == ObjectSKULabel {
		sym := SymbologyCode128
		tpl.BarcodeSymbology = &sym
	}
	if !enabled {
		tpl.Status = StatusDisabled
	}
	if err := e.repo.InsertTemplate(e.repo.DB(), tpl); err != nil {
		t.Fatalf("seed 模板失败: %v", err)
	}
	return tpl
}

// ---- 小工具 ----

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func pageSlice[T any](items []T, page, pageSize int) []T {
	if page <= 0 || pageSize <= 0 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// ---- 断言工具 ----

// asPrintErr 断言错误为指定业务码。
func asPrintErr(t *testing.T, err error, code string) *response.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，得到 nil", code)
	}
	var e *response.Error
	if !errors.As(err, &e) {
		t.Fatalf("期望业务错误 %s，得到 %v", code, err)
	}
	if !containsFold(e.Error(), code) {
		t.Fatalf("期望错误码 %s，得到 %s", code, e.Error())
	}
	return e
}
