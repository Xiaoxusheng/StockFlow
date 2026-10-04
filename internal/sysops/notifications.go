package sysops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// 站内通知写入与个人收件箱（backend-m3-plan §10.6，architecture §10）。
//
// 写入方：预警扫描（低库存/效期/积压）、任务失败告警、备份失败告警。M3 预警/告警均解析为
// 具体收件人逐行落库（user_id 恒非空，无 NULL 广播行）；dedup_key = 对象键 + :{user_id}
// 复合收件人后缀（plan §4.3），uk_notifications_dedup 部分唯一索引保证同对象同收件人
// 同日只通知一次。接收人解析规则（plan §10.6 冻结）：预警类 → super_admin/sys_admin/
// warehouse_manager 全量启用用户；失败告警类 → super_admin/sys_admin。

// Notification notifications GORM 模型（000014 迁移列结构；"read" 为列名，GORM 自动加引号）。
type Notification struct {
	ID        database.ID        `gorm:"primaryKey;autoIncrement"`
	UserID    int64              `gorm:"column:user_id"` // M3 恒非空（逐收件人落库）
	Type      string             `gorm:"column:type"`
	Title     string             `gorm:"column:title"`
	Content   string             `gorm:"column:content"`
	DedupKey  *string            `gorm:"column:dedup_key"`
	Read      bool               `gorm:"column:read"`
	ReadAt    *database.JSONTime `gorm:"column:read_at"`
	CreatedAt database.JSONTime  `gorm:"column:created_at"`
	UpdatedAt database.JSONTime  `gorm:"column:updated_at"`
	CreatedBy int64              `gorm:"column:created_by"`
	UpdatedBy int64              `gorm:"column:updated_by"`
}

// TableName 显式表名。
func (Notification) TableName() string { return "notifications" }

// 通知类型（chk_notifications_type 值域，architecture §10 六值）。
const (
	NotifyTypeSystem      = "SYSTEM"
	NotifyTypeStockAlert  = "STOCK_ALERT"
	NotifyTypeExpiryAlert = "EXPIRY_ALERT"
	NotifyTypeTask        = "TASK"
)

// 预警/告警接收人角色编码（plan §10.6 接收人解析规则冻结）。
var (
	// alertRecipientRoles 预警类（低库存/效期/积压）接收人角色。
	alertRecipientRoles = []string{"super_admin", "sys_admin", "warehouse_manager"}
	// failureRecipientRoles 失败告警类（定时任务失败/备份失败）接收人角色。
	failureRecipientRoles = []string{"super_admin", "sys_admin"}
)

// resolveRecipients 解析角色 → 具体启用用户 ID（只读 SELECT users/user_roles/roles；
// 收件人逐行落库前的唯一聚合点）。
func resolveRecipients(ctx context.Context, db *gorm.DB, roleCodes []string) ([]int64, error) {
	if db == nil {
		return nil, fmt.Errorf("sysops: 收件人解析失败: db 为 nil")
	}
	var ids []int64
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT u.id
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE r.code IN ? AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
		ORDER BY u.id`, roleCodes).Scan(&ids).Error
	return ids, err
}

// notifyInput 单条通知输入（dedupKey 为对象键，不含收件人后缀——落库前复合）。
type notifyInput struct {
	Type     string
	Title    string
	Content  string
	DedupKey string // 空串 = 不去重
}

// notifyStore 通知落库端口（生产 = gorm INSERT ... ON CONFLICT DO NOTHING；
// 单测 = 内存 map 模拟 uk_notifications_dedup 部分唯一索引——plan §14 内存替身）。
type notifyStore interface {
	// insertNotification 落库单条；返回是否新插入（dedup 命中=false）。
	insertNotification(ctx context.Context, userID int64, dedup string, in notifyInput) (bool, error)
}

// gormNotifyStore notifications 写入实现（白名单自有表写通路；dedup_key 部分唯一索引
// + ON CONFLICT DO NOTHING = 扫描幂等防线，plan §4.3）。
type gormNotifyStore struct{ db *gorm.DB }

func (s gormNotifyStore) insertNotification(ctx context.Context, userID int64, dedup string, in notifyInput) (bool, error) {
	var dedupArg any
	if dedup != "" {
		dedupArg = dedup
	}
	res := s.db.WithContext(ctx).Exec(`
		INSERT INTO notifications (user_id, type, title, content, dedup_key, read, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, FALSE, now(), now(), 0, 0)
		ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
		userID, in.Type, in.Title, in.Content, dedupArg)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// dedupKeyFor dedup_key 复合收件人组装（plan §4.3：对象键:{user_id}——同一对象同一收件人
// 同日只通知一次；不含 user_id 的键会使第二个收件人 INSERT 撞唯一索引）。
func dedupKeyFor(objectKey string, userID int64) string {
	return fmt.Sprintf("%s:%d", objectKey, userID)
}

