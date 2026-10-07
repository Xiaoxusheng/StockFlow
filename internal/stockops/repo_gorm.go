package stockops

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// gormStore Repository 生产实现（原生 SQL，全部参数化；列名仅来自代码内白名单，
// 禁止拼接任何外部输入——go-dev-standard 规则 8）。业务判断在 Service 层（architecture §1）。
type gormStore struct {
	db *gorm.DB
}

// newGormStore 构造生产存储。
func newGormStore(db *gorm.DB) Store { return &gormStore{db: db} }

// WithinTx 单事务执行（architecture §4：单据状态迁移 + 库存原语 + 审批 + 审计整体
// COMMIT/ROLLBACK）。嵌套调用（不存在于本域）由 GORM savepoint 承载。
func (s *gormStore) WithinTx(ctx context.Context, fn func(Tx) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormTx{tx: tx})
	})
}

// gormTx 事务内数据访问面。
type gormTx struct {
	tx *gorm.DB
}

// GormDB 返回底层事务句柄（库存原语/编号引擎组合用）。
func (t *gormTx) GormDB() *gorm.DB { return t.tx }

// Audit operation_logs 同事务写入（审计不可缺位，失败即业务失败）。
func (t *gormTx) Audit(entry middleware.AuditEntry) error {
	return middleware.Audit(t.tx, entry)
}

// ---- 调拨单 ----

const transferOrderCols = `id, transfer_no, type, from_warehouse_id, to_warehouse_id, status,
	approved_by, approved_at, outbound_at, received_at, cancelled_at, remark,
	created_at, updated_at, created_by, updated_by`

const transferItemCols = `id, transfer_id, line_no, sku_id, batch_id,
	from_warehouse_id, from_zone_id, from_shelf_id, from_bin_id,
	to_warehouse_id, to_zone_id, to_shelf_id, to_bin_id,
	qty, qty_out, qty_in, remark, created_at, updated_at, created_by, updated_by`

func (t *gormTx) InsertTransfer(ctx context.Context, o *TransferOrder, items []TransferItem) error {
	tx := t.tx.WithContext(ctx)
	var id int64
	if err := tx.Raw(`
		INSERT INTO transfer_orders
			(transfer_no, type, from_warehouse_id, to_warehouse_id, status, remark,
			 created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id`,
		o.TransferNo, o.Type, o.FromWarehouseID, o.ToWarehouseID, o.Status, o.Remark,
		o.CreatedBy, o.UpdatedBy,
	).Scan(&id).Error; err != nil {
		return err
	}
	o.ID = database.ID(id)
	// 明细批量写入（f6 同款：原逐行 INSERT RETURNING，一次往返改多行 VALUES）：
	// PG 对简单 INSERT 按行序返回 RETURNING id，逐行回填 it.ID 语义不变。
	if len(items) > 0 {
		var (
			sb   strings.Builder
			args = make([]any, 0, len(items)*17)
		)
		sb.WriteString(`
			INSERT INTO transfer_items
				(transfer_id, line_no, sku_id, batch_id,
				 from_warehouse_id, from_zone_id, from_shelf_id, from_bin_id,
				 to_warehouse_id, to_zone_id, to_shelf_id, to_bin_id,
				 qty, qty_out, qty_in, remark, created_at, updated_at, created_by, updated_by)
			VALUES `)
		for i := range items {
			it := &items[i]
			it.TransferID = id
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, now(), now(), ?, ?)")
			args = append(args, it.TransferID, it.LineNo, it.SKUID, it.BatchID,
				it.FromWarehouseID, it.FromZoneID, it.FromShelfID, it.FromBinID,
				it.ToWarehouseID, it.ToZoneID, it.ToShelfID, it.ToBinID,
				it.Qty.String(), it.QtyOut.String(), it.Remark, it.CreatedBy, it.UpdatedBy)
		}
		sb.WriteString(` RETURNING id`)
		var ids []int64
		if err := tx.Raw(sb.String(), args...).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) != len(items) {
			return fmt.Errorf("stockops: 调拨明细批量写入回执数不符: %d/%d", len(ids), len(items))
		}
		for i := range items {
			items[i].ID = database.ID(ids[i])
		}
	}
	return nil
}

