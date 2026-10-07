package docnum

// LED 类 Preallocate 规则的号段预取分配器（2026-10 渗透修复：库存流水发放串行化消除）。
//
// 背景：LED 规则 ResetAll → 计数恒定落在 doc_number_counters 单行（prefix='LED',
// period='ALL'）。原 nextInTx 逐号 UPDATE...RETURNING，计数行锁持有至调用方事务提交
// ——而库存流水是每个库存写事务的收尾写，全系统所有库存写事务因此在提交尾于该行
// 串行化（高并发下发号成为全局串行点）。
//
// 机制：进程内号段缓存。缓存耗尽时以独立短事务（根连接池 + 独立 3s 超时 context，
// 见 recoverRootPool/refillFromDB）对计数行一次原子预留 preallocBatch 个号——行锁仅在
// 毫秒级短事务内持有，不再与调用方长事务锁序交互；进程内 preallocator.mu 保护号段
// 发放（补号段持锁执行：预取频率 ~1/preallocBatch 次取号，串行化补段可接受）。
//
// 多实例部署安全：号段由 DB 原子 UPDATE ... RETURNING 划分，各实例（各进程）预留的
// 号段互不重叠；进程内号码唯一性由 mutex 串行发放保证。
//
// 语义变化（业务已确认接受，docs/inventory-rules.md §5）：
//   - LED 流水号允许跳号：调用方事务回滚不再"回收"号码（原同事务发放回滚即作废出
//     现空洞——business-flow §13.1 本不要求连续）；进程重启丢弃未发完的号段缓存 = 跳号。
//   - ledger_no 唯一性不变（uk_inventory_ledgers_ledger_no 照常兜底）。
//   - LED 以外取号规则语义一律不变（仍同事务逐号发放）。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	// preallocBatch 每次短事务预取的号段大小（UPDATE next_no = next_no + N 一次预留）。
	preallocBatch = int64(100)
	// preallocTimeout 补号段短事务超时。用独立 context（不派生自调用方 ctx）：调用方
	// 长事务的取消/超时不应中断补段（补段成功即号段已预留，作废才是浪费）。
	preallocTimeout = 3 * time.Second
)

// errPreallocUnavailable 根连接池不可恢复且无注入补段实现（如单测假事务、
// 非标准连接池包装）——调用方回退 nextInTx 同事务逐号发放，正确性不受影响。
var errPreallocUnavailable = errors.New("docnum: 号段预取根连接池不可用，回退同事务发放")

// numberSegment 本进程持有的号段 [begin, end]；cursor 为下一个待发放号。
type numberSegment struct {
	begin, end, cursor int64
}

// preallocator Preallocate 规则的进程内号段分配器（包级单例 prealloc）。
// mu 保护 cache 与补段调用；补段失败不写缓存（号段要么整体预留成功要么整体作废）。
type preallocator struct {
	mu    sync.Mutex
	cache map[string]*numberSegment // key: prefix+"\x00"+period（当前仅 LED→"ALL"；键含 period 以支持任意周期扩展）
	// refillFn 补号段实现注入点（nil = 生产实现 refillFromDB，需根连接池）：
	// 原子预留 count 个号，返回计数器新值（本次号段 = [新值-count+1, 新值]）。
	// 仅测试注入内存替身以在无 PostgreSQL 环境验证并发语义。
	refillFn func(ctx context.Context, prefix, period string, count int64) (int64, error)
}

var prealloc = &preallocator{cache: map[string]*numberSegment{}}

// nextPreallocated Preallocate 规则取号入口（Next 分派）：进程内号段直取；
// 号段不可用（根句柄恢复失败 / 补段失败——含 DB 抖动超时）时回退 nextInTx
// 同事务逐号发放：正确性兜底（错误最终照常上抛），仅失去行锁串行化消除的优化。
func nextPreallocated(ctx context.Context, tx *gorm.DB, rule Rule) (string, error) {
	now := time.Now()
	period := periodFor(rule, now)
	if seq, err := prealloc.take(rule.Prefix, period, recoverRootPool(tx)); err == nil {
		return format(rule, now, seq), nil
	}
	return nextInTx(ctx, tx, rule)
}

