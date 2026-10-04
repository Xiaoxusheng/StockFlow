package returns

// 测试替身（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
// 模式与 internal/masterdata、internal/inventory 既有替身同源（同一项目约定，非第二套机制）：
//   - fake gorm 驱动：事务可运行、审计写入经回调探针捕获（fakedb_test 同款），
//     并在 Begin/Commit/Rollback 挂钩子——回滚时恢复内存替身快照，使"任一步失败
//     整体回滚"（architecture.md §4）的断言在无库环境下仍然为真；
//   - fakeRepo：Repository 接口的内存实现（语义对齐 GORM 实现的守卫与状态机 UPDATE 行数判定）；
//   - fakeStock：StockGateway 的内存库存模拟器（六状态恒等式、幂等键重放、锁台账——
//     语义对齐 internal/inventory.Service）；
//   - fake readers：SalesOrderReader/PurchaseOrderReader/QCCreator/LedgerReader/StockStateReader
//     的内存实现（表驱动数据）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// ---- 事务生命周期钩子 ----

// txHooks 事务生命周期钩子（回滚恢复内存替身快照——真实 PG 中库存原语与单据写入
// 同事务回滚；本替身以快照/恢复等价复现该语义）。
type txHooks struct {
	mu         sync.Mutex
	onBegin    []func()
	onCommit   []func()
	onRollback []func()
	inTx       bool
}

func (h *txHooks) begin() {
	h.mu.Lock()
	h.inTx = true
	h.mu.Unlock()
	for _, f := range h.onBegin {
		f()
	}
}

func (h *txHooks) commit() {
	h.mu.Lock()
	h.inTx = false
	h.mu.Unlock()
}

func (h *txHooks) rollback() {
	h.mu.Lock()
	h.inTx = false
	h.mu.Unlock()
	for _, f := range h.onRollback {
		f()
	}
}

// ---- database/sql 假驱动（每个 testEnv 独立注册，钩子经 env 传递）----

var (
	drvMu  sync.Mutex
	drvSeq int
)

