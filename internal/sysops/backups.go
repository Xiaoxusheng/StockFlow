package sysops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 备份登记与状态核对（backend-m3-plan §10.5，Orchestrator 裁决②：登记 + 外部执行混合模式）。
//
// 职责划分（冻结）：pg_dump 由部署侧执行器（宿主机 cron / 独立 cron 容器，deployment §4）
// 轮询 REQUESTED 记录 FOR UPDATE SKIP LOCKED 拾取执行并回写结果；应用进程不执行、不依赖
// pg_dump 二进制。应用侧只做：备份记录登记（REQUESTED）/ 列表 / 下载 / 状态核对（watch.go
// file_cleanup 兼做：REQUESTED/RUNNING 超 30 分钟标记 FAILED + 告警）/ 过期清理。
// 恢复不做应用内接口（deployment §4.2 恢复为 DBA 离线操作，"一键恢复"属高危假能力）。

// 备份状态机（000014 chk_backup_records_status）。
const (
	backupStatusRequested = "REQUESTED"
	backupStatusRunning   = "RUNNING"
	backupStatusSuccess   = "SUCCESS"
	backupStatusFailed    = "FAILED"
)

// backupTrigger 值域（AUTO=部署侧执行器调度/MANUAL=管理端登记）。
const backupTriggerManual = "MANUAL"

