package purchase

// 测试替身：内存版 Repository（ask 约束：单测不依赖 PostgreSQL，接口替身）。
// 语义对齐 GORM 实现：状态迁移与数量累计全部按"条件判定 + 影响行数"建模
// （WHERE status=<前置态> / WHERE 累计 <= 上限），未命中返回 0 行供服务层守卫判定；
// 唯一约束（收货幂等键）以 *pgconn.PgError{23505} 建模兜底路径。

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/stock"
)

type fakeRepo struct {
	mu  sync.Mutex
	db  *gorm.DB
	seq int64

	pos        map[int64]*PurchaseOrder
	poItems    map[int64][]*PurchaseOrderItem // poID → items（line_no 升序）
	inbounds   map[int64]*InboundOrder
	inbItems   map[int64][]*InboundItem // inboundID → items
	receipts   map[int64]*Receipt
	rctItems   map[int64][]*ReceiptItem // receiptID → items
	qcs        map[int64]*QualityOrder
	qcItems    map[int64][]*QualityItem // qcID → items
	tasks      map[int64]*PutawayTask
	approvals  []*DocumentApproval
	idemKeys   map[string]int64 // idempotency_key → receiptID
	claimGuard chan struct{}    // 串行化抢占判定（模拟行锁）

	nextReceiptID int64

	taskPriorities map[int64]int // 优先级列（fake 单列承载，断言用）
}

func newFakeRepo(spy *auditSpy) *fakeRepo {
	db, err := openTestGorm(spy)
	if err != nil {
		panic("打开假 gorm 失败: " + err.Error())
	}
	return &fakeRepo{
		db:             db,
		pos:            map[int64]*PurchaseOrder{},
		poItems:        map[int64][]*PurchaseOrderItem{},
		inbounds:       map[int64]*InboundOrder{},
		inbItems:       map[int64][]*InboundItem{},
		receipts:       map[int64]*Receipt{},
		rctItems:       map[int64][]*ReceiptItem{},
		qcs:            map[int64]*QualityOrder{},
		qcItems:        map[int64][]*QualityItem{},
		tasks:          map[int64]*PutawayTask{},
		idemKeys:       map[string]int64{},
		claimGuard:     make(chan struct{}, 1),
		taskPriorities: map[int64]int{},
		nextReceiptID:  1,
	}
}

func (f *fakeRepo) DB() *gorm.DB { return f.db }

func (f *fakeRepo) nextID() int64 {
	f.seq++
	return f.seq
}

// ---- seed helpers（测试夹具）----

func (f *fakeRepo) seedPO(status string, whID int64, lines map[int64]stock.Qty) *PurchaseOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	po := &PurchaseOrder{
		BaseModel:   database.BaseModel{ID: database.ID(id)},
		PONo:        fmt.Sprintf("PO-TEST-%06d", id),
		SupplierID:  11,
		WarehouseID: whID,
		Status:      status,
	}
	f.pos[id] = po
	line := 0
	var total stock.Qty
	for sku, qty := range lines {
		line++
		amount, _ := mulAmount(qty, 10000) // 单价 1.0000
		total = total.Add(amount)
		f.poItems[id] = append(f.poItems[id], &PurchaseOrderItem{
			BaseModel:  database.BaseModel{ID: database.ID(f.nextID())},
			POID:       id,
			LineNo:     line,
			SKUID:      sku,
			QtyOrdered: qty,
			Price:      10000,
			Amount:     amount,
		})
	}
	po.TotalAmount = total
	sort.Slice(f.poItems[id], func(i, j int) bool { return f.poItems[id][i].LineNo < f.poItems[id][j].LineNo })
	return f.clonePO(po)
}

func (f *fakeRepo) seedInbound(status string, whID int64, sourceType, sourceNo string, lines map[int64]stock.Qty) *InboundOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	o := &InboundOrder{
		BaseModel:   database.BaseModel{ID: database.ID(id)},
		InboundNo:   fmt.Sprintf("IN-TEST-%06d", id),
		SourceType:  sourceType,
		SourceNo:    sourceNo,
		WarehouseID: whID,
		Status:      status,
	}
	f.inbounds[id] = o
	line := 0
	for sku, qty := range lines {
		line++
		f.inbItems[id] = append(f.inbItems[id], &InboundItem{
			BaseModel: database.BaseModel{ID: database.ID(f.nextID())},
			InboundID: id,
			LineNo:    line,
			SKUID:     sku,
			Qty:       qty,
		})
	}
	sort.Slice(f.inbItems[id], func(i, j int) bool { return f.inbItems[id][i].LineNo < f.inbItems[id][j].LineNo })
	cp := *o
	return &cp
}

