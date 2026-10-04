package sales

// 出库单/拣货单/发货单打印内容装配（backend-m3-plan §12.2：printing.ContentReader ×9
// 之本域三个实现，router 装配经 printing.WithContentReader(...) 注入）。
// 本文件只 import internal/printing 平台契约包（合法方向，plan §2.3 判据 2）。
//
// 数据面（打印内容装配白名单，Orchestrator 裁决 2026-10-03——internal/printing/doc.go）：
//   - 本域表：outbound_orders / outbound_items / pick_tasks / shipments /
//     sales_orders（来源单）/ check_tasks（复核操作人姓名）/ allocation_records（拣货库位）；
//   - 他域纯展示列 JOIN：customers.name、warehouses.name、bins.code、skus.code、
//     products.name/spec、units.name；
//   - 只读 SELECT 零写通路；不触碰库存族表（batches 不可访问——明细行 batch_no
//     无合法来源，装配不含该键）；不 import 其他业务域包。
//
// 装配契约（printing.ContentReader）：逐对象校验存在，任一缺失整体拒绝；主码内容 =
// 对应单据号（出库单号/拣货任务号/发货单号）。

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/printing"
)

// ---- 展示列公共投影（本包内三 reader 共用）----

// skuDisplay SKU 展示列投影。
type skuDisplay struct {
	Code        string // skus.code
	ProductName string // products.name
	Spec        string // products.spec
	Unit        string // units.name
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

// fillSkuLine 明细行公共投影（line_no/sku_code/product_name/spec/unit/qty/actual_qty；
// batch_no 无合法来源不产出——白名单禁访问 batches）。
func fillSkuLine(line map[string]string, lineNo int64, d skuDisplay, qty, actualQty string, extra map[string]string) {
	line["line_no"] = strconv.FormatInt(lineNo, 10)
	line["sku_code"] = d.Code
	line["product_name"] = d.ProductName
	line["spec"] = d.Spec
	line["unit"] = d.Unit
	line["qty"] = qty
	if actualQty != "" {
		line["actual_qty"] = actualQty
	}
	for k, v := range extra {
		line[k] = v
	}
}

// ============================================================================
// 出库单 OUTBOUND_ORDER
// ============================================================================

// NewOutboundContentReader 构建出库单内容装配器（router 装配注入 printing）。
func NewOutboundContentReader(db *gorm.DB) *OutboundContentReader {
	return &OutboundContentReader{db: db}
}

// OutboundContentReader 出库单装配实现。
type OutboundContentReader struct {
	db *gorm.DB
}

type outboundHeadRow struct {
	OutboundID    int64  // outbound_orders.id
	OutboundNo    string // outbound_orders.outbound_no
	CustomerName  string // customers.name（经 so_no → sales_orders）
	WarehouseName string // warehouses.name
	OutboundAt    string // to_char(shipped_at)
	Operator      string // 最近复核任务经办人（check_tasks.assignee_name）
}

type outboundLineRow struct {
	OutboundID int64
	LineNo     int64
	SKUID      int64
	Qty        string // CAST(qty AS varchar)
	QtyPicked  string // CAST(qty_picked AS varchar)
}

// Assemble 实现 printing.ContentReader。
func (r *OutboundContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var heads []outboundHeadRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT o.id AS outbound_id, o.outbound_no,
		       COALESCE(cu.name, '') AS customer_name,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(to_char(o.shipped_at, 'YYYY-MM-DD HH24:MI:SS'), '') AS outbound_at,
		       '' AS operator
		FROM outbound_orders o
		LEFT JOIN sales_orders so ON so.so_no = o.so_no
		LEFT JOIN customers cu ON cu.id = so.customer_id
		LEFT JOIN warehouses w ON w.id = o.warehouse_id
		WHERE o.id IN ?`, parsed).Scan(&heads).Error
	if err != nil {
		return nil, err
	}
	headByID := make(map[int64]outboundHeadRow, len(heads))
	outboundNos := make([]string, 0, len(heads))
	for _, h := range heads {
		headByID[h.OutboundID] = h
		outboundNos = append(outboundNos, h.OutboundNo)
	}

	// 操作人 = 最近复核任务经办人（出库执行链最后一个具名环节；后写覆盖取最新）。
	operators := map[string]string{}
	if len(outboundNos) > 0 {
		var rows []struct {
			OutboundNo   string
			AssigneeName string
		}
		err = r.db.WithContext(ctx).Raw(`
			SELECT ct.outbound_no, ct.assignee_name
			FROM check_tasks ct
			WHERE ct.outbound_no IN ? AND COALESCE(ct.assignee_name, '') <> ''
			ORDER BY ct.id`, outboundNos).Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			operators[row.OutboundNo] = row.AssigneeName
		}
	}

	var lines []outboundLineRow
	err = r.db.WithContext(ctx).Raw(`
		SELECT i.outbound_id, i.line_no, i.sku_id,
		       CAST(i.qty AS varchar) AS qty,
		       CAST(i.qty_picked AS varchar) AS qty_picked
		FROM outbound_items i
		WHERE i.outbound_id IN ?
		ORDER BY i.outbound_id, i.line_no`, parsed).Scan(&lines).Error
	if err != nil {
		return nil, err
	}
	linesByOrder := make(map[int64][]outboundLineRow, len(heads))
	skuSet := map[int64]bool{}
	for _, l := range lines {
		linesByOrder[l.OutboundID] = append(linesByOrder[l.OutboundID], l)
		skuSet[l.SKUID] = true
	}
	// 拣货来源库位 = 该行首次分配库位（多 bin 部分拣货场景取首个分配——打印口径）。
	lineBins, err := firstAllocBins(ctx, r.db, outboundNos)
	if err != nil {
		return nil, err
	}
	skuMap, err := skuDisplayByIDs(ctx, r.db, distinctIDs(mapKeys(skuSet)))
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
		head.Operator = operators[head.OutboundNo]
		values := map[string]string{}
		if fieldSet["order_no"] {
			values["order_no"] = head.OutboundNo
		}
		if fieldSet["customer_name"] {
			values["customer_name"] = head.CustomerName
		}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = head.WarehouseName
		}
		if fieldSet["outbound_at"] {
			values["outbound_at"] = head.OutboundAt
		}
		if fieldSet["operator"] {
			values["operator"] = head.Operator
		}
		row := printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   head.OutboundNo,
			Values: values,
			Lines:  []map[string]string{},
		}
		for _, l := range linesByOrder[id] {
			line := map[string]string{}
			extra := map[string]string{}
			if b, ok := lineBins[head.OutboundNo+"|"+strconv.FormatInt(l.LineNo, 10)]; ok {
				extra["bin_code"] = b
			}
			fillSkuLine(line, l.LineNo, skuMap[l.SKUID], l.Qty, l.QtyPicked, extra)
			row.Lines = append(row.Lines, line)
		}
		out = append(out, row)
	}
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}

// firstAllocBins 出库行首次分配库位（allocation_records 本域表 + bins.code 展示列；
// key = outbound_no|line_no）。
func firstAllocBins(ctx context.Context, db *gorm.DB, outboundNos []string) (map[string]string, error) {
	out := map[string]string{}
	if len(outboundNos) == 0 {
		return out, nil
	}
	var rows []struct {
		OutboundNo string
		LineNo     int64
		BinCode    string
	}
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (ar.outbound_no, ar.line_no)
		       ar.outbound_no, ar.line_no, COALESCE(b.code, '') AS bin_code
		FROM allocation_records ar
		LEFT JOIN bins b ON b.id = ar.bin_id
		WHERE ar.outbound_no IN ?
		ORDER BY ar.outbound_no, ar.line_no, ar.id`, outboundNos).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.OutboundNo+"|"+strconv.FormatInt(r.LineNo, 10)] = r.BinCode
	}
	return out, nil
}

