package purchase

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/stock"
)

// Repository 消费方窄接口（M1 masterdata 同款分层：Service 只依赖本接口，
// 单元测试以内存替身实现——ask 约束不依赖 PostgreSQL）。
// 约束：repository 无业务判断（architecture.md §1），只做数据访问；
// 数量累计与状态迁移均以"条件 UPDATE + 影响行数判定"落地（plan §6/§10，
// 状态守卫 WHERE status=<前置态> 与四量守卫 WHERE 累计 <= 上限 是数据层最后防线）。
type Repository interface {
	DB() *gorm.DB

	// —— 采购订单 ——
	FindPOByID(ctx context.Context, id int64) (*PurchaseOrder, error)
	FindPOByIDForUpdate(ctx context.Context, tx *gorm.DB, id int64) (*PurchaseOrder, error)
	FindPOByNo(ctx context.Context, poNo string) (*PurchaseOrder, error)
	FindPOByNoTx(ctx context.Context, tx *gorm.DB, poNo string) (*PurchaseOrder, error)
	ListPOs(ctx context.Context, f POListFilter) ([]*PurchaseOrder, int64, error)
	InsertPO(ctx context.Context, tx *gorm.DB, po *PurchaseOrder) error
	ReplacePOItems(ctx context.Context, tx *gorm.DB, poID int64, items []*PurchaseOrderItem, by int64) error
	UpdatePOCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	UpdatePOStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, approvedBy int64) (int64, error)
	ListPOItems(ctx context.Context, poID int64) ([]*PurchaseOrderItem, error)
	// ListPOItemsTx 事务内读明细：progressPOAfterReceipt 在同事务累计 PO 明细
	// qty_received 后判定推进，必须经 tx 读（ListInboundItemsTx 同理）。
	ListPOItemsTx(ctx context.Context, tx *gorm.DB, poID int64) ([]*PurchaseOrderItem, error)
	FindPOItemBySku(ctx context.Context, poID, skuID int64) (*PurchaseOrderItem, error)
	AddPOItemReceived(ctx context.Context, tx *gorm.DB, itemID int64, recv, rej stock.Qty, by int64) (int64, error)
	AddPOItemPutaway(ctx context.Context, tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error)

	// —— 入库单 ——
	FindInboundByID(ctx context.Context, id int64) (*InboundOrder, error)
	FindInboundByNo(ctx context.Context, no string) (*InboundOrder, error)
	FindInboundByNoTx(ctx context.Context, tx *gorm.DB, no string) (*InboundOrder, error)
	ListInbounds(ctx context.Context, f InboundListFilter) ([]*InboundOrder, int64, error)
	InsertInbound(ctx context.Context, tx *gorm.DB, o *InboundOrder) error
	ReplaceInboundItems(ctx context.Context, tx *gorm.DB, inboundID int64, items []*InboundItem, by int64) error
	UpdateInboundCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	UpdateInboundStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error)
	ListInboundItems(ctx context.Context, inboundID int64) ([]*InboundItem, error)
	// ListInboundItemsTx 事务内读明细：状态推进判定（progressAfterReceipt /
	// progressInboundAfterQC）在同事务累计 qty_received / qty_inspected 后必须经
	// tx 读取本事务未提交写入——r.db 连接 READ COMMITTED 下只见旧值，收齐/质检
	// 完成判定恒 false（2026-10-07 链路实测修复）。
	ListInboundItemsTx(ctx context.Context, tx *gorm.DB, inboundID int64) ([]*InboundItem, error)
	FindInboundItemBySku(ctx context.Context, inboundID, skuID int64) (*InboundItem, error)
	AddInboundItemReceived(ctx context.Context, tx *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error)
	AddInboundItemInspected(ctx context.Context, tx *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error)
	AddInboundItemPutaway(ctx context.Context, tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error)

	// —— 收货（事件型，无状态机）——
	FindReceiptByIdempotencyKey(ctx context.Context, key string) (*Receipt, error) // 未命中 (nil, nil)
	FindReceiptByID(ctx context.Context, id int64) (*Receipt, error)
	FindReceiptByNo(ctx context.Context, no string) (*Receipt, error)
	ListReceipts(ctx context.Context, f ReceiptListFilter) ([]*Receipt, int64, error)
	InsertReceipt(ctx context.Context, tx *gorm.DB, r *Receipt, items []*ReceiptItem) error
	ListReceiptItems(ctx context.Context, receiptID int64) ([]*ReceiptItem, error)

	// —— 质检 ——
	FindQCByID(ctx context.Context, id int64) (*QualityOrder, error)
	FindQCByNo(ctx context.Context, no string) (*QualityOrder, error)
	ListQCs(ctx context.Context, f QCListFilter) ([]*QualityOrder, int64, error)
	InsertQC(ctx context.Context, tx *gorm.DB, q *QualityOrder, items []*QualityItem) error
	UpdateQCCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	UpdateQCStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error)
	ListQCItems(ctx context.Context, qcID int64) ([]*QualityItem, error)
	UpdateQCItemCols(ctx context.Context, tx *gorm.DB, itemID int64, cols map[string]any) error
	ListCompletedQCLinesBySource(ctx context.Context, sourceNo string) ([]*QualityItem, error)

	// —— 上架任务 ——
	FindTaskByID(ctx context.Context, id int64) (*PutawayTask, error)
	FindTaskByNo(ctx context.Context, no string) (*PutawayTask, error)
	ListTasks(ctx context.Context, f TaskListFilter) ([]*PutawayTask, int64, error)
	InsertPutawayTasks(ctx context.Context, tx *gorm.DB, tasks []*PutawayTask) error
	ClaimPutawayTask(ctx context.Context, tx *gorm.DB, id, claimedBy int64) (int64, error)
	UpdatePutawayTaskCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	UpdatePutawayTaskStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error)
	// SetPutawayTaskPriority 任务优先级单列守卫更新（效率层一期 B3：终态 0 行；
	// 迁移 000023 chk_putaway_tasks_priority CHECK 0-9 兜底）。
	SetPutawayTaskPriority(ctx context.Context, tx *gorm.DB, id int64, priority int, by int64) (int64, error)
	CountTasksByInbound(ctx context.Context, inboundNo string) (map[string]int64, error)
	// CountTasksByInboundTx 事务内读任务计数：progressInboundAfterTask 在同事务
	// 完成当前任务（IN_PROGRESS→COMPLETED）后判定全部完成，必须经 tx 读。
	CountTasksByInboundTx(ctx context.Context, tx *gorm.DB, inboundNo string) (map[string]int64, error)
	ListCompletedPendingTasksBySKU(ctx context.Context, inboundNo string, skuID int64) ([]*PutawayTask, error)

	// —— 审批记录（000006 共享表，append-only）——
	InsertApproval(ctx context.Context, tx *gorm.DB, a *DocumentApproval) error
}

