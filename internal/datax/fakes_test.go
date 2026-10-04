package datax

// 测试替身（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
// 模式与 internal/returns、internal/masterdata 既有替身同源（同一项目约定）：
//   - fake gorm 驱动：事务可运行、审计写入经回调探针捕获；Begin/Commit/Rollback
//     挂钩子——回滚时恢复内存替身快照，使"任一步失败整体回滚"（architecture.md §4）
//     的断言在无库环境下仍然为真；
//   - fakeRepo：Repository 接口的内存实现（守卫 UPDATE 行数判定语义对齐 GORM 实现）；
//   - fakeQueue：asynqx.Queue 内存替身（记录入队任务；可选立即执行回调模拟 inline）；
//   - fakeWriter/fakeSource：ImportWriter/ExportSource 替身（excelize 用构造的内存工作簿）。

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
)

// ---- 事务生命周期钩子 ----

type txHooks struct {
	mu         sync.Mutex
	onBegin    []func()
	onCommit   []func()
	onRollback []func()
}

func (h *txHooks) begin() {
	for _, f := range h.onBegin {
		f()
	}
}

func (h *txHooks) commit() {
	for _, f := range h.onCommit {
		f()
	}
}

func (h *txHooks) rollback() {
	for _, f := range h.onRollback {
		f()
	}
}

// ---- database/sql 假驱动（每个 env 独立注册）----

var (
	drvMu  sync.Mutex
	drvSeq int
)

func registerFakeDriver(env *txHooks) string {
	drvMu.Lock()
	defer drvMu.Unlock()
	drvSeq++
	name := "sfake-datax-" + strconv.Itoa(drvSeq)
	sql.Register(name, fakeDriver{env: env})
	return name
}

type fakeDriver struct{ env *txHooks }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{env: d.env}, nil }

type fakeConn struct{ env *txHooks }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	c.env.begin()
	return fakeTx{env: c.env}, nil
}

type fakeTx struct{ env *txHooks }

func (t fakeTx) Commit() error   { t.env.commit(); return nil }
func (t fakeTx) Rollback() error { t.env.rollback(); return nil }

type fakeStmt struct{}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{}, nil
}

// fakeRows 单行单列（id=1）：支撑 Create RETURNING 路径（真实 ID 由 fakeRepo 分配）。
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

type fakeDialector struct{ hooks *txHooks }

func (fakeDialector) Name() string { return "sfake" }

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

func (fakeDialector) Explain(string, ...any) string { return "" }

// ---- auditSpy：捕获同事务写入的审计行 ----

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

// actions 返回全部审计动作序列。
func (s *auditSpy) actions() []string {
	var out []string
	for _, e := range s.all() {
		out = append(out, e.Action)
	}
	return out
}