// ============================================================================
// 拣货单 PICK_ORDER
// ============================================================================

// NewPickContentReader 构建拣货单内容装配器（router 装配注入 printing）。
// 拣货任务为行级任务：一次装配 = 一个任务一行明细。
func NewPickContentReader(db *gorm.DB) *PickContentReader {
	return &PickContentReader{db: db}
}

// PickContentReader 拣货单装配实现。
type PickContentReader struct {
	db *gorm.DB
}

type pickRow struct {
	PickID         int64  // pick_tasks.id
	PickNo         string // pick_tasks.pick_no
	PickerName     string // assignee_name
	WarehouseName  string // warehouses.name（source_warehouse_id）
	PickAt         string // to_char(picked_at)
	OutboundLineNo int64
	SKUID          int64
	SourceBinID    int64
	Qty            string
	QtyPicked      string
}

// Assemble 实现 printing.ContentReader。
func (r *PickContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var rows []pickRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.id AS pick_id, t.pick_no,
		       COALESCE(t.assignee_name, '') AS picker_name,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(to_char(t.picked_at, 'YYYY-MM-DD HH24:MI:SS'), '') AS pick_at,
		       t.outbound_line_no, t.sku_id, t.source_bin_id,
		       CAST(t.qty AS varchar) AS qty,
		       CAST(t.picked_qty AS varchar) AS qty_picked
		FROM pick_tasks t
		LEFT JOIN warehouses w ON w.id = t.source_warehouse_id
		WHERE t.id IN ?`, parsed).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]pickRow, len(rows))
	binIDs := make([]int64, 0, len(rows))
	skuIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		byID[row.PickID] = row
		if row.SourceBinID > 0 {
			binIDs = append(binIDs, row.SourceBinID)
		}
		skuIDs = append(skuIDs, row.SKUID)
	}
	binCodes, err := binCodesByIDs(ctx, r.db, distinctIDs(binIDs))
	if err != nil {
		return nil, err
	}
	skuMap, err := skuDisplayByIDs(ctx, r.db, distinctIDs(skuIDs))
	if err != nil {
		return nil, err
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
		if fieldSet["order_no"] {
			values["order_no"] = row.PickNo
		}
		if fieldSet["picker_name"] {
			values["picker_name"] = row.PickerName
		}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = row.WarehouseName
		}
		if fieldSet["pick_at"] {
			values["pick_at"] = row.PickAt
		}
		line := map[string]string{}
		extra := map[string]string{}
		if b, ok := binCodes[row.SourceBinID]; ok {
			extra["bin_code"] = b
		}
		fillSkuLine(line, row.OutboundLineNo, skuMap[row.SKUID], row.Qty, row.QtyPicked, extra)
		out = append(out, printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   row.PickNo,
			Values: values,
			Lines:  []map[string]string{line},
		})
	}
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
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

// ============================================================================
// 发货单 SHIPMENT_ORDER
// ============================================================================

// NewShipmentContentReader 构建发货单内容装配器（router 装配注入 printing）。
func NewShipmentContentReader(db *gorm.DB) *ShipmentContentReader {
	return &ShipmentContentReader{db: db}
}

// ShipmentContentReader 发货单装配实现。
type ShipmentContentReader struct {
	db *gorm.DB
}

type shipmentRow struct {
	ShipmentID    int64  // shipments.id
	ShipmentNo    string // shipments.shipment_no
	OutboundNo    string // shipments.outbound_no
	OutboundID    int64  // outbound_orders.id
	CarrierName   string // shipments.carrier
	WarehouseName string // warehouses.name
	ShipmentAt    string // to_char(shipped_at)
	Operator      string // shipper_name
}

// Assemble 实现 printing.ContentReader。
func (r *ShipmentContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	var rows []shipmentRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT sh.id AS shipment_id, sh.shipment_no, sh.outbound_no,
		       o.id AS outbound_id,
		       COALESCE(sh.carrier, '') AS carrier_name,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(to_char(sh.shipped_at, 'YYYY-MM-DD HH24:MI:SS'), '') AS shipment_at,
		       COALESCE(sh.shipper_name, '') AS operator
		FROM shipments sh
		LEFT JOIN outbound_orders o ON o.outbound_no = sh.outbound_no
		LEFT JOIN warehouses w ON w.id = sh.warehouse_id
		WHERE sh.id IN ?`, parsed).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]shipmentRow, len(rows))
	outboundIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		byID[row.ShipmentID] = row
		if row.OutboundID > 0 {
			outboundIDs = append(outboundIDs, row.OutboundID)
		}
	}
	// 客户名与发货明细经出库单关联（本域单据表 + customers.name 展示列）。
	customers, err := customerNameByOutboundIDs(ctx, r.db, distinctIDs(outboundIDs))
	if err != nil {
		return nil, err
	}
	shipLines, err := outboundShippedLines(ctx, r.db, distinctIDs(outboundIDs))
	if err != nil {
		return nil, err
	}
	skuIDs := make([]int64, 0)
	for _, ls := range shipLines {
		for _, l := range ls {
			skuIDs = append(skuIDs, l.SKUID)
		}
	}
	skuMap, err := skuDisplayByIDs(ctx, r.db, distinctIDs(skuIDs))
	if err != nil {
		return nil, err
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
		if fieldSet["order_no"] {
			values["order_no"] = row.ShipmentNo
		}
		if fieldSet["customer_name"] {
			values["customer_name"] = customers[row.OutboundID]
		}
		if fieldSet["carrier_name"] {
			values["carrier_name"] = row.CarrierName
		}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = row.WarehouseName
		}
		if fieldSet["shipment_at"] {
			values["shipment_at"] = row.ShipmentAt
		}
		if fieldSet["operator"] {
			values["operator"] = row.Operator
		}
		content := printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   row.ShipmentNo,
			Values: values,
			Lines:  []map[string]string{},
		}
		for _, l := range shipLines[row.OutboundID] {
			line := map[string]string{}
			fillSkuLine(line, l.LineNo, skuMap[l.SKUID], l.Qty, l.QtyShipped, nil)
			content.Lines = append(content.Lines, line)
		}
		out = append(out, content)
	}
	if err := printing.FailMissing(missing); err != nil {
		return nil, err
	}
	return out, nil
}

