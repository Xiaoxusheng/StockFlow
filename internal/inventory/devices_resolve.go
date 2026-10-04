package inventory

// devices_resolve 设备扫码域 resolve 匹配器 4/5 的实现侧（backend-m3-plan §2.2 D 冻结清单
// "仅新增"文件——本域读自己的表，实现消费方 internal/devices 定义的窄接口；
// router 装配经 devices.WithSerials/WithBatches 注入；plan §12.2 前例
// masterdata/m2readers.go、serialread.go 同款）。
//
// 前缀分派（plan §8.3 条 1 / §12.2 单一映射表 devices.docPrefixOwners）：
// 序列号（serial_numbers 全局唯一，uk_serial_numbers_serial_no）/
// 批次码（batches 批次号 SKU 内唯一，uk_batches_sku_batch_no——跨 SKU 命中返回列表）。
//
// 只读 SELECT，无任何写通路；签名只含内建类型与 devices 包值类型（devices.Hit），
// 不携带本域类型（域包 → 平台包单向依赖，plan §2.3 判据 2）。

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/devices"
)

// SerialResolveService 序列号解析读取实现（resolve 匹配器 4）。
type SerialResolveService struct {
	db *gorm.DB
}

// NewSerialReader 构建序列号读取器（router 装配：devices.WithSerials）。
func NewSerialReader(db *gorm.DB) *SerialResolveService {
	return &SerialResolveService{db: db}
}

// FindSerial 按序列号取台账命中；found=false 表示不存在。
// 台账状态（IN_STOCK/LOCKED/OUTBOUND/RETURNED/FROZEN）原样携带——序列号可用性校验
// 属业务执行侧（scanner.md §6.7：resolve 只识别，不因状态拒绝识别）。
func (s *SerialResolveService) FindSerial(ctx context.Context, sn string) (devices.Hit, bool, error) {
	var row struct {
		ID          int64
		SerialNo    string
		SKUID       int64
		SKUCode     string
		WarehouseID int64
		Status      string
	}
	err := s.db.WithContext(ctx).
		Table("serial_numbers").
		Select("serial_numbers.id, serial_numbers.serial_no, serial_numbers.sku_id, "+
			"serial_numbers.warehouse_id, serial_numbers.status, skus.code AS sku_code").
		Joins("LEFT JOIN skus ON skus.id = serial_numbers.sku_id AND skus.deleted_at IS NULL").
		Where("serial_numbers.serial_no = ?", sn).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return devices.Hit{}, false, nil
		}
		return devices.Hit{}, false, fmt.Errorf("按序列号 %s 查询台账失败: %w", sn, err)
	}
	name := "序列号 " + row.SerialNo
	if row.SKUCode != "" {
		name += "（" + row.SKUCode + "）"
	}
	return devices.Hit{
		ID:          row.ID,
		Code:        row.SerialNo,
		Name:        name,
		WarehouseID: row.WarehouseID,
		Status:      row.Status,
	}, true, nil
}

// BatchResolveService 批次码解析读取实现（resolve 匹配器 5）。
type BatchResolveService struct {
	db *gorm.DB
}

// NewBatchReader 构建批次码读取器（router 装配：devices.WithBatches）。
func NewBatchReader(db *gorm.DB) *BatchResolveService {
	return &BatchResolveService{db: db}
}

// FindBatch 按批次号取命中列表——批次号 SKU 内唯一（uk_batches_sku_batch_no），
// 跨 SKU 同批次号返回多命中（plan §8.3 歧义消解）；空列表 = 未命中。
// 批次为 SKU 级数据（无仓库维度），Hit.WarehouseID=0；归属库存分布由业务查询承担。
func (s *BatchResolveService) FindBatch(ctx context.Context, batchNo string) ([]devices.Hit, error) {
	var rows []struct {
		ID          int64
		BatchNo     string
		SKUID       int64
		SKUCode     string
		ProductName string
	}
	err := s.db.WithContext(ctx).
		Table("batches").
		Select("batches.id, batches.batch_no, batches.sku_id, skus.code AS sku_code, products.name AS product_name").
		Joins("JOIN skus ON skus.id = batches.sku_id AND skus.deleted_at IS NULL").
		Joins("JOIN products ON products.id = skus.product_id AND products.deleted_at IS NULL").
		Where("batches.batch_no = ?", batchNo).
		Order("batches.sku_id, batches.id").
		Limit(16). // 歧义列表上限（防御无界列表）
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("按批次号 %s 查询批次失败: %w", batchNo, err)
	}
	hits := make([]devices.Hit, 0, len(rows))
	for _, r := range rows {
		name := "批次 " + r.BatchNo
		if r.SKUCode != "" {
			name += "（" + r.SKUCode
			if r.ProductName != "" {
				name += " " + r.ProductName
			}
			name += "）"
		}
		hits = append(hits, devices.Hit{
			ID:     r.ID,
			Code:   r.BatchNo,
			Name:   name,
			Status: "",
		})
	}
	return hits, nil
}

// 编译期断言：实现消费方窄接口（plan §12.2 冻结签名）。
var (
	_ devices.SerialReader = (*SerialResolveService)(nil)
	_ devices.BatchReader  = (*BatchResolveService)(nil)
)
