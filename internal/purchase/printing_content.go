package purchase

// 入库单打印内容装配（backend-m3-plan §12.2：printing.ContentReader ×9 之本域实现，
// router 装配经 printing.WithContentReader(printing.ObjectInboundOrder, ...) 注入）。
// 本文件只 import internal/printing 平台契约包（合法方向，plan §2.3 判据 2）。
//
// 数据面（打印内容装配白名单，Orchestrator 裁决 2026-10-03——internal/printing/doc.go）：
//   - 本域表：inbound_orders / inbound_items / purchase_orders（来源单）/
//     receipts + receipt_items（收货事件：操作人姓名、批次号——本域单据表）；
//   - 他域纯展示列 JOIN：suppliers.name、warehouses.name、skus.code、products.name/spec、
//     units.name；
//   - 只读 SELECT 零写通路；不触碰库存族表（batches 不可访问，批次号取收货事件
//     receipts.batch_no 本域列）；不 import 其他业务域包。
//
// 装配契约（printing.ContentReader）：逐对象校验存在，任一缺失整体拒绝；主码内容 =
// 入库单号；qty 取计划量 inbound_items.qty（打印对象是入库单本身，实收进度以
// qty_received 列随单据流转，打印口径为单据计划量）。

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/printing"
)

// NewInboundContentReader 构建入库单内容装配器（router 装配注入 printing）。
func NewInboundContentReader(db *gorm.DB) *InboundContentReader {
	return &InboundContentReader{db: db}
}

// InboundContentReader 入库单装配实现。
type InboundContentReader struct {
	db *gorm.DB
}

// inboundHeadRow 单据头投影行。
type inboundHeadRow struct {
	InboundID     int64  // inbound_orders.id
	InboundNo     string // inbound_orders.inbound_no
	SourceType    string // inbound_orders.source_type
	SupplierName  string // suppliers.name（source_type=PURCHASE 时经 purchase_orders 关联）
	WarehouseName string // warehouses.name
	InboundAt     string // to_char(received_at)
	Operator      string // 最近收货事件操作人（receipts.operator_name）
}

// inboundLineRow 明细投影行。
type inboundLineRow struct {
	InboundID   int64
	LineNo      int64  // inbound_items.line_no
	SKUCode     string // skus.code
	ProductName string // products.name
	Spec        string // products.spec
	UnitName    string // units.name
	BatchNo     string // 最近收货批次（receipts.batch_no）
	Qty         string // CAST(inbound_items.qty AS varchar)
}

// receiptMetaRow 收货事件投影（操作人/批次，按 inbound_no 关联）。
type receiptMetaRow struct {
	InboundNo    string
	LineNo       int64
	OperatorName string
	BatchNo      string
}

