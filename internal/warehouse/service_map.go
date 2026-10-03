package warehouse

import (
	"context"
	"sort"

	"github.com/stockflow/server/internal/response"
)

// 库位地图（plan §5.4：GET /api/warehouses/{id}/map——按仓库返回区/架/位网格与
// 占用状态，供 requirements.md §2.6 仓库可视化使用；占用数据经 §4.3 注入的
// BinOccupancyReader 由 inventory 聚合）。
//
// 输出为全量层级树（区 → 架 → 位），非分页接口——地图按物理布局整体渲染，
// 行数受物理规模约束（architecture.md §7 的"禁无条件全量查询"针对任意列表，
// 本接口为可视化专用聚合，且必须先通过仓库范围校验）。
// 三次批量查询组装（区/架/位各一次），禁止循环查库（architecture.md §7）。

// 库位占用显示状态（requirements.md §2.6 可视化状态机；由本域可得的两种事实源推导：
// 容量占用 current_capacity/max_capacity 与注入的库存锁定数量）。
const (
	OccIdle    = "IDLE"    // 空闲
	OccPartial = "PARTIAL" // 部分占用
	OccFull    = "FULL"    // 满载
	OccLocked  = "LOCKED"  // 存在锁定/冻结中的库存（优先于容量状态显示）
)

// BinMapCell 地图库位格：库位视图 + 占用状态。quantity/locked_quantity 仅在
// BinOccupancyReader 已注入时返回（不造假数据，requirements.md §10）。
type BinMapCell struct {
	BinView
	OccupancyStatus string   `json:"occupancy_status"`
	Quantity        *float64 `json:"quantity,omitempty"`
	LockedQuantity  *float64 `json:"locked_quantity,omitempty"`
}

// ShelfMapNode 地图货架节点。
type ShelfMapNode struct {
	Shelf ShelfView    `json:"shelf"`
	Bins  []BinMapCell `json:"bins"`
}

// ZoneMapNode 地图库区节点。
type ZoneMapNode struct {
	Zone    ZoneView       `json:"zone"`
	Shelves []ShelfMapNode `json:"shelves"`
}

// WarehouseMapView 仓库地图。
type WarehouseMapView struct {
	Warehouse WarehouseView `json:"warehouse"`
	Zones     []ZoneMapNode `json:"zones"`
}

// GetWarehouseMap 仓库库位地图。
func (s *Service) GetWarehouseMap(ctx context.Context, scope Scope, warehouseID int64) (*WarehouseMapView, error) {
	wh, err := s.repo.FindWarehouseByID(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	if wh == nil {
		return nil, response.NewError(ErrWarehouseNotFound, nil)
	}
	if err := checkScope(scope, idOf(wh.ID)); err != nil {
		return nil, err
	}

	zones, err := s.repo.ListZonesByWarehouse(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	shelves, err := s.repo.ListShelvesByWarehouse(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	bins, err := s.repo.ListBinsByWarehouse(ctx, warehouseID)
	if err != nil {
		return nil, err
	}

	// 库存占用（可选注入）：按仓库一次批量读取，reader 失败即整图失败
	// （不静默降级出假"空闲"图）。
	quantity, locked := map[int64]float64{}, map[int64]float64{}
	hasOcc := false
	if s.occ != nil {
		q, l, err := s.occ.OccupancyByWarehouse(ctx, warehouseID)
		if err != nil {
			// 占用数据不可用：整图失败而非静默渲染全"空闲"（禁止假数据，requirements.md §10）。
			return nil, response.NewError(response.CodeServiceUnavailable, map[string]any{
				"reason": "库存占用数据不可用",
			})
		}
		hasOcc = true
		if q != nil {
			quantity = q
		}
		if l != nil {
			locked = l
		}
	}

	binsByShelf := make(map[int64][]BinMapCell, len(bins))
	for _, b := range bins {
		cell := BinMapCell{BinView: viewBin(b)}
		if hasOcc {
			q, lok := quantity[idOf(b.ID)]
			l, lok2 := locked[idOf(b.ID)]
			if lok || lok2 {
				qv, lv := q, l
				cell.Quantity = &qv
				cell.LockedQuantity = &lv
			}
		}
		cell.OccupancyStatus = occupancyStatus(b, cell.LockedQuantity)
		binsByShelf[idOf(b.ShelfID)] = append(binsByShelf[idOf(b.ShelfID)], cell)
	}

	shelvesByZone := make(map[int64][]ShelfMapNode, len(shelves))
	for _, sh := range shelves {
		node := ShelfMapNode{Shelf: viewShelf(sh), Bins: binsByShelf[idOf(sh.ID)]}
		if node.Bins == nil {
			node.Bins = []BinMapCell{}
		}
		shelvesByZone[idOf(sh.ZoneID)] = append(shelvesByZone[idOf(sh.ZoneID)], node)
	}

	out := &WarehouseMapView{Warehouse: viewWarehouse(wh), Zones: make([]ZoneMapNode, 0, len(zones))}
	for _, z := range zones {
		node := ZoneMapNode{Zone: viewZone(z), Shelves: shelvesByZone[idOf(z.ID)]}
		if node.Shelves == nil {
			node.Shelves = []ShelfMapNode{}
		}
		out.Zones = append(out.Zones, node)
	}

	// 稳定排序：区/架按编码，位按层/列（repo 已排序，此处对树内分组的相邻性兜底）。
	sort.Slice(out.Zones, func(i, j int) bool { return out.Zones[i].Zone.Code < out.Zones[j].Zone.Code })
	for zi := range out.Zones {
		zs := out.Zones[zi].Shelves
		sort.Slice(zs, func(i, j int) bool { return zs[i].Shelf.Code < zs[j].Shelf.Code })
		for si := range zs {
			bs := zs[si].Bins
			sort.Slice(bs, func(i, j int) bool {
				if bs[i].Layer != bs[j].Layer {
					return bs[i].Layer < bs[j].Layer
				}
				return bs[i].ColumnNo < bs[j].ColumnNo
			})
		}
	}
	return out, nil
}

// occupancyStatus 占用状态推导（纯函数）：锁定优先，其次按容量占比。
// max_capacity 未设置（0）时不判满载——容量不限的库位仅有空/占用两态。
// requirements §2.6 的"冻结/异常"依赖库存域状态数量（M1 未交付），由 LOCKED 表达锁定，
// 其余待库存域扩展后补充，不造假状态。
func occupancyStatus(b *Bin, locked *float64) string {
	if locked != nil && *locked > 0 {
		return OccLocked
	}
	switch {
	case b.MaxCapacity > 0 && b.CurrentCapacity >= b.MaxCapacity:
		return OccFull
	case b.CurrentCapacity > 0:
		return OccPartial
	default:
		return OccIdle
	}
}
