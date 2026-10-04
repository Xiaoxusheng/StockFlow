package returns

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/stock"
)

// 数据访问层（architecture.md §1：Service → Repository → Database；handler 禁止直连数据库）。
// Repository 为接口：Service 依赖接口，单元测试以内存替身替换（ask 约束：单测不依赖
// PostgreSQL/Redis，参照 internal/masterdata、internal/inventory 既有替身模式）。

// SourceReturnedLine 来源单已退量累计行（SumReturnedBySource 结果）。
type SourceReturnedLine struct {
	LineNo int64
	SKUID  int64
	Qty    stock.Qty
}

// ReturnOrderFilter 退货单列表过滤（列表强制分页，architecture.md §7）。
type ReturnOrderFilter struct {
	Type          string // SALES/PURCHASE，空=全部
	Status        string
	SourceNo      string
	WarehouseID   int64
	WarehouseIDs  []int64 // 数据权限仓库范围（auth.WarehouseScope；nil+AllWarehouses=false 表示全量语义由调用方组装）
	AllWarehouses bool
	Page          int
	PageSize      int
}

// ExceptionFilter 异常单列表过滤。
type ExceptionFilter struct {
	Type       string
	Status     string
	SourceType string
	SourceNo   string
	Page       int
	PageSize   int
}

// Repository 退货域数据访问接口。
type Repository interface {
	// DB 返回句柄（Service 开启外层事务用，masterdata 同款约定）。
	DB() *gorm.DB

	// —— 退货单 ——
	InsertReturnOrder(tx *gorm.DB, o *ReturnOrder) error
	InsertReturnItems(tx *gorm.DB, items []*ReturnItem) error
	ReplaceReturnItems(tx *gorm.DB, returnID int64, items []*ReturnItem) error
	FindReturnOrder(ctx context.Context, id int64) (*ReturnOrder, error)
	FindReturnOrderForUpdate(tx *gorm.DB, id int64) (*ReturnOrder, error)
	ListReturnOrders(ctx context.Context, f ReturnOrderFilter) ([]*ReturnOrder, int64, error)
	FindReturnItemForUpdate(tx *gorm.DB, returnID, lineNo int64) (*ReturnItem, error)
	ListReturnItemsForUpdate(tx *gorm.DB, returnID int64) ([]*ReturnItem, error)
	// UpdateReturnStatus 状态机守卫 UPDATE：WHERE id=? AND status IN from，影响行数 0 即
	// 状态冲突（plan §6 统一形态；tsCol 只接受 statusTimeCols 白名单；extra 附加列值，
	// 列名同样走白名单——如 approved_by）。
	UpdateReturnStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64, extra map[string]any) (int64, error)
	// AddItemReceived 收货量累计（带 WHERE qty_received+n <= qty_return 守卫，0 行=超量）。
	AddItemReceived(tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error)
	// AddItemInspected 质检量累计（qty_inspected+=q+d、qty_defective+=d，
	// WHERE qty_inspected+q+d <= qty_received，0 行=超量）。
	AddItemInspected(tx *gorm.DB, itemID int64, qualified, defective stock.Qty, by int64) (int64, error)
	// SumReturnedBySource 来源单已退量累计（status<>CANCELLED 的退货单明细合计；
	// excludeReturnID>0 时排除指定退货单）。q 为业务事务句柄（与插入同事务读取）或
	// 只读 DB 句柄。
	SumReturnedBySource(q *gorm.DB, sourceNo, orderType string, excludeReturnID int64) ([]SourceReturnedLine, error)
	// LockSourceSerial 事务级咨询锁（pg_advisory_xact_lock）：串行化同来源单的
	// 退货创建"读累计-校验-插入"窗口（修复轮）。退量防超是跨行读-算-写，无法用
	// 单行条件 UPDATE 守卫（与收货侧 addQtyGuarded 不同——后者是单行累计）；
	// 同来源创建并发时后到者在本锁上等待先到者提交后再读累计，杜绝双双读到
	// returned=0 而累计超退。锁键含来源单号，不同来源单互不阻塞。
	LockSourceSerial(tx *gorm.DB, sourceNo string) error

	// —— 审批记录（document_approvals，append-only INSERT）——
	InsertApproval(tx *gorm.DB, a *DocumentApproval) error

	// —— 异常单 ——
	InsertException(tx *gorm.DB, e *Exception) error
	FindException(ctx context.Context, id int64) (*Exception, error)
	FindExceptionForUpdate(tx *gorm.DB, id int64) (*Exception, error)
	ListExceptions(ctx context.Context, f ExceptionFilter) ([]*Exception, int64, error)
	UpdateExceptionStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64) (int64, error)
	UpdateExceptionAssignee(tx *gorm.DB, id, assigneeID int64, assigneeName string, by int64) error
	SetExceptionFreezeLock(tx *gorm.DB, id int64, lockID *int64) error
	AppendHandleRecord(tx *gorm.DB, id int64, rec HandleRecord) error

	// —— 操作日志（追溯数据源之一：inventory-rules §10；operation_logs 为平台审计表，
	// 只读查询）——
	ListOperationLogs(ctx context.Context, requestIDs []string, limit int) ([]middleware.OperationLog, error)
}

