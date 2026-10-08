package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// Repository 数据访问层（architecture.md §1：无业务判断）。本域表写路径一律原生 SQL：
// INSERT 带 RETURNING、状态迁移 UPDATE 必须带 WHERE status=前置态 守卫（business-flow
// §13.2、plan §2.3 判据 4），影响行数由 Service 判定（0 = 状态冲突）。所有语句参数化，
// 禁止拼接任何外部输入（go-dev-standard 规则 8）。列表路径经 GORM 查询构造器
// （本域自身表模型，models.go）承载动态筛选与强制分页。
//
// 事务边界在 Service 层（plan §2.3 判据 6）：Service 经 Repo.Tx 开启单外层事务，
// 库存原语（StockGateway）与审计写入共用同一 tx 句柄，任一步失败整体回滚。
// 对库存族表（inventory/batches/serial_numbers）仅限只读 SELECT（见 repository_stock.go），
// 自带扫描结构体、不引用库存族 GORM 模型（plan §2.3 判据 3）。

// Scope 数据权限仓库范围快照（permission.md §4；handler 从 auth.WarehouseScope 取出，
// 禁止接受前端传入的仓库范围参数决定数据可见性）。
type Scope struct {
	All          bool
	WarehouseIDs []int64
}

// visible 判断单行仓库是否在范围内（详情接口的行级校验）。
func (s Scope) visible(warehouseID int64) bool {
	return s.All || containsID(s.WarehouseIDs, warehouseID)
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// applyScope 给 GORM 查询注入仓库范围过滤（column 为仓库 ID 列名）。
// 指定仓库范围但无绑定 → 不可见任何行（fail-closed，与 auth.ApplyWarehouseScope 同口径）。
func applyScope(db *gorm.DB, column string, s Scope) *gorm.DB {
	if s.All {
		return db
	}
	if len(s.WarehouseIDs) == 0 {
		return db.Where("1 = 0")
	}
	return db.Where(column+" IN ?", s.WarehouseIDs)
}

// Repo 销售域仓储接口：Service 的唯一持久化面。生产实现 = gormRepository；
// 单测以内存替身实现（fakes_test.go，含事务快照回滚语义）。
type Repo interface {
	// Tx 开启单外层事务：fn 错误整体回滚（architecture.md §4）。
	Tx(ctx context.Context, fn func(tx *gorm.DB) error) error

	// ---- 销售订单 ----
	InsertSalesOrder(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error
	UpdateSalesOrderDraft(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error
	GetSalesOrderForUpdate(tx *gorm.DB, id int64) (*SalesOrder, error)
	GetSalesOrderByNo(tx *gorm.DB, no string) (*SalesOrder, error)
	ListSalesOrders(ctx context.Context, q SalesOrderQuery) ([]SalesOrder, int64, error)
	ListSalesOrderItems(tx *gorm.DB, soID int64) ([]SalesOrderItem, error)
	MarkSalesOrderStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error)
	UpdateSalesOrderItemProgress(tx *gorm.DB, soID, lineNo int64, allocated, shipped *Qty) error

	// ---- 审批记录（document_approvals，append-only 仅 INSERT；grants 双保险） ----
	InsertApproval(tx *gorm.DB, targetType, targetNo, action, result, opinion string, actor Actor) error

	// ---- 出库单 ----
	InsertOutboundOrder(tx *gorm.DB, o *OutboundOrder, items []OutboundItem) error
	GetOutboundOrderForUpdate(tx *gorm.DB, id int64) (*OutboundOrder, error)
	GetOutboundOrderByNo(tx *gorm.DB, no string) (*OutboundOrder, error)
	ListOutboundOrders(ctx context.Context, q OutboundQuery) ([]OutboundOrder, int64, error)
	ListOutboundOrdersBySO(tx *gorm.DB, soNo string) ([]OutboundOrder, error)
	ListOutboundItems(tx *gorm.DB, outboundID int64) ([]OutboundItem, error)
	MarkOutboundStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error)
	UpdateOutboundItemProgress(tx *gorm.DB, outboundID, lineNo int64, cols OutboundItemProgress) error

	// ---- 分配记录 ----
	InsertAllocations(tx *gorm.DB, recs []AllocationRecord) error
	// UpdateAllocationQty 短拣重同步：分配记录数量改写为实拣量（M2 修复轮；
	// reason 增量注记由调用方传入，updated_by/at 落列）。
	UpdateAllocationQty(tx *gorm.DB, recID int64, qty Qty, by int64, reasonNote map[string]any) error
	DeleteAllocations(tx *gorm.DB, outboundNo string, lineNo int64) error
	ListAllocations(tx *gorm.DB, outboundNo string, lineNo int64) ([]AllocationRecord, error)
	ListAllocationsByOutbound(tx *gorm.DB, outboundNo string) ([]AllocationRecord, error)
	ListAllocationsPage(ctx context.Context, q AllocationQuery) ([]AllocationRecord, int64, error)

	// ---- 拣货任务 ----
	InsertPickTasks(tx *gorm.DB, tasks []PickTask) error
	GetPickTask(tx *gorm.DB, id int64) (*PickTask, error)
	ClaimPickTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error)
	MarkPickTaskStatus(tx *gorm.DB, id int64, from, to string, stamp PickStamp) (int64, error)
	CancelPickTasks(tx *gorm.DB, outboundNo string, lineNo int64) error
	ListPickTasksByOutbound(tx *gorm.DB, outboundNo string) ([]PickTask, error)
	ListPickTasks(ctx context.Context, q PickQuery) ([]PickTask, int64, error)
	// SetPickTaskPriority 任务优先级单列守卫更新（效率层一期 B3：终态 0 行；
	// 迁移 000023 chk_pick_tasks_priority CHECK 0-9 兜底）。
	SetPickTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error)

	// ---- 复核任务 ----
	InsertCheckTasks(tx *gorm.DB, tasks []CheckTask) error
	GetCheckTask(tx *gorm.DB, id int64) (*CheckTask, error)
	AssignCheckTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error)
	MarkCheckTaskStatus(tx *gorm.DB, id int64, from, to, result string) (int64, error)
	ListCheckTasksByOutbound(tx *gorm.DB, outboundNo string) ([]CheckTask, error)
	ListCheckTasks(ctx context.Context, q CheckQuery) ([]CheckTask, int64, error)
	// SetCheckTaskPriority 复核任务优先级单列守卫更新（效率层一期 B3，同上）。
	SetCheckTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error)

	// ---- 打包 ----
	FindPackageByIdempotencyKey(tx *gorm.DB, key string) (*PackingRecord, error)
	InsertPackage(tx *gorm.DB, p *PackingRecord, items []PackingItem) error
	SumPackedByLine(tx *gorm.DB, outboundID int64) (map[int64]Qty, error)
	CountPackagesByOutbound(tx *gorm.DB, outboundNo string) (int64, error)
	ListPackagesByOutbound(tx *gorm.DB, outboundNo string) ([]PackingRecord, error)
	ListPackages(ctx context.Context, q PackingQuery) ([]PackingRecord, int64, error)

	// ---- 发货单 ----
	FindShipmentByIdempotencyKey(tx *gorm.DB, key string) (*Shipment, error)
	GetShipment(tx *gorm.DB, id int64) (*Shipment, error)
	InsertShipment(tx *gorm.DB, s *Shipment) error
	MarkShipmentStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error)
	ListShipmentsByOutbound(tx *gorm.DB, outboundNo string) ([]Shipment, error)
	ListShipments(ctx context.Context, q ShipmentQuery) ([]Shipment, int64, error)

	// ---- 库存族只读 SELECT（分配候选与序列号校验；plan §2.3 判据 3 读侧口径） ----
	ReadBatchCandidates(tx *gorm.DB, warehouseID, skuID int64) ([]BatchCandidate, error)
	ReadBinStock(tx *gorm.DB, warehouseID, skuID, batchID int64) ([]BinStock, error)
	ReadBinLocation(tx *gorm.DB, warehouseID, binID, skuID, batchID int64) (zoneID, shelfID int64, found bool, err error)
	ReadSerialStates(tx *gorm.DB, skuID int64, serials []string) ([]SerialState, error)
}

