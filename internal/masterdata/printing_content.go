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
// （printing.FailMissing）；主码内容 = 主条码（is_primary 优先，次取最小 id 条码，
// 无条码回退 SKU 编码——扫码直达 SKU 依据 printing.md §4.2）。
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

// skuLabelRow 装配投影行（纯展示列）。
type skuLabelRow struct {
	SKUID       int64  // skus.id
	SKUCode     string // skus.code
	ProductName string // products.name
	Spec        string // products.spec
	UnitName    string // units.name
}

// barcodeRow 条码投影行（主码内容来源）。
type barcodeRow struct {
	SKUID     int64
	Barcode   string
	IsPrimary bool
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
		       COALESCE(u.name, '') AS unit_name
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
	for _, id := range parsed {
		row, ok := byID[id]
		if !ok {
			missing = append(missing, printing.FormatID(id))
			continue
		}
		code := mainCode[id]
		if code == "" {
			code = row.SKUCode // 无条码回退 SKU 编码
		}
		values := map[string]string{}
		if fieldSet["sku_code"] {
			values["sku_code"] = row.SKUCode
		}
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
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}