func (f *fakeRepo) seedTask(inboundNo string, skuID, batchID int64, qty stock.Qty, fromState, status string, whID, binID int64) *PutawayTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	t := &PutawayTask{
		BaseModel:         database.BaseModel{ID: database.ID(id)},
		PutawayNo:         fmt.Sprintf("PW-TEST-%06d", id),
		InboundNo:         inboundNo,
		SKUID:             skuID,
		BatchID:           batchID,
		Qty:               qty,
		FromState:         fromState,
		TargetWarehouseID: whID,
		TargetBinID:       binID,
		Status:            status,
	}
	f.tasks[id] = t
	cp := *t
	return &cp
}

func (f *fakeRepo) clonePO(po *PurchaseOrder) *PurchaseOrder {
	cp := *po
	return &cp
}

func (f *fakeRepo) po(id int64) *PurchaseOrder { return f.pos[id] }

// ---- 采购订单 ----

func (f *fakeRepo) FindPOByID(_ context.Context, id int64) (*PurchaseOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if po, ok := f.pos[id]; ok {
		return f.clonePO(po), nil
	}
	return nil, nil
}

// FindPOByIDForUpdate 内存替身：单线程测试无真实行锁，语义同 FindPOByID
// （真实实现经 SELECT ... FOR UPDATE 与并发事务互斥）。
func (f *fakeRepo) FindPOByIDForUpdate(_ context.Context, _ *gorm.DB, id int64) (*PurchaseOrder, error) {
	return f.FindPOByID(context.Background(), id)
}

func (f *fakeRepo) FindPOByNoTx(_ context.Context, _ *gorm.DB, poNo string) (*PurchaseOrder, error) {
	return f.FindPOByNo(context.Background(), poNo)
}

