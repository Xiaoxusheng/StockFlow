package printing

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 打印域业务层（architecture.md §1：业务逻辑/状态机/事务边界/校验）。
// 依赖以窄接口注入（Repository + ports.go ContentReader/asynqx.Queue），
// 单元测试以内存替身替换（ask 约束：单测不依赖 PostgreSQL/Redis/网络）。
type Service struct {
	repo  Repository
	opt   options
	cache *pngCache // 条码/二维码 PNG 进程内缓存（barcode.go；render 预生成与端点共享）
}

// NewService 构建业务层（装配校验在 RegisterRoutes 启动期 fail-fast）。
func NewService(repo Repository, opts ...Option) *Service {
	o := options{readers: map[string]ContentReader{}}
	for _, opt := range opts {
		opt(&o)
	}
	if o.readers == nil {
		o.readers = map[string]ContentReader{}
	}
	return &Service{repo: repo, opt: o, cache: newPNGCache()}
}

// ---- 冻结常量（plan §7.2/§18.4 裁决④；防御边界为 go-dev-standard 规则 5）----

const (
	// MaxDataIDs 单任务打印对象数上限（Orchestrator 裁决④：单任务 data_ids ≤500，
	// 波次级全量单据打印需前端按上限分片——plan §18.4）。
	MaxDataIDs = 500
	// MinCopies / MaxCopies 打印份数边界（下限与 DDL chk_print_tasks_copies 同源；
	// 上限为防御性边界——份数乘以 500 行渲染数据，防失控批量）。
	MinCopies = 1
	MaxCopies = 999
	// MaxDataIDLen 单个对象标识长度上限（内置码值承接场景 data_ids 即码值原文，
	// 对齐 print_task_rows.code varchar(255) 扣除余量）。
	MaxDataIDLen = 200
)

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因；
// 形态对齐 internal/returns.Actor）。
type Actor struct {
	UserID    int64
	Username  string
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// auditEntry 构造 printing 域审计条目骨架（module=printing；plan §13.8 审计清单：
// 模板增改（create/update/copy/status）、打印确认（execute）经 middleware.Audit
// 与业务同事务写 operation_logs）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "printing",
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

// auditWithSnapshots 补齐审计条目（关键更新必带 before/after 快照，architecture.md §8.1；
// 快照不含密码/Token——本域无可脱敏字段）。
func auditWithSnapshots(e middleware.AuditEntry, request, before, after any) middleware.AuditEntry {
	e.Request = request
	e.Before = before
	e.After = after
	e.Success = true
	return e
}

// withTx 事务封装（architecture.md §4：审计与业务同事务，任一步失败整体回滚）。
func withTx(ctx context.Context, repo Repository, fn func(tx *gorm.DB) error) error {
	return repo.DB().WithContext(ctx).Transaction(fn)
}

// docRule 取冻结注册表中的 PT 编号规则（business-flow §13.1 统一编号引擎；
// 禁止自增 ID 当业务单号）。
func docRule() (docnum.Rule, error) {
	rule, ok := docnum.RuleFor(docPrefix)
	if !ok {
		return docnum.Rule{}, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "未注册的单据前缀（编程错误）", "prefix": docPrefix,
		})
	}
	return rule, nil
}

// paramError 参数错误（api.md §4：校验失败必须带 details）。
func paramError(field, reason string) *response.Error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

// ---- 视图（snake_case JSON tag；ID 字符串化——backend-m1-plan §1 全局约定。
// 前端先行 printing.ts 为 camelCase，按 plan §12.4 由前端对齐轮回对）----

// FieldView 模板绑定字段读视图（{key,label}，预设键序）。
type FieldView = FieldPreset

// ---- 批量结果契约（效率层一期计划 §2.7；api.md §9 冻结形态：
// {total, success_count, failed_count, skipped_count, results:[{id,status,reason}]}）----

// 批量结果逐条状态三态（skipped=幂等命中/已处目标态——plan §2.7 语义冻结）。
const (
	BatchStatusSuccess = "success"
	BatchStatusFailed  = "failed"
	BatchStatusSkipped = "skipped"
)

// ReasonDuplicateDataID 请求内重复 data_id 的 skipped 原因（同批重复对象已在任务中
// = 已处目标态；reason 用既有错误码形态的字符串）。
const ReasonDuplicateDataID = "DUPLICATE_DATA_ID"

// TaskBatchItem 批量结果逐条目（id=打印对象业务标识 data_id——重试语义=以失败
// data_ids 重建任务，成功项绝不重跑）。
type TaskBatchItem struct {
	ID     string `json:"id"`
	Status string `json:"status"` // success | failed | skipped
	Reason string `json:"reason,omitempty"`
}

// TaskBatchResult 打印任务批量结果（POST /api/prints/tasks 响应演进形态；
// 200 + 逐条结果，409+details.disabled_ids 整体拒绝语义废止）。
type TaskBatchResult struct {
	Total        int             `json:"total"`
	SuccessCount int             `json:"success_count"`
	FailedCount  int             `json:"failed_count"`
	SkippedCount int             `json:"skipped_count"`
	Results      []TaskBatchItem `json:"results"`
}

