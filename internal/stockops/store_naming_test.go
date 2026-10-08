package stockops

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// TestScanRefColumnMapping 守住只读投影结构体的列名映射（回归防线）。
//
// 背景（2026-10-07 全流程实测）：本包 InventoryRowRef/ActiveLockRef/SerialRef 的
// SKUID、Total、Available 等字段未打 gorm column tag，GORM 命名策略把它们推导成
// sk_uid / total / available（≠ 表列 sku_id / total_qty / available_qty），
// Raw(...).Scan 对不上即**静默落零值**（无报错）。后果：调拨出库 409
// STOCKOPS_LOCK_MISSING、盘点冻结空转/快照零值/差异方向扭反/complete 400。
//
// 本测试对每个字段断言落库名与表列一致——再犯即红。
func TestScanRefColumnMapping(t *testing.T) {
	cases := []struct {
		name    string
		dest    any
		columns []string
	}{
		{
			name: "InventoryRowRef→inventory",
			dest: &InventoryRowRef{},
			columns: []string{
				"id", "warehouse_id", "zone_id", "shelf_id", "bin_id", "sku_id", "batch_id",
				"total_qty", "available_qty", "locked_qty", "frozen_qty",
				"pending_inspect_qty", "defective_qty",
			},
		},
		{
			name: "ActiveLockRef→inventory_locks",
			dest: &ActiveLockRef{},
			columns: []string{
				"id", "source_no", "source_type", "qty", "warehouse_id", "bin_id", "sku_id", "batch_id",
			},
		},
		{
			name: "SerialRef→serial_numbers",
			dest: &SerialRef{},
			columns: []string{"id", "serial_no", "sku_id", "batch_id"},
		},
		{
			name: "InTransitRow→在途聚合投影",
			dest: &InTransitRow{},
			columns: []string{"sku_id", "batch_id", "out_transit", "in_transit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := schema.Parse(tc.dest, &sync.Map{}, schema.NamingStrategy{})
			if err != nil {
				t.Fatalf("schema.Parse: %v", err)
			}
			got := make(map[string]bool, len(s.Fields))
			for _, f := range s.Fields {
				got[f.DBName] = true
			}
			for _, want := range tc.columns {
				if !got[want] {
					t.Errorf("落库名 %q 无对应字段（Raw Scan 会静默落零值）——检查结构体 gorm column tag", want)
				}
			}
		})
	}
}