// notifyRecipients 按收件人逐行落库通知；dedup 命中（同对象同收件人已通知）跳过。
// 返回实际新落库行数。
func notifyRecipients(ctx context.Context, store notifyStore, in notifyInput, recipients []int64) (int64, error) {
	if store == nil {
		return 0, fmt.Errorf("sysops: 通知写入失败: store 为 nil")
	}
	var inserted int64
	for _, uid := range recipients {
		dedup := ""
		if in.DedupKey != "" {
			dedup = dedupKeyFor(in.DedupKey, uid)
		}
		ok, err := store.insertNotification(ctx, uid, dedup, in)
		if err != nil {
			return inserted, fmt.Errorf("sysops: 通知写入失败（user=%d）: %w", uid, err)
		}
		if ok {
			inserted++
		}
	}
	return inserted, nil
}

// notifyRoles 解析角色并逐收件人落库（单条/低频通知入口；扫描循环内禁止使用——
// 收件人集合每轮恒定，应 resolveRecipients 一次后经 notifyRecipients 逐行落库，
// 避免逐行重复执行相同角色→用户查询，N+1）。
func notifyRoles(ctx context.Context, db *gorm.DB, in notifyInput, roleCodes []string) (int64, error) {
	recipients, err := resolveRecipients(ctx, db, roleCodes)
	if err != nil {
		return 0, fmt.Errorf("sysops: 预警收件人解析失败: %w", err)
	}
	return notifyRecipients(ctx, gormNotifyStore{db: db}, in, recipients)
}

// NotifyTaskFailure 异步任务（asynqx）失败告警——供任务消费包（datax/printing）的
// 重试耗尽/inline 无重试终态回调调用（backend-m3-plan §4.2"重试耗尽 → 终态 FAILED +
// error_message + 站内告警通知"；接收人 = 失败告警类 super_admin/sys_admin，§10.6）。
// dedup = taskfail:{type}:{单号}：同一任务行至多告警一次（终态回调单任务至多触发一次，
// 重放/重启不重复）。db 为任务消费包侧 GORM 句柄（notifications 为平台自有表）。
func NotifyTaskFailure(ctx context.Context, db *gorm.DB, taskType, taskNo, message string) error {
	if db == nil {
		return fmt.Errorf("sysops: 任务失败告警失败: db 为 nil")
	}
	_, err := notifyRoles(ctx, db, notifyInput{
		Type:  NotifyTypeSystem,
		Title: "异步任务执行失败：" + taskNo,
		Content: fmt.Sprintf("任务 %s（%s）执行失败且已无重试机会，任务行已标记终态：%s。请到数据中心/打印中心任务列表核查。",
			taskNo, taskType, truncateContent(message, 500)),
		DedupKey: fmt.Sprintf("taskfail:%s:%s", taskType, taskNo),
	}, failureRecipientRoles)
	return err
}

// ReadConfigValue 跨域只读桥：读 system_configs 单键（行缺失/空值回退 def）。
// 供 reports 等域装配运行时参数（plan §10.2 键清单为冻结清单，读侧禁止发明键外含义）。
func ReadConfigValue(ctx context.Context, db *gorm.DB, key, def string) (string, error) {
	if db == nil {
		return def, fmt.Errorf("sysops: 配置读取失败: db 为 nil")
	}
	v, _, err := (&runtimeRepo{db: db}).configValue(ctx, key, def)
	return v, err
}

// ReadConfigInt 跨域只读桥：读 system_configs 数值键（非法值/负数返回错误 fail-closed，
// 行缺失回退 def）。供 reports 补货参数（insight.replenishment.*）装配。
func ReadConfigInt(ctx context.Context, db *gorm.DB, key string, def int) (int, error) {
	raw, err := ReadConfigValue(ctx, db, key, fmt.Sprintf("%d", def))
	if err != nil {
		return def, err
	}
	n, err := parseIntConfig(raw, key)
	if err != nil {
		return def, fmt.Errorf("sysops: 配置 %s 解析失败: %w", key, err)
	}
	return n, nil
}

// ReadConfigIntList 跨域只读桥：读 system_configs 逗号分隔整数清单键（去重降序，
// 与 parsePositiveIntList 同公式；行缺失回退 def）。供 reports 预警阈值
// （inventory.alert.*）装配——与预警扫描同键同值，两套口径不再漂移。
func ReadConfigIntList(ctx context.Context, db *gorm.DB, key string, def []int) ([]int, error) {
	defRaw := make([]string, len(def))
	for i, v := range def {
		defRaw[i] = strconv.Itoa(v)
	}
	raw, err := ReadConfigValue(ctx, db, key, strings.Join(defRaw, ","))
	if err != nil {
		return def, err
	}
	list, err := parsePositiveIntList(raw)
	if err != nil {
		return def, fmt.Errorf("sysops: 配置 %s 解析失败: %w", key, err)
	}
	return list, nil
}

