// audit.go 共享审计 helper（backend-m1-plan §4.4 冻结契约）：
// operation_logs / login_logs 的 GORM 模型与写入助手。
//
// 落位说明（Orchestrator 裁决 2026-10-02）：本文件属 scope A 独占（plan §4.1），
// 脚手架阶段漏交，经裁决由 auth 域（T2）按 §4.4 冻结形态代交——导出签名与 §4.4
// 一致，后续基础资料/仓库/库存等域的敏感操作审计同样经本入口写入，禁止任何包
// 另建第二套审计写入（plan §4.2 判据、architecture.md §8）。
//
// 契约要点：
//   - Audit 在业务事务内写 operation_logs（architecture.md §4：写操作日志与业务同事务；
//     失败即业务失败，整体回滚——审计不可缺位）；
//   - 写入方负责脱敏：密码、Token 等敏感值绝不进入任何快照（architecture.md §6 日志红线）；
//   - 两表为纯日志表（database.md §3 但书）：无 updated_by/deleted_at，只 INSERT，
//     不提供任何 UPDATE/DELETE 通路（database.md §7 审计数据不可篡改）。
//
// 与 §4.4 冻结形态的差异（以迁移为准，backend-m1-plan §6.2 列结构）：AuditEntry 增补
// UserAgent/Method/Path 三个可选字段——operation_logs 实际列含 user_agent/method/path，
// 且 architecture.md §8.1 要求操作日志记录"设备与请求"；§4.4 原有字段一字未改。
package middleware

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// AuditEntry 关键操作审计条目（§4.4 冻结字段 + 以迁移列为准增补的 UserAgent/Method/Path）。
type AuditEntry struct {
	Module, ObjectType, Action  string
	ObjectID                    int64
	OperatorID                  int64 // 调用方经 auth.CurrentUser 填入；系统操作为 0
	OperatorName, RequestID, IP string
	Success                     bool
	ErrorCode                   string // 失败时的业务错误码；成功为空
	Before, After, Request      any    // 关键更新必填 before/after 快照（architecture.md §8.1）；可为 nil
	UserAgent                   string // 设备信息（architecture.md §8.1）
	Method, Path                string // 请求方法与路径
}

// OperationLog operation_logs GORM 模型（000002 迁移列结构，append-only）。
type OperationLog struct {
	ID              database.ID `gorm:"primaryKey;autoIncrement"`
	RequestID       string      `gorm:"column:request_id;size:64"`
	UserID          database.ID `gorm:"column:user_id"`
	Username        string      `gorm:"column:username;size:64"`
	IP              string      `gorm:"column:ip;size:64"`
	UserAgent       string      `gorm:"column:user_agent;size:512"`
	Module          string      `gorm:"column:module;size:64"`
	ObjectType      string      `gorm:"column:object_type;size:64"`
	ObjectID        int64       `gorm:"column:object_id"`
	Action          string      `gorm:"column:action;size:64"`
	Method          string      `gorm:"column:method;size:16"`
	Path            string      `gorm:"column:path;size:512"`
	Success         bool        `gorm:"column:success"`
	ErrorCode       string      `gorm:"column:error_code;size:64"`
	RequestSnapshot jsonRaw     `gorm:"column:request_snapshot;type:jsonb"`
	BeforeSnapshot  jsonRaw     `gorm:"column:before_snapshot;type:jsonb"`
	AfterSnapshot   jsonRaw     `gorm:"column:after_snapshot;type:jsonb"`
	CreatedAt       database.JSONTime
}

// TableName 显式指定表名。
func (OperationLog) TableName() string { return "operation_logs" }

// LoginLogEntry 登录日志条目（permission.md §3.3：每次登录成功/失败均记录）。
type LoginLogEntry struct {
	UserID     int64 // 未知用户为 0
	Username   string
	IP         string
	UserAgent  string
	Success    bool
	FailReason string // 失败原因（错误码）；成功为空
}