func (t *gormTx) GetTransfer(ctx context.Context, id int64) (*TransferOrder, error) {
	var o TransferOrder
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+transferOrderCols+` FROM transfer_orders WHERE id = ?`, id).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func (t *gormTx) ReplaceTransferItems(ctx context.Context, transferID int64, items []TransferItem) error {
	tx := t.tx.WithContext(ctx)
	if err := tx.Exec(`DELETE FROM transfer_items WHERE transfer_id = ?`, transferID).Error; err != nil {
		return err
	}
	// 明细批量写入（f6 同款：一次往返多行 VALUES，PG 按插入序返回 id 逐行回填）。
	if len(items) > 0 {
		var (
			sb   strings.Builder
			args = make([]any, 0, len(items)*16)
		)
		sb.WriteString(`
			INSERT INTO transfer_items
				(transfer_id, line_no, sku_id, batch_id,
				 from_warehouse_id, from_zone_id, from_shelf_id, from_bin_id,
				 to_warehouse_id, to_zone_id, to_shelf_id, to_bin_id,
				 qty, qty_out, qty_in, remark, created_at, updated_at, created_by, updated_by)
			VALUES `)
		for i := range items {
			it := &items[i]
			it.TransferID = transferID
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, now(), now(), ?, ?)")
			args = append(args, it.TransferID, it.LineNo, it.SKUID, it.BatchID,
				it.FromWarehouseID, it.FromZoneID, it.FromShelfID, it.FromBinID,
				it.ToWarehouseID, it.ToZoneID, it.ToShelfID, it.ToBinID,
				it.Qty.String(), it.Remark, it.CreatedBy, it.UpdatedBy)
		}
		sb.WriteString(` RETURNING id`)
		var ids []int64
		if err := tx.Raw(sb.String(), args...).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) != len(items) {
			return fmt.Errorf("stockops: 调拨明细覆盖批量写入回执数不符: %d/%d", len(ids), len(items))
		}
		for i := range items {
			items[i].ID = database.ID(ids[i])
		}
	}
	return nil
}

func (t *gormTx) ListTransferItems(ctx context.Context, transferID int64) ([]TransferItem, error) {
	var rows []TransferItem
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+transferItemCols+` FROM transfer_items WHERE transfer_id = ? ORDER BY line_no`,
		transferID).Scan(&rows).Error
	return rows, err
}

// UpdateTransferStatus 状态机守卫迁移（business-flow §13.2：所有状态变化必须带
// WHERE status = 前置态并判定影响行数；时间列名来自代码内白名单）。
func (t *gormTx) UpdateTransferStatus(ctx context.Context, id int64, from, to string, stamps TransferStamps, actorID int64) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, actorID}
	if stamps.Approve {
		set += ", approved_at = now(), approved_by = ?"
		args = append(args, stamps.ApprovedBy)
	}
	if stamps.Outbound {
		set += ", outbound_at = now()"
	}
	if stamps.Received {
		set += ", received_at = now()"
	}
	if stamps.Cancelled {
		set += ", cancelled_at = now()"
	}
	args = append(args, id, from)
	res := t.tx.WithContext(ctx).Exec(
		`UPDATE transfer_orders SET `+set+` WHERE id = ? AND status = ?`, args...)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func (t *gormTx) BumpTransferItemOut(ctx context.Context, itemID int64, qty stock.Qty) error {
	return t.bumpItem(ctx, itemID, "qty_out", qty)
}

func (t *gormTx) BumpTransferItemIn(ctx context.Context, itemID int64, qty stock.Qty) error {
	return t.bumpItem(ctx, itemID, "qty_in", qty)
}

// bumpItem 明细进度列增量（列名白名单；guard 更新生效行数由调用方语义保证——
// 进度列与状态迁移同事务，状态守卫已串行化并发）。
func (t *gormTx) bumpItem(ctx context.Context, itemID int64, col string, qty stock.Qty) error {
	if col != "qty_out" && col != "qty_in" {
		return fmt.Errorf("stockops: 非法明细进度列（编程错误）: %s", col)
	}
	res := t.tx.WithContext(ctx).Exec(
		`UPDATE transfer_items SET `+col+` = `+col+` + ?, updated_at = now() WHERE id = ?`,
		qty.String(), itemID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("stockops: 调拨明细 %d 不存在", itemID)
	}
	return nil
}

