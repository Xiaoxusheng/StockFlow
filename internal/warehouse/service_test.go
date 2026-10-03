package warehouse

// Service 层单元测试（ask 约束：不依赖 PostgreSQL/Redis/网络）。
// 事务经假 gorm 方言器可运行（fakedb_test.go），数据由内存 fakeRepo 承载
// （fakerepo_test.go，唯一索引/软删除语义对齐 000004 迁移）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/response"
)

// ---- 测试工具 ----

var ctx = context.Background()

func newTestService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo), repo
}

// superActor 全量数据权限（超管经 auth.WarehouseScope 归一为 All 后的等价形态）。
func superActor() Actor {
	return Actor{UserID: 1, Username: "root", IsSuper: true, Scope: Scope{All: true}}
}

// scopedActor 指定仓库范围。
func scopedActor(ids ...int64) Actor {
	return Actor{UserID: 2, Username: "op", Scope: Scope{WarehouseIDs: ids}}
}

// codeOf 取业务错误码字符串（*response.Error 的 Error() 形如 "CODE: message"，取前缀；
// 形态对齐 internal/auth/support_test.go）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *response.Error
	if !errors.As(err, &e) {
		t.Fatalf("期望业务错误，得到 %T: %v", err, err)
	}
	full := e.Error()
	idx := strings.Index(full, ": ")
	require.Positive(t, idx, "错误码格式异常: %q", full)
	return full[:idx]
}

func expectCode(t *testing.T, err error, want string) {
	t.Helper()
	if got := codeOf(t, err); got != want {
		t.Fatalf("错误码不符：got=%s want=%s（err=%v）", got, want, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际出错: %v", err)
	}
}

// seedWarehouse 造一个启用仓库。
func seedWarehouse(t *testing.T, s *Service, code string) *WarehouseView {
	t.Helper()
	wh, err := s.CreateWarehouse(ctx, superActor(), WarehouseCreateInput{Code: code, Name: "仓库-" + code})
	mustOK(t, err)
	return wh
}

// seedZone 造一个启用库区。
func seedZone(t *testing.T, s *Service, whID int64, code string) *ZoneView {
	t.Helper()
	z, err := s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: whID, Code: code, Name: "库区-" + code})
	mustOK(t, err)
	return z
}

func seedShelf(t *testing.T, s *Service, zoneID int64, code string) *ShelfView {
	t.Helper()
	sh, err := s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: zoneID, Code: code})
	mustOK(t, err)
	return sh
}

func seedBin(t *testing.T, s *Service, shelfID int64, code string) *BinView {
	t.Helper()
	b, err := s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: shelfID, Code: code})
	mustOK(t, err)
	return b
}

// ---- 仓库 ----

func TestCreateWarehouse_Success(t *testing.T) {
	s, _ := newTestService()
	w, err := s.CreateWarehouse(ctx, superActor(), WarehouseCreateInput{
		Code: "  WH01  ", Name: " 一号仓 ", Type: "", Area: ptrF(100.5),
	})
	mustOK(t, err)
	if w.Code != "WH01" || w.Name != "一号仓" {
		t.Fatalf("输入未规范化: %+v", w)
	}
	if w.Type != WarehouseTypeNormal || w.Status != StatusEnabled {
		t.Fatalf("默认值不符: type=%s status=%s", w.Type, w.Status)
	}
	if w.Area != 100.5 {
		t.Fatalf("面积不符: %v", w.Area)
	}
	if w.ID == 0 {
		t.Fatal("ID 未回填")
	}
}

func TestCreateWarehouse_Validation(t *testing.T) {
	s, _ := newTestService()
	cases := []struct {
		name  string
		input WarehouseCreateInput
		field string
	}{
		{"非法编码", WarehouseCreateInput{Code: "仓库1", Name: "n"}, "code"},
		{"空名称", WarehouseCreateInput{Code: "WH1", Name: "  "}, "name"},
		{"负面积", WarehouseCreateInput{Code: "WH1", Name: "n", Area: ptrF(-1)}, "area"},
		{"负容量", WarehouseCreateInput{Code: "WH1", Name: "n", Capacity: ptrF(-0.1)}, "capacity"},
		{"非法类型", WarehouseCreateInput{Code: "WH1", Name: "n", Type: "normal"}, "type"},
		{"非法电话", WarehouseCreateInput{Code: "WH1", Name: "n", Phone: "abc#"}, "phone"},
		{"负负责人", WarehouseCreateInput{Code: "WH1", Name: "n", ManagerUserID: -2}, "manager_user_id"},
	}
	for _, tc := range cases {
		_, err := s.CreateWarehouse(ctx, superActor(), tc.input)
		if err == nil {
			t.Fatalf("%s: 期望出错", tc.name)
		}
		if codeOf(t, err) != "COMMON_INVALID_PARAM" {
			t.Fatalf("%s: 期望 COMMON_INVALID_PARAM, got %v", tc.name, err)
		}
		var e *response.Error
		if !errors.As(err, &e) {
			t.Fatalf("%s: 非业务错误 %v", tc.name, err)
		}
		if d, ok := e.Details.(map[string]any); !ok || d["field"] != tc.field {
			t.Fatalf("%s: details 未定位字段 %s: %v", tc.name, tc.field, e.Details)
		}
	}
}