// WarehouseScope 数据权限仓库范围（permission.md §4；handler 自 auth.WarehouseScope 取值传入）。
type WarehouseScope struct {
	All bool
	IDs []int64
}

// visible 仓库是否在数据权限范围内（permission.md §4 仓库维度隔离；详情接口
// fail-closed 判定——非 ALL 且 ID 不在集合内 = 不可见，按不存在处理）。
func (sc WarehouseScope) visible(warehouseID int64) bool {
	if sc.All {
		return true
	}
	for _, id := range sc.IDs {
		if id == warehouseID {
			return true
		}
	}
	return false
}

// applyScope 按仓库范围注入过滤（fail-closed：非 ALL 且空集 = 不可见任何行）。
func applyScope(q *gorm.DB, column string, sc WarehouseScope) *gorm.DB {
	if sc.All {
		return q
	}
	if len(sc.IDs) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where(column+" IN ?", sc.IDs)
}

// POListFilter 采购订单列表筛选（分页强制，architecture.md §7）。
type POListFilter struct {
	Keyword     string // po_no / remark 模糊
	Status      string
	SupplierID  int64
	WarehouseID int64
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// InboundListFilter 入库单列表筛选。
type InboundListFilter struct {
	Keyword     string // inbound_no / source_no
	Status      string
	SourceType  string
	SourceNo    string
	WarehouseID int64
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// ReceiptListFilter 收货记录列表筛选。
type ReceiptListFilter struct {
	InboundNo string
	ReceiptNo string
	// PoNo 来源采购单号：经 inbound_orders.source_no 关联（一张 PO 1:N 张入库单，
	// 其下全部收货记录——frontend.md §33.4 采购详情「收货记录」chip 承载）。
	PoNo        string
	WarehouseID int64
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// QCListFilter 质检单列表筛选。
type QCListFilter struct {
	Keyword     string // qc_no / source_no
	Status      string
	SourceType  string
	SourceNo    string
	WarehouseID int64
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// TaskListFilter 上架任务列表筛选。
type TaskListFilter struct {
	Keyword     string // putaway_no / inbound_no
	Status      string
	InboundNo   string
	SKUID       int64
	ClaimedBy   int64
	FromState   string
	WarehouseID int64
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// repo GORM 实现（NewRepository 装配入口）。
type repo struct{ db *gorm.DB }

// NewRepository 构建 GORM Repository。
func NewRepository(db *gorm.DB) Repository { return &repo{db: db} }

func (r *repo) DB() *gorm.DB { return r.db }

func withCtx(ctx context.Context, q *gorm.DB) *gorm.DB { return q.WithContext(ctx) }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// paginate 追加稳定排序与分页（architecture.md §7：列表强制分页）。
func paginate[T any](q *gorm.DB, page, pageSize int) ([]*T, int64, error) {
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*T, 0, pageSize)
	err := q.Order("id ASC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error
	return items, total, err
}

// firstOrNil 查询未命中返回 (nil, nil)；其他错误原样上抛。
func firstOrNil[T any](row *T, err error, what string) (*T, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapDB(err, what)
	}
	return row, nil
}

func wrapDB(err error, what string) error {
	if err == nil {
		return nil
	}
	return errors.New(what + "失败: " + err.Error())
}

// likeEscape 转义 ILIKE 通配符（用户输入不扰动匹配范围，M1 同款）。
func likeEscape(kw string) string {
	kw = strings.TrimSpace(kw)
	kw = strings.ReplaceAll(kw, "\\", "\\\\")
	kw = strings.ReplaceAll(kw, "%", "\\%")
	kw = strings.ReplaceAll(kw, "_", "\\_")
	return kw
}

// guardMiss 条件更新未命中的哨兵错误（数据层守卫 0 行；服务层据此转换为业务错误码）。
var guardMiss = errors.New("purchase: 条件更新未命中（守卫失败）")

// updateStatusGuarded 单据状态守卫迁移（plan §6 统一实现形态）：
// UPDATE ... SET status=<目标态>, <环节时间列>=now() WHERE id=? AND status=<前置态>，
// 影响行数 0 → guardMiss（服务层转 ErrStatusConflict / ErrXxxStatusNotAllowed）。
// 时间列按目标态映射（business-flow §13.4 各环节时间字段）。
func updateStatusGuarded(tx *gorm.DB, model any, id int64, from, to string, timeCols map[string][]string, extra map[string]any, by int64) (int64, error) {
	sets := map[string]any{"status": to, "updated_at": database.Now(), "updated_by": by}
	for _, col := range timeCols[to] {
		sets[col] = database.Now()
	}
	for k, v := range extra {
		sets[k] = v
	}
	q := tx.Model(model).Where("id = ? AND status = ?", id, from).Updates(sets)
	if q.Error != nil {
		return 0, q.Error
	}
	return q.RowsAffected, nil
}

// addQtyGuarded 累计数量守卫更新：cols 为 SET 表达式，guard 为附加 WHERE 条件（可空）。
func addQtyGuarded(tx *gorm.DB, model any, itemID int64, sets map[string]any, guard string, args []any, by int64) (int64, error) {
	sets["updated_at"] = database.Now()
	sets["updated_by"] = by
	q := tx.Model(model).Where("id = ?", itemID)
	if guard != "" {
		q = q.Where(guard, args...)
	}
	q = q.Updates(sets)
	if q.Error != nil {
		return 0, q.Error
	}
	return q.RowsAffected, nil
}

// ---- 采购订单 ----

func (r *repo) FindPOByID(ctx context.Context, id int64) (*PurchaseOrder, error) {
	var row PurchaseOrder
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "查询采购订单")
}

// FindPOByIDForUpdate 事务内行锁重读采购订单（SELECT ... FOR UPDATE）：
// 取消/收货互斥的串行化锚点——收货事务持锁期间取消事务的状态更新等待行锁，
// 其守卫 UPDATE（WHERE status=from）在锁释放后重新评估，不会把已推进/已取消的
// 单据再次迁移；反之取消先提交时收货事务在锁内立即读到终态并拒绝（并发 #修复轮）。
func (r *repo) FindPOByIDForUpdate(ctx context.Context, tx *gorm.DB, id int64) (*PurchaseOrder, error) {
	var row PurchaseOrder
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "行锁查询采购订单")
}

func (r *repo) FindPOByNo(ctx context.Context, poNo string) (*PurchaseOrder, error) {
	var row PurchaseOrder
	err := withCtx(ctx, r.db).Where("po_no = ?", poNo).First(&row).Error
	return firstOrNil(&row, err, "按单号查询采购订单")
}

// FindPOByNoTx 事务内读：收货确认 tx 内回读最终状态用。
func (r *repo) FindPOByNoTx(ctx context.Context, tx *gorm.DB, poNo string) (*PurchaseOrder, error) {
	var row PurchaseOrder
	err := tx.WithContext(ctx).Where("po_no = ?", poNo).First(&row).Error
	return firstOrNil(&row, err, "按单号查询采购订单")
}

func (r *repo) ListPOs(ctx context.Context, f POListFilter) ([]*PurchaseOrder, int64, error) {
	q := applyScope(withCtx(ctx, r.db).Model(&PurchaseOrder{}), "warehouse_id", f.Scope)
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("po_no ILIKE ?", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.SupplierID > 0 {
		q = q.Where("supplier_id = ?", f.SupplierID)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	return paginate[PurchaseOrder](q, f.Page, f.PageSize)
}

func (r *repo) InsertPO(ctx context.Context, tx *gorm.DB, po *PurchaseOrder) error {
	return wrapDB(tx.WithContext(ctx).Create(po).Error, "写入采购订单")
}

// ReplacePOItems 整单替换明细。必须 Unscoped 硬删：BaseModel 软删行物理保留，
// 会继续占用 uk_purchase_order_items_po_line (po_id, line_no) 唯一索引，重插同行号
// 即 23505（草稿编辑必现内部错误）。调用面仅 create + 仅草稿可改的 update——行无
// 收货/上架历史，硬删不丢业务事实（stockops ReplaceTransferItems raw DELETE 同口径）。
func (r *repo) ReplacePOItems(ctx context.Context, tx *gorm.DB, poID int64, items []*PurchaseOrderItem, by int64) error {
	if err := tx.WithContext(ctx).Unscoped().Where("po_id = ?", poID).Delete(&PurchaseOrderItem{}).Error; err != nil {
		return wrapDB(err, "清理采购订单明细")
	}
	for _, it := range items {
		it.POID = poID
		it.CreatedBy = database.ID(by)
		it.UpdatedBy = database.ID(by)
		if err := tx.WithContext(ctx).Create(it).Error; err != nil {
			return wrapDB(err, "写入采购订单明细")
		}
	}
	return nil
}

func (r *repo) UpdatePOCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return wrapDB(tx.WithContext(ctx).Model(&PurchaseOrder{}).Where("id = ?", id).Updates(cols).Error, "更新采购订单")
}

func (r *repo) UpdatePOStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, approvedBy int64) (int64, error) {
	timeCols := map[string][]string{
		POStatusApproved:    {"approved_at"},
		POStatusReceivedAll: {"received_at"},
		POStatusCompleted:   {"completed_at"},
		POStatusCancelled:   {"cancelled_at"},
	}
	extra := map[string]any(nil)
	if to == POStatusApproved {
		extra = map[string]any{"approved_by": approvedBy}
	}
	return updateStatusGuarded(tx.WithContext(ctx), &PurchaseOrder{}, id, from, to, timeCols, extra, approvedBy)
}

func (r *repo) ListPOItems(ctx context.Context, poID int64) ([]*PurchaseOrderItem, error) {
	return r.listPOItems(withCtx(ctx, r.db), poID)
}

func (r *repo) ListPOItemsTx(ctx context.Context, tx *gorm.DB, poID int64) ([]*PurchaseOrderItem, error) {
	return r.listPOItems(tx.WithContext(ctx), poID)
}

func (r *repo) listPOItems(q *gorm.DB, poID int64) ([]*PurchaseOrderItem, error) {
	var rows []*PurchaseOrderItem
	err := q.Where("po_id = ?", poID).Order("line_no ASC").Find(&rows).Error
	return rows, wrapDB(err, "查询采购订单明细")
}

func (r *repo) FindPOItemBySku(ctx context.Context, poID, skuID int64) (*PurchaseOrderItem, error) {
	var row PurchaseOrderItem
	err := withCtx(ctx, r.db).Where("po_id = ? AND sku_id = ?", poID, skuID).First(&row).Error
	return firstOrNil(&row, err, "按 SKU 查询采购订单明细")
}

func (r *repo) AddPOItemReceived(ctx context.Context, tx *gorm.DB, itemID int64, recv, rej stock.Qty, by int64) (int64, error) {
	return addQtyGuarded(tx.WithContext(ctx), &PurchaseOrderItem{}, itemID, map[string]any{
		"qty_received": gorm.Expr("qty_received + ?", recv),
		"qty_rejected": gorm.Expr("qty_rejected + ?", rej),
	},
		"qty_received + ? + ? <= qty_ordered", []any{recv, rej}, by)
}

func (r *repo) AddPOItemPutaway(ctx context.Context, tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	return addQtyGuarded(tx.WithContext(ctx), &PurchaseOrderItem{}, itemID, map[string]any{
		"qty_putaway": gorm.Expr("qty_putaway + ?", qty),
	},
		"qty_putaway + ? <= qty_received", []any{qty}, by)
}

// ---- 入库单 ----

func (r *repo) FindInboundByID(ctx context.Context, id int64) (*InboundOrder, error) {
	var row InboundOrder
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "查询入库单")
}

func (r *repo) FindInboundByNo(ctx context.Context, no string) (*InboundOrder, error) {
	var row InboundOrder
	err := withCtx(ctx, r.db).Where("inbound_no = ?", no).First(&row).Error
	return firstOrNil(&row, err, "按单号查询入库单")
}

// FindInboundByNoTx 事务内读：收货确认 tx 内回读最终状态用（r.db 只见提交前旧值）。
func (r *repo) FindInboundByNoTx(ctx context.Context, tx *gorm.DB, no string) (*InboundOrder, error) {
	var row InboundOrder
	err := tx.WithContext(ctx).Where("inbound_no = ?", no).First(&row).Error
	return firstOrNil(&row, err, "按单号查询入库单")
}

func (r *repo) ListInbounds(ctx context.Context, f InboundListFilter) ([]*InboundOrder, int64, error) {
	q := applyScope(withCtx(ctx, r.db).Model(&InboundOrder{}), "warehouse_id", f.Scope)
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("inbound_no ILIKE ? OR source_no ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.SourceType != "" {
		q = q.Where("source_type = ?", f.SourceType)
	}
	if f.SourceNo != "" {
		q = q.Where("source_no = ?", f.SourceNo)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	return paginate[InboundOrder](q, f.Page, f.PageSize)
}

func (r *repo) InsertInbound(ctx context.Context, tx *gorm.DB, o *InboundOrder) error {
	return wrapDB(tx.WithContext(ctx).Create(o).Error, "写入入库单")
}

// ReplaceInboundItems 整单替换明细。必须 Unscoped 硬删：软删行物理保留会占用
// uk_inbound_items_inbound_line (inbound_id, line_no) 唯一索引，重插同行号即 23505
// （仅草稿可改，行无收货历史——ReplacePOItems 同口径注释）。
func (r *repo) ReplaceInboundItems(ctx context.Context, tx *gorm.DB, inboundID int64, items []*InboundItem, by int64) error {
	if err := tx.WithContext(ctx).Unscoped().Where("inbound_id = ?", inboundID).Delete(&InboundItem{}).Error; err != nil {
		return wrapDB(err, "清理入库单明细")
	}
	for _, it := range items {
		it.InboundID = inboundID
		it.CreatedBy = database.ID(by)
		it.UpdatedBy = database.ID(by)
		if err := tx.WithContext(ctx).Create(it).Error; err != nil {
			return wrapDB(err, "写入入库单明细")
		}
	}
	return nil
}

func (r *repo) UpdateInboundCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return wrapDB(tx.WithContext(ctx).Model(&InboundOrder{}).Where("id = ?", id).Updates(cols).Error, "更新入库单")
}

func (r *repo) UpdateInboundStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	timeCols := map[string][]string{
		InboundStatusAwaitingQC:      {"received_at"},
		InboundStatusAwaitingPutaway: {"inspected_at"},
		InboundStatusCompleted:       {"putaway_at", "completed_at"},
		InboundStatusCancelled:       {"cancelled_at"},
	}
	return updateStatusGuarded(tx.WithContext(ctx), &InboundOrder{}, id, from, to, timeCols, nil, by)
}

