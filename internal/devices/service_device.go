package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// 设备管理业务层（architecture.md §1：业务逻辑/状态机/事务边界/校验）。
// 依赖以窄接口注入（Repository + ports.go），单元测试以接口替身替换。
//
// 事务边界（plan §13.1）：设备创建/绑定/停用/配置下发/重新生成激活码为单行事务
// + middleware.Audit 同事务审计（plan §13.8 审计清单：设备创建绑定停用配置下发）；
// 心跳/激活/日志上报为设备端动作，不在审计清单（激活的凭证发放已在创建/重新生成时审计）。

// onlineWindow 在线判定窗口（plan §8.2：last_online_at 在 3 分钟内 = 在线）。
const onlineWindow = 3 * time.Minute

// deviceCodePattern 设备编码规则（管理端命名如 SF-SCAN-001；000013 DDL 注：
// code 为管理端命名，非 docnum 单号）。2–64 位，字母数字开头，允许点/下划线/连字符。
var deviceCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,63}$`)

// configKeyKinds 配置下发键白名单（devices.md §7.3 冻结下发项）：
// 扫码模式/声音/震动/自动聚焦/连续扫码/扫码超时/默认仓库/任务刷新间隔/自动锁屏。
// 值类型约束：bool 开关类；int 正整数类（auto_lock_minutes 0=关闭，devices.md §6.5）。
var configKeyKinds = map[string]string{
	"scan_mode":            "string",
	"sound":                "bool",
	"vibrate":              "bool",
	"auto_focus":           "bool",
	"continuous_scan":      "bool",
	"scan_timeout_seconds": "int",
	"default_warehouse_id": "int",
	"task_refresh_seconds": "int",
	"auto_lock_minutes":    "int",
}

// Service 设备域业务层。
type Service struct {
	repo      Repository
	whChecker WarehouseChecker
	token     *tokenManager

	// resolve 匹配器（plan §8.3 顺序冻结；实现由各域 devices_resolve.go 提供）。
	skuBarcodes  SKUBarcodeReader
	bins         BinCodeReader
	serials      SerialReader
	batches      BatchReader
	purchaseDocs DocFinder
	salesDocs    DocFinder
	stockopsDocs DocFinder
	returnsDocs  DocFinder

	dedup     *DedupWindow
	logger    *zap.Logger
	serverURL string
	nowFn     func() time.Time
}

// NewService 构建业务层（装配校验在 RegisterRoutes 启动期 fail-fast）。
func NewService(repo Repository, opts ...Option) *Service {
	o := &options{dedupTTL: 0}
	for _, opt := range opts {
		opt(o)
	}
	return &Service{
		repo:         repo,
		whChecker:    o.whChecker,
		token:        mustTokenManager(),
		skuBarcodes:  o.skuBarcodes,
		bins:         o.bins,
		serials:      o.serials,
		batches:      o.batches,
		purchaseDocs: o.purchaseDocs,
		salesDocs:    o.salesDocs,
		stockopsDocs: o.stockopsDocs,
		returnsDocs:  o.returnsDocs,
		dedup:        NewDedupWindow(o.dedupTTL),
		logger:       o.logger,
		serverURL:    o.serverURL,
		nowFn:        time.Now,
	}
}

// mustTokenManager 构造令牌管理器（密钥非法启动期 panic 快速失败——与 auth 装配同模式）。
func mustTokenManager() *tokenManager {
	tm, err := newTokenManager()
	if err != nil {
		panic("devices 装配失败: " + err.Error())
	}
	return tm
}

// setNow 注入时钟（单测专用；生产恒 wall clock）——设备令牌时效与去重窗口同步跟随。
func (s *Service) setNow(now func() time.Time) { s.nowFn = now; s.token.now = now; s.dedup.now = now }

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因；
// 形态对齐 returns.Actor）。
type Actor struct {
	UserID    int64
	Username  string
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// auditEntry 构造 devices 域审计条目骨架（module=devices；plan §4.2 判据 2：
// 关键写操作经 middleware.Audit 与业务同事务写 operation_logs）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "devices",
		ObjectType:   objectType,
		Action:       action,
		ObjectID:     objectID,
		OperatorID:   a.UserID,
		OperatorName: a.Username,
		RequestID:    a.RequestID,
		IP:           a.IP,
		UserAgent:    a.UserAgent,
		Method:       a.Method,
		Path:         a.Path,
	}
}

// DeviceContext 设备端请求上下文（DeviceAuthRequired 注入 gin 上下文；
// WarehouseID 供 resolve 归因 scan_logs.warehouse_id——000013 DDL 注：作业仓）。
type DeviceContext struct {
	ID          int64
	Code        string
	Type        string
	WarehouseID int64
}

// gin 上下文键（本包内读写；跨包只经 DeviceFrom 取值）。
const ctxDeviceKey = "sf_device_ctx"

// setDeviceContext / DeviceFrom 设备上下文存取。
func setDeviceContext(c *gin.Context, d DeviceContext) { c.Set(ctxDeviceKey, d) }

// DeviceFrom 取当前设备上下文（DeviceAuthRequired 之后可用；resolve 设备归属归因用）。
func DeviceFrom(c *gin.Context) (DeviceContext, bool) {
	v, ok := c.Get(ctxDeviceKey)
	if !ok {
		return DeviceContext{}, false
	}
	d, ok := v.(DeviceContext)
	return d, ok
}

// ---- 出参形态 ----

// DeviceView 设备列表/详情视图（设备行 + 在线派生位；device.ts DeviceItem 契约对齐）。
type DeviceView struct {
	Device
	Online bool `json:"online"`
}

// DeviceDetailView 设备详情（frontend.md §14.2：基础信息/在线状态/配置/扫码统计）。
type DeviceDetailView struct {
	Device
	Online        bool            `json:"online"`
	Config        json.RawMessage `json:"config"`
	ConfigVersion int             `json:"config_version"`
	ScanTotal     int64           `json:"scan_total"`
	ScanToday     int64           `json:"scan_today"`
}

// DeviceActivationPayload 激活码载荷（device.ts DeviceActivation 契约对齐；
// plan §12.4 条 4：激活状态值冻结为大写枚举 PENDING/ACTIVATED，expired/disabled 为
// 前端派生态——本载荷同时携带 activation_status（冻结枚举）与 status（派生态））。
// qr_content 仅在创建/重新生成时返回：token 仅存哈希（plan §8.2），查询不可还原——
// 页面刷新丢失二维码时经 PUT /{id}/activation 重新生成。
type DeviceActivationPayload struct {
	DeviceID         database.ID       `json:"device_id"`
	DeviceCode       string            `json:"device_code"`
	ServerURL        string            `json:"server_url"`
	QRContent        string            `json:"qr_content,omitempty"`
	WarehouseID      int64             `json:"warehouse_id"`
	ActivationStatus string            `json:"activation_status"`
	Status           string            `json:"status"`
	ExpiresAt        database.JSONTime `json:"expires_at"`
	ActivatedAt      database.JSONTime `json:"activated_at"`
}

// qrContent 组装二维码内容（plan §8.2 冻结 JSON 三字段）。
func qrContent(serverURL, deviceCode, token string) string {
	b, err := json.Marshal(map[string]string{
		"server_url":  serverURL,
		"device_code": deviceCode,
		"token":       token,
	})
	if err != nil {
		return "" // map[string]string 序列化不会失败——防御兜底
	}
	return string(b)
}

// deriveActivationStatus 前端派生态（pending/activated/expired/disabled；device.ts:47）。
func deriveActivationStatus(d *Device, now time.Time) string {
	if d.Status == StatusDisabled {
		return "disabled"
	}
	if d.ActivationStatus == ActivationActivated {
		return "activated"
	}
	if !d.ActivationExpiresAt.IsZero() && !d.ActivationExpiresAt.After(now) {
		return "expired"
	}
	return "pending"
}

// activationPayload 组装激活载荷（withQR 时携带二维码内容）。serverURL 解析优先级：
// 装配固定值（WithServerURL，反向代理/多域名生产部署）> 请求推导（scheme://host）。
func (s *Service) activationPayload(d *Device, token, requestURL string) DeviceActivationPayload {
	serverURL := s.serverURL
	if serverURL == "" {
		serverURL = requestURL
	}
	p := DeviceActivationPayload{
		DeviceID:         d.ID,
		DeviceCode:       d.Code,
		ServerURL:        serverURL,
		WarehouseID:      d.WarehouseID,
		ActivationStatus: d.ActivationStatus,
		Status:           deriveActivationStatus(d, s.nowFn()),
		ExpiresAt:        d.ActivationExpiresAt,
		ActivatedAt:      d.ActivatedAt,
	}
	if token != "" {
		p.QRContent = qrContent(serverURL, d.Code, token)
	}
	return p
}

// ---- 设备注册（devices.md §6.1–§6.2）----

// DeviceCreateInput 新建设备入参（device.ts DeviceCreatePayload）。
type DeviceCreateInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Brand       string `json:"brand"`
	Model       string `json:"model"`
	WarehouseID int64  `json:"warehouse_id"` // 0=暂不绑定（devices.md §6.3 可后续绑定）
	Remark      string `json:"remark"`
}

// CreateDevice 注册设备并生成首张激活二维码（一次性 token 仅哈希落库——plan §8.2）。
// 同事务审计（plan §13.8：设备创建）。
func (s *Service) CreateDevice(ctx context.Context, actor Actor, in DeviceCreateInput, requestURL string) (*DeviceActivationPayload, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if !deviceCodePattern.MatchString(in.Code) {
		return nil, response.NewError(ErrDeviceCodeInvalid, ginH{"field": "code"})
	}
	if in.Name == "" || len([]rune(in.Name)) > 128 {
		return nil, response.NewError(ErrDeviceCodeInvalid, ginH{"field": "name", "reason": "设备名称必填且 ≤128 字"})
	}
	if !DeviceTypeValid(in.Type) {
		return nil, response.NewError(ErrDeviceTypeInvalid, ginH{"type": in.Type})
	}
	if len(in.Brand) > 64 || len(in.Model) > 64 {
		return nil, response.NewError(response.CodeInvalidParam, ginH{"field": "brand/model", "reason": "超长"})
	}
	if in.WarehouseID > 0 {
		ok, err := s.checkWarehouse(ctx, in.WarehouseID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, response.NewError(ErrWarehouseNotFound, ginH{"warehouse_id": in.WarehouseID})
		}
	}
	if existing, err := s.repo.FindDeviceByCode(ctx, in.Code); err == nil && existing != nil {
		return nil, response.NewError(ErrDeviceCodeConflict, ginH{"code": in.Code})
	}

	token, err := newActivationToken()
	if err != nil {
		return nil, err
	}
	now := s.nowFn()
	d := &Device{
		Code:                in.Code,
		Name:                in.Name,
		Type:                in.Type,
		Brand:               in.Brand,
		Model:               in.Model,
		WarehouseID:         in.WarehouseID,
		Status:              StatusEnabled,
		ActivationStatus:    ActivationPending,
		ActivationTokenHash: hashActivationToken(token),
		ActivationExpiresAt: database.JSONTime{Time: now.Add(activationTTL)},
		TokenVersion:        1,
		Remark:              in.Remark,
		CreatedBy:           actor.UserID,
		UpdatedBy:           actor.UserID,
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertDevice(tx, d); err != nil {
			return err
		}
		entry := actor.auditEntry("device", int64(d.ID), "create")
		entry.Success = true
		entry.After = map[string]any{"code": d.Code, "name": d.Name, "type": d.Type, "warehouse_id": d.WarehouseID}
		return middleware.Audit(tx, entry)
	})
	if err != nil {
		return nil, err
	}
	payload := s.activationPayload(d, token, requestURL)
	return &payload, nil
}

// checkWarehouse 仓库存在性校验（fail-closed：校验器未注入返回装配错误，不静默放行）。
func (s *Service) checkWarehouse(ctx context.Context, warehouseID int64) (bool, error) {
	if s.whChecker == nil {
		return false, response.NewError(ErrCheckerRequired, nil)
	}
	return s.whChecker.ExistsActive(ctx, warehouseID)
}

// ginH 别名（避免本文件散落 gin import——出参 details 用）。
type ginH = map[string]any

// WarehouseScope 数据权限仓库范围（permission.md §4；handler 自 auth.WarehouseScope
// 取值传入——列表过滤与单点端点可见性同源口径）。
type WarehouseScope struct {
	All bool
	IDs []int64
}

// visible 仓库是否在数据权限范围内（fail-closed：非 ALL 且 ID 不在集合 = 不可见，
// 按不存在处理防存在性探测；warehouse_id=0 未绑定仓库的设备对仓库受限用户同样不可见
// ——与列表过滤 warehouse_id IN (...) 口径一致）。
func (sc WarehouseScope) visible(warehouseID int64) bool {
	if sc.All {
		return true
	}
	for _, id := range sc.IDs {
		if id == warehouseID {
			return true
		}
	}
	return false
}

// loadVisibleDevice 装载设备并做仓库范围可见性校验（单点端点统一闸口——
// permission.md §4 数据权限在 Service 层过滤；不可见 = 不存在，防跨仓 ID 枚举）。
func (s *Service) loadVisibleDevice(ctx context.Context, id int64, scope WarehouseScope) (*Device, error) {
	d, err := s.repo.FindDevice(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, response.NewError(ErrDeviceNotFound, nil)
		}
		return nil, err
	}
	if !scope.visible(d.WarehouseID) {
		return nil, response.NewError(ErrDeviceNotFound, nil)
	}
	return d, nil
}

// ---- 设备查询 ----

// ListDevices 设备列表（type/status/warehouse_id/online/keyword 筛选 + 分页；
// 数据权限按 auth.WarehouseScope 注入 filter——permission.md §4）。
func (s *Service) ListDevices(ctx context.Context, f DeviceFilter) ([]*DeviceView, int64, error) {
	now := s.nowFn()
	if f.Online != nil {
		f.OnlineCutoff = now.Add(-onlineWindow)
	}
	rows, total, err := s.repo.ListDevices(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*DeviceView, 0, len(rows))
	for _, d := range rows {
		views = append(views, &DeviceView{Device: *d, Online: deviceOnline(d.LastOnlineAt, now)})
	}
	return views, total, nil
}

// deviceOnline 在线判定（plan §8.2：last_online_at 在 3 分钟内；纯函数供单测）。
func deviceOnline(last database.JSONTime, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	return now.Sub(last.Time) < onlineWindow && !now.Before(last.Time)
}

// GetDevice 设备详情（激活状态/心跳/扫码统计/配置——plan §8.1；仓库范围可见性校验）。
func (s *Service) GetDevice(ctx context.Context, id int64, scope WarehouseScope) (*DeviceDetailView, error) {
	d, err := s.loadVisibleDevice(ctx, id, scope)
	if err != nil {
		return nil, err
	}
	id = int64(d.ID)
	v := &DeviceDetailView{Device: *d, Online: deviceOnline(d.LastOnlineAt, s.nowFn())}
	if cfg, err := s.repo.FindDeviceConfig(ctx, id); err != nil {
		return nil, err
	} else if cfg != nil {
		v.Config = json.RawMessage(cfg.Config)
		v.ConfigVersion = cfg.Version
	}
	total, today, err := s.repo.CountScanLogs(ctx, id, startOfDay(s.nowFn()))
	if err != nil {
		return nil, err
	}
	v.ScanTotal, v.ScanToday = total, today
	return v, nil
}

// startOfDay 当日零点（scan_today 统计口径）。
func startOfDay(now time.Time) time.Time {
	t := now.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// GetActivation 激活状态查询（plan §8.1：前端待激活轮询依赖；不含 token——仅哈希存储；
// 仓库范围可见性校验）。
func (s *Service) GetActivation(ctx context.Context, id int64, requestURL string, scope WarehouseScope) (*DeviceActivationPayload, error) {
	d, err := s.loadVisibleDevice(ctx, id, scope)
	if err != nil {
		return nil, err
	}
	payload := s.activationPayload(d, "", requestURL)
	return &payload, nil
}

// ---- 激活码重新生成（plan §8.1：旧 token 作废，token_version+1）----

// RegenerateActivation 重新生成激活码：回退 PENDING、换新 token、旧设备令牌全失效
// （token_version+1——plan §8.2 撤销语义）。同事务审计。仓库范围可见性校验。
func (s *Service) RegenerateActivation(ctx context.Context, actor Actor, id int64, requestURL string, scope WarehouseScope) (*DeviceActivationPayload, error) {
	if _, err := s.loadVisibleDevice(ctx, id, scope); err != nil {
		return nil, err
	}
	token, err := newActivationToken()
	if err != nil {
		return nil, err
	}
	now := s.nowFn()
	var rows int64
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		rows, err = s.repo.RegenerateActivation(tx, id, hashActivationToken(token), now.Add(activationTTL), actor.UserID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return response.NewError(ErrDeviceStatusConflict, ginH{"reason": "设备已停用，无法重新生成激活码"})
		}
		entry := actor.auditEntry("device", id, "regenerate_activation")
		entry.Success = true
		return middleware.Audit(tx, entry)
	})
	if err != nil {
		return nil, err
	}
	d, err := s.repo.FindDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	payload := s.activationPayload(d, token, requestURL)
	return &payload, nil
}

// ---- 绑定/解绑（devices.md §6.4 共享设备；plan §8.1）----

// BindDevice 绑定操作者（devices §6.4：业务操作记录真实操作者——bound_user_id 裸 ID；
// 存在性校验无冻结跨域载体，见 DomainResult 未决项）。同事务审计。仓库范围可见性校验。
func (s *Service) BindDevice(ctx context.Context, actor Actor, id, userID int64, scope WarehouseScope) error {
	if userID <= 0 {
		return response.NewError(response.CodeInvalidParam, ginH{"field": "user_id", "reason": "必须为正整数"})
	}
	return s.updateBinding(ctx, actor, id, userID, "bind", false, scope)
}

// UnbindDevice 解绑（token_version+1——000013 注：解绑重置即旧设备令牌全失效）。
// 同事务审计。仓库范围可见性校验。
func (s *Service) UnbindDevice(ctx context.Context, actor Actor, id int64, scope WarehouseScope) error {
	return s.updateBinding(ctx, actor, id, 0, "unbind", true, scope)
}

func (s *Service) updateBinding(ctx context.Context, actor Actor, id, userID int64, action string, bump bool, scope WarehouseScope) error {
	if _, err := s.loadVisibleDevice(ctx, id, scope); err != nil {
		return err
	}
	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateDeviceBinding(tx, id, userID, actor.UserID, bump); err != nil {
			if errors.Is(err, ErrStatusConflict) {
				return response.NewError(ErrDeviceStatusConflict, ginH{"reason": "设备已停用"})
			}
			return err
		}
		entry := actor.auditEntry("device", id, action)
		entry.Success = true
		entry.After = map[string]any{"bound_user_id": userID, "token_version_bumped": bump}
		return middleware.Audit(tx, entry)
	})
}

// ---- 停用（devices.md §7.1；plan §8.1：token_version+1 设备端全部拒绝）----

// DisableDevice 停用设备：状态守卫 ENABLED→DISABLED + token_version+1（同事务两步，
// 任一步失败整体回滚）。同事务审计。仓库范围可见性校验（跨仓用户禁止停用他仓设备）。
func (s *Service) DisableDevice(ctx context.Context, actor Actor, id int64, scope WarehouseScope) error {
	if _, err := s.loadVisibleDevice(ctx, id, scope); err != nil {
		return err
	}
	return database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		rows, err := s.repo.UpdateDeviceStatus(tx, id, StatusEnabled, StatusDisabled, actor.UserID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return response.NewError(ErrDeviceStatusConflict, ginH{"reason": "设备不在启用状态"})
		}
		if err := s.repo.BumpTokenVersion(tx, id); err != nil {
			return err
		}
		entry := actor.auditEntry("device", id, "disable")
		entry.Success = true
		return middleware.Audit(tx, entry)
	})
}

// ---- 配置下发（devices.md §7.3；plan §8.1：device_configs upsert，version+1）----

// PutDeviceConfig 校验并下发配置（键白名单 + 值类型校验——api.md §4 后端完整校验）。
// 同事务审计。仓库范围可见性校验（跨仓用户禁止改他仓设备配置）。
func (s *Service) PutDeviceConfig(ctx context.Context, actor Actor, id int64, raw json.RawMessage, scope WarehouseScope) (int, error) {
	if _, err := s.loadVisibleDevice(ctx, id, scope); err != nil {
		return 0, err
	}
	clean, err := validateDeviceConfig(raw)
	if err != nil {
		return 0, err
	}
	var version int
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		version, err = s.repo.UpsertDeviceConfig(tx, id, jsonb(clean), actor.UserID)
		if err != nil {
			return err
		}
		entry := actor.auditEntry("device_config", id, "config")
		entry.Success = true
		entry.After = map[string]any{"config": json.RawMessage(clean)}
		return middleware.Audit(tx, entry)
	})
	return version, err
}

// validateDeviceConfig 配置白名单校验：顶层对象、键在 devices.md §7.3 冻结集、
// 值类型匹配；返回仅含合法键的规范化 JSON（键序稳定经 map→marshal，内容不变）。
func validateDeviceConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, response.NewError(ErrConfigInvalid, ginH{"reason": "配置不能为空"})
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, response.NewError(ErrConfigInvalid, ginH{"reason": "必须是 JSON 对象"})
	}
	clean := make(map[string]any, len(m))
	for k, v := range m {
		kind, ok := configKeyKinds[k]
		if !ok {
			return nil, response.NewError(ErrConfigInvalid, ginH{"key": k, "reason": "未知配置项"})
		}
		switch kind {
		case "bool":
			b, ok := v.(bool)
			if !ok {
				return nil, response.NewError(ErrConfigInvalid, ginH{"key": k, "reason": "必须为布尔值"})
			}
			clean[k] = b
		case "int":
			f, ok := v.(float64)
			if !ok || f != float64(int64(f)) || f < 0 {
				return nil, response.NewError(ErrConfigInvalid, ginH{"key": k, "reason": "必须为非负整数"})
			}
			clean[k] = int64(f)
		case "string":
			str, ok := v.(string)
			if !ok || str == "" || len(str) > 64 {
				return nil, response.NewError(ErrConfigInvalid, ginH{"key": k, "reason": "必须为 1-64 字符字符串"})
			}
			clean[k] = str
		}
	}
	out, err := json.Marshal(clean)
	if err != nil {
		return nil, fmt.Errorf("序列化配置失败: %w", err)
	}
	return out, nil
}

// ---- 设备端：激活（plan §8.2 激活协议）----

// ActivateInput 设备端激活入参。
type ActivateInput struct {
	DeviceCode string `json:"device_code"`
	Token      string `json:"token"`
	Brand      string `json:"brand"`
	Model      string `json:"model"`
	OS         string `json:"os"`
	AppVersion string `json:"app_version"`
}

// ActivationResult 激活响应（plan §8.2：device_token + warehouse + config——
// devices.md §6.2"自动获取服务器地址、设备编号、仓库、初始配置"）。
type ActivationResult struct {
	DeviceToken   string            `json:"device_token"`
	ExpiresAt     database.JSONTime `json:"expires_at"`
	DeviceID      database.ID       `json:"device_id"`
	DeviceCode    string            `json:"device_code"`
	DeviceName    string            `json:"device_name"`
	DeviceType    string            `json:"device_type"`
	WarehouseID   int64             `json:"warehouse_id"`
	Config        json.RawMessage   `json:"config"`
	ConfigVersion int               `json:"config_version"`
}

// Activate 设备激活：单条守卫 UPDATE 消费一次性激活码（PENDING + 哈希 + 未过期——
// 三条件同语句，重放/过期/错码统一失败，防重放裁决 plan §8.2），成功后签发设备令牌。
func (s *Service) Activate(ctx context.Context, in ActivateInput, ip string) (*ActivationResult, error) {
	in.DeviceCode = strings.TrimSpace(in.DeviceCode)
	in.Token = strings.TrimSpace(in.Token)
	if in.DeviceCode == "" || in.Token == "" || len(in.Token) > 128 {
		return nil, response.NewError(ErrDeviceActivationInvalid, nil)
	}
	info := DeviceSelfInfo{
		Brand:      truncate(in.Brand, 64),
		Model:      truncate(in.Model, 64),
		OS:         truncate(in.OS, 32),
		AppVersion: truncate(in.AppVersion, 32),
		IP:         truncate(ip, 64),
	}
	tokenHash := hashActivationToken(in.Token)
	now := s.nowFn()
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		rows, err := s.repo.ConsumeActivation(tx, in.DeviceCode, tokenHash, info, now)
		if err != nil {
			return err
		}
		if rows == 0 {
			// token 错误/已过期/已消费/编码不存在统一失败——不向设备端区分（防探测）。
			return response.NewError(ErrDeviceActivationInvalid, nil)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	d, err := s.repo.FindDeviceByCode(ctx, in.DeviceCode)
	if err != nil {
		return nil, err
	}
	token, exp, err := s.token.Issue(d)
	if err != nil {
		return nil, err
	}
	res := &ActivationResult{
		DeviceToken: token,
		ExpiresAt:   database.JSONTime{Time: exp},
		DeviceID:    d.ID,
		DeviceCode:  d.Code,
		DeviceName:  d.Name,
		DeviceType:  d.Type,
		WarehouseID: d.WarehouseID,
		Config:      json.RawMessage("null"),
	}
	if cfg, err := s.repo.FindDeviceConfig(ctx, int64(d.ID)); err != nil {
		return nil, err
	} else if cfg != nil {
		res.Config = json.RawMessage(cfg.Config)
		res.ConfigVersion = cfg.Version
	}
	return res, nil
}

// ---- 设备端：心跳（plan §8.2）----

// HeartbeatInput 心跳上报入参。
type HeartbeatInput struct {
	BatteryLevel *int   `json:"battery_level"`
	AppVersion   string `json:"app_version"`
	LastError    string `json:"last_error"`
}

// HeartbeatResult 心跳响应（config_version 有新版则设备端拉取——plan §8.2）。
type HeartbeatResult struct {
	ConfigVersion int               `json:"config_version"`
	ServerTime    database.JSONTime `json:"server_time"`
	Online        bool              `json:"online"`
}

// Heartbeat 心跳上报：更新 last_online_at/版本/电量，last_error 非空时落设备日志
// （ERROR 级——devices.md §7.2 最近错误数据源）。设备令牌已在中间件校验
// （ENABLED + ACTIVATED + token_version 匹配）。
func (s *Service) Heartbeat(ctx context.Context, dev DeviceContext, in HeartbeatInput, ip string) (*HeartbeatResult, error) {
	if in.BatteryLevel != nil && (*in.BatteryLevel < 0 || *in.BatteryLevel > 100) {
		return nil, response.NewError(ErrBatteryInvalid, nil)
	}
	if len(in.AppVersion) > 32 {
		return nil, response.NewError(response.CodeInvalidParam, ginH{"field": "app_version", "reason": "超长"})
	}
	now := s.nowFn()
	cols := map[string]any{"last_online_at": now, "updated_at": now}
	if in.BatteryLevel != nil {
		cols["battery_level"] = *in.BatteryLevel
	}
	if v := truncate(in.AppVersion, 32); v != "" {
		cols["app_version"] = v
	}
	if v := truncate(ip, 64); v != "" {
		cols["ip"] = v
	}
	err := s.repo.UpdateDeviceHeartbeat(s.repo.DB().WithContext(ctx), dev.ID, cols)
	if err != nil {
		return nil, err
	}
	if msg := strings.TrimSpace(in.LastError); msg != "" {
		logs := []*DeviceLog{{
			DeviceID:   dev.ID,
			Level:      LogLevelError,
			EventType:  "heartbeat_error",
			Message:    truncate(msg, 1024),
			OccurredAt: database.JSONTime{Time: now},
			CreatedAt:  database.JSONTime{Time: now},
		}}
		if err := s.repo.InsertDeviceLogs(s.repo.DB().WithContext(ctx), logs); err != nil {
			// 上报侧日志降级：心跳主路径不受影响（设备日志为辅助观测数据）。
			if s.logger != nil {
				s.logger.Error("device_logs 写入失败（心跳侧降级）", zap.Int64("device_id", dev.ID), zap.Error(err))
			}
		}
	}
	version := 0
	if cfg, err := s.repo.FindDeviceConfig(ctx, dev.ID); err != nil {
		return nil, err
	} else if cfg != nil {
		version = cfg.Version
	}
	return &HeartbeatResult{
		ConfigVersion: version,
		ServerTime:    database.JSONTime{Time: now},
		Online:        true,
	}, nil
}

// ---- 设备端：配置拉取 / 日志批量上报（plan §8.2）----

// DeviceConfigResult 设备端配置拉取响应。
type DeviceConfigResult struct {
	Config        json.RawMessage `json:"config"`
	ConfigVersion int             `json:"config_version"`
}

// GetSelfConfig 设备端拉取自身配置。
func (s *Service) GetSelfConfig(ctx context.Context, dev DeviceContext) (*DeviceConfigResult, error) {
	res := &DeviceConfigResult{Config: json.RawMessage("null")}
	if cfg, err := s.repo.FindDeviceConfig(ctx, dev.ID); err != nil {
		return nil, err
	} else if cfg != nil {
		res.Config = json.RawMessage(cfg.Config)
		res.ConfigVersion = cfg.Version
	}
	return res, nil
}

// maxDeviceLogBatch 设备日志单次上报上限（plan §8.2：≤100 条/次）。
const maxDeviceLogBatch = 100

// maxDeviceLogContextBytes 单条日志 context 字节上限（jsonb 诊断元数据；对齐 Message
// 截断宽度量级——超大数值/超长结构既撑爆行也借道 jsonb 数字精度把 INSERT 变 500）。
const maxDeviceLogContextBytes = 4096

// DeviceLogInput 设备日志上报行。
type DeviceLogInput struct {
	Level      string          `json:"level"`
	EventType  string          `json:"event_type"`
	Message    string          `json:"message"`
	Context    json.RawMessage `json:"context"`
	OccurredAt string          `json:"occurred_at"`
}

// UploadDeviceLogs 批量上报设备日志（≤100 条/次；occurred_at 为设备端时钟——
// YYYY-MM-DD HH:mm:ss 或 RFC3339）。
func (s *Service) UploadDeviceLogs(ctx context.Context, dev DeviceContext, in []DeviceLogInput) error {
	if len(in) == 0 || len(in) > maxDeviceLogBatch {
		return response.NewError(ErrDeviceLogBatchInvalid, ginH{"limit": maxDeviceLogBatch})
	}
	now := s.nowFn()
	logs := make([]*DeviceLog, 0, len(in))
	for i, item := range in {
		if !LogLevelValid(item.Level) {
			return response.NewError(ErrDeviceLogBatchInvalid, ginH{"index": i, "field": "level"})
		}
		at := now
		if item.OccurredAt != "" {
			var jt database.JSONTime
			if err := jt.UnmarshalJSON([]byte(`"` + item.OccurredAt + `"`)); err != nil {
				return response.NewError(ErrDeviceLogBatchInvalid, ginH{"index": i, "field": "occurred_at"})
			}
			at = jt.Time
		}
		ctxBytes := jsonb(item.Context)
		if len(ctxBytes) == 0 {
			ctxBytes = nil
		} else {
			// context 落 jsonb 列：非法 JSON/超大数值会使 INSERT 失败致整批上报 500——
			// 合法性 + 尺寸双校验（fail-closed 400 定位到行），服务端不透传不合法载荷。
			if !json.Valid(ctxBytes) {
				return response.NewError(ErrDeviceLogBatchInvalid, ginH{"index": i, "field": "context"})
			}
			if len(ctxBytes) > maxDeviceLogContextBytes {
				return response.NewError(ErrDeviceLogBatchInvalid, ginH{"index": i, "field": "context", "limit": maxDeviceLogContextBytes})
			}
		}
		logs = append(logs, &DeviceLog{
			DeviceID:   dev.ID,
			Level:      item.Level,
			EventType:  truncate(item.EventType, 64),
			Message:    truncate(item.Message, 4096),
			Context:    ctxBytes,
			OccurredAt: database.JSONTime{Time: at},
			CreatedAt:  database.JSONTime{Time: now},
		})
	}
	return s.repo.InsertDeviceLogs(s.repo.DB().WithContext(ctx), logs)
}

// ---- App 版本查询（devices.md §7.4；plan §15：M3 仅 latest，无写入端点）----

// LatestAppVersion 查询指定平台最新已发布版本（无已发布行 → DEVICE_APP_VERSION_NOT_FOUND，
// plan §15 字面冻结）。
func (s *Service) LatestAppVersion(ctx context.Context, platform string) (*AppVersion, error) {
	if platform != PlatformAndroid {
		return nil, response.NewError(ErrPlatformInvalid, ginH{"platform": platform})
	}
	v, err := s.repo.FindLatestAppVersion(ctx, platform)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, response.NewError(ErrAppVersionNotFound, nil)
		}
		return nil, err
	}
	return v, nil
}

// ---- 设备令牌认证（DeviceAuthRequired 中间件的 Service 支撑）----

// AuthenticateDevice 校验 Bearer 设备令牌：验签 + DB 行状态复查（status=ENABLED AND
// activated AND token_version 匹配——plan §8.2，每次请求查 DB）。返回设备上下文。
func (s *Service) AuthenticateDevice(ctx context.Context, raw string) (DeviceContext, error) {
	claims, err := s.token.Parse(raw)
	if err != nil {
		return DeviceContext{}, err
	}
	d, err := s.repo.FindDevice(ctx, claims.DeviceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeviceContext{}, response.NewError(ErrDeviceTokenInvalid, nil)
		}
		return DeviceContext{}, response.NewError(response.CodeServiceUnavailable, ginH{"reason": "设备数据不可用"})
	}
	if d.Status != StatusEnabled || d.ActivationStatus != ActivationActivated ||
		d.TokenVersion != claims.Version || d.Code != claims.Code {
		return DeviceContext{}, response.NewError(ErrDeviceTokenInvalid, nil)
	}
	return DeviceContext{ID: int64(d.ID), Code: d.Code, Type: d.Type, WarehouseID: d.WarehouseID}, nil
}

// truncate 字符串截断（列宽对齐——写入入口统一截断，超宽会使 INSERT 失败）。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
