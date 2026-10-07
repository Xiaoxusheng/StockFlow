package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 认证权限域业务层（architecture.md §1：业务逻辑/状态机/事务边界/权限校验）。
// 依赖以窄接口注入（Repository / redisStore），单元测试以接口替身替换（ask 约束）。
type Service struct {
	repo    Repository
	store   *sessionStore
	guard   *loginGuard
	pwGuard *loginGuard // S15：改密原密码失败保护（与登录保护独立阈值/窗口/键前缀）
	rdb     redisStore  // 登录验证码存取（captcha.go；与 guard 同一 Redis 实例）
	cfg     runtimeConfig
	checker WarehouseChecker // plan §4.3：用户绑定仓库的存在性校验（router 装配注入）
}

// NewService 构建业务层。
func NewService(repo Repository, store *sessionStore, guard *loginGuard, cfg runtimeConfig) *Service {
	svc := &Service{repo: repo, store: store, guard: guard, cfg: cfg}
	if guard != nil {
		// S15：改密失败保护与登录保护共用 Redis，但阈值/窗口/键前缀独立（互不干扰）。
		svc.pwGuard = &loginGuard{rdb: guard.rdb, maxFailures: changePwMaxFailures,
			window: changePwWindow, prefix: "pwdfail"}
		// 登录验证码（captcha.go）与失败保护共用同一 Redis 实例（窄接口 redisStore）。
		svc.rdb = guard.rdb
	}
	return svc
}

// useChecker 注入跨域仓库校验器（RegisterProtectedRoutes 装配期调用一次）。
func (s *Service) useChecker(c WarehouseChecker) { s.checker = c }

// Actor 操作者上下文（handler 自 gin 上下文提取后传入 Service，供审计归因）。
// DataScope/DeptID/WarehouseIDs 为会话数据范围快照（plan §7.4），授予侧特权边界（S1）
// 与用户目录收敛（S9）的判定数据源。
type Actor struct {
	UserID       int64
	Username     string
	IsSuper      bool
	DataScope    string
	DeptID       int64
	WarehouseIDs []int64
	RequestID    string
	IP           string
	UserAgent    string
	Method       string
	Path         string
}

// auditEntry 构造 auth 域审计条目骨架（module=auth；快照由调用方补充）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "auth",
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

// ---- 视图 DTO（Model/DTO 分离：PasswordHash 等内部字段不出 API，api-and-data 规范）----

// UserView 用户视图。
type UserView struct {
	ID                 database.ID       `json:"id"`
	Username           string            `json:"username"`
	RealName           string            `json:"real_name"`
	Phone              string            `json:"phone"`
	Email              string            `json:"email"`
	DepartmentID       *database.ID      `json:"department_id"`
	DataScope          string            `json:"data_scope"`
	Status             string            `json:"status"`
	MustChangePassword bool              `json:"must_change_password"`
	LockedUntil        database.JSONTime `json:"locked_until"`
	LastLoginAt        database.JSONTime `json:"last_login_at"`
	// LastLoginIP 仅用户详情（auth:user:read）返回；列表响应由 GetUsers 显式清空
	// 并 omitempty（安全审查 S9：用户目录不泄露他人登录 IP）。
	LastLoginIP  string            `json:"last_login_ip,omitempty"`
	CreatedAt    database.JSONTime `json:"created_at"`
	UpdatedAt    database.JSONTime `json:"updated_at"`
	RoleIDs      []database.ID     `json:"role_ids,omitempty"`      // 详情返回
	WarehouseIDs []database.ID     `json:"warehouse_ids,omitempty"` // 详情返回
}

// viewUser Model → DTO。
func viewUser(u *User) *UserView {
	return &UserView{
		ID:                 u.ID,
		Username:           u.Username,
		RealName:           u.RealName,
		Phone:              u.Phone,
		Email:              u.Email,
		DepartmentID:       u.DepartmentID,
		DataScope:          u.DataScope,
		Status:             u.Status,
		MustChangePassword: u.MustChangePassword,
		LockedUntil:        u.LockedUntil,
		LastLoginAt:        u.LastLoginAt,
		LastLoginIP:        u.LastLoginIP,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
	}
}