func openTestGorm(hooks *txHooks, spy *auditSpy) (*gorm.DB, error) {
	db, err := gorm.Open(fakeDialector{hooks: hooks}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if spy != nil {
		if err := db.Callback().Create().Before("gorm:create").Register("sf_datax_audit_spy", func(tx *gorm.DB) {
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
	mu  sync.Mutex
	db  *gorm.DB
	seq int64

	importTasks map[int64]*ImportTask
	importRows  map[int64]*ImportTaskRow // rowID → row
	exportTasks map[int64]*ExportTask
	files       map[int64]*storage.File
	// exportProgress 任务进度回写历史（进度断言用）。
	exportProgress map[int64][]int

	snap *fakeRepoSnap
}

type fakeRepoSnap struct {
	seq         int64
	importTasks map[int64]*ImportTask
	importRows  map[int64]*ImportTaskRow
	exportTasks map[int64]*ExportTask
	files       map[int64]*storage.File
}

func newFakeRepo(db *gorm.DB, hooks *txHooks) *fakeRepo {
	f := &fakeRepo{
		db:             db,
		importTasks:    map[int64]*ImportTask{},
		importRows:     map[int64]*ImportTaskRow{},
		exportTasks:    map[int64]*ExportTask{},
		files:          map[int64]*storage.File{},
		exportProgress: map[int64][]int{},
	}
	hooks.onBegin = append(hooks.onBegin, f.snapshot)
	hooks.onCommit = append(hooks.onCommit, f.dropSnap)
	hooks.onRollback = append(hooks.onRollback, f.restore)
	return f
}

func (f *fakeRepo) nextID() int64 {
	f.seq++
	return f.seq
}

func cloneTask(t *ImportTask) *ImportTask { cp := *t; return &cp }
func cloneExport(t *ExportTask) *ExportTask {
	cp := *t
	if t.Params != nil {
		cp.Params = JSONMap{}
		for k, v := range t.Params {
			cp.Params[k] = v
		}
	}
	return &cp
}

func (f *fakeRepo) snapshot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := map[int64]*ImportTaskRow{}
	for k, v := range f.importRows {
		cp := *v
		rows[k] = &cp
	}
	files := map[int64]*storage.File{}
	for k, v := range f.files {
		cp := *v
		files[k] = &cp
	}
	f.snap = &fakeRepoSnap{
		seq: f.seq, importTasks: map[int64]*ImportTask{}, importRows: rows,
		exportTasks: map[int64]*ExportTask{}, files: files,
	}
	for k, v := range f.importTasks {
		f.snap.importTasks[k] = cloneTask(v)
	}
	for k, v := range f.exportTasks {
		f.snap.exportTasks[k] = cloneExport(v)
	}
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
	f.seq = f.snap.seq
	f.importTasks = map[int64]*ImportTask{}
	for k, v := range f.snap.importTasks {
		f.importTasks[k] = cloneTask(v)
	}
	f.importRows = map[int64]*ImportTaskRow{}
	for k, v := range f.snap.importRows {
		f.importRows[k] = v
	}
	f.exportTasks = map[int64]*ExportTask{}
	for k, v := range f.snap.exportTasks {
		f.exportTasks[k] = cloneExport(v)
	}
	f.files = map[int64]*storage.File{}
	for k, v := range f.snap.files {
		f.files[k] = v
	}
}

func (f *fakeRepo) DB() *gorm.DB { return f.db }

func (f *fakeRepo) InsertImportTask(ctx context.Context, tx *gorm.DB, t *ImportTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = database.ID(f.nextID())
	f.seq++
	t.ImportNo = fmt.Sprintf("IMP-20260101-%06d", f.seq)
	cp := cloneTask(t)
	f.importTasks[t.ID.Int64()] = cp
	return nil
}

func (f *fakeRepo) FindImportTask(ctx context.Context, id int64) (*ImportTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.importTasks[id]
	if !ok {
		return nil, responseNotFound()
	}
	return cloneTask(t), nil
}

func (f *fakeRepo) FindImportTaskByNo(ctx context.Context, no string) (*ImportTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.importTasks {
		if t.ImportNo == no {
			return cloneTask(t), nil
		}
	}
	return nil, responseNotFound()
}

func (f *fakeRepo) ListImportTasks(ctx context.Context, fl TaskListFilter) ([]*ImportTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ImportTask
	for _, t := range f.importTasks {
		if fl.Module != "" && t.ImportType != fl.Module {
			continue
		}
		if fl.Status != "" && t.Status != fl.Status {
			continue
		}
		out = append(out, cloneTask(t))
	}
	// id DESC 排序（对齐 GORM 实现）。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].ID.Int64() < out[j].ID.Int64() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	total := int64(len(out))
	if fl.Page > 0 && fl.PageSize > 0 {
		start := (fl.Page - 1) * fl.PageSize
		if start >= len(out) {
			out = nil
		} else {
			end := start + fl.PageSize
			if end > len(out) {
				end = len(out)
			}
			out = out[start:end]
		}
	}
	return out, total, nil
}

func (f *fakeRepo) GuardUpdateImportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.importTasks[id]
	if !ok {
		return 0, nil
	}
	if len(from) > 0 {
		match := false
		for _, s := range from {
			if t.Status == s {
				match = true
				break
			}
		}
		if !match {
			return 0, nil
		}
	}
	applySet(t, set)
	return 1, nil
}

func (f *fakeRepo) InsertImportRows(tx *gorm.DB, rows []*ImportTaskRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		r.ID = database.ID(f.nextID())
		cp := *r
		f.importRows[r.ID.Int64()] = &cp
	}
	return nil
}

func (f *fakeRepo) ListImportRows(ctx context.Context, taskID int64, statuses []string, limit int) ([]*ImportTaskRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ImportTaskRow
	for _, r := range f.importRows {
		if r.TaskID != taskID {
			continue
		}
		if len(statuses) > 0 {
			match := false
			for _, s := range statuses {
				if r.Status == s {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, r)
	}
	// row_no ASC 排序。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].RowNo > out[j].RowNo {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRepo) UpdateImportRow(tx *gorm.DB, rowID int64, set map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.importRows[rowID]
	if !ok {
		return errors.New("fakeRepo: 行不存在")
	}
	applySetRow(r, set)
	return nil
}

func (f *fakeRepo) CountImportRowsByStatus(ctx context.Context, taskID int64) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int64{}
	for _, r := range f.importRows {
		if r.TaskID == taskID {
			out[r.Status]++
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertExportTask(ctx context.Context, tx *gorm.DB, t *ExportTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = database.ID(f.nextID())
	f.seq++
	t.ExportNo = fmt.Sprintf("EXP-20260101-%06d", f.seq)
	f.exportTasks[t.ID.Int64()] = cloneExport(t)
	return nil
}

func (f *fakeRepo) FindExportTask(ctx context.Context, id int64) (*ExportTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.exportTasks[id]
	if !ok {
		return nil, responseNotFound()
	}
	return cloneExport(t), nil
}

func (f *fakeRepo) FindExportTaskByNo(ctx context.Context, no string) (*ExportTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.exportTasks {
		if t.ExportNo == no {
			return cloneExport(t), nil
		}
	}
	return nil, responseNotFound()
}

func (f *fakeRepo) ListExportTasks(ctx context.Context, fl TaskListFilter) ([]*ExportTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ExportTask
	for _, t := range f.exportTasks {
		if fl.Module != "" && t.Module != fl.Module {
			continue
		}
		if fl.Status != "" && t.Status != fl.Status {
			continue
		}
		out = append(out, cloneExport(t))
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].ID.Int64() < out[j].ID.Int64() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	total := int64(len(out))
	return out, total, nil
}

func (f *fakeRepo) GuardUpdateExportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.exportTasks[id]
	if !ok {
		return 0, nil
	}
	if len(from) > 0 {
		match := false
		for _, s := range from {
			if t.Status == s {
				match = true
				break
			}
		}
		if !match {
			return 0, nil
		}
	}
	applySetExport(t, set)
	return 1, nil
}

func (f *fakeRepo) UpdateExportTask(ctx context.Context, id int64, set map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.exportTasks[id]
	if !ok {
		return errors.New("fakeRepo: 导出任务不存在")
	}
	if p, ok := set["progress"].(int); ok {
		t.Progress = p
		f.exportProgress[id] = append(f.exportProgress[id], p)
	}
	if tr, ok := set["total_rows"].(int64); ok {
		t.TotalRows = tr
	}
	return nil
}

func (f *fakeRepo) InsertFile(tx *gorm.DB, fi *storage.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi.ID = f.nextID()
	cp := *fi
	f.files[fi.ID] = &cp
	return nil
}

func (f *fakeRepo) FindFile(ctx context.Context, id int64) (*storage.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, ok := f.files[id]
	if !ok {
		return nil, responseNotFound()
	}
	cp := *fi
	return &cp, nil
}

func (f *fakeRepo) ListFiles(ctx context.Context, fl FileListFilter) ([]*storage.File, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*storage.File
	for _, fi := range f.files {
		if fl.Module != "" && fi.Module != fl.Module {
			continue
		}
		if fl.BusinessNo != "" && fi.BusinessNo != fl.BusinessNo {
			continue
		}
		// 数据权限（与 repo_gorm.go ListFiles / service_file.go fileVisible 同规则，
		// 内存替身镜像实现——service 单测口径与生产 SQL 一致）。
		if !f.fileVisibleLocked(ctx, fi, fl.Scope) {
			continue
		}
		cp := *fi
		out = append(out, &cp)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].ID < out[j].ID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	total := int64(len(out))
	return out, total, nil
}

// fileVisibleLocked FileScope 可见性判定（持锁调用——直接读 map，禁止再走带锁的
// Find*ByNo，sync.Mutex 不可重入；规则与 repo_gorm.go SQL 同源）。
func (f *fakeRepo) fileVisibleLocked(_ context.Context, fi *storage.File, scope *FileScope) bool {
	if scope == nil || scope.All {
		return true
	}
	if fi.UploaderID == scope.UserID {
		return true
	}
	if fi.Module == "datax" {
		for _, t := range f.exportTasks {
			if t.ExportNo == fi.BusinessNo {
				all, ids := exportScopeSnapshot(t.Params)
				if all {
					return true
				}
				for _, id := range ids {
					for _, wid := range scope.WarehouseIDs {
						if id == wid {
							return true
						}
					}
				}
				return false
			}
		}
		for _, t := range f.importTasks {
			if t.ImportNo == fi.BusinessNo {
				return int64(t.CreatedBy) == scope.UserID
			}
		}
	}
	return false
}

func (f *fakeRepo) SoftDeleteFile(tx *gorm.DB, id int64, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, ok := f.files[id]
	if !ok || fi.DeletedAt.Valid {
		return 0, nil
	}
	fi.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	fi.UpdatedBy = by
	return 1, nil
}

// asInt/asInt64 数值宽容转换（生产侧 int/int64 混用场景与 GORM Updates 行为对齐）。
func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	}
	return 0
}

// applySet/applySetRow/applySetExport set 键 → 内存字段（白名单语义与 repo_gorm 一致）。
func applySet(t *ImportTask, set map[string]any) {
	for k, v := range set {
		switch k {
		case "status":
			t.Status = v.(string)
		case "valid_rows":
			t.ValidRows = asInt(v)
		case "error_rows":
			t.ErrorRows = asInt(v)
		case "success_rows":
			t.SuccessRows = asInt(v)
		case "failed_rows":
			t.FailedRows = asInt(v)
		case "error_file_id":
			t.ErrorFileID = v.(int64)
		case "error_message":
			t.ErrorMessage = v.(string)
		case "started_at":
			if tv, ok := v.(time.Time); ok {
				t.StartedAt = &tv
			}
		case "finished_at":
			if tv, ok := v.(time.Time); ok {
				t.FinishedAt = &tv
			}
		}
	}
}

func applySetRow(r *ImportTaskRow, set map[string]any) {
	for k, v := range set {
		switch k {
		case "status":
			r.Status = v.(string)
		case "parsed":
			if v == nil {
				r.Parsed = nil
			} else if m, ok := v.(JSONMap); ok {
				r.Parsed = m
			}
		case "errors":
			if v == nil {
				r.Errors = nil
			} else if errs, ok := v.([]RowError); ok {
				r.Errors = errs
			}
		case "batch_no":
			r.BatchNo = asInt(v)
		}
	}
}

func applySetExport(t *ExportTask, set map[string]any) {
	for k, v := range set {
		switch k {
		case "status":
			t.Status = v.(string)
		case "progress":
			t.Progress = asInt(v)
		case "total_rows":
			t.TotalRows = asInt64(v)
		case "file_id":
			t.FileID = v.(int64)
		case "error_message":
			t.ErrorMessage = v.(string)
		case "started_at":
			if tv, ok := v.(time.Time); ok {
				t.StartedAt = &tv
			}
		case "finished_at":
			if tv, ok := v.(time.Time); ok {
				t.FinishedAt = &tv
			}
		}
	}
}

// ---- fakeQueue：asynqx.Queue 内存替身 ----

type fakeQueue struct {
	mu    sync.Mutex
	tasks []asynqx.Task
	// inline 非空时 Enqueue 即执行（模拟 inlineQueue 语义）。
	inline func(ctx context.Context, t asynqx.Task) error
}

func (q *fakeQueue) Enqueue(ctx context.Context, t asynqx.Task, _ ...asynqx.Option) error {
	q.mu.Lock()
	q.tasks = append(q.tasks, t)
	q.mu.Unlock()
	if q.inline != nil {
		return q.inline(ctx, t)
	}
	return nil
}

func (q *fakeQueue) all() []asynqx.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]asynqx.Task(nil), q.tasks...)
}

