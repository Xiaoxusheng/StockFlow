package sysops

import (
	"strconv"
	"strings"
)

// 阈值清单解析（system_configs 统一字符串存储："30,15,7,3" / "30,60,90"）。
// 与 internal/reports 同公式（避免平台包间依赖的本地副本，单测两侧各自断言同语义）：
// 去重降序；含非正数/非法项报错 fail-closed（不静默截断）。
func parsePositiveIntList(raw string) ([]int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	seen := map[int]bool{}
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, errInvalidThresholdItem(p)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, errInvalidThresholdItem(raw)
	}
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] > out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func errInvalidThresholdItem(v string) error {
	return &thresholdError{v}
}

type thresholdError struct{ value string }

func (e *thresholdError) Error() string {
	return "阈值清单含非法项: " + strconv.Quote(e.value)
}
