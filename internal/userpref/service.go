package userpref

import (
	"context"
	"encoding/json"
	"regexp"
	"unicode/utf8"

	"github.com/stockflow/server/internal/response"
)

// 服务层：校验与业务规则（计划 §2.2/§2.3 冻结口径）。
//   - page_key 正则 ^[a-z0-9._-]{1,64}$；三个 json 列合法 JSON 且各 ≤8KB；
//     name ≤64 字符且 (user_id, page_key, name) 唯一（重复 409）；page_size 1-100。
//   - 偏好 key 服务端白名单恰 7 个，白名单外 400 invalidParam；pref_value 合法 JSON
//     且 ≤16KB；recent_visits 服务端裁剪至 20 条。

const (
	// maxJSONBytes 三个视图 json 列的单列字节上限（8KB）。
	maxJSONBytes = 8 * 1024
	// maxPrefValueBytes 偏好值字节上限（16KB）。
	maxPrefValueBytes = 16 * 1024
	// maxNameLen 视图名最大字符数（varchar(64)）。
	maxNameLen = 64
	// defaultPageSize 视图 page_size 缺省值。
	defaultPageSize = 20
	// maxRecentVisits recent_visits 服务端裁剪上限（条数）。
	maxRecentVisits = 20
)

// pageKeyRegexp page_key 值域（与 000020 CHECK 约束同式）。
var pageKeyRegexp = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// prefKeyWhitelist 偏好 key 服务端白名单（恰 7 个，计划 §2.3 / api.md 冻结）。
var prefKeyWhitelist = map[string]bool{
	"default_warehouse_id":   true,
	"last_warehouse_id":      true,
	"last_business_type":     true,
	"last_printer_id":        true,
	"last_print_template_id": true,
	"recent_visits":          true,
	"recent_filters":         true,
}

// WhitelistedPrefKeys 白名单键清单（固定顺序，GET 缺省全量返回用）。
var WhitelistedPrefKeys = []string{
	"default_warehouse_id",
	"last_business_type",
	"last_print_template_id",
	"last_printer_id",
	"last_warehouse_id",
	"recent_filters",
	"recent_visits",
}

// invalidParam 参数校验失败统一错误（details 携带 field/reason）。
func invalidParam(field, reason string) error {
	return response.NewError(response.CodeInvalidParam, map[string]any{
		"field": field, "reason": reason,
	})
}

// validateJSONColumn 视图 json 列校验：合法 JSON 且 ≤8KB（nil 视为未提供）。
func validateJSONColumn(field string, raw []byte) error {
	if raw == nil {
		return nil
	}
	if len(raw) > maxJSONBytes {
		return invalidParam(field, "超出 8KB 上限")
	}
	if !json.Valid(raw) {
		return invalidParam(field, "必须为合法 JSON")
	}
	return nil
}

// ViewInput 视图创建/更新入参（PUT 语义：指针字段 nil = 不修改）。
type ViewInput struct {
	PageKey     string
	Name        *string
	FiltersJSON json.RawMessage
	SortJSON    json.RawMessage
	ColumnsJSON json.RawMessage
	PageSize    *int
	IsDefault   *bool
}

// Service userpref 业务服务。
type Service struct {
	views ViewRepo
	prefs PrefRepo
}

// NewService 装配（RegisterRoutes 唯一调用点）。
func NewService(views ViewRepo, prefs PrefRepo) *Service {
	return &Service{views: views, prefs: prefs}
}

// ListViews 同页签视图列表（page_key 必填并校验格式）。
func (s *Service) ListViews(ctx context.Context, userID int64, pageKey string) ([]SavedView, error) {
	if !pageKeyRegexp.MatchString(pageKey) {
		return nil, invalidParam("page_key", "必须匹配 ^[a-z0-9._-]{1,64}$")
	}
	return s.views.ListByPage(ctx, userID, pageKey)
}

