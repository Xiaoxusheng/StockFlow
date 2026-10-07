package returns

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// Service 退货域业务层（architecture.md §1：业务逻辑/状态机/事务边界/校验）。
// 依赖以窄接口注入（Repository + ports.go 跨域接口），单元测试以接口替身替换。
type Service struct {
	repo           Repository
	stock          StockGateway
	salesOrders    SalesOrderReader
	purchaseOrders PurchaseOrderReader
	qc             QCCreator
	ledgers        LedgerReader
	stockState     StockStateReader
	skuFlagReader  SKUFlagReader
	imageFiles     ImageFileChecker
}

// NewService 构建业务层（装配校验在 RegisterRoutes 启动期 fail-fast）。
func NewService(repo Repository, opts ...Option) *Service {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	return &Service{
		repo:           repo,
		stock:          o.stock,
		salesOrders:    o.salesOrders,
		purchaseOrders: o.purchaseOrders,
		qc:             o.qc,
		ledgers:        o.ledgers,
		stockState:     o.stockState,
		skuFlagReader:  o.skuFlags,
		imageFiles:     o.imageFiles,
	}
}

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因；
// 形态对齐 masterdata.Actor）。
type Actor struct {
	UserID    int64
	Username  string
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string

	// Scope 数据权限仓库范围快照（auth.WarehouseScope 口径；HTTP 入口 actorOf 注入；
	// nil = 未注入，仅限内部路径与测试，按 ID 直取的写动作不校验仓库范围）。
	Scope *stock.WhScope
}

// inventoryActor 转换为库存原语的归因结构（stock.Actor 为值类型）。
func (a Actor) inventoryActor() stock.Actor {
	return stock.Actor{
		ID: a.UserID, Name: a.Username, RequestID: a.RequestID,
		IP: a.IP, UserAgent: a.UserAgent, Method: a.Method, Path: a.Path,
		Scope: a.Scope,
	}
}

// canAccessWarehouse 仓库数据范围判定（f16：按 ID 直取的写动作在加载实体后
// fail-closed 校验，口径与详情接口 fail-closed 一致；任一仓库命中即通过；
// Scope 未注入 = 内部系统路径，不限制）。
func (a Actor) canAccessWarehouse(warehouseIDs ...int64) bool {
	return a.inventoryActor().CanAccessAny(warehouseIDs...)
}

// auditEntry 构造 returns 域审计条目骨架（module=returns；快照由调用方补充；
// plan §4.2 判据 2：关键写操作经 middleware.Audit 与业务同事务写 operation_logs）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "returns",
		ObjectType:   objectType,
		Action:       action,
		ObjectID:     objectID,
		OperatorID:   a.UserID,
		OperatorName: a.Username,
		RequestID:    a.RequestID,
		IP:           a.IP,
		UserAgent:    a.UserAgent,
		Method:       a.Method,
		Path:         a.Path,
	}
}

// ---- 状态机（business-flow §13.2：所有状态变化必须经业务方法守卫；plan §6.9/§6.10）----

// returnTransitions 各退货类型的合法迁移（目标态 → 允许的前置态集合）。
var returnTransitions = map[string]map[string][]string{
	ReturnTypeSales: {
		ReturnStatusPendingApproval: {ReturnStatusDraft},
		ReturnStatusApproved:        {ReturnStatusPendingApproval},
		ReturnStatusReceiving:       {ReturnStatusApproved},
		ReturnStatusInQC:            {ReturnStatusReceiving},
		ReturnStatusCompleted:       {ReturnStatusInQC},
		ReturnStatusCancelled:       {ReturnStatusDraft, ReturnStatusPendingApproval, ReturnStatusApproved}, // 未收货前
		ReturnStatusDraft:           {ReturnStatusPendingApproval},                                          // 审核驳回回草稿（plan §6.1 同族）
	},
	ReturnTypePurchase: {
		ReturnStatusPendingApproval: {ReturnStatusDraft},
		ReturnStatusApproved:        {ReturnStatusPendingApproval},
		ReturnStatusShipped:         {ReturnStatusApproved},
		ReturnStatusCompleted:       {ReturnStatusShipped},
		ReturnStatusCancelled:       {ReturnStatusDraft, ReturnStatusPendingApproval, ReturnStatusApproved}, // 未出库前
		ReturnStatusDraft:           {ReturnStatusPendingApproval},
	},
}

