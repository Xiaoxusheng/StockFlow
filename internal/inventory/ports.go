package inventory

import (
	"context"

	"gorm.io/gorm"
)

// 跨域消费接口与装配端口（backend-m1-plan §4.3 唯一跨域机制：消费方定义最小接口，
// 实现由被消费域提供、router 装配经 Option 注入；域包之间禁止 import）。

// SKUChecker 跨域消费接口：SKU 存在且启用（api.md §4 业务关系校验）。
// 实现由 masterdata 域提供（masterdata.NewSKUChecker(db)），router 注入。
type SKUChecker interface {
	ExistsActive(ctx context.Context, skuID int64) (bool, error)
}

// BinChecker 跨域消费接口：库位存在且启用（实现方须校验库位归属指定仓库）。
// 实现由 warehouse 域提供（warehouse.NewBinChecker(db)），router 注入。
type BinChecker interface {
	ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error)
}

// WithSKUChecker 注入 SKU 存在性校验（router 装配：masterdata.NewSKUChecker(db)）。
func WithSKUChecker(c SKUChecker) Option { return func(o *options) { o.skuChecker = c } }

// WithBinChecker 注入库位存在性校验（router 装配：warehouse.NewBinChecker(db)）。
func WithBinChecker(c BinChecker) Option { return func(o *options) { o.binChecker = c } }

// BinOccupancy 库位占用读取（plan §4.3 ②：warehouse 库位地图经消费接口
// BinOccupancyReader 由 inventory 聚合提供——inventory 是占用数据的属主域）。
type BinOccupancy struct {
	db *gorm.DB
}

// NewBinOccupancy 构造库位占用读取器（router 装配注入 warehouse 域）。
func NewBinOccupancy(db *gorm.DB) *BinOccupancy { return &BinOccupancy{db: db} }

// BinOccupancySummary 单库位占用汇总（零占用的库位不出现在结果中）。
type BinOccupancySummary struct {
	BinID    int64 `json:"bin_id"`
	SKUKinds int64 `json:"sku_kinds"` // 占用 SKU 种类数
	TotalQty Qty   `json:"total_qty"` // 库位库存合计（六状态总和）
}

// SumByBin 按仓库汇总每个库位的占用（warehouse 库位地图数据源）。
func (o *BinOccupancy) SumByBin(ctx context.Context, warehouseID int64) ([]BinOccupancySummary, error) {
	if o.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var rows []BinOccupancySummary
	err := o.db.WithContext(ctx).Raw(`
		SELECT bin_id,
		       COUNT(DISTINCT sku_id) AS sku_kinds,
		       COALESCE(SUM(total_qty), 0) AS total_qty
		FROM inventory
		WHERE warehouse_id = ?
		GROUP BY bin_id
		ORDER BY bin_id`, warehouseID).Scan(&rows).Error
	return rows, err
}