func (r *repo) ListInboundItems(ctx context.Context, inboundID int64) ([]*InboundItem, error) {
	return r.listInboundItems(withCtx(ctx, r.db), inboundID)
}

func (r *repo) ListInboundItemsTx(ctx context.Context, tx *gorm.DB, inboundID int64) ([]*InboundItem, error) {
	return r.listInboundItems(tx.WithContext(ctx), inboundID)
}

func (r *repo) listInboundItems(q *gorm.DB, inboundID int64) ([]*InboundItem, error) {
	var rows []*InboundItem
	err := q.Where("inbound_id = ?", inboundID).Order("line_no ASC").Find(&rows).Error
	return rows, wrapDB(err, "查询入库单明细")
}

func (r *repo) FindInboundItemBySku(ctx context.Context, inboundID, skuID int64) (*InboundItem, error) {
	var row InboundItem
	err := withCtx(ctx, r.db).Where("inbound_id = ? AND sku_id = ?", inboundID, skuID).First(&row).Error
	return firstOrNil(&row, err, "按 SKU 查询入库单明细")
}

func (r *repo) AddInboundItemReceived(ctx context.Context, tx *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error) {
	return addQtyGuarded(tx.WithContext(ctx), &InboundItem{}, itemID, map[string]any{
		"qty_received": gorm.Expr("qty_received + ?", delta),
	},
		"qty_received + ? <= qty", []any{delta}, by)
}