// gormRepository GORM 实现（迁移 000010 列结构）。
type gormRepository struct {
	db *gorm.DB
}

// NewGormRepository 构造 GORM 数据访问实现。
func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) DB() *gorm.DB { return r.db }

// ---- 退货单 ----

func (r *gormRepository) InsertReturnOrder(tx *gorm.DB, o *ReturnOrder) error {
	return tx.Create(o).Error
}

func (r *gormRepository) InsertReturnItems(tx *gorm.DB, items []*ReturnItem) error {
	if len(items) == 0 {
		return nil
	}
	return tx.Create(&items).Error
}

func (r *gormRepository) ReplaceReturnItems(tx *gorm.DB, returnID int64, items []*ReturnItem) error {
	if err := tx.Where("return_id = ?", returnID).Delete(&ReturnItem{}).Error; err != nil {
		return err
	}
	return r.InsertReturnItems(tx, items)
}

func (r *gormRepository) FindReturnOrder(ctx context.Context, id int64) (*ReturnOrder, error) {
	var o ReturnOrder
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&o).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *gormRepository) FindReturnOrderForUpdate(tx *gorm.DB, id int64) (*ReturnOrder, error) {
	var o ReturnOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&o).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *gormRepository) ListReturnOrders(ctx context.Context, f ReturnOrderFilter) ([]*ReturnOrder, int64, error) {
	q := r.db.WithContext(ctx).Model(&ReturnOrder{})
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.SourceNo != "" {
		q = q.Where("source_no = ?", f.SourceNo)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.WarehouseIDs != nil {
		// 空切片生成 IN (NULL) 零匹配——指定范围但无绑定仓库 = 不可见任何行（fail-closed，
		// auth.ApplyWarehouseScope 同口径）。
		q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*ReturnOrder
	err := q.Order("id DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) FindReturnItemForUpdate(tx *gorm.DB, returnID, lineNo int64) (*ReturnItem, error) {
	var it ReturnItem
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("return_id = ? AND line_no = ?", returnID, lineNo).First(&it).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (r *gormRepository) ListReturnItemsForUpdate(tx *gorm.DB, returnID int64) ([]*ReturnItem, error) {
	var rows []*ReturnItem
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("return_id = ?", returnID).Order("line_no").Find(&rows).Error
	return rows, err
}

// returnExtraCols UpdateReturnStatus 附加列白名单（列名不拼接外部输入，go-dev-standard 规则 8）。
var returnExtraCols = map[string]bool{"approved_by": true, "remark": true}

func (r *gormRepository) UpdateReturnStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64, extra map[string]any) (int64, error) {
	if !statusTimeCols[tsCol] {
		return 0, responseErrorParam("ts_col", "非法时间列（编程错误）")
	}
	q := tx.Model(&ReturnOrder{}).
		Where("id = ? AND status IN ?", id, from)
	sets := map[string]any{"status": to, "updated_by": by}
	if tsCol != "" {
		sets[tsCol] = gorm.Expr("now()")
	}
	for col, v := range extra {
		if !returnExtraCols[col] {
			return 0, responseErrorParam("extra."+col, "非法附加列（编程错误）")
		}
		sets[col] = v
	}
	res := q.Updates(sets)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) AddItemReceived(tx *gorm.DB, itemID int64, qty stock.Qty, by int64) (int64, error) {
	res := tx.Model(&ReturnItem{}).
		Where("id = ? AND qty_received + ? <= qty_return", itemID, qty).
		Updates(map[string]any{
			"qty_received": gorm.Expr("qty_received + ?", qty),
			"updated_at":   gorm.Expr("now()"),
			"updated_by":   by,
		})
	return res.RowsAffected, res.Error
}

func (r *gormRepository) AddItemInspected(tx *gorm.DB, itemID int64, qualified, defective stock.Qty, by int64) (int64, error) {
	res := tx.Model(&ReturnItem{}).
		Where("id = ? AND qty_inspected + ? + ? <= qty_received", itemID, qualified, defective).
		Updates(map[string]any{
			"qty_inspected": gorm.Expr("qty_inspected + ? + ?", qualified, defective),
			"qty_defective": gorm.Expr("qty_defective + ?", defective),
			"updated_at":    gorm.Expr("now()"),
			"updated_by":    by,
		})
	return res.RowsAffected, res.Error
}

func (r *gormRepository) SumReturnedBySource(q *gorm.DB, sourceNo, orderType string, excludeReturnID int64) ([]SourceReturnedLine, error) {
	var rows []SourceReturnedLine
	err := q.Raw(`
		SELECT i.line_no, i.sku_id, COALESCE(SUM(i.qty_return), 0) AS qty
		FROM return_orders o
		JOIN return_items i ON i.return_id = o.id
		WHERE o.source_no = ? AND o.type = ? AND o.status <> ?
		  AND (? = 0 OR o.id <> ?)
		GROUP BY i.line_no, i.sku_id`,
		sourceNo, orderType, ReturnStatusCancelled, excludeReturnID, excludeReturnID).
		Scan(&rows).Error
	return rows, err
}

// LockSourceSerial 实现见接口注释（pg_advisory_xact_lock 事务级：事务结束自动释放，
// 无需配对解锁；hashtext 冲突概率可忽略，即使偶发跨单据串行化也只是短暂等待）。
func (r *gormRepository) LockSourceSerial(tx *gorm.DB, sourceNo string) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, "returns:create:"+sourceNo).Error
}

func (r *gormRepository) InsertApproval(tx *gorm.DB, a *DocumentApproval) error {
	return tx.Create(a).Error
}

// ---- 异常单 ----

func (r *gormRepository) InsertException(tx *gorm.DB, e *Exception) error {
	return tx.Create(e).Error
}

func (r *gormRepository) FindException(ctx context.Context, id int64) (*Exception, error) {
	var e Exception
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *gormRepository) FindExceptionForUpdate(tx *gorm.DB, id int64) (*Exception, error) {
	var e Exception
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *gormRepository) ListExceptions(ctx context.Context, f ExceptionFilter) ([]*Exception, int64, error) {
	q := r.db.WithContext(ctx).Model(&Exception{})
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
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
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*Exception
	err := q.Order("id DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) UpdateExceptionStatus(tx *gorm.DB, id int64, from []string, to, tsCol string, by int64) (int64, error) {
	if !statusTimeCols[tsCol] {
		return 0, responseErrorParam("ts_col", "非法时间列（编程错误）")
	}
	q := tx.Model(&Exception{}).
		Where("id = ? AND status IN ?", id, from)
	sets := map[string]any{"status": to, "updated_by": by}
	if tsCol != "" {
		sets[tsCol] = gorm.Expr("now()")
	}
	res := q.Updates(sets)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateExceptionAssignee(tx *gorm.DB, id, assigneeID int64, assigneeName string, by int64) error {
	return tx.Model(&Exception{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"assignee_id": assigneeID, "assignee_name": assigneeName,
			"updated_at": gorm.Expr("now()"), "updated_by": by,
		}).Error
}

func (r *gormRepository) SetExceptionFreezeLock(tx *gorm.DB, id int64, lockID *int64) error {
	return tx.Model(&Exception{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"freeze_lock_id": lockID,
			"updated_at":     gorm.Expr("now()"),
		}).Error
}

func (r *gormRepository) AppendHandleRecord(tx *gorm.DB, id int64, rec HandleRecord) error {
	// 追加式（business-flow §11.2）：行锁内读改写 jsonb 数组，不覆盖既有记录。
	var e Exception
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&e).Error
	if err != nil {
		return err
	}
	records := e.HandleRecordList()
	records = append(records, rec)
	return tx.Model(&Exception{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"handle_records": marshalJSONB(records),
			"updated_at":     gorm.Expr("now()"),
		}).Error
}

// ---- 操作日志（只读）----

func (r *gormRepository) ListOperationLogs(ctx context.Context, requestIDs []string, limit int) ([]middleware.OperationLog, error) {
	if len(requestIDs) == 0 {
		return nil, nil
	}
	var rows []middleware.OperationLog
	err := r.db.WithContext(ctx).
		Where("module = ? AND request_id IN ?", "inventory", requestIDs).
		Order("id ASC").Limit(limit).
		Find(&rows).Error
	return rows, err
}