// exceptionTransitions 异常生命周期（business-flow §11.2：发现→创建→分派→处理中→待复核→
// 已解决→已关闭；plan §6.10）。
var exceptionTransitions = map[string][]string{
	ExceptionStatusAssigned:      {ExceptionStatusOpen},
	ExceptionStatusProcessing:    {ExceptionStatusAssigned},
	ExceptionStatusPendingReview: {ExceptionStatusProcessing},
	ExceptionStatusResolved:      {ExceptionStatusPendingReview},
	ExceptionStatusClosed:        {ExceptionStatusResolved},
}

// returnTransitionAllowed 迁移合法性（纯函数，供单测）。
func returnTransitionAllowed(orderType, from, to string) bool {
	allowed, ok := returnTransitions[orderType][to]
	if !ok {
		return false
	}
	for _, f := range allowed {
		if f == from {
			return true
		}
	}
	return false
}

// exceptionTransitionAllowed 异常迁移合法性（纯函数，供单测）。
func exceptionTransitionAllowed(from, to string) bool {
	for _, f := range exceptionTransitions[to] {
		if f == from {
			return true
		}
	}
	return false
}

// guardReturnStatus 状态机守卫迁移：合法迁移执行守卫 UPDATE（WHERE status IN from），
// 0 行=状态冲突（并发/状态已变），回滚并报域内码（plan §6 统一形态）。
func (s *Service) guardReturnStatus(tx *gorm.DB, o *ReturnOrder, to, tsCol string, by int64) error {
	if !returnTransitionAllowed(o.Type, o.Status, to) {
		return response.NewError(ErrStatusConflict, map[string]any{
			"return_id": o.ID.Int64(), "return_no": o.ReturnNo,
			"from": o.Status, "to": to, "reason": "非法状态迁移",
		})
	}
	n, err := s.repo.UpdateReturnStatus(tx, o.ID.Int64(), []string{o.Status}, to, tsCol, by, nil)
	if err != nil {
		return err
	}
	if n == 0 {
		return response.NewError(ErrStatusConflict, map[string]any{
			"return_id": o.ID.Int64(), "return_no": o.ReturnNo,
			"from": o.Status, "to": to, "reason": "状态已被并发变更",
		})
	}
	o.Status = to
	return nil
}

// guardExceptionStatus 异常状态机守卫迁移（同上形态）。
func (s *Service) guardExceptionStatus(tx *gorm.DB, e *Exception, to, tsCol string, by int64) error {
	if !exceptionTransitionAllowed(e.Status, to) {
		return response.NewError(ErrExceptionStatusConflict, map[string]any{
			"exception_id": e.ID.Int64(), "exception_no": e.ExceptionNo,
			"from": e.Status, "to": to, "reason": "非法状态迁移",
		})
	}
	n, err := s.repo.UpdateExceptionStatus(tx, e.ID.Int64(), []string{e.Status}, to, tsCol, by)
	if err != nil {
		return err
	}
	if n == 0 {
		return response.NewError(ErrExceptionStatusConflict, map[string]any{
			"exception_id": e.ID.Int64(), "exception_no": e.ExceptionNo,
			"from": e.Status, "to": to, "reason": "状态已被并发变更",
		})
	}
	e.Status = to
	return nil
}

// ---- 输入校验与公共助手 ----

func paramError(field, reason string) *response.Error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

func responseErrorParam(field, reason string) *response.Error {
	return paramError(field, reason)
}

// validateIdempotencyKey 幂等键校验（最终准绳 inventory_ledgers.idempotency_key，
// service.go validateIdempotencyKey 同口径：≤128）。
func validateIdempotencyKey(key string) error {
	if len(key) > 128 {
		return response.NewError(ErrIdempotencyInvalid, map[string]any{
			"field": "idempotency_key", "reason": "长度不能超过 128", "length": len(key),
		})
	}
	return nil
}

// requireStock 库存原语网关 fail-closed（plan §3.1 规则①）。
func (s *Service) requireStock() (StockGateway, error) {
	if s.stock == nil {
		return nil, response.NewError(ErrGatewayRequired, map[string]any{
			"reason": "router 未注入 WithStock（plan §3.1 规则①）",
		})
	}
	return s.stock, nil
}

// requireSKUFlagReader SKU 开关读取 fail-closed（plan §3.1 规则①；依赖开关分支的
// 业务动作缺依赖即拒绝，杜绝静默按非序列号 SKU 处理）。
func (s *Service) requireSKUFlagReader() (SKUFlagReader, error) {
	if s.skuFlagReader == nil {
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"reason": "router 未注入 WithSKUFlags（plan §3.1 规则①）",
		})
	}
	return s.skuFlagReader, nil
}

