package sales

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// TestScanRowColumnMapping 守住自带扫描结构体的列名映射（回归防线）。
//
// 背景（2026-10-07 全流程实测）：GORM 命名策略把 SKUID 推导成 sk_uid（≠ 表列
// sku_id），Raw(...).Scan 对不上即静默落零值——打印内容、批次分配候选、序列号
// 校验数据源都会拿到错误的 sku_id。
func TestScanRowColumnMapping(t *testing.T) {
	cases := []struct {
		name    string
		dest    any
		columns []string
	}{
		{"BinStock", &BinStock{}, []string{"warehouse_id", "zone_id", "shelf_id", "bin_id", "sku_id", "batch_id", "available_qty"}},
		{"SerialState", &SerialState{}, []string{"serial_no", "sku_id", "batch_id", "warehouse_id", "bin_id", "status"}},
		{"outboundLineRow", &outboundLineRow{}, []string{"outbound_id", "line_no", "sku_id", "qty", "qty_picked"}},
		{"pickRow", &pickRow{}, []string{"pick_id", "pick_no", "sku_id", "source_bin_id", "qty", "qty_picked"}},
		{"shippedLineRow", &shippedLineRow{}, []string{"outbound_id", "line_no", "sku_id", "qty", "qty_shipped"}},
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