// Assemble 实现 printing.ContentReader（plan §12.2 冻结签名）。
func (r *InboundContentReader) Assemble(ctx context.Context, ids []string, fields []string) ([]printing.ContentRow, error) {
	parsed, missing := printing.ParseIDs(ids)
	if len(parsed) == 0 {
		return nil, printing.FailMissing(missing)
	}

	// 单据头（来源 PURCHASE 经 source_no=po_no 关联供应商；OTHER 无供应商留空）。
	// 数据权限（f20）：请求 context 携带仓库范围快照时越仓单据按缺失处理。
	args := []any{parsed}
	whFilter := ""
	if sc, restricted := printing.RestrictedScope(ctx); restricted {
		if printing.EmptyScopeIDs(sc) {
			return nil, printing.FailMissing(ids)
		}
		whFilter, args = printing.InScope(ctx, "io.warehouse_id", args)
	}
	var heads []inboundHeadRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT io.id AS inbound_id, io.inbound_no, io.source_type,
		       COALESCE(sp.name, '') AS supplier_name,
		       COALESCE(w.name, '') AS warehouse_name,
		       COALESCE(to_char(io.received_at, 'YYYY-MM-DD HH24:MI:SS'), '') AS inbound_at,
		       '' AS operator
		FROM inbound_orders io
		LEFT JOIN purchase_orders po ON io.source_type = 'PURCHASE' AND po.po_no = io.source_no
		LEFT JOIN suppliers sp ON sp.id = po.supplier_id
		LEFT JOIN warehouses w ON w.id = io.warehouse_id
		WHERE io.id IN ?`+whFilter, args...).Scan(&heads).Error
	if err != nil {
		return nil, err
	}
	headByID := make(map[int64]inboundHeadRow, len(heads))
	inboundNos := make([]string, 0, len(heads))
	for _, h := range heads {
		headByID[h.InboundID] = h
		inboundNos = append(inboundNos, h.InboundNo)
	}

	// 收货事件（操作人/批次）：后写覆盖 → 每单/每行取最近一次收货。
	meta := map[string]receiptMetaRow{} // key: inbound_no|line_no（line_no=0 为单据级操作人）
	if len(inboundNos) > 0 {
		var metas []receiptMetaRow
		err = r.db.WithContext(ctx).Raw(`
			SELECT rc.inbound_no, ri.line_no, rc.operator_name, rc.batch_no
			FROM receipts rc
			JOIN receipt_items ri ON ri.receipt_id = rc.id
			WHERE rc.inbound_no IN ?
			ORDER BY rc.id, ri.id`, inboundNos).Scan(&metas).Error
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			meta[m.InboundNo+"|"+strconv.FormatInt(m.LineNo, 10)] = m             // 行级批次
			meta[m.InboundNo+"|0"] = receiptMetaRow{OperatorName: m.OperatorName} // 单据级操作人
		}
	}

	// 明细行（本域单据表 + 展示列 JOIN）。
	var lines []inboundLineRow
	err = r.db.WithContext(ctx).Raw(`
		SELECT i.inbound_id, i.line_no,
		       COALESCE(s.code, '') AS sku_code,
		       COALESCE(p.name, '') AS product_name,
		       COALESCE(p.spec, '') AS spec,
		       COALESCE(u.name, '') AS unit_name,
		       CAST(i.qty AS varchar) AS qty
		FROM inbound_items i
		LEFT JOIN skus s ON s.id = i.sku_id
		LEFT JOIN products p ON p.id = s.product_id
		LEFT JOIN units u ON u.id = p.unit_id
		WHERE i.inbound_id IN ?
		ORDER BY i.inbound_id, i.line_no`, parsed).Scan(&lines).Error
	if err != nil {
		return nil, err
	}
	linesByOrder := make(map[int64][]inboundLineRow, len(heads))
	for _, l := range lines {
		linesByOrder[l.InboundID] = append(linesByOrder[l.InboundID], l)
	}

	fieldSet := printing.FieldFilter(fields)
	out := make([]printing.ContentRow, 0, len(parsed))
	for _, id := range parsed {
		head, ok := headByID[id]
		if !ok {
			missing = append(missing, printing.FormatID(id))
			continue
		}
		if m, ok := meta[head.InboundNo+"|0"]; ok {
			head.Operator = m.OperatorName
		}
		values := map[string]string{}
		if fieldSet["order_no"] {
			values["order_no"] = head.InboundNo
		}
		if fieldSet["supplier_name"] {
			values["supplier_name"] = head.SupplierName
		}
		if fieldSet["warehouse_name"] {
			values["warehouse_name"] = head.WarehouseName
		}
		if fieldSet["inbound_at"] {
			values["inbound_at"] = head.InboundAt
		}
		if fieldSet["operator"] {
			values["operator"] = head.Operator
		}
		row := printing.ContentRow{
			ID:     printing.FormatID(id),
			Code:   head.InboundNo,
			Values: values,
			Lines:  []map[string]string{},
		}
		for _, l := range linesByOrder[id] {
			// 明细行按预设键全量投影（行键不受表头绑定约束——lines 即单据明细行快照）。
			line := map[string]string{
				"line_no":      strconv.FormatInt(l.LineNo, 10),
				"sku_code":     l.SKUCode,
				"product_name": l.ProductName,
				"spec":         l.Spec,
				"unit":         l.UnitName,
				"qty":          l.Qty,
			}
			if m, ok := meta[head.InboundNo+"|"+strconv.FormatInt(l.LineNo, 10)]; ok && m.BatchNo != "" {
				line["batch_no"] = m.BatchNo
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
