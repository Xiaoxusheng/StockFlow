package sysops

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 预警扫描任务（backend-m3-plan §4.3 注册表；inventory-rules §7/§11 阈值口径）：
//   - inventory_low_stock_scan  available ≤ safety_stock 行 → 通知（dedup=日+SKU+仓+收件人）
//   - inventory_expiry_scan     临期 30/15/7/3 天 + 已过期（阈值 system_configs）→ 通知
//   - inventory_stagnant_scan   30/60/90 天未动 → 通知（dedup=日+SKU+仓+档位+收件人）
//
// 幂等：dedup_key = 对象键:{user_id} 复合收件人（plan §4.3），uk_notifications_dedup
// 部分唯一索引 + ON CONFLICT DO NOTHING——同日重复扫描/重投递不重复通知。
// 数据面：库存族只读 SELECT（零写），通知写 notifications（白名单自有表）。
// 阈值真相源 system_configs（ensureSystemConfigKeys 幂等补行），行缺失回退代码缺省值。

// M3 缺省阈值（与 configs.go configSeeds 同源；行缺失时回退，不静默空转）。
const (
	cfgKeyExpiryDays      = "inventory.alert.expiry_days"
	defExpiryDays         = "30,15,7,3"
	cfgKeyLowStockEnabled = "inventory.alert.low_stock_enabled"
	defLowStockEnabled    = "true"
	cfgKeyStagnantDays    = "inventory.alert.stagnant_days"
	defStagnantDays       = "30,60,90"
)

// scanRunner 扫描执行体（db 绑定的闭包集合，wiring.go 注册到 cron 注册表）。
type scanRunner struct {
	db  *gorm.DB
	log *zap.Logger
	now func() time.Time
}

// dayKey dedup 对象键的日期段（本地时区 YYYYMMDD——同日同对象同收件人只通知一次）。
func dayKey(t time.Time) string {
	return t.Format("20060102")
}

// fmtF4 数量人读格式（numeric(18,4) 标度）。
func fmtF4(v float64) string {
	return fmt.Sprintf("%.4f", v)
}

// cfgReader 阈值配置读取（scanRunner 内聚，避免每次构造 runtimeRepo）。
func (s scanRunner) configValue(ctx context.Context, key, def string) (string, error) {
	v, _, err := (&runtimeRepo{db: s.db}).configValue(ctx, key, def)
	return v, err
}

// ---- 低库存扫描（inventory_low_stock_scan，*/10 * * * *）----

// lowStockRow 低库存行（按仓+SKU 聚合 available 与安全库存比较）。
// SKUID 显式 tag：gorm 默认把 SKUID 映射为 sk_uid（≠ 表列 sku_id），扫描静默落零值。
type lowStockRow struct {
	WarehouseID   int64   `gorm:"column:warehouse_id"`
	WarehouseCode string  `gorm:"column:warehouse_code"`
	WarehouseName string  `gorm:"column:warehouse_name"`
	SKUID         int64   `gorm:"column:sku_id"`
	SKUCode       string  `gorm:"column:sku_code"`
	SKUName       string  `gorm:"column:sku_name"`
	Available     float64 `gorm:"column:available"`
	SafetyStock   float64 `gorm:"column:safety_stock"`
}

