package stockops

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/stock"
)

// Repository 契约（architecture.md §1：Repository 无业务判断）。Store 提供事务边界，
// Tx 为事务内数据访问面——生产实现 gormStore（同一 GORM 事务贯穿单据状态迁移、
// 审批/审计与库存原语组合，backend-m2-plan §10.1），单测实现 memStore（内存替身，
// 零 PostgreSQL/Redis 依赖，参照 internal/masterdata、internal/inventory 替身模式）。

// Store 事务边界。
type Store interface {
	// WithinTx 单事务执行 fn（architecture §4：单据状态迁移 + 库存原语 + 审批 +
	// 审计整体 COMMIT/ROLLBACK）。fn 收到的事务句柄即 Tx。
	WithinTx(ctx context.Context, fn func(Tx) error) error
}

// Tx 事务内数据访问面。GormDB 返回底层 GORM 事务句柄（传给库存原语/编号引擎组合，
// backend-m2-plan §8.3"业务单据状态更新 + 库存变更同一外层事务"；内存替身返回 nil，
// 由替身网关忽略）。
type Tx interface {
	GormDB() *gorm.DB

	// Audit 业务同事务写 operation_logs（middleware.Audit 的 Tx 适配：审计不可缺位，
	// 失败即业务失败整体回滚）。
	Audit(entry middleware.AuditEntry) error

	// —— 调拨单 ——

	// InsertTransfer 创建调拨单 + 明细（同事务；o.ID 回填）。
	InsertTransfer(ctx context.Context, o *TransferOrder, items []TransferItem) error
	// GetTransfer 按 ID 取单（含数据权限过滤后的调用方判定；不存在的语义由调用方定）。
	GetTransfer(ctx context.Context, id int64) (*TransferOrder, error)
	// ListTransfers 分页列表（f 携带数据权限仓库范围，permission.md §4）。
	ListTransfers(ctx context.Context, f TransferFilter, page, pageSize int) ([]TransferOrder, int64, error)
	// ReplaceTransferItems 整单替换明细（仅 DRAFT 修改；守卫由 Service 状态迁移承担）。
	ReplaceTransferItems(ctx context.Context, transferID int64, items []TransferItem) error
	// ListTransferItems 明细（按 line_no 升序）。
	ListTransferItems(ctx context.Context, transferID int64) ([]TransferItem, error)
	// UpdateTransferStatus 状态机守卫迁移：UPDATE ... SET status=?, <stamps 环节时间列>=now()
	// WHERE id=? AND status=?（business-flow §13.2：禁止跳状态；影响行数 0 = 状态冲突）。
	UpdateTransferStatus(ctx context.Context, id int64, from, to string, stamps TransferStamps, actorID int64) (int64, error)
	// BumpTransferItemOut / BumpTransferItemIn 出库/收货进度落行（qty_out/qty_in += n）。
	BumpTransferItemOut(ctx context.Context, itemID int64, qty stock.Qty) error
	BumpTransferItemIn(ctx context.Context, itemID int64, qty stock.Qty) error
	// InsertApproval 审批记录（document_approvals，append-only）。
	InsertApproval(ctx context.Context, rec ApprovalRecord) error

	// —— 盘点单 ——

	// InsertCount 创建盘点单（DRAFT；o.ID 回填）。
	InsertCount(ctx context.Context, o *CountOrder) error
	GetCount(ctx context.Context, id int64) (*CountOrder, error)
	ListCounts(ctx context.Context, f CountFilter, page, pageSize int) ([]CountOrder, int64, error)
	// UpdateCountStatus 状态机守卫迁移（同 UpdateTransferStatus 口径）。
	UpdateCountStatus(ctx context.Context, id int64, from, to string, stamps CountStamps, actorID int64) (int64, error)
	// ReplaceCountItems 冻结快照写入（start 事务内一次性覆盖该单明细——该单此前无明细）。
	ReplaceCountItems(ctx context.Context, countID int64, items []CountItem) error
	// ListCountItems 明细（按 inventory_row_id, serial_no 升序）。
	ListCountItems(ctx context.Context, countID int64) ([]CountItem, error)
	// UpsertCountRegistrations 实盘登记（PUT 幂等覆盖既有行；序列号新增行 = 盘盈多出件，
	// qty_system=0）。返回登记后的该单全部明细。
	UpsertCountRegistrations(ctx context.Context, countID int64, actor stock.Actor, regs []CountRegistration) ([]CountItem, error)
	// ReplaceCountDifferences 差异生成（finish 事务内一次性写入——该单此前无差异行）。
	ReplaceCountDifferences(ctx context.Context, countID int64, diffs []CountDifference) error
	ListCountDifferences(ctx context.Context, countID int64) ([]CountDifference, error)
	// SettleCountDifferences 差异行落定：status 迁移（PENDING→EXECUTED/REJECTED，
	// WHERE status='PENDING' 守卫）+ adjust_no 回写。
	SettleCountDifferences(ctx context.Context, countID int64, to string, adjustNos map[int64]string) error

	// —— 库存域只读编排（跨域窄接口的 MT5 收编候选，见文件头说明）——

	// FindInventoryRows 枚举盘点范围内库存行（只读 SELECT inventory，扫描进本地结构，
	// 不引用库存族 GORM 模型、无写通路；按 id 升序——plan §10.1 多行操作加锁顺序）。
	FindInventoryRows(ctx context.Context, warehouseID int64, scope CountScope) ([]InventoryRowRef, error)
	// GetInventoryRow 单行重读（差异生成/调整 RowKey 组装）。
	GetInventoryRow(ctx context.Context, id int64) (*InventoryRowRef, error)
	// FindRowLocks 按行维度查活跃锁（盘点互斥判定：同行已有 COUNT_FREEZE 冻结）。
	FindRowLocks(ctx context.Context, row InventoryRowRef, lockType string) ([]ActiveLockRef, error)
	// FindSourceLocks 按来源单据查活跃锁（调拨出库取预占锁 / 取消释放 / 盘点解冻）。
	FindSourceLocks(ctx context.Context, sourceType, sourceNo, lockType string) ([]ActiveLockRef, error)
	// FindSerialsForRow 行维度在库序列号（盘点逐件冻结明细，inventory-rules §8.2）。
	FindSerialsForRow(ctx context.Context, row InventoryRowRef) ([]SerialRef, error)
	// FindSerialsInBin 源库位在库序列号（调拨出库逐件转在途；按 id 升序取 limit 件）。
	FindSerialsInBin(ctx context.Context, warehouseID, binID, skuID, batchID int64, limit int64) ([]SerialRef, error)
	// FindSerialsInTransit 本调拨单转在途的序列号（到货回件定位）。
	FindSerialsInTransit(ctx context.Context, sourceNo string, skuID int64) ([]SerialRef, error)
	// FindSerialByNo 序列号档案（登记时点校验归属 SKU）。
	FindSerialByNo(ctx context.Context, serialNo string) (*SerialRef, error)
	// FindAdjustmentNo 回查最近执行的调整单号（count_differences.adjust_no 回写；
	// Adjust 原语未返回 adjustment_no——见 count.go 完成流程注释与交付偏差说明）。
	FindAdjustmentNo(ctx context.Context, warehouseID, binID, skuID, batchID int64, adjustType string, qty stock.Qty) (string, error)

	// —— 在途聚合（inventory-rules §2 在途口径 = TRANSFERRING 单据 qty_out - qty_in）——
	ListInTransit(ctx context.Context, f InTransitFilter, page, pageSize int) ([]InTransitRow, int64, error)
}

