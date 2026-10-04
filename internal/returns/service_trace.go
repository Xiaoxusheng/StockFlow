package returns

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// 库存追溯（inventory-rules §10：任何 SKU 可以查看完整追溯链——
// 库存当前状态 → 当前库位 → 批次 → 入库来源（采购订单）→ 调拨记录 → 出库记录 → 退货记录；
// 序列号支持完整生命周期追溯 §8）。
//
// 实现（plan §5 000010 注：追溯是查询编排接口，不建任何追溯中间表）：
//   - 主轴 = 库存流水（LedgerReader 窄接口，inventory 域只读实现，plan §2.3 判据 7：
//     跨域读走消费接口 + router 注入，无旁路 SELECT）——inventory-rules §5.1
//     "任何库存调整都能够通过流水追溯来源"，change_type/business_type/business_no
//     即承载入库/调拨/出库/退货全链；
//   - 当前状态 = StockStateReader 窄接口（inventory 只读实现）：按 SKU 的实时库存行
//     （当前库位/批次/六状态）与序列号台账（last_source_* 追溯指针）；
//   - 业务单据富化 = §3.1 冻结的 SalesOrderReader/PurchaseOrderReader（来源单明细证据）；
//   - 操作日志 = operation_logs（平台审计表，只读；经流水的 request_id 关联——
//     流水与操作日志同事务写入，request_id 即同一次请求的审计链）。

// TraceScope 数据权限范围（handler 自 auth.WarehouseScope 提取；禁止接受前端仓库范围
// 参数决定可见性——permission.md §4）。
type TraceScope struct {
	AllWarehouses bool
	WarehouseIDs  []int64
}

// TraceQuery 追溯查询（sku_id 与 serial_no 至少其一）。
type TraceQuery struct {
	SKUID       int64
	SerialNo    string
	WarehouseID int64 // 0=全部仓库（仅 ALL 数据权限可用）
	Limit       int   // 追溯链上限（1-500，默认 100）
}

// TraceDocument 追溯富化的来源单据（business_no 命中 sales/purchase 单时经窄接口取明细）。
type TraceDocument struct {
	Type        string         `json:"type"` // sales_order / purchase_order
	No          string         `json:"no"`
	Found       bool           `json:"found"`
	WarehouseID int64          `json:"warehouse_id"`
	Lines       []TraceDocLine `json:"lines,omitempty"`
}

// TraceDocLine 来源单行证据。
type TraceDocLine struct {
	LineNo int64  `json:"line_no"`
	SKUID  int64  `json:"sku_id"`
	Qty    string `json:"qty"`
}

// TraceOperation 追溯关联的操作日志（operation_logs 投影，只读）。
type TraceOperation struct {
	ID           database.ID       `json:"id"`
	RequestID    string            `json:"request_id"`
	Module       string            `json:"module"`
	ObjectType   string            `json:"object_type"`
	ObjectID     int64             `json:"object_id"`
	Action       string            `json:"action"`
	OperatorID   database.ID       `json:"operator_id"`
	OperatorName string            `json:"operator_name"`
	Success      bool              `json:"success"`
	CreatedAt    database.JSONTime `json:"created_at"`
}

// TraceResult 追溯结果（场景 7 交付形态）。
type TraceResult struct {
	SKUID          int64            `json:"sku_id"`
	SerialNo       string           `json:"serial_no,omitempty"`
	WarehouseID    int64            `json:"warehouse_id"` // 0=全部仓库
	StockRows      []TraceStockRow  `json:"stock_rows"`
	Serial         *TraceSerialRow  `json:"serial,omitempty"`
	Chain          []TraceLedger    `json:"chain"`
	ChainTruncated bool             `json:"chain_truncated"`
	Documents      []TraceDocument  `json:"documents"`
	Operations     []TraceOperation `json:"operations"`
}

const (
	traceDefaultLimit = 100
	traceMaxLimit     = 500
	traceMaxOps       = 200 // 操作日志关联上限
	traceMaxDocLines  = 50  // 单据富化行上限（防止超长单据放大响应）
)