// lowStockRows 只读聚合：available ≤ safety_stock 的仓+SKU 行
// （inventory 按仓+SKU 聚合；safety_stock 为 SKU 阈值列，零值不预警）。
func (s scanRunner) lowStockRows(ctx context.Context) ([]lowStockRow, error) {
	var rows []lowStockRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT t.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
		       t.sku_id, sk.code AS sku_code, p.name AS sku_name,
		       t.available_qty::float8 AS available, sk.safety_stock::float8 AS safety_stock
		FROM (
		    SELECT i.warehouse_id, i.sku_id, SUM(i.available_qty) AS available_qty
		    FROM inventory i
		    GROUP BY 1, 2
		) t
		JOIN skus sk ON sk.id = t.sku_id AND sk.safety_stock > 0
		JOIN products p ON p.id = sk.product_id
		JOIN warehouses w ON w.id = t.warehouse_id
		WHERE t.available_qty <= sk.safety_stock
		ORDER BY t.warehouse_id, t.sku_id`).Scan(&rows).Error
	return rows, err
}

func (s scanRunner) lowStockScan(ctx context.Context) error {
	raw, err := s.configValue(ctx, cfgKeyLowStockEnabled, defLowStockEnabled)
	if err != nil {
		return fmt.Errorf("读取低库存扫描开关失败: %w", err)
	}
	enabled, err := parseBoolConfig(raw, cfgKeyLowStockEnabled)
	if err != nil {
		return err
	}
	if !enabled {
		s.log.Info("低库存扫描已被配置停用（inventory.alert.low_stock_enabled=false），本轮空转")
		return nil
	}
	rows, err := s.lowStockRows(ctx)
	if err != nil {
		return fmt.Errorf("低库存行聚合失败: %w", err)
	}
	day := dayKey(s.now())
	// 收件人集合每轮扫描恒定：循环外解析一次，循环内逐对象落库（防 N+1——
	// notifyRoles 会逐行重复执行相同的角色→用户查询）。
	recipients, err := resolveRecipients(ctx, s.db, alertRecipientRoles)
	if err != nil {
		return fmt.Errorf("低库存收件人解析失败: %w", err)
	}
	var notified int64
	for _, r := range rows {
		n, err := notifyRecipients(ctx, gormNotifyStore{db: s.db}, notifyInput{
			Type:  NotifyTypeStockAlert,
			Title: fmt.Sprintf("低库存预警：%s（%s）", r.SKUName, r.SKUCode),
			Content: fmt.Sprintf(
				"仓库 %s（%s）SKU %s（%s）可用库存 %s 已低于等于安全库存 %s，请及时补货。",
				r.WarehouseName, r.WarehouseCode, r.SKUName, r.SKUCode,
				fmtF4(r.Available), fmtF4(r.SafetyStock)),
			DedupKey: fmt.Sprintf("lowstock:%s:%s:%s", day, r.SKUCode, r.WarehouseCode),
		}, recipients)
		if err != nil {
			return fmt.Errorf("低库存通知写入失败（sku=%s wh=%s）: %w", r.SKUCode, r.WarehouseCode, err)
		}
		notified += n
	}
	s.log.Info("低库存扫描完成",
		zap.Int("alert_rows", len(rows)),
		zap.Int64("notified", notified),
		zap.Int64("deduped", int64(len(rows))*int64(len(alertRecipientRoles))-notified))
	return nil
}

// ---- 效期预警扫描（inventory_expiry_scan，0 6 * * *；inventory-rules §7.1 阈值口径）----

// expiryBatchRow 临期/过期批次行（批次为 SKU 维度台账，现存量按 inventory 行聚合）。
type expiryBatchRow struct {
	SKUID      int64     `gorm:"column:sku_id"`
	SKUCode    string    `gorm:"column:sku_code"`
	SKUName    string    `gorm:"column:sku_name"`
	BatchNo    string    `gorm:"column:batch_no"`
	ExpiryDate time.Time `gorm:"column:expiry_date"`
	DaysLeft   int       // 剩余效期天数（≤0 已过期）
	Threshold  int       // 命中的预警档位（30/15/7/3；已过期记 0）
	Level      string    // "expired" 或 "30"/"15"/"7"/"3"
	TotalQty   float64   `gorm:"column:total_qty"` // 该批次现存量（全仓合计，>0 才预警）
	Warehouses string    `gorm:"column:warehouses"` // 现存仓库编码清单（去重，供人读内容）
}

// expiryThresholds 阈值清单（system_configs inventory.alert.expiry_days，降序）。
func (s scanRunner) expiryThresholds(ctx context.Context) ([]int, error) {
	raw, err := s.configValue(ctx, cfgKeyExpiryDays, defExpiryDays)
	if err != nil {
		return nil, fmt.Errorf("读取效期阈值失败: %w", err)
	}
	return parsePositiveIntList(raw)
}

// expiryBatchRows 只读聚合：有现存量的临期/已过期批次（效期档位在 Go 侧按阈值清单判定，
// 与 /api/inventory/alerts 效期分支同口径）。
func (s scanRunner) expiryBatchRows(ctx context.Context) ([]expiryBatchRow, error) {
	var rows []expiryBatchRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT sk.id AS sku_id, sk.code AS sku_code, p.name AS sku_name,
		       b.batch_no, b.expiry_date,
		       COALESCE(SUM(i.total_qty), 0)::float8 AS total_qty,
		       COALESCE(string_agg(DISTINCT w.code, ','), '') AS warehouses
		FROM batches b
		JOIN skus sk ON sk.id = b.sku_id
		JOIN products p ON p.id = sk.product_id
		JOIN inventory i ON i.batch_id = b.id AND i.batch_id > 0
		JOIN warehouses w ON w.id = i.warehouse_id
		GROUP BY sk.id, sk.code, p.name, b.batch_no, b.expiry_date
		HAVING COALESCE(SUM(i.total_qty), 0) > 0
		   AND b.expiry_date IS NOT NULL
		   AND b.expiry_date <= CURRENT_DATE + ?::interval
		ORDER BY b.expiry_date, sk.code`, "366 days").Scan(&rows).Error
	return rows, err
}

