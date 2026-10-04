package devices

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// 数据访问层（architecture.md §1：Service → Repository → Database；handler 禁止直连
// 数据库）。Repository 为接口：Service 依赖接口，单元测试以内存替身替换（ask 约束：
// 单测不依赖 PostgreSQL/Redis/网络，参照 internal/returns、internal/masterdata 既有模式）。
//
// 审计红线：本包对 scan_logs/device_logs 仅 INSERT + SELECT（000013 append-only，
// app_grants.sql 分层无 UPDATE/DELETE 通路——plan §2.3 判据 3）。

// DeviceFilter 设备列表过滤（列表强制分页，architecture.md §7）。
type DeviceFilter struct {
	Type        string // 空=全部
	Status      string
	Keyword     string // code/name ILIKE
	WarehouseID int64
	// Online 在线过滤：nil=不过滤；true=仅在线（last_online_at >= OnlineCutoff），
	// false=仅离线。OnlineCutoff 由 Service 按 3 分钟窗口计算（plan §8.2）。
	Online       *bool
	OnlineCutoff time.Time
	// 数据权限仓库范围（auth.WarehouseScope，permission.md §4；handler 组装，
	// 禁止接受前端仓库范围参数决定可见性）。
	WarehouseIDs  []int64
	AllWarehouses bool
	Page          int
	PageSize      int
}

// ScanLogFilter 扫码日志过滤（devices:scanlog:list，管理端 devices.md §7.1 查看扫码日志）。
type ScanLogFilter struct {
	DeviceID    int64
	UserID      int64
	WarehouseID int64
	Success     *bool
	Keyword     string // raw_code/page/device_code ILIKE
	// 数据权限仓库范围（同 DeviceFilter）。
	WarehouseIDs  []int64
	AllWarehouses bool
	Page          int
	PageSize      int
}

// DeviceLogFilter 设备日志过滤（GET /api/devices/{id}/logs）。
type DeviceLogFilter struct {
	DeviceID  int64
	Level     string
	Page      int
	PageSize  int
	StartTime *time.Time
	EndTime   *time.Time
}

// ErrNotFound 统一未命中信号（Service 转换为域错误码；替身与 GORM 实现同形）。
var ErrNotFound = errors.New("devices: 记录不存在")

// ErrStatusConflict 守卫 UPDATE 影响行数 0 的内部信号（Service 转 DEVICE_STATUS_CONFLICT；
// UpdateDeviceBinding 带 token_version 递增时的启用态守卫复用）。
var ErrStatusConflict = errors.New("devices: 状态守卫未命中")

// Repository 设备域数据访问接口。
type Repository interface {
	// DB 返回句柄（Service 开启外层事务用，masterdata/returns 同款约定）。
	DB() *gorm.DB

	// —— 设备档案 ——
	InsertDevice(tx *gorm.DB, d *Device) error
	FindDevice(ctx context.Context, id int64) (*Device, error)
	FindDeviceByCode(ctx context.Context, code string) (*Device, error)
	ListDevices(ctx context.Context, f DeviceFilter) ([]*Device, int64, error)
	// UpdateDeviceStatus 状态机守卫 UPDATE：WHERE id=? AND status=<from>，影响行数 0 即冲突
	// （plan §13.2 状态守卫幂等统一形态）。
	UpdateDeviceStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error)
	// BumpTokenVersion 设备令牌撤销：token_version+1（旧设备令牌全失效——plan §8.2；
	// 停用事务的第二步）。
	BumpTokenVersion(tx *gorm.DB, id int64) error
	// UpdateDeviceBinding 绑定/解绑（unbind 时 userID=0 且 tokenVersion 递增——
	// 000013 注：解绑重置即 token_version+1 全量失效旧设备令牌）。
	UpdateDeviceBinding(tx *gorm.DB, id, userID, by int64, bumpTokenVersion bool) error
	// RegenerateActivation 重新生成激活码：写新哈希与时效、回退 PENDING、token_version+1
	// （旧设备令牌全失效——plan §8.1/§8.2）。守卫 status=ENABLED，0 行即冲突。
	RegenerateActivation(tx *gorm.DB, id int64, tokenHash string, expiresAt time.Time, by int64) (int64, error)
	// ConsumeActivation 一次性激活消费（防重放裁决）：单条守卫 UPDATE——
	// WHERE code=? AND activation_token_hash=? AND activation_status='PENDING'
	// AND activation_expires_at > ?（now 由 Service 注入，守卫判定确定性可测）；
	// 置 ACTIVATED、清哈希与时效、落设备自报信息。
	// 影响行数 0 = token 错误/已过期/已消费/已停用，调用方统一按激活失败处理。
	ConsumeActivation(tx *gorm.DB, code, tokenHash string, info DeviceSelfInfo, now time.Time) (int64, error)
	// UpdateDeviceHeartbeat 心跳：last_online_at=now、可选电量/版本/IP（列名白名单，
	// map 值由 Service 组装）。返回当前行 token_version 供设备令牌有效性再确认由
	// DeviceAuthRequired 的行读取承担，本方法不返回设备行。
	UpdateDeviceHeartbeat(tx *gorm.DB, id int64, cols map[string]any) error
	// UpdateDeviceLastScan 挂接 last_scan_at（resolve 设备路径降级写——失败不影响识别）。
	UpdateDeviceLastScan(ctx context.Context, id int64, at time.Time) error

	// —— 设备配置 ——
	UpsertDeviceConfig(tx *gorm.DB, deviceID int64, cfg jsonb, by int64) (int, error)
	FindDeviceConfig(ctx context.Context, deviceID int64) (*DeviceConfig, error)

	// —— 扫码审计（append-only）——
	InsertScanLog(tx *gorm.DB, l *ScanLog) error
	ListScanLogs(ctx context.Context, f ScanLogFilter) ([]*ScanLog, int64, error)
	CountScanLogs(ctx context.Context, deviceID int64, since time.Time) (total, today int64, err error)

	// —— 设备日志（append-only）——
	InsertDeviceLogs(tx *gorm.DB, logs []*DeviceLog) error
	ListDeviceLogs(ctx context.Context, f DeviceLogFilter) ([]*DeviceLog, int64, error)

	// —— App 版本（只读）——
	FindLatestAppVersion(ctx context.Context, platform string) (*AppVersion, error)
}

