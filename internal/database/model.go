package database

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ID 业务主键：库内 int64(bigserial)，JSON 序列化为字符串，
// 防 JS Number 2^53 精度丢失（backend-m1-plan §1 全局约定）。
type ID int64

// Int64 还原底层值（Service/Repository 传参用）。
func (i ID) Int64() int64 { return int64(i) }

func (i ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

// UnmarshalJSON 接受字符串与数字两种入参（前端一律传字符串，兼容调试直传数字）。
func (i *ID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null" || s == `""`:
		*i = 0
		return nil
	case strings.HasPrefix(s, `"`):
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("ID 必须是整数字符串: %w", err)
	}
	*i = ID(n)
	return nil
}

// JSONTime 统一时间序列化 YYYY-MM-DD HH:mm:ss（api.md §2）；零值序列化为 null。
// 实现 GormDataType("time") + Valuer/Scanner，GORM 的 autoCreateTime/autoUpdateTime
// 与自动填充照常生效。
type JSONTime struct{ time.Time }

// Now 当前时间（业务代码统一入口，便于测试替换）。
func Now() JSONTime { return JSONTime{Time: time.Now()} }

// timeLayout 对外/对库的统一时间格式（api.md §2）。
const timeLayout = "2006-01-02 15:04:05"

func (t JSONTime) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.Format(timeLayout) + `"`), nil
}

// UnmarshalJSON 接受 "YYYY-MM-DD HH:mm:ss"、RFC3339 与 "YYYY-MM-DD"。
func (t *JSONTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "null" || s == "" {
		t.Time = time.Time{}
		return nil
	}
	return t.parseString(s)
}

// GormDataType 声明为 time 类型，GORM 按时间列维护（自动填充/类型推导）。
func (JSONTime) GormDataType() string { return "time" }

// Value 零值写 NULL（配合可空列；业务必填列由 schema NOT NULL 约束）。
func (t JSONTime) Value() (driver.Value, error) {
	if t.IsZero() {
		return nil, nil
	}
	return t.Time, nil
}

// Scan 支持驱动返回的 time.Time / string / []byte / nil。
func (t *JSONTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		t.Time = time.Time{}
	case time.Time:
		t.Time = v
	case []byte:
		return t.parseString(strings.TrimSpace(string(v)))
	case string:
		return t.parseString(strings.TrimSpace(v))
	default:
		return fmt.Errorf("JSONTime.Scan 不支持类型 %T", src)
	}
	return nil
}

func (t *JSONTime) parseString(s string) error {
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	for _, layout := range []string{timeLayout, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("时间格式必须为 %s 或 RFC3339: %q", timeLayout, s)
}

// BaseModel 业务表通用字段（database.md §3）：
// id / created_at / updated_at / created_by / updated_by / deleted_at。
// 纯日志表（operation_logs/login_logs）按 §3 但书不用全量字段，可不嵌入；
// 软删除对象清单见 database.md §5.1（products/skus/suppliers/customers/warehouses/bins/users）。
type BaseModel struct {
	ID        ID             `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt JSONTime       `json:"created_at"`
	UpdatedAt JSONTime       `json:"updated_at"`
	CreatedBy ID             `json:"created_by"` // 业务侧显式赋值；系统操作为 0
	UpdatedBy ID             `json:"updated_by"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"` // 软删除标记，不出现在 API 响应
}

// BeforeCreate 兜底填充创建/更新时间（GORM autoCreateTime 之外的双保险）。
func (b *BaseModel) BeforeCreate(tx *gorm.DB) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = Now()
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.CreatedAt
	}
	return nil
}

// BeforeUpdate 兜底刷新更新时间（结构体更新路径；map 更新路径由 autoUpdateTime 覆盖）。
func (b *BaseModel) BeforeUpdate(tx *gorm.DB) error {
	b.UpdatedAt = Now()
	return nil
}

// Tx 事务助手：fn 内所有操作共用同一事务，返回错误整体回滚（architecture.md §4）。
// 跨域/多表写（如“业务单据 + 库存变更 + 操作日志”）必须在同一 Tx 内完成。
func Tx(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(fn)
}