// TemplateView 模板视图。
type TemplateView struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	ObjectType       string      `json:"object_type"`
	Paper            string      `json:"paper"`
	BarcodeSymbology string      `json:"barcode_symbology,omitempty"`
	QRCodeEnabled    bool        `json:"qrcode_enabled"`
	Fields           []FieldView `json:"fields"`
	HeaderText       string      `json:"header_text"`
	Status           string      `json:"status"`
	Remark           string      `json:"remark"`
	CreatedAt        string      `json:"created_at"`
	UpdatedAt        string      `json:"updated_at"`
	CreatedBy        string      `json:"created_by"`
}

// templateView 模型 → 视图。
func templateView(t *PrintTemplate) TemplateView {
	v := TemplateView{
		ID:            strconv.FormatInt(t.ID.Int64(), 10),
		Name:          t.Name,
		ObjectType:    t.ObjectType,
		Paper:         t.Paper,
		QRCodeEnabled: t.QRCodeEnabled,
		Fields:        orderedFieldList(t.ObjectType, t.FieldMap()),
		HeaderText:    t.HeaderText,
		Status:        t.Status,
		Remark:        t.Remark,
		CreatedAt:     t.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:     t.UpdatedAt.Format("2006-01-02 15:04:05"),
		CreatedBy:     strconv.FormatInt(t.CreatedBy, 10),
	}
	if t.BarcodeSymbology != nil {
		v.BarcodeSymbology = *t.BarcodeSymbology
	}
	return v
}

// RowView 渲染数据行（print_task_rows 读视图；id=主码内容——行身份在渲染数据包内
// 以主码承载（000012 冻结 DDL）；data_id=行业务身份快照（000019，重打取数依据，
// qr-code.md §7.4；旧行 NULL → 空串，凡空即不可自动重打——前端 fail-closed）。
type RowView struct {
	Seq    int                 `json:"seq"`
	ID     string              `json:"id"`
	Code   string              `json:"code"`
	DataID string              `json:"data_id,omitempty"`
	Values map[string]string   `json:"values,omitempty"`
	Lines  []map[string]string `json:"lines,omitempty"`
}

// rowView 行模型 → 视图。
func rowView(r *PrintTaskRow) RowView {
	return RowView{
		Seq:    r.Seq,
		ID:     r.Code,
		Code:   r.Code,
		DataID: r.DataID,
		Values: r.RowValues(),
		Lines:  r.RowLines(),
	}
}

// TaskView 任务视图（列表/详情共用；Template/Rows 仅详情携带——前端先行契约
// 列表 omitempty 口径，plan §7.2）。
type TaskView struct {
	ID           string `json:"id"`
	PrintNo      string `json:"print_no"`
	ObjectType   string `json:"object_type"`
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name,omitempty"` // 快照派生（任务创建时冻结）
	Paper        string `json:"paper"`
	Copies       int    `json:"copies"`
	TotalCount   int    `json:"total_count"`
	Status       string `json:"status"`
	Result       string `json:"result,omitempty"`
	PrintedBy    string `json:"printed_by,omitempty"`
	PrintedAt    string `json:"printed_at,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    string `json:"created_at"`

	Template *TemplateSnapshot `json:"template,omitempty"`
	Rows     []RowView         `json:"rows,omitempty"`
}

// taskView 任务模型 → 视图（detail=true 时携带快照与内容行）。
func (s *Service) taskView(t *PrintTask, detail bool) TaskView {
	snap := t.Snapshot()
	v := TaskView{
		ID:           strconv.FormatInt(t.ID.Int64(), 10),
		PrintNo:      t.PrintNo,
		ObjectType:   t.ObjectType,
		TemplateID:   strconv.FormatInt(t.TemplateID, 10),
		TemplateName: snap.Name,
		Paper:        t.Paper,
		Copies:       t.Copies,
		TotalCount:   t.TotalCount,
		Status:       t.Status,
		PrintedBy:    "", // 执行确认后回填（见下）
		ErrorMessage: t.ErrorMessage,
		CreatedBy:    strconv.FormatInt(t.CreatedBy, 10),
		CreatedAt:    t.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if t.Result != nil {
		v.Result = *t.Result
	}
	if t.PrintedBy != 0 {
		v.PrintedBy = strconv.FormatInt(t.PrintedBy, 10)
	}
	if !t.PrintedAt.IsZero() {
		v.PrintedAt = t.PrintedAt.Format("2006-01-02 15:04:05")
	}
	if detail {
		v.Template = &snap
		v.Rows = []RowView{}
	}
	return v
}

// HistoryItem 打印历史条目（printing.md §1.2 五字段：打印人/打印时间/模板/数据量/
// 打印结果；= print_tasks 已确认子集，plan §7.2）。
type HistoryItem struct {
	ID           string `json:"id"`
	PrintNo      string `json:"print_no"`
	ObjectType   string `json:"object_type"`
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name,omitempty"`
	PrintedBy    string `json:"printed_by"`
	PrintedAt    string `json:"printed_at"`
	TotalCount   int    `json:"total_count"`
	Result       string `json:"result"`
	ErrorMessage string `json:"error_message,omitempty"`
}