func (r *repo) AddInboundItemInspected(ctx context.Context, tx *gorm.DB, itemID int64, delta stock.Qty, by int64) (int64, error) {
	return addQtyGuarded(tx.WithContext(ctx), &InboundItem{}, itemID, map[string]any{
		"qty_inspected": gorm.Expr("qty_inspected + ?", delta),
	},
		"qty_inspected + ? <= qty_received", []any{delta}, by)
}

func (r *repo) AddInboundItemPutaway(ctx context.Context, tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	return addQtyGuarded(tx.WithContext(ctx), &InboundItem{}, itemID, map[string]any{
		"qty_putaway": gorm.Expr("qty_putaway + ?", qty),
	},
		"qty_putaway + ? <= qty_received", []any{qty}, by)
}

// ---- 收货 ----

func (r *repo) FindReceiptByIdempotencyKey(ctx context.Context, key string) (*Receipt, error) {
	var row Receipt
	err := withCtx(ctx, r.db).Where("idempotency_key = ?", key).First(&row).Error
	return firstOrNil(&row, err, "按幂等键查询收货记录")
}

func (r *repo) FindReceiptByID(ctx context.Context, id int64) (*Receipt, error) {
	var row Receipt
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "查询收货记录")
}

func (r *repo) FindReceiptByNo(ctx context.Context, no string) (*Receipt, error) {
	var row Receipt
	err := withCtx(ctx, r.db).Where("receipt_no = ?", no).First(&row).Error
	return firstOrNil(&row, err, "按单号查询收货记录")
}

