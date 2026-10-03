package warehouse

// 库位地图与占用状态推导的单元测试（不依赖数据库）。

import (
	"context"
	"errors"
	"testing"

	"github.com/stockflow/server/internal/response"
)

func seedMapTree(t *testing.T, s *Service) (whID int64) {
	t.Helper()
	w := seedWarehouse(t, s, "WH01")
	zB := seedZone(t, s, w.ID.Int64(), "B")
	zA := seedZone(t, s, w.ID.Int64(), "A")
	shA := seedShelf(t, s, zA.ID.Int64(), "A-S1")
	seedBin(t, s, shA.ID.Int64(), "A-S1-0101") // 空闲
	b2, err := s.CreateBin(ctx, superActor(), BinCreateInput{
		ShelfID: shA.ID.Int64(), Code: "A-S1-0102", Layer: ptrI(1), ColumnNo: ptrI(2), MaxCapacity: ptrF(10),
	})
	mustOK(t, err)
	// b2 占用 6/10 → PARTIAL（容量由 fake 直改模拟上架维护结果）
	repo := s.repo.(*fakeRepo)
	stored, _ := repo.FindBinByID(ctx, b2.ID.Int64())
	stored.CurrentCapacity = 6
	repo.bins[b2.ID.Int64()] = stored

	shB := seedShelf(t, s, zB.ID.Int64(), "B-S1")
	b3 := seedBin(t, s, shB.ID.Int64(), "B-S1-0101")
	stored3, _ := repo.FindBinByID(ctx, b3.ID.Int64())
	stored3.CurrentCapacity = 5
	stored3.MaxCapacity = 5 // 5/5 → FULL
	repo.bins[b3.ID.Int64()] = stored3

	return w.ID.Int64()
}

func TestGetWarehouseMap_Tree(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)

	m, err := s.GetWarehouseMap(ctx, Scope{All: true}, whID)
	mustOK(t, err)
	if m.Warehouse.Code != "WH01" {
		t.Fatalf("仓库头不符: %+v", m.Warehouse)
	}
	if len(m.Zones) != 2 || m.Zones[0].Zone.Code != "A" || m.Zones[1].Zone.Code != "B" {
		t.Fatalf("库区排序/分组不符: %+v", m.Zones)
	}
	// A 区 1 架 2 位
	if len(m.Zones[0].Shelves) != 1 || len(m.Zones[0].Shelves[0].Bins) != 2 {
		t.Fatalf("A 区树不符: %+v", m.Zones[0])
	}
	cells := m.Zones[0].Shelves[0].Bins
	if cells[0].OccupancyStatus != OccIdle {
		t.Fatalf("空闲位状态不符: %+v", cells[0])
	}
	if cells[1].OccupancyStatus != OccPartial || cells[1].CurrentCapacity != 6 {
		t.Fatalf("部分占用位状态不符: %+v", cells[1])
	}
	// B 区满载
	if got := m.Zones[1].Shelves[0].Bins[0].OccupancyStatus; got != OccFull {
		t.Fatalf("满载位状态不符: %s", got)
	}
	// 未注入占用读取器：不携带库存数量字段（不造假数据）
	if cells[0].Quantity != nil || cells[0].LockedQuantity != nil {
		t.Fatalf("未注入 reader 不应出现数量字段: %+v", cells[0])
	}
}

func TestGetWarehouseMap_WithOccupancyReader(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)
	s.occ = &fakeOccupancy{ // 注入库存占用（inventory 提供方的等价替身）
		quantity: map[int64]float64{},
		locked:   map[int64]float64{},
	}
	// 找到 A-S1-0101 的 binID 并注入锁定数量 → LOCKED 优先
	m, err := s.GetWarehouseMap(ctx, Scope{All: true}, whID)
	mustOK(t, err)
	cells := m.Zones[0].Shelves[0].Bins
	if cells[0].OccupancyStatus != OccIdle {
		t.Fatalf("无占用信息应保持容量推导: %s", cells[0].OccupancyStatus)
	}
}