// M3 冻结配置键的导出常量（跨域只读桥消费方引用，避免散落字符串字面量；
// 键清单 = configs.go configSeeds，plan §10.2）。
const (
	// CfgKeyExpiryDays 效期预警阈值（天，逗号分隔降序档位）。
	CfgKeyExpiryDays = "inventory.alert.expiry_days"
	// CfgKeyStagnantDays 积压分档阈值（天，逗号分隔降序档位）。
	CfgKeyStagnantDays = "inventory.alert.stagnant_days"
	// CfgKeyReplenishmentLeadDays 补货建议采购周期（天）。
	CfgKeyReplenishmentLeadDays = "insight.replenishment.lead_time_days"
	// CfgKeyReplenishmentBufferDays 补货建议安全缓冲（天）。
	CfgKeyReplenishmentBufferDays = "insight.replenishment.buffer_days"
)

// notifyJobFailure 定时任务失败告警（super_admin/sys_admin；dedup=jobfail:{code}:{起始秒}——
// 单次执行至多一条，重启重试不重复；plan §4.3/§10.6）。
func notifyJobFailure(ctx context.Context, db *gorm.DB, jobCode, name, message string, startedAt time.Time) error {
	_, err := notifyRoles(ctx, db, notifyInput{
		Type:  NotifyTypeSystem,
		Title: fmt.Sprintf("定时任务执行失败：%s", name),
		Content: fmt.Sprintf("任务 %s（%s）执行失败：%s。系统不做同周期重试，将在下一周期自然重试，请检查执行日志（/api/system/jobs）。",
			jobCode, name, truncateContent(message, 500)),
		DedupKey: fmt.Sprintf("jobfail:%s:%d", jobCode, startedAt.Unix()),
	}, failureRecipientRoles)
	return err
}

// ---- 个人收件箱（认证即可用，无权限点；plan §10.6）----

// inboxItem 收件箱行（前端 NotificationItem 契约，snake_case JSON tag；
// 前端先行 camelCase（web/src/api/notifications.ts）由前端对齐轮回对——plan §12.4）。
type inboxItem struct {
	// ID 用 database.ID（JSON 字符串形态，backend-m1-plan §1 全局约定：业务 ID 一律
	// 字符串防 JS 2^53 精度丢失；裸 int64 会序列化为 JSON number，违反全仓约定）。
	// CreatedAt 用 database.JSONTime（api.md §2 YYYY-MM-DD HH:mm:ss；裸 time.Time
	// 会序列化为 RFC3339）。
	ID        database.ID       `json:"id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Read      bool              `json:"read"`
	CreatedAt database.JSONTime `json:"created_at"`
}

type inboxFilter struct {
	UserID   int64
	Read     *bool
	Page     int
	PageSize int
}

func (r *runtimeRepo) listInbox(ctx context.Context, f inboxFilter) ([]inboxItem, int64, error) {
	readCond := "1 = 1"
	args := []any{f.UserID}
	if f.Read != nil {
		readCond = `"read" = ?`
		args = append(args, *f.Read)
	}
	base := `FROM notifications WHERE user_id = ? AND ` + readCond
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) "+base, args...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 通知计数失败: %w", err)
	}
	var rows []inboxItem
	list := `SELECT id, type, title, content, "read", created_at ` + base +
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	if err := r.db.WithContext(ctx).Raw(list, args...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 通知查询失败: %w", err)
	}
	return rows, total, nil
}

func (r *runtimeRepo) unreadCount(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND "read" = FALSE`, userID).Scan(&n).Error
	return n, err
}

func (r *runtimeRepo) markRead(ctx context.Context, userID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE notifications SET "read" = TRUE, read_at = now(), updated_at = now()
		WHERE id = ? AND user_id = ? AND "read" = FALSE`, id, userID)
	return res.RowsAffected, res.Error
}

func (r *runtimeRepo) markAllRead(ctx context.Context, userID int64) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE notifications SET "read" = TRUE, read_at = now(), updated_at = now()
		WHERE user_id = ? AND "read" = FALSE`, userID)
	return res.RowsAffected, res.Error
}

// truncateContent 内容截断（notifications.content 为 text 无宽度约束，仅防超长刷屏）。
// 按字节限长但回退到完整 UTF-8 rune 边界——避免切断多字节字符产生非法 UTF-8 通知内容。
func truncateContent(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s[:max])
	for len(b) > 0 && !utf8.RuneStart(b[len(b)-1]) {
		b = b[:len(b)-1] // 回退切点，跳过被切断的多字节序列尾部
	}
	if len(b) > 0 {
		if r, size := utf8.DecodeLastRune(b); r == utf8.RuneError && size == 1 {
			// 切点落在多字节字符起始字节上（该字节自身不完整）——去掉它。
			b = b[:len(b)-1]
		}
	}
	return string(b) + "…"
}