func (r *repo) ListReceipts(ctx context.Context, f ReceiptListFilter) ([]*Receipt, int64, error) {
	// PoNo 走 JOIN（见下），warehouse_id 条件限定表名防歧义
	q := applyScope(withCtx(ctx, r.db).Model(&Receipt{}), "receipts.warehouse_id", f.Scope)
	if f.InboundNo != "" {
		q = q.Where("inbound_no = ?", f.InboundNo)
	}
	if f.PoNo != "" {
		// 一张 PO 派生 1:N 张入库单：经入库单来源单号关联取其下全部收货记录
		// （inbound_no 在 inbound_orders 唯一，JOIN 不产生重复行）
		q = q.Joins("JOIN inbound_orders io ON io.inbound_no = receipts.inbound_no AND io.deleted_at IS NULL").
			Where("io.source_no = ?", f.PoNo)
	}
	if f.ReceiptNo != "" {
		q = q.Where("receipt_no = ?", f.ReceiptNo)
	}
	if f.WarehouseID > 0 {
		q = q.Where("receipts.warehouse_id = ?", f.WarehouseID)
	}
	return paginate[Receipt](q, f.Page, f.PageSize)
}

func (r *repo) InsertReceipt(ctx context.Context, tx *gorm.DB, rc *Receipt, items []*ReceiptItem) error {
	if err := tx.WithContext(ctx).Create(rc).Error; err != nil {
		return wrapDB(err, "写入收货记录")
	}
	for _, it := range items {
		it.ReceiptID = rc.ID.Int64()
		it.CreatedBy = rc.CreatedBy
		it.UpdatedBy = rc.UpdatedBy
		if err := tx.WithContext(ctx).Create(it).Error; err != nil {
			return wrapDB(err, "写入收货明细")
		}
	}
	return nil
}

