package database

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIDMarshalJSONString(t *testing.T) {
	// 2^53+1：JS Number 无法精确表示，必须序列化为字符串（backend-m1-plan §1）
	b, err := json.Marshal(ID(9007199254740993))
	require.NoError(t, err)
	require.Equal(t, `"9007199254740993"`, string(b))

	b, err = json.Marshal(ID(0))
	require.NoError(t, err)
	require.Equal(t, `"0"`, string(b))
}

func TestIDUnmarshalJSON(t *testing.T) {
	var id ID
	require.NoError(t, json.Unmarshal([]byte(`"42"`), &id))
	require.Equal(t, ID(42), id)

	require.NoError(t, json.Unmarshal([]byte(`42`), &id)) // 兼容数字
	require.Equal(t, ID(42), id)

	require.NoError(t, json.Unmarshal([]byte(`null`), &id))
	require.Equal(t, ID(0), id)

	require.Error(t, json.Unmarshal([]byte(`"abc"`), &id))
}

func TestJSONTimeMarshalFormat(t *testing.T) {
	// api.md §2：时间格式 YYYY-MM-DD HH:mm:ss
	tm := time.Date(2026, 10, 2, 8, 9, 10, 0, time.Local)
	b, err := json.Marshal(JSONTime{Time: tm})
	require.NoError(t, err)
	require.Equal(t, `"2026-10-02 08:09:10"`, string(b))
}

func TestJSONTimeZeroMarshalsNull(t *testing.T) {
	b, err := json.Marshal(JSONTime{})
	require.NoError(t, err)
	require.Equal(t, "null", string(b))
}

func TestJSONTimeUnmarshal(t *testing.T) {
	var jt JSONTime
	require.NoError(t, json.Unmarshal([]byte(`"2026-01-02 03:04:05"`), &jt))
	require.Equal(t, "2026-01-02 03:04:05", jt.Format(timeLayout))

	require.NoError(t, json.Unmarshal([]byte(`"2026-01-02T03:04:05Z"`), &jt)) // RFC3339
	require.Equal(t, "2026-01-02", jt.Format("2006-01-02"))

	require.NoError(t, json.Unmarshal([]byte(`"2026-01-02"`), &jt))

	require.NoError(t, json.Unmarshal([]byte(`null`), &jt))
	require.True(t, jt.IsZero())

	require.Error(t, json.Unmarshal([]byte(`"02/03/2026"`), &jt))
}

func TestJSONTimeScanAndValue(t *testing.T) {
	var jt JSONTime
	require.NoError(t, jt.Scan("2026-01-02 03:04:05"))
	require.False(t, jt.IsZero())
	require.NoError(t, jt.Scan([]byte("2026-01-02 03:04:05")))
	require.NoError(t, jt.Scan(time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)))

	v, err := jt.Value()
	require.NoError(t, err)
	require.IsType(t, time.Time{}, v)

	require.NoError(t, jt.Scan(nil)) // 零值 → Value 写 NULL
	require.True(t, jt.IsZero())
	v, err = (JSONTime{}).Value()
	require.NoError(t, err)
	require.Nil(t, v)

	require.Error(t, jt.Scan(123)) // 不支持的类型
}

func TestBaseModelBeforeCreateFillsTimestamps(t *testing.T) {
	m := &BaseModel{}
	require.NoError(t, m.BeforeCreate(nil))
	require.False(t, m.CreatedAt.IsZero())
	require.Equal(t, m.CreatedAt.Time, m.UpdatedAt.Time)
}

func TestBaseModelBeforeUpdateRefreshesTimestamp(t *testing.T) {
	m := &BaseModel{CreatedAt: JSONTime{Time: time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)}}
	m.UpdatedAt = m.CreatedAt
	require.NoError(t, m.BeforeUpdate(nil))
	require.True(t, m.UpdatedAt.After(m.CreatedAt.Time))
}

func TestBaseModelJSONShape(t *testing.T) {
	b, err := json.Marshal(&BaseModel{ID: 7})
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	// 通用字段（database.md §3）+ ID 字符串化；deleted_at 不出 API
	require.Contains(t, m, "id")
	require.Contains(t, m, "created_at")
	require.Contains(t, m, "updated_at")
	require.Contains(t, m, "created_by")
	require.Contains(t, m, "updated_by")
	require.NotContains(t, m, "deleted_at")
	require.Equal(t, "7", m["id"])
}