// expiryLevel 档位判定（阈值清单；daysLeft ≤ 0 → 已过期；命中"不小于剩余天数的最小
// 档位"——剩余 1 天属 3 天档而非 30 天档；超出最大档位 → 不预警返回空）。
func (s scanRunner) expiryLevel(daysLeft int, thresholds []int) string {
	if daysLeft <= 0 {
		return "expired"
	}
	best := 0
	for _, t := range thresholds {
		if daysLeft <= t && (best == 0 || t < best) {
			best = t
		}
	}
	if best == 0 {
		return ""
	}
	return fmt.Sprintf("%d", best)
}

func (s scanRunner) expiryScan(ctx context.Context) error {
	thresholds, err := s.expiryThresholds(ctx)
	if err != nil {
		return err
	}
	rows, err := s.expiryBatchRows(ctx)
	if err != nil {
		return fmt.Errorf("临期批次聚合失败: %w", err)
	}
	today := dateAtLocal(s.now())
	day := dayKey(today)
	// 收件人集合每轮扫描恒定：循环外解析一次（防 N+1）。
	recipients, err := resolveRecipients(ctx, s.db, alertRecipientRoles)
	if err != nil {
		return fmt.Errorf("效期收件人解析失败: %w", err)
	}
	var notified int64
	alerted := 0
	for _, r := range rows {
		daysLeft := int(dateAtLocal(r.ExpiryDate).Sub(today).Hours() / 24)
		level := s.expiryLevel(daysLeft, thresholds)
		if level == "" {
			continue
		}
		alerted++
		dedupKey := fmt.Sprintf("expiry:%s:%s:%s", day, r.BatchNo, level)
		title := fmt.Sprintf("效期预警：%s（%s）批次 %s", r.SKUName, r.SKUCode, r.BatchNo)
		content := fmt.Sprintf(
			"批次 %s（SKU %s %s）效期至 %s，%s，现存量 %s（仓库：%s），请按 FEFO 优先出库或处置。",
			r.BatchNo, r.SKUCode, r.SKUName, r.ExpiryDate.Format("2006-01-02"),
			expiryLevelText(level, daysLeft), fmtF4(r.TotalQty), r.Warehouses)
		notifyType := NotifyTypeExpiryAlert
		if level == "expired" {
			title = "过期库存：" + title
			content += "该批次已过期，请按过期品处置流程处理。"
		}
		n, err := notifyRecipients(ctx, gormNotifyStore{db: s.db}, notifyInput{
			Type: notifyType, Title: title, Content: content, DedupKey: dedupKey,
		}, recipients)
		if err != nil {
			return fmt.Errorf("效期通知写入失败（batch=%s）: %w", r.BatchNo, err)
		}
		notified += n
	}
	s.log.Info("效期预警扫描完成",
		zap.Int("alert_rows", alerted),
		zap.Int64("notified", notified))
	return nil
}

