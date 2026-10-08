package masterdata

// devices_resolve 设备扫码域 resolve 匹配器 2 的实现侧（backend-m3-plan §2.2 D 冻结清单
// "仅新增"文件——本域读自己的表，实现消费方 internal/devices 定义的窄接口；
// router 装配经 devices.WithSKUBarcodes 注入；plan §12.2 前例 masterdata/m2readers.go 同款）。
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

// SKUBarcodeResolveService SKU 条码解析读取实现（barcodes 表唯一索引精确命中——
// scanner.md §5.2 匹配器 2，uk_barcodes_barcode 一码一 SKU；SFQR 载荷查证共用本
// 实现——qr-code.md §6 同一实现双接口）。
type SKUBarcodeResolveService struct {
	db *gorm.DB
}

// NewSKUBarcodeReader 构建 SKU 条码读取器（router 装配：devices.WithSKUBarcodes）。
func NewSKUBarcodeReader(db *gorm.DB) *SKUBarcodeResolveService {
	return &SKUBarcodeResolveService{db: db}
}

// FindByBarcode 按条码取 SKU 命中；found=false 表示未命中（含条码行悬空——sku/product
// 已删的脏数据按未命中落回后续匹配器，最终 UNKNOWN_BARCODE）。
// Hit.Status 携带 SKU 启用位（ENABLED/DISABLED）：停用由消费方转 SKU_NOT_FOUND
// （plan §8.3 条 6"命中类型但对象不存在/停用"）。
func (s *SKUBarcodeResolveService) FindByBarcode(ctx context.Context, code string) (devices.Hit, bool, error) {
	var row struct {
		ID          int64  `gorm:"column:id"`
		SKUCode     string `gorm:"column:sku_code"`
		SKUID       int64  `gorm:"column:sku_id"` // 显式 tag：gorm 默认把 SKUID 映射为 sk_uid
		ProductName string `gorm:"column:product_name"`
		IsEnabled   bool   `gorm:"column:is_enabled"`
	}
	err := s.db.WithContext(ctx).
		Table("barcodes").
		Select("skus.id AS id, skus.code AS sku_code, skus.id AS sku_id, products.name AS product_name, skus.is_enabled").
		Joins("JOIN skus ON skus.id = barcodes.sku_id AND skus.deleted_at IS NULL").
		Joins("JOIN products ON products.id = skus.product_id AND products.deleted_at IS NULL").
		Where("barcodes.barcode = ?", code).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return devices.Hit{}, false, nil
		}
		return devices.Hit{}, false, fmt.Errorf("按条码 %s 查询 SKU 失败: %w", code, err)
	}
	status := "ENABLED"
	if !row.IsEnabled {
		status = "DISABLED"
	}
	return devices.Hit{
		ID:     row.ID,
		Code:   row.SKUCode,
		Name:   row.ProductName,
		Status: status,
	}, true, nil
}

// FindBySkuCode 按 SKU 编码取命中（resolve 管线第 0 段 SFQR 载荷查证——qr-code.md §6；
// devices.SfqrSkuReader 实现，router 装配 WithSfqrSkus 与 WithSKUBarcodes 同一实现双接口）：
// skus.code 精确命中（uk_skus_code 部分唯一索引）JOIN products，双 deleted_at IS NULL
// 对齐既有 FindByBarcode 口径；软删未命中 → found=false。Hit.Status 携带 SKU 启用位
// （ENABLED/DISABLED）：停用由消费方转 SKU_NOT_FOUND（与条码分支同口径）。
func (s *SKUBarcodeResolveService) FindBySkuCode(ctx context.Context, code string) (devices.Hit, bool, error) {
	var row struct {
		ID          int64  `gorm:"column:id"`
		SKUCode     string `gorm:"column:sku_code"`
		ProductName string `gorm:"column:product_name"`
		IsEnabled   bool   `gorm:"column:is_enabled"`
	}
	err := s.db.WithContext(ctx).
		Table("skus").
		Select("skus.id AS id, skus.code AS sku_code, products.name AS product_name, skus.is_enabled").
		Joins("JOIN products ON products.id = skus.product_id AND products.deleted_at IS NULL").
		Where("skus.code = ? AND skus.deleted_at IS NULL", code).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return devices.Hit{}, false, nil
		}
		return devices.Hit{}, false, fmt.Errorf("按 SKU 编码 %s 查询失败: %w", code, err)
	}
	status := "ENABLED"
	if !row.IsEnabled {
		status = "DISABLED"
	}
	return devices.Hit{
		ID:     row.ID,
		Code:   row.SKUCode,
		Name:   row.ProductName,
		Status: status,
	}, true, nil
}