// parseQty 数量解析（string 入参 → stock.Qty；非法走 RETURNS_QTY_INVALID 并带字段）。
func parseQty(field, raw string) (stock.Qty, error) {
	q, err := stock.ParseQty(raw)
	if err != nil {
		return 0, response.NewError(ErrQtyInvalid, map[string]any{"field": field, "value": raw, "reason": err.Error()})
	}
	if !q.IsPositive() {
		return 0, response.NewError(ErrQtyInvalid, map[string]any{"field": field, "value": raw, "reason": "必须为正数"})
	}
	return q, nil
}

// parseQtyNonNeg 数量解析（允许 0 与空串——JSON 字段省略语义；质检合格/不良单项
// 可为 0，但两项之和必须为正）。
func parseQtyNonNeg(field, raw string) (stock.Qty, error) {
	if raw == "" {
		return 0, nil
	}
	q, err := stock.ParseQty(raw)
	if err != nil {
		return 0, response.NewError(ErrQtyInvalid, map[string]any{"field": field, "value": raw, "reason": err.Error()})
	}
	if q.IsNegative() {
		return 0, response.NewError(ErrQtyInvalid, map[string]any{"field": field, "value": raw, "reason": "不能为负数"})
	}
	return q, nil
}

// withSnapshots 补齐审计条目（关键更新必带 before/after 快照，architecture.md §8.1）。
func withSnapshots(e middleware.AuditEntry, request, before, after any) middleware.AuditEntry {
	e.Request = request
	e.Before = before
	e.After = after
	e.Success = true
	return e
}

// loadReturnOrError 装载退货单或返回 404。
func (s *Service) loadReturnOrError(ctx context.Context, id int64) (*ReturnOrder, error) {
	o, err := s.repo.FindReturnOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
	}
	return o, nil
}

// requireSalesOrders 销售单读取接口 fail-closed。
func (s *Service) requireSalesOrders() (SalesOrderReader, error) {
	if s.salesOrders == nil {
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"reader": "SalesOrderReader", "reason": "router 未注入 WithSalesOrders（plan §3.1 规则①）",
		})
	}
	return s.salesOrders, nil
}

// requirePurchaseOrders 采购单读取接口 fail-closed。
func (s *Service) requirePurchaseOrders() (PurchaseOrderReader, error) {
	if s.purchaseOrders == nil {
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"reader": "PurchaseOrderReader", "reason": "router 未注入 WithPurchaseOrders（plan §3.1 规则①）",
		})
	}
	return s.purchaseOrders, nil
}

// requireQC 质检单创建接口 fail-closed。
func (s *Service) requireQC() (QCCreator, error) {
	if s.qc == nil {
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"reader": "QCCreator", "reason": "router 未注入 WithQCCreator（plan §3.1 规则①）",
		})
	}
	return s.qc, nil
}

// docRule 取冻结注册表中的编号规则（business-flow §13.1：统一编号引擎，
// 禁止自增 ID 当业务单号；发放与单据创建同事务，回滚作废出现空洞为 §13.1 接受口径）。
func docRule(prefix string) (docnum.Rule, error) {
	rule, ok := docnum.RuleFor(prefix)
	if !ok {
		return docnum.Rule{}, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "未注册的单据前缀（编程错误）", "prefix": prefix,
		})
	}
	return rule, nil
}

// approvalRecord 构造审批记录行（business-flow §12.2：审批人/时间/意见/结果，append-only）。
func approvalRecord(targetType, targetNo, action, result, opinion string, a Actor) *DocumentApproval {
	return &DocumentApproval{
		TargetType: targetType, TargetNo: targetNo,
		Action: action, Result: result, Opinion: opinion,
		OperatorID: a.UserID, OperatorName: a.Username,
		RequestID: a.RequestID, CreatedAt: database.Now(),
	}
}

// lineKey 来源单累计维度。
type lineKey struct {
	LineNo int64
	SKUID  int64
}

// returnedBySourceMap 累计结果转 map。
func returnedBySourceMap(rows []SourceReturnedLine) map[lineKey]stock.Qty {
	m := make(map[lineKey]stock.Qty, len(rows))
	for _, r := range rows {
		m[lineKey{LineNo: r.LineNo, SKUID: r.SKUID}] = m[lineKey{LineNo: r.LineNo, SKUID: r.SKUID}].Add(r.Qty)
	}
	return m
}

// inventoryQtyString 输出 numeric(18,4) 文本（追溯契约统一口径）。
func inventoryQtyString(q stock.Qty) string { return q.String() }

// fmtKey 拼接幂等键（plan §7 键构成："动作:单号:行:五维尾缀" 确定性拼接）。
func fmtKey(parts ...any) string {
	strs := make([]string, 0, len(parts))
	for _, p := range parts {
		strs = append(strs, fmt.Sprint(p))
	}
	return strings.Join(strs, ":")
}