// CreateView 新建视图。is_default=true 时在单事务内清除同页签旧默认
// （部分唯一索引 uk_user_saved_views_default 兜底）。
func (s *Service) CreateView(ctx context.Context, userID int64, in ViewInput) (*SavedView, error) {
	if !pageKeyRegexp.MatchString(in.PageKey) {
		return nil, invalidParam("page_key", "必须匹配 ^[a-z0-9._-]{1,64}$")
	}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	if err := validateName(name); err != nil {
		return nil, err
	}
	if err := validateJSONColumn("filters_json", in.FiltersJSON); err != nil {
		return nil, err
	}
	if err := validateJSONColumn("sort_json", in.SortJSON); err != nil {
		return nil, err
	}
	if err := validateJSONColumn("columns_json", in.ColumnsJSON); err != nil {
		return nil, err
	}
	pageSize := defaultPageSize
	if in.PageSize != nil {
		if *in.PageSize < 1 || *in.PageSize > 100 {
			return nil, invalidParam("page_size", "必须为 1-100")
		}
		pageSize = *in.PageSize
	}
	if dup, err := s.views.NameExists(ctx, userID, in.PageKey, name, 0); err != nil {
		return nil, err
	} else if dup {
		return nil, response.NewError(ErrViewNameConflict, nil)
	}

	v := &SavedView{
		UserID:      userID,
		PageKey:     in.PageKey,
		Name:        name,
		FiltersJSON: defaultJSONObject(in.FiltersJSON),
		SortJSON:    defaultJSONObject(in.SortJSON),
		ColumnsJSON: defaultJSONArray(in.ColumnsJSON),
		PageSize:    pageSize,
	}
	setDefault := in.IsDefault != nil && *in.IsDefault
	err := s.views.InTx(ctx, func(tx ViewRepo) error {
		if setDefault {
			if err := tx.ClearDefault(ctx, userID, in.PageKey, 0); err != nil {
				return err
			}
			v.IsDefault = true
		}
		return tx.Create(ctx, v)
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// UpdateView 更新视图（改名/改内容/is_default）。先按 (id, user_id) 查归属，
// 非本人一律 404 防探测。is_default=false 即「恢复默认」。
func (s *Service) UpdateView(ctx context.Context, userID, id int64, in ViewInput) (*SavedView, error) {
	v, err := s.views.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, response.NewError(ErrViewNotFound, nil)
	}
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return nil, err
		}
	}
	if err := validateJSONColumn("filters_json", in.FiltersJSON); err != nil {
		return nil, err
	}
	if err := validateJSONColumn("sort_json", in.SortJSON); err != nil {
		return nil, err
	}
	if err := validateJSONColumn("columns_json", in.ColumnsJSON); err != nil {
		return nil, err
	}
	if in.PageSize != nil && (*in.PageSize < 1 || *in.PageSize > 100) {
		return nil, invalidParam("page_size", "必须为 1-100")
	}
	if in.Name != nil && *in.Name != v.Name {
		if dup, err := s.views.NameExists(ctx, userID, v.PageKey, *in.Name, v.ID.Int64()); err != nil {
			return nil, err
		} else if dup {
			return nil, response.NewError(ErrViewNameConflict, nil)
		}
	}

	err = s.views.InTx(ctx, func(tx ViewRepo) error {
		if in.Name != nil {
			v.Name = *in.Name
		}
		if in.FiltersJSON != nil {
			v.FiltersJSON = jsonb(in.FiltersJSON)
		}
		if in.SortJSON != nil {
			v.SortJSON = jsonb(in.SortJSON)
		}
		if in.ColumnsJSON != nil {
			v.ColumnsJSON = jsonb(in.ColumnsJSON)
		}
		if in.PageSize != nil {
			v.PageSize = *in.PageSize
		}
		if in.IsDefault != nil {
			if *in.IsDefault {
				// 设为默认：单事务内清除旧默认（排除自身），部分唯一索引兜底。
				if err := tx.ClearDefault(ctx, userID, v.PageKey, v.ID.Int64()); err != nil {
					return err
				}
				v.IsDefault = true
			} else {
				v.IsDefault = false
			}
		}
		return tx.Update(ctx, v)
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// DeleteView 删除视图（非本人一律 404 防探测）。
func (s *Service) DeleteView(ctx context.Context, userID, id int64) error {
	n, err := s.views.Delete(ctx, userID, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return response.NewError(ErrViewNotFound, nil)
	}
	return nil
}

// validateName 视图名：非空且 ≤64 字符（varchar(64) 按字符计）。
func validateName(name string) error {
	if name == "" {
		return invalidParam("name", "不能为空")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return invalidParam("name", "不能超过 64 字符")
	}
	return nil
}

// defaultJSONObject filters/sort 缺省 '{}'。
func defaultJSONObject(raw json.RawMessage) jsonb {
	if raw == nil {
		return jsonb([]byte(`{}`))
	}
	return jsonb(raw)
}

// defaultJSONArray columns 缺省 '[]'（hidden 列键数组，SfTable storageKey 形态）。
func defaultJSONArray(raw json.RawMessage) jsonb {
	if raw == nil {
		return jsonb([]byte(`[]`))
	}
	return jsonb(raw)
}

// ListPreferences 当前用户偏好；keys 为空（或全空白）返回白名单全量，
// 白名单外 key → 400 invalidParam。
func (s *Service) ListPreferences(ctx context.Context, userID int64, keys []string) ([]Preference, error) {
	requested, err := normalizePrefKeys(keys)
	if err != nil {
		return nil, err
	}
	return s.prefs.ListByUser(ctx, userID, requested)
}

// PutPreference 写偏好（复合主键 upsert）。白名单外 400；值必须合法 JSON 且
// ≤16KB；recent_visits 服务端裁剪至 20 条。
func (s *Service) PutPreference(ctx context.Context, userID int64, key string, value json.RawMessage) (*Preference, error) {
	if !prefKeyWhitelist[key] {
		return nil, invalidParam("key", "不在偏好白名单内")
	}
	if len(value) == 0 {
		return nil, invalidParam("value", "不能为空")
	}
	if len(value) > maxPrefValueBytes {
		return nil, invalidParam("value", "超出 16KB 上限")
	}
	if !json.Valid(value) {
		return nil, invalidParam("value", "必须为合法 JSON")
	}
	if key == "recent_visits" {
		value = trimRecentVisits(value)
	}
	p := &Preference{
		UserID:    userID,
		PrefKey:   key,
		PrefValue: jsonb(value),
	}
	if err := s.prefs.Upsert(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// normalizePrefKeys 拆分/去空白/去重并校验白名单；返回 nil 表示取全量。
func normalizePrefKeys(keys []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range keys {
		for _, k := range splitComma(raw) {
			if !prefKeyWhitelist[k] {
				return nil, invalidParam("keys", "不在偏好白名单内: "+k)
			}
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out, nil
}

// splitComma 按逗号拆分并去空白，丢弃空段。
func splitComma(raw string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			seg := trimSpace(raw[start:i])
			if seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// trimRecentVisits recent_visits 为 JSON 数组时服务端裁剪至前 20 条
// （前端写入约定最新在前）；非数组原样保留。
func trimRecentVisits(value json.RawMessage) json.RawMessage {
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil || len(items) <= maxRecentVisits {
		return value
	}
	trimmed, err := json.Marshal(items[:maxRecentVisits])
	if err != nil {
		return value
	}
	return trimmed
}