// StatusStamp 状态迁移附带的时间戳/操作者冗余列（业务时间字段，business-flow §13.4）。
type StatusStamp struct {
	By int64 // updated_by + 对应业务列的 *_by
	// 各 *_at 业务时间戳置位开关（置 now()）。
	Approved, Shipped, Completed, Cancelled bool // 销售订单
	Picked, Checked, Packed                 bool // 出库单
}

// OutboundItemProgress 出库单明细进度列（绝对值赋值，由 Service 按记录汇总计算）。
type OutboundItemProgress struct {
	Picked  *Qty
	Checked *Qty
	Packed  *Qty
	Shipped *Qty
}

// ---- 查询结构（列表强制分页：page/pageSize 由 handler 经 response.ParsePage 取） ----

// SalesOrderQuery 销售订单列表查询。
type SalesOrderQuery struct {
	Scope
	Page, PageSize int
	Status         string
	CustomerID     int64
	WarehouseID    int64
	SoNo           string
	CreatedFrom    *time.Time
	CreatedTo      *time.Time
}

// OutboundQuery 出库单列表查询。
type OutboundQuery struct {
	Scope
	Page, PageSize int
	Status         string
	WarehouseID    int64
	SoNo           string
	OutboundNo     string
}

// AllocationQuery 分配记录列表查询。
type AllocationQuery struct {
	Scope
	Page, PageSize int
	OutboundNo     string
	SKUID          int64
	Strategy       string
}