// ---- fakeWriter / fakeSource：契约替身 ----

type fakeWriter struct {
	spec         TemplateSpec
	validateErrs []RowError
	validateErr  error
	// commitFn 自定义提交行为；nil = 全部成功。
	commitFn func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error)
	// commits 记录每次 Commit 收到的行号集合（断点续跑断言）。
	commitRows [][]int
}

func (w *fakeWriter) Template() TemplateSpec { return w.spec }

func (w *fakeWriter) Validate(ctx context.Context, rows []ImportRow) ([]RowError, error) {
	if w.validateErr != nil {
		return nil, w.validateErr
	}
	return w.validateErrs, nil
}

func (w *fakeWriter) Commit(ctx context.Context, actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
	rowNos := make([]int, 0, len(rows))
	for _, r := range rows {
		rowNos = append(rowNos, r.RowNo)
	}
	w.commitRows = append(w.commitRows, rowNos)
	if w.commitFn != nil {
		return w.commitFn(actor, ref, rows)
	}
	return CommitResult{SuccessRows: len(rows)}, nil
}

type fakeSource struct {
	cols    []Column
	rows    []Row
	summary []SummaryRow
	batch   int

	countErr    error
	batchErrAt  int  // 第 N 次 Batch 返回基础设施错误（1 起；0=不注入）
	brokenEnd   bool // 返回 0 行但 Done=false（契约违约）
	stuckCursor bool // 游标不推进（契约违约）

	batchCalls int
}

