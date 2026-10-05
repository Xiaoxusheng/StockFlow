package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// buildStockDistributionTree 纯函数单测（树组装与聚合不触库；SQL 侧行为见 integration_test.go）。
func TestBuildStockDistributionTree(t *testing.T) {
	t.Run("多仓多区多库位聚合与排序", func(t *testing.T) {
		rows := []stockDistRow{
			// 输入序 = SQL ORDER BY w.code,z.code,b.code 的产出序：仓库 A 在前，仓内 Z1 在 Z2 前
			{WarehouseID: 1, WarehouseCode: "WH-A", WarehouseName: "甲仓", ZoneID: 11, ZoneCode: "Z1", BinID: 101, BinCode: "B1", TotalQty: 7, AvailableQty: 7},
			{WarehouseID: 2, WarehouseCode: "WH-B", WarehouseName: "乙仓", ZoneID: 21, ZoneCode: "Z1", BinID: 211, BinCode: "B1", TotalQty: 5, AvailableQty: 0},
			// Z2 区：B1 一行 + B2 同库位两批行（→ 叶聚合，bin_id 决胜相邻）
			{WarehouseID: 2, WarehouseCode: "WH-B", WarehouseName: "乙仓", ZoneID: 22, ZoneCode: "Z2", BinID: 201, BinCode: "B1", TotalQty: 10, AvailableQty: 10},
			{WarehouseID: 2, WarehouseCode: "WH-B", WarehouseName: "乙仓", ZoneID: 22, ZoneCode: "Z2", BinID: 202, BinCode: "B2", TotalQty: 100, AvailableQty: 60},
			{WarehouseID: 2, WarehouseCode: "WH-B", WarehouseName: "乙仓", ZoneID: 22, ZoneCode: "Z2", BinID: 202, BinCode: "B2", TotalQty: 50, AvailableQty: 50},
		}

		tree := buildStockDistributionTree(rows)
		require.Len(t, tree, 2, "两个仓库根节点")

		// 仓库 A 在前（SQL ORDER BY w.code 的输入顺序即组装顺序）
		a := tree[0]
		require.Equal(t, "WH-A", a.WarehouseCode)
		require.Equal(t, "甲仓", a.WarehouseName)
		require.Empty(t, a.ZoneCode)
		require.Empty(t, a.BinCode)
		require.Equal(t, Qty(7), a.TotalQty)
		require.Equal(t, Qty(7), a.AvailableQty)
		require.Len(t, a.Children, 1)
		require.Equal(t, "Z1", a.Children[0].ZoneCode)
		require.Len(t, a.Children[0].Children, 1)
		require.Equal(t, "B1", a.Children[0].Children[0].BinCode)

		// 仓库 B：总量 = 同库位两批行聚合（150）+ 其余库位；库区按编码序 Z1 在前
		b := tree[1]
		require.Equal(t, Qty(165), b.TotalQty, "100+50+10+5")
		require.Equal(t, Qty(120), b.AvailableQty, "60+50+10+0")
		require.Len(t, b.Children, 2)
		require.Equal(t, "Z1", b.Children[0].ZoneCode, "区按编码排序")
		require.Equal(t, "Z2", b.Children[1].ZoneCode)

		z2 := b.Children[1]
		require.Equal(t, Qty(160), z2.TotalQty, "库区总量=子树求和（同库位两批聚合 150 + B1 10）")
		require.Len(t, z2.Children, 2, "同库位两批行聚合为单叶")
		require.Equal(t, "B1", z2.Children[0].BinCode)
		require.Equal(t, "B2", z2.Children[1].BinCode)
		require.Equal(t, Qty(150), z2.Children[1].TotalQty, "叶=同库位两批行之和")
		require.Equal(t, Qty(110), z2.Children[1].AvailableQty)

		// 判层契约：仓库层无 zone/bin code；区层无 bin code（前端 nodeLevel 依赖）
		require.Empty(t, z2.BinCode)
		require.NotEmpty(t, z2.ZoneCode)
		require.Empty(t, b.BinCode)
		require.NotEmpty(t, b.WarehouseCode)
	})

	t.Run("空输入返回空切片非nil", func(t *testing.T) {
		tree := buildStockDistributionTree([]stockDistRow{})
		require.NotNil(t, tree, "空分布必须序列化为 [] 而非 null（前端 roots.length 判空态）")
		require.Empty(t, tree)
	})
}
