package batchresult

// 批量结果统一类型与构造辅助（计划 §2.7 冻结契约；形状与语义见 doc.go）。

// Status 批量结果逐条状态（§2.7 冻结三态字面量）。
type Status string

const (
	// StatusSuccess 单条执行成功。
	StatusSuccess Status = "success"
	// StatusFailed 单条失败（reason 携既有错误码字符串）。
	StatusFailed Status = "failed"
	// StatusSkipped 幂等命中 / 已处目标态（reason 恒空）。
	StatusSkipped Status = "skipped"
)

// Item 批量结果逐条项（api.md §2 业务 ID 字符串形态）。
type Item struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	// Reason 仅 failed 携带（既有错误码字符串）；omitempty 保证 success/skipped
	// 不出现该字段（计划 §2.7：skipped/failed 之外恒空）。
	Reason string `json:"reason,omitempty"`
}

// Result 批量结果（§2.7 冻结形状；计数由 Result()/SummaryOf 依 items 收敛，
// 不信任手工计数——计划 §7.1 T8「计数与 results 逐条对账」）。
type Result struct {
	Total        int    `json:"total"`
	SuccessCount int    `json:"success_count"`
	FailedCount  int    `json:"failed_count"`
	SkippedCount int    `json:"skipped_count"`
	Results      []Item `json:"results"`
}

// Builder 批量结果构造器（非并发安全：批量端点在单请求单 goroutine 内逐条追加；
// 跨 goroutine 聚合由调用方先各自收集再 SummaryOf 收敛）。
type Builder struct {
	items []Item
}

// NewBuilder 构造构造器（capacity 为预期条数，0 表示不预分配）。
func NewBuilder(capacity int) *Builder {
	if capacity < 0 {
		capacity = 0
	}
	return &Builder{items: make([]Item, 0, capacity)}
}

// Success 记一条成功。
func (b *Builder) Success(id string) *Builder {
	b.items = append(b.items, Item{ID: id, Status: StatusSuccess})
	return b
}

// Failed 记一条失败（reason = 既有模块错误码字符串）。
func (b *Builder) Failed(id, reason string) *Builder {
	b.items = append(b.items, Item{ID: id, Status: StatusFailed, Reason: reason})
	return b
}

// Skipped 记一条跳过（幂等命中/已处目标态；reason 恒空——语义冻结，不收外部值）。
func (b *Builder) Skipped(id string) *Builder {
	b.items = append(b.items, Item{ID: id, Status: StatusSkipped})
	return b
}

// Result 计数收敛并返回结果（items 顺序即 results 顺序；空批量 results=[] 非 null）。
func (b *Builder) Result() Result {
	out := Result{Total: len(b.items)}
	if out.Results = b.items; out.Results == nil {
		out.Results = []Item{}
	}
	for _, it := range out.Results {
		switch it.Status {
		case StatusSuccess:
			out.SuccessCount++
		case StatusFailed:
			out.FailedCount++
		case StatusSkipped:
			out.SkippedCount++
		}
	}
	return out
}

// SummaryOf 既有逐条项切片 → Result（计数收敛同 Result()；域包自有构造路径接入用）。
func SummaryOf(items []Item) Result {
	b := &Builder{items: items}
	return b.Result()
}

// ---- 既有批量端点接入方式（计划 §2.7 交付形态 → 本包收敛路径） ----
//
// 集成收口进度（2026-10-06）：
//
//  ✔ internal/printing/service_task.go CreateTask 已收敛至本包（TaskBatchResult/
//    TaskBatchItem 私有定义删除；id 原为字符串、reason omitempty 同形，JSON 契约
//    零变化；skipped 携 DUPLICATE_DATA_ID reason 经 Item 直构——Builder.Skipped
//    语义冻结 reason 恒空，不可用于该形态）。
//
// 以下两处仍持有同形状私有类型（ID 为 JSON 数字——api.md §2 冻结口径为业务 ID
// 字符串形态，收敛即改契约，须先改 api.md 再同步前端消费点，见步骤 3）：
//
//  1. internal/sales/service_batch.go   BatchResult/BatchResultItem（ID int64，
//     batchStatus* 私有常量 + normalizeBatchResult 私有收敛）——该域两条
//     batch-claim 使用中；
//  2. internal/purchase/service_batch.go 同构私有定义——putaway batch-claim 使用中。
//
// 收敛步骤（由拥有对应文件的实现者/集成执行，一次一个域、conventional commit）：
//  1. 域包 import 本包；handler 返回类型改 batchresult.Result；
//  2. 逐条构造改 Builder（Failed 的 reason 沿用既有错误码字符串，行为不变）；
//  3. ID 形态差异披露：sales/purchase 现为 JSON 数字、本包为字符串——api.md §2
//     冻结口径为业务 ID 字符串形态；收敛 sales/purchase 时需同步前端消费点
//     （web/src/api/task.ts 等，重试流以 results[].id 回填请求 ids，数字→字符串
//     形态变化会使 gin 整数绑定 400）并按 docs 先行规则先改 api.md；
//  4. 域包私有类型删除前 grep 引用清零（go-dev-standard 规则 2）。
