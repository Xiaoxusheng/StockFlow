package sales

// 测试替身（ask 约束：单测不依赖 PostgreSQL/Redis/网络——参照 internal/masterdata 与
// internal/inventory 既有替身模式）：
//   - fakeRepo：内存承载本域十表 + 事务快照回滚语义（Tx 内 fn 错误 → 恢复快照，
//     与真实事务回滚等价）；状态守卫 UPDATE 同样实现 WHERE status=from 影响行数判定；
//     领取/指派原子操作经 mutex 串行化（对应数据库行级 UPDATE 原子性）；
//   - fakeStock：内存库存模型（available/locked/total 守卫、ACTIVE 锁登记、
//     ledger 幂等键登记）——复刻 inventory 原语的正确性语义（防超卖/幂等重放/核销拆分），
//     仅承载测试断言所需的语义子集，不是第二套库存实现；
//   - fakeCheckers / fakeExceptions / spyAudit / fakeNumbers：跨域校验、异常登记、
//     审计探针与单号计数器。

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// ---- fakeRepo：内存仓储 ----

type fakeRepo struct {
	mu sync.Mutex

	soRows     map[int64]*SalesOrder
	soItems    map[int64][]SalesOrderItem // so_id → items
	obRows     map[int64]*OutboundOrder
	obItems    map[int64][]OutboundItem // outbound_id → items
	allocs     []AllocationRecord
	picks      map[int64]*PickTask
	checks     map[int64]*CheckTask
	packages   map[int64]*PackingRecord
	packItems  []PackingItem
	shipments  map[int64]*Shipment
	approvals  []approvalRow
	batchCands map[string][]BatchCandidate // "wh:sku" → 候选
	binStock   map[string][]BinStock       // "wh:sku:batch" → 行
	serials    map[string]SerialState      // serial_no → 状态
	// pickPriorities/checkPriorities 任务优先级落列承载（效率层一期 B3 SetPick/CheckTaskPriority）。
	pickPriorities  map[int64]int
	checkPriorities map[int64]int
	nextID          int64
	txDepth         int
	failNextTx      error // Tx 预置失败（模拟底层故障）
}

type approvalRow struct {
	TargetType, TargetNo, Action, Result, Opinion string
	OperatorID                                    int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		soRows: map[int64]*SalesOrder{}, soItems: map[int64][]SalesOrderItem{},
		obRows: map[int64]*OutboundOrder{}, obItems: map[int64][]OutboundItem{},
		picks: map[int64]*PickTask{}, checks: map[int64]*CheckTask{},
		packages: map[int64]*PackingRecord{}, shipments: map[int64]*Shipment{},
		approvals:  []approvalRow{},
		batchCands: map[string][]BatchCandidate{}, binStock: map[string][]BinStock{},
		serials: map[string]SerialState{}, nextID: 1000,
		pickPriorities: map[int64]int{}, checkPriorities: map[int64]int{},
	}
}

func (f *fakeRepo) id() int64 { f.nextID++; return f.nextID }

func binKey(wh, sku, batch int64) string { return fmt.Sprintf("%d:%d:%d", wh, sku, batch) }

// Tx 事务语义：fn 正常 → 提交（内存即当前状态）；fn 错误 → 整体回滚（恢复快照）。
// 事务整体互斥：真实 PostgreSQL 中本域各业务事务均先以 SELECT ... FOR UPDATE 锁定
// 单据行（互斥点在单据行），并发同单事务被数据库串行化——内存替身以全事务互斥模拟
// 该语义，并规避"早快照 + 全量恢复"误伤已提交并发写的假回滚；真实并发交叉与
// 行级 UPDATE 原子性由 //go:build integration 集成测试在真实 PG 上覆盖（testing.md §4）。
var txMu sync.Mutex

func (f *fakeRepo) Tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if f.failNextTx != nil {
		err := f.failNextTx
		f.failNextTx = nil
		return err
	}
	txMu.Lock()
	defer txMu.Unlock()
	snap := f.snapshot()
	f.txDepth++
	err := fn(nil)
	f.txDepth--
	if err != nil {
		f.restore(snap)
	}
	return err
}