func (t *gormTx) InsertApproval(ctx context.Context, rec ApprovalRecord) error {
	return t.tx.WithContext(ctx).Exec(`
		INSERT INTO document_approvals
			(target_type, target_no, action, result, opinion, operator_id, operator_name, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, now())`,
		rec.TargetType, rec.TargetNo, rec.Action, rec.Result, rec.Opinion,
		rec.OperatorID, rec.OperatorName, rec.RequestID).Error
}

// transferFilterWhere 列表过滤（数据权限 fail-closed：无可见仓库即无数据，permission.md §4）。
func transferFilterWhere(f TransferFilter) (string, []any) {
	where := "1 = 1"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil
		}
		where += " AND (from_warehouse_id IN ? OR to_warehouse_id IN ?)"
		args = append(args, f.WarehouseIDs, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND (from_warehouse_id = ? OR to_warehouse_id = ?)"
		args = append(args, f.WarehouseID, f.WarehouseID)
	}
	if f.ToWarehouseID > 0 {
		where += " AND to_warehouse_id = ?"
		args = append(args, f.ToWarehouseID)
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	if f.Type != "" {
		where += " AND type = ?"
		args = append(args, f.Type)
	}
	if f.TransferNo != "" {
		where += " AND transfer_no = ?"
		args = append(args, f.TransferNo)
	}
	return where, args
}

func (t *gormTx) ListTransfers(ctx context.Context, f TransferFilter, page, pageSize int) ([]TransferOrder, int64, error) {
	tx := t.tx.WithContext(ctx)
	where, args := transferFilterWhere(f)
	var total int64
	if err := tx.Raw("SELECT COUNT(*) FROM transfer_orders WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []TransferOrder
	err := tx.Raw(`SELECT `+transferOrderCols+` FROM transfer_orders WHERE `+where+`
		ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

// ---- 盘点单 ----

const countOrderCols = `id, count_no, warehouse_id, scope, status,
	frozen_at, reviewed_at, completed_at, cancelled_at, remark,
	created_at, updated_at, created_by, updated_by`

const countItemCols = `id, count_id, inventory_row_id, sku_id, warehouse_id, zone_id, shelf_id, bin_id,
	qty_system, qty_counted, counted_by, counted_at, serial_no, created_at, updated_at, created_by, updated_by`

const countDiffCols = `id, count_id, line_no, sku_id, warehouse_id, bin_id, batch_id,
	qty_system, qty_counted, diff_qty, adjust_no, status, remark, created_at, updated_at, created_by, updated_by`

func (t *gormTx) InsertCount(ctx context.Context, o *CountOrder) error {
	var id int64
	err := t.tx.WithContext(ctx).Raw(`
		INSERT INTO count_orders
			(count_no, warehouse_id, scope, status, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id`,
		o.CountNo, o.WarehouseID, o.Scope, o.Status, o.Remark, o.CreatedBy, o.UpdatedBy,
	).Scan(&id).Error
	if err != nil {
		return err
	}
	o.ID = database.ID(id)
	return nil
}

func (t *gormTx) GetCount(ctx context.Context, id int64) (*CountOrder, error) {
	var o CountOrder
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+countOrderCols+` FROM count_orders WHERE id = ?`, id).Scan(&o).Error
	if err != nil {
		return nil, err
	}
	if o.ID == 0 {
		return nil, nil
	}
	return &o, nil
}

func countFilterWhere(f CountFilter) (string, []any) {
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
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	if f.CountNo != "" {
		where += " AND count_no = ?"
		args = append(args, f.CountNo)
	}
	return where, args
}

func (t *gormTx) ListCounts(ctx context.Context, f CountFilter, page, pageSize int) ([]CountOrder, int64, error) {
	tx := t.tx.WithContext(ctx)
	where, args := countFilterWhere(f)
	var total int64
	if err := tx.Raw("SELECT COUNT(*) FROM count_orders WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CountOrder
	err := tx.Raw(`SELECT `+countOrderCols+` FROM count_orders WHERE `+where+`
		ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}

func (t *gormTx) UpdateCountStatus(ctx context.Context, id int64, from, to string, stamps CountStamps, actorID int64) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, actorID}
	if stamps.Frozen {
		set += ", frozen_at = now()"
	}
	if stamps.Reviewed {
		set += ", reviewed_at = now()"
	}
	if stamps.Completed {
		set += ", completed_at = now()"
	}
	if stamps.Cancelled {
		set += ", cancelled_at = now()"
	}
	args = append(args, id, from)
	res := t.tx.WithContext(ctx).Exec(
		`UPDATE count_orders SET `+set+` WHERE id = ? AND status = ?`, args...)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ReplaceCountItems 覆盖写盘点明细（StartCount 冻结快照）。
// f6：原实现逐行 INSERT RETURNING（范围行数 = SQL 往返数），改为分块多行
// INSERT ... RETURNING id（PG 按插入序返回，逐行回填 it.ID 语义不变）。
func (t *gormTx) ReplaceCountItems(ctx context.Context, countID int64, items []CountItem) error {
	tx := t.tx.WithContext(ctx)
	if err := tx.Exec(`DELETE FROM count_items WHERE count_id = ?`, countID).Error; err != nil {
		return err
	}
	// 分块大小：13 参数/行 × 1000 行 = 1.3 万 < 65535（PG 协议参数上限）。
	const chunk = 1000
	for start := 0; start < len(items); start += chunk {
		end := start + chunk
		if end > len(items) {
			end = len(items)
		}
		batch := items[start:end]
		var (
			sb   strings.Builder
			args = make([]any, 0, len(batch)*13)
		)
		sb.WriteString(`
			INSERT INTO count_items
				(count_id, inventory_row_id, sku_id, warehouse_id, zone_id, shelf_id, bin_id,
				 qty_system, qty_counted, counted_by, counted_at, serial_no,
				 created_at, updated_at, created_by, updated_by)
			VALUES `)
		for j := range batch {
			it := &batch[j]
			it.CountID = countID
			if j > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, '', now(), now(), ?, ?)")
			args = append(args, it.CountID, it.InventoryRowID, it.SKUID, it.WarehouseID,
				it.ZoneID, it.ShelfID, it.BinID, it.QtySystem.String(), it.QtyCounted,
				it.CreatedBy, it.UpdatedBy)
		}
		sb.WriteString(` RETURNING id`)
		var ids []int64
		if err := tx.Raw(sb.String(), args...).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) != len(batch) {
			return fmt.Errorf("stockops: 盘点明细批量写入回执数不符: %d/%d", len(ids), len(batch))
		}
		for j := range batch {
			batch[j].ID = database.ID(ids[j])
		}
	}
	return nil
}