func (f *fakeRepo) FindPOByNo(_ context.Context, poNo string) (*PurchaseOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, po := range f.pos {
		if po.PONo == poNo {
			return f.clonePO(po), nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListPOs(_ context.Context, fl POListFilter) ([]*PurchaseOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PurchaseOrder
	for _, po := range f.pos {
		if !scopeMatch(po.WarehouseID, fl.Scope) {
			continue
		}
		if fl.Status != "" && po.Status != fl.Status {
			continue
		}
		if fl.SupplierID > 0 && po.SupplierID != fl.SupplierID {
			continue
		}
		if fl.WarehouseID > 0 && po.WarehouseID != fl.WarehouseID {
			continue
		}
		if fl.Keyword != "" && !strings.Contains(po.PONo, fl.Keyword) {
			continue
		}
		out = append(out, f.clonePO(po))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := int64(len(out))
	return pageSlice(out, fl.Page, fl.PageSize), total, nil
}

func scopeMatch(whID int64, sc WarehouseScope) bool {
	if sc.All {
		return true
	}
	for _, id := range sc.IDs {
		if id == whID {
			return true
		}
	}
	return false
}

func pageSlice[T any](items []*T, page, pageSize int) []*T {
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func (f *fakeRepo) InsertPO(_ context.Context, _ *gorm.DB, po *PurchaseOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	po.ID = database.ID(f.nextID())
	cp := *po
	f.pos[po.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) ReplacePOItems(_ context.Context, _ *gorm.DB, poID int64, items []*PurchaseOrderItem, by int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.poItems[poID] = nil
	for _, it := range items {
		cp := *it
		cp.POID = poID
		cp.ID = database.ID(f.nextID())
		cp.CreatedBy = database.ID(by)
		cp.UpdatedBy = database.ID(by)
		f.poItems[poID] = append(f.poItems[poID], &cp)
	}
	return nil
}

func (f *fakeRepo) UpdatePOCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	po, ok := f.pos[id]
	if !ok {
		return fmt.Errorf("采购订单 %d 不存在", id)
	}
	applyCols(po, cols)
	return nil
}

// statusTimeCols 状态迁移附带的环节时间列（与 GORM 实现 updateStatusGuarded 同语义，
// 内存替身仅记录状态与 approved_by，时间列置 now 不参与断言）。
func (f *fakeRepo) UpdatePOStatus(_ context.Context, _ *gorm.DB, id int64, from, to string, approvedBy int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	po, ok := f.pos[id]
	if !ok || po.Status != from {
		return 0, nil
	}
	po.Status = to
	if to == POStatusApproved {
		po.ApprovedBy = approvedBy
	}
	return 1, nil
}

func (f *fakeRepo) ListPOItemsTx(_ context.Context, _ *gorm.DB, poID int64) ([]*PurchaseOrderItem, error) {
	return f.ListPOItems(context.Background(), poID)
}

func (f *fakeRepo) ListPOItems(_ context.Context, poID int64) ([]*PurchaseOrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PurchaseOrderItem
	for _, it := range f.poItems[poID] {
		cp := *it
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepo) FindPOItemBySku(_ context.Context, poID, skuID int64) (*PurchaseOrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.poItems[poID] {
		if it.SKUID == skuID {
			cp := *it
			return &cp, nil
		}
	}
	return nil, nil
}

// AddPOItemReceived 数据层四量守卫：qty_received + recv + rej <= qty_ordered（§2.3）。
func (f *fakeRepo) AddPOItemReceived(_ context.Context, _ *gorm.DB, itemID int64, recv, rej stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, poItems := range f.poItems {
		for _, it := range poItems {
			if it.ID.Int64() != itemID {
				continue
			}
			if it.QtyReceived.Add(recv).Add(rej).Sub(it.QtyOrdered).IsPositive() {
				return 0, nil // 守卫未命中
			}
			it.QtyReceived = it.QtyReceived.Add(recv)
			it.QtyRejected = it.QtyRejected.Add(rej)
			it.UpdatedBy = database.ID(by)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) AddPOItemPutaway(_ context.Context, _ *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, poItems := range f.poItems {
		for _, it := range poItems {
			if it.ID.Int64() != itemID {
				continue
			}
			if it.QtyPutaway.Add(qty).Sub(it.QtyReceived).IsPositive() {
				return 0, nil
			}
			it.QtyPutaway = it.QtyPutaway.Add(qty)
			it.UpdatedBy = database.ID(by)
			return 1, nil
		}
	}
	return 0, nil
}

// ---- 入库单 ----

func (f *fakeRepo) FindInboundByID(_ context.Context, id int64) (*InboundOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.inbounds[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindInboundByNoTx(_ context.Context, _ *gorm.DB, no string) (*InboundOrder, error) {
	return f.FindInboundByNo(context.Background(), no)
}

func (f *fakeRepo) FindInboundByNo(_ context.Context, no string) (*InboundOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.inbounds {
		if o.InboundNo == no {
			cp := *o
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListInbounds(_ context.Context, fl InboundListFilter) ([]*InboundOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*InboundOrder
	for _, o := range f.inbounds {
		if !scopeMatch(o.WarehouseID, fl.Scope) {
			continue
		}
		if fl.Status != "" && o.Status != fl.Status {
			continue
		}
		if fl.SourceType != "" && o.SourceType != fl.SourceType {
			continue
		}
		if fl.SourceNo != "" && o.SourceNo != fl.SourceNo {
			continue
		}
		if fl.WarehouseID > 0 && o.WarehouseID != fl.WarehouseID {
			continue
		}
		cp := *o
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := int64(len(out))
	return pageSlice(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertInbound(_ context.Context, _ *gorm.DB, o *InboundOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o.ID = database.ID(f.nextID())
	cp := *o
	f.inbounds[o.ID.Int64()] = &cp
	return nil
}

func (f *fakeRepo) ReplaceInboundItems(_ context.Context, _ *gorm.DB, inboundID int64, items []*InboundItem, by int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inbItems[inboundID] = nil
	for _, it := range items {
		cp := *it
		cp.InboundID = inboundID
		cp.ID = database.ID(f.nextID())
		cp.CreatedBy = database.ID(by)
		cp.UpdatedBy = database.ID(by)
		f.inbItems[inboundID] = append(f.inbItems[inboundID], &cp)
	}
	return nil
}

func (f *fakeRepo) UpdateInboundCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.inbounds[id]
	if !ok {
		return fmt.Errorf("入库单 %d 不存在", id)
	}
	applyCols(o, cols)
	return nil
}

func (f *fakeRepo) UpdateInboundStatus(_ context.Context, _ *gorm.DB, id int64, from, to string, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.inbounds[id]
	if !ok || o.Status != from {
		return 0, nil
	}
	o.Status = to
	return 1, nil
}

func (f *fakeRepo) ListInboundItemsTx(_ context.Context, _ *gorm.DB, inboundID int64) ([]*InboundItem, error) {
	return f.ListInboundItems(context.Background(), inboundID)
}

func (f *fakeRepo) ListInboundItems(_ context.Context, inboundID int64) ([]*InboundItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*InboundItem
	for _, it := range f.inbItems[inboundID] {
		cp := *it
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepo) FindInboundItemBySku(_ context.Context, inboundID, skuID int64) (*InboundItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.inbItems[inboundID] {
		if it.SKUID == skuID {
			cp := *it
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) AddInboundItemReceived(_ context.Context, _ *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addInboundQty(itemID, delta, "qty_received", by)
}

func (f *fakeRepo) AddInboundItemInspected(_ context.Context, _ *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addInboundQty(itemID, delta, "qty_inspected", by)
}

func (f *fakeRepo) AddInboundItemPutaway(_ context.Context, _ *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addInboundQty(itemID, qty, "qty_putaway", by)
}

// addInboundQty 入库单明细累计守卫（与 GORM 条件 UPDATE 同语义）：
// qty_received ≤ qty；qty_inspected/qty_putaway ≤ qty_received。
func (f *fakeRepo) addInboundQty(itemID int64, delta stock.Qty, col string, by int64) (int64, error) {
	for _, items := range f.inbItems {
		for _, it := range items {
			if it.ID.Int64() != itemID {
				continue
			}
			var cap_ stock.Qty
			var cur stock.Qty
			switch col {
			case "qty_received":
				cap_, cur = it.Qty, it.QtyReceived
			case "qty_inspected":
				cap_, cur = it.QtyReceived, it.QtyInspected
			case "qty_putaway":
				cap_, cur = it.QtyReceived, it.QtyPutaway
			}
			if cur.Add(delta).Sub(cap_).IsPositive() {
				return 0, nil
			}
			switch col {
			case "qty_received":
				it.QtyReceived = it.QtyReceived.Add(delta)
			case "qty_inspected":
				it.QtyInspected = it.QtyInspected.Add(delta)
			case "qty_putaway":
				it.QtyPutaway = it.QtyPutaway.Add(delta)
			}
			it.UpdatedBy = database.ID(by)
			return 1, nil
		}
	}
	return 0, nil
}

// ---- 收货 ----

func (f *fakeRepo) FindReceiptByIdempotencyKey(_ context.Context, key string) (*Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.idemKeys[key]; ok {
		cp := *f.receipts[id]
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindReceiptByID(_ context.Context, id int64) (*Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.receipts[id]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindReceiptByNo(_ context.Context, no string) (*Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.receipts {
		if r.ReceiptNo == no {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListReceipts(_ context.Context, fl ReceiptListFilter) ([]*Receipt, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Receipt
	for _, r := range f.receipts {
		if !scopeMatch(r.WarehouseID, fl.Scope) {
			continue
		}
		if fl.InboundNo != "" && r.InboundNo != fl.InboundNo {
			continue
		}
		if fl.ReceiptNo != "" && r.ReceiptNo != fl.ReceiptNo {
			continue
		}
		if fl.WarehouseID > 0 && r.WarehouseID != fl.WarehouseID {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := int64(len(out))
	return pageSlice(out, fl.Page, fl.PageSize), total, nil
}

// InsertReceipt 幂等键唯一索引建模：同键重复插入返回 23505（uk_receipts_idempotency）。
func (f *fakeRepo) InsertReceipt(_ context.Context, _ *gorm.DB, r *Receipt, items []*ReceiptItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.IdempotencyKey != nil {
		if _, dup := f.idemKeys[*r.IdempotencyKey]; dup {
			return &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint",
				ConstraintName: "uk_receipts_idempotency"}
		}
	}
	r.ID = database.ID(f.nextReceiptID)
	f.nextReceiptID++
	cp := *r
	f.receipts[r.ID.Int64()] = &cp
	if r.IdempotencyKey != nil {
		f.idemKeys[*r.IdempotencyKey] = r.ID.Int64()
	}
	for _, it := range items {
		cpItem := *it
		cpItem.ReceiptID = r.ID.Int64()
		cpItem.ID = database.ID(f.nextID())
		f.rctItems[r.ID.Int64()] = append(f.rctItems[r.ID.Int64()], &cpItem)
	}
	return nil
}

func (f *fakeRepo) ListReceiptItems(_ context.Context, receiptID int64) ([]*ReceiptItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ReceiptItem
	for _, it := range f.rctItems[receiptID] {
		cp := *it
		out = append(out, &cp)
	}
	return out, nil
}

// ---- 质检 ----

func (f *fakeRepo) FindQCByID(_ context.Context, id int64) (*QualityOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q, ok := f.qcs[id]; ok {
		cp := *q
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindQCByNo(_ context.Context, no string) (*QualityOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.qcs {
		if q.QCNo == no {
			cp := *q
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListQCs(_ context.Context, fl QCListFilter) ([]*QualityOrder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*QualityOrder
	for _, q := range f.qcs {
		if !scopeMatch(q.WarehouseID, fl.Scope) {
			continue
		}
		if fl.Status != "" && q.Status != fl.Status {
			continue
		}
		if fl.SourceType != "" && q.SourceType != fl.SourceType {
			continue
		}
		if fl.SourceNo != "" && q.SourceNo != fl.SourceNo {
			continue
		}
		if fl.WarehouseID > 0 && q.WarehouseID != fl.WarehouseID {
			continue
		}
		cp := *q
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := int64(len(out))
	return pageSlice(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertQC(_ context.Context, _ *gorm.DB, q *QualityOrder, items []*QualityItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	q.ID = database.ID(f.nextID())
	cp := *q
	f.qcs[q.ID.Int64()] = &cp
	for _, it := range items {
		cpItem := *it
		cpItem.QCID = q.ID.Int64()
		cpItem.ID = database.ID(f.nextID())
		f.qcItems[q.ID.Int64()] = append(f.qcItems[q.ID.Int64()], &cpItem)
	}
	return nil
}

func (f *fakeRepo) UpdateQCCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.qcs[id]
	if !ok {
		return fmt.Errorf("质检单 %d 不存在", id)
	}
	applyCols(q, cols)
	return nil
}

func (f *fakeRepo) UpdateQCStatus(_ context.Context, _ *gorm.DB, id int64, from, to string, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.qcs[id]
	if !ok || q.Status != from {
		return 0, nil
	}
	q.Status = to
	return 1, nil
}

func (f *fakeRepo) ListQCItems(_ context.Context, qcID int64) ([]*QualityItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*QualityItem
	for _, it := range f.qcItems[qcID] {
		cp := *it
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepo) UpdateQCItemCols(_ context.Context, _ *gorm.DB, itemID int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, items := range f.qcItems {
		for _, it := range items {
			if it.ID.Int64() == itemID {
				applyCols(it, cols)
				return nil
			}
		}
	}
	return fmt.Errorf("质检明细 %d 不存在", itemID)
}

func (f *fakeRepo) ListCompletedQCLinesBySource(_ context.Context, sourceNo string) ([]*QualityItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*QualityItem
	for _, q := range f.qcs {
		if q.SourceNo != sourceNo || q.Status != QCStatusCompleted {
			continue
		}
		for _, it := range f.qcItems[q.ID.Int64()] {
			cp := *it
			out = append(out, &cp)
		}
	}
	return out, nil
}

// ---- 上架任务 ----

func (f *fakeRepo) FindTaskByID(_ context.Context, id int64) (*PutawayTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeRepo) FindTaskByNo(_ context.Context, no string) (*PutawayTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tasks {
		if t.PutawayNo == no {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListTasks(_ context.Context, fl TaskListFilter) ([]*PutawayTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PutawayTask
	for _, t := range f.tasks {
		if !scopeMatch(t.TargetWarehouseID, fl.Scope) {
			continue
		}
		if fl.Status != "" && t.Status != fl.Status {
			continue
		}
		if fl.InboundNo != "" && t.InboundNo != fl.InboundNo {
			continue
		}
		if fl.SKUID > 0 && t.SKUID != fl.SKUID {
			continue
		}
		if fl.FromState != "" && t.FromState != fl.FromState {
			continue
		}
		if fl.WarehouseID > 0 && t.TargetWarehouseID != fl.WarehouseID {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := int64(len(out))
	return pageSlice(out, fl.Page, fl.PageSize), total, nil
}

func (f *fakeRepo) InsertPutawayTasks(_ context.Context, _ *gorm.DB, tasks []*PutawayTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range tasks {
		t.ID = database.ID(f.nextID())
		cp := *t
		f.tasks[t.ID.Int64()] = &cp
	}
	return nil
}

// ClaimPutawayTask 原子抢占建模：claimGuard 通道模拟行锁串行化——并发领取
// 在判定区串行执行，第二个到达者看到 status != PENDING 返回 0 行。
func (f *fakeRepo) ClaimPutawayTask(_ context.Context, _ *gorm.DB, id, claimedBy int64) (int64, error) {
	f.claimGuard <- struct{}{}
	f.mu.Lock()
	defer func() {
		f.mu.Unlock()
		<-f.claimGuard
	}()
	t, ok := f.tasks[id]
	if !ok || t.Status != TaskStatusPending {
		return 0, nil
	}
	t.Status = TaskStatusInProgress
	t.ClaimedBy = claimedBy
	return 1, nil
}

func (f *fakeRepo) UpdatePutawayTaskCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return fmt.Errorf("上架任务 %d 不存在", id)
	}
	applyCols(t, cols)
	return nil
}

func (f *fakeRepo) UpdatePutawayTaskStatus(_ context.Context, _ *gorm.DB, id int64, from, to string, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || t.Status != from {
		return 0, nil
	}
	t.Status = to
	return 1, nil
}

// SetPutawayTaskPriority 优先级单列守卫更新建模（终态 COMPLETED/CANCELLED 0 行；
// 列承载 taskPriorities，updated_by 落列）。
func (f *fakeRepo) SetPutawayTaskPriority(_ context.Context, _ *gorm.DB, id int64, priority int, by int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || t.Status == TaskStatusCompleted || t.Status == TaskStatusCancelled {
		return 0, nil
	}
	t.UpdatedBy = database.ID(by)
	f.taskPriorities[id] = priority
	return 1, nil
}

func (f *fakeRepo) CountTasksByInboundTx(_ context.Context, _ *gorm.DB, inboundNo string) (map[string]int64, error) {
	return f.CountTasksByInbound(context.Background(), inboundNo)
}

func (f *fakeRepo) CountTasksByInbound(_ context.Context, inboundNo string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int64{}
	for _, t := range f.tasks {
		if t.InboundNo != inboundNo {
			continue
		}
		out[t.Status]++
	}
	return out, nil
}

func (f *fakeRepo) ListCompletedPendingTasksBySKU(_ context.Context, inboundNo string, skuID int64) ([]*PutawayTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PutawayTask
	for _, t := range f.tasks {
		if t.InboundNo != inboundNo || t.SKUID != skuID {
			continue
		}
		if t.FromState != FromStatePendingInspect || t.Status != TaskStatusCompleted {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TargetBinID != out[j].TargetBinID {
			return out[i].TargetBinID < out[j].TargetBinID
		}
		if out[i].BatchID != out[j].BatchID {
			return out[i].BatchID < out[j].BatchID
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ---- 审批记录 ----

func (f *fakeRepo) InsertApproval(_ context.Context, _ *gorm.DB, a *DocumentApproval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.ID = database.ID(f.nextID())
	cp := *a
	f.approvals = append(f.approvals, &cp)
	return nil
}

func (f *fakeRepo) approvalsFor(targetNo string) []*DocumentApproval {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*DocumentApproval
	for _, a := range f.approvals {
		if a.TargetNo == targetNo {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out
}

// applyCols map 列更新（替身内通用；仅接受各模型已知列，updated_* 由替身持有）。
func applyCols(m any, cols map[string]any) {
	switch t := m.(type) {
	case *PurchaseOrder:
		for k, v := range cols {
			switch k {
			case "supplier_id":
				t.SupplierID = v.(int64)
			case "warehouse_id":
				t.WarehouseID = v.(int64)
			case "remark":
				t.Remark = v.(string)
			}
		}
	case *InboundOrder:
		for k, v := range cols {
			switch k {
			case "remark":
				t.Remark = v.(string)
			}
		}
	case *QualityOrder:
		for k, v := range cols {
			switch k {
			case "qty_qualified":
				t.QtyQualified = v.(stock.Qty)
			case "qty_defective":
				t.QtyDefective = v.(stock.Qty)
			case "result":
				t.Result = v.(string)
			case "image_refs":
				t.ImageRefs = v.(StringList)
			case "inspector_id":
				t.InspectorID = v.(int64)
			case "inspector_name":
				t.InspectorName = v.(string)
			case "remark":
				t.Remark = v.(string)
			}
		}
	case *QualityItem:
		for k, v := range cols {
			switch k {
			case "qty_qualified":
				t.QtyQualified = v.(stock.Qty)
			case "qty_defective":
				t.QtyDefective = v.(stock.Qty)
			case "remark":
				t.Remark = v.(string)
			}
		}
	case *PutawayTask:
		for k, v := range cols {
			switch k {
			case "target_bin_id":
				t.TargetBinID = v.(int64)
			case "target_zone_id":
				t.TargetZoneID = v.(int64)
			case "target_shelf_id":
				t.TargetShelfID = v.(int64)
			}
		}
	}
}