// Trace 追溯编排。
func (s *Service) Trace(ctx context.Context, q TraceQuery, scope TraceScope) (*TraceResult, error) {
	if s.ledgers == nil || s.stockState == nil {
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"reader": "LedgerReader/StockStateReader", "reason": "router 未注入追溯读接口（plan §3.1 规则①）",
		})
	}
	if q.SKUID <= 0 && q.SerialNo == "" {
		return nil, response.NewError(ErrTraceParamRequired, nil)
	}
	if q.Limit <= 0 {
		q.Limit = traceDefaultLimit
	}
	if q.Limit > traceMaxLimit {
		q.Limit = traceMaxLimit
	}
	// 数据权限（permission.md §4）：非全量范围必须（显式或唯一绑定）指定范围内仓库；
	// 越界仓库按 403 拒绝（fail-closed，inventory 域 GetInventoryDetail 同口径）。
	if !scope.AllWarehouses {
		if len(scope.WarehouseIDs) == 0 {
			return nil, response.NewError(response.CodePermissionDenied, map[string]any{
				"reason": "当前数据权限范围无可见仓库",
			})
		}
		if q.WarehouseID > 0 && !containsID(scope.WarehouseIDs, q.WarehouseID) {
			return nil, response.NewError(response.CodePermissionDenied, map[string]any{
				"reason": "仓库不在当前数据权限范围内", "warehouse_id": q.WarehouseID,
			})
		}
		if q.WarehouseID == 0 {
			if len(scope.WarehouseIDs) == 1 {
				q.WarehouseID = scope.WarehouseIDs[0] // 唯一绑定仓库自动收窄
			} else {
				return nil, response.NewError(response.CodePermissionDenied, map[string]any{
					"reason": "请指定仓库（当前数据权限为多仓范围）",
				})
			}
		}
	}

	// 序列号锚定：按序列号追溯时以序列号台账定位 SKU（inventory-rules §8）。
	res := &TraceResult{WarehouseID: q.WarehouseID}
	if q.SerialNo != "" {
		serial, found, err := s.stockState.SerialByNo(ctx, q.SerialNo)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, response.NewError(ErrSerialNotFound, map[string]any{"serial_no": q.SerialNo})
		}
		serial.LastEventAtStr = formatTime(serial.LastEventAt)
		serial.LastEventAt = time.Time{}
		res.Serial = &serial
		res.SerialNo = q.SerialNo
		if q.SKUID <= 0 {
			q.SKUID = serial.SKUID
		}
	}
	res.SKUID = q.SKUID

	// 当前状态 → 当前库位 → 批次（inventory-rules §10 链起点）。
	rows, err := s.stockState.StockRowsBySKU(ctx, q.SKUID, q.WarehouseID, traceMaxLimit)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].UpdatedAtStr = formatTime(rows[i].UpdatedAt)
		rows[i].UpdatedAt = time.Time{}
	}
	res.StockRows = rows

	// 追溯链主轴：最新 limit 条流水，时间正序呈现（append-only，inventory-rules §5.1）。
	leds, err := s.ledgers.LedgersForTrace(ctx, q.SKUID, q.SerialNo, q.WarehouseID, q.Limit)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(leds, func(i, j int) bool { return leds[i].CreatedAt.Before(leds[j].CreatedAt) })
	for i := range leds {
		leds[i].CreatedAtStr = formatTime(leds[i].CreatedAt)
		leds[i].CreatedAt = time.Time{}
	}
	res.Chain = leds
	res.ChainTruncated = len(leds) >= q.Limit

	// 单据富化（plan §3.1 冻结 Reader；found=false 为正常情形——business_no 可能属于
	// 其他单据类型或他域单据，不视为错误）。
	res.Documents = s.enrichDocuments(ctx, leds)

	// 操作日志关联：流水与 operation_logs 同事务写入（architecture §4），
	// request_id 即同一次请求的审计链锚点。
	ops, err := s.correlateOperations(ctx, leds)
	if err != nil {
		return nil, err
	}
	res.Operations = ops
	return res, nil
}

// enrichDocuments 从追溯链提取来源单号并经窄接口富化。
func (s *Service) enrichDocuments(ctx context.Context, leds []TraceLedger) []TraceDocument {
	docs := []TraceDocument{}
	seen := map[string]bool{}
	appendDoc := func(t, no string) {
		key := t + ":" + no
		if no == "" || seen[key] {
			return
		}
		seen[key] = true
		d := TraceDocument{Type: t, No: no}
		switch t {
		case "sales_order":
			if s.salesOrders != nil {
				_, wh, lines, found, err := s.salesOrders.FindReturnable(ctx, no)
				if err == nil && found {
					d.Found = true
					d.WarehouseID = wh
					for _, l := range lines {
						if len(d.Lines) >= traceMaxDocLines {
							break
						}
						d.Lines = append(d.Lines, TraceDocLine{LineNo: l.LineNo, SKUID: l.SKUID, Qty: strconv.FormatInt(l.Qty, 10)})
					}
				}
			}
		case "purchase_order":
			if s.purchaseOrders != nil {
				_, wh, lines, found, err := s.purchaseOrders.FindReturnable(ctx, no)
				if err == nil && found {
					d.Found = true
					d.WarehouseID = wh
					for _, l := range lines {
						if len(d.Lines) >= traceMaxDocLines {
							break
						}
						d.Lines = append(d.Lines, TraceDocLine{LineNo: l.LineNo, SKUID: l.SKUID, Qty: strconv.FormatInt(l.Qty, 10)})
					}
				}
			}
		default:
			return // 其他业务类型（inbound/outbound/transfer…）M2 无冻结 Reader，仅以链上 business_no 呈现
		}
		docs = append(docs, d)
	}
	for _, l := range leds {
		appendDoc(l.BusinessType, l.BusinessNo)
	}
	return docs
}

// correlateOperations 以流水 request_id 关联操作日志（cap 防放大）；查询失败上抛
// （不静默降级——trace 响应缺操作日志区块即视为失败，fail loudly）。
func (s *Service) correlateOperations(ctx context.Context, leds []TraceLedger) ([]TraceOperation, error) {
	seen := map[string]bool{}
	requestIDs := make([]string, 0, len(leds))
	for _, l := range leds {
		if l.RequestID == "" || seen[l.RequestID] {
			continue
		}
		seen[l.RequestID] = true
		if len(requestIDs) >= traceMaxOps {
			break
		}
		requestIDs = append(requestIDs, l.RequestID)
	}
	logs, err := s.repo.ListOperationLogs(ctx, requestIDs, traceMaxOps)
	if err != nil {
		return nil, err
	}
	ops := make([]TraceOperation, 0, len(logs))
	for _, l := range logs {
		ops = append(ops, TraceOperation{
			ID: l.ID, RequestID: l.RequestID, Module: l.Module,
			ObjectType: l.ObjectType, ObjectID: l.ObjectID, Action: l.Action,
			OperatorID: l.UserID, OperatorName: l.Username,
			Success: l.Success, CreatedAt: l.CreatedAt,
		})
	}
	return ops, nil
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// formatTime 统一时间文本（api.md §2：YYYY-MM-DD HH:mm:ss；零值→空串）。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