func (s *fakeSource) Columns() []Column { return s.cols }

func (s *fakeSource) Count(ctx context.Context, f ExportFilter) (int64, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return int64(len(s.rows)), nil
}

func (s *fakeSource) Batch(ctx context.Context, f ExportFilter, c Cursor) ([]Row, Cursor, error) {
	s.batchCalls++
	if s.batchErrAt > 0 && s.batchCalls == s.batchErrAt {
		return nil, c, errors.New("fake source batch error")
	}
	batch := s.batch
	if batch <= 0 {
		batch = 3
	}
	start := 0
	if c.Value != "" {
		v, err := strconv.Atoi(c.Value)
		if err != nil {
			return nil, c, err
		}
		start = v
	}
	if start >= len(s.rows) {
		return nil, Cursor{Done: true}, nil
	}
	if s.brokenEnd && start > 0 && start+batch > len(s.rows) {
		return nil, Cursor{Value: c.Value}, nil // 0 行且未结束（违约）
	}
	end := start + batch
	if end > len(s.rows) {
		end = len(s.rows)
	}
	if s.stuckCursor {
		return s.rows[start:end], Cursor{Value: c.Value}, nil
	}
	next := Cursor{Value: strconv.Itoa(end)}
	if end >= len(s.rows) {
		next = Cursor{Done: true}
	}
	return s.rows[start:end], next, nil
}