func TestCreateWarehouse_DuplicateCode(t *testing.T) {
	s, _ := newTestService()
	seedWarehouse(t, s, "WH01")
	_, err := s.CreateWarehouse(ctx, superActor(), WarehouseCreateInput{Code: "WH01", Name: "重复"})
	expectCode(t, err, "WAREHOUSE_CODE_EXISTS")
}

func TestWarehouse_CodeReusableAfterSoftDelete(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	mustOK(t, s.DeleteWarehouse(ctx, superActor(), w.ID.Int64())) // 软删
	// 软删行不占用编码（uk_warehouses_code 部分索引）
	w2, err := s.CreateWarehouse(ctx, superActor(), WarehouseCreateInput{Code: "WH01", Name: "复用"})
	mustOK(t, err)
	if w2.ID == w.ID {
		t.Fatal("新建仓库不应复用软删行 ID")
	}
}

func TestUpdateWarehouse_Partial(t *testing.T) {
	s, repo := newTestService()
	w := seedWarehouse(t, s, "WH01")
	got, err := s.UpdateWarehouse(ctx, superActor(), w.ID.Int64(), WarehouseUpdateInput{
		Name: ptrS("新名称"), Area: ptrF(9.9),
	})
	mustOK(t, err)
	if got.Name != "新名称" || got.Area != 9.9 {
		t.Fatalf("部分更新未生效: %+v", got)
	}
	if got.Code != "WH01" {
		t.Fatalf("未提供字段不应变更: code=%s", got.Code)
	}
	if stored, _ := repo.FindWarehouseByID(ctx, w.ID.Int64()); stored.Status != StatusEnabled {
		t.Fatalf("status 不在更新面，不应被改: %s", stored.Status)
	}
}

func TestUpdateWarehouse_CodeConflict(t *testing.T) {
	s, _ := newTestService()
	seedWarehouse(t, s, "WH01")
	w2 := seedWarehouse(t, s, "WH02")
	_, err := s.UpdateWarehouse(ctx, superActor(), w2.ID.Int64(), WarehouseUpdateInput{Code: ptrS("WH01")})
	expectCode(t, err, "WAREHOUSE_CODE_EXISTS")
	// 改回自身编码（不变更归属）应放行
	_, err = s.UpdateWarehouse(ctx, superActor(), w2.ID.Int64(), WarehouseUpdateInput{Code: ptrS("WH02")})
	mustOK(t, err)
}

func TestUpdateWarehouse_EmptyBody(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	_, err := s.UpdateWarehouse(ctx, superActor(), w.ID.Int64(), WarehouseUpdateInput{})
	if err == nil {
		t.Fatal("空更新体应拒绝")
	}
}

func TestUpdateWarehouseStatus_DisableBlockedByEnabledZone(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	err := s.UpdateWarehouseStatus(ctx, superActor(), w.ID.Int64(), StatusDisabled)
	expectCode(t, err, "WAREHOUSE_HAS_ENABLED_ZONES")
	// 停用库区后可停用仓库
	mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z.ID.Int64(), StatusDisabled))
	mustOK(t, s.UpdateWarehouseStatus(ctx, superActor(), w.ID.Int64(), StatusDisabled))
}

func TestUpdateWarehouseStatus_InvalidStatus(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	err := s.UpdateWarehouseStatus(ctx, superActor(), w.ID.Int64(), "OFF")
	expectCode(t, err, "COMMON_INVALID_PARAM")
}

func TestDeleteWarehouse_BlockedByAnyZone(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	// 库区已停用也不允许删（zones 无删除通路，存在即阻塞）
	mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z.ID.Int64(), StatusDisabled))
	err := s.DeleteWarehouse(ctx, superActor(), w.ID.Int64())
	expectCode(t, err, "WAREHOUSE_HAS_ZONES")

	s2, _ := newTestService()
	empty := seedWarehouse(t, s2, "WHE")
	mustOK(t, s2.DeleteWarehouse(ctx, superActor(), empty.ID.Int64()))
	_, err = s2.GetWarehouse(ctx, Scope{All: true}, empty.ID.Int64())
	expectCode(t, err, "WAREHOUSE_NOT_FOUND")
}

