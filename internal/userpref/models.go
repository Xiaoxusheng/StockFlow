package userpref

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
)

// GORM 模型（迁移 000020/000021 列结构；列名一律显式 column tag——下划线形态
// 禁止依赖 GORM 蛇形推导，计划 §6 B1 口径）。jsonb 列经 jsonb 载体类型读写
//（printing/models.go 同款约定，非第二套机制）。

// jsonb json.RawMessage 载体：库内 jsonb，对外原样透传 JSON。
type jsonb json.RawMessage

func (j jsonb) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
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
		return fmt.Errorf("userpref.jsonb.Scan 不支持类型 %T", src)
	}
	return nil
}

func (j jsonb) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *jsonb) UnmarshalJSON(b []byte) error {
	*j = append(jsonb(nil), b...)
	return nil
}

// SavedView 用户保存视图（user_saved_views 行）。
type SavedView struct {
	ID          database.ID       `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID      int64             `gorm:"column:user_id" json:"-"`
	PageKey     string            `gorm:"column:page_key" json:"page_key"`
	Name        string            `gorm:"column:name" json:"name"`
	FiltersJSON jsonb             `gorm:"column:filters_json;type:jsonb" json:"filters_json"`
	SortJSON    jsonb             `gorm:"column:sort_json;type:jsonb" json:"sort_json"`
	ColumnsJSON jsonb             `gorm:"column:columns_json;type:jsonb" json:"columns_json"`
	PageSize    int               `gorm:"column:page_size" json:"page_size"`
	IsDefault   bool              `gorm:"column:is_default" json:"is_default"`
	CreatedAt   database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy   int64             `gorm:"column:created_by" json:"-"`
	UpdatedBy   int64             `gorm:"column:updated_by" json:"-"`
}

// TableName 显式表名。
func (SavedView) TableName() string { return "user_saved_views" }

// Preference 用户偏好（user_preferences 行，复合主键 (user_id, pref_key)）。
type Preference struct {
	UserID    int64             `gorm:"column:user_id;primaryKey" json:"-"`
	PrefKey   string            `gorm:"column:pref_key;primaryKey" json:"key"`
	PrefValue jsonb             `gorm:"column:pref_value;type:jsonb" json:"value"`
	CreatedAt database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
}

// TableName 显式表名。
func (Preference) TableName() string { return "user_preferences" }
