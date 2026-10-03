package warehouse

import (
	"context"
	"errors"
	"gorm.io/gorm"
)

// 跨域契约（backend-m1-plan §4.3：消费方窄接口 + router 装配注入；域包之间禁止 import，
// 因此接口方法签名只用内建类型，实现方无需 import 本包即可结构化满足）。

// BinOccupancyReader 库位占用读取接口（本域为消费方，实现由 inventory 域提供并在
// router 装配注入：warehouse.RegisterRoutes(..., warehouse.WithBinOccupancy(...))，
// plan §4.3 ②）。
//
// 返回值仅用内建类型（域间契约不携带域类型，实现方无需 import 本包即可结构化满足）：
//   - quantity：binID → 在库数量合计（含锁定部分）；
//   - locked：binID → 其中锁定/冻结数量合计；缺省键/nil map = 无占用信息。
//
// 按仓库聚合而非按 binID 集合：地图以仓库为单位取数，一次批量查询（architecture.md §7）。
// 已知实现源：inventory.BinOccupancy.SumByBin(ctx, warehouseID)（返回域具名类型切片），
// 因其返回类型本包不可命名，router 装配（scope A，plan §5.3 TODO T6）以闭包适配器桥接
// 到本接口；未注入时库位地图不带库存占用字段（不造假数据，requirements.md §10）。
type BinOccupancyReader interface {
	OccupancyByWarehouse(ctx context.Context, warehouseID int64) (quantity, locked map[int64]float64, err error)
}

// Option RegisterRoutes 的可选注入项：仅用于跨域消费接口注入（plan §5.2），
// 不得携带业务配置。
type Option func(*options)

type options struct {
	occupancy BinOccupancyReader
}

// WithBinOccupancy 注入库位占用读取实现（inventory 域提供）。
func WithBinOccupancy(r BinOccupancyReader) Option {
	return func(o *options) { o.occupancy = r }
}

// —— 被消费方导出的实现（读自己的表，plan §4.3：实现方对消费方接口无感知）——

// WarehouseCheckService 仓库存在性校验实现。供 auth 域的 WarehouseChecker 消费接口
// 结构化满足（auth.WithWarehouseChecker(warehouse.NewChecker(db))，plan §4.3/§5.3）：
// 用户绑定仓库的存在性与可用性校验（api.md §4 业务关系）。
type WarehouseCheckService struct{ db *gorm.DB }

// NewChecker 构造仓库存在性校验实现（router 装配入口）。
func NewChecker(db *gorm.DB) *WarehouseCheckService { return &WarehouseCheckService{db: db} }

// ExistsActive 仓库是否存在且启用（软删除行自动排除；未命中返回 false, nil）。
func (s *WarehouseCheckService) ExistsActive(ctx context.Context, warehouseID int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("warehouse.NewChecker: db 未装配")
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&Warehouse{}).
		Where("id = ? AND status = ?", warehouseID, StatusEnabled).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// BinCheckService 库位存在性校验实现。供 inventory 域的 BinChecker 消费接口结构化满足
// （inventory.WithBinChecker(warehouse.NewBinChecker(db))，plan §4.3/§5.3）：
// 上架/分配/移库前校验目标库位归属于指定仓库且启用。
type BinCheckService struct{ db *gorm.DB }

// NewBinChecker 构造库位存在性校验实现（router 装配入口）。
func NewBinChecker(db *gorm.DB) *BinCheckService { return &BinCheckService{db: db} }

// ExistsActive 库位是否存在、归属指定仓库且启用（未命中返回 false, nil）。
func (s *BinCheckService) ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("warehouse.NewBinChecker: db 未装配")
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&Bin{}).
		Where("id = ? AND warehouse_id = ? AND status = ?", binID, warehouseID, StatusEnabled).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}
