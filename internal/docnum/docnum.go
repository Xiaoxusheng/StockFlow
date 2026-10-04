// Package docnum 统一单据编号引擎（business-flow §13.1、backend-m2-plan §4.1）。
//
// 格式：{前缀}-{YYYYMMDD}-{流水}，如 PO-20261002-000001（§13.1 示例口径）。
// 流水按日从 000001 起（Reset=DAY，period=YYYYMMDD，跨日自然重置）或永不重置
// （Reset=ALL，period='ALL'——M1 存量 LED/ADJ 承接口径，plan §4.1）。
// 禁止简单使用数据库自增 ID 当业务单号（business-flow §13.1）。
//
// 发放机制（plan §4.1 冻结）：计数表 doc_number_counters(prefix, period, next_no)，
// 事务内 INSERT ... ON CONFLICT DO NOTHING + UPDATE ... RETURNING next_no——行锁原子，
// 与 nextval 同为单语句原子操作。流水发放必须与单据创建同事务，事务回滚则号码作废
// 出现空洞——§13.1 不要求连续，接受。
//
// 为何不用裸 PG sequence：§13.1 流水按日重置，PG sequence 无法按日重置（需 cron，
// 属阶段 19）；此为对 ask 字面"PG sequence"的有意偏离（plan §4.1 决策、§15.1 开放问题），
// 若拍板改用 PG sequence 仅需替换本包发放实现，Next 签名不变。
//
// 本包为平台包：只依赖标准库 + gorm + internal/response，禁止反向依赖任何业务域包。
package docnum

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// Reset 流水重置周期（business-flow §13.1 流水段口径）。
type Reset string

const (
	// ResetDay 按日重置：period=YYYYMMDD，每日从 1 重新起号（§13.1 示例口径）。
	ResetDay Reset = "DAY"
	// ResetAll 永不重置：period='ALL'，全周期连续（M1 存量 LED/ADJ 承接口径，plan §4.1）。
	ResetAll Reset = "ALL"
)

// DefaultSeqWidth 默认流水位宽（business-flow §13.1 示例为 6 位流水）。
const DefaultSeqWidth = 6

// maxPrefixLen 前缀最大长度（M2 冻结注册表最长前缀为 OUT/CK 等 2–3 字符，预留至 8）。
const maxPrefixLen = 8

// Rule 单据编号规则（plan §4.1：前缀与流水位宽为代码内冻结规则注册表值）。
// "仓库编码段/日期段/前缀可配置"预留为结构体字段，M2 不开放运行时配置
// （system_configs/dictionaries 表属阶段 14+/19）。
type Rule struct {
	// Prefix 单据前缀（如 PO/IN/OUT/TR/CK/QC/LED/ADJ），大写字母与数字，1–8 位。
	Prefix string
	// DateSeg 是否携带日期段（M2 全部前缀均携带；预留 false 支持纯前缀+流水规则）。
	DateSeg bool
	// SeqWidth 流水位宽（0–9，0 取 DefaultSeqWidth）；超出位宽的流水原样扩展不截断。
	SeqWidth int
	// Reset 流水重置周期（ResetDay/ResetAll）。
	Reset Reset
}

// validate 校验规则值域（规则为代码内冻结注册表值，触发即编程错误）。
func (r Rule) validate() error {
	if r.Prefix == "" || len(r.Prefix) > maxPrefixLen {
		return response.NewError(ErrRuleInvalid, map[string]any{"prefix": r.Prefix})
	}
	for i := 0; i < len(r.Prefix); i++ {
		c := r.Prefix[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return response.NewError(ErrRuleInvalid, map[string]any{"prefix": r.Prefix})
		}
	}
	if r.Reset != ResetDay && r.Reset != ResetAll {
		return response.NewError(ErrRuleInvalid, map[string]any{"prefix": r.Prefix, "reset": string(r.Reset)})
	}
	if r.SeqWidth < 0 || r.SeqWidth > 9 {
		return response.NewError(ErrRuleInvalid, map[string]any{"prefix": r.Prefix, "seq_width": r.SeqWidth})
	}
	return nil
}

// periodFor 计数周期键：ResetAll → 常量 "ALL"（永不重置）；否则当日 YYYYMMDD（跨日自然重置）。
func periodFor(rule Rule, t time.Time) string {
	if rule.Reset == ResetAll {
		return "ALL"
	}
	return t.Format("20060102")
}

// format 组装单号：{prefix}-{YYYYMMDD}-{seq}（DateSeg=false 时无日期段）。
// seq 超出位宽原样十进制扩展（不截断、不回绕——号码唯一性由计数器单调递增保证）。
func format(rule Rule, t time.Time, seq int64) string {
	width := rule.SeqWidth
	if width == 0 {
		width = DefaultSeqWidth
	}
	seqSeg := fmt.Sprintf("%0*d", width, seq)
	if rule.DateSeg {
		return rule.Prefix + "-" + t.Format("20060102") + "-" + seqSeg
	}
	return rule.Prefix + "-" + seqSeg
}