// ---- 数据权限（permission.md §4：Service 层过滤）----

func TestScope_WarehouseVisibility(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	seedWarehouse(t, s, "WHB")

	// 指定仓库范围只见 WHA
	actor := scopedActor(w1.ID.Int64())
	items, total, err := s.ListWarehouses(ctx, WarehouseListFilter{Scope: actor.Scope, Page: 1, PageSize: 20})
	mustOK(t, err)
	if total != 1 || items[0].Code != "WHA" {
		t.Fatalf("范围过滤失效: total=%d", total)
	}
	// 范围外详情 → 不存在（fail-closed，不泄漏存在性）
	_, err = s.GetWarehouse(ctx, actor.Scope, w1.ID.Int64()+1)
	expectCode(t, err, "COMMON_NOT_FOUND")

	// 空范围（SPECIFIED 但未绑定仓库）不可见任何行
	empty := scopedActor()
	items, total, err = s.ListWarehouses(ctx, WarehouseListFilter{Scope: empty.Scope, Page: 1, PageSize: 20})
	mustOK(t, err)
	if total != 0 || len(items) != 0 {
		t.Fatalf("空范围应不可见任何仓库: total=%d", total)
	}
}

func TestScope_WriteBlockedOutsideScope(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	actor := scopedActor(999) // 不包含 WH01
	_, err := s.CreateZone(ctx, actor, ZoneCreateInput{WarehouseID: w.ID.Int64(), Code: "A", Name: "区"})
	expectCode(t, err, "COMMON_NOT_FOUND")
	_, err = s.CreateWarehouse(ctx, actor, WarehouseCreateInput{Code: "WH02", Name: "n"})
	mustOK(t, err) // 创建仓库为 HQ 级操作，仅 RBAC 权限点约束（见交付说明）
}

// ---- 库区 ----

func TestCreateZone_HierarchyAndUniqueness(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	w2 := seedWarehouse(t, s, "WH02")

	z, err := s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: w.ID.Int64(), Code: "A", Name: "存储区"})
	mustOK(t, err)
	if z.ZoneType != ZoneTypeStorage || z.Status != StatusEnabled {
		t.Fatalf("默认值不符: %+v", z)
	}
	// 仓库内编码唯一
	_, err = s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: w.ID.Int64(), Code: "A", Name: "重复"})
	expectCode(t, err, "WAREHOUSE_ZONE_CODE_EXISTS")
	// 不同仓库同编码合法
	_, err = s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: w2.ID.Int64(), Code: "A", Name: "另一仓同码"})
	mustOK(t, err)
	// 仓库不存在
	_, err = s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: 99999, Code: "B", Name: "n"})
	expectCode(t, err, "WAREHOUSE_NOT_FOUND")
}

func TestCreateZone_ParentDisabled(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	mustOK(t, s.UpdateWarehouseStatus(ctx, superActor(), w.ID.Int64(), StatusDisabled))
	_, err := s.CreateZone(ctx, superActor(), ZoneCreateInput{WarehouseID: w.ID.Int64(), Code: "A", Name: "n"})
	expectCode(t, err, "WAREHOUSE_PARENT_DISABLED")
}

func TestUpdateZoneStatus_DisableBlockedByEnabledShelf(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")
	err := s.UpdateZoneStatus(ctx, superActor(), z.ID.Int64(), StatusDisabled)
	expectCode(t, err, "WAREHOUSE_ZONE_HAS_ENABLED_SHELVES")
	mustOK(t, s.UpdateShelfStatus(ctx, superActor(), sh.ID.Int64(), StatusDisabled))
	mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z.ID.Int64(), StatusDisabled))
}

// ---- 货架 ----

func TestCreateShelf_DerivedAnchors(t *testing.T) {
	s, repo := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")

	sh, err := s.CreateShelf(ctx, superActor(), ShelfCreateInput{
		ZoneID: z.ID.Int64(), WarehouseID: w.ID.Int64(), Code: "S01", Layers: ptrI(4), Columns: ptrI(6),
	})
	mustOK(t, err)
	if sh.WarehouseID.Int64() != w.ID.Int64() || sh.ZoneID.Int64() != z.ID.Int64() {
		t.Fatalf("锚点应以 zone 归属为准: %+v", sh)
	}
	if sh.Layers != 4 || sh.Columns != 6 {
		t.Fatalf("层列不符: %+v", sh)
	}
	stored, _ := repo.FindShelfByID(ctx, sh.ID.Int64())
	if stored.Status != StatusEnabled || stored.Capacity != 0 {
		t.Fatalf("默认值不符: %+v", stored)
	}
}