// outboundCustomer 客户名投影行。
type outboundCustomerRow struct {
	OutboundID   int64
	CustomerName string
}

// customerNameByOutboundIDs 出库单 → 客户名（本域 sales_orders + customers.name 展示列）。
func customerNameByOutboundIDs(ctx context.Context, db *gorm.DB, outboundIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(outboundIDs))
	if len(outboundIDs) == 0 {
		return out, nil
	}
	var rows []outboundCustomerRow
	err := db.WithContext(ctx).Raw(`
		SELECT o.id AS outbound_id, COALESCE(cu.name, '') AS customer_name
		FROM outbound_orders o
		LEFT JOIN sales_orders so ON so.so_no = o.so_no
		LEFT JOIN customers cu ON cu.id = so.customer_id
		WHERE o.id IN ?`, outboundIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.OutboundID] = r.CustomerName
	}
	return out, nil
}

// shippedLineRow 发货明细投影行（outbound_items 本域表，实发 = qty_shipped）。
type shippedLineRow struct {
	OutboundID int64
	LineNo     int64
	SKUID      int64
	Qty        string
	QtyShipped string
}

// outboundShippedLines 发货明细（仅已发货行 qty_shipped > 0）。
func outboundShippedLines(ctx context.Context, db *gorm.DB, outboundIDs []int64) (map[int64][]shippedLineRow, error) {
	out := make(map[int64][]shippedLineRow, len(outboundIDs))
	if len(outboundIDs) == 0 {
		return out, nil
	}
	var rows []shippedLineRow
	err := db.WithContext(ctx).Raw(`
		SELECT i.outbound_id, i.line_no, i.sku_id,
		       CAST(i.qty AS varchar) AS qty,
		       CAST(i.qty_shipped AS varchar) AS qty_shipped
		FROM outbound_items i
		WHERE i.outbound_id IN ? AND i.qty_shipped > 0
		ORDER BY i.outbound_id, i.line_no`, outboundIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.OutboundID] = append(out[r.OutboundID], r)
	}
	return out, nil
}

// mapKeys 集合键切片（IN 参数收敛）。
func mapKeys(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