// Scope 数据权限仓库范围快照（经 auth.WarehouseScope 取得，permission.md §4；
// 禁止接受前端传入的仓库范围参数决定数据可见性）。
type Scope struct {
	AllWarehouses bool
	WarehouseIDs  []int64
}

// TransferStamps 状态迁移附带环节时间戳（business-flow §13.4 业务时间字段；
// bool = 是否写该环节时间，approved_by 随 Approve 写入）。
type TransferStamps struct {
	Approve    bool
	ApprovedBy int64
	Outbound   bool
	Received   bool
	Cancelled  bool
}

// CountStamps 盘点单环节时间戳。
type CountStamps struct {
	Frozen    bool
	Reviewed  bool
	Completed bool
	Cancelled bool
}

// TransferFilter 调拨列表过滤（数据权限：from/to 任一端在范围内即可见——
// 源仓作业员可见自己发出的调拨；plan §10.5 双端仓库均为检索条件）。
type TransferFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64 // 业务筛选：匹配 from 或 to
	ToWarehouseID int64 // 业务筛选：精确目标仓
	Status        string
	Type          string
	TransferNo    string
}

// CountFilter 盘点列表过滤。
type CountFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64
	Status        string
	CountNo       string
}

// InTransitFilter 在途聚合过滤（warehouse_id 为空时聚合全局在途）。
type InTransitFilter struct {
	AllWarehouses bool
	WarehouseIDs  []int64
	WarehouseID   int64
	SKUID         int64
}

