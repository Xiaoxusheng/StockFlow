package masterdata

// SKU 标签打印内容装配（backend-m3-plan §12.2：printing.ContentReader ×9 之本域实现，
// router 装配经 printing.WithContentReader(printing.ObjectSKULabel, ...) 注入）。
// 本文件只 import internal/printing 平台契约包（合法方向，plan §2.3 判据 2）。
//
// 数据面（打印内容装配白名单，Orchestrator 裁决 2026-10-03——internal/printing/doc.go）：
//   - 本域表：skus / products / units / barcodes（SKU 标签预设字段全部本域自洽）；
//   - 只读 SELECT 零写通路；不触碰库存族表；不 import 其他业务域包。
//
// 装配契约（printing.ContentReader）：逐对象校验存在，任一缺失整体拒绝
// （printing.FailMissing）；命中停用 SKU 整体拒绝 PRINT_SKU_DISABLED（details 携
// disabled_ids=不可打印 SKU 编码——qr-code.md §9 不可打印校验，约束 10「商品已停用」）；
// 主码内容 = 主条码（is_primary 优先，次取最小 id 条码，无条码回退 SKU 编码——
// 扫码直达 SKU 依据 printing.md §4.2）。
//
// values["sku_code"] 恒产出（置于字段绑定判定前，不受模板绑定影响）：SFQR 载荷
// 构造依据（qr-code.md §7.3——标签 QR 内容 = SFQR|1|SKU|<sku_code>），其余键仍按
// 模板 fields 绑定装配（未绑定的键不出现——渲染层留空展示）。
//
// qty 字段说明：SKU 无数量属性（前端预设键，供标签数量语义），本 reader 不产出
// 该键——渲染层留空展示。

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/printing"
)

// NewSKUContentReader 构建 SKU 标签内容装配器（router 装配注入 printing）。
func NewSKUContentReader(db *gorm.DB) *SKUContentReader {
	return &SKUContentReader{db: db}
}

// SKUContentReader SKU 标签装配实现（只读本域表）。
type SKUContentReader struct {
	db *gorm.DB
}

// skuLabelRow 装配投影行（纯展示列；IsEnabled 供停用拒绝判定——qr-code.md §9。
// 列名显式 tag 对齐 models.go 既有约定——gorm 默认把 SKUID 映射为 sk_uid，
// 与 SELECT 别名 sku_id 不符）。
type skuLabelRow struct {
	SKUID       int64  `gorm:"column:sku_id"` // skus.id
	SKUCode     string `gorm:"column:sku_code"`
	ProductName string `gorm:"column:product_name"`
	Spec        string `gorm:"column:spec"`
	UnitName    string `gorm:"column:unit_name"`
	IsEnabled   bool   `gorm:"column:is_enabled"`
}

// barcodeRow 条码投影行（主码内容来源；SKUID 显式 tag 对齐 models.go:106 约定——
// gorm 默认映射 sk_uid 与 SELECT 别名 sku_id 不符，缺 tag 会使主条码恒装配失败、
// 退化到 SKU 编码回退分支。修复只影响新任务：print_task_rows 创建时快照冻结，
// 历史行不可变）。
type barcodeRow struct {
	SKUID     int64  `gorm:"column:sku_id"`
	Barcode   string `gorm:"column:barcode"`
	IsPrimary bool   `gorm:"column:is_primary"`
}

// Assemble 实现 printing.ContentReader（plan §12.2 冻结签名）。
func (r *SKUContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var rows []skuLabelRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT s.id AS sku_id, s.code AS sku_code,
		       COALESCE(p.name, '') AS product_name,
		       COALESCE(p.spec, '') AS spec,
		       COALESCE(u.name, '') AS unit_name,
		       s.is_enabled
		FROM skus s
		LEFT JOIN products p ON p.id = s.product_id
		LEFT JOIN units u ON u.id = p.unit_id
		WHERE s.id IN ?`, parsed).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]skuLabelRow, len(rows))
	for _, row := range rows {
		byID[row.SKUID] = row
	}

	// 主条码：is_primary 优先、次取最小 id（一码一 SKU 全局唯一 uk_barcodes_barcode）。
	var codes []barcodeRow
	err = r.db.WithContext(ctx).Raw(`
		SELECT b.sku_id, b.barcode, b.is_primary
		FROM barcodes b
		WHERE b.sku_id IN ?
		ORDER BY b.sku_id, b.is_primary DESC, b.id`, parsed).Scan(&codes).Error
	if err != nil {
		return nil, err
	}
	mainCode := make(map[int64]string, len(codes))
	for _, b := range codes {
		if _, dup := mainCode[b.SKUID]; !dup { // 排序后首条即主条码
			mainCode[b.SKUID] = b.Barcode
		}
	}

	fieldSet := printing.FieldFilter(fields)
	out := make([]printing.ContentRow, 0, len(parsed))
	var disabled []string // 停用 SKU 编码（qr-code.md §9 不可打印清单）
	for _, id := range parsed {
		row, ok := byID[id]
		if !ok {
			missing = append(missing, printing.FormatID(id))
			continue
		}
		if !row.IsEnabled {
			disabled = append(disabled, row.SKUCode)
			continue
		}
		code := mainCode[id]
		if code == "" {
			code = row.SKUCode // 无条码回退 SKU 编码
		}
		// sku_code 恒产出（SFQR 载荷构造依据，不受模板绑定影响——文件头注）。
		values := map[string]string{"sku_code": row.SKUCode}
		if fieldSet["product_name"] {
			values["product_name"] = row.ProductName
		}
		if fieldSet["spec"] {
			values["spec"] = row.Spec
		}
		if fieldSet["unit"] {
			values["unit"] = row.UnitName
		}
		out = append(out, printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   code,
			Values: values,
		})
	}
	// 数据完整性优先于可打印性：先缺失整体拒绝，再停用整体拒绝（均禁止部分行静默）。
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	if len(disabled) > 0 {
		return nil, printing.NewDataDisabledError(disabled)
	}
	return out, nil
}
