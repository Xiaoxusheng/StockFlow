package reports

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Service 报表与智能能力（只读；Handler→Service→Repository 分层，architecture §1）。
// 数据权限 Scope 由 handler 自 auth.WarehouseScope 注入（plan §7.4：禁止接受前端
// 传入的仓库范围参数决定数据可见性）。
type Service struct {
	db   *gorm.DB
	repo *repository
	// now 可注入时钟（单测确定性）。
	now func() time.Time
	// replenishmentParams 补货参数读取（system_configs 运行时可调，缺省 7/3——plan §9.3/§10.2）。
	replenishmentParams func(ctx context.Context) (leadDays, bufferDays int, err error)
	// alertThresholds 预警阈值读取（eff: 效期天数清单；stagnant: 积压天数清单）。
	alertThresholds func(ctx context.Context) (expiryDaysList []int, stagnantDaysList []int, err error)
}

// Option Service 装配项（router 唯一装配点）。
type Option func(*Service)

// WithClock 注入时钟（单测）。
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithReplenishmentParams 注入补货参数读取（缺省固定 7/3 默认值）。
func WithReplenishmentParams(fn func(ctx context.Context) (int, int, error)) Option {
	return func(s *Service) { s.replenishmentParams = fn }
}

// WithAlertThresholds 注入预警阈值读取（缺省固定 30,15,7,3 / 30,60,90 默认值）。
func WithAlertThresholds(fn func(ctx context.Context) ([]int, []int, error)) Option {
	return func(s *Service) { s.alertThresholds = fn }
}