func TestCreateShelf_MismatchAndDup(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WH01")
	w2 := seedWarehouse(t, s, "WH02")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	z2 := seedZone(t, s, w2.ID.Int64(), "B")
	seedShelf(t, s, z1.ID.Int64(), "S01")

	// warehouse_id 与 zone 归属不一致
	_, err := s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: z1.ID.Int64(), WarehouseID: w2.ID.Int64(), Code: "S09"})
	expectCode(t, err, "WAREHOUSE_HIERARCHY_MISMATCH")
	// 库区内唯一
	_, err = s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: z1.ID.Int64(), Code: "S01"})
	expectCode(t, err, "WAREHOUSE_SHELF_CODE_EXISTS")
	// 不同库区同码合法
	_, err = s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: z2.ID.Int64(), Code: "S01"})
	mustOK(t, err)
	// zone 不存在
	_, err = s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: 99999, Code: "S02"})
	expectCode(t, err, "WAREHOUSE_ZONE_NOT_FOUND")
	// 层列越界
	_, err = s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: z1.ID.Int64(), Code: "S10", Layers: ptrI(0)})
	expectCode(t, err, "COMMON_INVALID_PARAM")
}

func TestCreateShelf_ParentDisabled(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z.ID.Int64(), StatusDisabled))
	_, err := s.CreateShelf(ctx, superActor(), ShelfCreateInput{ZoneID: z.ID.Int64(), Code: "S01"})
	expectCode(t, err, "WAREHOUSE_PARENT_DISABLED")
}

func TestUpdateShelfStatus_DisableBlockedByEnabledBin(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")
	b := seedBin(t, s, sh.ID.Int64(), "B01")
	err := s.UpdateShelfStatus(ctx, superActor(), sh.ID.Int64(), StatusDisabled)
	expectCode(t, err, "WAREHOUSE_SHELF_HAS_ENABLED_BINS")
	mustOK(t, s.UpdateBinStatus(ctx, superActor(), b.ID.Int64(), StatusDisabled))
	mustOK(t, s.UpdateShelfStatus(ctx, superActor(), sh.ID.Int64(), StatusDisabled))
}

// ---- 库位 ----

func TestCreateBin_DerivedAnchorsAndAttrs(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")

	b, err := s.CreateBin(ctx, superActor(), BinCreateInput{
		ShelfID: sh.ID.Int64(), ZoneID: z.ID.Int64(), WarehouseID: w.ID.Int64(),
		Code: "A-S01-0101", BinType: "STORAGE", Layer: ptrI(2), ColumnNo: ptrI(3), MaxCapacity: ptrF(12.5),
	})
	mustOK(t, err)
	if b.WarehouseID.Int64() != w.ID.Int64() || b.ZoneID.Int64() != z.ID.Int64() || b.ShelfID.Int64() != sh.ID.Int64() {
		t.Fatalf("锚点应以 shelf 归属为准: %+v", b)
	}
	if b.Layer != 2 || b.ColumnNo != 3 || b.BinType != "STORAGE" || b.MaxCapacity != 12.5 {
		t.Fatalf("库位属性不符: %+v", b)
	}
	if b.CurrentCapacity != 0 || b.Status != StatusEnabled {
		t.Fatalf("当前容量应恒为 0、状态启用: %+v", b)
	}
	// 缺省属性：PICK / 层 1 / 列 1
	d, err := s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh.ID.Int64(), Code: "A-S01-0102"})
	mustOK(t, err)
	if d.BinType != BinTypePick || d.Layer != 1 || d.ColumnNo != 1 {
		t.Fatalf("缺省属性不符: %+v", d)
	}
}

func TestCreateBin_ValidationAndMismatch(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WH01")
	w2 := seedWarehouse(t, s, "WH02")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	z2 := seedZone(t, s, w2.ID.Int64(), "B")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S01")

	_, err := s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), ZoneID: z2.ID.Int64(), Code: "X"})
	expectCode(t, err, "WAREHOUSE_HIERARCHY_MISMATCH")
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), WarehouseID: w2.ID.Int64(), Code: "X"})
	expectCode(t, err, "WAREHOUSE_HIERARCHY_MISMATCH")
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: 99999, Code: "X"})
	expectCode(t, err, "WAREHOUSE_SHELF_NOT_FOUND")
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), Code: "X", MaxCapacity: ptrF(-3)})
	expectCode(t, err, "COMMON_INVALID_PARAM")
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), Code: "X", BinType: "pick"})
	expectCode(t, err, "COMMON_INVALID_PARAM")
}