// snapshot/restore 深拷贝快照（回滚语义）。
func (f *fakeRepo) snapshot() *fakeRepo {
	c := newFakeRepo()
	c.nextID = f.nextID
	for k, v := range f.soRows {
		row := *v
		c.soRows[k] = &row
	}
	for k, v := range f.soItems {
		c.soItems[k] = append([]SalesOrderItem(nil), v...)
	}
	for k, v := range f.obRows {
		row := *v
		c.obRows[k] = &row
	}
	for k, v := range f.obItems {
		c.obItems[k] = append([]OutboundItem(nil), v...)
	}
	c.allocs = append([]AllocationRecord(nil), f.allocs...)
	for k, v := range f.picks {
		row := *v
		c.picks[k] = &row
	}
	for k, v := range f.checks {
		row := *v
		c.checks[k] = &row
	}
	for k, v := range f.packages {
		row := *v
		c.packages[k] = &row
	}
	c.packItems = append([]PackingItem(nil), f.packItems...)
	for k, v := range f.shipments {
		row := *v
		c.shipments[k] = &row
	}
	c.approvals = append([]approvalRow(nil), f.approvals...)
	for k, v := range f.batchCands {
		c.batchCands[k] = append([]BatchCandidate(nil), v...)
	}
	for k, v := range f.binStock {
		c.binStock[k] = append([]BinStock(nil), v...)
	}
	for k, v := range f.serials {
		c.serials[k] = v
	}
	for k, v := range f.pickPriorities {
		c.pickPriorities[k] = v
	}
	for k, v := range f.checkPriorities {
		c.checkPriorities[k] = v
	}
	return c
}

func (f *fakeRepo) restore(c *fakeRepo) {
	f.soRows, f.soItems, f.obRows, f.obItems = c.soRows, c.soItems, c.obRows, c.obItems
	f.allocs, f.picks, f.checks, f.packages, f.packItems, f.shipments =
		c.allocs, c.picks, c.checks, c.packages, c.packItems, c.shipments
	f.approvals, f.batchCands, f.binStock, f.serials, f.nextID =
		c.approvals, c.batchCands, c.binStock, c.serials, c.nextID
	f.pickPriorities, f.checkPriorities = c.pickPriorities, c.checkPriorities
}

// ---- 销售订单 ----

func (f *fakeRepo) InsertSalesOrder(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.soRows {
		if row.SoNo == o.SoNo {
			return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_sales_orders_no"})
		}
	}
	o.ID = database_ID(f.id())
	f.soRows[o.ID.Int64()] = o
	for i := range items {
		items[i].ID = database_ID(f.id())
		items[i].SoID = o.ID.Int64()
	}
	f.soItems[o.ID.Int64()] = items
	return nil
}

func (f *fakeRepo) UpdateSalesOrderDraft(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.soRows[o.ID.Int64()]
	if !ok || row.Status != SOStatusDraft {
		return response.NewError(ErrStateConflict, nil)
	}
	*row = *o
	f.soItems[o.ID.Int64()] = items
	return nil
}

func (f *fakeRepo) GetSalesOrderForUpdate(tx *gorm.DB, id int64) (*SalesOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.soRows[id]; ok {
		c := *row
		return &c, nil
	}
	return nil, nil
}

