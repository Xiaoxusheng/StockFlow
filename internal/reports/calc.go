package reports

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// 纯计算函数（无 IO，单测直测）：报表聚合边界、积压分档、补货公式——
// backend-m3-plan §14（reports/sysops：补货建议公式与依据字段、reader 替身注入边界值）。

// maxRangeDays 报表时间范围上限（plan §9.1：时间范围上限 366 天）。
const maxRangeDays = 366

// roundQty 数量/金额按 numeric(18,4) 标度取整（与库存数量精度同源，inventory/aliases.go）。
func roundQty(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// validateRange 校验并规范化时间范围：缺省 to=now；from 必填或缺省 30 天窗口；
// 范围超 366 天或 from > to 报错（plan §9.1 REPORT_RANGE_TOO_LARGE）。
func validateRange(from, to *time.Time, now time.Time) (time.Time, time.Time, error) {
	end := now
	if to != nil {
		end = *to
	}
	start := end.AddDate(0, 0, -30) // 缺省近 30 天
	if from != nil {
		start = *from
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("time_from 晚于 time_to")
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("时间范围超过 %d 天上限", maxRangeDays)
	}
	return start, end, nil
}

// parseDay 解析日期/时间参数：YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss（api.md §2 时间格式）。
func parseDay(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("时间格式非法: %q（应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss）", raw)
}

// parsePositiveIntList 解析逗号分隔正整数阈值清单（system_configs 统一字符串存储：
// 如 "30,15,7,3"），去重降序返回；含非正数/非法项报错（fail-closed，不静默截断）。
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
			return nil, fmt.Errorf("阈值清单含非法项: %q", p)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("阈值清单为空")
	}
	// 降序（大→小），供档位匹配从大到小判定。
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] > out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// stagnantTier 积压分档（inventory-rules §11：30/60/90 天未动；plan §9.3 档位口径）：
// 未动天数 idleDays 命中的最大阈值为档位（"90"/"60"/"30"…），未达最小阈值返回空串。
// thresholds 降序（parsePositiveIntList 保证）。
func stagnantTier(idleDays int, thresholds []int) string {
	for _, t := range thresholds {
		if idleDays >= t {
			return strconv.Itoa(t)
		}
	}
	return ""
}

// replenishmentBasis 补货建议计算依据与结果（plan §9.3 公式；inventory-rules §11：
// 建议必须可追溯计算依据，且不自动改变任何库存与单据状态）。
//
//	建议补货量 = max(0, 目标库存 − 可用库存 − 在途)
//	目标库存   = max(安全库存, 日均销量 × (采购周期天数 + 安全缓冲天数))
type replenishmentBasis struct {
	DailyAvgSales    float64 // 近 30 天 OUTBOUND 流水量 / 30（日均销量）
	SafetyStock      float64 // 安全库存（skus.safety_stock）
	LeadTimeDays     int     // 采购周期天数（insight.replenishment.lead_time_days）
	BufferDays       int     // 安全缓冲天数（insight.replenishment.buffer_days）
	IncomingQty      float64 // 在途 = 采购在途 + 调拨在途
	CurrentAvailable float64 // 当前可用库存
	TargetStock      float64 // 目标库存（计算得出）
	SuggestedQty     float64 // 建议补货量（计算得出，≥0）
}

// calcReplenishment 补货建议纯计算（边界：负日均按 0；可用/在途负值按 0 归一；
// 结果四舍五入到 numeric(18,4) 标度）。basisText 为人读依据说明。
func calcReplenishment(available, safetyStock, dailyAvgSales, incoming float64, leadDays, bufferDays int) replenishmentBasis {
	if available < 0 {
		available = 0
	}
	if dailyAvgSales < 0 {
		dailyAvgSales = 0
	}
	if incoming < 0 {
		incoming = 0
	}
	demandWindow := float64(leadDays+bufferDays) * dailyAvgSales
	target := math.Max(safetyStock, demandWindow)
	suggested := math.Max(0, target-available-incoming)
	return replenishmentBasis{
		DailyAvgSales:    roundQty(dailyAvgSales),
		SafetyStock:      roundQty(safetyStock),
		LeadTimeDays:     leadDays,
		BufferDays:       bufferDays,
		IncomingQty:      roundQty(incoming),
		CurrentAvailable: roundQty(available),
		TargetStock:      roundQty(target),
		SuggestedQty:     roundQty(suggested),
	}
}

// turnoverRate 库存周转率/周转天数纯计算（plan §9.1：出库量/平均库存）。
// 平均库存 ≤ 0（期初/期末均为空仓或倒挂）时周转率记 0、周转天数记 0——不造假分母。
func turnoverRate(outboundQty, avgInventory, periodDays float64) (rate, days float64) {
	if avgInventory <= 0 || outboundQty <= 0 {
		return 0, 0
	}
	rate = outboundQty / avgInventory
	days = 0
	if rate > 0 && periodDays > 0 {
		days = periodDays / rate
	}
	return roundQty(rate), roundQty(days)
}
