package warehouse

// 库位标签打印内容装配（backend-m3-plan §12.2：printing.ContentReader ×9 之本域实现，
// router 装配经 printing.WithContentReader(printing.ObjectBinLabel, ...) 注入）。
// 本文件只 import internal/printing 平台契约包（合法方向，plan §2.3 判据 2）。
//
// 数据面（打印内容装配白名单，Orchestrator 裁决 2026-10-03——internal/printing/doc.go）：
//   - 本域表：bins + 展示列关联 shelves/zones/warehouses（库位标签预设字段全部本域自洽）；
//   - 只读 SELECT 零写通路；不触碰库存族表；不 import 其他业务域包。
//
// 装配契约（printing.ContentReader）：逐对象校验存在，任一缺失整体拒绝；主码内容 =
// 库位编码（库位二维码扫码直达——printing.md §4.3）。

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/printing"
)

// NewBinContentReader 构建库位标签内容装配器（router 装配注入 printing）。
func NewBinContentReader(db *gorm.DB) *BinContentReader {
	return &BinContentReader{db: db}
}

// BinContentReader 库位标签装配实现（只读本域表）。
type BinContentReader struct {
	db *gorm.DB
}

// binLabelRow 装配投影行（纯展示列）。
type binLabelRow struct {
	BinID         int64  // bins.id
	BinCode       string // bins.code
	WarehouseName string // warehouses.name
	ZoneName      string // zones.name
	ShelfCode     string // shelves.code
}

// Assemble 实现 printing.ContentReader（plan §12.2 冻结签名）。
func (r *BinContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var rows []binLabelRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT b.id AS bin_id, b.code AS bin_code,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(z.name, '') AS zone_name,
		       COALESCE(sf.code, '') AS shelf_code
		FROM bins b
		LEFT JOIN warehouses w ON w.id = b.warehouse_id
		LEFT JOIN zones z ON z.id = b.zone_id
		LEFT JOIN shelves sf ON sf.id = b.shelf_id
		WHERE b.id IN ?`, parsed).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]binLabelRow, len(rows))
	for _, row := range rows {
		byID[row.BinID] = row
	}

	fieldSet := printing.FieldFilter(fields)
	out := make([]printing.ContentRow, 0, len(parsed))
	for _, id := range parsed {
		row, ok := byID[id]
		if !ok {
			missing = append(missing, printing.FormatID(id))
			continue
		}
		values := map[string]string{}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = row.WarehouseName
		}
		if fieldSet["zone_name"] {
			values["zone_name"] = row.ZoneName
		}
		if fieldSet["shelf_code"] {
			values["shelf_code"] = row.ShelfCode
		}
		if fieldSet["bin_code"] {
			values["bin_code"] = row.BinCode
		}
		out = append(out, printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   row.BinCode,
			Values: values,
		})
	}
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}