func (f *fakeRepo) GetSalesOrderByNo(tx *gorm.DB, no string) (*SalesOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.soRows {
		if row.SoNo == no {
			c := *row
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListSalesOrders(ctx context.Context, q SalesOrderQuery) ([]SalesOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []SalesOrder
	for _, row := range f.soRows {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		if q.Status != "" && row.Status != q.Status {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	total := int64(len(rows))
	if lo := (q.Page - 1) * q.PageSize; lo < len(rows) {
		hi := lo + q.PageSize
		if hi > len(rows) {
			hi = len(rows)
		}
		rows = rows[lo:hi]
	} else {
		rows = nil
	}
	return rows, total, nil
}

func (f *fakeRepo) ListSalesOrderItems(tx *gorm.DB, soID int64) ([]SalesOrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SalesOrderItem(nil), f.soItems[soID]...), nil
}

func (f *fakeRepo) MarkSalesOrderStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.soRows[id]
	if !ok || row.Status != from {
		return 0, nil
	}
	row.Status = to
	row.UpdatedBy = stamp.By
	if stamp.Approved {
		row.ApprovedBy = stamp.By
	}
	return 1, nil
}

func (f *fakeRepo) UpdateSalesOrderItemProgress(tx *gorm.DB, soID, lineNo int64, allocated, shipped *Qty) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.soItems[soID] {
		if f.soItems[soID][i].LineNo == lineNo {
			if allocated != nil {
				f.soItems[soID][i].QtyAllocated = *allocated
			}
			if shipped != nil {
				f.soItems[soID][i].QtyShipped = *shipped
			}
		}
	}
	return nil
}

func (f *fakeRepo) InsertApproval(tx *gorm.DB, targetType, targetNo, action, result, opinion string, actor Actor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals = append(f.approvals, approvalRow{targetType, targetNo, action, result, opinion, actor.ID})
	return nil
}

// ---- 出库单 ----

func (f *fakeRepo) InsertOutboundOrder(tx *gorm.DB, o *OutboundOrder, items []OutboundItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.obRows {
		if row.OutboundNo == o.OutboundNo {
			return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_outbound_orders_no"})
		}
	}
	o.ID = database_ID(f.id())
	f.obRows[o.ID.Int64()] = o
	for i := range items {
		items[i].ID = database_ID(f.id())
		items[i].OutboundID = o.ID.Int64()
	}
	f.obItems[o.ID.Int64()] = items
	return nil
}

func (f *fakeRepo) GetOutboundOrderForUpdate(tx *gorm.DB, id int64) (*OutboundOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.obRows[id]; ok {
		c := *row
		return &c, nil
	}
	return nil, nil
}

func (f *fakeRepo) GetOutboundOrderByNo(tx *gorm.DB, no string) (*OutboundOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.obRows {
		if row.OutboundNo == no {
			c := *row
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListOutboundOrders(ctx context.Context, q OutboundQuery) ([]OutboundOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []OutboundOrder
	for _, row := range f.obRows {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	total := int64(len(rows))
	if lo := (q.Page - 1) * q.PageSize; lo < len(rows) {
		rows = rows[lo:min(lo+q.PageSize, len(rows))]
	} else {
		rows = nil
	}
	return rows, total, nil
}

func (f *fakeRepo) ListOutboundOrdersBySO(tx *gorm.DB, soNo string) ([]OutboundOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []OutboundOrder
	for _, row := range f.obRows {
		if row.SoNo == soNo {
			rows = append(rows, *row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListOutboundItems(tx *gorm.DB, outboundID int64) ([]OutboundItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]OutboundItem(nil), f.obItems[outboundID]...), nil
}

func (f *fakeRepo) MarkOutboundStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.obRows[id]
	if !ok || row.Status != from {
		return 0, nil
	}
	row.Status = to
	row.UpdatedBy = stamp.By
	return 1, nil
}

func (f *fakeRepo) UpdateOutboundItemProgress(tx *gorm.DB, outboundID, lineNo int64, cols OutboundItemProgress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.obItems[outboundID] {
		if f.obItems[outboundID][i].LineNo == lineNo {
			if cols.Picked != nil {
				f.obItems[outboundID][i].QtyPicked = *cols.Picked
			}
			if cols.Checked != nil {
				f.obItems[outboundID][i].QtyChecked = *cols.Checked
			}
			if cols.Packed != nil {
				f.obItems[outboundID][i].QtyPacked = *cols.Packed
			}
			if cols.Shipped != nil {
				f.obItems[outboundID][i].QtyShipped = *cols.Shipped
			}
		}
	}
	return nil
}

// ---- 分配记录 ----

func (f *fakeRepo) InsertAllocations(tx *gorm.DB, recs []AllocationRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range recs {
		recs[i].ID = database_ID(f.id())
		f.allocs = append(f.allocs, recs[i])
	}
	return nil
}

// UpdateAllocationQty 短拣重同步替身：内存改写分配记录数量与 reason 注记。
func (f *fakeRepo) UpdateAllocationQty(tx *gorm.DB, recID int64, qty Qty, by int64, reasonNote map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.allocs {
		if f.allocs[i].ID.Int64() == recID {
			f.allocs[i].Qty = qty
			if reasonNote != nil {
				if f.allocs[i].Reason == nil {
					f.allocs[i].Reason = map[string]any{}
				}
				for k, v := range reasonNote {
					f.allocs[i].Reason[k] = v
				}
			}
			f.allocs[i].UpdatedBy = by
			return nil
		}
	}
	return response.NewError(response.CodeNotFound, map[string]any{"reason": "分配记录不存在", "allocation_record_id": recID})
}

func (f *fakeRepo) DeleteAllocations(tx *gorm.DB, outboundNo string, lineNo int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []AllocationRecord
	for _, rec := range f.allocs {
		if rec.OutboundNo == outboundNo && (lineNo == 0 || rec.LineNo == lineNo) {
			continue
		}
		kept = append(kept, rec)
	}
	f.allocs = kept
	return nil
}

func (f *fakeRepo) ListAllocations(tx *gorm.DB, outboundNo string, lineNo int64) ([]AllocationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []AllocationRecord
	for _, rec := range f.allocs {
		if rec.OutboundNo == outboundNo && (lineNo == 0 || rec.LineNo == lineNo) {
			rows = append(rows, rec)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListAllocationsByOutbound(tx *gorm.DB, outboundNo string) ([]AllocationRecord, error) {
	return f.ListAllocations(tx, outboundNo, 0)
}

func (f *fakeRepo) ListAllocationsPage(ctx context.Context, q AllocationQuery) ([]AllocationRecord, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []AllocationRecord
	for _, rec := range f.allocs {
		if !q.All && !containsID(q.WarehouseIDs, rec.WarehouseID) {
			continue
		}
		rows = append(rows, rec)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	return rows, int64(len(rows)), nil
}

// ---- 拣货任务 ----

func (f *fakeRepo) InsertPickTasks(tx *gorm.DB, tasks []PickTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range tasks {
		tasks[i].ID = database_ID(f.id())
		t := tasks[i]
		f.picks[t.ID.Int64()] = &t
	}
	return nil
}

func (f *fakeRepo) GetPickTask(tx *gorm.DB, id int64) (*PickTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.picks[id]; ok {
		c := *row
		return &c, nil
	}
	return nil, nil
}

// ClaimPickTask 原子抢占（mutex 串行化模拟数据库行级 UPDATE 原子性）。
func (f *fakeRepo) ClaimPickTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.picks[id]
	if !ok || row.Status != PickStatusPending {
		return 0, nil
	}
	row.Status = PickStatusClaimed
	row.AssigneeID, row.AssigneeName = assigneeID, assigneeName
	return 1, nil
}

func (f *fakeRepo) MarkPickTaskStatus(tx *gorm.DB, id int64, from, to string, stamp PickStamp) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.picks[id]
	if !ok || row.Status != from {
		return 0, nil
	}
	row.Status = to
	row.UpdatedBy = stamp.By
	if stamp.PickedQty != nil {
		row.PickedQty = *stamp.PickedQty
	}
	if stamp.ScannedCode != nil {
		row.ScannedCode = *stamp.ScannedCode
	}
	return 1, nil
}

// SetPickTaskPriority 优先级单列守卫更新建模（终态 0 行；列承载 pickPriorities）。
func (f *fakeRepo) SetPickTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.picks[id]
	if !ok || row.Status == PickStatusPicked || row.Status == PickStatusCancelled {
		return 0, nil
	}
	row.UpdatedBy = by
	f.pickPriorities[id] = priority
	return 1, nil
}

// SetCheckTaskPriority 优先级单列守卫更新建模（终态 DONE/EXCEPTION 0 行）。
func (f *fakeRepo) SetCheckTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.checks[id]
	if !ok || row.Status == CheckStatusDone || row.Status == CheckStatusException {
		return 0, nil
	}
	row.UpdatedBy = by
	f.checkPriorities[id] = priority
	return 1, nil
}

func (f *fakeRepo) CancelPickTasks(tx *gorm.DB, outboundNo string, lineNo int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.picks {
		if row.OutboundNo == outboundNo && (lineNo == 0 || row.OutboundLineNo == lineNo) &&
			(row.Status == PickStatusPending || row.Status == PickStatusClaimed) {
			row.Status = PickStatusCancelled
		}
	}
	return nil
}

func (f *fakeRepo) ListPickTasksByOutbound(tx *gorm.DB, outboundNo string) ([]PickTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []PickTask
	for _, row := range f.picks {
		if row.OutboundNo == outboundNo {
			rows = append(rows, *row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListPickTasks(ctx context.Context, q PickQuery) ([]PickTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []PickTask
	for _, row := range f.picks {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	return rows, int64(len(rows)), nil
}

// ---- 复核任务 ----

func (f *fakeRepo) InsertCheckTasks(tx *gorm.DB, tasks []CheckTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range tasks {
		tasks[i].ID = database_ID(f.id())
		t := tasks[i]
		f.checks[t.ID.Int64()] = &t
	}
	return nil
}

func (f *fakeRepo) GetCheckTask(tx *gorm.DB, id int64) (*CheckTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.checks[id]; ok {
		c := *row
		return &c, nil
	}
	return nil, nil
}

func (f *fakeRepo) AssignCheckTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.checks[id]
	if !ok || row.Status != CheckStatusPending {
		return 0, nil
	}
	row.AssigneeID, row.AssigneeName = assigneeID, assigneeName
	return 1, nil
}

func (f *fakeRepo) MarkCheckTaskStatus(tx *gorm.DB, id int64, from, to, result string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.checks[id]
	if !ok || row.Status != from {
		return 0, nil
	}
	row.Status, row.Result = to, result
	return 1, nil
}

func (f *fakeRepo) ListCheckTasksByOutbound(tx *gorm.DB, outboundNo string) ([]CheckTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []CheckTask
	for _, row := range f.checks {
		if row.OutboundNo == outboundNo {
			rows = append(rows, *row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListCheckTasks(ctx context.Context, q CheckQuery) ([]CheckTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []CheckTask
	for _, row := range f.checks {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	return rows, int64(len(rows)), nil
}

// ---- 打包 ----

func (f *fakeRepo) FindPackageByIdempotencyKey(tx *gorm.DB, key string) (*PackingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.packages {
		if row.IdempotencyKey != nil && *row.IdempotencyKey == key {
			c := *row
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) InsertPackage(tx *gorm.DB, p *PackingRecord, items []PackingItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.IdempotencyKey != nil {
		for _, row := range f.packages {
			if row.IdempotencyKey != nil && *row.IdempotencyKey == *p.IdempotencyKey {
				return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_packing_records_idempotency"})
			}
		}
	}
	for _, row := range f.packages {
		if row.PackageNo == p.PackageNo {
			return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_packing_records_no"})
		}
	}
	p.ID = database_ID(f.id())
	f.packages[p.ID.Int64()] = p
	for i := range items {
		items[i].ID = database_ID(f.id())
		items[i].PackageID = p.ID.Int64()
		f.packItems = append(f.packItems, items[i])
	}
	return nil
}

func (f *fakeRepo) SumPackedByLine(tx *gorm.DB, outboundID int64) (map[int64]Qty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]Qty{}
	for _, it := range f.packItems {
		if it.OutboundID == outboundID {
			out[it.LineNo] = out[it.LineNo].Add(it.Qty)
		}
	}
	return out, nil
}

func (f *fakeRepo) CountPackagesByOutbound(tx *gorm.DB, outboundNo string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, row := range f.packages {
		if row.OutboundNo == outboundNo {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListPackagesByOutbound(tx *gorm.DB, outboundNo string) ([]PackingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []PackingRecord
	for _, row := range f.packages {
		if row.OutboundNo == outboundNo {
			rows = append(rows, *row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListPackages(ctx context.Context, q PackingQuery) ([]PackingRecord, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []PackingRecord
	for _, row := range f.packages {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	return rows, int64(len(rows)), nil
}

// ---- 发货单 ----

func (f *fakeRepo) FindShipmentByIdempotencyKey(tx *gorm.DB, key string) (*Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.shipments {
		if row.IdempotencyKey != nil && *row.IdempotencyKey == key {
			c := *row
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) InsertShipment(tx *gorm.DB, s *Shipment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.IdempotencyKey != nil {
		for _, row := range f.shipments {
			if row.IdempotencyKey != nil && *row.IdempotencyKey == *s.IdempotencyKey {
				return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_shipments_idempotency"})
			}
		}
	}
	for _, row := range f.shipments {
		if row.ShipmentNo == s.ShipmentNo {
			return response.NewError(response.CodeConflict, map[string]any{"constraint": "uk_shipments_no"})
		}
	}
	s.ID = database_ID(f.id())
	f.shipments[s.ID.Int64()] = s
	return nil
}

func (f *fakeRepo) GetShipment(tx *gorm.DB, id int64) (*Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.shipments[id]; ok {
		c := *row
		return &c, nil
	}
	return nil, nil
}

func (f *fakeRepo) MarkShipmentStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.shipments[id]
	if !ok || row.Status != from {
		return 0, nil
	}
	row.Status = to
	row.UpdatedBy = stamp.By
	if stamp.Shipped { // 生产实现落 shipped_at = now()（business-flow §13.4）
		row.ShippedAt = database.Now()
	}
	return 1, nil
}

func (f *fakeRepo) ListShipmentsByOutbound(tx *gorm.DB, outboundNo string) ([]Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []Shipment
	for _, row := range f.shipments {
		if row.OutboundNo == outboundNo {
			rows = append(rows, *row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeRepo) ListShipments(ctx context.Context, q ShipmentQuery) ([]Shipment, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []Shipment
	for _, row := range f.shipments {
		if !q.All && !containsID(q.WarehouseIDs, row.WarehouseID) {
			continue
		}
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	return rows, int64(len(rows)), nil
}

// ---- 库存族只读替身 ----

func (f *fakeRepo) ReadBatchCandidates(tx *gorm.DB, warehouseID, skuID int64) ([]BatchCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []BatchCandidate
	for _, c := range f.batchCands[fmt.Sprintf("%d:%d", warehouseID, skuID)] {
		rows = append(rows, c)
	}
	// 测试数据按 warehouse 塞入；同仓多 SKU 场景由测试数据自身保证 sku 匹配语义
	// （fake 不模拟批次表 join，候选行即该 SKU 的候选）。
	return rows, nil
}

func (f *fakeRepo) ReadBinStock(tx *gorm.DB, warehouseID, skuID, batchID int64) ([]BinStock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []BinStock
	for _, r := range f.binStock[binKey(warehouseID, skuID, batchID)] {
		if r.AvailableQty.IsPositive() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BinID < out[j].BinID })
	return out, nil
}

func (f *fakeRepo) ReadBinLocation(tx *gorm.DB, warehouseID, binID, skuID, batchID int64) (zoneID, shelfID int64, found bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.binStock[binKey(warehouseID, skuID, batchID)] {
		if r.BinID == binID {
			return r.ZoneID, r.ShelfID, true, nil
		}
	}
	return 0, 0, false, nil
}

func (f *fakeRepo) ReadSerialStates(tx *gorm.DB, skuID int64, serials []string) ([]SerialState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []SerialState
	for _, sn := range serials {
		if st, ok := f.serials[sn]; ok && st.SKUID == skuID {
			rows = append(rows, st)
		}
	}
	return rows, nil
}

// ---- fakeStock：库存原语语义替身（防超卖守卫 / 锁登记 / 幂等键重放 / 核销拆分） ----

type fakeStockRow struct {
	Available, Locked, Total Qty
}

type fakeStock struct {
	mu        sync.Mutex
	rows      map[string]*fakeStockRow // "wh:bin:sku:batch"
	locks     map[int64]*fakeLock
	ledgers   map[string]int64 // 幂等键 → 流水 id
	lockByKey map[string]int64 // 幂等键 → 锁 id（Lock 重放路径回查）
	nextID    int64
	failOn    string // 非空时对含该子串的幂等键动作注入失败（可选）
}

// fake 中模拟 inventory 原语错误（错误码值与 internal/inventory 冻结注册一致——
// 断言按码匹配；不 import inventory 包，与判据 2 一致）。
var (
	fakeErrNotEnough    = response.Register("INVENTORY_NOT_ENOUGH", "库存不足", 409)
	fakeErrLockNotFound = response.Register("INVENTORY_LOCK_NOT_FOUND", "库存锁定记录不存在或已完结", 404)
	fakeErrRowNotFound  = response.Register("INVENTORY_RECORD_NOT_FOUND", "库存记录不存在", 404)
)

type fakeLock struct {
	ID                         int64
	Wh, Bin, SKU, Batch        int64
	Type, SourceType, SourceNo string
	Qty                        Qty
	Status                     string // ACTIVE/RELEASED/CONSUMED
}

func newFakeStock() *fakeStock {
	return &fakeStock{rows: map[string]*fakeStockRow{}, locks: map[int64]*fakeLock{}, ledgers: map[string]int64{}, lockByKey: map[string]int64{}, nextID: 5000}
}

func sk(wh, bin, sku, batch int64) string { return fmt.Sprintf("%d:%d:%d:%d", wh, bin, sku, batch) }

// Seed 塞入库存（available，total 同步；供 Lock/Deduct 守卫语义）。
func (s *fakeStock) Seed(wh, bin, sku, batch int64, avail Qty) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[sk(wh, bin, sku, batch)] = &fakeStockRow{Available: avail, Total: avail}
}

// Snapshot 读六状态简表（available/locked/total）供断言。
func (s *fakeStock) Snapshot(wh, bin, sku, batch int64) (avail, locked, total Qty) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[sk(wh, bin, sku, batch)]; ok {
		return r.Available, r.Locked, r.Total
	}
	return 0, 0, 0
}

func (s *fakeStock) LockByID(id int64) *fakeLock {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.locks[id]; ok {
		c := *l
		return &c
	}
	return nil
}

func (s *fakeStock) LedgerReplay(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ledgers[key]
	return ok
}

func (s *fakeStock) Lock(ctx context.Context, tx *gorm.DB, op LockOp) (MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.IdempotencyKey != "" {
		if id, ok := s.ledgers[op.IdempotencyKey]; ok {
			return MutationResult{Replay: true, Ledger: LedgerRef{ID: id}, LockID: s.lockByKey[op.IdempotencyKey]}, nil
		}
	}
	row, ok := s.rows[sk(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID)]
	if !ok {
		return MutationResult{}, response.NewError(fakeErrRowNotFound, nil)
	}
	// 同来源同类型 ACTIVE 锁幂等（双保险，inventory service.go:510-531；先查重后变更）。
	for _, l := range s.locks {
		if l.Status == "ACTIVE" && l.SourceType == op.Source.Type && l.SourceNo == op.Source.No &&
			l.Wh == op.Key.WarehouseID && l.Bin == op.Key.BinID && l.SKU == op.Key.SKUID && l.Batch == op.Key.BatchID &&
			l.Type == op.LockType {
			return MutationResult{Replay: true, LockID: l.ID}, nil
		}
	}
	if row.Available.Sub(op.Qty).IsNegative() { // 数据层守卫（防超卖）
		return MutationResult{}, response.NewError(fakeErrNotEnough, map[string]any{
			"need": op.Qty.String(), "available": row.Available.String(),
		})
	}
	row.Available = row.Available.Sub(op.Qty)
	row.Locked = row.Locked.Add(op.Qty)
	s.nextID++
	id := s.nextID
	s.locks[id] = &fakeLock{ID: id, Wh: op.Key.WarehouseID, Bin: op.Key.BinID, SKU: op.Key.SKUID,
		Batch: op.Key.BatchID, Type: op.LockType, SourceType: op.Source.Type, SourceNo: op.Source.No,
		Qty: op.Qty, Status: "ACTIVE"}
	if op.IdempotencyKey != "" {
		s.ledgers[op.IdempotencyKey] = id
		s.lockByKey[op.IdempotencyKey] = id
	}
	return MutationResult{LockID: id}, nil
}

func (s *fakeStock) ReleaseLock(ctx context.Context, tx *gorm.DB, op ReleaseLockOp) (MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.IdempotencyKey != "" {
		if id, ok := s.ledgers[op.IdempotencyKey]; ok {
			return MutationResult{Replay: true, Ledger: LedgerRef{ID: id}, LockID: op.LockID}, nil
		}
	}
	l, ok := s.locks[op.LockID]
	if !ok || l.Status != "ACTIVE" || l.Qty.Sub(op.Qty).IsNegative() {
		return MutationResult{}, response.NewError(fakeErrLockNotFound, nil)
	}
	// 行守卫：locked ≥ qty。
	row, ok := s.rows[sk(l.Wh, l.Bin, l.SKU, l.Batch)]
	if !ok || row.Locked.Sub(op.Qty).IsNegative() {
		return MutationResult{}, response.NewError(fakeErrNotEnough, nil)
	}
	row.Available = row.Available.Add(op.Qty)
	row.Locked = row.Locked.Sub(op.Qty)
	remaining := l.Qty.Sub(op.Qty)
	if remaining.IsZero() {
		l.Status = "RELEASED"
	} else {
		l.Qty = remaining
	}
	if op.IdempotencyKey != "" {
		s.nextID++
		s.ledgers[op.IdempotencyKey] = s.nextID
	}
	return MutationResult{LockID: op.LockID}, nil
}

func (s *fakeStock) Deduct(ctx context.Context, tx *gorm.DB, op DeductOp) (MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.IdempotencyKey != "" {
		if id, ok := s.ledgers[op.IdempotencyKey]; ok {
			return MutationResult{Replay: true, Ledger: LedgerRef{ID: id}}, nil
		}
	}
	row, ok := s.rows[sk(op.Key.WarehouseID, op.Key.BinID, op.Key.SKUID, op.Key.BatchID)]
	if !ok || row.Locked.Sub(op.Qty).IsNegative() {
		return MutationResult{}, response.NewError(fakeErrNotEnough, nil)
	}
	row.Locked = row.Locked.Sub(op.Qty)
	row.Total = row.Total.Sub(op.Qty)
	if op.LockID > 0 {
		l, ok := s.locks[op.LockID]
		if !ok || l.Status != "ACTIVE" || l.Wh != op.Key.WarehouseID || l.Bin != op.Key.BinID ||
			l.SKU != op.Key.SKUID || l.Batch != op.Key.BatchID || l.Qty.Sub(op.Qty).IsNegative() {
			return MutationResult{}, response.NewError(fakeErrLockNotFound, nil)
		}
		remaining := l.Qty.Sub(op.Qty)
		if remaining.IsZero() {
			l.Status = "CONSUMED"
		} else {
			l.Qty = remaining
		}
	}
	if op.IdempotencyKey != "" {
		s.nextID++
		s.ledgers[op.IdempotencyKey] = s.nextID
	}
	return MutationResult{}, nil
}

func (s *fakeStock) SerialEvent(ctx context.Context, tx *gorm.DB, op SerialOp) (int64, bool, error) {
	return 0, false, nil // 发货序列号台账断言经 fakeRepo.serials 由测试自设（本替身不建模）
}

// ---- 跨域校验替身 ----

type fakeSKUs struct {
	mu    sync.Mutex
	flags map[int64]SKUFlags
}

func newFakeSKUs(flags map[int64]SKUFlags) *fakeSKUs { return &fakeSKUs{flags: flags} }

func (f *fakeSKUs) GetFlags(ctx context.Context, skuID int64) (SKUFlags, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fl, ok := f.flags[skuID]; ok {
		return fl, nil
	}
	return SKUFlags{}, nil
}

type fakeCustomers struct{ ok bool }

func (f fakeCustomers) ExistsActive(ctx context.Context, customerID int64) (bool, error) {
	return f.ok, nil
}

type fakeBins struct{ ok bool }

func (f fakeBins) ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error) {
	return f.ok, nil
}

type fakeExceptions struct {
	mu   sync.Mutex
	rows []string
	fail bool
}

func (f *fakeExceptions) Create(ctx context.Context, tx *gorm.DB, excType, sourceType, sourceNo string, detail ExceptionDetail) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", response.NewError(response.CodeInternalError, nil)
	}
	no := "EX-TEST-" + excType
	f.rows = append(f.rows, no+"|"+sourceType+"|"+sourceNo+"|"+detail.Reason)
	return no, nil
}

// spyAudit 审计探针（签名 = middleware.Audit；捕获同事务写入的审计条目）。
type spyAudit struct {
	mu      sync.Mutex
	entries []middleware.AuditEntry
}

func (s *spyAudit) audit(tx *gorm.DB, e middleware.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *spyAudit) count(action string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

// fakeNumbers 内存单号计数器（生产绑定 docnum；单测缝，见 NumberIssuer）。
type fakeNumbers struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFakeNumbers() *fakeNumbers { return &fakeNumbers{counts: map[string]int{}} }

// fakeNumRunToken 本轮运行令牌（三位，100–999）。单号桩原样输出
// `{prefix}-20261003-%06d`，同库重复运行会与上一轮/种子行（如 dev_seed.sql 的
// SO-20261003-000001）撞号 → 单据创建 23505 → 409。把令牌放进 6 位序号高位后，
// 本轮号码恒 ≥100001，既不撞低号段历史数据，也保留 `^[A-Z]{2}-\d{8}-\d{6}$` 形态。
var fakeNumRunToken = 100 + time.Now().UnixNano()%900

// fakeNumGlobalSeq 进程级单号序号（跨 harness 全局递增）：集成测试每个用例各自
// newHarness（独立 fakeNumbers），若按用例计数则各用例都从 1 开始 → 同一轮内互相撞号。
var fakeNumGlobalSeq sync.Map // prefix → *atomic.Int64

func (f *fakeNumbers) next(ctx context.Context, tx *gorm.DB, prefix string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[prefix]++ // 保留按用例计数（单测断言 h.nums.counts[...] 依赖）
	v, _ := fakeNumGlobalSeq.LoadOrStore(prefix, new(atomic.Int64))
	n := fakeNumRunToken*1000 + int64(v.(*atomic.Int64).Add(1))
	return fmt.Sprintf("%s-20261003-%06d", prefix, n), nil
}

// database_ID 替身文件内的 database.ID 直写（与生产模型同类型）。
func database_ID(v int64) database.ID { return database.ID(v) }
