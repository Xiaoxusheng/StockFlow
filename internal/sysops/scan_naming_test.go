package sysops

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// TestScanRowColumnMapping 守住定时扫描只读聚合行的列名映射（回归防线）。
//
// 背景（2026-10-07 全流程实测）：GORM 命名策略把 SKUID 推导成 sk_uid（≠ 表列
// sku_id），Raw(...).Scan 对不上即静默落零值——低库存/效期/积压三类扫描的
// 通知内容会带 sku_id=0 与空 SKU 编码。
func TestScanRowColumnMapping(t *testing.T) {
	cases := []struct {
		name    string
		dest    any
		columns []string
	}{
		{"lowStockRow", &lowStockRow{}, []string{"warehouse_id", "sku_id", "sku_code", "available", "safety_stock"}},
		{"expiryBatchRow", &expiryBatchRow{}, []string{"sku_id", "sku_code", "batch_no", "expiry_date", "total_qty", "warehouses"}},
		{"stagnantScanRow", &stagnantScanRow{}, []string{"warehouse_id", "sku_id", "sku_code", "total_qty", "last_moved_at"}},
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
