package stockops

// 内存替身（单测不依赖 PostgreSQL/Redis，参照 internal/masterdata、internal/inventory
// 既有替身模式）：
//   - fakeWorld    域内五表 + 库存族六表的内存镜像（fakeGateway 忠实复刻 inventory
//     Service 原语的守卫/幂等/流水语义，供 Service 层恒等式与重放断言）；
//   - memStore     Store 的内存实现：WithinTx 以整世界快照/恢复实现真回滚语义；
//   - fakeBins/skus/flags/issuer 跨域端口替身。
//
// fakeGateway 与 memStore 共享同一 fakeWorld：WithinTx 串行化整个事务（单 goroutine
// 测试语义），回滚同时恢复单据表与库存表——"任一行失败整体回滚"可在替身上如实断言。

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// ---- 世界 ----

type fakeInvRow struct {
	id                                                      int64
	wh, zone, shelf, bin, sku, batch                        int64
	total, avail, locked, frozen, pendingInspect, defective inventory.Qty
}

func (r fakeInvRow) state() inventory.StockState {
	return inventory.StockState{
		Total: r.total, Available: r.avail, Locked: r.locked,
		Frozen: r.frozen, PendingInspect: r.pendingInspect, Defective: r.defective,
	}
}

type fakeLock struct {
	id                          int64
	wh, bin, sku, batch         int64
	lockType, sourceType, srcNo string
	qty                         inventory.Qty
	status                      string // ACTIVE/RELEASED/CONSUMED
}

type fakeLedger struct {
	id                              int64
	changeType, businessType, bizNo string
	idemKey                         string
	wh, sku                         int64
}

type fakeSerial struct {
	serialNo                  string
	sku, batch, wh, bin       int64
	status                    string
	lastSourceType, lastSrcNo string
}

type fakeAdjustment struct {
	id                  int64
	adjustmentNo        string
	wh, bin, sku, batch int64
	adjustType          string
	qty                 inventory.Qty
}

type fakeWorld struct {
	rows         map[int64]*fakeInvRow
	nextRowID    int64
	locks        map[int64]*fakeLock
	nextLockID   int64
	ledgers      []fakeLedger
	nextLedgerID int64
	serials      map[string]*fakeSerial
	adjustments  []fakeAdjustment
	nextAdjID    int64

	transfers      map[int64]*TransferOrder
	transferItems  map[int64][]TransferItem
	nextTransferID int64
	counts         map[int64]*CountOrder
	countItems     map[int64][]CountItem
	countDiffs     map[int64][]CountDifference
	nextCountID    int64
	approvals      []ApprovalRecord
	audits         []middleware.AuditEntry
	counterSeq     map[string]int64
}

func newFakeWorld() *fakeWorld {
	return &fakeWorld{
		rows: map[int64]*fakeInvRow{}, locks: map[int64]*fakeLock{},
		serials:   map[string]*fakeSerial{},
		transfers: map[int64]*TransferOrder{}, transferItems: map[int64][]TransferItem{},
		counts: map[int64]*CountOrder{}, countItems: map[int64][]CountItem{},
		countDiffs: map[int64][]CountDifference{},
		counterSeq: map[string]int64{},
		nextRowID:  1, nextLockID: 1, nextLedgerID: 1, nextAdjID: 1,
		nextTransferID: 1, nextCountID: 1,
	}
}

// seedRow 播种一条库存行（恒等式由调用方给定 init 保证）。
func (w *fakeWorld) seedRow(wh, zone, shelf, bin, sku, batch int64, total inventory.Qty) *fakeInvRow {
	r := &fakeInvRow{
		id: w.nextRowID, wh: wh, zone: zone, shelf: shelf, bin: bin, sku: sku, batch: batch,
		total: total, avail: total,
	}
	w.nextRowID++
	w.rows[r.id] = r
	return r
}

func (w *fakeWorld) seedSerial(serialNo string, sku, batch, wh, bin int64) {
	w.serials[serialNo] = &fakeSerial{
		serialNo: serialNo, sku: sku, batch: batch, wh: wh, bin: bin, status: "IN_STOCK",
	}
}