// PickQuery 拣货任务列表查询。
type PickQuery struct {
	Scope
	Page, PageSize int
	Status         string
	OutboundNo     string
	AssigneeID     int64
	WarehouseID    int64
}

// CheckQuery 复核任务列表查询。
type CheckQuery struct {
	Scope
	Page, PageSize int
	Status         string
	OutboundNo     string
	AssigneeID     int64
	WarehouseID    int64
}

// PackingQuery 打包记录列表查询。
type PackingQuery struct {
	Scope
	Page, PageSize int
	OutboundNo     string
	WarehouseID    int64
}

// ShipmentQuery 发货单列表查询。
type ShipmentQuery struct {
	Scope
	Page, PageSize int
	Status         string
	OutboundNo     string
	WarehouseID    int64
}

// ---- 生产实现 ----

// gormRepository Repo 的 GORM/PostgreSQL 实现。
type gormRepository struct {
	db *gorm.DB
}

// newGormRepository 构造生产仓储。
func newGormRepository(db *gorm.DB) Repo { return &gormRepository{db: db} }

// pgUniqueViolation 判断 err 是否为指定约束的 PostgreSQL 唯一冲突（23505）。
func pgUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		(constraint == "" || pgErr.ConstraintName == constraint)
}

// Tx 事务边界：fn 错误整体回滚（architecture.md §4）。
func (r *gormRepository) Tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

// ---- 销售订单 ----

// insertStamps INSERT ... RETURNING 回填的最小列集。
//
// 为什么不能把 RETURNING 结果直接 Scan 进业务模型（2026-10-07 全流程实测缺陷根因）：
// GORM 的 `Raw(...).Scan(dest)` 走 ScanRows → ScanInitialized 模式，进入结构体分支前
// 先执行 `ReflectValue.Set(reflect.Zero(...))` 把**整个结构体清零**
// （gorm@v1.31.2 scan.go:352-353），随后只写入结果行命中的列。因此
// 「INSERT ... RETURNING id, created_at, updated_at」+「Scan(完整业务结构体)」
// 会把单号/状态/数量/锁 ID 等业务字段全部抹成零值。实测现象：
// 写端点响应体回读字段全空（销售单/包裹/发货单）、审核响应 lock_count/locked_qty=0、
// sales_order_items.qty_allocated 恒 0（分配量连同 rec.Qty/rec.LockID 一起丢失）。
// 一律改为「扫进本结构体再逐字段回填」，业务字段原样保留。
type insertStamps struct {
	ID        database.ID       `gorm:"column:id"`
	CreatedAt database.JSONTime `gorm:"column:created_at"`
	UpdatedAt database.JSONTime `gorm:"column:updated_at"`
}