// NewService 构造报表 Service（db 为 nil 视为装配错误，由 RegisterRoutes fail-fast）。
func NewService(db *gorm.DB, opts ...Option) *Service {
	s := &Service{
		db:   db,
		repo: &repository{db: db},
		now:  time.Now,
		replenishmentParams: func(context.Context) (int, int, error) {
			return defaultLeadTimeDays, defaultBufferDays, nil
		},
		alertThresholds: func(context.Context) ([]int, []int, error) {
			expiry, _ := parsePositiveIntList(defaultExpiryDays)
			stagnant, _ := parsePositiveIntList(defaultStagnantDays)
			return expiry, stagnant, nil
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// M3 缺省阈值（backend-m3-plan §10.2 seed 键缺省值；system_configs 行存在时优先）。
const (
	defaultLeadTimeDays = 7
	defaultBufferDays   = 3
	defaultExpiryDays   = "30,15,7,3"
	defaultStagnantDays = "30,60,90"
	defaultSalesWindow  = 30 // 日均销量窗口（天）
	defaultExpiryWindow = 30 // Dashboard 临期窗口（天）
)

// Catalog 报表目录（代码内冻结注册表）。
func (s *Service) Catalog() []reportCatalogItem { return Catalog() }

// InventorySummary 库存汇总（按仓/SKU 分页）。
func (s *Service) InventorySummary(ctx context.Context, sc Scope, warehouseID, skUID int64, page, pageSize int) ([]InventorySummaryRow, int64, error) {
	rows, total, err := s.repo.inventorySummary(ctx, sc, warehouseID, skUID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].TotalQty = roundQty(rows[i].TotalQty)
		rows[i].AvailableQty = roundQty(rows[i].AvailableQty)
		rows[i].LockedQty = roundQty(rows[i].LockedQty)
		rows[i].FrozenQty = roundQty(rows[i].FrozenQty)
		rows[i].PendingInspectQty = roundQty(rows[i].PendingInspectQty)
		rows[i].DefectiveQty = roundQty(rows[i].DefectiveQty)
		rows[i].StockValue = roundQty(rows[i].StockValue)
	}
	return rows, total, nil
}

// InboundStats 入库统计（口径：INBOUND 流水落账；金额按 SKU 成本价估值）。
func (s *Service) InboundStats(ctx context.Context, sc Scope, from, to time.Time, page, pageSize int) ([]FlowStatRow, int64, error) {
	return s.repo.flowStats(ctx, sc, "INBOUND", "cost_price", from, to, page, pageSize)
}

// OutboundStats 出库统计（口径：OUTBOUND 流水落账；金额按 SKU 销售价估值）。
func (s *Service) OutboundStats(ctx context.Context, sc Scope, from, to time.Time, page, pageSize int) ([]FlowStatRow, int64, error) {
	return s.repo.flowStats(ctx, sc, "OUTBOUND", "sale_price", from, to, page, pageSize)
}

// InventoryTurnover 库存周转（plan §9.1：出库量/平均库存，按仓/SKU）。
// 平均库存 =（期初 + 期末）/2，期初由流水净变化重构；比率/天数为 Service 层纯函数计算。
func (s *Service) InventoryTurnover(ctx context.Context, sc Scope, from, to time.Time, page, pageSize int) ([]TurnoverRow, int64, error) {
	rows, total, err := s.repo.turnover(ctx, sc, from, to, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	periodDays := to.Sub(from).Hours() / 24
	if periodDays <= 0 {
		periodDays = 1
	}
	for i := range rows {
		rows[i].EndQty = roundQty(rows[i].EndQty)
		rows[i].StartQty = roundQty(rows[i].StartQty)
		rows[i].AvgInventory = roundQty((rows[i].StartQty + rows[i].EndQty) / 2)
		rows[i].OutboundQty = roundQty(rows[i].OutboundQty)
		rate, days := turnoverRate(rows[i].OutboundQty, rows[i].AvgInventory, periodDays)
		rows[i].TurnoverRate = rate
		rows[i].TurnoverDays = days
	}
	return rows, total, nil
}

// StagnantStock 积压识别（30/60/90 天未动清单，inventory-rules §11；分档阈值
// inventory.alert.stagnant_days 可配置）。
func (s *Service) StagnantStock(ctx context.Context, sc Scope, page, pageSize int) ([]StagnantRow, int64, error) {
	_, tiers, err := s.alertThresholds(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("reports: 积压阈值读取失败: %w", err)
	}
	minTier := tiers[len(tiers)-1] // parsePositiveIntList 降序，末位最小
	cutoff := s.now().AddDate(0, 0, -minTier)
	rows, total, err := s.repo.stagnant(ctx, sc, cutoff, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	now := s.now()
	out := rows[:0]
	for _, r := range rows {
		idleDays := int(now.Sub(r.LastMovedAt).Hours() / 24)
		tier := stagnantTier(idleDays, tiers)
		if tier == "" {
			continue // 时间边界内的行（cutoff 为最小档位边界，防御性过滤）
		}
		r.IdleDays = idleDays
		r.Tier = tier
		r.TotalQty = roundQty(r.TotalQty)
		out = append(out, r)
	}
	return out, total, nil
}

// ReplenishmentSuggestion 补货建议行（含人读计算依据，inventory-rules §11 可追溯）。
type ReplenishmentSuggestion struct {
	ReplenishmentRow
	// LeadTimeDays/BufferDays 依据参数（insight.replenishment.*）。
	LeadTimeDays int `json:"lead_time_days"`
	BufferDays   int `json:"buffer_days"`
	// BasisText 计算依据说明（公式 + 各输入值，人读）。
	BasisText string `json:"basis_text"`
}

// ReplenishmentSuggestions 智能补货建议（只读；不自动生成任何单据、不改任何库存——
// inventory-rules §11 红线）。公式：建议量 = max(0, max(安全库存, 日均销量×(采购周期+缓冲)) − 可用 − 在途)。
func (s *Service) ReplenishmentSuggestions(ctx context.Context, sc Scope, onlyShortage bool, page, pageSize int) ([]ReplenishmentSuggestion, int64, error) {
	leadDays, bufferDays, err := s.replenishmentParams(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("reports: 补货参数读取失败: %w", err)
	}
	if leadDays < 0 || bufferDays < 0 {
		return nil, 0, fmt.Errorf("reports: 补货参数非法（lead=%d buffer=%d）", leadDays, bufferDays)
	}
	salesFrom := s.now().AddDate(0, 0, -defaultSalesWindow)
	rows, total, err := s.repo.replenishment(ctx, sc, salesFrom, leadDays+bufferDays, onlyShortage, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	out := make([]ReplenishmentSuggestion, 0, len(rows))
	for _, r := range rows {
		basis := calcReplenishment(r.CurrentAvailable, r.SafetyStock, r.DailyAvgSales, r.IncomingQty, leadDays, bufferDays)
		sug := ReplenishmentSuggestion{
			ReplenishmentRow: r,
			LeadTimeDays:     leadDays,
			BufferDays:       bufferDays,
		}
		// SQL 与 Go 同公式双算：以纯函数结果为准（roundQty 归一浮点尾差）。
		sug.TargetStock = basis.TargetStock
		sug.SuggestedQty = basis.SuggestedQty
		sug.DailyAvgSales = basis.DailyAvgSales
		sug.CurrentAvailable = basis.CurrentAvailable
		sug.IncomingQty = basis.IncomingQty
		sug.SafetyStock = basis.SafetyStock
		sug.BasisText = fmt.Sprintf(
			"日均销量 %.4f（近 %d 天 OUTBOUND 流水/%d 天）×（采购周期 %d 天 + 缓冲 %d 天）=%.4f，与安全库存 %.4f 取大 = 目标库存 %.4f；"+
				"减可用库存 %.4f、在途 %.4f（采购在途+调拨在途）= 建议补货 %.4f",
			basis.DailyAvgSales, defaultSalesWindow, defaultSalesWindow, leadDays, bufferDays,
			basis.DailyAvgSales*float64(leadDays+bufferDays), basis.SafetyStock, basis.TargetStock,
			basis.CurrentAvailable, basis.IncomingQty, basis.SuggestedQty)
		out = append(out, sug)
	}
	return out, total, nil
}

// DashboardSummary 库存总览（GET /api/inventory/summary）。
func (s *Service) DashboardSummary(ctx context.Context, sc Scope) (*DashboardSummary, error) {
	expiryDays := defaultExpiryWindow
	if list, _, err := s.alertThresholds(ctx); err == nil && len(list) > 0 && list[0] > 0 {
		expiryDays = list[0] // 效期阈值最大档位（降序首位）为临期窗口
	}
	row, err := s.repo.dashboardSummary(ctx, sc, expiryDays)
	if err != nil {
		return nil, err
	}
	row.TotalQty = roundQty(row.TotalQty)
	row.AvailableQty = roundQty(row.AvailableQty)
	row.LockedQty = roundQty(row.LockedQty)
	row.FrozenQty = roundQty(row.FrozenQty)
	row.NearExpiryQty = roundQty(row.NearExpiryQty)
	row.AbnormalQty = roundQty(row.AbnormalQty)
	return row, nil
}

// DashboardAlerts 库存预警（GET /api/inventory/alerts；Message 携计算依据）。
func (s *Service) DashboardAlerts(ctx context.Context, sc Scope, level, keyword string, page, pageSize int) ([]AlertItem, int64, error) {
	expiryList, stagnantList, err := s.alertThresholds(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("reports: 预警阈值读取失败: %w", err)
	}
	minStagnant := stagnantList[len(stagnantList)-1]
	rows, total, err := s.repo.alerts(ctx, sc, level, keyword, expiryList[0], minStagnant, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	now := s.now()
	for i := range rows {
		rows[i].CurrentQty = roundQty(rows[i].CurrentQty)
		rows[i].Threshold = roundQty(rows[i].Threshold)
		rows[i].Message = alertMessage(rows[i], now)
	}
	return rows, total, nil
}

// alertMessage 组装预警说明（携计算依据；不引入任何前端猜测）。
func alertMessage(a AlertItem, now time.Time) string {
	switch a.Level {
	case "low_stock":
		return fmt.Sprintf("可用库存 %.4f 已低于等于安全库存 %.4f", a.CurrentQty, a.Threshold)
	case "overstock":
		return fmt.Sprintf("库存总量 %.4f 已超过最大库存 %.4f", a.CurrentQty, a.Threshold)
	case "near_expiry":
		return fmt.Sprintf("批次 %s 距到期还有 %s 天，现存量 %.4f", a.BatchNo, trimFloat(a.Threshold), a.CurrentQty)
	case "expired":
		return fmt.Sprintf("批次 %s 已过期，现存量 %.4f（需按过期品流程处置）", a.BatchNo, a.CurrentQty)
	case "slow_moving":
		idle := 0
		if a.LastMovedAt != nil {
			idle = int(now.Sub(*a.LastMovedAt).Hours() / 24)
		}
		return fmt.Sprintf("已 %d 天未发生移动，现存量 %.4f（阈值 %s 天）", idle, a.CurrentQty, trimFloat(a.Threshold))
	default:
		return ""
	}
}

// trimFloat 阈值/天数的人读格式化（整数不带小数尾巴）。
func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.4f", v)
}
