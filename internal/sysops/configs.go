package sysops

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/middleware"
)

// 系统配置（/api/system/configs，backend-m3-plan §10.2）：
// GET 全量（量级有限不分页）；PUT 批量保存（逐项 UPDATE + middleware.Audit 同事务审计，
// permission §6；readonly 项服务端拒绝写入）。键以 system_configs 行为真相源——
// fail-closed：PUT 键不存在即拒绝（禁止经 API 发明清单外配置键）；M3 冻结键清单由
// ensureSystemConfigKeys 以 ON CONFLICT DO NOTHING 幂等补行（seed 语义，plan §10.2）。

// systemConfigItem 配置项（web/src/api/system.ts SystemConfigItem 契约，snake_case tag）。
type systemConfigItem struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Name      string    `json:"name"`
	Group     string    `json:"group"`
	Type      string    `json:"type,omitempty"`
	Options   []string  `json:"options,omitempty"`
	Remark    string    `json:"remark,omitempty"`
	Readonly  bool      `json:"readonly"`
	UpdatedAt time.Time `json:"updated_at"`
	// optionsRaw options jsonb 的文本形态（Scan 载体，解析后填充 Options）。
	OptionsRaw string `json:"-"`
}

// M3 冻结配置键（backend-m3-plan §10.2 seed 键清单，消费方：预警扫描/文件清理/智能能力）。
type configSeed struct {
	Key, Value, Name, Group, Type, Remark string
	Options                               []string
	Readonly                              bool
}

var configSeeds = []configSeed{
	{Key: "inventory.alert.expiry_days", Value: "30,15,7,3", Name: "效期预警阈值（天）", Group: "库存预警", Type: "text",
		Remark: "逗号分隔的临期天数档位（inventory-rules §7.1：30/15/7/3 + 已过期），效期扫描与库存预警页共用"},
	{Key: "inventory.alert.low_stock_enabled", Value: "true", Name: "库存预警扫描开关", Group: "库存预警", Type: "boolean",
		Remark: "false 时低库存扫描任务空转（不产生通知）"},
	{Key: "inventory.alert.stagnant_days", Value: "30,60,90", Name: "积压分档阈值（天）", Group: "库存预警", Type: "text",
		Remark: "逗号分隔的未动天数档位（inventory-rules §11：30/60/90），积压扫描与报表共用"},
	{Key: "task.timeout.pick_hours", Value: "4", Name: "拣货任务超时阈值（小时）", Group: "作业任务", Type: "number",
		Remark: "拣货任务创建后超过该时长未完成 → 站内通知（task_timeout_scan，backend-m3-plan §10.2）"},
	{Key: "task.timeout.putaway_hours", Value: "4", Name: "上架任务超时阈值（小时）", Group: "作业任务", Type: "number",
		Remark: "上架任务创建后超过该时长未完成 → 站内通知（task_timeout_scan，backend-m3-plan §10.2）"},
	{Key: "insight.replenishment.lead_time_days", Value: "7", Name: "补货建议：采购周期（天）", Group: "智能能力", Type: "number",
		Remark: "补货建议目标库存公式参数（backend-m3-plan §9.3）"},
	{Key: "insight.replenishment.buffer_days", Value: "3", Name: "补货建议：安全缓冲（天）", Group: "智能能力", Type: "number",
		Remark: "补货建议目标库存公式参数（backend-m3-plan §9.3）"},
}

// ensureSystemConfigKeys 幂等补齐 M3 冻结键（缺行补默认值，已存在行不覆盖——
// 管理端修改不被启动冲掉）。RegisterRoutes 装配期调用，失败 fail-fast。
func ensureSystemConfigKeys(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("sysops: 配置键初始化失败: db 为 nil")
	}
	for _, s := range configSeeds {
		options := "{}"
		if len(s.Options) > 0 {
			options = `["` + strings.Join(s.Options, `","`) + `"]`
		}
		err := db.WithContext(ctx).Exec(`
			INSERT INTO system_configs (key, value, name, "group", type, options, remark, readonly, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?::jsonb, ?, ?, now(), now(), 0, 0)
			ON CONFLICT (key) DO NOTHING`,
			s.Key, s.Value, s.Name, s.Group, s.Type, options, s.Remark, s.Readonly).Error
		if err != nil {
			return fmt.Errorf("sysops: 配置键 %s 初始化失败: %w", s.Key, err)
		}
	}
	return nil
}

