package stock

// Actor / Source / RowKey——库存原语的归因、来源与五维定位值类型
// （自 internal/inventory/service.go:54-99 原样搬迁，backend-m2-plan §3 冻结形状；
// 仅搬结构体定义与注释，未搬 service.go 内使用它们的未导出错误拼装管道）。

// Actor 操作者归因（操作日志与流水 operator 冗余字段；调用方经 auth.CurrentUser 带出，
// 系统级调用 OperatorID=0，plan §4.4）。
type Actor struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	RequestID string `json:"request_id"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Method    string `json:"method"`
	Path      string `json:"path"`
}

// RowKey 五维定位键（inventory-rules §3）。定位 = warehouse + bin + sku + batch
// （zone/shelf 随 bin 冗余，迁移 uk_inventory_location 注释）；BatchID=0 表示非批次 SKU。
// 行创建类变更（Putaway/盘盈/移库目标行）必须携带 ZoneID/ShelfID。
type RowKey struct {
	WarehouseID int64 `json:"warehouse_id"`
	ZoneID      int64 `json:"zone_id"`
	ShelfID     int64 `json:"shelf_id"`
	BinID       int64 `json:"bin_id"`
	SKUID       int64 `json:"sku_id"`
	BatchID     int64 `json:"batch_id"`
}

// Source 来源单据（inventory-rules §5：任何库存变化必须可追溯来源单据）。
type Source struct {
	Type string `json:"type"` // 业务单据类型（如 inbound_order/sales_order/count_order）
	No   string `json:"no"`   // 业务单号
}
