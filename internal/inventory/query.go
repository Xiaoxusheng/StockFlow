package inventory

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Service 查询方法（handler → service → repository 分层，architecture.md §1：
// handler 禁止直连数据库；数据权限在 Service 层注入，permission.md §4/api.md §6）。

// Scope 数据权限仓库范围快照（经 auth.WarehouseScope 取得，plan §7.4：
// 禁止接受前端传入的仓库范围参数决定数据可见性）。
type Scope struct {
	AllWarehouses bool
	WarehouseIDs  []int64
}

// InventoryQuery 实时库存查询条件（五维定位维度，inventory-rules §3）。
type InventoryQuery struct {
	Scope       Scope
	WarehouseID int64
	ZoneID      int64
	ShelfID     int64
	BinID       int64
	SKUID       int64
	BatchID     *int64
	Page        int
	PageSize    int
}

// QueryInventory 分页查询实时库存（GET /api/inventory）。
func (s *Service) QueryInventory(ctx context.Context, q InventoryQuery) ([]Inventory, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	return s.repo.listInventory(ctx, inventoryFilter{
		AllWarehouses: q.Scope.AllWarehouses,
		WarehouseIDs:  q.Scope.WarehouseIDs,
		WarehouseID:   q.WarehouseID, ZoneID: q.ZoneID, ShelfID: q.ShelfID,
		BinID: q.BinID, SKUID: q.SKUID, BatchID: q.BatchID,
	}, q.Page, q.PageSize)
}