// configValue 读单键值（行缺失返回缺省值与 false；预警扫描的缺省口径源）。
func (r *runtimeRepo) configValue(ctx context.Context, key, def string) (string, bool, error) {
	var v string
	err := r.db.WithContext(ctx).Raw(`SELECT value FROM system_configs WHERE key = ?`, key).Scan(&v).Error
	if err != nil {
		return def, false, err
	}
	if v == "" {
		// Scan 无行时 v 为零值；显式区分空串与缺失（空值行视同缺失走缺省）。
		var exists bool
		if err := r.db.WithContext(ctx).Raw(
			`SELECT EXISTS(SELECT 1 FROM system_configs WHERE key = ?)`, key).Scan(&exists).Error; err != nil {
			return def, false, err
		}
		if !exists {
			return def, false, nil
		}
	}
	return v, true, nil
}

// listConfigs 全量配置（按 group/key 排序，前端分组渲染）。
func (r *runtimeRepo) listConfigs(ctx context.Context) ([]systemConfigItem, error) {
	var rows []systemConfigItem
	err := r.db.WithContext(ctx).Raw(`
		SELECT key, value, name, "group", type, COALESCE(options::text, 'null') AS options_raw, remark, readonly, updated_at
		FROM system_configs
		ORDER BY "group", key`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Options = parseOptionsJSON(rows[i].OptionsRaw)
	}
	return rows, nil
}

// parseOptionsJSON 解析 options jsonb 文本（["a","b"]）；null/空/解析失败返回 nil（不造假候选值）。
func parseOptionsJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// saveConfigItem 单项保存入参。
type saveConfigItem struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	OldValue string `json:"-"` // 审计快照回填（读取后填充）
	Exists   bool   `json:"-"`
	Readonly bool   `json:"-"`
}

// saveConfigs 批量保存（单事务逐项 UPDATE + 逐项审计；任一项失败整体回滚——
// 批内原子，避免半套配置落地）。逐项 readonly 校验与存在性 fail-closed。
func (r *runtimeRepo) saveConfigs(ctx context.Context, actor auditActor, items []saveConfigItem) (int, error) {
	if len(items) == 0 {
		return 0, ErrConfigEmpty
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range items {
			var row struct {
				Value    string
				Readonly bool
			}
			err := tx.Raw(`SELECT value, readonly FROM system_configs WHERE key = ? FOR UPDATE`,
				items[i].Key).Scan(&row).Error
			if err != nil {
				return fmt.Errorf("读取配置 %s 失败: %w", items[i].Key, err)
			}
			if row.Value == "" {
				return ErrConfigNotFound
			}
			items[i].OldValue, items[i].Readonly, items[i].Exists = row.Value, row.Readonly, true
			if row.Readonly {
				return ErrConfigReadonly
			}
			if row.Value == items[i].Value {
				continue // 未变更项跳过（前端仅提交变更项；服务端兜底跳过，不刷审计）
			}
			if err := tx.Exec(`UPDATE system_configs SET value = ?, updated_at = now(), updated_by = ? WHERE key = ?`,
				items[i].Value, actor.UserID, items[i].Key).Error; err != nil {
				return fmt.Errorf("更新配置 %s 失败: %w", items[i].Key, err)
			}
			// 逐项审计（同事务，permission §6）：before/after 快照不含敏感值——
			// 配置值为运行参数（阈值/开关），非密钥类（密钥经 SF_* 环境变量注入，不入库）。
			if err := middleware.Audit(tx, middleware.AuditEntry{
				Module: "system", ObjectType: "system_config", ObjectID: 0,
				Action: "update", Success: true,
				OperatorID: actor.UserID, OperatorName: actor.Name,
				RequestID: actor.RequestID, IP: actor.IP, UserAgent: actor.UserAgent,
				Method: actor.Method, Path: actor.Path,
				Before: map[string]string{"key": items[i].Key, "value": items[i].OldValue},
				After:  map[string]string{"key": items[i].Key, "value": items[i].Value},
			}); err != nil {
				return fmt.Errorf("配置 %s 审计写入失败: %w", items[i].Key, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// parseBoolConfig 布尔配置解析（宽松值域：true/false；非法值报错 fail-closed）。
func parseBoolConfig(raw, key string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "on":
		return true, nil
	case "false", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("配置 %s 布尔值非法: %q", key, raw)
	}
}

// parseIntConfig 数值配置解析（正整数）。
func parseIntConfig(raw, key string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("配置 %s 数值非法: %q", key, raw)
	}
	return n, nil
}