// RoleView 角色视图。
type RoleView struct {
	ID            database.ID       `json:"id"`
	Code          string            `json:"code"`
	Name          string            `json:"name"`
	IsSystem      bool              `json:"is_system"`
	Status        string            `json:"status"`
	CreatedAt     database.JSONTime `json:"created_at"`
	UpdatedAt     database.JSONTime `json:"updated_at"`
	PermissionIDs []database.ID     `json:"permission_ids,omitempty"` // 详情返回
	UserCount     int64             `json:"user_count,omitempty"`     // 详情返回
}

func viewRole(r *Role) *RoleView {
	return &RoleView{
		ID: r.ID, Code: r.Code, Name: r.Name, IsSystem: r.IsSystem,
		Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// DepartmentView 部门视图。
type DepartmentView struct {
	ID        database.ID       `json:"id"`
	ParentID  *database.ID      `json:"parent_id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
}

func viewDept(d *Department) *DepartmentView {
	return &DepartmentView{
		ID: d.ID, ParentID: d.ParentID, Code: d.Code, Name: d.Name,
		Status: d.Status, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// PermissionView 权限点视图。
type PermissionView struct {
	ID       database.ID  `json:"id"`
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Type     string       `json:"type"`
	ParentID *database.ID `json:"parent_id"`
	Sort     int          `json:"sort"`
	Status   string       `json:"status"`
}

func viewPerm(p *Permission) *PermissionView {
	return &PermissionView{
		ID: p.ID, Code: p.Code, Name: p.Name, Type: p.Type,
		ParentID: p.ParentID, Sort: p.Sort, Status: p.Status,
	}
}

// ---- 登录 / 刷新 / 登出 ----

// LoginInput 登录入参（IP/UserAgent/RequestID 由 handler 注入，不信任 body 传入）。
type LoginInput struct {
	Username  string
	Password  string
	IP        string
	UserAgent string
	RequestID string
}

// LoginResult 登录/刷新结果。
type LoginResult struct {
	AccessToken        string    `json:"access_token"`
	TokenType          string    `json:"token_type"` // 恒为 Bearer
	ExpiresIn          int64     `json:"expires_in"` // Access Token 有效期（秒）
	RefreshToken       string    `json:"refresh_token"`
	MustChangePassword bool      `json:"must_change_password"`
	User               *UserView `json:"user"`
}

// Login 登录（plan §7.3 全流程：保护校验 → bcrypt → 锁定计数 → 会话登记 → JWT 签发 → login_logs）。
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	username := strings.TrimSpace(in.Username)
	if username == "" || in.Password == "" {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"fields": []string{"username", "password"}, "reason": "用户名与密码必填",
		})
	}
	if len(username) > 64 || len(in.Password) > maxPasswordLen {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"reason": "用户名或密码长度非法",
		})
	}

	logEntry := middleware.LoginLogEntry{Username: username, IP: in.IP, UserAgent: in.UserAgent}

	// S4（IP 维度防喷洒）：IP 失败计数达到阈值即拒绝——对客户端统一 AUTH_CREDENTIALS_INVALID，
	// 真实原因（AUTH_IP_LOCKED）写入 login_logs；先于用户查询执行，喷洒请求不触达用户表。
	// Redis 故障时计数缺位 → 预检放行（与用户名维度一致 fail-open，见下）。
	ipCount, cerr := s.guard.Count(ctx, failIPDim(in.IP))
	if cerr != nil {
		ipCount = 0
	}
	if ipCount >= int64(s.cfg.maxLoginFailures) {
		s.writeLoginLog(ctx, middleware.LoginLogEntry{IP: in.IP, UserAgent: in.UserAgent},
			"AUTH_IP_LOCKED", in.RequestID)
		return nil, response.NewError(ErrCredentialsInvalid, nil)
	}

	u, err := s.repo.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}

	if u == nil {
		// 防账号枚举：不存在也执行一次同代价 bcrypt 比较，统一 AUTH_CREDENTIALS_INVALID（plan §7.3）。
		_ = VerifyPassword(dummyHash, in.Password)
		// S4：未知用户名的失败计入 IP 维度（防跨用户名喷洒）；无用户行可锁，仅计数。
		_, _ = s.guard.Fail(ctx, failIPDim(in.IP))
		s.writeLoginLog(ctx, logEntry, "AUTH_CREDENTIALS_INVALID", in.RequestID)
		return nil, response.NewError(ErrCredentialsInvalid, nil)
	}
	logEntry.UserID = u.ID.Int64()

	if u.Status == UserStatusDisabled {
		// S8（防枚举，复核确认的预言机）：停用账户对客户端与"用户名或密码错误"统一文案，
		// 不再回显 AUTH_ACCOUNT_DISABLED——否则攻击者可借此枚举有效用户名并探知账户状态；
		// 真实原因仅写入 login_logs 供审计追溯。
		s.writeLoginLog(ctx, logEntry, "AUTH_ACCOUNT_DISABLED", in.RequestID)
		return nil, response.NewError(ErrCredentialsInvalid, nil)
	}
	// 锁定窗口内直接拒绝（不做 bcrypt，锁定期间无暴力破解面）。
	if !u.LockedUntil.IsZero() && u.LockedUntil.After(time.Now()) {
		// S8：同上统一 AUTH_CREDENTIALS_INVALID，不再回显 locked_until（枚举预言机）。
		s.writeLoginLog(ctx, logEntry, "AUTH_ACCOUNT_LOCKED", in.RequestID)
		return nil, response.NewError(ErrCredentialsInvalid, nil)
	}

	if !VerifyPassword(u.PasswordHash, in.Password) {
		// 失败计数（permission.md §3.2）：用户名 + IP 双维度（S4）。
		// 用户名维度达到阈值 → 锁定目标账户行（落库，进程重启不丢）；
		// IP 维度达到阈值 → 由上方入口预检对同 IP 后续请求统一拒绝（不触发账户行锁，
		// 避免喷洒者以他人 IP 维度计数锁死无辜账户）。
		// Redis 故障时计数缺位→本轮锁定保护缺位但登录可用性优先（限流类功能 fail-open，
		// go-dev-standard 缓存故障策略的例外面），锁定落库路径不受影响。
		count, ferr := s.guard.Fail(ctx, username)
		if ferr != nil {
			count = 0
		}
		_, _ = s.guard.Fail(ctx, failIPDim(in.IP)) // IP 维度计数由入口预检消费；计数失败 fail-open
		// 豁免用户名（SF_AUTH_LOCK_EXEMPT，默认 bootstrap 管理员）跳过用户名维度的账户行锁：
		// admin 必建且不可删，若可被锁，攻击者每窗口 maxLoginFailures 次错密即可无限维持
		// 锁定（拒绝服务）。IP 维度限流与失败计数全部保留——防喷洒防线不受豁免影响。
		if count >= int64(s.cfg.maxLoginFailures) && !s.cfg.isLockExempt(username) {
			until := time.Now().Add(s.cfg.lockDuration)
			if lerr := s.repo.LockUser(ctx, u.ID.Int64(), until); lerr != nil {
				return nil, lerr
			}
			// 锁定审计（安全渗透修复：锁定事件必须可追溯）。
			s.auditUserLocked(ctx, u, until, in)
		}
		s.writeLoginLog(ctx, logEntry, "AUTH_CREDENTIALS_INVALID", in.RequestID)
		return nil, response.NewError(ErrCredentialsInvalid, nil)
	}

	// 登录成功：清空用户名维度失败计数（best-effort：计数残余仅影响后续窗口，不阻断本次登录）。
	// IP 维度不清——成功登录不得复位喷洒者累计的 IP 预算。
	_ = s.guard.Reset(ctx, username)

	isSuper, err := s.repo.ListRoleByCodeForUser(ctx, u.ID.Int64(), SuperAdminRoleCode)
	if err != nil {
		return nil, err
	}
	whIDs, err := s.repo.ListWarehouseIDsByUser(ctx, u.ID.Int64())
	if err != nil {
		return nil, err
	}

	now := time.Now()
	sess := &sessionData{
		UserID:             u.ID.Int64(),
		Username:           u.Username,
		IsSuper:            isSuper,
		DataScope:          u.DataScope,
		WarehouseIDs:       whIDs,
		DeptID:             deptIDOf(u),
		MustChangePassword: u.MustChangePassword,
		IP:                 in.IP,
		UserAgent:          in.UserAgent,
		LoginAt:            now,
		LastActiveAt:       now,
	}

	// 事务：登录信息更新 + login_logs 落库（permission.md §3.3 每次登录必记）。
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, u.ID.Int64(), map[string]any{
			"last_login_at": database.JSONTime{Time: now},
			"last_login_ip": in.IP,
		}); err != nil {
			return err
		}
		logEntry.Success = true
		return middleware.WriteLoginLog(tx, logEntry)
	})
	if err != nil {
		return nil, err
	}

	// 会话登记（Access JWT + Refresh Token 双轨，plan §7.2）。
	if err := s.store.createWithTokens(ctx, sess); err != nil {
		return nil, err
	}
	token, exp, err := issueAccessToken(s.cfg, sess.UserID, sess.SID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		AccessToken:        token,
		TokenType:          "Bearer",
		ExpiresIn:          int64(time.Until(exp).Seconds()),
		RefreshToken:       sess.RefreshToken,
		MustChangePassword: sess.MustChangePassword,
		User:               viewUser(u),
	}, nil
}

// RefreshInput 刷新入参。
type RefreshInput struct {
	RefreshToken string
	IP           string
	UserAgent    string
	RequestID    string
}

// Refresh 刷新 Access Token（plan §7.2：轮换 sid 并作废旧会话；刷新时重载数据范围快照，
// 缩小 plan §13.3 快照漂移窗口）。刷新事件写 login_logs。
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (*LoginResult, error) {
	if in.RefreshToken == "" {
		return nil, response.NewError(ErrRefreshInvalid, nil)
	}
	logEntry := middleware.LoginLogEntry{IP: in.IP, UserAgent: in.UserAgent}

	old, err := s.store.FindByRefreshToken(ctx, in.RefreshToken)
	if err != nil {
		return nil, err
	}
	if old == nil {
		logEntry.Username = "unknown"
		s.writeLoginLog(ctx, logEntry, "AUTH_REFRESH_INVALID", in.RequestID)
		return nil, response.NewError(ErrRefreshInvalid, nil)
	}
	logEntry.UserID = old.UserID

	// 刷新凭证绑定 User-Agent（f3：被盗凭证跨设备重放防护）。会话创建时已登记
	// 登录 UA；刷新请求 UA 与登记不一致 = 凭证离开原设备的强特征——拒绝刷新
	// （ErrRefreshInvalid，不泄漏区分性信息），但不删除会话：正牌用户不受影响
	// （其后续请求 UA 一致仍可刷新），盗用者持有的凭证在本服务不可续期。
	// 仅绑定 UA 不绑定 IP——移动办公跨网切换属正常行为，IP 绑定会误伤；
	// 登记端 UA 为空（历史会话/特殊客户端）时跳过校验保持兼容。
	if old.UserAgent != "" && strings.TrimSpace(in.UserAgent) != old.UserAgent {
		logEntry.Username = old.Username
		s.writeLoginLog(ctx, logEntry, "AUTH_REFRESH_INVALID", in.RequestID)
		return nil, response.NewError(ErrRefreshInvalid, nil)
	}

	u, err := s.repo.FindUserByID(ctx, old.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		// 用户已删除：会话立即失效（fail-closed）。
		_ = s.store.Delete(ctx, old)
		logEntry.Username = old.Username
		s.writeLoginLog(ctx, logEntry, "AUTH_REFRESH_INVALID", in.RequestID)
		return nil, response.NewError(ErrRefreshInvalid, nil)
	}
	logEntry.Username = u.Username
	if u.Status == UserStatusDisabled {
		_ = s.store.Delete(ctx, old)
		s.writeLoginLog(ctx, logEntry, "AUTH_ACCOUNT_DISABLED", in.RequestID)
		return nil, response.NewError(ErrAccountDisabled, nil)
	}

	// 刷新时重载快照（角色/范围/仓库绑定可能已变更；plan §7.4 快照来源）。
	isSuper, err := s.repo.ListRoleByCodeForUser(ctx, u.ID.Int64(), SuperAdminRoleCode)
	if err != nil {
		return nil, err
	}
	whIDs, err := s.repo.ListWarehouseIDsByUser(ctx, u.ID.Int64())
	if err != nil {
		return nil, err
	}
	old.IsSuper = isSuper
	old.DataScope = u.DataScope
	old.WarehouseIDs = whIDs
	old.DeptID = deptIDOf(u)
	old.MustChangePassword = u.MustChangePassword

	fresh, err := s.store.Rotate(ctx, old)
	if err != nil {
		return nil, err
	}

	logEntry.Success = true
	if err := middleware.WriteLoginLog(s.repo.DB(), logEntry); err != nil {
		// 登录日志缺位不阻断刷新（刷新已通过认证）；S7：不再静默丢弃，记 zap error 供审计排查。
		logLoginLogWriteFailed(err, logEntry, "", in.RequestID)
	}
	token, exp, err := issueAccessToken(s.cfg, fresh.UserID, fresh.SID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		AccessToken:        token,
		TokenType:          "Bearer",
		ExpiresIn:          int64(time.Until(exp).Seconds()),
		RefreshToken:       fresh.RefreshToken,
		MustChangePassword: fresh.MustChangePassword,
		User:               viewUser(u),
	}, nil
}

// Logout 登出：删除会话与刷新索引（Token 立即失效），写 login_logs。
func (s *Service) Logout(ctx context.Context, sess *sessionData, logIP, logUA string) error {
	if sess != nil {
		if err := s.store.Delete(ctx, sess); err != nil {
			return err
		}
	}
	entry := middleware.LoginLogEntry{UserID: sessUserID(sess), Username: sessUsername(sess),
		IP: logIP, UserAgent: logUA, Success: true}
	if err := middleware.WriteLoginLog(s.repo.DB(), entry); err != nil {
		// 同 Refresh：登出已生效，日志缺位不阻断；S7：记 zap error（登出路径无 request_id，留空）。
		logLoginLogWriteFailed(err, entry, "", "")
	}
	return nil
}

// MeResult /api/auth/me 响应（permission.md §5：前端路由/按钮权限的数据源）。
type MeResult struct {
	User               *UserView `json:"user"`
	IsSuper            bool      `json:"is_super"`
	DataScope          string    `json:"data_scope"`
	WarehouseIDs       []int64   `json:"warehouse_ids"`
	DeptID             int64     `json:"dept_id"`
	MustChangePassword bool      `json:"must_change_password"`
	Permissions        []string  `json:"permissions"`
}

// Me 当前用户信息 + 权限点集（must_change_password 以会话快照为准）。
func (s *Service) Me(ctx context.Context, uc UserContext, sess *sessionData) (*MeResult, error) {
	u, err := s.repo.FindUserByID(ctx, uc.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewError(ErrSessionInvalid, nil)
	}
	perms, err := s.PermissionCodes(ctx, uc.UserID)
	if err != nil {
		return nil, err
	}
	if perms == nil {
		perms = []string{} // JSON 输出空数组而非 null，前端 reduce 安全
	}
	whIDs := uc.WarehouseIDs
	if whIDs == nil {
		whIDs = []int64{}
	}
	return &MeResult{
		User:               viewUser(u),
		IsSuper:            uc.IsSuper,
		DataScope:          uc.DataScope,
		WarehouseIDs:       whIDs,
		DeptID:             uc.DeptID,
		MustChangePassword: sessMustChange(sess, uc),
		Permissions:        perms,
	}, nil
}

// changePwMaxFailures/changePwWindow 改密原密码失败保护参数（安全审查 S15）：
// 独立于登录保护的低阈值——键维度 username|ip，连续错误 5 次 / 15 分钟窗口。
const (
	changePwMaxFailures = 5
	changePwWindow      = 15 * time.Minute
)

// ChangePassword 修改本人密码（permission.md §3.2 密码修改；plan §7.3 强密码策略）。
// 原密码校验接入失败计数与短期锁定（S15：达到阈值后窗口内拒绝再次尝试，即使原密码正确）；
// 成功后：失败计数清零、must_change_password 复位、本人其余会话强制下线、审计（快照不含任何密码值）。
func (s *Service) ChangePassword(ctx context.Context, sess *sessionData, actor Actor, oldPw, newPw string) error {
	if sess == nil {
		return response.NewError(ErrSessionInvalid, nil)
	}
	// S15：先做阈值预检（fail-open：Redis 故障时计数缺位放行，与登录保护同一策略）。
	dim := sess.Username + "|" + actor.IP
	if n, cerr := s.pwGuard.Count(ctx, dim); cerr == nil && n >= changePwMaxFailures {
		return response.NewError(ErrAccountLocked, nil)
	}
	if err := ValidatePassword(newPw); err != nil {
		return err
	}
	u, err := s.repo.FindUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	if u == nil {
		return response.NewError(ErrUserNotFound, nil)
	}
	if !VerifyPassword(u.PasswordHash, oldPw) {
		// S15：原密码错误计入失败保护（best-effort），并写 login_logs 审计（真实原因可追溯）。
		_, _ = s.pwGuard.Fail(ctx, dim)
		s.writeLoginLog(ctx, middleware.LoginLogEntry{
			UserID: u.ID.Int64(), Username: u.Username, IP: actor.IP, UserAgent: actor.UserAgent,
		}, "AUTH_PASSWORD_MISMATCH", actor.RequestID)
		return response.NewError(ErrPasswordMismatch, nil)
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		return err
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, u.ID.Int64(), map[string]any{
			"password_hash":        hash,
			"must_change_password": false,
			"updated_by":           database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		// 审计快照仅记录标记位变化，绝不记录密码（architecture.md §6 日志红线）。
		e := actor.auditEntry("user", u.ID.Int64(), "change-password")
		e.Before = map[string]any{"must_change_password": u.MustChangePassword}
		e.After = map[string]any{"must_change_password": false}
		e.Success = true
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}

	// 本会话复位 must_change_password（免重登），其余会话全部强制下线（旧密码凭证即刻失效）。
	sess.MustChangePassword = false
	if err := s.store.Save(ctx, sess); err != nil {
		return err
	}
	// S15：改密成功清空该维度的原密码失败计数（best-effort）。
	_ = s.pwGuard.Reset(ctx, sess.Username+"|"+actor.IP)
	return s.kickUserSessions(ctx, sess.UserID, sess.SID, actor)
}

// kickUserSessions 强制下线某用户除 keepSID 外的全部会话。
func (s *Service) kickUserSessions(ctx context.Context, uid int64, keepSID string, actor Actor) error {
	sessions, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	for _, d := range sessions {
		if d.UserID != uid || d.SID == keepSID {
			continue
		}
		if err := s.store.Delete(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

// ---- 在线会话管理（permission.md §3.4）----

// SessionView 会话视图。
type SessionView struct {
	SessionID    string            `json:"session_id"`
	UserID       database.ID       `json:"user_id"`
	Username     string            `json:"username"`
	IsSuper      bool              `json:"is_super"`
	IP           string            `json:"ip"`
	UserAgent    string            `json:"user_agent"`
	LoginAt      database.JSONTime `json:"login_at"`
	LastActiveAt database.JSONTime `json:"last_active_at"`
	Current      bool              `json:"current"` // 是否当前请求会话
}

// ListSessions 在线会话列表（SCAN 汇总；运维视图，plan §7.2）。
// 在线会话量级 = 并发用户数（小），内存分页满足强制分页约定（api.md §2.1）。
func (s *Service) ListSessions(ctx context.Context) ([]*SessionView, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LoginAt.After(list[j].LoginAt) })
	out := make([]*SessionView, 0, len(list))
	for _, d := range list {
		out = append(out, &SessionView{
			SessionID:    d.SID,
			UserID:       database.ID(d.UserID),
			Username:     d.Username,
			IsSuper:      d.IsSuper,
			IP:           d.IP,
			UserAgent:    d.UserAgent,
			LoginAt:      database.JSONTime{Time: d.LoginAt},
			LastActiveAt: database.JSONTime{Time: d.LastActiveAt},
		})
	}
	return out, nil
}

// KickSession 强制下线（permission.md §3.4/§6 敏感操作：强制审计）。
// 先删会话（Token 立即失效）后审计；审计失败返回 500，会话已失效可幂等重试核验。
func (s *Service) KickSession(ctx context.Context, actor Actor, sid string) error {
	if sid == "" {
		return response.NewError(response.CodeInvalidParam, map[string]any{"field": "id", "reason": "必填"})
	}
	target, err := s.store.Get(ctx, sid)
	if err != nil {
		return err
	}
	if target == nil {
		return response.NewError(ErrSessionNotFound, nil)
	}
	if err := s.store.Delete(ctx, target); err != nil {
		return err
	}

	e := actor.auditEntry("session", 0, "kick")
	e.After = map[string]any{
		"session_id": sid, "target_user_id": target.UserID, "target_username": target.Username,
	}
	e.Success = true
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	// 若踢的是当前登录用户自己之外的同用户会话，无需额外处理；Redis 会话键已即时生效。
	return nil
}

// ---- 权限点读取（RequirePermission / Me 共用）----

// PermissionCodes 用户权限点集：Redis 缓存优先，未命中回源 DB 并回填（TTL 权限缓存）。
// RBAC 写路径主动失效（DelPerms），TTL 兜底（plan §13.3 权限缓存版本失效精神的 M1 简化）。
func (s *Service) PermissionCodes(ctx context.Context, uid int64) ([]string, error) {
	codes, hit, err := s.store.GetPerms(ctx, uid)
	if err == nil && hit {
		return codes, nil
	}
	codes, err = s.repo.ListPermissionCodesByUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	s.store.SetPerms(ctx, uid, codes, s.cfg.permsCacheTTL)
	return codes, nil
}

// ---- 内部辅助 ----

// writeLoginLog 写登录失败日志（best-effort：失败原因入 login_logs.fail_reason）。
// S7：写失败不再 `_ = err` 静默丢弃——记 zap error（含 request_id/用户名）供审计完整性排查；
// 认证结论不因日志缺位改变。
func (s *Service) writeLoginLog(ctx context.Context, e middleware.LoginLogEntry, reason, requestID string) {
	e.Success = false
	e.FailReason = reason
	if err := middleware.WriteLoginLog(s.repo.DB(), e); err != nil {
		logLoginLogWriteFailed(err, e, reason, requestID)
	}
}

// auditUserLocked 账户锁定审计事件（operation_logs：module=auth / object_type=user /
// action=locked，即 auth.user.locked；安全渗透修复：锁定事件必须可追溯）。
// 复用 middleware.Audit 统一审计通道（audit.go 冻结契约：禁止任何包另建第二套审计写入；
// kick/change-password 同款经 database.Tx 落库）。
// 落位说明：LockUser 为独立短路径更新（repository.go，不经事务参数），审计无法与其
// 同事务——锁定是已生效的保护动作，审计缺位不应回滚锁本身，故与 login_logs 写失败
// 同口径（S7）：失败记 error 日志供完整性排查，不改变本次认证结论。
// After 快照仅含用户名/截止时间/原因，不含密码与 Token（architecture.md §6 日志红线）。
func (s *Service) auditUserLocked(ctx context.Context, u *User, until time.Time, in LoginInput) {
	e := middleware.AuditEntry{
		Module:     "auth",
		ObjectType: "user",
		Action:     "locked",
		ObjectID:   u.ID.Int64(),
		IP:         in.IP,
		UserAgent:  in.UserAgent,
		RequestID:  in.RequestID,
		Success:    true,
		After: map[string]any{
			"username":     u.Username,
			"locked_until": until.Format(time.RFC3339),
			"reason":       "login_failures_exceeded",
		},
	}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		return middleware.Audit(tx, e)
	})
	if err != nil {
		businessLogger().Error("operation_logs 账户锁定审计写入失败（审计缺位，需完整性排查）",
			zap.Error(err),
			zap.String("request_id", in.RequestID),
			zap.String("username", u.Username),
			zap.String("locked_until", until.Format(time.RFC3339)),
		)
	}
}

// createWithTokens 生成 sid + refresh token 并登记会话。
func (s *sessionStore) createWithTokens(ctx context.Context, d *sessionData) error {
	sid, err := newOpaqueToken()
	if err != nil {
		return err
	}
	tok, err := newOpaqueToken()
	if err != nil {
		return err
	}
	d.SID = sid
	d.RefreshToken = tok
	return s.Create(ctx, d)
}

// Save 覆盖会话内容（改密后复位 must_change_password 等场景）；TTL 重置为滑动窗口。
func (s *sessionStore) Save(ctx context.Context, d *sessionData) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	return s.rdb.Set(ctx, sessionKey(d.SID), raw, s.cfg.refreshTTL).Err()
}

// GetPerms 读权限缓存；hit=false 未命中。
func (s *sessionStore) GetPerms(ctx context.Context, uid int64) ([]string, bool, error) {
	raw, err := s.rdb.Get(ctx, permsKey(uid)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return nil, false, fmt.Errorf("解析权限缓存失败: %w", err)
	}
	return codes, true, nil
}

// SetPerms 写权限缓存（调用方负责失效时 DelPerms）。
func (s *sessionStore) SetPerms(ctx context.Context, uid int64, codes []string, ttl time.Duration) {
	if codes == nil {
		codes = []string{}
	}
	_ = s.rdb.Set(ctx, permsKey(uid), codes, ttl).Err() // 缓存写失败仅损失命中，回源路径不受影响
}

// DelPerms 定向失效权限缓存（RBAC 变更路径调用，plan §13.3）。
func (s *sessionStore) DelPerms(ctx context.Context, uids ...int64) error {
	if len(uids) == 0 {
		return nil
	}
	keys := make([]string, 0, len(uids))
	for _, uid := range uids {
		keys = append(keys, permsKey(uid))
	}
	return s.rdb.Del(ctx, keys...).Err()
}

func deptIDOf(u *User) int64 {
	if u.DepartmentID == nil {
		return 0
	}
	return u.DepartmentID.Int64()
}

func sessUserID(d *sessionData) int64 {
	if d == nil {
		return 0
	}
	return d.UserID
}

func sessUsername(d *sessionData) string {
	if d == nil {
		return ""
	}
	return d.Username
}

// sessMustChange 强制改密标记：会话快照为准（UserContext 冻结结构不含该字段）；
// sess 缺位时退回 false（仅测试装配可达）。
func sessMustChange(sess *sessionData, _ UserContext) bool {
	if sess == nil {
		return false
	}
	return sess.MustChangePassword
}
