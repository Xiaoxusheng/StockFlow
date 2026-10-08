package idempotency

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
)

// GORM 模型（迁移 000024 列结构；列名一律显式 column tag——下划线形态禁止依赖
// GORM 蛇形推导，效率层一期计划 §8.11 口径）。

// 幂等键行状态（000024 CHECK 同源）：
//   - PROCESSING 占用中（首次请求执行权持有者尚未收尾）；
//   - COMPLETED 响应快照就绪（同键重放依据）。
const (
	StatusProcessing = "PROCESSING"
	StatusCompleted  = "COMPLETED"
)

// Key idempotency_keys 行。
type Key struct {
	ID       int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Key      string `gorm:"column:key;size:64"`
	UserID   int64  `gorm:"column:user_id"`
	Endpoint string `gorm:"column:endpoint;size:128"`
	// RequestHash 请求体 SHA-256 hex（64 字符）。
	RequestHash string `gorm:"column:request_hash;size:64"`
	// ResponseSnapshot 首次响应快照（jsonb {status, body}；PROCESSING 态为缺省 '{}'）。
	ResponseSnapshot jsonb             `gorm:"column:response_snapshot;type:jsonb"`
	Status           string            `gorm:"column:status;size:16"`
	CreatedAt        database.JSONTime `gorm:"column:created_at"`
	UpdatedAt        database.JSONTime `gorm:"column:updated_at"`
	CreatedBy        int64             `gorm:"column:created_by"`
	UpdatedBy        int64             `gorm:"column:updated_by"`
}

// TableName 显式表名（DDL 契约）。
func (Key) TableName() string { return "idempotency_keys" }

// jsonb json.RawMessage 载体：库内 jsonb（userpref/models.go 同款约定，非第二套机制）。
type jsonb json.RawMessage

// Value 落库值：nil/空切片统一落 '{}'。
//
// 000024 response_snapshot 为 NOT NULL DEFAULT '{}'：首次占用的 PROCESSING 行尚未
// 产生快照，Key.ResponseSnapshot 为 nil——若原样落 NULL 即
// `null value in column "response_snapshot" violates not-null constraint`（SQLSTATE 23502），
// 带 Idempotency-Key 的首请求整事务回滚必现 500（2026-10-07 全流程实测命中
// /api/receipts 与 /api/packing）。此处按列缺省同形落 '{}'（与 masterdata/purchase
// StringList.Value() len==0 落 '[]' 同一口径），不依赖 GORM 的零值跳过。
func (j jsonb) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

func (j *jsonb) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(jsonb(nil), v...)
	case string:
		*j = jsonb(v)
	default:
		return fmt.Errorf("idempotency.jsonb.Scan 不支持类型 %T", src)
	}
	return nil
}

// Snapshot 首次响应快照（response_snapshot jsonb 存储形态）：
//   - Status 首次响应的 HTTP 状态码（成功与业务错误信封均回放）；
//   - Body 首次响应体原文（统一信封 JSON；回放时改写 request_id 为当前请求）。
type Snapshot struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
