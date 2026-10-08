package sysops

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdvisoryLockUsesNativePlaceholders 守住 advisory lock 的占位符形态（回归防线）。
//
// 背景（2026-10-07 实测服务日志复现）：本路径走 database/sql 原生连接（*sql.Conn），
// 绕过 GORM 方言器，底层是 pgx/v5 stdlib 驱动——它不认 `?`。原实现写
// `SELECT pg_try_advisory_lock(hashtext(?))`，语句被原样下发，PG 报
// `syntax error at or near ")"`（SQLSTATE 42601），TryLock 恒失败 → 调度器按
// fail-closed 逻辑跳过**全部**定时任务（低库存/效期/积压扫描等静默不执行）。
// 占位符必须是 $n。
func TestAdvisoryLockUsesNativePlaceholders(t *testing.T) {
	sysCapture.reset()
	db, err := openSysFakeGorm()
	require.NoError(t, err)

	locker := &sqlLockAcquirer{db: db}
	_, _, _ = locker.TryLock(context.Background(), "sfjob:test_placeholder")

	acquire := sysCapture.queryOf("pg_try_advisory_lock")
	require.NotEmpty(t, acquire, "应下发 advisory lock 加锁语句")
	require.Contains(t, acquire, "$1", "加锁语句必须用 pgx 原生 $n 占位符")
	require.NotContains(t, acquire, "hashtext(?)", "不得使用 `?` 占位符（pgx 不识别，PG 报 42601）")
	require.False(t, strings.Contains(acquire, "?") && !strings.Contains(acquire, "$1"),
		"占位符形态错误：%s", acquire)
}

// TestLockKeyFormat plan §4.3：任务级锁键统一前缀。
func TestLockKeyFormatPrefix(t *testing.T) {
	for _, code := range []string{"low_stock_scan", "task_timeout_scan", "inventory_expiry_scan"} {
		require.True(t, strings.HasPrefix(lockKey(code), "sfjob:"), "锁键应为 sfjob:{code}：%s", lockKey(code))
	}
}