func (r *repo) ListReceiptItems(ctx context.Context, receiptID int64) ([]*ReceiptItem, error) {
	var rows []*ReceiptItem
	err := withCtx(ctx, r.db).Where("receipt_id = ?", receiptID).Order("line_no ASC").Find(&rows).Error
	return rows, wrapDB(err, "查询收货明细")
}

// ---- 质检 ----

func (r *repo) FindQCByID(ctx context.Context, id int64) (*QualityOrder, error) {
	var row QualityOrder
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "查询质检单")
}

func (r *repo) FindQCByNo(ctx context.Context, no string) (*QualityOrder, error) {
	var row QualityOrder
	err := withCtx(ctx, r.db).Where("qc_no = ?", no).First(&row).Error
	return firstOrNil(&row, err, "按单号查询质检单")
}

func (r *repo) ListQCs(ctx context.Context, f QCListFilter) ([]*QualityOrder, int64, error) {
	q := applyScope(withCtx(ctx, r.db).Model(&QualityOrder{}), "warehouse_id", f.Scope)
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("qc_no ILIKE ? OR source_no ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.SourceType != "" {
		q = q.Where("source_type = ?", f.SourceType)
	}
	if f.SourceNo != "" {
		q = q.Where("source_no = ?", f.SourceNo)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	return paginate[QualityOrder](q, f.Page, f.PageSize)
}

func (r *repo) InsertQC(ctx context.Context, tx *gorm.DB, q *QualityOrder, items []*QualityItem) error {
	if err := tx.WithContext(ctx).Create(q).Error; err != nil {
		return wrapDB(err, "写入质检单")
	}
	for _, it := range items {
		it.QCID = q.ID.Int64()
		it.CreatedBy = q.CreatedBy
		it.UpdatedBy = q.UpdatedBy
		if err := tx.WithContext(ctx).Create(it).Error; err != nil {
			return wrapDB(err, "写入质检明细")
		}
	}
	return nil
}

func (r *repo) UpdateQCCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return wrapDB(tx.WithContext(ctx).Model(&QualityOrder{}).Where("id = ?", id).Updates(cols).Error, "更新质检单")
}

