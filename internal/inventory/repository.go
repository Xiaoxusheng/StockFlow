package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/response"
)

// Repository 数据访问层（architecture.md §1：无业务判断；backend-m1-plan §8.3：
// 库存关键路径一律原生 SQL——原子 UPDATE 与行锁精确控制）。所有语句参数化，
// 列名仅从 StateColumn 白名单取值，禁止拼接任何外部输入（go-dev-standard 规则 8）。
type repository struct {
	db *gorm.DB
}

// insertRetryLimit 行创建竞态重试上限（唯一键冲突 → 重读，收敛到更新路径）。
const insertRetryLimit = 3

// pgUniqueViolation 判断 err 是否为指定约束的 PostgreSQL 唯一冲突（23505）。
func pgUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		(constraint == "" || pgErr.ConstraintName == constraint)
}

// withTx 事务边界：tx 非 nil 直接复用（M2 业务域组合外层事务，plan §8.3）；
// 为 nil 时自建事务，fn 错误整体回滚（architecture.md §4）。
func withTx(ctx context.Context, db *gorm.DB, tx *gorm.DB, fn func(tx *gorm.DB) error) error {
	if tx != nil {
		return fn(tx)
	}
	return db.WithContext(ctx).Transaction(fn)
}

// newBusinessNo 已删除（backend-m2-plan §8.3 条 2 / 判据 5）：LED/ADJ 单号统一经
// internal/docnum 编号引擎发放（business-flow §13.1；writeLedger/writeAdjustment），
// M1"日期+8位随机hex+唯一索引重试"过渡实现随 MT0 移除。

// ---- 库存行 ----

