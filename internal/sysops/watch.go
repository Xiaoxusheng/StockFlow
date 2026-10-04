package sysops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// file_cleanup 任务实现（backend-m3-plan §4.3/§10.5，0 6 * * *→注册表 30 3 * * *）：
//  1. backup_records REQUESTED/RUNNING 超时核对（30 分钟）：标记 FAILED + 站内告警
//     （裁决② 应用侧核对义务——外部执行器崩溃/未拾取的可观测兜底）；
//  2. 备份保留期清理（runtimeCfg.BackupRetentionDays，默认 14 天）：SUCCESS/FAILED 记录
//     超期 → 删记录 + 删物理文件（storage.root/backups/ 内）；
//  3. files 表过期清理（仅当 runtimeCfg.StorageRoot 已配置——files 域数据面属 datax，
//     物理根由 router 装配注入；未配置时跳过并记 debug 日志，不猜测存储根）。
//
// 幂等：状态守卫（UPDATE ... WHERE status=<前置态>）天然幂等；物理删除按主键定位。

// backupTimeout 备份在途超时（REQUESTED/RUNNING 超过该时长标记 FAILED——plan §4.3）。
const backupTimeout = 30 * time.Minute

// cfgKeyBackupRetentionDays 备份保留期运行时覆盖键（未 seed——以 runtimeCfg 为准，
// system_configs 出现该键时优先，便于不停机调整保留策略）。
const cfgKeyBackupRetentionDays = "sysops.backup_retention_days"
const defBackupRetentionDays = 14

// cfgKeyFileRetentionDays files 过期保留天数运行时覆盖键（与 datax.file_retention_days
// 环境变量默认值同源——plan §10.2：实际取值优先级 system_configs > 环境变量默认）。
const cfgKeyFileRetentionDays = "datax.file_retention_days"
const defFileRetentionDays = 30

// runtimeConfig 运行时注入参数（router 装配经 Configure 注入；零值=功能降级不猜测）。
type runtimeConfig struct {
	// StorageRoot 文件存储根（config storage.root；为空时 files 清理与备份文件删除跳过）。
	StorageRoot string
	// BackupRetentionDays 备份文件/记录保留期（config sysops.backup_retention_days）。
	BackupRetentionDays int
	// 数据库连接参数（pg_dump 命令模板生成用；密码绝不注入——PGPASSWORD/.pgpass 承担）。
	DBHost string
	DBPort int
	DBUser string
	DBName string
}

var runtimeCfgValue runtimeConfig

// Configure 注入运行时参数（router 装配期调用一次；幂等覆盖）。
func Configure(cfg runtimeConfig) { runtimeCfgValue = cfg }

func (s scanRunner) retentionDays(ctx context.Context) (int, error) {
	if runtimeCfgValue.BackupRetentionDays > 0 {
		return runtimeCfgValue.BackupRetentionDays, nil
	}
	raw, err := s.configValue(ctx, cfgKeyBackupRetentionDays, fmt.Sprintf("%d", defBackupRetentionDays))
	if err != nil {
		return defBackupRetentionDays, nil // 配置面故障不阻断清理主流程（默认值兜底）
	}
	n, err := parseIntConfig(raw, cfgKeyBackupRetentionDays)
	if err != nil || n < 1 {
		return defBackupRetentionDays, nil
	}
	return n, nil
}