// clone 整世界深拷贝（快照）。
func (w *fakeWorld) clone() *fakeWorld {
	c := &fakeWorld{
		rows: map[int64]*fakeInvRow{}, locks: map[int64]*fakeLock{},
		serials: map[string]*fakeSerial{}, transfers: map[int64]*TransferOrder{},
		transferItems: map[int64][]TransferItem{}, counts: map[int64]*CountOrder{},
		countItems: map[int64][]CountItem{}, countDiffs: map[int64][]CountDifference{},
		counterSeq: map[string]int64{},
	}
	*c = *w // 标量字段整体拷贝，随后替换引用型字段
	c.rows = map[int64]*fakeInvRow{}
	for id, r := range w.rows {
		cp := *r
		c.rows[id] = &cp
	}
	c.locks = map[int64]*fakeLock{}
	for id, l := range w.locks {
		cp := *l
		c.locks[id] = &cp
	}
	c.serials = map[string]*fakeSerial{}
	for no, s := range w.serials {
		cp := *s
		c.serials[no] = &cp
	}
	c.ledgers = make([]fakeLedger, len(w.ledgers))
	copy(c.ledgers, w.ledgers)
	c.adjustments = append([]fakeAdjustment(nil), w.adjustments...)
	c.approvals = append([]ApprovalRecord(nil), w.approvals...)
	c.audits = append([]middleware.AuditEntry(nil), w.audits...)
	c.transfers = map[int64]*TransferOrder{}
	for id, o := range w.transfers {
		cp := *o
		c.transfers[id] = &cp
	}
	c.transferItems = map[int64][]TransferItem{}
	for id, items := range w.transferItems {
		c.transferItems[id] = append([]TransferItem(nil), items...)
	}
	c.counts = map[int64]*CountOrder{}
	for id, o := range w.counts {
		cp := *o
		c.counts[id] = &cp
	}
	c.countItems = map[int64][]CountItem{}
	for id, items := range w.countItems {
		c.countItems[id] = append([]CountItem(nil), items...)
	}
	c.countDiffs = map[int64][]CountDifference{}
	for id, diffs := range w.countDiffs {
		c.countDiffs[id] = append([]CountDifference(nil), diffs...)
	}
	c.counterSeq = map[string]int64{}
	for k, v := range w.counterSeq {
		c.counterSeq[k] = v
	}
	return c
}

func (w *fakeWorld) putLedger(changeType, bizType, bizNo, idem string, wh, sku int64) {
	w.ledgers = append(w.ledgers, fakeLedger{
		id: w.nextLedgerID, changeType: changeType, businessType: bizType, bizNo: bizNo,
		idemKey: idem, wh: wh, sku: sku,
	})
	w.nextLedgerID++
}

// ledgerByKey 幂等键查流水（值拷贝返回，避免切片扩容指针失效）。
func (w *fakeWorld) ledgerByKey(key string) (fakeLedger, bool) {
	if key == "" {
		return fakeLedger{}, false
	}
	for _, l := range w.ledgers {
		if l.idemKey == key {
			return l, true
		}
	}
	return fakeLedger{}, false
}

// findLock 精确行维度 + 来源查找 ACTIVE 锁（复刻 findLockBySource 语义）。
func (w *fakeWorld) findLock(wh, bin, sku, batch int64, lockType, sourceType, sourceNo string) *fakeLock {
	var found *fakeLock
	for _, l := range w.locks {
		if l.status == "ACTIVE" && l.wh == wh && l.bin == bin && l.sku == sku && l.batch == batch &&
			l.lockType == lockType && l.sourceType == sourceType && l.srcNo == sourceNo {
			found = l
		}
	}
	return found
}

func (w *fakeWorld) lockByID(id int64) *fakeLock { return w.locks[id] }

// rowByKey 五维定位。
func (w *fakeWorld) rowByKey(wh, bin, sku, batch int64) *fakeInvRow {
	for _, r := range w.rows {
		if r.wh == wh && r.bin == bin && r.sku == sku && r.batch == batch {
			return r
		}
	}
	return nil
}

func (w *fakeWorld) ensureRow(key inventory.RowKey) *fakeInvRow {
	if r := w.rowByKey(key.WarehouseID, key.BinID, key.SKUID, key.BatchID); r != nil {
		return r
	}
	r := &fakeInvRow{
		id: w.nextRowID, wh: key.WarehouseID, zone: key.ZoneID, shelf: key.ShelfID,
		bin: key.BinID, sku: key.SKUID, batch: key.BatchID,
	}
	w.nextRowID++
	w.rows[r.id] = r
	return r
}

// ---- fakeGateway：复刻 inventory.Service 原语语义 ----

type fakeGateway struct {
	w *fakeWorld
}

func (g *fakeGateway) replay(idem string) (inventory.MutationResult, bool) {
	if l, ok := g.w.ledgerByKey(idem); ok {
		return inventory.MutationResult{Replay: true, Ledger: inventory.LedgerRef{ID: l.id, LedgerNo: fmt.Sprintf("LED-%d", l.id)}}, true
	}
	return inventory.MutationResult{}, false
}