func (r *repo) UpdateQCStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	timeCols := map[string][]string{
		QCStatusCompleted: {"inspected_at"},
	}
	return updateStatusGuarded(tx.WithContext(ctx), &QualityOrder{}, id, from, to, timeCols, nil, by)
}

func (r *repo) ListQCItems(ctx context.Context, qcID int64) ([]*QualityItem, error) {
	var rows []*QualityItem
	err := withCtx(ctx, r.db).Where("qc_id = ?", qcID).Order("line_no ASC").Find(&rows).Error
	return rows, wrapDB(err, "查询质检明细")
}

func (r *repo) UpdateQCItemCols(ctx context.Context, tx *gorm.DB, itemID int64, cols map[string]any) error {
	return wrapDB(tx.WithContext(ctx).Model(&QualityItem{}).Where("id = ?", itemID).Updates(cols).Error, "更新质检明细")
}

// ListCompletedQCLinesBySource 已完成质检单的明细行（质检结果汇总/上架排程数据面）。
func (r *repo) ListCompletedQCLinesBySource(ctx context.Context, sourceNo string) ([]*QualityItem, error) {
	var rows []*QualityItem
	err := withCtx(ctx, r.db).Raw(`
		SELECT qi.id, qi.qc_id, qi.line_no, qi.sku_id, qi.batch_no,
		       qi.qty_inspected, qi.qty_qualified, qi.qty_defective,
		       qi.remark, qi.created_at, qi.updated_at, qi.created_by, qi.updated_by
		FROM quality_items qi
		JOIN quality_orders qo ON qo.id = qi.qc_id
		WHERE qo.source_no = ? AND qo.status = ?
		ORDER BY qo.id ASC, qi.line_no ASC`, sourceNo, QCStatusCompleted).Scan(&rows).Error
	return rows, wrapDB(err, "查询已完成质检明细")
}

// ---- 上架任务 ----

func (r *repo) FindTaskByID(ctx context.Context, id int64) (*PutawayTask, error) {
	var row PutawayTask
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&row).Error
	return firstOrNil(&row, err, "查询上架任务")
}

func (r *repo) FindTaskByNo(ctx context.Context, no string) (*PutawayTask, error) {
	var row PutawayTask
	err := withCtx(ctx, r.db).Where("putaway_no = ?", no).First(&row).Error
	return firstOrNil(&row, err, "按单号查询上架任务")
}

func (r *repo) ListTasks(ctx context.Context, f TaskListFilter) ([]*PutawayTask, int64, error) {
	q := applyScope(withCtx(ctx, r.db).Model(&PutawayTask{}), "target_warehouse_id", f.Scope)
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("putaway_no ILIKE ? OR inbound_no ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.InboundNo != "" {
		q = q.Where("inbound_no = ?", f.InboundNo)
	}
	if f.SKUID > 0 {
		q = q.Where("sku_id = ?", f.SKUID)
	}
	if f.ClaimedBy > 0 {
		q = q.Where("claimed_by = ?", f.ClaimedBy)
	}
	if f.FromState != "" {
		q = q.Where("from_state = ?", f.FromState)
	}
	if f.WarehouseID > 0 {
		q = q.Where("target_warehouse_id = ?", f.WarehouseID)
	}
	return paginate[PutawayTask](q, f.Page, f.PageSize)
}