func (s *fakeSource) Summary() []SummaryRow { return s.summary }

// ---- 测试装配 ----

type env struct {
	svc    *Service
	repo   *fakeRepo
	queue  *fakeQueue
	spy    *auditSpy
	store  *storage.Store
	writer *fakeWriter
	source *fakeSource
}

// newEnv 构建测试环境（临时存储根 + 假驱动 gorm + 内存仓库/队列）。
func newEnv(t *testing.T) *env {
	t.Helper()
	hooks := &txHooks{}
	spy := &auditSpy{}
	db, err := openTestGorm(hooks, spy)
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	repo := newFakeRepo(db, hooks)
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("创建临时存储失败: %v", err)
	}
	e := &env{
		repo:  repo,
		queue: &fakeQueue{},
		spy:   spy,
		store: store,
	}
	e.writer = &fakeWriter{spec: testSpec()}
	e.source = &fakeSource{cols: testExportColumns()}
	e.svc = NewService(repo, store, e.queue, Config{
		ImportMaxRows:     50,
		BatchSize:         3,
		ExportBatchSize:   3,
		FileRetentionDays: 30,
		UploadMaxBytes:    1 << 20,
	}, nil, WithImportWriter(ImportProduct, e.writer), WithExportSource(ModuleProduct, e.source))
	return e
}

