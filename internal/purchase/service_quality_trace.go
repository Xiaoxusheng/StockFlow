package purchase

// 质量追溯与不合格品列表（business-flow §4：质量记录链"检验 → 处置"两级；
// web/src/api/quality.ts QualityTraceItem/NonconformingItem 前端先行契约的立项落地——
// 2026-10-04 补端点 GET /api/quality/trace、GET /api/quality/nonconforming，销项
// "前端已消费呈永久错误态"缺口）。
//
// 数据面（对齐 printing_content.go 打印内容装配白名单的裁决口径，只读、零写通路）：
//   - 本域表：quality_orders / quality_items（检验与处置结果承载）；
//   - 他域纯展示列 JOIN：skus.code、products.name（记录的 SKU 识别列）；
//   - 不触碰库存族表；不 import 其他业务域包；
//   - 软删排除：000016 起两表带 deleted_at（模型挂 BaseModel），显式 IS NULL。
//
// 已知边界（诚实披露，requirements.md §10 禁假数据）：
//   - serial_no：质检链无序列号台账列，trace 的 serial_no 检索显式拒绝（400）；
//   - destination：quality_orders 无"处置去向"列，nonconforming 的 destination 检索
//     显式拒绝、记录字段不下发（前端为可选字段，呈空）；
//   - record_type 恒 "inspection"：质检单 result 列同时承载检验（合格/部分合格/不合格）
//     与处置（六处置值）两段记录，处置段以 disposition 字段呈现（§4.3 九值 CHECK）。