func (r *repo) InsertPutawayTasks(ctx context.Context, tx *gorm.DB, tasks []*PutawayTask) error {
	for _, t := range tasks {
		if err := tx.WithContext(ctx).Create(t).Error; err != nil {
			return wrapDB(err, "写入上架任务")
		}
	}
	return nil
}

// ClaimPutawayTask 原子抢占（architecture §5.2、plan §6.3）：
// UPDATE ... SET claimed_by=?, claimed_at=now(), status='IN_PROGRESS'
// WHERE id=? AND status='PENDING'——影响行数 0 = 领取冲突。
func (r *repo) ClaimPutawayTask(ctx context.Context, tx *gorm.DB, id, claimedBy int64) (int64, error) {
	q := tx.WithContext(ctx).Model(&PutawayTask{}).
		Where("id = ? AND status = ?", id, TaskStatusPending).
		Updates(map[string]any{
			"status":     TaskStatusInProgress,
			"claimed_by": claimedBy,
			"claimed_at": database.Now(),
			"updated_at": database.Now(),
			"updated_by": claimedBy,
		})
	if q.Error != nil {
		return 0, q.Error
	}
	return q.RowsAffected, nil
}

func (r *repo) UpdatePutawayTaskCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	return wrapDB(tx.WithContext(ctx).Model(&PutawayTask{}).Where("id = ?", id).Updates(cols).Error, "更新上架任务")
}

func (r *repo) UpdatePutawayTaskStatus(ctx context.Context, tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	timeCols := map[string][]string{
		TaskStatusCompleted: {"completed_at"},
	}
	return updateStatusGuarded(tx.WithContext(ctx), &PutawayTask{}, id, from, to, timeCols, nil, by)
}

// SetPutawayTaskPriority 任务优先级单列守卫更新（效率层一期 B3：终态 COMPLETED/CANCELLED
// 拒绝 0 行；值域 0-9 由迁移 000023 chk_putaway_tasks_priority CHECK 兜底）。
func (r *repo) SetPutawayTaskPriority(ctx context.Context, tx *gorm.DB, id int64, priority int, by int64) (int64, error) {
	q := tx.WithContext(ctx).Model(&PutawayTask{}).
		Where("id = ? AND status NOT IN (?, ?)", id, TaskStatusCompleted, TaskStatusCancelled).
		Updates(map[string]any{
			"priority":   priority,
			"updated_at": database.Now(),
			"updated_by": by,
		})
	if q.Error != nil {
		return 0, q.Error
	}
	return q.RowsAffected, nil
}

func (r *repo) CountTasksByInbound(ctx context.Context, inboundNo string) (map[string]int64, error) {
	return r.countTasksByInbound(withCtx(ctx, r.db), inboundNo)
}

func (r *repo) CountTasksByInboundTx(ctx context.Context, tx *gorm.DB, inboundNo string) (map[string]int64, error) {
	return r.countTasksByInbound(tx.WithContext(ctx), inboundNo)
}

func (r *repo) countTasksByInbound(q *gorm.DB, inboundNo string) (map[string]int64, error) {
	var rows []struct {
		Status string `gorm:"column:status"`
		N      int64  `gorm:"column:n"`
	}
	err := q.Raw(`
		SELECT status, COUNT(*) AS n FROM putaway_tasks WHERE inbound_no = ? GROUP BY status`,
		inboundNo).Scan(&rows).Error
	if err != nil {
		return nil, wrapDB(err, "统计上架任务状态")
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.N
	}
	return out, nil
}

// ListCompletedPendingTasksBySKU 已完成、入待检（from_state=pending_inspect）的任务——
// 质检执行事务定位待检库存所在库位的数据面（InspectResult 的 RowKey 来源）。
func (r *repo) ListCompletedPendingTasksBySKU(ctx context.Context, inboundNo string, skuID int64) ([]*PutawayTask, error) {
	var rows []*PutawayTask
	err := withCtx(ctx, r.db).Where(
		"inbound_no = ? AND sku_id = ? AND from_state = ? AND status = ?",
		inboundNo, skuID, FromStatePendingInspect, TaskStatusCompleted).
		Order("target_bin_id ASC, batch_id ASC, id ASC").Find(&rows).Error
	return rows, wrapDB(err, "查询待检上架任务")
}

// ---- 审批记录 ----

func (r *repo) InsertApproval(ctx context.Context, tx *gorm.DB, a *DocumentApproval) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = database.Now()
	}
	return wrapDB(tx.WithContext(ctx).Create(a).Error, "写入审批记录")
}