// frozenRules 冻结前缀注册表（M2 plan §4.1 全量 + M3 plan §12.3 追加；前缀即 doc_number_counters.prefix 取值）。
// business-flow §13.1 已列 PO/IN/OUT/TR/CK/QC；RC/PW/SO/PK/CH/BP/SH/RT/EX 为 M2 补录；
// LED/ADJ 为 M1 存量前缀承接（ResetAll，日期段保留——plan §4.1"日期段 + 独立流水"语义）；
// IMP/EXP/PT 为 M3 补录（backend-m3-plan §12.3：导入/导出/打印任务，均 ResetDay 6 位流水；
// 有意避开 PRT——api §7 已冻结 prt: 为采购退货预占幂等键动作前缀，日志/审计中会混淆）。
var frozenRules = map[string]Rule{
	"PO":  {Prefix: "PO", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 采购单
	"IN":  {Prefix: "IN", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 入库单
	"RC":  {Prefix: "RC", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 收货单
	"QC":  {Prefix: "QC", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 质检单
	"PW":  {Prefix: "PW", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 上架任务
	"SO":  {Prefix: "SO", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 销售订单
	"OUT": {Prefix: "OUT", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay}, // 出库单
	"PK":  {Prefix: "PK", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 拣货任务
	"CH":  {Prefix: "CH", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 复核任务
	"BP":  {Prefix: "BP", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 包裹
	"SH":  {Prefix: "SH", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 发货单
	"TR":  {Prefix: "TR", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 调拨单
	"CK":  {Prefix: "CK", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 盘点单
	"RT":  {Prefix: "RT", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 退货单
	"EX":  {Prefix: "EX", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 异常单
	"IMP": {Prefix: "IMP", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay}, // 导入任务（M3，backend-m3-plan §12.3）
	"EXP": {Prefix: "EXP", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay}, // 导出任务（M3，backend-m3-plan §12.3）
	"PT":  {Prefix: "PT", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},  // 打印任务（M3，backend-m3-plan §12.3）
	"LED": {Prefix: "LED", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetAll}, // 库存流水（M1 承接）
	"ADJ": {Prefix: "ADJ", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetAll}, // 库存调整单（M1 承接）
}

// RuleFor 返回冻结注册表中的编号规则（各域统一经此处取规则，禁止散落字面量拼规则）。
func RuleFor(prefix string) (Rule, bool) {
	r, ok := frozenRules[prefix]
	return r, ok
}

// Next 在业务事务内取下一个单号（plan §4.1 冻结签名与发放机制）。
//
// tx 必须非 nil：流水发放必须与单据创建同事务（§4.1），事务回滚则号码作废出现空洞。
// 同事务重复调用返回递增新号——调用方 INSERT 撞历史遗留单号唯一约束时可再取号重试
// （或直接使用 NextWithRetry）。
func Next(ctx context.Context, tx *gorm.DB, rule Rule) (string, error) {
	if err := rule.validate(); err != nil {
		return "", err
	}
	if tx == nil {
		return "", response.NewError(ErrTxRequired, nil)
	}
	if ctx != nil {
		tx = tx.WithContext(ctx)
	}
	now := time.Now()
	period := periodFor(rule, now)
	// 冻结两语句发放（plan §4.1）：先补行（并发首建竞态由 ON CONFLICT 收敛到既有行），
	// 再原子自增 RETURNING（同事务内 INSERT 后 UPDATE 必命中；行锁串行化保证并发下号码唯一递增）。
	if err := tx.Exec(`
		INSERT INTO doc_number_counters (prefix, period, next_no, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, 0, now(), now(), 0, 0)
		ON CONFLICT (prefix, period) DO NOTHING`,
		rule.Prefix, period).Error; err != nil {
		return "", err
	}
	var seq int64
	if err := tx.Raw(`
		UPDATE doc_number_counters SET next_no = next_no + 1, updated_at = now()
		WHERE prefix = ? AND period = ?
		RETURNING next_no`,
		rule.Prefix, period).Scan(&seq).Error; err != nil {
		return "", err
	}
	if seq <= 0 {
		// 不可达路径（INSERT 后同事务 UPDATE 必命中一行），防御计数表被外部破坏。
		return "", fmt.Errorf("docnum: 计数器发放未命中（prefix=%s period=%s）", rule.Prefix, period)
	}
	return format(rule, now, seq), nil
}

// RetryLimit 单号唯一冲突重试上限（兜底历史遗留同格式单号碰撞；计数器本身保证新号
// 之间唯一且递增，正常路径一次命中）。
const RetryLimit = 3

// NextWithRetry 取号并执行落库写入：insert 返回 PostgreSQL 唯一约束冲突（23505，
// 如历史遗留同格式单号撞 uk_*_no）时自动取下一个号重试，直至成功或重试耗尽
// （返回 DOCNUM_NUMBER_CONFLICT）。insert 回调收到同一 tx（与取号同事务），
// 任一次非冲突错误立即原样返回，事务由调用方统一回滚。
func NextWithRetry(ctx context.Context, tx *gorm.DB, rule Rule, insert func(tx *gorm.DB, no string) error) (string, error) {
	if insert == nil {
		return "", fmt.Errorf("docnum: insert 回调不能为空（编程错误）")
	}
	var lastErr error
	for range RetryLimit {
		no, err := Next(ctx, tx, rule)
		if err != nil {
			return "", err
		}
		if err = insert(tx, no); err == nil {
			return no, nil
		}
		lastErr = err
		if !isUniqueViolation(err) {
			return "", err
		}
	}
	return "", response.NewError(ErrNumberConflict, map[string]any{
		"prefix":     rule.Prefix,
		"last_error": lastErr.Error(),
	})
}

// isUniqueViolation 判断 err 是否为 PostgreSQL 唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