// testSpec 结构层校验矩阵用模板规格（覆盖全部列类型 + 必填 + 文件内去重）。
func testSpec() TemplateSpec {
	return TemplateSpec{
		ImportType:  ImportProduct,
		Name:        "商品导入",
		FileName:    "商品导入模板.xlsx",
		Description: "测试模板",
		Sheet:       "商品导入",
		Columns: []Column{
			{Key: "code", Title: "商品编码", Type: CellText, Required: true, UniqueInFile: true},
			{Key: "name", Title: "商品名称", Type: CellText, Required: true},
			{Key: "weight", Title: "重量", Type: CellNumber},
			{Key: "price", Title: "价格", Type: CellMoney},
			{Key: "made_at", Title: "生产日期", Type: CellDate},
		},
		SampleRows: [][]string{{"P-1", "示例", "1", "2", "2026-01-01"}},
		Notes:      []string{"测试校验说明"},
	}
}

func testExportColumns() []Column {
	return []Column{
		{Key: "code", Title: "编码"},
		{Key: "qty", Title: "数量", Type: CellNumber},
		{Key: "amount", Title: "金额", Type: CellMoney},
		{Key: "made_at", Title: "日期", Type: CellDate},
	}
}

// ---- xlsx 构造与 multipart 封装（ask：excelize 用构造的内存工作簿）----

// buildXlsx 构造内存工作簿（首页名 = "Sheet1"，headers 首行，rows 数据行）。
func buildXlsx(t *testing.T, sheet string, headers []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if sheet != "" && sheet != f.GetSheetName(0) {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatalf("建页失败: %v", err)
		}
		_ = f.DeleteSheet(f.GetSheetName(0))
	}
	name := f.GetSheetName(0)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(name, cell, h); err != nil {
			t.Fatalf("写表头失败: %v", err)
		}
	}
	for r, row := range rows {
		for i, v := range row {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := f.SetCellValue(name, cell, v); err != nil {
				t.Fatalf("写数据行失败: %v", err)
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("生成工作簿失败: %v", err)
	}
	return buf.Bytes()
}

// fileHeader bytes → multipart.FileHeader（模拟浏览器 multipart 上传）。
func fileHeader(t *testing.T, name string, content []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写 multipart 失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	mr := multipart.NewReader(&buf, mw.Boundary())
	form, err := mr.ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("解析 multipart 失败: %v", err)
	}
	if len(form.File["file"]) == 0 {
		t.Fatalf("multipart 中无 file 部件")
	}
	return form.File["file"][0]
}

// openWorkbook 内存打开工作簿（导出产物断言用）。
func openWorkbook(t *testing.T, content []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("打开工作簿失败: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// testActor 测试操作者。
func testActor() Actor {
	return Actor{UserID: 42, Username: "tester", RequestID: "req-1", AllWarehouses: true}
}

// requireErrCode 断言业务错误码。
func requireErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，得到 nil", want)
	}
	var re *response.Error
	if !errors.As(err, &re) || re.Error() == "" {
		t.Fatalf("期望业务错误，得到 %v", err)
	}
	if got := codeOf(re); got != want {
		t.Fatalf("错误码不符：want=%s got=%s (%v)", want, got, re)
	}
}

func codeOf(e *response.Error) string {
	// response.Error 未导出 code 字段——以 Error() 文本前缀判定（"CODE: message"）。
	msg := e.Error()
	for i := 0; i < len(msg); i++ {
		if msg[i] == ':' {
			return msg[:i]
		}
	}
	return msg
}

var _ = middleware.Audit

// mustJSON 测试载荷序列化。
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonMarshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return b
}

// asynqxTaskForTest 构造队列任务（测试驱动执行器用）。
func asynqxTaskForTest(taskID string, payload []byte) asynqx.Task {
	return asynqx.Task{Type: asynqx.TaskTypeExportRun, TaskID: taskID, Payload: payload}
}
