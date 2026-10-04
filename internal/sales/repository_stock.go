package sales

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// 仓储实现第二部分：拣货/复核任务、打包、发货单，以及对库存族表的只读 SELECT
// （分配候选与序列号校验；plan §2.3 判据 3 读侧口径——仅 SELECT、自带扫描结构体、
// 不引用库存族 GORM 模型；库存写路径只能经 StockGateway 原语）。

// ---- 拣货任务 ----

func (r *gormRepository) InsertPickTasks(tx *gorm.DB, tasks []PickTask) error {
	for i := range tasks {
		t := &tasks[i]
		err := tx.Raw(`
			INSERT INTO pick_tasks
				(pick_no, outbound_no, outbound_line_no, sku_id, batch_id,
				 source_warehouse_id, source_zone_id, source_shelf_id, source_bin_id,
				 qty, picked_qty, status, assignee_id, assignee_name, warehouse_id, remark,
				 created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, '', ?, ?, now(), now(), ?, ?)
			RETURNING id, created_at, updated_at`,
			t.PickNo, t.OutboundNo, t.OutboundLineNo, t.SKUID, t.BatchID,
			t.SourceWarehouseID, t.SourceZoneID, t.SourceShelfID, t.SourceBinID,
			t.Qty.String(), t.Status, t.WarehouseID, t.Remark, t.CreatedBy, t.UpdatedBy,
		).Scan(t).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *gormRepository) GetPickTask(tx *gorm.DB, id int64) (*PickTask, error) {
	var t PickTask
	err := tx.Raw(`SELECT * FROM pick_tasks WHERE id = ?`, id).Scan(&t).Error
	if err != nil {
		return nil, err
	}
	if t.ID == 0 {
		return nil, nil
	}
	return &t, nil
}

// ClaimPickTask 原子抢占（architecture.md §5.2）：WHERE id AND status='PENDING'，
// 影响行数 0 = 领取冲突（并发互斥由数据库行级 UPDATE 原子性保证，inventory-rules §9.2）。
func (r *gormRepository) ClaimPickTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error) {
	res := tx.Exec(`
		UPDATE pick_tasks SET status = ?, assignee_id = ?, assignee_name = ?,
		       claimed_at = now(), updated_at = now(), updated_by = ?
		WHERE id = ? AND status = ?`,
		PickStatusClaimed, assigneeID, assigneeName, assigneeID, id, PickStatusPending)
	return res.RowsAffected, res.Error
}

// MarkPickTaskStatus 拣货任务守卫迁移（确认/上报/取消）。
func (r *gormRepository) MarkPickTaskStatus(tx *gorm.DB, id int64, from, to string, stamp PickStamp) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, stamp.By}
	if stamp.PickedQty != nil {
		set += ", picked_qty = ?"
		args = append(args, stamp.PickedQty.String())
	}
	if stamp.ScannedCode != nil {
		set += ", scanned_code = ?"
		args = append(args, *stamp.ScannedCode)
	}
	if stamp.StampPickedAt {
		set += ", picked_at = now()"
	}
	args = append(args, id, from)
	res := tx.Exec("UPDATE pick_tasks SET "+set+" WHERE id = ? AND status = ?", args...)
	return res.RowsAffected, res.Error
}

// PickStamp 拣货任务迁移附带列。
type PickStamp struct {
	By            int64
	PickedQty     *Qty
	ScannedCode   *string
	StampPickedAt bool
}

// CancelPickTasks 取消任务的 PENDING/CLAIMED 行（出库单取消 / 行重分配联动）；
// lineNo=0 表示整单。已 PICKED/EXCEPTION 的任务不动（历史留痕）。
func (r *gormRepository) CancelPickTasks(tx *gorm.DB, outboundNo string, lineNo int64) error {
	res := tx.Exec(`
		UPDATE pick_tasks SET status = ?, updated_at = now()
		WHERE outbound_no = ? AND (? = 0 OR outbound_line_no = ?)
		  AND status IN (?, ?)`,
		PickStatusCancelled, outboundNo, lineNo, lineNo, PickStatusPending, PickStatusClaimed)
	return res.Error
}