// locateRowForUpdate 按五维唯一键定位库存行并加行锁（plan §8.3：先 SELECT 行 id
// 再条件 UPDATE，禁止"先 SELECT 数量再内存判断后无条件 UPDATE"两段式写法）。
// 行不存在返回 (nil, nil)——由调用方按原语语义处理（不存在报错 / 创建行）。
func locateRowForUpdate(tx *gorm.DB, key RowKey) (*Inventory, error) {
	var row Inventory
	err := tx.Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
		       total_qty, available_qty, locked_qty, frozen_qty,
		       pending_inspect_qty, defective_qty,
		       created_at, updated_at, created_by, updated_by
		FROM inventory
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?
		FOR UPDATE`, key.WarehouseID, key.BinID, key.SKUID, key.BatchID).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// colDelta 单列增量（SET col = col + delta）。仅覆盖五状态列；
// total_qty 不在 StateColumn 值域（流水 status_from/to 的 CHECK 限定五状态），
// 由 applyDelta 的 totalDelta 参数单独承载。
type colDelta struct {
	col   StateColumn
	delta Qty
}

// colGuard 数据层守卫（WHERE col >= min）——影响行数为 0 即失败并回滚
// （inventory-rules §9.3：原子 UPDATE 带数量条件，行数 0 判失败）。
type colGuard struct {
	col StateColumn
	min Qty
}

// applyDelta 对库存行执行原子条件 UPDATE：成对列增减 + total 同步增量保证恒等式
// 由构造成立（backend-m1-plan §8.1：每条 UPDATE 成对改两列、和不变），
// 返回影响行数（0 = 数量条件不满足或行已消失）。
func applyDelta(tx *gorm.DB, id int64, updatedBy int64, totalDelta Qty, deltas []colDelta, guards []colGuard) (int64, error) {
	if len(deltas) == 0 {
		return 0, fmt.Errorf("applyDelta: 缺少列增量（编程错误）")
	}
	set := "updated_at = now(), updated_by = ?"
	args := []any{updatedBy}
	if !totalDelta.IsZero() {
		set += ", total_qty = total_qty + ?"
		args = append(args, totalDelta.String())
	}
	for _, d := range deltas {
		col := d.col.dbColumn()
		if col == "" {
			return 0, fmt.Errorf("applyDelta: 非法状态列（编程错误）")
		}
		set += fmt.Sprintf(", %s = %s + ?", col, col)
		args = append(args, d.delta.String())
	}
	where := "id = ?"
	args = append(args, id)
	for _, g := range guards {
		col := g.col.dbColumn()
		if col == "" {
			return 0, fmt.Errorf("applyDelta: 非法守卫列（编程错误）")
		}
		where += fmt.Sprintf(" AND %s >= ?", col)
		args = append(args, g.min.String())
	}
	res := tx.Exec("UPDATE inventory SET "+set+" WHERE "+where, args...)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// locateRowID 无锁定位库存行 id（多行变更先取 id 排序，再按 id 升序加锁防死锁，
// plan §8.3）。行不存在返回 0。
func locateRowID(tx *gorm.DB, key RowKey) (int64, error) {
	var id int64
	err := tx.Raw(`
		SELECT id FROM inventory
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?`,
		key.WarehouseID, key.BinID, key.SKUID, key.BatchID).Scan(&id).Error
	return id, err
}

// lockRowByID 按 id 取库存行并加行锁（持有至事务结束）。行不存在返回 (nil, nil)。
func lockRowByID(tx *gorm.DB, id int64) (*Inventory, error) {
	return reloadRow(tx, id) // reloadRow 即 SELECT ... FOR UPDATE
}

// insertRow 新建库存行（初始六状态由 init 给定，恒等式由 init 保证）。
// 必须 RETURNING id 带回真实主键：新建行紧接的 applyDelta（WHERE id=?）、
// reloadRow（变更后快照）与审计 ObjectID 都依赖该 ID——无 RETURNING 时 ID 为零值，
// PUTAWAY/盘盈/移库到全新库位会在首条流水处 panic 或 UPDATE 影响 0 行。
// 并发首建竞态：唯一冲突后重读（收敛到 FOR UPDATE 更新路径）。
func insertRow(tx *gorm.DB, key RowKey, actor Actor, init StockState) (*Inventory, error) {
	if err := init.ValidateIdentity(); err != nil {
		return nil, err
	}
	row := &Inventory{
		WarehouseID: key.WarehouseID, ZoneID: key.ZoneID, ShelfID: key.ShelfID,
		BinID: key.BinID, SKUID: key.SKUID, BatchID: key.BatchID,
		TotalQty: init.Total, AvailableQty: init.Available, LockedQty: init.Locked,
		FrozenQty: init.Frozen, PendingInspectQty: init.PendingInspect, DefectiveQty: init.Defective,
		CreatedBy: actor.ID, UpdatedBy: actor.ID,
	}
	var id int64
	err := tx.Raw(`
		INSERT INTO inventory
			(warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
			 total_qty, available_qty, locked_qty, frozen_qty,
			 pending_inspect_qty, defective_qty,
			 created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id`,
		key.WarehouseID, key.ZoneID, key.ShelfID, key.BinID, key.SKUID, key.BatchID,
		init.Total.String(), init.Available.String(), init.Locked.String(), init.Frozen.String(),
		init.PendingInspect.String(), init.Defective.String(),
		actor.ID, actor.ID,
	).Scan(&id).Error
	if err != nil {
		return nil, err
	}
	row.ID = database.ID(id)
	return row, nil
}

// ensureRow 取库存行，不存在时创建（初始状态由 init 给定）。行锁持有至事务结束。
func ensureRow(tx *gorm.DB, key RowKey, actor Actor, init StockState) (row *Inventory, created bool, err error) {
	for range insertRetryLimit {
		row, err = locateRowForUpdate(tx, key)
		if err != nil {
			return nil, false, err
		}
		if row != nil {
			return row, false, nil
		}
		row, err = insertRow(tx, key, actor, init)
		if err == nil {
			return row, true, nil
		}
		if !pgUniqueViolation(err, "uk_inventory_location") {
			return nil, false, err
		}
		// 并发首建竞态：另一事务已插入并提交 → 重读走更新路径。
	}
	return nil, false, fmt.Errorf("inventory: 库存行创建重试耗尽（key=%+v）", key)
}

// reloadRow 重读库存行（同事务、行锁内），用于变更后快照与恒等式断言。
func reloadRow(tx *gorm.DB, id int64) (*Inventory, error) {
	var row Inventory
	err := tx.Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
		       total_qty, available_qty, locked_qty, frozen_qty,
		       pending_inspect_qty, defective_qty,
		       created_at, updated_at, created_by, updated_by
		FROM inventory WHERE id = ? FOR UPDATE`, id).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// ---- 库存流水（append-only：仅 INSERT）----

// insertLedger 写一条库存流水（必须与库存变更同事务，inventory-rules §5.2）。
// 必须 RETURNING id 回填 l.ID：首调路径的 MutationResult.Ledger 与幂等重放路径
// （replayResult/findLockCreationLedger 从 SELECT 回读真实 ID）返回形态一致，
// 否则首调 LedgerRef.ID 恒为 0 而重放为真实 ID。
func insertLedger(tx *gorm.DB, l *InventoryLedger) error {
	var zoneID, shelfID any
	if l.ZoneID != nil && *l.ZoneID != 0 {
		zoneID = *l.ZoneID
	}
	if l.ShelfID != nil && *l.ShelfID != 0 {
		shelfID = *l.ShelfID
	}
	var idem any
	if l.IdempotencyKey != nil && *l.IdempotencyKey != "" {
		idem = *l.IdempotencyKey
	}
	var id int64
	if err := tx.Raw(`
		INSERT INTO inventory_ledgers
			(ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id, serial_no,
			 change_type, business_type, business_no, status_from, status_to,
			 qty_before, qty_change, qty_after, idempotency_key,
			 operator_id, operator_name, request_id, remark, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now())
		RETURNING id`,
		l.LedgerNo, l.SKUID, l.WarehouseID, zoneID, shelfID, l.BinID, l.BatchID, l.SerialNo,
		l.ChangeType, l.BusinessType, l.BusinessNo, l.StatusFrom, l.StatusTo,
		l.QtyBefore.String(), l.QtyChange.String(), l.QtyAfter.String(), idem,
		l.OperatorID, l.OperatorName, l.RequestID, l.Remark,
	).Scan(&id).Error; err != nil {
		return err
	}
	l.ID = database.ID(id)
	return nil
}

// findLedgerByIdempotencyKey 按幂等键查既有流水（plan §8.5：重复 key 返回既有结果）。
func findLedgerByIdempotencyKey(db *gorm.DB, key string) (*InventoryLedger, bool, error) {
	var row InventoryLedger
	err := db.Raw(`
		SELECT id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
		       serial_no, change_type, business_type, business_no,
		       status_from, status_to, qty_before, qty_change, qty_after,
		       idempotency_key, operator_id, operator_name, request_id, remark, created_at
		FROM inventory_ledgers WHERE idempotency_key = ? ORDER BY id LIMIT 1`, key).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// ---- 库存锁定 ----

// insertLock 写锁定记录并返回 ID（inventory-rules §4.1：记录来源/类型/数量/操作人/时间）。
func insertLock(tx *gorm.DB, l *InventoryLock) (int64, error) {
	var id int64
	err := tx.Raw(`
		INSERT INTO inventory_locks
			(warehouse_id, bin_id, sku_id, batch_id, lock_type, source_type, source_no,
			 qty, status, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?, now(), now(), ?, ?)
		RETURNING id`,
		l.WarehouseID, l.BinID, l.SKUID, l.BatchID, l.LockType, l.SourceType, l.SourceNo,
		l.Qty.String(), l.Remark, l.CreatedBy, l.UpdatedBy,
	).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	return id, nil
}

// lockForUpdate 取锁定记录并加行锁（部分释放/核销的串行化点）。
func lockForUpdate(tx *gorm.DB, id int64) (*InventoryLock, error) {
	var row InventoryLock
	err := tx.Raw(`
		SELECT id, warehouse_id, bin_id, sku_id, batch_id, lock_type,
		       source_type, source_no, qty, status, released_at, released_by, remark,
		       created_at, updated_at, created_by, updated_by
		FROM inventory_locks WHERE id = ? FOR UPDATE`, id).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// updateLockAfterSplit 锁记录部分释放/核销后的落库（qty 递减、清零时终态）。
// 状态由 Go 侧基于行锁内读数计算，WHERE 仍带 ACTIVE 守卫（数据层最后防线）。
func updateLockAfterSplit(tx *gorm.DB, row *InventoryLock, releasedBy int64) error {
	res := tx.Exec(`
		UPDATE inventory_locks
		SET qty = ?, status = ?, released_at = ?, released_by = ?,
		    updated_at = now(), updated_by = ?
		WHERE id = ? AND status = 'ACTIVE'`,
		row.Qty.String(), row.Status, row.ReleasedAt, row.ReleasedBy, releasedBy, row.ID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("inventory: 锁定记录 %d 状态并发变化（影响行数 0）", row.ID)
	}
	return nil
}

// findLockBySource 按来源单据 + 锁类型查最近一笔锁记录（幂等重放路径：
// plan §6.2 idx(source_type, source_no) 按来源单据幂等查重）。
func findLockBySource(db *gorm.DB, whID, binID, skuID, batchID int64, lockType, sourceType, sourceNo string) (*InventoryLock, bool, error) {
	var row InventoryLock
	err := db.Raw(`
		SELECT id, warehouse_id, bin_id, sku_id, batch_id, lock_type,
		       source_type, source_no, qty, status, released_at, released_by, remark,
		       created_at, updated_at, created_by, updated_by
		FROM inventory_locks
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?
		  AND lock_type = ? AND source_type = ? AND source_no = ?
		ORDER BY id DESC LIMIT 1`,
		whID, binID, skuID, batchID, lockType, sourceType, sourceNo).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// ---- 调整单 ----

// insertAdjustment 写调整单（status=EXECUTED，执行即落账，M1 无审批流）。
func insertAdjustment(tx *gorm.DB, a *InventoryAdjustment) error {
	return tx.Exec(`		INSERT INTO inventory_adjustments
			(adjustment_no, warehouse_id, sku_id, bin_id, batch_id, adjust_type, qty,
			 reason, status, executed_by, executed_at, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'EXECUTED', ?, now(), now(), now(), ?, ?)`,
		a.AdjustmentNo, a.WarehouseID, a.SKUID, a.BinID, a.BatchID, a.AdjustType, a.Qty.String(),
		a.Reason, a.ExecutedBy, a.CreatedBy, a.UpdatedBy,
	).Error
}

// writeAdjustment 写/落执行调整单并返回 adjustment_no（plan §8.3 条 2/条 3）：
//   - existingID<=0（M1 形态）：经 docnum（ADJ 前缀，ResetAll）发放单号 insertAdjustment
//     （status=EXECUTED）；历史遗留同格式单号撞唯一索引时取下一号重试；
//   - existingID>0（§8.3 条 3，调整审批流复用）：不再插入，按 ID 将既有调整单守卫更新
//     为 EXECUTED（WHERE status='APPROVED'，影响行数 0 = 状态冲突）。
//
// 返回本次落账的调整单单号（Adjust 原语经 MutationResult.AdjustmentNo 透出）。
func writeAdjustment(tx *gorm.DB, a *InventoryAdjustment, existingID, updatedBy int64) (string, error) {
	if existingID > 0 {
		if err := executeAdjustmentByID(tx, existingID, updatedBy); err != nil {
			return "", err
		}
		no, err := findAdjustmentNoByID(tx, existingID)
		if err != nil {
			return "", err
		}
		return no, nil
	}
	rule, _ := docnum.RuleFor("ADJ")
	for range insertRetryLimit {
		no, err := docnum.Next(context.Background(), tx, rule)
		if err != nil {
			return "", err
		}
		a.AdjustmentNo = no
		if err := insertAdjustment(tx, a); err == nil {
			return no, nil
		} else if pgUniqueViolation(err, "uk_inventory_adjustments_no") {
			continue
		} else {
			return "", err
		}
	}
	return "", response.NewError(ErrLedgerNumberConflict, nil)
}

// executeAdjustmentByID 既有调整单执行落账（守卫 UPDATE：APPROVED→EXECUTED，
// business-flow §11.1 执行必须经审批）。
func executeAdjustmentByID(tx *gorm.DB, id, updatedBy int64) error {
	res := tx.Exec(`
		UPDATE inventory_adjustments
		SET status = 'EXECUTED', executed_by = ?, executed_at = now(),
		    updated_at = now(), updated_by = ?
		WHERE id = ? AND status = 'APPROVED'`, updatedBy, updatedBy, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return response.NewError(response.CodeConflict, map[string]any{
			"reason": "调整单不存在或未处于已审核（APPROVED）状态", "adjustment_id": id,
		})
	}
	return nil
}

// findAdjustmentNoByID 回读调整单单号（existingID 路径的结果补全）。
func findAdjustmentNoByID(tx *gorm.DB, id int64) (string, error) {
	var no string
	err := tx.Raw(`SELECT adjustment_no FROM inventory_adjustments WHERE id = ?`, id).Scan(&no).Error
	return no, err
}

// hasActiveCountFreeze 盘点冻结守卫查询：行上是否存在 ACTIVE COUNT_FREEZE 锁
// （business-flow §10.2"冻结范围"；盘点冻结期间 total 恒定的唯一保障点）。
func hasActiveCountFreeze(tx *gorm.DB, key RowKey) (bool, error) {
	var n int64
	err := tx.Raw(`
		SELECT COUNT(*) FROM inventory_locks
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?
		  AND lock_type = 'COUNT_FREEZE' AND status = 'ACTIVE'`,
		key.WarehouseID, key.BinID, key.SKUID, key.BatchID).Scan(&n).Error
	return n > 0, err
}

// ---- 锁定/调整单只读列表（plan §8.3 条 5：GET /api/inventory/locks、/adjustments；
// 前端先行契约 web/src/api/inventory.ts 已调用）----

// lockFilter 锁定记录查询过滤（数据权限仓库集强制收敛）。
type lockFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64
	SKUID         int64
	LockType      string
	Status        string
	SourceType    string
	SourceNo      string
}

func (f lockFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil // fail-closed（permission.md §4）
		}
		where += " AND warehouse_id IN ?"
		args = append(args, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.LockType != "" {
		where += " AND lock_type = ?"
		args = append(args, f.LockType)
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	if f.SourceType != "" {
		where += " AND source_type = ?"
		args = append(args, f.SourceType)
	}
	if f.SourceNo != "" {
		where += " AND source_no = ?"
		args = append(args, f.SourceNo)
	}
	return where, args
}

func (r *repository) listLocks(ctx context.Context, f lockFilter, page, pageSize int) ([]InventoryLock, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM inventory_locks WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []InventoryLock
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, warehouse_id, bin_id, sku_id, batch_id, lock_type,
		       source_type, source_no, qty, status, released_at, released_by, remark,
		       created_at, updated_at, created_by, updated_by
		FROM inventory_locks WHERE `+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

// adjustmentFilter 调整单查询过滤。
type adjustmentFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64
	SKUID         int64
	AdjustType    string
	Status        string
}

func (f adjustmentFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil
		}
		where += " AND warehouse_id IN ?"
		args = append(args, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.AdjustType != "" {
		where += " AND adjust_type = ?"
		args = append(args, f.AdjustType)
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	return where, args
}

func (r *repository) listAdjustments(ctx context.Context, f adjustmentFilter, page, pageSize int) ([]InventoryAdjustment, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM inventory_adjustments WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []InventoryAdjustment
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, adjustment_no, warehouse_id, sku_id, bin_id, batch_id, adjust_type, qty,
		       reason, status, approved_by, approved_at, executed_by, executed_at,
		       created_at, updated_at, created_by, updated_by
		FROM inventory_adjustments WHERE `+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

// ---- 批次 ----

// findBatchByNo 按唯一键 (sku_id, batch_no) 查批次。
func findBatchByNo(tx *gorm.DB, skuID int64, batchNo string) (*Batch, error) {
	var row Batch
	err := tx.Raw(`
		SELECT id, sku_id, batch_no, supplier_id, production_date, inbound_date,
		       expiry_date, cost_price, remark, created_at, updated_at, created_by, updated_by
		FROM batches WHERE sku_id = ? AND batch_no = ?`, skuID, batchNo).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// insertBatch 新建批次（首写为准，不覆盖；unique(sku_id, batch_no) 兜底）。
func insertBatch(tx *gorm.DB, b *Batch) (int64, error) {
	var id int64
	err := tx.Raw(`
		INSERT INTO batches
			(sku_id, batch_no, supplier_id, production_date, inbound_date, expiry_date,
			 cost_price, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id`,
		b.SKUID, b.BatchNo, b.SupplierID, b.ProductionDate, b.InboundDate, b.ExpiryDate,
		b.CostPrice.String(), b.Remark, b.CreatedBy, b.UpdatedBy,
	).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ---- 序列号 ----

// serialForUpdate 按序列号取行并加锁（inventory-rules §8：一物一行）。
func serialForUpdate(tx *gorm.DB, serialNo string) (*SerialNumber, error) {
	var row SerialNumber
	err := tx.Raw(`
		SELECT id, serial_no, sku_id, batch_id, warehouse_id, bin_id, status,
		       last_source_type, last_source_no, last_event_at,
		       created_at, updated_at, created_by, updated_by
		FROM serial_numbers WHERE serial_no = ? FOR UPDATE`, serialNo).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// insertSerial 新建序列号（uk_serial_numbers_serial_no 兜底全局唯一）。
func insertSerial(tx *gorm.DB, s *SerialNumber) error {
	return tx.Exec(`
		INSERT INTO serial_numbers
			(serial_no, sku_id, batch_id, warehouse_id, bin_id, status,
			 last_source_type, last_source_no, last_event_at,
			 created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)`,
		s.SerialNo, s.SKUID, s.BatchID, s.WarehouseID, s.BinID, s.Status,
		s.LastSourceType, s.LastSourceNo, s.LastEventAt, s.CreatedBy, s.UpdatedBy,
	).Error
}

// updateSerial 记录序列号状态变化（每次变化更新 last_source_*/last_event_at：
// 来源单据、位置、操作人、时间，inventory-rules §8.3）。
func updateSerial(tx *gorm.DB, s *SerialNumber) error {
	return tx.Exec(`
		UPDATE serial_numbers
		SET sku_id = ?, batch_id = ?, warehouse_id = ?, bin_id = ?, status = ?,
		    last_source_type = ?, last_source_no = ?, last_event_at = ?,
		    updated_at = now(), updated_by = ?
		WHERE id = ?`,
		s.SKUID, s.BatchID, s.WarehouseID, s.BinID, s.Status,
		s.LastSourceType, s.LastSourceNo, s.LastEventAt, s.UpdatedBy, s.ID,
	).Error
}

// ---- 查询（列表接口，api.md §2.1 强制分页）----

// inventoryFilter 库存列表过滤（五维定位维度，inventory-rules §3）。
type inventoryFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64 // 数据权限仓库集（auth.ApplyWarehouseScope 结果）
	WarehouseID   int64
	ZoneID        int64
	ShelfID       int64
	BinID         int64
	SKUID         int64
	BatchID       *int64
}

func (f inventoryFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil // fail-closed：无可见仓库即无数据（permission.md §4）
		}
		where += " AND warehouse_id IN ?"
		args = append(args, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.ZoneID > 0 {
		where += " AND zone_id = ?"
		args = append(args, f.ZoneID)
	}
	if f.ShelfID > 0 {
		where += " AND shelf_id = ?"
		args = append(args, f.ShelfID)
	}
	if f.BinID > 0 {
		where += " AND bin_id = ?"
		args = append(args, f.BinID)
	}
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.BatchID != nil {
		where += " AND batch_id = ?"
		args = append(args, *f.BatchID)
	}
	return where, args
}

func (r *repository) listInventory(ctx context.Context, f inventoryFilter, page, pageSize int) ([]Inventory, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM inventory WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Inventory
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
		       total_qty, available_qty, locked_qty, frozen_qty,
		       pending_inspect_qty, defective_qty,
		       created_at, updated_at, created_by, updated_by
		FROM inventory WHERE `+where+`
		ORDER BY id
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

func (r *repository) getInventory(ctx context.Context, id int64) (*Inventory, error) {
	var row Inventory
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
		       total_qty, available_qty, locked_qty, frozen_qty,
		       pending_inspect_qty, defective_qty,
		       created_at, updated_at, created_by, updated_by
		FROM inventory WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// ---- 库存层级分布（GET /api/inventory/{id}/distribution，frontend.md §10.4 前端先行契约补齐）----

// stockDistRow 分布扁平行（单查询联 warehouses/zones/bins 取编码/名称，树组装在应用层
// buildStockDistributionTree——纯函数可单测；只读联表仅 SELECT 编码/名称，reports 域读
// inventory 表同口径先例）。LEFT JOIN 保证悬挂引用不丢行（展示降级为空编码）。
type stockDistRow struct {
	WarehouseID   int64  `gorm:"column:warehouse_id"`
	WarehouseCode string `gorm:"column:warehouse_code"`
	WarehouseName string `gorm:"column:warehouse_name"`
	ZoneID        int64  `gorm:"column:zone_id"`
	ZoneCode      string `gorm:"column:zone_code"`
	BinID         int64  `gorm:"column:bin_id"`
	BinCode       string `gorm:"column:bin_code"`
	TotalQty      Qty    `gorm:"column:total_qty"`
	AvailableQty  Qty    `gorm:"column:available_qty"`
}

// stockDistribution 查询该 SKU 在数据权限范围内所有仓库的库存分布扁平行。
// 零量行不进分布树（total_qty > 0：分布回答"库存在哪里"，空仓零位是噪音）；
// Scope 仓库集强制收敛（permission.md §4，禁止接受前端传入仓库范围参数）；
// 排序按 仓库/库区/库位编码 + bin_id 保证同键多批行聚合后输出稳定。
func (r *repository) stockDistribution(ctx context.Context, skuID int64, scope Scope) ([]stockDistRow, error) {
	rows := []stockDistRow{}
	q := `
		SELECT i.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
		       i.zone_id, z.code AS zone_code,
		       i.bin_id, b.code AS bin_code,
		       i.total_qty, i.available_qty
		FROM inventory i
		LEFT JOIN warehouses w ON w.id = i.warehouse_id
		LEFT JOIN zones z ON z.id = i.zone_id
		LEFT JOIN bins b ON b.id = i.bin_id
		WHERE i.sku_id = ? AND i.total_qty > 0`
	args := []any{skuID}
	if !scope.AllWarehouses {
		if len(scope.WarehouseIDs) == 0 {
			return rows, nil
		}
		q += " AND i.warehouse_id IN ?"
		args = append(args, scope.WarehouseIDs)
	}
	q += " ORDER BY w.code, z.code, b.code, i.bin_id"
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ledgerFilter 流水查询过滤（只读审计查询；追加条件与迁移索引对齐）。
type ledgerFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64
	SKUID         int64
	BinID         int64
	BatchID       *int64
	ChangeType    string
	BusinessNo    string
	SerialNo      string
	CreatedFrom   *time.Time
	CreatedTo     *time.Time
}

func (f ledgerFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil
		}
		where += " AND warehouse_id IN ?"
		args = append(args, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.BinID > 0 {
		where += " AND bin_id = ?"
		args = append(args, f.BinID)
	}
	if f.BatchID != nil {
		where += " AND batch_id = ?"
		args = append(args, *f.BatchID)
	}
	if f.ChangeType != "" {
		where += " AND change_type = ?"
		args = append(args, f.ChangeType)
	}
	if f.BusinessNo != "" {
		where += " AND business_no = ?"
		args = append(args, f.BusinessNo)
	}
	if f.SerialNo != "" {
		where += " AND serial_no = ?"
		args = append(args, f.SerialNo)
	}
	if f.CreatedFrom != nil {
		where += " AND created_at >= ?"
		args = append(args, *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		where += " AND created_at <= ?"
		args = append(args, *f.CreatedTo)
	}
	return where, args
}

func (r *repository) listLedgers(ctx context.Context, f ledgerFilter, page, pageSize int) ([]InventoryLedger, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM inventory_ledgers WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []InventoryLedger
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
		       serial_no, change_type, business_type, business_no,
		       status_from, status_to, qty_before, qty_change, qty_after,
		       idempotency_key, operator_id, operator_name, request_id, remark, created_at
		FROM inventory_ledgers WHERE `+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

// batchFilter 批次查询过滤（批次为 SKU 维度台账，仓库无关——inventory-rules §6）。
type batchFilter struct {
	SKUID       int64
	BatchNo     string
	SupplierID  int64
	ExpiryFrom  *time.Time
	ExpiryTo    *time.Time
	ExpiryFirst bool // 按效期升序（NULLS LAST，FEFO 审阅视图）；否则按 id
}

func (f batchFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.BatchNo != "" {
		where += " AND batch_no = ?"
		args = append(args, f.BatchNo)
	}
	if f.SupplierID > 0 {
		where += " AND supplier_id = ?"
		args = append(args, f.SupplierID)
	}
	if f.ExpiryFrom != nil {
		where += " AND expiry_date >= ?"
		args = append(args, *f.ExpiryFrom)
	}
	if f.ExpiryTo != nil {
		where += " AND expiry_date <= ?"
		args = append(args, *f.ExpiryTo)
	}
	return where, args
}

func (r *repository) listBatches(ctx context.Context, f batchFilter, page, pageSize int) ([]Batch, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM batches WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "id"
	if f.ExpiryFirst {
		order = "expiry_date ASC NULLS LAST, id"
	}
	var rows []Batch
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, sku_id, batch_no, supplier_id, production_date, inbound_date,
		       expiry_date, cost_price, remark, created_at, updated_at, created_by, updated_by
		FROM batches WHERE `+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

// serialFilter 序列号查询过滤。
type serialFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	SerialNo      string
	SKUID         int64
	WarehouseID   int64
	BinID         int64
	BatchID       *int64
	Status        string
}

func (f serialFilter) where() (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil
		}
		// 序列号 warehouse_id 0=不在库（退货/出库后），仅过滤在库范围。
		where += " AND (warehouse_id IN ? OR warehouse_id = 0)"
		args = append(args, f.WarehouseIDs)
	}
	if f.SerialNo != "" {
		where += " AND serial_no = ?"
		args = append(args, f.SerialNo)
	}
	if f.SKUID > 0 {
		where += " AND sku_id = ?"
		args = append(args, f.SKUID)
	}
	if f.WarehouseID > 0 {
		where += " AND warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.BinID > 0 {
		where += " AND bin_id = ?"
		args = append(args, f.BinID)
	}
	if f.BatchID != nil {
		where += " AND batch_id = ?"
		args = append(args, *f.BatchID)
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	return where, args
}

func (r *repository) listSerials(ctx context.Context, f serialFilter, page, pageSize int) ([]SerialNumber, int64, error) {
	where, args := f.where()
	var total int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM serial_numbers WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []SerialNumber
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, serial_no, sku_id, batch_id, warehouse_id, bin_id, status,
		       last_source_type, last_source_no, last_event_at,
		       created_at, updated_at, created_by, updated_by
		FROM serial_numbers WHERE `+where+`
		ORDER BY id
		LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}