func (g *fakeGateway) Lock(_ context.Context, _ *gorm.DB, op inventory.LockOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	if l := g.w.findLock(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID, op.LockType, op.Source.Type, op.Source.No); l != nil {
		// 同来源同类型 ACTIVE 锁幂等返回（service.go Lock 既有机制）。
		return inventory.MutationResult{Replay: true, LockID: l.id}, nil
	}
	r := g.w.rowByKey(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID)
	if r == nil {
		return inventory.MutationResult{}, response.NewError(inventory.ErrRecordNotFound, nil)
	}
	if r.avail.Sub(op.Qty).IsNegative() {
		return inventory.MutationResult{}, response.NewError(inventory.ErrNotEnough, nil)
	}
	r.avail = r.avail.Sub(op.Qty)
	if op.LockType == "ORDER_HOLD" {
		r.locked = r.locked.Add(op.Qty)
	} else {
		r.frozen = r.frozen.Add(op.Qty)
	}
	lock := &fakeLock{
		id: g.w.nextLockID, wh: op.Key.WarehouseID, bin: op.Key.BinID, sku: op.Key.SKUID,
		batch: op.Key.BatchID, lockType: op.LockType, sourceType: op.Source.Type,
		srcNo: op.Source.No, qty: op.Qty, status: "ACTIVE",
	}
	g.w.nextLockID++
	g.w.locks[lock.id] = lock
	g.w.putLedger("LOCK", op.Source.Type, op.Source.No, op.IdempotencyKey, r.wh, r.sku)
	return inventory.MutationResult{LockID: lock.id}, nil
}

func (g *fakeGateway) ReleaseLock(_ context.Context, _ *gorm.DB, op inventory.ReleaseLockOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	l := g.w.lockByID(op.LockID)
	if l == nil || l.status != "ACTIVE" || l.qty.Sub(op.Qty).IsNegative() {
		return inventory.MutationResult{}, response.NewError(inventory.ErrLockNotFound, nil)
	}
	r := g.w.rowByKey(l.wh, l.bin, l.sku, l.batch)
	if r == nil {
		return inventory.MutationResult{}, response.NewError(inventory.ErrRecordNotFound, nil)
	}
	if l.lockType == "ORDER_HOLD" {
		r.locked = r.locked.Sub(op.Qty)
	} else {
		r.frozen = r.frozen.Sub(op.Qty)
	}
	r.avail = r.avail.Add(op.Qty)
	l.qty = l.qty.Sub(op.Qty)
	if l.qty.IsZero() {
		l.status, l.qty = "RELEASED", qtyUnit
	}
	g.w.putLedger("RELEASE", op.Source.Type, op.Source.No, op.IdempotencyKey, r.wh, r.sku)
	return inventory.MutationResult{LockID: op.LockID}, nil
}

func (g *fakeGateway) TransferOut(_ context.Context, _ *gorm.DB, op inventory.TransferOutOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	r := g.w.rowByKey(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID)
	if r == nil {
		return inventory.MutationResult{}, response.NewError(inventory.ErrRecordNotFound, nil)
	}
	if r.locked.Sub(op.Qty).IsNegative() {
		return inventory.MutationResult{}, response.NewError(inventory.ErrNotEnough, nil)
	}
	r.locked = r.locked.Sub(op.Qty)
	r.total = r.total.Sub(op.Qty)
	if op.LockID > 0 {
		l := g.w.lockByID(op.LockID)
		if l == nil || l.status != "ACTIVE" || l.qty.Sub(op.Qty).IsNegative() {
			return inventory.MutationResult{}, response.NewError(inventory.ErrLockNotFound, nil)
		}
		l.qty = l.qty.Sub(op.Qty)
		if l.qty.IsZero() {
			l.status, l.qty = "CONSUMED", qtyUnit
		}
	}
	g.w.putLedger("TRANSFER_OUT", op.Source.Type, op.Source.No, op.IdempotencyKey, r.wh, r.sku)
	return inventory.MutationResult{}, nil
}

func (g *fakeGateway) TransferIn(_ context.Context, _ *gorm.DB, op inventory.TransferInOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	r := g.w.ensureRow(op.Key)
	r.total = r.total.Add(op.Qty)
	r.avail = r.avail.Add(op.Qty)
	g.w.putLedger("TRANSFER_IN", op.Source.Type, op.Source.No, op.IdempotencyKey, r.wh, r.sku)
	return inventory.MutationResult{}, nil
}

