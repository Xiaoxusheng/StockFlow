package sysops

import (
	"context"
	"fmt"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
)

// 备份 Service 面（裁决②混合模式：应用侧只登记/列表/下载/核对，pg_dump 外部执行）。

// ListBackups 备份记录分页。
func (s *Service) ListBackups(ctx context.Context, status string, page, pageSize int) ([]backupRecord, int64, error) {
	switch status {
	case "", backupStatusRequested, backupStatusRunning, backupStatusSuccess, backupStatusFailed:
	default:
		return nil, 0, fmt.Errorf("sysops: 备份状态筛选非法: %q", status)
	}
	return s.repo.listBackups(ctx, status, page, pageSize)
}

// RegisterBackup 登记 REQUESTED 记录（同事务审计；并发在途 409——plan §10.5）。
func (s *Service) RegisterBackup(ctx context.Context, actor auditActor) (*backupRecord, error) {
	return s.repo.registerBackup(ctx, actor)
}

// PrepareBackupDownload 下载准备：记录校验 + 物理路径解析（防穿越）+ 同事务下载审计。
func (s *Service) PrepareBackupDownload(ctx context.Context, actor auditActor, id int64) (string, string, error) {
	rec, ok, err := s.repo.getBackup(ctx, id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrBackupNotFound
	}
	path, err := backupFilePath(runtimeCfgValue.StorageRoot, rec)
	if err != nil {
		return "", "", err
	}
	// 下载审计（敏感操作 permission §6）：独立事务，失败不阻断已校验的合法下载。
	if aerr := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return middleware.Audit(tx, auditEntry(actor, "backup_download", "backup_record", id,
			nil, map[string]any{"id": id, "file": rec.FileName}, nil))
	}); aerr != nil {
		return "", "", fmt.Errorf("备份下载审计写入失败: %w", aerr)
	}
	fileName := rec.FileName
	if fileName == "" {
		fileName = filepath.Base(path)
	}
	return path, fileName, nil
}

// PGDumpTemplate pg_dump 命令模板（连接参数由 router 装配注入；密码绝不进模板——
// PGPASSWORD/.pgpass 注入，实际执行为外部宿主 cron，deployment §4）。
func (s *Service) PGDumpTemplate(ctx context.Context) (pgDumpTemplate, error) {
	if err := ctx.Err(); err != nil {
		return pgDumpTemplate{}, err
	}
	return buildPGDumpTemplate(runtimeCfgValue.DBHost, runtimeCfgValue.DBPort,
		runtimeCfgValue.DBUser, runtimeCfgValue.DBName), nil
}

// ---- 通知个人收件箱（认证即可用）----

// UnreadCount 未读数。
func (s *Service) UnreadCount(ctx context.Context, userID int64) (int64, error) {
	return s.repo.unreadCount(ctx, userID)
}

// ListInbox 收件箱分页（read 筛选）。
func (s *Service) ListInbox(ctx context.Context, f inboxFilter) ([]inboxItem, int64, error) {
	return s.repo.listInbox(ctx, f)
}

// MarkRead 标记已读（仅本人行；0 行=不存在或不属于当前用户）。
func (s *Service) MarkRead(ctx context.Context, userID, id int64) (int64, error) {
	return s.repo.markRead(ctx, userID, id)
}

// MarkAllRead 全部已读。
func (s *Service) MarkAllRead(ctx context.Context, userID int64) (int64, error) {
	return s.repo.markAllRead(ctx, userID)
}