func (t *gormTx) ListCountItems(ctx context.Context, countID int64) ([]CountItem, error) {
	var rows []CountItem
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+countItemCols+` FROM count_items WHERE count_id = ?
		ORDER BY inventory_row_id, serial_no`, countID).Scan(&rows).Error
	return rows, err
}

// UpsertCountRegistrations 实盘登记（PUT 幂等覆盖；序列号新增行 = 盘盈多出件
// qty_system=0，唯一键 count_id+inventory_row_id+serial_no 兜底并发）。
func (t *gormTx) UpsertCountRegistrations(ctx context.Context, countID int64, actor stock.Actor, regs []CountRegistration) ([]CountItem, error) {
	tx := t.tx.WithContext(ctx)
	for _, reg := range regs {
		// 既有行 PUT 覆盖（幂等；WHERE count_id 双保险——登记行必须属于该单）。
		res := tx.Exec(`
			UPDATE count_items
			SET qty_counted = ?, counted_by = ?, counted_at = now(), updated_at = now(), updated_by = ?
			WHERE count_id = ? AND inventory_row_id = ? AND serial_no = ?`,
			reg.Qty.String(), actor.ID, actor.ID, countID, reg.InventoryRowID, reg.SerialNo)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			// 新增行：仅序列号维度（普通行冻结时必已建行）——盘盈多出件。
			if err := insertCountItem(tx, countID, actor, reg); err != nil {
				return nil, err
			}
		}
	}
	return t.ListCountItems(ctx, countID)
}