// InventoryRowRef 库存行只读引用（SELECT inventory 扫描目标；不含任何写通路）。
//
// 列名一律显式 gorm column tag：GORM v1.31 NamingStrategy 的 commonInitialisms 含 ID
// 不含 SKU，SKUID 会被推导成 sk_uid（≠ 表列 sku_id）；Total/Available/Locked/Frozen/
// PendingInspect/Defective 亦推导成 total/available/...（≠ 表列 *_qty）。缺 tag 时
// Raw(...).Scan 对该列静默落零值（无报错）——曾致盘点冻结空转、快照/差异零值与 complete 400。
type InventoryRowRef struct {
	ID             int64     `gorm:"column:id"`
	WarehouseID    int64     `gorm:"column:warehouse_id"`
	ZoneID         int64     `gorm:"column:zone_id"`
	ShelfID        int64     `gorm:"column:shelf_id"`
	BinID          int64     `gorm:"column:bin_id"`
	SKUID          int64     `gorm:"column:sku_id"`
	BatchID        int64     `gorm:"column:batch_id"`
	Total          stock.Qty `gorm:"column:total_qty"`
	Available      stock.Qty `gorm:"column:available_qty"`
	Locked         stock.Qty `gorm:"column:locked_qty"`
	Frozen         stock.Qty `gorm:"column:frozen_qty"`
	PendingInspect stock.Qty `gorm:"column:pending_inspect_qty"`
	Defective      stock.Qty `gorm:"column:defective_qty"`
}

// Key 转换为库存原语五维定位键。
func (r InventoryRowRef) Key() stock.RowKey {
	return stock.RowKey{
		WarehouseID: r.WarehouseID, ZoneID: r.ZoneID, ShelfID: r.ShelfID,
		BinID: r.BinID, SKUID: r.SKUID, BatchID: r.BatchID,
	}
}

// ActiveLockRef 活跃锁只读引用（inventory_locks SELECT 扫描目标）。
// SKUID 必须显式 column tag（同 InventoryRowRef 说明）：否则推导为 sk_uid 落零值，
// 调拨出库的锁行匹配 l.SKUID == it.SKUID 恒为 0==sku_id 假 → 409 STOCKOPS_LOCK_MISSING。
type ActiveLockRef struct {
	ID          int64     `gorm:"column:id"`
	SourceNo    string    `gorm:"column:source_no"`
	SourceType  string    `gorm:"column:source_type"`
	Qty         stock.Qty `gorm:"column:qty"`
	WarehouseID int64     `gorm:"column:warehouse_id"`
	BinID       int64     `gorm:"column:bin_id"`
	SKUID       int64     `gorm:"column:sku_id"`
	BatchID     int64     `gorm:"column:batch_id"`
}

// SerialRef 序列号只读引用（serial_numbers SELECT 扫描目标；SKUID 同上）。
type SerialRef struct {
	ID       int64  `gorm:"column:id"`
	SerialNo string `gorm:"column:serial_no"`
	SKUID    int64  `gorm:"column:sku_id"`
	BatchID  int64  `gorm:"column:batch_id"`
}

// CountRegistration 实盘登记输入（PUT 幂等：同 (row, serial) 覆盖）。
type CountRegistration struct {
	InventoryRowID int64
	SerialNo       string
	Qty            stock.Qty
}

// InTransitRow 在途聚合行（batch_id 0=非批次）。
// SKUID 必须显式 column tag（同 SerialRef 说明）——否则在途列表 sku_id 全落 0。
type InTransitRow struct {
	SKUID      int64     `gorm:"column:sku_id" json:"sku_id"`
	BatchID    int64     `gorm:"column:batch_id" json:"batch_id"`
	OutTransit stock.Qty `gorm:"column:out_transit" json:"out_transit"` // 自源仓已出未收（源仓视角在途）
	InTransit  stock.Qty `gorm:"column:in_transit" json:"in_transit"`   // 向目标仓在途（目标仓视角在途）
}