// take 发放下一个流水序号；号段耗尽触发补段（持 preallocator.mu 串行）。
// root 为从调用方事务恢复的根连接池（可为 nil——此时必须有注入的 refillFn，否则
// 返回 errPreallocUnavailable）。
func (p *preallocator) take(prefix, period string, root *sql.DB) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := prefix + "\x00" + period
	seg := p.cache[key]
	if seg == nil || seg.cursor > seg.end {
		ctx, cancel := context.WithTimeout(context.Background(), preallocTimeout)
		defer cancel()
		var newNo int64
		var err error
		switch {
		case p.refillFn != nil: // 测试注入（docnum 单测无 PostgreSQL）
			newNo, err = p.refillFn(ctx, prefix, period, preallocBatch)
		case root != nil: // 生产：根连接池独立短事务补段
			newNo, err = refillFromDB(ctx, root, prefix, period, preallocBatch)
		default:
			return 0, errPreallocUnavailable
		}
		if err != nil {
			return 0, err
		}
		if newNo <= 0 {
			// 不可达路径（UPDATE ... RETURNING 必命中一行且返回 ≥ count），防御计数表被外部破坏。
			return 0, fmt.Errorf("docnum: 计数器预取未命中（prefix=%s period=%s next_no=%d）", prefix, period, newNo)
		}
		seg = &numberSegment{begin: newNo - preallocBatch + 1, end: newNo, cursor: newNo - preallocBatch + 1}
		p.cache[key] = seg
	}
	seq := seg.cursor
	seg.cursor++
	return seq, nil
}

// recoverRootPool 从调用方业务事务恢复根连接池（补号段短事务需独立于调用方长事务）。
//
// 根句柄方案选型（渗透修复决策，两候选："启动注入根 db" vs "从事务恢复"）：
// 选择后者——gorm v1.31.2 的 Begin 仅替换 tx.Statement.ConnPool（→ *sql.Tx），
// tx.Config.ConnPool 仍指向根连接池（Session 对 Config 浅拷贝、Begin 不触碰该字段，
// 已对照源码确认），故免装配自动恢复，不依赖 router/cmd 启动路径注入（注入遗漏只会
// 静默回退旧路径、丢失优化，反而更难发现）。防错三重守卫，任一不满足即返回 nil 回退：
//  1. Config.ConnPool 为 nil → 不可恢复；
//  2. 实现 gorm.TxCommitter（Commit/Rollback，即其本身是事务而非连接池）→ 拒绝，
//     防止把调用方事务当根池——那会让补段短事务退化回调用方长事务，行锁恰又持有至
//     提交（正是本修复要消除的串行化）；
//  3. 非标准 *sql.DB（PrepareStmt/dbresolver 等包装池）→ 拒绝。
//
// postgres 方言器 Initialize 以 stdlib.OpenDB 建池，根池即 *sql.DB（go.mod 固定
// gorm v1.31.2 + gorm.io/driver/postgres，database.Connect 未启用 PrepareStmt）。
func recoverRootPool(tx *gorm.DB) *sql.DB {
	if tx == nil || tx.Config == nil || tx.Config.ConnPool == nil {
		return nil
	}
	pool := tx.Config.ConnPool
	if _, isTx := pool.(gorm.TxCommitter); isTx {
		return nil
	}
	if sqldb, ok := pool.(*sql.DB); ok {
		return sqldb
	}
	return nil
}

// refillFromDB 生产补段实现：根连接池上开独立短事务（独立 3s 超时 context）——
// 先补行（并发首建竞态由 ON CONFLICT DO NOTHING 收敛，与 nextInTx 同口径），
// 再一条 UPDATE ... RETURNING 原子预留 count 个号。计数行锁只在毫秒级短事务内持有。
// 底层为 pgx/v5 stdlib 驱动（gorm postgres 方言器默认），占位符用 $n。
func refillFromDB(ctx context.Context, root *sql.DB, prefix, period string, count int64) (int64, error) {
	dbTx, err := root.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = dbTx.Rollback() }() // 提交后再 Rollback 返回 ErrTxDone，忽略
	if _, err = dbTx.ExecContext(ctx, `
		INSERT INTO doc_number_counters (prefix, period, next_no, created_at, updated_at, created_by, updated_by)
		VALUES ($1, $2, 0, now(), now(), 0, 0)
		ON CONFLICT (prefix, period) DO NOTHING`, prefix, period); err != nil {
		return 0, err
	}
	var newNo int64
	if err = dbTx.QueryRowContext(ctx, `
		UPDATE doc_number_counters SET next_no = next_no + $1, updated_at = now()
		WHERE prefix = $2 AND period = $3
		RETURNING next_no`, count, prefix, period).Scan(&newNo); err != nil {
		return 0, err
	}
	if err = dbTx.Commit(); err != nil {
		return 0, err
	}
	return newNo, nil
}
