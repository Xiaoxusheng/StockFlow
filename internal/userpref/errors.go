package userpref

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// userpref 域错误码。参数类校验失败一律用既有 COMMON_INVALID_PARAM（计划要求
// 400 用 invalidParam，不新发明参数码）；本域仅注册资源语义两枚。
var (
	// ErrViewNotFound 视图不存在（含非本人访问——按 (id, user_id) 查归属，
	// 一律 404 防探测，计划 §2.2）。
	ErrViewNotFound = response.Register("USERPREF_VIEW_NOT_FOUND", "视图不存在", http.StatusNotFound)
	// ErrViewNameConflict 同页签下视图名重复（uk_user_saved_views_name）。
	ErrViewNameConflict = response.Register("USERPREF_VIEW_NAME_CONFLICT", "同名视图已存在", http.StatusConflict)
)