func TestGetWarehouseMap_LockedPriority(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)

	m, err := s.GetWarehouseMap(ctx, Scope{All: true}, whID)
	mustOK(t, err)
	cell := m.Zones[0].Shelves[0].Bins[0] // A-S1-0101（空闲）

	// 注入锁定数量 → LOCKED（优先于容量状态）
	s.occ = &fakeOccupancy{
		quantity: map[int64]float64{cell.ID.Int64(): 3},
		locked:   map[int64]float64{cell.ID.Int64(): 3},
	}
	m, err = s.GetWarehouseMap(ctx, Scope{All: true}, whID)
	mustOK(t, err)
	got := m.Zones[0].Shelves[0].Bins[0]
	if got.OccupancyStatus != OccLocked {
		t.Fatalf("锁定应优先: %s", got.OccupancyStatus)
	}
	if got.Quantity == nil || *got.Quantity != 3 || got.LockedQuantity == nil || *got.LockedQuantity != 3 {
		t.Fatalf("数量字段未透出: %+v", got)
	}
}

func TestGetWarehouseMap_ReaderFailureFailsWhole(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)
	s.occ = &fakeOccupancy{err: errors.New("inventory down")}
	_, err := s.GetWarehouseMap(ctx, Scope{All: true}, whID)
	expectCode(t, err, "COMMON_SERVICE_UNAVAILABLE") // 整图失败，不渲染假"空闲"图
}

func TestGetWarehouseMap_ScopeAndMissing(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)
	// 仓库不存在
	_, err := s.GetWarehouseMap(ctx, Scope{All: true}, 99999)
	expectCode(t, err, "WAREHOUSE_NOT_FOUND")
	// 范围外 → 不存在
	_, err = s.GetWarehouseMap(ctx, scopedActor(42).Scope, whID)
	expectCode(t, err, "COMMON_NOT_FOUND")
}

func TestOccupancyStatus(t *testing.T) {
	mk := func(max, cur float64) *Bin {
		return &Bin{MaxCapacity: max, CurrentCapacity: cur}
	}
	three := 3.0
	zero := 0.0
	cases := []struct {
		name string
		bin  *Bin
		lock *float64
		want string
	}{
		{"空闲", mk(10, 0), nil, OccIdle},
		{"部分占用", mk(10, 4), nil, OccPartial},
		{"满载", mk(10, 10), nil, OccFull},
		{"超满按满载", mk(10, 11), nil, OccFull},
		{"未设容量不判满", mk(0, 5), nil, OccPartial},
		{"锁定优先", mk(10, 0), &three, OccLocked},
		{"锁定为零忽略", mk(10, 0), &zero, OccIdle},
	}
	for _, tc := range cases {
		if got := occupancyStatus(tc.bin, tc.lock); got != tc.want {
			t.Fatalf("%s: got=%s want=%s", tc.name, got, tc.want)
		}
	}
}

// fakeOccupancy BinOccupancyReader 测试替身（纯内建类型签名，与 inventory 实现方
// 结构化满足同一接口）。
type fakeOccupancy struct {
	quantity map[int64]float64
	locked   map[int64]float64
	err      error
}

func (f *fakeOccupancy) OccupancyByWarehouse(_ context.Context, _ int64) (map[int64]float64, map[int64]float64, error) {
	return f.quantity, f.locked, f.err
}

// 编译期确认 *BinCheckService 满足消费者所需形态的锚点（inventory.BinChecker 同型）。
var _ interface {
	ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error)
} = &BinCheckService{}

// 确认业务错误码可被 response 信封按注册码写出（smoke：注册表完整性）。
func TestErrorCodesRegistered(t *testing.T) {
	for _, c := range []string{
		"WAREHOUSE_NOT_FOUND", "WAREHOUSE_CODE_EXISTS", "WAREHOUSE_HAS_ZONES",
		"WAREHOUSE_HAS_ENABLED_ZONES", "WAREHOUSE_ZONE_NOT_FOUND", "WAREHOUSE_ZONE_CODE_EXISTS",
		"WAREHOUSE_ZONE_HAS_ENABLED_SHELVES", "WAREHOUSE_SHELF_NOT_FOUND", "WAREHOUSE_SHELF_CODE_EXISTS",
		"WAREHOUSE_SHELF_HAS_ENABLED_BINS", "WAREHOUSE_BIN_NOT_FOUND", "WAREHOUSE_BIN_CODE_EXISTS",
		"WAREHOUSE_BIN_OCCUPIED", "WAREHOUSE_HIERARCHY_MISMATCH", "WAREHOUSE_PARENT_DISABLED",
	} {
		if _, ok := response.Lookup(c); !ok {
			t.Fatalf("错误码未注册: %s", c)
		}
	}
}
