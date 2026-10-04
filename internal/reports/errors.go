package reports

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// 报表域错误码（backend-m3-plan §1 错误码清单 REPORT_* 段；经 internal/response 注册）。
var (
	// ErrRangeTooLarge 时间范围超过 366 天上限（plan §9.1 通用约束）。
	ErrRangeTooLarge = response.Register("REPORT_RANGE_TOO_LARGE", "时间范围超出上限（最大 366 天）", http.StatusBadRequest)
)