func (r *gormRepository) InsertSalesOrder(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error {
	var st insertStamps
	err := tx.Raw(`
		INSERT INTO sales_orders
			(so_no, customer_id, warehouse_id, shipping_address, delivery_method,
			 total_amount, status, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id, created_at, updated_at`,
		o.SoNo, o.CustomerID, o.WarehouseID, o.ShippingAddress, o.DeliveryMethod,
		o.TotalAmount.String(), o.Status, o.Remark, o.CreatedBy, o.UpdatedBy,
	).Scan(&st).Error
	if err != nil {
		return err
	}
	o.ID, o.CreatedAt, o.UpdatedAt = st.ID, st.CreatedAt, st.UpdatedAt
	return insertSOItems(tx, o.ID.Int64(), o.CreatedBy, items)
}

func insertSOItems(tx *gorm.DB, soID, by int64, items []SalesOrderItem) error {
	// 明细批量写入（stockops f6 同款：原逐行 INSERT RETURNING，一次往返改多行 VALUES；
	// PG 按插入序返回 id 逐行回填，it.SoID 语义不变）。
	if len(items) == 0 {
		return nil
	}
	var (
		sb   strings.Builder
		args = make([]any, 0, len(items)*10)
	)
	sb.WriteString(`
		INSERT INTO sales_order_items
			(so_id, line_no, sku_id, qty, price, amount, qty_allocated, qty_shipped,
			 remark, created_at, updated_at, created_by, updated_by)
		VALUES `)
	for i := range items {
		it := &items[i]
		it.SoID = soID
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?, ?, ?, ?, ?, ?, ?, 0, ?, now(), now(), ?, ?)")
		args = append(args, soID, it.LineNo, it.SKUID, it.Qty.String(), it.Price.String(), it.Amount.String(),
			it.QtyAllocated.String(), it.Remark, by, by)
	}
	sb.WriteString(` RETURNING id`)
	var ids []int64
	if err := tx.Raw(sb.String(), args...).Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) != len(items) {
		return fmt.Errorf("sales: 销售订单明细批量写入回执数不符: %d/%d", len(ids), len(items))
	}
	for i := range items {
		items[i].ID = database.ID(ids[i])
	}
	return nil
}

// UpdateSalesOrderDraft 草稿编辑：表头守卫更新（仅 DRAFT 可改）+ 明细整组重写
// （草稿明细重编辑属编辑行为，非业务单据删除；订单提交后明细不可再改）。
func (r *gormRepository) UpdateSalesOrderDraft(tx *gorm.DB, o *SalesOrder, items []SalesOrderItem) error {
	res := tx.Exec(`
		UPDATE sales_orders SET customer_id = ?, warehouse_id = ?, shipping_address = ?,
		       delivery_method = ?, total_amount = ?, remark = ?, updated_at = now(), updated_by = ?
		WHERE id = ? AND status = ?`,
		o.CustomerID, o.WarehouseID, o.ShippingAddress, o.DeliveryMethod,
		o.TotalAmount.String(), o.Remark, o.UpdatedBy, o.ID.Int64(), SOStatusDraft)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return response.NewError(ErrStateConflict, map[string]any{"so_id": o.ID, "reason": "仅草稿状态可编辑"})
	}
	if err := tx.Exec(`DELETE FROM sales_order_items WHERE so_id = ?`, o.ID.Int64()).Error; err != nil {
		return err
	}
	return insertSOItems(tx, o.ID.Int64(), o.UpdatedBy, items)
}