// DeviceSelfInfo 激活时设备自报信息（plan §8.2 device_info{brand,model,os,app_version}）。
type DeviceSelfInfo struct {
	Brand      string
	Model      string
	OS         string
	AppVersion string
	IP         string
}

// gormRepository GORM 实现（迁移 000013 列结构）。
type gormRepository struct {
	db *gorm.DB
}

// NewGormRepository 构造 GORM 数据访问实现。
func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) DB() *gorm.DB { return r.db }

// ---- 设备档案 ----

func (r *gormRepository) InsertDevice(tx *gorm.DB, d *Device) error {
	return tx.Create(d).Error
}

func (r *gormRepository) FindDevice(ctx context.Context, id int64) (*Device, error) {
	var d Device
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (r *gormRepository) FindDeviceByCode(ctx context.Context, code string) (*Device, error) {
	var d Device
	err := r.db.WithContext(ctx).Where("code = ?", code).Take(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (r *gormRepository) ListDevices(ctx context.Context, f DeviceFilter) ([]*Device, int64, error) {
	q := r.db.WithContext(ctx).Model(&Device{})
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("code ILIKE ? OR name ILIKE ?", kw, kw)
	}
	if f.Online != nil {
		if *f.Online {
			q = q.Where("last_online_at IS NOT NULL AND last_online_at >= ?", f.OnlineCutoff)
		} else {
			q = q.Where("last_online_at IS NULL OR last_online_at < ?", f.OnlineCutoff)
		}
	}
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return nil, 0, nil // 指定仓库范围但无绑定 → 不可见任何行（fail-closed，permission.md §4）
		}
		q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*Device
	err := q.Order("created_at DESC, id DESC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) UpdateDeviceStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	res := tx.Model(&Device{}).Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{"status": to, "updated_by": by, "updated_at": time.Now()})
	return res.RowsAffected, res.Error
}

func (r *gormRepository) BumpTokenVersion(tx *gorm.DB, id int64) error {
	return tx.Model(&Device{}).Where("id = ?", id).
		Update("token_version", gorm.Expr("token_version + 1")).Error
}

func (r *gormRepository) UpdateDeviceBinding(tx *gorm.DB, id, userID, by int64, bumpTokenVersion bool) error {
	cols := map[string]any{"bound_user_id": userID, "updated_by": by, "updated_at": time.Now()}
	if bumpTokenVersion {
		// 解绑重置即 token_version+1（000013 注撤销语义）；map 更新 + gorm.Expr 列自增，
		// 守卫 WHERE status=ENABLED（0 行 = 已停用，Service 转 DEVICE_STATUS_CONFLICT）。
		cols["token_version"] = gorm.Expr("token_version + 1")
		res := tx.Model(&Device{}).
			Where("id = ? AND status = ?", id, StatusEnabled).
			Updates(cols)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrStatusConflict
		}
		return nil
	}
	return tx.Model(&Device{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepository) RegenerateActivation(tx *gorm.DB, id int64, tokenHash string, expiresAt time.Time, by int64) (int64, error) {
	res := tx.Model(&Device{}).
		Where("id = ? AND status = ?", id, StatusEnabled).
		Updates(map[string]any{
			"activation_status":     ActivationPending,
			"activation_token_hash": tokenHash,
			"activation_expires_at": expiresAt,
			"activated_at":          nil,
			"activated_by":          0,
			"token_version":         gorm.Expr("token_version + 1"),
			"updated_by":            by,
			"updated_at":            time.Now(),
		})
	return res.RowsAffected, res.Error
}

func (r *gormRepository) ConsumeActivation(tx *gorm.DB, code, tokenHash string, info DeviceSelfInfo, now time.Time) (int64, error) {
	res := tx.Model(&Device{}).
		Where("code = ? AND activation_token_hash = ? AND activation_status = ? AND activation_expires_at > ?",
			code, tokenHash, ActivationPending, now).
		Updates(map[string]any{
			"activation_status":     ActivationActivated,
			"activation_token_hash": "", // 一次性消费：哈希即清，重放必落空
			"activation_expires_at": nil,
			"activated_at":          now,
			"brand":                 info.Brand,
			"model":                 info.Model,
			"os":                    info.OS,
			"app_version":           info.AppVersion,
			"ip":                    info.IP,
			"updated_at":            time.Now(),
		})
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateDeviceHeartbeat(tx *gorm.DB, id int64, cols map[string]any) error {
	return tx.Model(&Device{}).Where("id = ?", id).Updates(cols).Error
}

func (r *gormRepository) UpdateDeviceLastScan(ctx context.Context, id int64, at time.Time) error {
	return r.db.WithContext(ctx).Model(&Device{}).Where("id = ?", id).
		Update("last_scan_at", at).Error
}

// ---- 设备配置 ----

func (r *gormRepository) UpsertDeviceConfig(tx *gorm.DB, deviceID int64, cfg jsonb, by int64) (int, error) {
	// 一机一配置（uk_device_configs_device）：INSERT ... ON CONFLICT (device_id) 时版本在
	// 既有行上 +1（plan §8.1：PUT config version+1）。RETURNING version 取发放后值。
	res := tx.Exec(`
		INSERT INTO device_configs (device_id, config, version, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, 1, now(), now(), ?, ?)
		ON CONFLICT (device_id) DO UPDATE
		SET config = EXCLUDED.config, version = device_configs.version + 1,
		    updated_at = now(), updated_by = EXCLUDED.updated_by
		RETURNING version`,
		deviceID, cfg, by, by)
	if res.Error != nil {
		return 0, res.Error
	}
	var v int
	if err := tx.Raw(`SELECT version FROM device_configs WHERE device_id = ?`, deviceID).Scan(&v).Error; err != nil {
		return 0, err
	}
	return v, nil
}

func (r *gormRepository) FindDeviceConfig(ctx context.Context, deviceID int64) (*DeviceConfig, error) {
	var c DeviceConfig
	err := r.db.WithContext(ctx).Where("device_id = ?", deviceID).Take(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 未配置过 ≠ 错误：调用方按 version 0 / 空 config 处理
		}
		return nil, err
	}
	return &c, nil
}

// ---- 扫码审计（append-only：仅 INSERT + SELECT）----

func (r *gormRepository) InsertScanLog(tx *gorm.DB, l *ScanLog) error {
	return tx.Create(l).Error
}

func (r *gormRepository) ListScanLogs(ctx context.Context, f ScanLogFilter) ([]*ScanLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&ScanLog{})
	if f.DeviceID > 0 {
		q = q.Where("device_id = ?", f.DeviceID)
	}
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.WarehouseID > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseID)
	}
	if f.Success != nil {
		q = q.Where("success = ?", *f.Success)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("raw_code ILIKE ? OR page ILIKE ? OR device_code ILIKE ?", kw, kw, kw)
	}
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			return nil, 0, nil // fail-closed（permission.md §4）
		}
		q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*ScanLog
	err := q.Order("created_at DESC, id DESC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) CountScanLogs(ctx context.Context, deviceID int64, since time.Time) (total, today int64, err error) {
	if err = r.db.WithContext(ctx).Model(&ScanLog{}).Where("device_id = ?", deviceID).Count(&total).Error; err != nil {
		return 0, 0, err
	}
	if err = r.db.WithContext(ctx).Model(&ScanLog{}).
		Where("device_id = ? AND created_at >= ?", deviceID, since).Count(&today).Error; err != nil {
		return 0, 0, err
	}
	return total, today, nil
}

// ---- 设备日志（append-only）----

func (r *gormRepository) InsertDeviceLogs(tx *gorm.DB, logs []*DeviceLog) error {
	if len(logs) == 0 {
		return nil
	}
	return tx.Create(&logs).Error
}

func (r *gormRepository) ListDeviceLogs(ctx context.Context, f DeviceLogFilter) ([]*DeviceLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&DeviceLog{}).Where("device_id = ?", f.DeviceID)
	if f.Level != "" {
		q = q.Where("level = ?", f.Level)
	}
	if f.StartTime != nil {
		q = q.Where("occurred_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("occurred_at < ?", *f.EndTime)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*DeviceLog
	err := q.Order("occurred_at DESC, id DESC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

// ---- App 版本（只读）----

func (r *gormRepository) FindLatestAppVersion(ctx context.Context, platform string) (*AppVersion, error) {
	var v AppVersion
	err := r.db.WithContext(ctx).
		Where("platform = ? AND status = ?", platform, AppVersionPublished).
		Order("version_code DESC").
		Take(&v).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &v, nil
}