func (s scanRunner) fileCleanup(ctx context.Context) error {
	var failures []string

	// 1) 备份在途超时核对（REQUESTED/RUNNING > 30 分钟 → FAILED + 告警）。
	//    SELECT...FOR UPDATE 与状态回写同事务——行锁持有至核对完成，防并发扫描
	//    双标（自动提交下锁随语句结束释放，防并发核对名存实亡）。
	staleDeadline := s.now().Add(-backupTimeout)
	// 告警收件人每轮恒定：事务外解析一次（事务内查询会延长锁持有时间，且防逐行 N+1）。
	failureRecipients, rerr := resolveRecipients(ctx, s.db, failureRecipientRoles)
	if rerr != nil {
		// 收件人解析失败不阻断超时回填（状态回填是主事实），告警降级为失败记录。
		failureRecipients = nil
	}
	txErr := database.Tx(ctx, s.db, func(tx *gorm.DB) error {
		var stale []backupRecord
		if err := tx.WithContext(ctx).Raw(`
			SELECT id, file_name, status, started_at, created_at
			FROM backup_records
			WHERE status IN ('REQUESTED','RUNNING')
			  AND COALESCE(started_at, created_at) <= ?
			FOR UPDATE`, staleDeadline).Scan(&stale).Error; err != nil {
			failures = append(failures, fmt.Sprintf("备份超时记录读取失败: %v", err))
			return nil // 失败已记录：不回滚（无业务写入），继续后续子任务
		}
		for _, rec := range stale {
			res := tx.WithContext(ctx).Exec(`
				UPDATE backup_records
				SET status = 'FAILED', finished_at = now(), updated_at = now(),
				    message = CASE WHEN status = 'REQUESTED'
				              THEN '已登记但部署侧执行器超时未拾取（30 分钟），请检查备份执行器'
				              ELSE '执行器回写 RUNNING 后超时未完成（30 分钟），请检查备份执行器' END
				WHERE id = ? AND status IN ('REQUESTED','RUNNING')`, rec.ID)
			if res.Error != nil {
				failures = append(failures, fmt.Sprintf("备份 %d 超时标记失败: %v", rec.ID, res.Error))
				continue
			}
			if res.RowsAffected > 0 {
				if _, err := notifyRecipients(ctx, gormNotifyStore{db: s.db}, notifyInput{
					Type:  NotifyTypeSystem,
					Title: "备份任务超时失败",
					Content: fmt.Sprintf(
						"备份记录 #%d（%s）超过 30 分钟未完成，已标记 FAILED，请检查部署侧备份执行器（deployment.md §4）。",
						rec.ID, rec.FileName),
					DedupKey: fmt.Sprintf("backuptimeout:%d", rec.ID),
				}, failureRecipients); err != nil {
					// 告警失败不阻断状态回填（状态已是事实），记录失败原因。
					failures = append(failures, fmt.Sprintf("备份 %d 超时告警失败: %v", rec.ID, err))
				}
			}
		}
		return nil
	})
	if txErr != nil {
		failures = append(failures, fmt.Sprintf("备份超时核对事务失败: %v", txErr))
	}

	// 2) 备份保留期清理：SUCCESS/FAILED 超期记录 + 物理文件。
	retention, err := s.retentionDays(ctx)
	if err != nil {
		failures = append(failures, fmt.Sprintf("备份保留期读取失败: %v", err))
	} else if runtimeCfgValue.StorageRoot != "" {
		cutoff := s.now().AddDate(0, 0, -retention)
		var expired []backupRecord
		if err := s.db.WithContext(ctx).Raw(`
			SELECT id, file_name, file_path FROM backup_records
			WHERE status IN ('SUCCESS','FAILED') AND file_path <> '' AND created_at <= ?`, cutoff).
			Scan(&expired).Error; err != nil {
			failures = append(failures, fmt.Sprintf("过期备份记录读取失败: %v", err))
		}
		for _, rec := range expired {
			if path, perr := backupFilePath(runtimeCfgValue.StorageRoot, &backupRecord{
				Status: backupStatusSuccess, FilePath: rec.FilePath,
			}); perr == nil {
				if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
					failures = append(failures, fmt.Sprintf("备份文件删除失败（%s）: %v", path, rmErr))
				}
			}
			if err := s.db.WithContext(ctx).Exec(`DELETE FROM backup_records WHERE id = ?`, rec.ID).Error; err != nil {
				failures = append(failures, fmt.Sprintf("备份记录 %d 删除失败: %v", rec.ID, err))
			}
		}
	}

	// 3) files 过期清理（files 域数据面；StorageRoot 未配置时跳过——不猜测存储根）。
	if runtimeCfgValue.StorageRoot == "" {
		s.log.Debug("storage root 未注入（router 装配 Configure），本轮跳过 files 过期清理")
	} else {
		retentionDays, err := s.fileRetentionDays(ctx)
		if err != nil {
			failures = append(failures, fmt.Sprintf("文件保留期读取失败: %v", err))
		} else {
			n, err := s.cleanupExpiredFiles(ctx, runtimeCfgValue.StorageRoot, retentionDays)
			if err != nil {
				failures = append(failures, fmt.Sprintf("过期文件清理失败: %v", err))
			} else {
				s.log.Info("过期文件清理完成", zap.Int64("removed", n))
			}
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("file_cleanup 部分子任务失败: %v", failures)
	}
	return nil
}

func (s scanRunner) fileRetentionDays(ctx context.Context) (int, error) {
	if raw, err := s.configValue(ctx, cfgKeyFileRetentionDays, fmt.Sprintf("%d", defFileRetentionDays)); err == nil {
		if n, perr := parseIntConfig(raw, cfgKeyFileRetentionDays); perr == nil && n >= 1 {
			return n, nil
		}
	}
	return defFileRetentionDays, nil
}

// cleanupExpiredFiles files.expires_at 已过期 → 物理删除 + 删行
// （files 无业务软删语义残留价值——excel §5"过期自动清理"；行删除为本白名单清理通路）。
// 选取（SELECT...FOR UPDATE）与删除同事务：行锁持有至删除完成，防并发清理双删。
func (s scanRunner) cleanupExpiredFiles(ctx context.Context, root string, retentionDays int) (int64, error) {
	var removed int64
	err := database.Tx(ctx, s.db, func(tx *gorm.DB) error {
		var rows []struct {
			ID          int64
			StoragePath string
		}
		if err := tx.WithContext(ctx).Raw(`
			SELECT id, storage_path FROM files
			WHERE expires_at IS NOT NULL AND expires_at <= ?
			FOR UPDATE`, s.now()).Scan(&rows).Error; err != nil {
			return err
		}
		for _, f := range rows {
			// storage_path 为相对根路径（files 表列注释），Join 归一并防逃逸。
			full := filepath.Join(root, filepath.FromSlash(f.StoragePath))
			if absRoot, err := filepath.Abs(root); err == nil {
				if absFull, err := filepath.Abs(full); err == nil &&
					absFull != absRoot && !isWithin(absFull, absRoot) {
					s.log.Warn("过期文件路径越界，跳过物理删除", zap.Int64("file_id", f.ID), zap.String("path", f.StoragePath))
				} else if rmErr := os.Remove(full); rmErr != nil && !os.IsNotExist(rmErr) {
					s.log.Warn("过期文件物理删除失败", zap.Int64("file_id", f.ID), zap.Error(rmErr))
				}
			}
			if err := tx.WithContext(ctx).Exec(`DELETE FROM files WHERE id = ?`, f.ID).Error; err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && rel != "" && rel[:2] != ".."
}

// watchRunner file_cleanup 执行体别名（wiring 注册名与注册表 code 对应）。
func (s scanRunner) watchScan(ctx context.Context) error {
	return s.fileCleanup(ctx)
}