func TestBinCodeUniqueWithinWarehouse_IncludingSoftDeleted(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z.ID.Int64(), "S01")
	sh2 := seedShelf(t, s, z.ID.Int64(), "S02")

	// 同仓库不同货架，编码仍唯一（uk_bins_warehouse_code：仓库内唯一）
	seedBin(t, s, sh1.ID.Int64(), "B-01")
	_, err := s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh2.ID.Int64(), Code: "B-01"})
	expectCode(t, err, "WAREHOUSE_BIN_CODE_EXISTS")

	// 软删后编码仍被占用（普通唯一索引，编码永久保留避免历史追溯混淆）
	b2 := seedBin(t, s, sh2.ID.Int64(), "B-02")
	mustOK(t, s.DeleteBin(ctx, superActor(), b2.ID.Int64()))
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), Code: "B-02"})
	expectCode(t, err, "WAREHOUSE_BIN_CODE_EXISTS")

	// 其他仓库同码合法
	wB := seedWarehouse(t, s, "WH02")
	zB := seedZone(t, s, wB.ID.Int64(), "ZA")
	shB := seedShelf(t, s, zB.ID.Int64(), "SB")
	_, err = s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: shB.ID.Int64(), Code: "B-01"})
	mustOK(t, err)
}

func TestDeleteBin_OccupiedBlocked(t *testing.T) {
	s, repo := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")
	b := seedBin(t, s, sh.ID.Int64(), "B01")

	// 占用容量 > 0（由上架业务维护）→ 拒绝删除
	stored, _ := repo.FindBinByID(ctx, b.ID.Int64())
	stored.CurrentCapacity = 5
	repo.bins[b.ID.Int64()] = stored
	err := s.DeleteBin(ctx, superActor(), b.ID.Int64())
	expectCode(t, err, "WAREHOUSE_BIN_OCCUPIED")

	stored.CurrentCapacity = 0
	repo.bins[b.ID.Int64()] = stored
	mustOK(t, s.DeleteBin(ctx, superActor(), b.ID.Int64()))
	_, err = s.GetBin(ctx, Scope{All: true}, b.ID.Int64())
	expectCode(t, err, "WAREHOUSE_BIN_NOT_FOUND")
}

func TestUpdateBin_CurrentCapacityImmutable(t *testing.T) {
	s, repo := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")
	b := seedBin(t, s, sh.ID.Int64(), "B01")

	got, err := s.UpdateBin(ctx, superActor(), b.ID.Int64(), BinUpdateInput{MaxCapacity: ptrF(20), Code: ptrS("B01X")})
	mustOK(t, err)
	if got.MaxCapacity != 20 || got.Code != "B01X" {
		t.Fatalf("更新未生效: %+v", got)
	}
	stored, _ := repo.FindBinByID(ctx, b.ID.Int64())
	if stored.CurrentCapacity != 0 {
		t.Fatalf("current_capacity 禁止直改（000004 列注释），被改为 %v", stored.CurrentCapacity)
	}
	// 更新入参不存在 current_capacity 字段（编译期保证），此处验证行为面
}

// ---- 列表分页 ----

func TestListBins_PagedAndFiltered(t *testing.T) {
	s, _ := newTestService()
	w := seedWarehouse(t, s, "WH01")
	z := seedZone(t, s, w.ID.Int64(), "A")
	sh := seedShelf(t, s, z.ID.Int64(), "S01")
	for i := 1; i <= 25; i++ {
		seedBin(t, s, sh.ID.Int64(), fmt.Sprintf("B-%03d", i))
	}
	items, total, err := s.ListBins(ctx, BinListFilter{Scope: Scope{All: true}, Page: 2, PageSize: 20})
	mustOK(t, err)
	if total != 25 || len(items) != 5 {
		t.Fatalf("分页不符: total=%d len=%d", total, len(items))
	}
	items, total, err = s.ListBins(ctx, BinListFilter{Scope: Scope{All: true}, Keyword: "B-01", Page: 1, PageSize: 20})
	mustOK(t, err)
	if total != 10 { // B-001..B-010
		t.Fatalf("关键字过滤不符: total=%d", total)
	}
}

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }
func ptrS(v string) *string   { return &v }