// backupRecord 备份记录（backup_records 列全量）。
type backupRecord struct {
	ID         int64      `json:"id"`
	FileName   string     `json:"file_name"`
	FilePath   string     `json:"file_path"`
	SizeBytes  int64      `json:"size_bytes"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  int64      `json:"created_by"`
}

// registerBackup 登记 REQUESTED 记录（trigger=MANUAL；同事务审计）。
// 并发手动触发命中 uk_backup_records_inflight（trigger 部分唯一）→ 409 SYSTEM_BACKUP_INFLIGHT。
// 返回"已登记，等待部署侧执行器拾取"——真实能力非假按钮：外部执行器轮询 REQUESTED 拾取。
func (r *runtimeRepo) registerBackup(ctx context.Context, actor auditActor) (*backupRecord, error) {
	row := &backupRecord{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// CTE RETURNING 原子取回插入 ID（store.go BeginRun 同款——规避连接池下 currval 会话漂移）。
		var newID int64
		if err := tx.Raw(`
			WITH ins AS (
				INSERT INTO backup_records (file_name, file_path, size_bytes, "trigger", status, message, created_at, updated_at, created_by, updated_by)
				VALUES ('', '', 0, ?, ?, '已登记，等待部署侧执行器拾取', now(), now(), ?, ?)
				RETURNING id
			)
			SELECT id FROM ins`,
			backupTriggerManual, backupStatusRequested, actor.UserID, actor.UserID).Scan(&newID).Error; err != nil {
			return translateBackupDup(err)
		}
		if err := tx.Raw(`
			SELECT id, file_name, file_path, size_bytes, "trigger", status, message,
			       started_at, finished_at, created_at, created_by
			FROM backup_records WHERE id = ?`, newID).Scan(row).Error; err != nil {
			return err
		}
		row.ID = newID
		return middleware.Audit(tx, auditEntry(actor, "backup_register", "backup_record", row.ID,
			nil, map[string]any{"id": row.ID, "trigger": backupTriggerManual, "status": backupStatusRequested}, nil))
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// translateBackupDup 在途唯一索引冲突 → 业务 409（其余原样上抛）。
func translateBackupDup(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		strings.Contains(pgErr.ConstraintName, "uk_backup_records_inflight") {
		return ErrBackupInflight
	}
	return err
}

// listBackups 备份记录分页（status 筛选，created_at 倒序）。
func (r *runtimeRepo) listBackups(ctx context.Context, status string, page, pageSize int) ([]backupRecord, int64, error) {
	where, args := "1 = 1", []any{}
	if status != "" {
		where, args = "status = ?", []any{status}
	}
	base := "FROM backup_records WHERE " + where
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) "+base, args...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 备份记录计数失败: %w", err)
	}
	var rows []backupRecord
	list := `SELECT id, file_name, file_path, size_bytes, "trigger", status, message,
	                started_at, finished_at, created_at, created_by
	         ` + base + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (page-1)*pageSize)
	if err := r.db.WithContext(ctx).Raw(list, args...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 备份记录查询失败: %w", err)
	}
	return rows, total, nil
}

// getBackup 按 id 取记录。
func (r *runtimeRepo) getBackup(ctx context.Context, id int64) (*backupRecord, bool, error) {
	var row backupRecord
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, file_name, file_path, size_bytes, "trigger", status, message,
		       started_at, finished_at, created_at, created_by
		FROM backup_records WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// backupFilePath 解析备份物理路径（storage.root/backups/{file_path}）。
// 防路径穿越（api.md §5）：相对路径规范化后必须仍在 backups 根内，禁止任何上级目录逃逸。
func backupFilePath(root string, rec *backupRecord) (string, error) {
	if root == "" {
		return "", ErrStorageRootNotConfigured
	}
	if rec.Status != backupStatusSuccess || rec.FilePath == "" {
		return "", ErrBackupNotDownloadable
	}
	rel := filepath.ToSlash(filepath.Clean("/" + rec.FilePath)) // 绝对化后 Clean 抹平 .. 前缀
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", ErrBackupPathInvalid
	}
	full := filepath.Join(root, "backups", filepath.FromSlash(rel))
	// 双保险：Join 结果必须仍在 backups 根内。
	absRoot, err := filepath.Abs(filepath.Join(root, "backups"))
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return "", ErrBackupPathInvalid
	}
	if _, err := os.Stat(absFull); err != nil {
		return "", ErrBackupFileMissing
	}
	return absFull, nil
}

// ---- pg_dump 命令模板（裁决②：实际执行为外部宿主 cron，deployment §4 runbook）----

// pgDumpTemplate 备份命令模板（密码绝不进入命令行——进程参数对同机用户可见；
// 以 PGPASSWORD 环境变量或 .pgpass 注入，文档注明 deployment.md §4）。
type pgDumpTemplate struct {
	// Command pg_dump 命令模板（未注入的连接参数保持 <placeholder> 占位）。
	Command string `json:"command"`
	// CrontabLine 宿主机 crontab 样例（每日 02:30，随 deployment.md §4 交付）。
	CrontabLine string `json:"crontab_line"`
	// Notes 执行边界注记（应用进程不执行备份——裁决②；执行器拾取 REQUESTED 回写）。
	Notes []string `json:"notes"`
}

func buildPGDumpTemplate(host string, port int, user, dbname string) pgDumpTemplate {
	if host == "" {
		host = "<db_host>"
	}
	if port <= 0 {
		port = 5432
	}
	if user == "" {
		user = "<db_user>"
	}
	if dbname == "" {
		dbname = "<db_name>"
	}
	// 文件名占位由执行侧 shell 展开日期（宿主机 cron 环境变量与 umask 由部署侧控制）。
	file := "/var/backups/stockflow/stockflow_$(date +%Y%m%d_%H%M%S).dump"
	return pgDumpTemplate{
		Command: fmt.Sprintf(
			"PGPASSWORD=\"$SF_DATABASE_PASSWORD\" pg_dump --host=%s --port=%d --username=%s --format=custom --no-owner --no-privileges --file=%s %s",
			host, port, user, file, dbname),
		CrontabLine: "30 2 * * * /opt/stockflow/bin/backup-executor.sh >> /var/log/stockflow/backup.log 2>&1",
		Notes: []string{
			"pg_dump 由部署侧执行（宿主机 cron/独立 cron 容器，Orchestrator 裁决②），应用进程不执行备份",
			"密码经 PGPASSWORD 环境变量或 ~/.pgpass 注入，禁止写入命令行参数与 crontab 明文",
			"--format=custom 产物配合 pg_restore 恢复；恢复为 DBA 离线操作，应用不提供恢复接口（deployment.md §4.2）",
			"执行器应轮询 backup_records 中 status=REQUESTED 的登记行（FOR UPDATE SKIP LOCKED 拾取）并回写结果与文件信息",
			"备份文件保留期默认 14 天，超期由 file_cleanup 定时任务清理记录与物理文件",
		},
	}
}

// serveBackupDownload 流式下载（http.ServeContent，不整读内存——go-dev-standard 规则 11）。
func serveBackupDownload(c *gin.Context, path string, fileName string) {
	f, err := os.Open(path)
	if err != nil {
		response.Err(c, ErrBackupFileMissing)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		response.Err(c, ErrBackupFileMissing)
		return
	}
	if fileName == "" {
		fileName = filepath.Base(path)
	}
	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(fileName)+`"`)
	http.ServeContent(c.Writer, c.Request, fileName, st.ModTime(), f)
}

// sanitizeFilename 下载文件名白名单化（防 Content-Disposition 注入）。
func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	if name == "" || name == "." {
		return "backup"
	}
	return name
}