func (g *fakeGateway) Adjust(_ context.Context, _ *gorm.DB, op inventory.AdjustOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	var r *fakeInvRow
	if adjustDirectionOf(op.AdjustType) > 0 {
		r = g.w.ensureRow(op.Key)
		r.total = r.total.Add(op.Qty)
		r.avail = r.avail.Add(op.Qty)
	} else {
		r = g.w.rowByKey(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID)
		if r == nil {
			return inventory.MutationResult{}, response.NewError(inventory.ErrRecordNotFound, nil)
		}
		if r.avail.Sub(op.Qty).IsNegative() {
			return inventory.MutationResult{}, response.NewError(inventory.ErrNotEnough, nil)
		}
		r.total = r.total.Sub(op.Qty)
		r.avail = r.avail.Sub(op.Qty)
	}
	g.w.putLedger("ADJUST", op.Source.Type, op.Source.No, op.IdempotencyKey, r.wh, r.sku)
	g.w.nextAdjID++
	g.w.adjustments = append(g.w.adjustments, fakeAdjustment{
		id: g.w.nextAdjID, adjustmentNo: fmt.Sprintf("ADJ-%06d", g.w.nextAdjID),
		wh: op.Key.WarehouseID, bin: op.Key.BinID, sku: op.Key.SKUID, batch: op.Key.BatchID,
		adjustType: op.AdjustType, qty: op.Qty,
	})
	return inventory.MutationResult{}, nil
}

// MoveBin 复刻 inventory.MoveBin（可用库存、源/目标行 total 同步增减、MOVE 流水）。
func (g *fakeGateway) MoveBin(_ context.Context, _ *gorm.DB, op inventory.MoveBinOp) (inventory.MutationResult, error) {
	if res, ok := g.replay(op.IdempotencyKey); ok {
		return res, nil
	}
	if op.From.WarehouseID != op.To.WarehouseID || op.From.SKUID != op.To.SKUID || op.From.BatchID != op.To.BatchID {
		return inventory.MutationResult{}, response.NewError(inventory.ErrMoveCrossWarehouse, nil)
	}
	// 同库位拒绝（对齐 inventory/service.go MoveBin 的 ErrMoveSameLocation 守卫——
	// 源/目标同位属于 no-op，原语层一律拒绝，替身必须同口径）。
	if op.From.BinID == op.To.BinID {
		return inventory.MutationResult{}, response.NewError(inventory.ErrMoveSameLocation, nil)
	}
	from := g.w.rowByKey(op.From.WarehouseID, op.From.BinID, op.From.SKUID, op.From.BatchID)
	if from == nil {
		return inventory.MutationResult{}, response.NewError(inventory.ErrRecordNotFound, nil)
	}
	if from.avail.Sub(op.Qty).IsNegative() {
		return inventory.MutationResult{}, response.NewError(inventory.ErrNotEnough, nil)
	}
	to := g.w.ensureRow(op.To)
	from.total = from.total.Sub(op.Qty)
	from.avail = from.avail.Sub(op.Qty)
	to.total = to.total.Add(op.Qty)
	to.avail = to.avail.Add(op.Qty)
	g.w.putLedger("MOVE", op.Source.Type, op.Source.No, op.IdempotencyKey, from.wh, from.sku)
	return inventory.MutationResult{}, nil
}

func (g *fakeGateway) SerialEvent(_ context.Context, _ *gorm.DB, op inventory.SerialOp) (int64, bool, error) {
	sr := g.w.serials[op.SerialNo]
	if sr == nil {
		sr = &fakeSerial{serialNo: op.SerialNo}
		g.w.serials[op.SerialNo] = sr
	} else if sr.sku != op.SKUID {
		return 0, false, response.NewError(inventory.ErrSerialSKUMismatch, nil)
	}
	sr.sku, sr.batch, sr.wh, sr.bin = op.SKUID, op.BatchID, op.WarehouseID, op.BinID
	sr.status = op.Status
	sr.lastSourceType, sr.lastSrcNo = op.Source.Type, op.Source.No
	return 1, sr != nil, nil
}

// adjustDirectionOf 复刻 inventory.adjustDirection（盘盈为增，其余为减）。
func adjustDirectionOf(adjustType string) int {
	if adjustType == "盘盈" {
		return 1
	}
	return -1
}

// ---- memStore：Store 内存实现（快照/恢复 = 真回滚语义）----

type memStore struct {
	w *fakeWorld
}

func (m *memStore) WithinTx(_ context.Context, fn func(Tx) error) error {
	snap := m.w.clone()
	err := fn(&memTx{w: m.w})
	if err != nil {
		*m.w = *snap // 整世界恢复（clone 已深拷贝引用型字段）
	}
	return err
}

type memTx struct {
	w *fakeWorld
}

func (t *memTx) GormDB() *gorm.DB { return nil }

func (t *memTx) Audit(e middleware.AuditEntry) error {
	t.w.audits = append(t.w.audits, e)
	return nil
}

// ---- memTx：调拨单 ----

