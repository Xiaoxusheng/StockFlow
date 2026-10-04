package sysops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 审计日志只读查询（/api/logs，backend-m3-plan §10.1、architecture §8.2）：
// 筛选维度 = 用户/模块/时间/IP/操作类型/成功失败（keyword 覆盖 username+ip ILIKE）；
// 详情含三快照 jsonb 原样返回（脱敏由写入方保证——architecture §6 日志红线）。
// 无任何写接口（database §7 审计不可篡改；应用账号亦无 UPDATE/DELETE 权限——grants 红线）。

// operationLogItem 操作日志行（operation_logs 列全量，web/src/api/system.ts OperationLogItem 契约）。
type operationLogItem struct {
	ID              int64           `json:"id"`
	RequestID       string          `json:"request_id"`
	UserID          int64           `json:"user_id"`
	Username        string          `json:"username"`
	IP              string          `json:"ip"`
	UserAgent       string          `json:"user_agent,omitempty"`
	Module          string          `json:"module"`
	ObjectType      string          `json:"object_type,omitempty"`
	ObjectID        int64           `json:"object_id"`
	Action          string          `json:"action"`
	Method          string          `json:"method,omitempty"`
	Path            string          `json:"path,omitempty"`
	Success         bool            `json:"success"`
	ErrorCode       string          `json:"error_code,omitempty"`
	RequestSnapshot json.RawMessage `json:"request_snapshot,omitempty"`
	BeforeSnapshot  json.RawMessage `json:"before_snapshot,omitempty"`
	AfterSnapshot   json.RawMessage `json:"after_snapshot,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// loginLogItem 登录日志行（login_logs 列全量）。
type loginLogItem struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Username   string    `json:"username"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Success    bool      `json:"success"`
	FailReason string    `json:"fail_reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// logFilter 日志筛选（keyword=用户名/IP 模糊；module/action 维度筛选；success 三态）。
type logFilter struct {
	Keyword  string
	Module   string
	Action   string
	Success  *bool
	From, To *time.Time
	Page     int
	PageSize int
}

// pagedLog 通用日志分页（count 子查询 + 列表；排序 created_at DESC, id DESC 稳定序）。
func (r *runtimeRepo) pagedLog(ctx context.Context, base, columns string, args []any, page, pageSize int, dst any) (int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) "+base, args...).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sysops: 日志计数失败: %w", err)
	}
	list := "SELECT " + columns + " " + base + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, pageSize, (page-1)*pageSize)
	if err := r.db.WithContext(ctx).Raw(list, args...).Scan(dst).Error; err != nil {
		return 0, fmt.Errorf("sysops: 日志查询失败: %w", err)
	}
	return total, nil
}

// logWhere 组装公共 WHERE（keyword/module/action/success/时间范围；值一律参数化）。
func logWhere(f logFilter) (string, []any) {
	conds := []string{"1 = 1"}
	var args []any
	if f.Keyword != "" {
		conds = append(conds, "(username ILIKE ? OR ip ILIKE ?)")
		like := "%" + f.Keyword + "%"
		args = append(args, like, like)
	}
	if f.Module != "" {
		conds = append(conds, "module = ?")
		args = append(args, f.Module)
	}
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, f.Action)
	}
	if f.Success != nil {
		conds = append(conds, "success = ?")
		args = append(args, *f.Success)
	}
	if f.From != nil {
		conds = append(conds, "created_at >= ?")
		args = append(args, *f.From)
	}
	if f.To != nil {
		conds = append(conds, "created_at < ?")
		args = append(args, *f.To)
	}
	return strings.Join(conds, " AND "), args
}

// listOperationLogs 操作日志分页（含三快照原样返回）。
func (r *runtimeRepo) listOperationLogs(ctx context.Context, f logFilter) ([]operationLogItem, int64, error) {
	where, args := logWhere(f)
	base := "FROM operation_logs WHERE " + where
	var rows []operationLogItem
	total, err := r.pagedLog(ctx, base, "*", args, f.Page, f.PageSize, &rows)
	return rows, total, err
}

// getOperationLog 单行详情（三快照完整内容，system:log:read）。
func (r *runtimeRepo) getOperationLog(ctx context.Context, id int64) (*operationLogItem, bool, error) {
	var row operationLogItem
	err := r.db.WithContext(ctx).Raw(`SELECT * FROM operation_logs WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// listLoginLogs 登录日志分页。
func (r *runtimeRepo) listLoginLogs(ctx context.Context, f logFilter) ([]loginLogItem, int64, error) {
	where, args := logWhere(f)
	base := "FROM login_logs WHERE " + where
	var rows []loginLogItem
	total, err := r.pagedLog(ctx, base, "*", args, f.Page, f.PageSize, &rows)
	return rows, total, err
}
