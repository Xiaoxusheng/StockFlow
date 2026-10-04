package warehouse

// devices_resolve 设备扫码域 resolve 匹配器 3 的实现侧（backend-m3-plan §2.2 D 冻结清单
// "仅新增"文件——本域读自己的表，实现消费方 internal/devices 定义的窄接口；
// router 装配经 devices.WithBins 注入；plan §12.2 前例 masterdata/m2readers.go 同款）。
//
// 只读 SELECT，无任何写通路；签名只含内建类型与 devices 包值类型（devices.Hit），
// 不携带本域类型（域包 → 平台包单向依赖，plan §2.3 判据 2）。

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/devices"
)

// BinCodeResolveService 库位码解析读取实现（scanner.md §5.2 匹配器 3）。
type BinCodeResolveService struct {
	db *gorm.DB
}

// NewBinCodeReader 构建库位码读取器（router 装配：devices.WithBins）。
func NewBinCodeReader(db *gorm.DB) *BinCodeResolveService {
	return &BinCodeResolveService{db: db}
}

// FindByCode 按库位编码取命中列表——库位编码仓内唯一（uk_bins_warehouse_code），
// 跨仓同码返回多命中由前端选择（plan §8.3 歧义消解）；空列表 = 未命中。
// Hit.Status 携带库位状态（ENABLED/DISABLED）：全部停用由消费方转 BIN_NOT_FOUND。
func (s *BinCodeResolveService) FindByCode(ctx context.Context, code string) ([]devices.Hit, error) {
	var rows []struct {
		ID          int64
		Code        string
		WarehouseID int64
		WHCode      string
		Status      string
	}
	err := s.db.WithContext(ctx).
		Table("bins").
		Select("bins.id, bins.code, bins.warehouse_id, warehouses.code AS wh_code, bins.status").
		Joins("JOIN warehouses ON warehouses.id = bins.warehouse_id AND warehouses.deleted_at IS NULL").
		Where("bins.code = ? AND bins.deleted_at IS NULL", code).
		Order("bins.warehouse_id, bins.id").
		Limit(16). // 歧义列表上限（跨仓同码属部署异常形态，防御无界列表）
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("按编码 %s 查询库位失败: %w", code, err)
	}
	hits := make([]devices.Hit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, devices.Hit{
			ID:          r.ID,
			Code:        r.Code,
			Name:        r.WHCode + "-" + r.Code,
			WarehouseID: r.WarehouseID,
			Status:      r.Status,
		})
	}
	return hits, nil
}

// 编译期断言：实现消费方窄接口（plan §12.2 冻结签名）。
var _ devices.BinCodeReader = (*BinCodeResolveService)(nil)