import (
	"context"
	"strings"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// QualityTraceFilter 质量追溯筛选（SKU 编码/批次号/单据号三维；serial_no 拒绝）。
type QualityTraceFilter struct {
	SKUCode  string
	BatchNo  string
	BizNo    string
	SerialNo string
	Scope    WarehouseScope
	Page     int
	PageSize int
}

// QualityTraceItem 质量追溯记录（quality_items 行粒度：每质检单每 SKU 行一条记录；
// 字段形态对齐 web/src/api/quality.ts QualityTraceItem 前端先行契约，snake_case）。
type QualityTraceItem struct {
	ID               database.ID       `json:"id"`
	OccurredAt       database.JSONTime `json:"occurred_at"` // 检验时间（未检验回退行创建时间）
	RecordType       string            `json:"record_type"`
	QCNo             string            `json:"qc_no,omitempty"`
	BizType          string            `json:"biz_type,omitempty"` // 来源业务 INBOUND/RETURN
	BizNo            string            `json:"biz_no,omitempty"`   // 来源单号（入库单号/退货单号）
	SKUCode          string            `json:"sku_code"`
	ProductName      string            `json:"product_name"`
	BatchNo          string            `json:"batch_no,omitempty"`
	InspectionMethod string            `json:"inspection_method,omitempty"` // 免检/抽检/全检
	Result           string            `json:"result,omitempty"`            // 九类中文值域（chk_quality_orders_result）
	Disposition      string            `json:"disposition,omitempty"`       // 处置六值英文键（前端 QualityDisposition 值域）
	InspectorName    string            `json:"inspector_name,omitempty"`
	Remark           string            `json:"remark,omitempty"`
}

// NonconformingFilter 不合格品筛选。
type NonconformingFilter struct {
	Keyword     string // QC 单号 / SKU 编码模糊
	Disposition string // 处置六值英文键（前端 QualityDisposition）
	Destination string // 未建模，显式拒绝（见文件头"已知边界"）
	Scope       WarehouseScope
	Page        int
	PageSize    int
}

// NonconformingItem 不合格品记录（quality_items 行粒度，qty = 行不良数量）。
type NonconformingItem struct {
	ID          database.ID       `json:"id"`
	QCNo        string            `json:"qc_no"`
	SourceNo    string            `json:"source_no,omitempty"`
	SKUCode     string            `json:"sku_code"`
	ProductName string            `json:"product_name"`
	BatchNo     string            `json:"batch_no,omitempty"`
	Qty         stock.Qty         `json:"qty"` // 不合格数量（行 qty_defective）
	Disposition string            `json:"disposition,omitempty"`
	HandlerName string            `json:"handler_name,omitempty"` // 质检员（处置执行人）
	HandledAt   database.JSONTime `json:"handled_at,omitempty"`   // 检验（处置）时间
	CreatedAt   database.JSONTime `json:"created_at"`
}

// dispositionByResult 处置中文值 → 英文键（web/src/api/quality.ts QualityDisposition
// 六值；检验三值不入表——无处置段）。
var dispositionByResult = map[string]string{
	"退供应商":  "return_supplier",
	"报废":    "scrap",
	"返工":    "rework",
	"降级":    "downgrade",
	"转不良品仓": "to_defective_warehouse",
	"特批放行":  "special_release",
}

// resultByDisposition 反查表（筛选参数英文键 → 中文值）。
var resultByDisposition = func() map[string]string {
	m := make(map[string]string, len(dispositionByResult))
	for k, v := range dispositionByResult {
		m[v] = k
	}
	return m
}()

// QualityTrace 质量追溯分页查询。
func (s *Service) QualityTrace(ctx context.Context, f QualityTraceFilter) ([]*QualityTraceItem, int64, error) {
	if strings.TrimSpace(f.SerialNo) != "" {
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "serial_no", "reason": "质检链未记录序列号，暂不支持该检索维度",
		})
	}
	f.Page, f.PageSize = normalizePage(f.Page, f.PageSize)
	where, args := qualityTraceWhere(f)
	from := ` FROM quality_items qi
		JOIN quality_orders q ON q.id = qi.qc_id
		LEFT JOIN skus s ON s.id = qi.sku_id
		LEFT JOIN products p ON p.id = s.product_id`
	var total int64
	if err := s.qualityDB.WithContext(ctx).Raw(`SELECT COUNT(*)`+from+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*QualityTraceItem
	listArgs := append(append([]any{}, args...), f.PageSize, (f.Page-1)*f.PageSize)
	err := s.qualityDB.WithContext(ctx).Raw(`SELECT qi.id,
			COALESCE(q.inspected_at, qi.created_at) AS occurred_at,
			q.qc_no, q.source_type, q.source_no, q.inspection_type,
			TRIM(q.result) AS result, COALESCE(q.inspector_name, '') AS inspector_name,
			TRIM(qi.batch_no) AS batch_no, TRIM(qi.remark) AS remark, qi.created_at,
			COALESCE(s.code, '') AS sku_code, COALESCE(p.name, '') AS product_name
		`+from+where+` ORDER BY qi.id DESC LIMIT ? OFFSET ?`, listArgs...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	for _, it := range rows {
		it.RecordType = "inspection"
		it.Disposition = dispositionByResult[it.Result]
	}
	return rows, total, nil
}

// Nonconforming 不合格品分页查询（已完成质检且行不良数量 > 0；处置六值入 disposition，
// 检验三值（不合格等）disposition 留空——待处置记录）。
func (s *Service) Nonconforming(ctx context.Context, f NonconformingFilter) ([]*NonconformingItem, int64, error) {
	f.Keyword = strings.TrimSpace(f.Keyword)
	if f.Destination != "" {
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "destination", "reason": "质检单未记录处置去向，暂不支持该筛选",
		})
	}
	dispositionCN := ""
	if f.Disposition != "" {
		var ok bool
		dispositionCN, ok = resultByDisposition[f.Disposition]
		if !ok {
			keys := make([]string, 0, len(resultByDisposition))
			for k := range resultByDisposition {
				keys = append(keys, k)
			}
			return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "disposition", "reason": "必须为处置六值之一: " + strings.Join(keys, "/"),
			})
		}
	}
	f.Page, f.PageSize = normalizePage(f.Page, f.PageSize)

	where := ` WHERE q.deleted_at IS NULL AND qi.deleted_at IS NULL
		AND q.status = 'COMPLETED' AND qi.qty_defective > 0`
	args := []any{}
	if !f.Scope.All {
		if len(f.Scope.IDs) == 0 {
			where += ` AND 1 = 0` // 数据权限 fail-closed：空集不可见任何行
		} else {
			where += ` AND q.warehouse_id IN ?`
			args = append(args, f.Scope.IDs)
		}
	}
	if dispositionCN != "" {
		where += ` AND q.result = ?`
		args = append(args, dispositionCN)
	}
	if f.Keyword != "" {
		where += ` AND (q.qc_no ILIKE ? OR s.code ILIKE ?)`
		kw := "%" + f.Keyword + "%"
		args = append(args, kw, kw)
	}
	from := ` FROM quality_items qi
		JOIN quality_orders q ON q.id = qi.qc_id
		LEFT JOIN skus s ON s.id = qi.sku_id`
	var total int64
	if err := s.qualityDB.WithContext(ctx).Raw(`SELECT COUNT(*)`+from+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*NonconformingItem
	listArgs := append(append([]any{}, args...), f.PageSize, (f.Page-1)*f.PageSize)
	err := s.qualityDB.WithContext(ctx).Raw(`SELECT qi.id, qi.qty_defective, qi.created_at,
			TRIM(qi.batch_no) AS batch_no,
			q.qc_no, q.source_no, q.inspected_at,
			COALESCE(q.inspector_name, '') AS handler_name,
			TRIM(q.result) AS result,
			COALESCE(s.code, '') AS sku_code, COALESCE(p.name, '') AS product_name
		`+from+` LEFT JOIN products p ON p.id = s.product_id`+where+
		` ORDER BY qi.id DESC LIMIT ? OFFSET ?`, listArgs...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	for _, it := range rows {
		it.Disposition = dispositionByResult[strings.TrimSpace(it.Disposition)]
	}
	return rows, total, nil
}

// qualityTraceWhere 追溯筛选条件（软删排除 + 数据权限 fail-closed + 等值检索）。
func qualityTraceWhere(f QualityTraceFilter) (string, []any) {
	where := ` WHERE q.deleted_at IS NULL AND qi.deleted_at IS NULL`
	args := []any{}
	if !f.Scope.All {
		if len(f.Scope.IDs) == 0 {
			where += ` AND 1 = 0` // 数据权限 fail-closed：空集不可见任何行
		} else {
			where += ` AND q.warehouse_id IN ?`
			args = append(args, f.Scope.IDs)
		}
	}
	if c := strings.TrimSpace(f.SKUCode); c != "" {
		where += ` AND s.code = ?`
		args = append(args, c)
	}
	if b := strings.TrimSpace(f.BatchNo); b != "" {
		where += ` AND qi.batch_no = ?`
		args = append(args, b)
	}
	if n := strings.TrimSpace(f.BizNo); n != "" {
		where += ` AND q.source_no = ?`
		args = append(args, n)
	}
	return where, args
}