// insertCountItem 新增盘盈多出件明细行（qty_system=0）。
func insertCountItem(tx *gorm.DB, countID int64, actor stock.Actor, reg CountRegistration) error {
	var row InventoryRowRef
	if err := tx.Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id
		FROM inventory WHERE id = ?`, reg.InventoryRowID).Scan(&row).Error; err != nil {
		return err
	}
	if row.ID == 0 {
		return response.NewError(ErrCountItemForeign, map[string]any{"inventory_row_id": reg.InventoryRowID})
	}
	var itemID int64
	err := tx.Raw(`
		INSERT INTO count_items
			(count_id, inventory_row_id, sku_id, warehouse_id, zone_id, shelf_id, bin_id,
			 qty_system, qty_counted, counted_by, counted_at, serial_no,
			 created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, now(), ?, now(), now(), ?, ?)
		RETURNING id`,
		countID, reg.InventoryRowID, row.SKUID, row.WarehouseID, row.ZoneID, row.ShelfID, row.BinID,
		reg.Qty.String(), actor.ID, reg.SerialNo, actor.ID, actor.ID,
	).Scan(&itemID).Error
	return err
}

func (t *gormTx) ReplaceCountDifferences(ctx context.Context, countID int64, diffs []CountDifference) error {
	tx := t.tx.WithContext(ctx)
	if err := tx.Exec(`DELETE FROM count_differences WHERE count_id = ?`, countID).Error; err != nil {
		return err
	}
	// 差异批量写入（f6 同款：一次往返多行 VALUES，PG 按插入序返回 id 逐行回填）。
	if len(diffs) > 0 {
		var (
			sb   strings.Builder
			args = make([]any, 0, len(diffs)*14)
		)
		sb.WriteString(`
			INSERT INTO count_differences
				(count_id, line_no, sku_id, warehouse_id, bin_id, batch_id,
				 qty_system, qty_counted, diff_qty, adjust_no, status, remark,
				 created_at, updated_at, created_by, updated_by)
			VALUES `)
		for i := range diffs {
			d := &diffs[i]
			d.CountID = countID
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, now(), now(), ?, ?)")
			args = append(args, d.CountID, d.LineNo, d.SKUID, d.WarehouseID, d.BinID, d.BatchID,
				d.QtySystem.String(), d.QtyCounted.String(), d.DiffQty.String(), d.Status, d.Remark,
				d.CreatedBy, d.UpdatedBy)
		}
		sb.WriteString(` RETURNING id`)
		var ids []int64
		if err := tx.Raw(sb.String(), args...).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) != len(diffs) {
			return fmt.Errorf("stockops: 盘点差异批量写入回执数不符: %d/%d", len(ids), len(diffs))
		}
		for i := range diffs {
			diffs[i].ID = database.ID(ids[i])
		}
	}
	return nil
}

func (t *gormTx) ListCountDifferences(ctx context.Context, countID int64) ([]CountDifference, error) {
	var rows []CountDifference
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+countDiffCols+` FROM count_differences WHERE count_id = ? ORDER BY line_no`,
		countID).Scan(&rows).Error
	return rows, err
}

// SettleCountDifferences 差异行落定（PENDING→EXECUTED/REJECTED，守卫迁移；
// adjustNos 为差异 ID → 调整单单号回写）。
func (t *gormTx) SettleCountDifferences(ctx context.Context, countID int64, to string, adjustNos map[int64]string) error {
	tx := t.tx.WithContext(ctx)
	for diffID, adjustNo := range adjustNos {
		res := tx.Exec(`
			UPDATE count_differences
			SET status = ?, adjust_no = ?, updated_at = now()
			WHERE id = ? AND count_id = ? AND status = ?`,
			to, adjustNo, diffID, countID, DiffPending)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "差异行状态已变化", "difference_id": diffID,
			})
		}
	}
	if to == DiffRejected {
		// 驳回：剩余 PENDING 行全部落 REJECTED（一次审核动作对整单差异生效）。
		if err := tx.Exec(`
			UPDATE count_differences SET status = ?, updated_at = now()
			WHERE count_id = ? AND status = ?`, DiffRejected, countID, DiffPending).Error; err != nil {
			return err
		}
	}
	return nil
}

// ---- 库存域只读编排 ----
//
// 说明：以下均为只读 SELECT（无任何写通路、不引用库存族 GORM 模型、扫描进本包
// 本地结构）。backend-m2-plan §3.1 规划此类跨域读取走"消费方窄接口 + 实现方导出 +
// router 注入"，但实现方（internal/inventory）的查询导出属集成工程师 MT0 §8.3 条 5
// 范围且本工程师无权在 inventory 新增查询文件（任务书仅豁免 transfer.go）——
// 故以本域内只读编排落地并在交付偏差中披露，MT5 可无损收编（语句与本文件自包含）。

const inventoryRowCols = `id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
	total_qty, available_qty, locked_qty, frozen_qty, pending_inspect_qty, defective_qty`

// FindInventoryRows 枚举盘点范围内库存行（按 id 升序——plan §10.1 多行操作加锁顺序）。
func (t *gormTx) FindInventoryRows(ctx context.Context, warehouseID int64, scope CountScope) ([]InventoryRowRef, error) {
	where := "warehouse_id = ?"
	args := []any{warehouseID}
	if len(scope.ZoneIDs) > 0 {
		where += " AND zone_id IN ?"
		args = append(args, scope.ZoneIDs)
	}
	if len(scope.ShelfIDs) > 0 {
		where += " AND shelf_id IN ?"
		args = append(args, scope.ShelfIDs)
	}
	if len(scope.BinIDs) > 0 {
		where += " AND bin_id IN ?"
		args = append(args, scope.BinIDs)
	}
	if len(scope.SKUIDs) > 0 {
		where += " AND sku_id IN ?"
		args = append(args, scope.SKUIDs)
	}
	var rows []InventoryRowRef
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+inventoryRowCols+` FROM inventory WHERE `+where+` ORDER BY id`, args...).Scan(&rows).Error
	return rows, err
}

// GetInventoryRow 单行重读（差异生成/调整 RowKey 组装）。
func (t *gormTx) GetInventoryRow(ctx context.Context, id int64) (*InventoryRowRef, error) {
	var row InventoryRowRef
	err := t.tx.WithContext(ctx).Raw(
		`SELECT `+inventoryRowCols+` FROM inventory WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

const activeLockCols = `id, source_type, source_no, qty, warehouse_id, bin_id, sku_id, batch_id`

// FindRowLocks 按行维度查活跃锁（盘点互斥判定）。
func (t *gormTx) FindRowLocks(ctx context.Context, row InventoryRowRef, lockType string) ([]ActiveLockRef, error) {
	var rows []ActiveLockRef
	err := t.tx.WithContext(ctx).Raw(`
		SELECT `+activeLockCols+` FROM inventory_locks
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?
		  AND lock_type = ? AND status = 'ACTIVE'
		ORDER BY id`,
		row.WarehouseID, row.BinID, row.SKUID, row.BatchID, lockType).Scan(&rows).Error
	return rows, err
}

// FindSourceLocks 按来源单据查活跃锁。
func (t *gormTx) FindSourceLocks(ctx context.Context, sourceType, sourceNo, lockType string) ([]ActiveLockRef, error) {
	var rows []ActiveLockRef
	err := t.tx.WithContext(ctx).Raw(`
		SELECT `+activeLockCols+` FROM inventory_locks
		WHERE source_type = ? AND source_no = ? AND lock_type = ? AND status = 'ACTIVE'
		ORDER BY id`,
		sourceType, sourceNo, lockType).Scan(&rows).Error
	return rows, err
}

// FindSerialsForRow 行维度在库序列号（盘点逐件冻结明细）。
func (t *gormTx) FindSerialsForRow(ctx context.Context, row InventoryRowRef) ([]SerialRef, error) {
	var rows []SerialRef
	q := `
		SELECT id, serial_no, sku_id, batch_id FROM serial_numbers
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND status = 'IN_STOCK'`
	args := []any{row.WarehouseID, row.BinID, row.SKUID}
	if row.BatchID > 0 {
		q += ` AND batch_id = ?`
		args = append(args, row.BatchID)
	}
	err := t.tx.WithContext(ctx).Raw(q+` ORDER BY id`, args...).Scan(&rows).Error
	return rows, err
}

// FindSerialsInBin 源库位在库序列号（调拨出库逐件转在途；按 id 升序取 limit 件）。
func (t *gormTx) FindSerialsInBin(ctx context.Context, warehouseID, binID, skuID, batchID int64, limit int64) ([]SerialRef, error) {
	q := `
		SELECT id, serial_no, sku_id, batch_id FROM serial_numbers
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND status = 'IN_STOCK'`
	args := []any{warehouseID, binID, skuID}
	if batchID > 0 {
		q += ` AND batch_id = ?`
		args = append(args, batchID)
	}
	var rows []SerialRef
	err := t.tx.WithContext(ctx).Raw(q+` ORDER BY id LIMIT ?`, append(args, limit)...).Scan(&rows).Error
	return rows, err
}

// FindSerialsInTransit 本调拨单转在途的序列号（last_source 指针定位，inventory-rules §8.3）。
func (t *gormTx) FindSerialsInTransit(ctx context.Context, sourceNo string, skuID int64) ([]SerialRef, error) {
	var rows []SerialRef
	err := t.tx.WithContext(ctx).Raw(`
		SELECT id, serial_no, sku_id, batch_id FROM serial_numbers
		WHERE last_source_type = 'transfer_order' AND last_source_no = ? AND sku_id = ?
		  AND status = 'OUTBOUND'
		ORDER BY id`, sourceNo, skuID).Scan(&rows).Error
	return rows, err
}

// FindSerialByNo 序列号档案（登记时点校验归属 SKU）。
func (t *gormTx) FindSerialByNo(ctx context.Context, serialNo string) (*SerialRef, error) {
	var row SerialRef
	err := t.tx.WithContext(ctx).Raw(`
		SELECT id, serial_no, sku_id, batch_id FROM serial_numbers WHERE serial_no = ?`,
		serialNo).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// FindAdjustmentNo 回查最近执行的调整单号（同事务内可见本事务刚写入的调整单；
// Adjust 原语未返回 adjustment_no——M1 形态，见交付偏差说明，MT0 §8.3.3 落地后切换）。
func (t *gormTx) FindAdjustmentNo(ctx context.Context, warehouseID, binID, skuID, batchID int64, adjustType string, qty stock.Qty) (string, error) {
	var no string
	err := t.tx.WithContext(ctx).Raw(`
		SELECT adjustment_no FROM inventory_adjustments
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?
		  AND adjust_type = ? AND qty = ? AND status = 'EXECUTED'
		ORDER BY id DESC LIMIT 1`,
		warehouseID, binID, skuID, batchID, adjustType, qty.String()).Scan(&no).Error
	return no, err
}

// ---- 在途聚合 ----

func inTransitFilterWhere(f InTransitFilter) (string, []any, error) {
	where := "o.status = 'TRANSFERRING'"
	var args []any
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return "1 = 0", nil, nil
		}
		where += " AND (o.from_warehouse_id IN ? OR o.to_warehouse_id IN ?)"
		args = append(args, f.WarehouseIDs, f.WarehouseIDs)
	}
	if f.WarehouseID > 0 {
		where += " AND (o.from_warehouse_id = ? OR o.to_warehouse_id = ?)"
		args = append(args, f.WarehouseID, f.WarehouseID)
	}
	if f.SKUID > 0 {
		where += " AND ti.sku_id = ?"
		args = append(args, f.SKUID)
	}
	return where, args, nil
}