// GetInventoryDetail 库存行详情（GET /api/inventory/{id}）。
// 数据权限 fail-closed：行仓库不在当前用户范围内按不存在处理（testing.md §8 越权过滤）。
func (s *Service) GetInventoryDetail(ctx context.Context, id int64, scope Scope) (*Inventory, error) {
	if s.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	row, err := s.repo.getInventory(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	if !scope.AllWarehouses && !containsID(scope.WarehouseIDs, row.WarehouseID) {
		return nil, nil
	}
	return row, nil
}

// StockDistributionNode 库存层级分布节点（frontend.md §10.4 / 前端 StockDistributionNode 契约对齐）。
// 判层规则（前端 nodeLevel）：warehouse 层仅 warehouse_code/name；zone 层 + zone_code；
// bin 层再 + bin_code——编码有无即层级标记，故中间层必须回填 warehouse_code。
// total_qty/available_qty 恒输出（Qty 裸数字线格式）；children 仅非末层持有。
type StockDistributionNode struct {
	WarehouseCode string                  `json:"warehouse_code"`
	WarehouseName string                  `json:"warehouse_name,omitempty"`
	ZoneCode      string                  `json:"zone_code,omitempty"`
	BinCode       string                  `json:"bin_code,omitempty"`
	TotalQty      Qty                     `json:"total_qty"`
	AvailableQty  Qty                     `json:"available_qty"`
	Children      []StockDistributionNode `json:"children,omitempty"`
}

// GetStockDistribution 库存层级分布（GET /api/inventory/{id}/distribution，frontend.md §10.4）。
// 语义：入口为库存行 id——按该行 SKU 聚合其在数据权限范围内所有仓库的 仓库→库区→库位 分布
// （详情页头部是行维度五指标，本页签回答"该 SKU 的库存在哪里"，前端空态文案同语境）。
// fail-closed 与 GetInventoryDetail 同口径：入口行不存在或行仓库越权 → (nil, nil) → handler 404；
// 入口行在库但 SKU 无正数库存行 → 空数组（前端呈空态）。
func (s *Service) GetStockDistribution(ctx context.Context, id int64, scope Scope) ([]StockDistributionNode, error) {
	if s.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	row, err := s.repo.getInventory(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	if !scope.AllWarehouses && !containsID(scope.WarehouseIDs, row.WarehouseID) {
		return nil, nil
	}
	rows, err := s.repo.stockDistribution(ctx, row.SKUID, scope)
	if err != nil {
		return nil, err
	}
	return buildStockDistributionTree(rows), nil
}

// buildStockDistributionTree 扁平行 → 仓库→库区→库位 两层树（聚合与排序，纯函数可单测）。
// 同库位多批次行（uk_inventory_location 含 batch 维度）在库位叶聚合——分布维度不含批次
// （批次口径由批次页签承载）；仓库/库区总量=子树求和。输入顺序经 SQL ORDER BY 编码保证稳定。
func buildStockDistributionTree(rows []stockDistRow) []StockDistributionNode {
	whOrder := make([]int64, 0, 8)
	whNodes := make(map[int64]*StockDistributionNode, 8)
	zoneOrderInWh := make(map[int64][]int64, 16)
	zoneNodes := make(map[int64]*StockDistributionNode, 16)
	binOrderInZone := make(map[int64][]int64, 32)
	binNodes := make(map[int64]*StockDistributionNode, 32)

	for _, row := range rows {
		wn, ok := whNodes[row.WarehouseID]
		if !ok {
			wn = &StockDistributionNode{
				WarehouseCode: row.WarehouseCode,
				WarehouseName: row.WarehouseName,
				Children:      []StockDistributionNode{},
			}
			whNodes[row.WarehouseID] = wn
			whOrder = append(whOrder, row.WarehouseID)
		}
		wn.TotalQty = wn.TotalQty.Add(row.TotalQty)
		wn.AvailableQty = wn.AvailableQty.Add(row.AvailableQty)

		zn, ok := zoneNodes[row.ZoneID]
		if !ok {
			zn = &StockDistributionNode{
				WarehouseCode: row.WarehouseCode,
				ZoneCode:      row.ZoneCode,
				Children:      []StockDistributionNode{},
			}
			zoneNodes[row.ZoneID] = zn
			zoneOrderInWh[row.WarehouseID] = append(zoneOrderInWh[row.WarehouseID], row.ZoneID)
		}
		zn.TotalQty = zn.TotalQty.Add(row.TotalQty)
		zn.AvailableQty = zn.AvailableQty.Add(row.AvailableQty)

		bn, ok := binNodes[row.BinID]
		if !ok {
			bn = &StockDistributionNode{
				WarehouseCode: row.WarehouseCode,
				ZoneCode:      row.ZoneCode,
				BinCode:       row.BinCode,
			}
			binNodes[row.BinID] = bn
			binOrderInZone[row.ZoneID] = append(binOrderInZone[row.ZoneID], row.BinID)
		}
		bn.TotalQty = bn.TotalQty.Add(row.TotalQty)
		bn.AvailableQty = bn.AvailableQty.Add(row.AvailableQty)
	}

	tree := make([]StockDistributionNode, 0, len(whOrder))
	for _, whID := range whOrder {
		wn := whNodes[whID]
		for _, zoneID := range zoneOrderInWh[whID] {
			zn := zoneNodes[zoneID]
			for _, binID := range binOrderInZone[zoneID] {
				zn.Children = append(zn.Children, *binNodes[binID])
			}
			wn.Children = append(wn.Children, *zn)
		}
		tree = append(tree, *wn)
	}
	return tree
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// LedgerQuery 流水查询条件（只读审计查询；筛选与迁移索引对齐：sku+created_at、
// warehouse+created_at、business_no）。
type LedgerQuery struct {
	Scope       Scope
	WarehouseID int64
	SKUID       int64
	BinID       int64
	BatchID     *int64
	ChangeType  string
	BusinessNo  string
	SerialNo    string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Page        int
	PageSize    int
}

// QueryLedgers 分页查询库存流水（GET /api/inventory-ledgers，按时间倒序）。
func (s *Service) QueryLedgers(ctx context.Context, q LedgerQuery) ([]InventoryLedger, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	f := ledgerFilter{
		AllWarehouses: q.Scope.AllWarehouses,
		WarehouseIDs:  q.Scope.WarehouseIDs,
		WarehouseID:   q.WarehouseID, SKUID: q.SKUID, BinID: q.BinID, BatchID: q.BatchID,
		ChangeType: q.ChangeType, BusinessNo: q.BusinessNo, SerialNo: q.SerialNo,
	}
	if q.CreatedFrom != nil {
		t := *q.CreatedFrom
		f.CreatedFrom = &t
	}
	if q.CreatedTo != nil {
		t := *q.CreatedTo
		f.CreatedTo = &t
	}
	return s.repo.listLedgers(ctx, f, q.Page, q.PageSize)
}

// BatchQuery 批次查询条件（批次为 SKU 维度台账，仓库无关——inventory-rules §6）。
type BatchQuery struct {
	SKUID       int64
	BatchNo     string
	SupplierID  int64
	ExpiryFrom  *time.Time
	ExpiryTo    *time.Time
	ExpiryFirst bool // 按效期升序（FEFO 审阅视图）
	Page        int
	PageSize    int
}

// QueryBatches 分页查询批次台账（GET /api/batches）。
func (s *Service) QueryBatches(ctx context.Context, q BatchQuery) ([]Batch, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	f := batchFilter{
		SKUID: q.SKUID, BatchNo: q.BatchNo, SupplierID: q.SupplierID, ExpiryFirst: q.ExpiryFirst,
	}
	if q.ExpiryFrom != nil {
		t := *q.ExpiryFrom
		f.ExpiryFrom = &t
	}
	if q.ExpiryTo != nil {
		t := *q.ExpiryTo
		f.ExpiryTo = &t
	}
	return s.repo.listBatches(ctx, f, q.Page, q.PageSize)
}

// SerialQuery 序列号查询条件。
type SerialQuery struct {
	Scope       Scope
	SerialNo    string
	SKUID       int64
	WarehouseID int64
	BinID       int64
	BatchID     *int64
	Status      string
	Page        int
	PageSize    int
}

// QuerySerials 分页查询序列号（GET /api/serials）。
func (s *Service) QuerySerials(ctx context.Context, q SerialQuery) ([]SerialNumber, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	return s.repo.listSerials(ctx, serialFilter{
		AllWarehouses: q.Scope.AllWarehouses,
		WarehouseIDs:  q.Scope.WarehouseIDs,
		SerialNo:      q.SerialNo, SKUID: q.SKUID, WarehouseID: q.WarehouseID,
		BinID: q.BinID, BatchID: q.BatchID, Status: q.Status,
	}, q.Page, q.PageSize)
}

// LockQuery 锁定记录查询条件（GET /api/inventory/locks，plan §8.3 条 5；
// 数据权限 Scope 仓库集强制收敛，permission.md §4）。
type LockQuery struct {
	Scope       Scope
	WarehouseID int64
	SKUID       int64
	LockType    string
	Status      string
	SourceType  string
	SourceNo    string
	Page        int
	PageSize    int
}

// QueryLocks 分页查询库存锁定记录。
func (s *Service) QueryLocks(ctx context.Context, q LockQuery) ([]InventoryLock, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	f := lockFilter{
		AllWarehouses: q.Scope.AllWarehouses,
		WarehouseIDs:  q.Scope.WarehouseIDs,
		WarehouseID:   q.WarehouseID, SKUID: q.SKUID,
		LockType: q.LockType, Status: q.Status,
		SourceType: q.SourceType, SourceNo: q.SourceNo,
	}
	return s.repo.listLocks(ctx, f, q.Page, q.PageSize)
}

// AdjustmentQuery 库存调整单查询条件（GET /api/inventory/adjustments，plan §8.3 条 5）。
type AdjustmentQuery struct {
	Scope       Scope
	WarehouseID int64
	SKUID       int64
	AdjustType  string
	Status      string
	Page        int
	PageSize    int
}

// QueryAdjustments 分页查询库存调整单。
func (s *Service) QueryAdjustments(ctx context.Context, q AdjustmentQuery) ([]InventoryAdjustment, int64, error) {
	if s.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	f := adjustmentFilter{
		AllWarehouses: q.Scope.AllWarehouses,
		WarehouseIDs:  q.Scope.WarehouseIDs,
		WarehouseID:   q.WarehouseID, SKUID: q.SKUID,
		AdjustType: q.AdjustType, Status: q.Status,
	}
	return s.repo.listAdjustments(ctx, f, q.Page, q.PageSize)
}