func (t *memTx) InsertTransfer(_ context.Context, o *TransferOrder, items []TransferItem) error {
	o.ID = database.ID(t.w.nextTransferID)
	t.w.nextTransferID++
	cp := *o
	t.w.transfers[o.ID.Int64()] = &cp
	for i := range items {
		items[i].ID = database.ID(int64(i+1) * 1000)
		items[i].TransferID = o.ID.Int64()
	}
	t.w.transferItems[o.ID.Int64()] = append([]TransferItem(nil), items...)
	return nil
}

func (t *memTx) GetTransfer(_ context.Context, id int64) (*TransferOrder, error) {
	if o, ok := t.w.transfers[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (t *memTx) ListTransfers(_ context.Context, f TransferFilter, page, pageSize int) ([]TransferOrder, int64, error) {
	var all []TransferOrder
	for _, o := range t.w.transfers {
		if transferFilterMatch(f, *o) {
			all = append(all, *o)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	return paginate(all, page, pageSize)
}

func transferFilterMatch(f TransferFilter, o TransferOrder) bool {
	if !f.AllWarehouses {
		visible := false
		for _, id := range f.WarehouseIDs {
			if id == o.FromWarehouseID || id == o.ToWarehouseID {
				visible = true
			}
		}
		if !visible {
			return false
		}
	}
	if f.WarehouseID > 0 && f.WarehouseID != o.FromWarehouseID && f.WarehouseID != o.ToWarehouseID {
		return false
	}
	// 精确目标仓过滤（对齐 repo_gorm.go transferFilterWhere 的 ToWarehouseID 分支）。
	if f.ToWarehouseID > 0 && f.ToWarehouseID != o.ToWarehouseID {
		return false
	}
	if f.Status != "" && f.Status != o.Status {
		return false
	}
	if f.Type != "" && f.Type != o.Type {
		return false
	}
	if f.TransferNo != "" && f.TransferNo != o.TransferNo {
		return false
	}
	return true
}

func (t *memTx) ReplaceTransferItems(_ context.Context, transferID int64, items []TransferItem) error {
	for i := range items {
		items[i].TransferID = transferID
	}
	t.w.transferItems[transferID] = append([]TransferItem(nil), items...)
	return nil
}

func (t *memTx) ListTransferItems(_ context.Context, transferID int64) ([]TransferItem, error) {
	items := append([]TransferItem(nil), t.w.transferItems[transferID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].LineNo < items[j].LineNo })
	return items, nil
}

func (t *memTx) UpdateTransferStatus(_ context.Context, id int64, from, to string, stamps TransferStamps, actorID int64) (int64, error) {
	o := t.w.transfers[id]
	if o == nil || o.Status != from {
		return 0, nil
	}
	o.Status = to
	o.UpdatedBy = actorID
	now := jsonNow()
	if stamps.Approve {
		o.ApprovedAt, o.ApprovedBy = now, stamps.ApprovedBy
	}
	if stamps.Outbound {
		o.OutboundAt = now
	}
	if stamps.Received {
		o.ReceivedAt = now
	}
	if stamps.Cancelled {
		o.CancelledAt = now
	}
	return 1, nil
}

func (t *memTx) bumpItem(transferID, itemID int64, apply func(*TransferItem)) error {
	for i := range t.w.transferItems[transferID] {
		if t.w.transferItems[transferID][i].ID.Int64() == itemID {
			apply(&t.w.transferItems[transferID][i])
			return nil
		}
	}
	return fmt.Errorf("memStore: 调拨明细 %d 不存在", itemID)
}

func (t *memTx) BumpTransferItemOut(_ context.Context, itemID int64, qty inventory.Qty) error {
	for tid := range t.w.transferItems {
		if err := t.bumpItem(tid, itemID, func(it *TransferItem) { it.QtyOut = it.QtyOut.Add(qty) }); err == nil {
			return nil
		}
	}
	return fmt.Errorf("memStore: 调拨明细 %d 不存在", itemID)
}

func (t *memTx) BumpTransferItemIn(_ context.Context, itemID int64, qty inventory.Qty) error {
	for tid := range t.w.transferItems {
		if err := t.bumpItem(tid, itemID, func(it *TransferItem) { it.QtyIn = it.QtyIn.Add(qty) }); err == nil {
			return nil
		}
	}
	return fmt.Errorf("memStore: 调拨明细 %d 不存在", itemID)
}

func (t *memTx) InsertApproval(_ context.Context, rec ApprovalRecord) error {
	t.w.approvals = append(t.w.approvals, rec)
	return nil
}

// ---- memTx：盘点单 ----

func (t *memTx) InsertCount(_ context.Context, o *CountOrder) error {
	o.ID = database.ID(t.w.nextCountID)
	t.w.nextCountID++
	cp := *o
	t.w.counts[o.ID.Int64()] = &cp
	return nil
}

func (t *memTx) GetCount(_ context.Context, id int64) (*CountOrder, error) {
	if o, ok := t.w.counts[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (t *memTx) ListCounts(_ context.Context, f CountFilter, page, pageSize int) ([]CountOrder, int64, error) {
	var all []CountOrder
	for _, o := range t.w.counts {
		if countFilterMatch(f, *o) {
			all = append(all, *o)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	return paginate(all, page, pageSize)
}

func countFilterMatch(f CountFilter, o CountOrder) bool {
	if !f.AllWarehouses {
		visible := false
		for _, id := range f.WarehouseIDs {
			if id == o.WarehouseID {
				visible = true
			}
		}
		if !visible {
			return false
		}
	}
	if f.WarehouseID > 0 && f.WarehouseID != o.WarehouseID {
		return false
	}
	if f.Status != "" && f.Status != o.Status {
		return false
	}
	if f.CountNo != "" && f.CountNo != o.CountNo {
		return false
	}
	return true
}

func (t *memTx) UpdateCountStatus(_ context.Context, id int64, from, to string, stamps CountStamps, actorID int64) (int64, error) {
	o := t.w.counts[id]
	if o == nil || o.Status != from {
		return 0, nil
	}
	o.Status = to
	o.UpdatedBy = actorID
	now := jsonNow()
	if stamps.Frozen {
		o.FrozenAt = now
	}
	if stamps.Reviewed {
		o.ReviewedAt = now
	}
	if stamps.Completed {
		o.CompletedAt = now
	}
	if stamps.Cancelled {
		o.CancelledAt = now
	}
	return 1, nil
}

func (t *memTx) ReplaceCountItems(_ context.Context, countID int64, items []CountItem) error {
	for i := range items {
		items[i].ID = database.ID(int64(i + 1))
		items[i].CountID = countID
	}
	t.w.countItems[countID] = append([]CountItem(nil), items...)
	return nil
}

func (t *memTx) ListCountItems(_ context.Context, countID int64) ([]CountItem, error) {
	items := append([]CountItem(nil), t.w.countItems[countID]...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].InventoryRowID != items[j].InventoryRowID {
			return items[i].InventoryRowID < items[j].InventoryRowID
		}
		return items[i].SerialNo < items[j].SerialNo
	})
	return items, nil
}

func (t *memTx) UpsertCountRegistrations(_ context.Context, countID int64, actor inventory.Actor, regs []CountRegistration) ([]CountItem, error) {
	for _, reg := range regs {
		found := false
		for i := range t.w.countItems[countID] {
			it := &t.w.countItems[countID][i]
			if it.InventoryRowID == reg.InventoryRowID && it.SerialNo == reg.SerialNo {
				it.QtyCounted = NullQty{Qty: reg.Qty, Valid: true}
				it.CountedBy = actor.ID
				it.CountedAt = jsonNow()
				found = true
				break
			}
		}
		if !found {
			row := t.w.rows[reg.InventoryRowID]
			if row == nil {
				return nil, response.NewError(ErrCountItemForeign, nil)
			}
			t.w.countItems[countID] = append(t.w.countItems[countID], CountItem{
				ID: database.ID(int64(len(t.w.countItems[countID]) + 10000)), CountID: countID,
				InventoryRowID: reg.InventoryRowID, SKUID: row.sku,
				WarehouseID: row.wh, ZoneID: row.zone, ShelfID: row.shelf, BinID: row.bin,
				QtySystem: 0, QtyCounted: NullQty{Qty: reg.Qty, Valid: true},
				CountedBy: actor.ID, CountedAt: jsonNow(), SerialNo: reg.SerialNo,
			})
		}
	}
	return t.ListCountItems(context.Background(), countID)
}

func (t *memTx) ReplaceCountDifferences(_ context.Context, countID int64, diffs []CountDifference) error {
	for i := range diffs {
		diffs[i].ID = database.ID(int64(i + 1))
		diffs[i].CountID = countID
	}
	t.w.countDiffs[countID] = append([]CountDifference(nil), diffs...)
	return nil
}

func (t *memTx) ListCountDifferences(_ context.Context, countID int64) ([]CountDifference, error) {
	diffs := append([]CountDifference(nil), t.w.countDiffs[countID]...)
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].LineNo < diffs[j].LineNo })
	return diffs, nil
}

func (t *memTx) SettleCountDifferences(_ context.Context, countID int64, to string, adjustNos map[int64]string) error {
	for i := range t.w.countDiffs[countID] {
		d := &t.w.countDiffs[countID][i]
		if d.Status != DiffPending {
			return response.NewError(ErrStatusConflict, nil)
		}
		d.Status = to
		if no, ok := adjustNos[d.ID.Int64()]; ok {
			d.AdjustNo = no
		}
	}
	return nil
}

// ---- memTx：库存族只读编排 ----

func (t *memTx) rowRef(r *fakeInvRow) InventoryRowRef {
	return InventoryRowRef{
		ID: r.id, WarehouseID: r.wh, ZoneID: r.zone, ShelfID: r.shelf, BinID: r.bin,
		SKUID: r.sku, BatchID: r.batch,
		Total: r.total, Available: r.avail, Locked: r.locked, Frozen: r.frozen,
		PendingInspect: r.pendingInspect, Defective: r.defective,
	}
}

func (t *memTx) FindInventoryRows(_ context.Context, warehouseID int64, scope CountScope) ([]InventoryRowRef, error) {
	var out []InventoryRowRef
	for _, r := range t.w.rows {
		if r.wh != warehouseID {
			continue
		}
		if len(scope.ZoneIDs) > 0 && !containsI64(scope.ZoneIDs, r.zone) {
			continue
		}
		if len(scope.ShelfIDs) > 0 && !containsI64(scope.ShelfIDs, r.shelf) {
			continue
		}
		if len(scope.BinIDs) > 0 && !containsI64(scope.BinIDs, r.bin) {
			continue
		}
		if len(scope.SKUIDs) > 0 && !containsI64(scope.SKUIDs, r.sku) {
			continue
		}
		out = append(out, t.rowRef(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *memTx) GetInventoryRow(_ context.Context, id int64) (*InventoryRowRef, error) {
	if r, ok := t.w.rows[id]; ok {
		ref := t.rowRef(r)
		return &ref, nil
	}
	return nil, nil
}

func (t *memTx) FindRowLocks(_ context.Context, row InventoryRowRef, lockType string) ([]ActiveLockRef, error) {
	var out []ActiveLockRef
	for _, l := range t.w.locks {
		if l.status == "ACTIVE" && l.lockType == lockType &&
			l.wh == row.WarehouseID && l.bin == row.BinID && l.sku == row.SKUID && l.batch == row.BatchID {
			out = append(out, lockRef(*l))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *memTx) FindSourceLocks(_ context.Context, sourceType, sourceNo, lockType string) ([]ActiveLockRef, error) {
	var out []ActiveLockRef
	for _, l := range t.w.locks {
		if l.status == "ACTIVE" && l.lockType == lockType &&
			l.sourceType == sourceType && l.srcNo == sourceNo {
			out = append(out, lockRef(*l))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func lockRef(l fakeLock) ActiveLockRef {
	return ActiveLockRef{
		ID: l.id, SourceNo: l.srcNo, SourceType: l.sourceType, Qty: l.qty,
		WarehouseID: l.wh, BinID: l.bin, SKUID: l.sku, BatchID: l.batch,
	}
}

func (t *memTx) serialRef(s *fakeSerial) SerialRef {
	return SerialRef{ID: 1, SerialNo: s.serialNo, SKUID: s.sku, BatchID: s.batch}
}

func (t *memTx) FindSerialsForRow(_ context.Context, row InventoryRowRef) ([]SerialRef, error) {
	var out []SerialRef
	for _, s := range t.w.serials {
		if s.status == "IN_STOCK" && s.wh == row.WarehouseID && s.bin == row.BinID && s.sku == row.SKUID {
			if row.BatchID > 0 && s.batch != row.BatchID {
				continue
			}
			out = append(out, t.serialRef(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SerialNo < out[j].SerialNo })
	return out, nil
}

func (t *memTx) FindSerialsInBin(_ context.Context, warehouseID, binID, skuID, batchID int64, limit int64) ([]SerialRef, error) {
	var out []SerialRef
	for _, s := range t.w.serials {
		if s.status == "IN_STOCK" && s.wh == warehouseID && s.bin == binID && s.sku == skuID {
			if batchID > 0 && s.batch != batchID {
				continue
			}
			out = append(out, t.serialRef(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SerialNo < out[j].SerialNo })
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (t *memTx) FindSerialsInTransit(_ context.Context, sourceNo string, skuID int64) ([]SerialRef, error) {
	var out []SerialRef
	for _, s := range t.w.serials {
		if s.status == "OUTBOUND" && s.lastSourceType == sourceTransfer &&
			s.lastSrcNo == sourceNo && s.sku == skuID {
			out = append(out, t.serialRef(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SerialNo < out[j].SerialNo })
	return out, nil
}

func (t *memTx) FindSerialByNo(_ context.Context, serialNo string) (*SerialRef, error) {
	if s, ok := t.w.serials[serialNo]; ok {
		ref := t.serialRef(s)
		return &ref, nil
	}
	return nil, nil
}

func (t *memTx) FindAdjustmentNo(_ context.Context, warehouseID, binID, skuID, batchID int64, adjustType string, qty inventory.Qty) (string, error) {
	var no string
	for _, a := range t.w.adjustments {
		if a.wh == warehouseID && a.bin == binID && a.sku == skuID && a.batch == batchID &&
			a.adjustType == adjustType && a.qty == qty {
			no = a.adjustmentNo
		}
	}
	return no, nil
}

func (t *memTx) ListInTransit(_ context.Context, f InTransitFilter, page, pageSize int) ([]InTransitRow, int64, error) {
	agg := map[[2]int64]*InTransitRow{}
	for _, o := range t.w.transfers {
		if o.Status != TransferMoving {
			continue
		}
		if !transferFilterMatch(TransferFilter{AllWarehouses: f.AllWarehouses, WarehouseIDs: f.WarehouseIDs}, *o) {
			continue
		}
		if f.WarehouseID > 0 && f.WarehouseID != o.FromWarehouseID && f.WarehouseID != o.ToWarehouseID {
			continue
		}
		for _, it := range t.w.transferItems[o.ID.Int64()] {
			if f.SKUID > 0 && f.SKUID != it.SKUID {
				continue
			}
			key := [2]int64{it.SKUID, it.BatchID}
			row := agg[key]
			if row == nil {
				row = &InTransitRow{SKUID: it.SKUID, BatchID: it.BatchID}
				agg[key] = row
			}
			transit := it.QtyOut.Sub(it.QtyIn)
			if f.WarehouseID == 0 {
				row.OutTransit, row.InTransit = row.OutTransit.Add(transit), row.InTransit.Add(transit)
			} else {
				if o.FromWarehouseID == f.WarehouseID {
					row.OutTransit = row.OutTransit.Add(transit)
				}
				if o.ToWarehouseID == f.WarehouseID {
					row.InTransit = row.InTransit.Add(transit)
				}
			}
		}
	}
	var out []InTransitRow
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SKUID != out[j].SKUID {
			return out[i].SKUID < out[j].SKUID
		}
		return out[i].BatchID < out[j].BatchID
	})
	return paginate(out, page, pageSize)
}

// ---- 跨域端口替身 ----

type fakeBins struct{ ok map[[2]int64]bool }

func (f *fakeBins) ExistsActive(_ context.Context, warehouseID, binID int64) (bool, error) {
	return f.ok[[2]int64{warehouseID, binID}], nil
}

type fakeSKUs struct{ ok map[int64]bool }

func (f *fakeSKUs) ExistsActive(_ context.Context, skuID int64) (bool, error) {
	return f.ok[skuID], nil
}

type fakeFlags struct{ m map[int64]SKUFlags }

func (f *fakeFlags) GetFlags(_ context.Context, skuID int64) (SKUFlags, error) {
	return f.m[skuID], nil
}

type fakeFlagsMissing struct{}

func (fakeFlagsMissing) GetFlags(_ context.Context, _ int64) (SKUFlags, error) {
	return SKUFlags{}, fmt.Errorf("not wired")
}

type fakeIssuer struct{ w *fakeWorld }

func (f *fakeIssuer) Issue(_ context.Context, _ *gorm.DB, rule docnum.Rule, insert func(_ *gorm.DB, no string) error) (string, error) {
	f.w.counterSeq[rule.Prefix]++
	no := fmt.Sprintf("%s-20261003-%06d", rule.Prefix, f.w.counterSeq[rule.Prefix])
	return no, insert(nil, no)
}

// ---- 公共助手 ----

func paginate[T any](all []T, page, pageSize int) ([]T, int64, error) {
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

func containsI64(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func jsonNow() database.JSONTime {
	return database.JSONTime{Time: time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)}
}

func testActor(name string) inventory.Actor {
	return inventory.Actor{ID: 7, Name: name, RequestID: "req-test"}
}

// q 构造 Qty（整数语义捷径，小数走 ParseQty）。
func q(units int64) inventory.Qty { return inventory.Qty(units * 10000) }

// ctxBG 测试上下文。
func ctxBG() context.Context { return context.Background() }

// checkAllIdentity 断言世界内所有库存行恒等式成立（inventory-rules §2）。
func checkAllIdentity(t *testing.T, w *fakeWorld) {
	t.Helper()
	for _, r := range w.rows {
		if err := r.state().ValidateIdentity(); err != nil {
			t.Fatalf("库存行 %d 恒等式破坏: %v", r.id, err)
		}
	}
}