// ListInTransit 在途聚合（inventory-rules §2 在途口径：TRANSFERRING 单据的
// qty_out - qty_in 聚合；指定仓库时按源/目标方向分列，未指定时两列同值 = 全局在途）。
func (t *gormTx) ListInTransit(ctx context.Context, f InTransitFilter, page, pageSize int) ([]InTransitRow, int64, error) {
	tx := t.tx.WithContext(ctx)
	where, args, err := inTransitFilterWhere(f)
	if err != nil {
		return nil, 0, err
	}
	whID := f.WarehouseID
	outExpr := fmt.Sprintf("SUM(CASE WHEN o.from_warehouse_id = %d THEN ti.qty_out - ti.qty_in ELSE 0 END)", whID)
	inExpr := fmt.Sprintf("SUM(CASE WHEN o.to_warehouse_id = %d THEN ti.qty_out - ti.qty_in ELSE 0 END)", whID)
	if whID == 0 {
		// 全局在途：方向无意义，两列同值（qty_out - qty_in）。
		outExpr, inExpr = "SUM(ti.qty_out - ti.qty_in)", "SUM(ti.qty_out - ti.qty_in)"
	}
	base := `FROM transfer_items ti JOIN transfer_orders o ON o.id = ti.transfer_id WHERE ` + where
	var total int64
	if err := tx.Raw(`SELECT COUNT(*) FROM (
		SELECT ti.sku_id, ti.batch_id `+base+` GROUP BY ti.sku_id, ti.batch_id) s`, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []InTransitRow
	err = tx.Raw(`SELECT ti.sku_id, ti.batch_id,
		COALESCE(`+outExpr+`, 0) AS out_transit,
		COALESCE(`+inExpr+`, 0) AS in_transit
		`+base+`
		GROUP BY ti.sku_id, ti.batch_id ORDER BY ti.sku_id, ti.batch_id
		LIMIT ? OFFSET ?`, append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error
	return rows, total, err
}