// GetSalesOrderForUpdate 读取销售订单并加行锁（FOR UPDATE：审核/取消等并发动作
// 以订单行锁串行化，杜绝双审核双预占——inventory-rules §9.2 并发保护）。
func (r *gormRepository) GetSalesOrderForUpdate(tx *gorm.DB, id int64) (*SalesOrder, error) {
	var o SalesOrder
	err := tx.Raw(`SELECT * FROM sales_orders WHERE id = ? FOR UPDATE`, id).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func (r *gormRepository) GetSalesOrderByNo(tx *gorm.DB, no string) (*SalesOrder, error) {
	var o SalesOrder
	err := tx.Raw(`SELECT * FROM sales_orders WHERE so_no = ?`, no).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func (r *gormRepository) ListSalesOrders(ctx context.Context, q SalesOrderQuery) ([]SalesOrder, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&SalesOrder{}), "warehouse_id", q.Scope)
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.CustomerID > 0 {
		db = db.Where("customer_id = ?", q.CustomerID)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	if q.SoNo != "" {
		db = db.Where("so_no ILIKE ?", like(q.SoNo))
	}
	if q.CreatedFrom != nil {
		db = db.Where("created_at >= ?", *q.CreatedFrom)
	}
	if q.CreatedTo != nil {
		db = db.Where("created_at <= ?", *q.CreatedTo)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []SalesOrder
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) ListSalesOrderItems(tx *gorm.DB, soID int64) ([]SalesOrderItem, error) {
	var rows []SalesOrderItem
	err := tx.Raw(`SELECT * FROM sales_order_items WHERE so_id = ? ORDER BY line_no`, soID).Scan(&rows).Error
	return rows, err
}

// MarkSalesOrderStatus 状态机守卫迁移：WHERE id AND status=from，影响行数 0 = 冲突
// （plan §6 统一实现形态）。stamp 置位的业务时间列以 now() 落库（§13.4）。
func (r *gormRepository) MarkSalesOrderStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, stamp.By}
	if stamp.Approved {
		set += ", approved_at = now(), approved_by = ?"
		args = append(args, stamp.By)
	}
	if stamp.Shipped {
		set += ", shipped_at = now()"
	}
	if stamp.Completed {
		set += ", completed_at = now()"
	}
	if stamp.Cancelled {
		set += ", cancelled_at = now()"
	}
	args = append(args, id, from)
	res := tx.Exec("UPDATE sales_orders SET "+set+" WHERE id = ? AND status = ?", args...)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateSalesOrderItemProgress(tx *gorm.DB, soID, lineNo int64, allocated, shipped *Qty) error {
	return updateItemProgress(tx, "sales_order_items", "so_id", soID, lineNo, map[string]any{
		"qty_allocated": qtyPtrString(allocated), "qty_shipped": qtyPtrString(shipped),
	})
}

// ---- 共享 helper ----

func qtyPtrString(q *Qty) any {
	if q == nil {
		return nil
	}
	return q.String()
}

// updateItemProgress 明细进度列更新（列名取自调用方白名单字面量，值参数化）。
func updateItemProgress(tx *gorm.DB, table, fkCol string, fkID, lineNo int64, cols map[string]any) error {
	set := "updated_at = now()"
	var args []any
	for col, val := range cols {
		if val == nil {
			continue
		}
		set += ", " + col + " = ?"
		args = append(args, val)
	}
	args = append(args, fkID, lineNo)
	return tx.Exec("UPDATE "+table+" SET "+set+" WHERE "+fkCol+" = ? AND line_no = ?", args...).Error
}

// like 列表包含匹配（转义 %/_\ 防通配符注入）。
func like(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

// jsonMarshal 序列化 jsonb 载荷（reason 等）；nil → SQL NULL 语义的空对象。
func jsonMarshal(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- 审批记录（document_approvals：append-only，grants 仅 SELECT+INSERT） ----

func (r *gormRepository) InsertApproval(tx *gorm.DB, targetType, targetNo, action, result, opinion string, actor Actor) error {
	return tx.Exec(`
		INSERT INTO document_approvals
			(target_type, target_no, action, result, opinion, operator_id, operator_name, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, now())`,
		targetType, targetNo, action, result, opinion, actor.ID, actor.Name, actor.RequestID).Error
}

// ---- 出库单 ----

func (r *gormRepository) InsertOutboundOrder(tx *gorm.DB, o *OutboundOrder, items []OutboundItem) error {
	var st insertStamps
	err := tx.Raw(`
		INSERT INTO outbound_orders
			(outbound_no, so_no, type, warehouse_id, status, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id, created_at, updated_at`,
		o.OutboundNo, o.SoNo, o.Type, o.WarehouseID, o.Status, o.Remark, o.CreatedBy, o.UpdatedBy,
	).Scan(&st).Error
	if err != nil {
		return err
	}
	o.ID, o.CreatedAt, o.UpdatedAt = st.ID, st.CreatedAt, st.UpdatedAt
	for i := range items {
		it := &items[i]
		var id int64
		err := tx.Raw(`
			INSERT INTO outbound_items
				(outbound_id, line_no, sku_id, qty, qty_picked, qty_checked, qty_packed, qty_shipped,
				 remark, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, 0, 0, 0, 0, ?, now(), now(), ?, ?)
			RETURNING id`,
			o.ID.Int64(), it.LineNo, it.SKUID, it.Qty.String(), it.Remark, o.CreatedBy, o.UpdatedBy,
		).Scan(&id).Error
		if err != nil {
			return err
		}
		it.ID = database.ID(id)
		it.OutboundID = o.ID.Int64()
	}
	return nil
}

func (r *gormRepository) GetOutboundOrderForUpdate(tx *gorm.DB, id int64) (*OutboundOrder, error) {
	var o OutboundOrder
	err := tx.Raw(`SELECT * FROM outbound_orders WHERE id = ? FOR UPDATE`, id).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func (r *gormRepository) GetOutboundOrderByNo(tx *gorm.DB, no string) (*OutboundOrder, error) {
	var o OutboundOrder
	err := tx.Raw(`SELECT * FROM outbound_orders WHERE outbound_no = ?`, no).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func (r *gormRepository) ListOutboundOrders(ctx context.Context, q OutboundQuery) ([]OutboundOrder, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&OutboundOrder{}), "warehouse_id", q.Scope)
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	if q.SoNo != "" {
		db = db.Where("so_no = ?", q.SoNo)
	}
	if q.OutboundNo != "" {
		db = db.Where("outbound_no ILIKE ?", like(q.OutboundNo))
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []OutboundOrder
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) ListOutboundOrdersBySO(tx *gorm.DB, soNo string) ([]OutboundOrder, error) {
	var rows []OutboundOrder
	err := tx.Raw(`SELECT * FROM outbound_orders WHERE so_no = ? ORDER BY id`, soNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListOutboundItems(tx *gorm.DB, outboundID int64) ([]OutboundItem, error) {
	var rows []OutboundItem
	err := tx.Raw(`SELECT * FROM outbound_items WHERE outbound_id = ? ORDER BY line_no`, outboundID).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) MarkOutboundStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, stamp.By}
	if stamp.Picked {
		set += ", picked_at = now()"
	}
	if stamp.Checked {
		set += ", checked_at = now()"
	}
	if stamp.Packed {
		set += ", packed_at = now()"
	}
	if stamp.Shipped {
		set += ", shipped_at = now()"
	}
	if stamp.Cancelled {
		set += ", cancelled_at = now()"
	}
	args = append(args, id, from)
	res := tx.Exec("UPDATE outbound_orders SET "+set+" WHERE id = ? AND status = ?", args...)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateOutboundItemProgress(tx *gorm.DB, outboundID, lineNo int64, cols OutboundItemProgress) error {
	return updateItemProgress(tx, "outbound_items", "outbound_id", outboundID, lineNo, map[string]any{
		"qty_picked": qtyPtrString(cols.Picked), "qty_checked": qtyPtrString(cols.Checked),
		"qty_packed": qtyPtrString(cols.Packed), "qty_shipped": qtyPtrString(cols.Shipped),
	})
}

// ---- 分配记录 ----

func (r *gormRepository) InsertAllocations(tx *gorm.DB, recs []AllocationRecord) error {
	for i := range recs {
		rec := &recs[i]
		reason, err := jsonMarshal(rec.Reason)
		if err != nil {
			return err
		}
		var st insertStamps
		err = tx.Raw(`
			INSERT INTO allocation_records
				(outbound_no, line_no, sku_id, batch_id, warehouse_id, bin_id, qty, strategy, reason, lock_id,
				 created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, now(), now(), ?, ?)
			RETURNING id, created_at, updated_at`,
			rec.OutboundNo, rec.LineNo, rec.SKUID, rec.BatchID, rec.WarehouseID, rec.BinID,
			rec.Qty.String(), rec.Strategy, reason, rec.LockID, rec.CreatedBy, rec.UpdatedBy,
		).Scan(&st).Error
		if err != nil {
			return err
		}
		// 只回填 RETURNING 三列：Qty/LockID/SKUID 等业务字段必须原样保留——调用方
		// approveOrder 正是据此累计 lineAlloc 与 res.LockCount/LockedQty
		// （直接 Scan(rec) 会把它们清零，致 qty_allocated 恒 0 与审核回执失真）。
		rec.ID, rec.CreatedAt, rec.UpdatedAt = st.ID, st.CreatedAt, st.UpdatedAt
	}
	return nil
}

// UpdateAllocationQty 短拣重同步实现：qty 改写 + reason jsonb 增量注记（GORM
// serializer 承载 map 序列化；仅允许收缩方向由调用方保证——picked ≤ 任务量已守卫）。
func (r *gormRepository) UpdateAllocationQty(tx *gorm.DB, recID int64, qty Qty, by int64, reasonNote map[string]any) error {
	var rec AllocationRecord
	if err := tx.Raw(`SELECT * FROM allocation_records WHERE id = ? FOR UPDATE`, recID).Scan(&rec).Error; err != nil {
		return err
	}
	if rec.ID == 0 {
		return response.NewError(response.CodeNotFound, map[string]any{"reason": "分配记录不存在", "allocation_record_id": recID})
	}
	if reasonNote != nil {
		if rec.Reason == nil {
			rec.Reason = map[string]any{}
		}
		for k, v := range reasonNote {
			rec.Reason[k] = v
		}
	}
	res := tx.Model(&AllocationRecord{}).Where("id = ?", recID).Updates(map[string]any{
		"qty":        qty.String(),
		"reason":     rec.Reason,
		"updated_at": gorm.Expr("now()"),
		"updated_by": by,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return response.NewError(response.CodeConflict, map[string]any{"reason": "分配记录更新未生效", "allocation_record_id": recID})
	}
	return nil
}

func (r *gormRepository) DeleteAllocations(tx *gorm.DB, outboundNo string, lineNo int64) error {
	return tx.Exec(`DELETE FROM allocation_records WHERE outbound_no = ? AND line_no = ?`, outboundNo, lineNo).Error
}

func (r *gormRepository) ListAllocations(tx *gorm.DB, outboundNo string, lineNo int64) ([]AllocationRecord, error) {
	var rows []AllocationRecord
	err := tx.Raw(`SELECT * FROM allocation_records WHERE outbound_no = ? AND (? = 0 OR line_no = ?) ORDER BY id`,
		outboundNo, lineNo, lineNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListAllocationsByOutbound(tx *gorm.DB, outboundNo string) ([]AllocationRecord, error) {
	var rows []AllocationRecord
	err := tx.Raw(`SELECT * FROM allocation_records WHERE outbound_no = ? ORDER BY id`, outboundNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListAllocationsPage(ctx context.Context, q AllocationQuery) ([]AllocationRecord, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&AllocationRecord{}), "warehouse_id", q.Scope)
	if q.OutboundNo != "" {
		db = db.Where("outbound_no = ?", q.OutboundNo)
	}
	if q.SKUID > 0 {
		db = db.Where("sku_id = ?", q.SKUID)
	}
	if q.Strategy != "" {
		db = db.Where("strategy = ?", q.Strategy)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AllocationRecord
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

// SetPickTaskPriority 拣货任务优先级单列守卫更新（效率层一期 B3：终态 PICKED/CANCELLED
// 拒绝 0 行；值域 0-9 由迁移 000023 chk_pick_tasks_priority CHECK 兜底）。
func (r *gormRepository) SetPickTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error) {
	res := tx.Exec(`
		UPDATE pick_tasks SET priority = ?, updated_at = now(), updated_by = ?
		WHERE id = ? AND status NOT IN (?, ?)`,
		priority, by, id, PickStatusPicked, PickStatusCancelled)
	return res.RowsAffected, res.Error
}

// SetCheckTaskPriority 复核任务优先级单列守卫更新（终态 DONE/EXCEPTION 拒绝 0 行）。
func (r *gormRepository) SetCheckTaskPriority(tx *gorm.DB, id int64, priority int, by int64) (int64, error) {
	res := tx.Exec(`
		UPDATE check_tasks SET priority = ?, updated_at = now(), updated_by = ?
		WHERE id = ? AND status NOT IN (?, ?)`,
		priority, by, id, CheckStatusDone, CheckStatusException)
	return res.RowsAffected, res.Error
}
