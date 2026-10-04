package warehouse

// 推荐库位（BinRecommender 实现侧导出；backend-m2-plan §5.3 基础规则、business-flow
// §5.3——2026-10-04 补齐装配，此前 purchase.BinRecommender 未注入，GET /api/putaway/
// recommend 运行期 fail-closed 返回 ErrBinRequired）。
//
// 架构落位（plan §4.3 唯一跨域机制）：本域实现 purchase.BinRecommender 的数据面——
//   - 库位静态属性（容量/启用态/层级归属）= 本域 bins 表，直读；
//   - 库存动态占用（同 SKU 分布 / 库位在库合计）= inventory 域数据，经消费侧窄接口
//     BinOccupancyReader / SKUBinReader 注入（实现 inventory.BinOccupancy，router
//     闭包桥接），本域无跨域 SELECT 旁路（backend-m1-plan §4.2 判据 9）；
//   - 域类型隔离：Recommend 返回本域 BinSuggestion，router 桥接为 purchase.BinSuggestion
//     （先例 m3_bridges.go 各 Flags 桥）。
//
// 算法分级（§5.3 注）：M2 基础规则 = 同 SKU 集中优先 + 库位剩余容量 + 库位启用态；
// 温区/重量/拣货频次等多因子属阶段 18，不实现不造假。

import (
	"context"
	"fmt"
	"sort"

	"gorm.io/gorm"
)

// SKUBinReader SKU 库位分布读取接口（本域为消费方，实现由 inventory 域提供；
// 返回仅内建类型——binID → 该 SKU 六状态在库合计）。
type SKUBinReader interface {
	QtyByBinForSKU(ctx context.Context, warehouseID, skuID int64) (map[int64]float64, error)
}

// BinSuggestion 推荐库位候选（本域形态；router 桥接 purchase.BinSuggestion 逐字段构造）。
type BinSuggestion struct {
	BinID   int64
	ZoneID  int64
	ShelfID int64
	Score   float64 // 推荐序（降序）
	Reason  string  // 分配理由展示（business-flow §8.1 展示义务同源）
}

// scoring 基础规则权重（可读常量；score 仅表序不表概率）。
const (
	// scoreSameSKU 同 SKU 集中存放加分（§5.3 首要因子）。
	scoreSameSKU = 2.0
	// scoreRemainingWeight 剩余容量占比权重（0-1 归一）。
	scoreRemainingWeight = 1.0
	// recommendLimit 推荐候选上限（响应体防放大）。
	recommendLimit = 10
)

// BinRecommenderService 推荐库位实现（purchase.BinRecommender 数据面）。
type BinRecommenderService struct {
	db        *gorm.DB
	occupancy BinOccupancyReader // 库位在库合计（inventory 域提供）
	skuBins   SKUBinReader       // 同 SKU 库位分布（inventory 域提供）
}

// NewBinRecommender 构建（router 装配：purchase.WithBinRecommender(桥接)）。
func NewBinRecommender(db *gorm.DB, occupancy BinOccupancyReader, skuBins SKUBinReader) *BinRecommenderService {
	return &BinRecommenderService{db: db, occupancy: occupancy, skuBins: skuBins}
}

// recommendBinRow bins 表投影行（本域表直读）。
type recommendBinRow struct {
	ID          int64
	ZoneID      int64
	ShelfID     int64
	MaxCapacity float64
}

// Recommend 推荐库位（§5.3 基础规则）：
//  1. 候选 = 仓库内启用（ENABLED、未软删）库位；
//  2. 容量过滤：max_capacity > 0 时剩余容量（max_capacity − 在库合计）必须 ≥ 建议上架量；
//     max_capacity = 0 视为不限容（迁移默认值语义：0 = 未设置容量上限）；
//  3. 评分：同 SKU 已存放库位 +scoreSameSKU（集中优先），剩余容量占比 ×scoreRemainingWeight；
//     同分按 bin_id 升序稳定排序（同输入同输出，可重放）。
//
// 无候选返回空切片（调用方 purchase 侧转 ErrBinRequired fail-closed，不造假推荐）。
func (r *BinRecommenderService) Recommend(ctx context.Context, warehouseID, skuID int64, qty float64) ([]BinSuggestion, error) {
	if r.db == nil {
		return nil, fmt.Errorf("warehouse.NewBinRecommender: db 未装配")
	}
	if r.occupancy == nil || r.skuBins == nil {
		return nil, fmt.Errorf("warehouse.NewBinRecommender: occupancy/skuBins 未注入（router 装配缺失）")
	}
	var bins []recommendBinRow
	err := r.db.WithContext(ctx).Model(&Bin{}).
		Select("id, zone_id, shelf_id, max_capacity").
		Where("warehouse_id = ? AND status = ?", warehouseID, StatusEnabled).
		Order("id ASC").
		Limit(1000). // 单仓库位规模上限内一次取数；异常超大仓按 id 升序截断（可预期）
		Scan(&bins).Error
	if err != nil {
		return nil, fmt.Errorf("读取仓库 %d 启用库位失败: %w", warehouseID, err)
	}
	if len(bins) == 0 {
		return []BinSuggestion{}, nil
	}
	quantity, _, err := r.occupancy.OccupancyByWarehouse(ctx, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("读取仓库 %d 库位占用失败: %w", warehouseID, err)
	}
	skuQty, err := r.skuBins.QtyByBinForSKU(ctx, warehouseID, skuID)
	if err != nil {
		return nil, fmt.Errorf("读取仓库 %d SKU %d 库位分布失败: %w", warehouseID, skuID, err)
	}

	out := make([]BinSuggestion, 0, len(bins))
	for _, b := range bins {
		used := quantity[b.ID] // 缺省键 = 无占用
		if b.MaxCapacity > 0 && b.MaxCapacity-used < qty {
			continue // 剩余容量不足（§5.3 因子二）
		}
		s := BinSuggestion{BinID: b.ID, ZoneID: b.ZoneID, ShelfID: b.ShelfID}
		if skuQty[b.ID] > 0 {
			s.Score += scoreSameSKU
			s.Reason = "同SKU集中存放"
		}
		if b.MaxCapacity > 0 {
			remaining := b.MaxCapacity - used
			if remaining < 0 {
				remaining = 0
			}
			s.Score += scoreRemainingWeight * (remaining / b.MaxCapacity)
		}
		if s.Reason == "" {
			s.Reason = "空位可用"
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].BinID < out[j].BinID
	})
	if len(out) > recommendLimit {
		out = out[:recommendLimit]
	}
	return out, nil
}