// LoginLog login_logs GORM 模型（000002 迁移列结构，append-only）。
type LoginLog struct {
	ID         database.ID `gorm:"primaryKey;autoIncrement"`
	UserID     database.ID `gorm:"column:user_id"`
	Username   string      `gorm:"column:username;size:64"`
	IP         string      `gorm:"column:ip;size:64"`
	UserAgent  string      `gorm:"column:user_agent;size:512"`
	Success    bool        `gorm:"column:success"`
	FailReason string      `gorm:"column:fail_reason;size:255"`
	CreatedAt  database.JSONTime
}

// TableName 显式指定表名。
func (LoginLog) TableName() string { return "login_logs" }

// ErrAuditDBNil db 参数缺失错误：审计写入必须显式传入事务或 db 句柄（不静默兜底）。
var ErrAuditDBNil = errors.New("middleware.Audit/LoginLog: db 句柄为 nil，审计写入缺位（禁止）")

// 审计列宽对齐（安全审查 S7）：request_id/ip varchar(64)、user_agent varchar(512)。
// 采集点（各域 handler 从客户端头读取）不可控，在写入入口统一截断——超列宽会使
// INSERT 失败并连带业务事务回滚（审计不可缺位，architecture.md §4）。
// IP 为 ASCII 字节截断即字符截断；UA/请求 ID 含多字节时截断结果字符数只会更少。

// Audit 在业务事务内写 operation_logs（§4.4）。tx 为业务事务或普通 db 句柄，禁止 nil。
// 快照经 jsonRaw 序列化；调用方保证快照内不含密码/Token（architecture.md §6）。
func Audit(tx *gorm.DB, e AuditEntry) error {
	if tx == nil {
		return ErrAuditDBNil
	}
	row := OperationLog{
		RequestID:       truncateStr(e.RequestID, maxRequestIDLen),
		UserID:          database.ID(e.OperatorID),
		Username:        e.OperatorName,
		IP:              truncateStr(e.IP, maxLogIPLen),
		UserAgent:       truncateStr(e.UserAgent, maxLogUserAgentLen),
		Module:          e.Module,
		ObjectType:      e.ObjectType,
		ObjectID:        e.ObjectID,
		Action:          e.Action,
		Method:          e.Method,
		Path:            e.Path,
		Success:         e.Success,
		ErrorCode:       e.ErrorCode,
		RequestSnapshot: marshalJSONRaw(e.Request),
		BeforeSnapshot:  marshalJSONRaw(e.Before),
		AfterSnapshot:   marshalJSONRaw(e.After),
		CreatedAt:       database.Now(),
	}
	return tx.Create(&row).Error
}

// WriteLoginLog 写 login_logs（§4.4：auth 的登录日志经本入口写入）。db 为事务 tx 或普通 db，禁止 nil。
func WriteLoginLog(db *gorm.DB, e LoginLogEntry) error {
	if db == nil {
		return ErrAuditDBNil
	}
	row := LoginLog{
		UserID:     database.ID(e.UserID),
		Username:   e.Username,
		IP:         truncateStr(e.IP, maxLogIPLen),
		UserAgent:  truncateStr(e.UserAgent, maxLogUserAgentLen),
		Success:    e.Success,
		FailReason: e.FailReason,
		CreatedAt:  database.Now(),
	}
	return db.Create(&row).Error
}

// jsonRaw jsonb 列的载体：nil → SQL NULL，其余为已编码 JSON 文档。
type jsonRaw json.RawMessage

// Value 实现 driver.Valuer：pgx 对 jsonb 目标列按原始 JSON 处理 string。
func (j jsonRaw) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

// Scan 支持驱动返回的 []byte/string/nil（预留查询路径使用）。
func (j *jsonRaw) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = jsonRaw(append(json.RawMessage(nil), v...))
	case string:
		*j = jsonRaw(v)
	default:
		return fmt.Errorf("jsonRaw.Scan 不支持类型 %T", src)
	}
	return nil
}

// marshalJSONRaw 将任意值序列化为 jsonRaw；nil → nil（NULL）。
func marshalJSONRaw(v any) jsonRaw {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		// 快照不可序列化：落一个可定位的占位（审计行不可因快照失败而缺位）。
		return jsonRaw(`{"error":"snapshot_marshal_failed"}`)
	}
	return jsonRaw(b)
}