// dateAtLocal 归一为本地日期零点（效期天数按自然日计算，消除时区/时分尾差）。
func dateAtLocal(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// expiryLevelText 档位人读文案（预警口径随内容落库，可追溯 inventory-rules §7.1）。
func expiryLevelText(level string, daysLeft int) string {
	if level == "expired" {
		return "已过期"
	}
	return fmt.Sprintf("剩余效期 %d 天（命中 %s 天档位）", daysLeft, level)
}

// ---- 长期库存（积压）扫描（inventory_stagnant_scan，0 6 * * *；inventory-rules §11）----

// stagnantScanRow 积压行（与 /api/reports/stagnant-stock 同一末次移动口径）。
type stagnantScanRow struct {
	WarehouseID   int64     `gorm:"column:warehouse_id"`
	WarehouseCode string    `gorm:"column:warehouse_code"`
	WarehouseName string    `gorm:"column:warehouse_name"`
	SKUID         int64     `gorm:"column:sku_id"`
	SKUCode       string    `gorm:"column:sku_code"`
	SKUName       string    `gorm:"column:sku_name"`
	TotalQty      float64   `gorm:"column:total_qty"`
	LastMovedAt   time.Time `gorm:"column:last_moved_at"`
	IdleDays      int
	Tier          string
}

// stagnantThresholds 阈值清单（system_configs inventory.alert.stagnant_days，降序）。
func (s scanRunner) stagnantThresholds(ctx context.Context) ([]int, error) {
	raw, err := s.configValue(ctx, cfgKeyStagnantDays, defStagnantDays)
	if err != nil {
		return nil, fmt.Errorf("读取积压阈值失败: %w", err)
	}
	return parsePositiveIntList(raw)
}

// stagnantScanRows 只读聚合：有现存量且末次移动早于最小档位边界的仓+SKU 行
// （末次移动 = INBOUND/OUTBOUND/TRANSFER/MOVE/ADJUST 最后落账；无流水回退首次建账）。
func (s scanRunner) stagnantScanRows(ctx context.Context, cutoff time.Time) ([]stagnantScanRow, error) {
	var rows []stagnantScanRow
	err := s.db.WithContext(ctx).Raw(`
		WITH lastm AS (
		    SELECT l.warehouse_id, l.sku_id, MAX(l.created_at) AS last_moved_at
		    FROM inventory_ledgers l
		    WHERE l.change_type IN ('INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','MOVE','ADJUST')
		    GROUP BY 1, 2
		), stock AS (
		    SELECT i.warehouse_id, i.sku_id, SUM(i.total_qty) AS total_qty, MIN(i.created_at) AS first_stock_at
		    FROM inventory i
		    GROUP BY 1, 2
		    HAVING SUM(i.total_qty) > 0
		)
		SELECT st.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
		       st.sku_id, sk.code AS sku_code, p.name AS sku_name,
		       st.total_qty::float8 AS total_qty,
		       COALESCE(lm.last_moved_at, st.first_stock_at) AS last_moved_at
		FROM stock st
		LEFT JOIN lastm lm ON lm.warehouse_id = st.warehouse_id AND lm.sku_id = st.sku_id
		JOIN skus sk ON sk.id = st.sku_id
		JOIN products p ON p.id = sk.product_id
		JOIN warehouses w ON w.id = st.warehouse_id
		WHERE COALESCE(lm.last_moved_at, st.first_stock_at) <= ?
		ORDER BY last_moved_at, st.warehouse_id, st.sku_id`, cutoff).Scan(&rows).Error
	return rows, err
}

func (s scanRunner) stagnantScan(ctx context.Context) error {
	tiers, err := s.stagnantThresholds(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	minTier := tiers[len(tiers)-1]
	rows, err := s.stagnantScanRows(ctx, now.AddDate(0, 0, -minTier))
	if err != nil {
		return fmt.Errorf("积压行聚合失败: %w", err)
	}
	day := dayKey(now)
	// 收件人集合每轮扫描恒定：循环外解析一次（防 N+1）。
	recipients, err := resolveRecipients(ctx, s.db, alertRecipientRoles)
	if err != nil {
		return fmt.Errorf("积压收件人解析失败: %w", err)
	}
	var notified int64
	alerted := 0
	for _, r := range rows {
		idleDays := int(now.Sub(r.LastMovedAt).Hours() / 24)
		tier := stagnantTierFor(idleDays, tiers)
		if tier == "" {
			continue // 边界防御（cutoff 已按最小档位过滤）
		}
		alerted++
		n, err := notifyRecipients(ctx, gormNotifyStore{db: s.db}, notifyInput{
			Type:  NotifyTypeStockAlert,
			Title: fmt.Sprintf("积压预警：%s（%s）%d 天未动", r.SKUName, r.SKUCode, idleDays),
			Content: fmt.Sprintf(
				"仓库 %s（%s）SKU %s（%s）现存量 %s 已 %d 天未发生任何移动（末次移动 %s，命中 %s 天档位），请复核补货/促销/调拨处置。",
				r.WarehouseName, r.WarehouseCode, r.SKUName, r.SKUCode,
				fmtF4(r.TotalQty), idleDays, r.LastMovedAt.Format("2006-01-02"), tier),
			// dedup = 日+SKU+仓+档位+收件人（档位进键：30→60 档升级时重新提醒一次）。
			DedupKey: fmt.Sprintf("stagnant:%s:%s:%s:%s", day, r.SKUCode, r.WarehouseCode, tier),
		}, recipients)
		if err != nil {
			return fmt.Errorf("积压通知写入失败（sku=%s wh=%s）: %w", r.SKUCode, r.WarehouseCode, err)
		}
		notified += n
	}
	s.log.Info("积压扫描完成",
		zap.Int("alert_rows", alerted),
		zap.Int64("notified", notified))
	return nil
}

// stagnantTierFor 积压分档（阈值降序；命中最大档位——与 reports.stagnantTier 同公式，
// 双包各自内聚避免跨包依赖）。
func stagnantTierFor(idleDays int, thresholds []int) string {
	for _, t := range thresholds {
		if idleDays >= t {
			return fmt.Sprintf("%d", t)
		}
	}
	return ""
}