func registerFakeDriver(env *txHooks) string {
	drvMu.Lock()
	defer drvMu.Unlock()
	drvSeq++
	name := "sfake-returns-" + strconv.Itoa(drvSeq)
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

func (fakeDialector) Explain(sqlStr string, vars ...any) string { return "" }

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

func (s *auditSpy) actions() []string {
	var out []string
	for _, e := range s.all() {
		out = append(out, e.Action)
	}
	return out
}

// openTestGorm 打开假 gorm 句柄（事务/INSERT 可运行，数据不落任何真实存储）。
func openTestGorm(hooks *txHooks, spy *auditSpy) (*gorm.DB, error) {
	db, err := gorm.Open(fakeDialector{hooks: hooks}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if spy != nil {
		if err := db.Callback().Create().Before("gorm:create").Register("sf_returns_audit_spy", func(tx *gorm.DB) {
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
	mu         sync.Mutex
	db         *gorm.DB
	seq        int64
	orders     map[int64]*ReturnOrder
	items      map[int64][]*ReturnItem // returnID → items
	exceptions map[int64]*Exception
	approvals  []*DocumentApproval
	opLogs     map[string][]middleware.OperationLog // request_id → 操作日志（追溯关联测试）
	snap       *fakeRepoSnap
}

type fakeRepoSnap struct {
	seq        int64
	orders     map[int64]*ReturnOrder
	items      map[int64][]*ReturnItem
	exceptions map[int64]*Exception
	approvals  []*DocumentApproval
}

func newFakeRepo(db *gorm.DB, hooks *txHooks) *fakeRepo {
	f := &fakeRepo{
		db:         db,
		orders:     map[int64]*ReturnOrder{},
		items:      map[int64][]*ReturnItem{},
		exceptions: map[int64]*Exception{},
		opLogs:     map[string][]middleware.OperationLog{},
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
	o := make(map[int64]*ReturnOrder, len(f.orders))
	for k, v := range f.orders {
		cp := *v
		o[k] = &cp
	}
	it := make(map[int64][]*ReturnItem, len(f.items))
	for k, vs := range f.items {
		cp := make([]*ReturnItem, len(vs))
		for i, v := range vs {
			c := *v
			cp[i] = &c
		}
		it[k] = cp
	}
	e := make(map[int64]*Exception, len(f.exceptions))
	for k, v := range f.exceptions {
		cp := *v
		e[k] = &cp
	}
	ap := append([]*DocumentApproval(nil), f.approvals...)
	f.snap = &fakeRepoSnap{seq: f.seq, orders: o, items: it, exceptions: e, approvals: ap}
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
	f.seq, f.orders, f.items, f.exceptions, f.approvals =
		f.snap.seq, f.snap.orders, f.snap.items, f.snap.exceptions, f.snap.approvals
	f.snap = nil
}

func (f *fakeRepo) InsertReturnOrder(tx *gorm.DB, o *ReturnOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o.ID = database.ID(f.nextID())
	cp := *o
	f.orders[o.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) InsertReturnItems(tx *gorm.DB, items []*ReturnItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		it.ID = database.ID(f.nextID())
		cp := *it
		f.items[it.ReturnID] = append(f.items[it.ReturnID], &cp)
	}
	return nil
}

func (f *fakeRepo) ReplaceReturnItems(tx *gorm.DB, returnID int64, items []*ReturnItem) error {
	f.mu.Lock()
	delete(f.items, returnID)
	f.mu.Unlock()
	return f.InsertReturnItems(tx, items)
}

func (f *fakeRepo) FindReturnOrder(_ context.Context, id int64) (*ReturnOrder, error) {
	return f.findOrder(id)
}

func (f *fakeRepo) findOrder(id int64) (*ReturnOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.orders[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindReturnOrderForUpdate(tx *gorm.DB, id int64) (*ReturnOrder, error) {
	return f.findOrder(id)
}

func (f *fakeRepo) ListReturnOrders(_ context.Context, flt ReturnOrderFilter) ([]*ReturnOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ReturnOrder
	for _, o := range f.orders {
		if flt.Type != "" && o.Type != flt.Type {
			continue
		}
		if flt.Status != "" && o.Status != flt.Status {
			continue
		}
		if flt.SourceNo != "" && o.SourceNo != flt.SourceNo {
			continue
		}
		if flt.WarehouseID > 0 && o.WarehouseID != flt.WarehouseID {
			continue
		}
		if flt.WarehouseIDs != nil && !int64In(flt.WarehouseIDs, o.WarehouseID) {
			continue
		}
		cp := *o
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	total := int64(len(out))
	if flt.Page > 0 && flt.PageSize > 0 {
		lo := (flt.Page - 1) * flt.PageSize
		hi := lo + flt.PageSize
		if lo > len(out) {
			out = nil
		} else if hi > len(out) {
			out = out[lo:]
		} else {
			out = out[lo:hi]
		}
	}
	return out, total, nil
}

func int64In(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func (f *fakeRepo) FindReturnItemForUpdate(tx *gorm.DB, returnID, lineNo int64) (*ReturnItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.items[returnID] {
		if it.LineNo == lineNo {
			cp := *it
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListReturnItemsForUpdate(tx *gorm.DB, returnID int64) ([]*ReturnItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ReturnItem
	for _, it := range f.items[returnID] {
		cp := *it
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LineNo < out[j].LineNo })
	return out, nil
}

func (f *fakeRepo) UpdateReturnStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64, extra map[string]any) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return 0, nil
	}
	matched := false
	for _, s := range from {
		if o.Status == s {
			matched = true
			break
		}
	}
	if !matched {
		return 0, nil
	}
	o.Status = to
	o.UpdatedBy = by
	o.UpdatedAt = database.Now()
	switch tsCol {
	case "approved_at":
		o.ApprovedAt = database.Now()
	case "received_at":
		o.ReceivedAt = database.Now()
	case "qc_at":
		o.QcAt = database.Now()
	case "completed_at":
		o.CompletedAt = database.Now()
	case "cancelled_at":
		o.CancelledAt = database.Now()
	}
	for k, v := range extra {
		switch k {
		case "approved_by":
			o.ApprovedBy = v.(int64)
		}
	}
	return 1, nil
}

func (f *fakeRepo) AddItemReceived(tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders {
		for _, it := range f.items[o.ID.Int64()] {
			if it.ID.Int64() != itemID {
				continue
			}
			if it.QtyReceived.Add(qty).Sub(it.QtyReturn).IsPositive() {
				return 0, nil // 守卫未生效（超量）
			}
			it.QtyReceived = it.QtyReceived.Add(qty)
			it.UpdatedBy = by
			it.UpdatedAt = database.Now()
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) AddItemInspected(tx *gorm.DB, itemID int64, qualified, defective stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders {
		for _, it := range f.items[o.ID.Int64()] {
			if it.ID.Int64() != itemID {
				continue
			}
			if it.QtyInspected.Add(qualified).Add(defective).Sub(it.QtyReceived).IsPositive() {
				return 0, nil
			}
			it.QtyInspected = it.QtyInspected.Add(qualified).Add(defective)
			it.QtyDefective = it.QtyDefective.Add(defective)
			it.UpdatedBy = by
			it.UpdatedAt = database.Now()
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) SumReturnedBySource(q *gorm.DB, sourceNo, orderType string, excludeReturnID int64) ([]SourceReturnedLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	agg := map[lineKey]stock.Qty{}
	for _, o := range f.orders {
		if o.SourceNo != sourceNo || o.Type != orderType || o.Status == ReturnStatusCancelled {
			continue
		}
		if excludeReturnID > 0 && o.ID.Int64() == excludeReturnID {
			continue
		}
		for _, it := range f.items[o.ID.Int64()] {
			k := lineKey{LineNo: it.LineNo, SKUID: it.SKUID}
			agg[k] = agg[k].Add(it.QtyReturn)
		}
	}
	var out []SourceReturnedLine
	for k, v := range agg {
		out = append(out, SourceReturnedLine{LineNo: k.LineNo, SKUID: k.SKUID, Qty: v})
	}
	return out, nil
}

func (f *fakeRepo) InsertApproval(tx *gorm.DB, a *DocumentApproval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals = append(f.approvals, a)
	return nil
}

// LockSourceSerial 内存替身：无真实咨询锁（单测单线程，语义为空操作）。
func (f *fakeRepo) LockSourceSerial(tx *gorm.DB, sourceNo string) error {
	return nil
}

func (f *fakeRepo) InsertException(tx *gorm.DB, e *Exception) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID = database.ID(f.nextID())
	cp := *e
	f.exceptions[e.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) FindException(_ context.Context, id int64) (*Exception, error) {
	return f.findException(id)
}

func (f *fakeRepo) findException(id int64) (*Exception, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.exceptions[id]; ok {
		cp := *e
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindExceptionForUpdate(tx *gorm.DB, id int64) (*Exception, error) {
	return f.findException(id)
}

func (f *fakeRepo) ListExceptions(_ context.Context, flt ExceptionFilter) ([]*Exception, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Exception
	for _, e := range f.exceptions {
		if flt.Type != "" && e.Type != flt.Type {
			continue
		}
		if flt.Status != "" && e.Status != flt.Status {
			continue
		}
		if flt.SourceNo != "" && e.SourceNo != flt.SourceNo {
			continue
		}
		if flt.SourceType != "" && e.SourceType != flt.SourceType {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, int64(len(out)), nil
}

func (f *fakeRepo) UpdateExceptionStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.exceptions[id]
	if !ok {
		return 0, nil
	}
	matched := false
	for _, s := range from {
		if e.Status == s {
			matched = true
			break
		}
	}
	if !matched {
		return 0, nil
	}
	e.Status = to
	e.UpdatedBy = by
	e.UpdatedAt = database.Now()
	switch tsCol {
	case "assigned_at":
		e.AssignedAt = database.Now()
	case "resolved_at":
		e.ResolvedAt = database.Now()
	case "closed_at":
		e.ClosedAt = database.Now()
	}
	return 1, nil
}

func (f *fakeRepo) UpdateExceptionAssignee(tx *gorm.DB, id, assigneeID int64, assigneeName string, by int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.exceptions[id]; ok {
		e.AssigneeID = assigneeID
		e.AssigneeName = assigneeName
		e.UpdatedBy = by
	}
	return nil
}

func (f *fakeRepo) SetExceptionFreezeLock(tx *gorm.DB, id int64, lockID *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.exceptions[id]; ok {
		e.FreezeLockID = lockID
	}
	return nil
}

func (f *fakeRepo) AppendHandleRecord(tx *gorm.DB, id int64, rec HandleRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.exceptions[id]
	if !ok {
		return errors.New("fakeRepo: exception not found")
	}
	records := e.HandleRecordList()
	records = append(records, rec)
	e.HandleRecords = marshalJSONB(records)
	return nil
}

func (f *fakeRepo) UpdateExceptionImageRefs(tx *gorm.DB, id int64, refs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.exceptions[id]
	if !ok {
		return errors.New("fakeRepo: exception not found")
	}
	e.ImageRefs = marshalJSONB(refs)
	return nil
}

func (f *fakeRepo) ListOperationLogs(_ context.Context, requestIDs []string, limit int) ([]middleware.OperationLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []middleware.OperationLog
	seen := map[string]bool{}
	for _, rid := range requestIDs {
		for _, l := range f.opLogs[rid] {
			key := rid + "#" + l.Action + "#" + strconv.FormatInt(l.ID.Int64(), 10)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, l)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// seedOperationLog 追溯测试夹具：注入 request_id 关联的操作日志。
func (f *fakeRepo) seedOperationLog(l middleware.OperationLog) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opLogs[l.RequestID] = append(f.opLogs[l.RequestID], l)
}

// ---- fakeStock：库存原语内存模拟器（语义对齐 internal/inventory.Service）----

type fakeStockRow struct {
	key     stock.RowKey
	total   stock.Qty
	avail   stock.Qty
	locked  stock.Qty
	frozen  stock.Qty
	pending stock.Qty
	defect  stock.Qty
}

// rowKey 库存行定位维度（对齐 inventory.locateRowForUpdate：warehouse+bin+sku+batch，
// zone/shelf 为随 bin 冗余的行属性，不参与定位）。
func rowKey(k stock.RowKey) string {
	return fmt.Sprintf("%d:%d:%d:%d", k.WarehouseID, k.BinID, k.SKUID, k.BatchID)
}

type fakeLockRow struct {
	id                       int64
	key                      stock.RowKey
	lockType, srcType, srcNo string
	qty                      stock.Qty
	status                   string // ACTIVE/RELEASED/CONSUMED
}

type fakeSerialRow struct {
	serialNo       string
	skuID, batchID int64
	whID, binID    int64
	status         string
	lastSrcType    string
	lastSrcNo      string
}

type fakeLedgerRow struct {
	id           int64
	ledgerNo     string
	key          stock.RowKey
	changeType   string
	src          stock.Source
	statusFromTo [2]string
	qty          stock.Qty // 变化量（含方向）
	idemKey      string
}

type fakeStock struct {
	mu      sync.Mutex
	hooks   *txHooks
	seq     int64
	rows    map[string]*fakeStockRow
	locks   map[int64]*fakeLockRow
	serials map[string]*fakeSerialRow
	batches map[string]int64 // sku:batchNo → batchID
	idem    map[string]fakeIdemResult
	ledgers []fakeLedgerRow
	snap    *fakeStockSnap
}

type fakeIdemResult struct {
	replay bool
	res    stock.MutationResult
}

type fakeStockSnap struct {
	seq     int64
	rows    map[string]*fakeStockRow
	locks   map[int64]*fakeLockRow
	serials map[string]*fakeSerialRow
	idem    map[string]fakeIdemResult
	ledgers []fakeLedgerRow
}

func newFakeStock(hooks *txHooks) *fakeStock {
	f := &fakeStock{
		hooks:   hooks,
		rows:    map[string]*fakeStockRow{},
		locks:   map[int64]*fakeLockRow{},
		serials: map[string]*fakeSerialRow{},
		batches: map[string]int64{},
		idem:    map[string]fakeIdemResult{},
	}
	hooks.onBegin = append(hooks.onBegin, f.snapshot)
	hooks.onCommit = append(hooks.onCommit, f.dropSnap)
	hooks.onRollback = append(hooks.onRollback, f.restore)
	return f
}

func (f *fakeStock) snapshot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := make(map[string]*fakeStockRow, len(f.rows))
	for k, v := range f.rows {
		cp := *v
		rows[k] = &cp
	}
	locks := make(map[int64]*fakeLockRow, len(f.locks))
	for k, v := range f.locks {
		cp := *v
		locks[k] = &cp
	}
	serials := make(map[string]*fakeSerialRow, len(f.serials))
	for k, v := range f.serials {
		cp := *v
		serials[k] = &cp
	}
	idem := make(map[string]fakeIdemResult, len(f.idem))
	for k, v := range f.idem {
		idem[k] = v
	}
	ledgers := append([]fakeLedgerRow(nil), f.ledgers...)
	f.snap = &fakeStockSnap{seq: f.seq, rows: rows, locks: locks, serials: serials, idem: idem, ledgers: ledgers}
}

func (f *fakeStock) dropSnap() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = nil
}

func (f *fakeStock) restore() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snap == nil {
		return
	}
	f.seq, f.rows, f.locks, f.serials, f.idem, f.ledgers =
		f.snap.seq, f.snap.rows, f.snap.locks, f.snap.serials, f.snap.idem, f.snap.ledgers
	f.snap = nil
}

func (f *fakeStock) nextID() int64 { f.seq++; return f.seq }

// replay 幂等预检（对齐 inventory.mutate：键命中即重放，不再变更）。
func (f *fakeStock) replay(idemKey string) (stock.MutationResult, bool) {
	if idemKey == "" {
		return stock.MutationResult{}, false
	}
	if r, ok := f.idem[idemKey]; ok {
		res := r.res
		res.Replay = true
		return res, true
	}
	return stock.MutationResult{}, false
}

func (f *fakeStock) remember(idemKey string, res stock.MutationResult) {
	if idemKey == "" {
		return
	}
	f.idem[idemKey] = fakeIdemResult{res: res}
}

func (f *fakeStock) rowFor(k stock.RowKey) *fakeStockRow {
	return f.rows[rowKey(k)]
}

func (f *fakeStock) ensureRow(k stock.RowKey) *fakeStockRow {
	key := rowKey(k)
	if r, ok := f.rows[key]; ok {
		return r
	}
	r := &fakeStockRow{key: k}
	f.rows[key] = r
	return r
}

func (f *fakeStock) Putaway(_ context.Context, _ *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	if !op.Qty.IsPositive() {
		return stock.MutationResult{}, response.NewError(response.CodeInvalidParam, nil)
	}
	r := f.ensureRow(op.Key)
	if op.RequireInspect {
		r.pending = r.pending.Add(op.Qty)
	} else {
		r.avail = r.avail.Add(op.Qty)
	}
	r.total = r.total.Add(op.Qty)
	led := f.writeLedgerResult(op.Key, "INBOUND", op.Source, op.Qty, op.IdempotencyKey)
	res := stock.MutationResult{Ledger: led}
	f.remember(op.IdempotencyKey, res)
	return res, nil
}

func (f *fakeStock) writeLedgerResult(k stock.RowKey, changeType string, src stock.Source, qty stock.Qty, idemKey string) stock.LedgerRef {
	id := f.nextID()
	f.ledgers = append(f.ledgers, fakeLedgerRow{
		id: id, ledgerNo: "LED-TEST-" + strconv.FormatInt(id, 10),
		key: k, changeType: changeType, src: src, qty: qty, idemKey: idemKey,
	})
	return stock.LedgerRef{ID: id, LedgerNo: "LED-TEST-" + strconv.FormatInt(id, 10)}
}

func (f *fakeStock) Lock(_ context.Context, _ *gorm.DB, op stock.LockOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	// 同来源同类型 ACTIVE 锁存在 → 幂等返回（service.go Lock 既有机制）。
	for _, l := range f.locks {
		if l.status == "ACTIVE" && l.lockType == op.LockType && l.srcType == op.Source.Type && l.srcNo == op.Source.No &&
			l.key.BinID == op.Key.BinID && l.key.SKUID == op.Key.SKUID && l.key.BatchID == op.Key.BatchID {
			res := stock.MutationResult{Replay: true, LockID: l.id}
			f.remember(op.IdempotencyKey, res)
			return res, nil
		}
	}
	r := f.rowFor(op.Key)
	if r == nil {
		return stock.MutationResult{}, errors.New("fakeStock: 库存行不存在")
	}
	if r.avail.Sub(op.Qty).IsNegative() {
		return stock.MutationResult{}, errors.New("fakeStock: 可用库存不足")
	}
	r.avail = r.avail.Sub(op.Qty)
	switch op.LockType {
	case "ORDER_HOLD":
		r.locked = r.locked.Add(op.Qty)
	default:
		r.frozen = r.frozen.Add(op.Qty)
	}
	id := f.nextID()
	f.locks[id] = &fakeLockRow{id: id, key: op.Key, lockType: op.LockType, srcType: op.Source.Type, srcNo: op.Source.No, qty: op.Qty, status: "ACTIVE"}
	led := f.writeLedgerResult(op.Key, "LOCK", op.Source, op.Qty.Neg(), op.IdempotencyKey)
	res := stock.MutationResult{Ledger: led, LockID: id}
	f.remember(op.IdempotencyKey, res)
	return res, nil
}

func (f *fakeStock) ReleaseLock(_ context.Context, _ *gorm.DB, op stock.ReleaseLockOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	l, ok := f.locks[op.LockID]
	if !ok || l.status != "ACTIVE" || l.qty.Sub(op.Qty).IsNegative() {
		return stock.MutationResult{}, errors.New("fakeStock: 锁不存在或已完结")
	}
	r := f.rowFor(l.key)
	if r == nil {
		return stock.MutationResult{}, errors.New("fakeStock: 库存行不存在")
	}
	switch l.lockType {
	case "ORDER_HOLD":
		r.locked = r.locked.Sub(op.Qty)
	default:
		r.frozen = r.frozen.Sub(op.Qty)
	}
	r.avail = r.avail.Add(op.Qty)
	remaining := l.qty.Sub(op.Qty)
	if remaining.IsZero() {
		l.status = "RELEASED"
	} else {
		l.qty = remaining
	}
	led := f.writeLedgerResult(l.key, "RELEASE", op.Source, op.Qty, op.IdempotencyKey)
	res := stock.MutationResult{Ledger: led, LockID: op.LockID}
	f.remember(op.IdempotencyKey, res)
	return res, nil
}

func (f *fakeStock) Deduct(_ context.Context, _ *gorm.DB, op stock.DeductOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	r := f.rowFor(op.Key)
	if r == nil {
		return stock.MutationResult{}, errors.New("fakeStock: 库存行不存在")
	}
	if r.locked.Sub(op.Qty).IsNegative() {
		return stock.MutationResult{}, errors.New("fakeStock: 锁定库存不足")
	}
	r.locked = r.locked.Sub(op.Qty)
	r.total = r.total.Sub(op.Qty)
	if op.LockID > 0 {
		l, ok := f.locks[op.LockID]
		if !ok || l.status != "ACTIVE" {
			return stock.MutationResult{}, errors.New("fakeStock: 锁不存在或已核销")
		}
		remaining := l.qty.Sub(op.Qty)
		if remaining.IsZero() {
			l.status = "CONSUMED"
		} else {
			l.qty = remaining
		}
	}
	led := f.writeLedgerResult(op.Key, "OUTBOUND", op.Source, op.Qty.Neg(), op.IdempotencyKey)
	res := stock.MutationResult{Ledger: led}
	f.remember(op.IdempotencyKey, res)
	return res, nil
}

func (f *fakeStock) InspectResult(_ context.Context, _ *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	r := f.rowFor(op.Key)
	if r == nil {
		return stock.MutationResult{}, errors.New("fakeStock: 库存行不存在")
	}
	if r.pending.Sub(op.Qty).IsNegative() {
		return stock.MutationResult{}, errors.New("fakeStock: 待检库存不足")
	}
	r.pending = r.pending.Sub(op.Qty)
	if op.Pass {
		r.avail = r.avail.Add(op.Qty)
	} else {
		r.defect = r.defect.Add(op.Qty)
	}
	changeType := "INSPECT_PASS"
	if !op.Pass {
		changeType = "INSPECT_DEFECTIVE"
	}
	led := f.writeLedgerResult(op.Key, changeType, op.Source, op.Qty.Neg(), op.IdempotencyKey)
	res := stock.MutationResult{Ledger: led}
	f.remember(op.IdempotencyKey, res)
	return res, nil
}

func (f *fakeStock) EnsureBatch(_ context.Context, _ *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strconv.FormatInt(op.SKUID, 10) + ":" + op.BatchNo
	if id, ok := f.batches[key]; ok {
		return id, false, nil
	}
	id := f.nextID()
	f.batches[key] = id
	return id, true, nil
}

func (f *fakeStock) SerialEvent(_ context.Context, _ *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.serials[op.SerialNo]; ok {
		if row.skuID != op.SKUID {
			return 0, false, errors.New("fakeStock: 序列号 SKU 不一致")
		}
		row.batchID, row.whID, row.binID, row.status = op.BatchID, op.WarehouseID, op.BinID, op.Status
		row.lastSrcType, row.lastSrcNo = op.Source.Type, op.Source.No
		return 0, false, nil
	}
	id := f.nextID()
	f.serials[op.SerialNo] = &fakeSerialRow{
		serialNo: op.SerialNo, skuID: op.SKUID, batchID: op.BatchID,
		whID: op.WarehouseID, binID: op.BinID, status: op.Status,
		lastSrcType: op.Source.Type, lastSrcNo: op.Source.No,
	}
	return id, true, nil
}

// SerialStates 实现 returns.StockGateway（M2 修复轮）：按集合读序列号台账当前状态，
// skuID>0 时过滤不属于该 SKU 的序列号（与真实实现一致——缺失即调用方 fail-closed）。
func (f *fakeStock) SerialStates(_ context.Context, _ *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]stock.SerialState, 0, len(serials))
	for _, sn := range serials {
		row, ok := f.serials[sn]
		if !ok || (skuID > 0 && row.skuID != skuID) {
			continue
		}
		out = append(out, stock.SerialState{
			SerialNo: sn, SKUID: row.skuID, BatchID: row.batchID,
			WarehouseID: row.whID, BinID: row.binID, Status: row.status,
		})
	}
	return out, nil
}

// ---- 断言助手 ----

// stateOf 读取某五维键的六状态（含恒等式校验——inventory-rules §2）。
func (f *fakeStock) stateOf(t *testing.T, wh, zone, shelf, bin, sku, batch int64) fakeStockRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[rowKey(stock.RowKey{WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku, BatchID: batch})]
	if r == nil {
		t.Fatalf("库存行不存在: %s", rowKey(stock.RowKey{WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku, BatchID: batch}))
	}
	if r.total.Sub(r.avail.Add(r.locked).Add(r.frozen).Add(r.pending).Add(r.defect)) != 0 {
		t.Fatalf("库存恒等式破坏: total=%s avail=%s locked=%s frozen=%s pending=%s defect=%s",
			r.total, r.avail, r.locked, r.frozen, r.pending, r.defect)
	}
	return *r
}

// serialOf 读取序列号台账（断言助手）。
func (f *fakeStock) serialOf(serialNo string) (fakeSerialRow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.serials[serialNo]
	if !ok {
		return fakeSerialRow{}, false
	}
	return *r, true
}

// totalOf 全部行 total 合计（数量守恒断言）。
func (f *fakeStock) totalOf() stock.Qty {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum stock.Qty
	for _, r := range f.rows {
		sum = sum.Add(r.total)
	}
	return sum
}

// ---- fake readers ----

type fakeSalesOrders struct {
	mu   sync.Mutex
	data map[string]struct {
		wh    int64
		lines []ReturnableLine
	}
}

func (f *fakeSalesOrders) seed(no string, wh int64, lines ...ReturnableLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]struct {
			wh    int64
			lines []ReturnableLine
		}{}
	}
	f.data[no] = struct {
		wh    int64
		lines []ReturnableLine
	}{wh: wh, lines: lines}
}

func (f *fakeSalesOrders) FindReturnable(_ context.Context, soNo string) (int64, int64, []ReturnableLine, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[soNo]
	if !ok {
		return 0, 0, nil, false, nil
	}
	return 100, d.wh, append([]ReturnableLine(nil), d.lines...), true, nil
}

type fakePurchaseOrders struct {
	mu   sync.Mutex
	data map[string]struct {
		wh    int64
		lines []ReturnableLine
	}
}

func (f *fakePurchaseOrders) seed(no string, wh int64, lines ...ReturnableLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]struct {
			wh    int64
			lines []ReturnableLine
		}{}
	}
	f.data[no] = struct {
		wh    int64
		lines []ReturnableLine
	}{wh: wh, lines: lines}
}

func (f *fakePurchaseOrders) FindReturnable(_ context.Context, poNo string) (int64, int64, []ReturnableLine, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[poNo]
	if !ok {
		return 0, 0, nil, false, nil
	}
	return 200, d.wh, append([]ReturnableLine(nil), d.lines...), true, nil
}

type fakeQC struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeQC) CreateQC(_ context.Context, sourceType, sourceNo, qcType string, warehouseID int64, lines []QCLine) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return "QC-TEST-" + strconv.Itoa(f.calls), nil
}

type fakeLedgerReader struct {
	mu   sync.Mutex
	rows []TraceLedger
}

func (f *fakeLedgerReader) seed(rows ...TraceLedger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows...)
}

func (f *fakeLedgerReader) LedgersForTrace(_ context.Context, skuID int64, serialNo string, warehouseID int64, limit int) ([]TraceLedger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []TraceLedger
	for _, l := range f.rows {
		if skuID > 0 && l.SKUID != skuID {
			continue
		}
		if serialNo != "" && l.SerialNo != serialNo {
			continue
		}
		if warehouseID > 0 && l.WarehouseID != warehouseID {
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeStockState struct {
	mu      sync.Mutex
	rows    []TraceStockRow
	serials map[string]TraceSerialRow
}

func (f *fakeStockState) seedRows(rows ...TraceStockRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows...)
}

func (f *fakeStockState) seedSerial(serialNo string, row TraceSerialRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serials == nil {
		f.serials = map[string]TraceSerialRow{}
	}
	f.serials[serialNo] = row
}

func (f *fakeStockState) StockRowsBySKU(_ context.Context, skuID, warehouseID int64, limit int) ([]TraceStockRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []TraceStockRow
	for _, r := range f.rows {
		if skuID > 0 && r.SKUID != skuID {
			continue
		}
		if warehouseID > 0 && r.WarehouseID != warehouseID {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStockState) SerialByNo(_ context.Context, serialNo string) (TraceSerialRow, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.serials[serialNo]
	return r, ok, nil
}

// ---- testEnv 组装 ----

// fakeSKUFlags SKUFlagReader 内存替身：按 SKU 精确返回开关（缺省全零 = 非序列号
// SKU），序列号场景测试用 set 注入 SerialManaged=true。
type fakeSKUFlags struct {
	mu    sync.Mutex
	flags map[int64]SKUFlags
}

func (f *fakeSKUFlags) GetFlags(_ context.Context, skuID int64) (SKUFlags, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flags[skuID], nil
}

func (f *fakeSKUFlags) set(skuID int64, fl SKUFlags) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flags == nil {
		f.flags = map[int64]SKUFlags{}
	}
	f.flags[skuID] = fl
}

type testEnv struct {
	hooks    *txHooks
	spy      *auditSpy
	repo     *fakeRepo
	stock    *fakeStock
	sales    *fakeSalesOrders
	purchase *fakePurchaseOrders
	qc       *fakeQC
	ledgers  *fakeLedgerReader
	state    *fakeStockState
	flags    *fakeSKUFlags
	svc      *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	hooks := &txHooks{}
	spy := &auditSpy{}
	db, err := openTestGorm(hooks, spy)
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	env := &testEnv{
		hooks:    hooks,
		spy:      spy,
		repo:     newFakeRepo(db, hooks),
		stock:    newFakeStock(hooks),
		sales:    &fakeSalesOrders{},
		purchase: &fakePurchaseOrders{},
		qc:       &fakeQC{},
		ledgers:  &fakeLedgerReader{},
		state:    &fakeStockState{},
		flags:    &fakeSKUFlags{},
	}
	env.svc = NewService(env.repo,
		WithStock(env.stock),
		WithSKUFlags(env.flags),
		WithSalesOrders(env.sales),
		WithPurchaseOrders(env.purchase),
		WithQCCreator(env.qc),
		WithLedgers(env.ledgers),
		WithStockState(env.state),
	)
	return env
}
