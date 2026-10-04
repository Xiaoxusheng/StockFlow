package stockops

// 盘点单打印内容装配（backend-m3-plan §12.2：printing.ContentReader ×9 之本域实现，
// router 装配经 printing.WithContentReader(printing.ObjectCountOrder, ...) 注入）。
// 本文件只 import internal/printing 平台契约包（合法方向，plan §2.3 判据 2）。
//
// 数据面（打印内容装配白名单，Orchestrator 裁决 2026-10-03——internal/printing/doc.go）：
//   - 本域表：count_orders / count_items；
//   - 他域纯展示列 JOIN：warehouses.name、bins.code、skus.code、products.name/spec、
//     units.name；
//   - 只读 SELECT 零写通路；不触碰库存族表（batches/serial_numbers 不可访问——
//     盘点明细行 batch_no 无合法来源，装配不含该键）；不 import 其他业务域包。
//
// 装配契约（printing.ContentReader）：逐对象校验存在，任一缺失整体拒绝；主码内容 =
// 盘点单号。counter_name 说明：盘点人落列 counted_by 为用户 ID（000009 冻结 DDL），
// 本域无姓名列、用户表属 auth 域（不在装配白名单），装配不含该键——渲染层留空。

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/printing"
)

// NewCountContentReader 构建盘点单内容装配器（router 装配注入 printing）。
func NewCountContentReader(db *gorm.DB) *CountContentReader {
	return &CountContentReader{db: db}
}

// CountContentReader 盘点单装配实现。
type CountContentReader struct {
	db *gorm.DB
}

// countHeadRow 单据头投影行。
type countHeadRow struct {
	CountID       int64  // count_orders.id
	CountNo       string // count_orders.count_no
	WarehouseName string // warehouses.name
	CountAt       string // to_char(completed_at)（盘点完成时间；未完成为空）
}

// countLineRow 明细投影行（qty_counted NULL=未登记，投影为空串）。
type countLineRow struct {
	CountID    int64
	SeqNo      int64 // ROW_NUMBER 序号（count_items 无 line_no 列——000009 冻结 DDL）
	SKUID      int64
	BinID      int64
	QtySystem  string
	QtyCounted string
}

// Assemble 实现 printing.ContentReader（plan §12.2 冻结签名）。
func (r *CountContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var heads []countHeadRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT o.id AS count_id, o.count_no,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(to_char(o.completed_at, 'YYYY-MM-DD HH24:MI:SS'), '') AS count_at
		FROM count_orders o
		LEFT JOIN warehouses w ON w.id = o.warehouse_id
		WHERE o.id IN ?`, parsed).Scan(&heads).Error
	if err != nil {
		return nil, err
	}
	headByID := make(map[int64]countHeadRow, len(heads))
	for _, h := range heads {
		headByID[h.CountID] = h
	}

	var lines []countLineRow
	err = r.db.WithContext(ctx).Raw(`
		SELECT t.count_id,
		       ROW_NUMBER() OVER (PARTITION BY t.count_id ORDER BY t.id) AS seq_no,
		       t.sku_id, t.bin_id,
		       CAST(t.qty_system AS varchar) AS qty_system,
		       COALESCE(CAST(t.qty_counted AS varchar), '') AS qty_counted
		FROM count_items t
		WHERE t.count_id IN ?
		ORDER BY t.count_id, t.id`, parsed).Scan(&lines).Error
	if err != nil {
		return nil, err
	}
	linesByOrder := make(map[int64][]countLineRow, len(heads))
	skuSet := map[int64]bool{}
	binIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		linesByOrder[l.CountID] = append(linesByOrder[l.CountID], l)
		skuSet[l.SKUID] = true
		if l.BinID > 0 {
			binIDs = append(binIDs, l.BinID)
		}
	}
	skuMap, err := skuDisplayByIDs(ctx, r.db, distinctIDs(setKeys(skuSet)))
	if err != nil {
		return nil, err
	}
	binCodes, err := binCodesByIDs(ctx, r.db, distinctIDs(binIDs))
	if err != nil {
		return nil, err
	}

	fieldSet := printing.FieldFilter(fields)
	out := make([]printing.ContentRow, 0, len(parsed))
	for _, id := range parsed {
		head, ok := headByID[id]
		if !ok {
			missing = append(missing, printing.FormatID(id))
			continue
		}
		values := map[string]string{}
		if fieldSet["order_no"] {
			values["order_no"] = head.CountNo
		}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = head.WarehouseName
		}
		if fieldSet["count_at"] {
			values["count_at"] = head.CountAt
		}
		row := printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   head.CountNo,
			Values: values,
			Lines:  []map[string]string{},
		}
		for _, l := range linesByOrder[id] {
			line := map[string]string{
				"line_no":      strconv.FormatInt(l.SeqNo, 10),
				"sku_code":     skuMap[l.SKUID].Code,
				"product_name": skuMap[l.SKUID].ProductName,
				"spec":         skuMap[l.SKUID].Spec,
				"unit":         skuMap[l.SKUID].Unit,
				"qty":          l.QtySystem,
			}
			if l.QtyCounted != "" {
				line["actual_qty"] = l.QtyCounted // 实盘数（未登记留空）
			}
			if b, ok := binCodes[l.BinID]; ok {
				line["bin_code"] = b
			}
			row.Lines = append(row.Lines, line)
		}
		out = append(out, row)
	}
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}

// skuDisplay SKU 展示列投影。
type skuDisplay struct {
	Code        string
	ProductName string
	Spec        string
	Unit        string
}

// skuDisplayByIDs 批量取 SKU 展示列（白名单：masterdata 纯展示列）。
func skuDisplayByIDs(ctx context.Context, db *gorm.DB, skuIDs []int64) (map[int64]skuDisplay, error) {
	out := make(map[int64]skuDisplay, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SKUID       int64
		Code        string
		ProductName string
		Spec        string
		UnitName    string
	}
	err := db.WithContext(ctx).Raw(`
		SELECT s.id AS sku_id, s.code,
		       COALESCE(p.name, '') AS product_name,
		       COALESCE(p.spec, '') AS spec,
		       COALESCE(u.name, '') AS unit_name
		FROM skus s
		LEFT JOIN products p ON p.id = s.product_id
		LEFT JOIN units u ON u.id = p.unit_id
		WHERE s.id IN ?`, skuIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.SKUID] = skuDisplay{Code: r.Code, ProductName: r.ProductName, Spec: r.Spec, Unit: r.UnitName}
	}
	return out, nil
}

// binCodesByIDs 库位展示列（bins.code，白名单）。
func binCodesByIDs(ctx context.Context, db *gorm.DB, binIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(binIDs))
	if len(binIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		BinID   int64
		BinCode string
	}
	err := db.WithContext(ctx).Raw(`SELECT id AS bin_id, code AS bin_code FROM bins WHERE id IN ?`, binIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.BinID] = r.BinCode
	}
	return out, nil
}

// distinctIDs 去重（IN 参数收敛）。
func distinctIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// setKeys 集合键切片（IN 参数收敛）。
func setKeys(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