func (r *gormRepository) ListPickTasksByOutbound(tx *gorm.DB, outboundNo string) ([]PickTask, error) {
	var rows []PickTask
	err := tx.Raw(`SELECT * FROM pick_tasks WHERE outbound_no = ? ORDER BY id`, outboundNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListPickTasks(ctx context.Context, q PickQuery) ([]PickTask, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&PickTask{}), "warehouse_id", q.Scope)
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.OutboundNo != "" {
		db = db.Where("outbound_no = ?", q.OutboundNo)
	}
	if q.AssigneeID > 0 {
		db = db.Where("assignee_id = ?", q.AssigneeID)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PickTask
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

// ---- 复核任务 ----

func (r *gormRepository) InsertCheckTasks(tx *gorm.DB, tasks []CheckTask) error {
	for i := range tasks {
		t := &tasks[i]
		err := tx.Raw(`
			INSERT INTO check_tasks
				(check_no, outbound_no, outbound_line_no, sku_id, batch_id, serial_no, qty,
				 status, result, assignee_id, assignee_name, warehouse_id, remark,
				 created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', 0, '', ?, ?, now(), now(), ?, ?)
			RETURNING id, created_at, updated_at`,
			t.CheckNo, t.OutboundNo, t.OutboundLineNo, t.SKUID, t.BatchID, t.SerialNo,
			t.Qty.String(), t.Status, t.WarehouseID, t.Remark, t.CreatedBy, t.UpdatedBy,
		).Scan(t).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *gormRepository) GetCheckTask(tx *gorm.DB, id int64) (*CheckTask, error) {
	var t CheckTask
	err := tx.Raw(`SELECT * FROM check_tasks WHERE id = ?`, id).Scan(&t).Error
	if err != nil {
		return nil, err
	}
	if t.ID == 0 {
		return nil, nil
	}
	return &t, nil
}

// AssignCheckTask 复核任务原子指派：check_tasks 状态值域无 CLAIMED（000008 CHECK 冻结），
// 领取语义 = 不迁移状态的原子指派（WHERE status='PENDING' 影响行数 0 = 冲突），
// 与拣货抢占同为数据库行级 UPDATE 原子性保证的并发互斥（architecture.md §5.2）。
func (r *gormRepository) AssignCheckTask(tx *gorm.DB, id, assigneeID int64, assigneeName string) (int64, error) {
	res := tx.Exec(`
		UPDATE check_tasks SET assignee_id = ?, assignee_name = ?,
		       claimed_at = now(), updated_at = now(), updated_by = ?
		WHERE id = ? AND status = ?`,
		assigneeID, assigneeName, assigneeID, id, CheckStatusPending)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) MarkCheckTaskStatus(tx *gorm.DB, id int64, from, to, result string) (int64, error) {
	res := tx.Exec(`
		UPDATE check_tasks SET status = ?, result = ?, done_at = now(), updated_at = now(), updated_by = (
			SELECT assignee_id FROM check_tasks WHERE id = ?
		) WHERE id = ? AND status = ?`,
		to, result, id, id, from)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) ListCheckTasksByOutbound(tx *gorm.DB, outboundNo string) ([]CheckTask, error) {
	var rows []CheckTask
	err := tx.Raw(`SELECT * FROM check_tasks WHERE outbound_no = ? ORDER BY id`, outboundNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListCheckTasks(ctx context.Context, q CheckQuery) ([]CheckTask, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&CheckTask{}), "warehouse_id", q.Scope)
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.OutboundNo != "" {
		db = db.Where("outbound_no = ?", q.OutboundNo)
	}
	if q.AssigneeID > 0 {
		db = db.Where("assignee_id = ?", q.AssigneeID)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CheckTask
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

// ---- 打包 ----

func (r *gormRepository) FindPackageByIdempotencyKey(tx *gorm.DB, key string) (*PackingRecord, error) {
	var p PackingRecord
	err := tx.Raw(`SELECT * FROM packing_records WHERE idempotency_key = ?`, key).Scan(&p).Error
	if err != nil {
		return nil, err
	}
	if p.ID == 0 {
		return nil, nil
	}
	return &p, nil
}

func (r *gormRepository) InsertPackage(tx *gorm.DB, p *PackingRecord, items []PackingItem) error {
	var idem any
	if p.IdempotencyKey != nil {
		idem = *p.IdempotencyKey
	}
	err := tx.Raw(`
		INSERT INTO packing_records
			(package_no, outbound_no, packing_material, length, width, height, weight, volume,
			 carrier, tracking_no, warehouse_id, idempotency_key, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id, created_at, updated_at`,
		p.PackageNo, p.OutboundNo, p.PackingMaterial, p.Length.String(), p.Width.String(),
		p.Height.String(), p.Weight.String(), p.Volume.String(),
		p.Carrier, p.TrackingNo, p.WarehouseID, idem, p.Remark, p.CreatedBy, p.UpdatedBy,
	).Scan(p).Error
	if err != nil {
		return err
	}
	for i := range items {
		it := &items[i]
		err := tx.Raw(`
			INSERT INTO packing_items
				(package_id, outbound_id, line_no, qty, remark, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, now(), now(), ?, ?)
			RETURNING id`,
			p.ID.Int64(), it.OutboundID, it.LineNo, it.Qty.String(), it.Remark, p.CreatedBy, p.UpdatedBy,
		).Scan(it).Error
		if err != nil {
			return err
		}
		it.PackageID = p.ID.Int64()
	}
	return nil
}

// SumPackedByLine 汇总各明细行已打包数量（packing_items 聚合，业务校验数据源）。
func (r *gormRepository) SumPackedByLine(tx *gorm.DB, outboundID int64) (map[int64]Qty, error) {
	var rows []struct {
		LineNo int64
		Qty    Qty
	}
	err := tx.Raw(`
		SELECT pi.line_no, COALESCE(SUM(pi.qty), 0) AS qty
		FROM packing_items pi
		WHERE pi.outbound_id = ?
		GROUP BY pi.line_no`, outboundID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Qty, len(rows))
	for _, row := range rows {
		out[row.LineNo] = row.Qty
	}
	return out, nil
}

func (r *gormRepository) CountPackagesByOutbound(tx *gorm.DB, outboundNo string) (int64, error) {
	var n int64
	err := tx.Raw(`SELECT COUNT(*) FROM packing_records WHERE outbound_no = ?`, outboundNo).Scan(&n).Error
	return n, err
}

func (r *gormRepository) ListPackagesByOutbound(tx *gorm.DB, outboundNo string) ([]PackingRecord, error) {
	var rows []PackingRecord
	err := tx.Raw(`SELECT * FROM packing_records WHERE outbound_no = ? ORDER BY id`, outboundNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListPackages(ctx context.Context, q PackingQuery) ([]PackingRecord, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&PackingRecord{}), "warehouse_id", q.Scope)
	if q.OutboundNo != "" {
		db = db.Where("outbound_no = ?", q.OutboundNo)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PackingRecord
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

// ---- 发货单 ----

func (r *gormRepository) FindShipmentByIdempotencyKey(tx *gorm.DB, key string) (*Shipment, error) {
	var s Shipment
	err := tx.Raw(`SELECT * FROM shipments WHERE idempotency_key = ?`, key).Scan(&s).Error
	if err != nil {
		return nil, err
	}
	if s.ID == 0 {
		return nil, nil
	}
	return &s, nil
}

func (r *gormRepository) InsertShipment(tx *gorm.DB, s *Shipment) error {
	var idem any
	if s.IdempotencyKey != nil {
		idem = *s.IdempotencyKey
	}
	return tx.Raw(`
		INSERT INTO shipments
			(shipment_no, outbound_no, carrier, tracking_no, warehouse_id, shipper_id, shipper_name,
			 package_count, status, idempotency_key, remark, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?)
		RETURNING id, created_at, updated_at`,
		s.ShipmentNo, s.OutboundNo, s.Carrier, s.TrackingNo, s.WarehouseID,
		s.ShipperID, s.ShipperName, s.PackageCount, s.Status, idem, s.Remark, s.CreatedBy, s.UpdatedBy,
	).Scan(s).Error
}

func (r *gormRepository) GetShipment(tx *gorm.DB, id int64) (*Shipment, error) {
	var s Shipment
	err := tx.Raw(`SELECT * FROM shipments WHERE id = ?`, id).Scan(&s).Error
	if err != nil {
		return nil, err
	}
	if s.ID == 0 {
		return nil, nil
	}
	return &s, nil
}

func (r *gormRepository) MarkShipmentStatus(tx *gorm.DB, id int64, from, to string, stamp StatusStamp) (int64, error) {
	set := "status = ?, updated_at = now(), updated_by = ?"
	args := []any{to, stamp.By}
	if stamp.Shipped {
		set += ", shipped_at = now()"
	}
	args = append(args, id, from)
	res := tx.Exec("UPDATE shipments SET "+set+" WHERE id = ? AND status = ?", args...)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) ListShipmentsByOutbound(tx *gorm.DB, outboundNo string) ([]Shipment, error) {
	var rows []Shipment
	err := tx.Raw(`SELECT * FROM shipments WHERE outbound_no = ? ORDER BY id`, outboundNo).Scan(&rows).Error
	return rows, err
}

func (r *gormRepository) ListShipments(ctx context.Context, q ShipmentQuery) ([]Shipment, int64, error) {
	db := applyScope(r.db.WithContext(ctx).Model(&Shipment{}), "warehouse_id", q.Scope)
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.OutboundNo != "" {
		db = db.Where("outbound_no = ?", q.OutboundNo)
	}
	if q.WarehouseID > 0 {
		db = db.Where("warehouse_id = ?", q.WarehouseID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Shipment
	err := db.Order("id DESC").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error
	return rows, total, err
}

// ---- 库存族只读 SELECT（plan §2.3 判据 3 读侧口径） ----

// BatchCandidate 批次分配候选（聚合自 inventory JOIN batches；自带扫描结构体，
// 非库存族 GORM 模型）。AvailableQty = 该批次在指定仓库内所有库位 available_qty 之和。
type BatchCandidate struct {
	BatchID      int64
	BatchNo      string
	ExpiryDate   *database.JSONTime
	InboundDate  *database.JSONTime
	AvailableQty Qty
}

// ReadBatchCandidates 批次候选（business-flow §8.1 策略输入；只读）。
func (r *gormRepository) ReadBatchCandidates(tx *gorm.DB, warehouseID, skuID int64) ([]BatchCandidate, error) {
	var rows []BatchCandidate
	err := tx.Raw(`
		SELECT i.batch_id,
		       COALESCE(b.batch_no, '') AS batch_no,
		       b.expiry_date, b.inbound_date,
		       COALESCE(SUM(i.available_qty), 0) AS available_qty
		FROM inventory i
		LEFT JOIN batches b ON b.id = i.batch_id
		WHERE i.warehouse_id = ? AND i.sku_id = ? AND i.batch_id > 0 AND i.available_qty > 0
		GROUP BY i.batch_id, b.batch_no, b.expiry_date, b.inbound_date`,
		warehouseID, skuID).Scan(&rows).Error
	return rows, err
}

// BinStock 库位级库存（自带扫描结构体；只读）。分配锁定量 = 分配量（plan §6.4）。
type BinStock struct {
	WarehouseID  int64
	ZoneID       int64
	ShelfID      int64
	BinID        int64
	SKUID        int64
	BatchID      int64
	AvailableQty Qty
}

// ReadBinStock 读取指定仓库 + SKU（+ 批次）有可用量的库位行，按 bin_id 升序
// （确定性消耗顺序；zone/shelf 随 bin 冗余回填，供拣货任务来源四维定位）。
func (r *gormRepository) ReadBinStock(tx *gorm.DB, warehouseID, skuID, batchID int64) ([]BinStock, error) {
	var rows []BinStock
	err := tx.Raw(`
		SELECT warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id, available_qty
		FROM inventory
		WHERE warehouse_id = ? AND sku_id = ? AND batch_id = ? AND available_qty > 0
		ORDER BY bin_id`,
		warehouseID, skuID, batchID).Scan(&rows).Error
	return rows, err
}

// ReadBinLocation 读取指定库存行的 zone/shelf 冗余定位（不限可用量——已被锁定的行
// 仍需为拣货任务回填来源四维；found=false 表示行不存在）。只读。
func (r *gormRepository) ReadBinLocation(tx *gorm.DB, warehouseID, binID, skuID, batchID int64) (zoneID, shelfID int64, found bool, err error) {
	var row struct {
		ZoneID  int64
		ShelfID int64
	}
	res := tx.Raw(`
		SELECT zone_id, shelf_id FROM inventory
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?`,
		warehouseID, binID, skuID, batchID).Scan(&row)
	if res.Error != nil {
		return 0, 0, false, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, 0, false, nil
	}
	return row.ZoneID, row.ShelfID, true, nil
}

// SerialState 序列号当前状态（自带扫描结构体；只读——发货前的可出库校验数据源）。
type SerialState struct {
	SerialNo    string
	SKUID       int64
	BatchID     int64
	WarehouseID int64
	BinID       int64
	Status      string
}

// ReadSerialStates 按序列号集合读取台账状态（inventory-rules §8：出库必须逐序列号校验）。
func (r *gormRepository) ReadSerialStates(tx *gorm.DB, skuID int64, serials []string) ([]SerialState, error) {
	if len(serials) == 0 {
		return nil, nil
	}
	var rows []SerialState
	err := tx.Raw(`SELECT serial_no, sku_id, batch_id, warehouse_id, bin_id, status
		FROM serial_numbers WHERE sku_id = ? AND serial_no IN ?`, skuID, serials).Scan(&rows).Error
	return rows, err
}
