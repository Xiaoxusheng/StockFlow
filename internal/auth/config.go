package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// 运行时配置（plan §7.2/§7.3 默认值，环境变量可覆盖）。
//
// 落位说明：auth 域禁止 import internal/config（plan §3：依赖值由构造函数注入），
// 而 RegisterPublicRoutes/RegisterProtectedRoutes 为冻结三参签名（plan §5.1），
// 无法携带配置——因此本域配置经 SF_AUTH_* 环境变量解析（与 cmd 读取
// SF_ADMIN_INITIAL_PASSWORD 同一通道，deployment.md §1 环境变量 + Secret）。
// 禁止硬编码 Secret（go-dev-standard security：生产密钥一律注入）：
// gin release 模式下 SF_AUTH_JWT_SECRET 缺失即 panic 快速失败（deployment.md §3
// 禁止带病启动）；debug/test 模式（本地冒烟、单测）缺失时降级为进程内随机密钥，
// 重启即失效，并已在 doc.go 显式说明。
type runtimeConfig struct {
	jwtSecret        []byte        // HS256 签名密钥
	issuer           string        // JWT iss
	accessTTL        time.Duration // Access JWT 有效期（plan §7.2 默认 2h）
	refreshTTL       time.Duration // Refresh Token/会话滑动 TTL（plan §7.2 默认 7d）
	maxLoginFailures int           // 连续失败锁定阈值（plan §7.3 默认 5）
	lockDuration     time.Duration // 锁定时长（plan §7.3 默认 15 分钟）
	lockExempt       []string      // 用户名维度账户锁定的豁免清单（bootstrap 管理员防拒绝服务）
	permsCacheTTL    time.Duration // 权限缓存 TTL（失效由 RBAC 写路径主动清除兜底）
	captchaTTL       time.Duration // 登录验证码有效期（captcha.go 默认 3m）
}

// 环境变量名（SF_AUTH_ 前缀；与 viper 的 SF_ 约定一致但独立解析——auth 不 import config）。
const (
	envJWTSecret     = "SF_AUTH_JWT_SECRET"
	envAccessTTL     = "SF_AUTH_ACCESS_TTL"
	envRefreshTTL    = "SF_AUTH_REFRESH_TTL"
	envMaxLoginFails = "SF_AUTH_MAX_LOGIN_FAILURES"
	envLockDuration  = "SF_AUTH_LOCK_DURATION"
	envLockExempt    = "SF_AUTH_LOCK_EXEMPT"
	envCaptchaTTL    = "SF_AUTH_CAPTCHA_TTL"
)

// plan 冻结默认值（backend-m1-plan §7.2/§7.3）。
const (
	defaultAccessTTL     = 2 * time.Hour
	defaultRefreshTTL    = 7 * 24 * time.Hour
	defaultMaxLoginFails = 5
	defaultLockDuration  = 15 * time.Minute
	defaultPermsCacheTTL = 10 * time.Minute
	defaultCaptchaTTL    = 3 * time.Minute
	jwtIssuer            = "stockflow-auth"
	// minJWTSecretLen HS256 密钥最小长度（安全渗透修复：16 字节弱密钥可离线爆破，
	// 收紧为 32 字节 ≈ 256bit，与随机降级密钥长度对齐）。
	minJWTSecretLen = 32
)

// defaultLockExemptUsers 用户名维度锁定的默认豁免清单：bootstrap 管理员（必建且不可删，
// database.AdminUsername）。豁免仅跳过账户行锁——IP 维度限流与失败计数全部保留，
// 防喷洒防线不受影响；SF_AUTH_LOCK_EXEMPT（逗号分隔用户名）可覆盖。
var defaultLockExemptUsers = []string{database.AdminUsername}

// resolveRuntimeConfig 解析运行时配置：环境变量 > plan 默认值。
func resolveRuntimeConfig() (runtimeConfig, error) {
	cfg := runtimeConfig{
		issuer:           jwtIssuer,
		accessTTL:        defaultAccessTTL,
		refreshTTL:       defaultRefreshTTL,
		maxLoginFailures: defaultMaxLoginFails,
		lockDuration:     defaultLockDuration,
		permsCacheTTL:    defaultPermsCacheTTL,
		captchaTTL:       defaultCaptchaTTL,
	}

	if v := os.Getenv(envAccessTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("%s 必须为正时长（如 2h）: %q", envAccessTTL, v)
		}
		cfg.accessTTL = d
	}
	if v := os.Getenv(envRefreshTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("%s 必须为正时长（如 168h）: %q", envRefreshTTL, v)
		}
		cfg.refreshTTL = d
	}
	if v := os.Getenv(envMaxLoginFails); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("%s 必须为 >=1 的整数: %q", envMaxLoginFails, v)
		}
		cfg.maxLoginFailures = n
	}
	if v := os.Getenv(envLockDuration); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("%s 必须为正时长（如 15m）: %q", envLockDuration, v)
		}
		cfg.lockDuration = d
	}
	if v := os.Getenv(envCaptchaTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("%s 必须为正时长（如 3m）: %q", envCaptchaTTL, v)
		}
		cfg.captchaTTL = d
	}

	// 锁定豁免清单（安全渗透修复）：默认豁免 bootstrap 管理员——admin 必建且不可删，
	// 若可被用户名维度锁定，攻击者每窗口 maxLoginFailures 次错密即可无限维持锁定
	// （拒绝服务）。SF_AUTH_LOCK_EXEMPT（逗号分隔用户名）覆盖默认值；IP 维度限流
	// 与失败计数不受豁免影响。
	cfg.lockExempt = append([]string(nil), defaultLockExemptUsers...)
	if v := os.Getenv(envLockExempt); v != "" {
		exempt := make([]string, 0, 4)
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				exempt = append(exempt, name)
			}
		}
		cfg.lockExempt = exempt
	}

	secret := os.Getenv(envJWTSecret)
	switch {
	case len(secret) > 0:
		// 弱密钥双重拒绝（安全渗透修复：HS256 密钥可被离线爆破，长度与熵都要把关）：
		// ① 长度 < minJWTSecretLen；② 仅含单一字符类别（字母/数字/符号三类中只含一类，
		// 如纯字母重复串）——该形态实质熵远低于长度暗示，一并拒绝。
		if len(secret) < minJWTSecretLen {
			return cfg, fmt.Errorf("%s 长度不足 %d 字节，拒绝弱密钥启动", envJWTSecret, minJWTSecretLen)
		}
		if jwtSecretClassCount(secret) < 2 {
			return cfg, fmt.Errorf("%s 仅含单一字符类别（字母/数字/符号），拒绝弱密钥启动", envJWTSecret)
		}
		cfg.jwtSecret = []byte(secret)
	case gin.Mode() == gin.ReleaseMode:
		return cfg, fmt.Errorf("%s 未设置：release 模式禁止以未知密钥启动（deployment.md §1/§3；debug 模式才会降级为进程内随机密钥）", envJWTSecret)
	default:
		// debug/test 模式：进程内随机密钥（重启即全部 Token 失效，安全但仅限本地冒烟）。
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return cfg, fmt.Errorf("生成临时 JWT 密钥失败: %w", err)
		}
		cfg.jwtSecret = []byte(base64.RawURLEncoding.EncodeToString(raw))
	}
	return cfg, nil
}

// jwtSecretClassCount 统计密钥覆盖的字符类别数（字母/数字/符号三类）。
// 弱密钥判据（安全渗透修复）：类别数 < 2 即"单一字符类别"（如全字母/全数字）。
func jwtSecretClassCount(secret string) int {
	var letter, digit, symbol bool
	for _, r := range secret {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		default:
			symbol = true
		}
	}
	n := 0
	for _, hit := range []bool{letter, digit, symbol} {
		if hit {
			n++
		}
	}
	return n
}

// isLockExempt 用户名是否豁免用户名维度账户锁定（SF_AUTH_LOCK_EXEMPT 可覆盖默认清单）。
// 仅豁免账户行锁（LockUser）；IP 维度限流与失败计数全部保留（S4 防喷洒不受影响）。
func (c runtimeConfig) isLockExempt(username string) bool {
	for _, name := range c.lockExempt {
		if name == username {
			return true
		}
	}
	return false
}

// deps 包级装配态：Register* 写入，冻结签名的 AuthRequired/RequirePermission 读取。
// router 装配先于任何请求（plan §5.3），包级单例与 response.SetErrorLogger 同一模式。
type deps struct {
	mu      sync.RWMutex
	wired   bool
	cfg     runtimeConfig
	store   *sessionStore
	guard   *loginGuard
	repo    Repository
	svc     *Service
	checker WarehouseChecker // plan §4.3：用户绑定仓库的存在性校验（warehouse.NewChecker 注入）
}

var appDeps deps

// buildDeps 由 db/rdb 构建全部域内依赖。rdb 允许为 nil（redis.enabled=false 或 T0 测试
// 装配，该形态下认证不可用、无路由可达）：归一化为 nil 接口，避免 nil *redis.Client
// 装入接口后判空失效。
func buildDeps(db *gorm.DB, rdb *redis.Client, cfg runtimeConfig) (store *sessionStore, guard *loginGuard, repo Repository, svc *Service) {
	var rs redisStore
	if rdb != nil {
		rs = rdb
	}
	repo = NewRepository(db)
	store = newSessionStore(rs, cfg)
	guard = &loginGuard{rdb: rs, maxFailures: cfg.maxLoginFailures, window: cfg.lockDuration}
	svc = NewService(repo, store, guard, cfg)
	return store, guard, repo, svc
}

// ensureWired 幂等装配：首次调用解析配置并构建依赖；重复调用直接返回（router 顺序调用
// RegisterPublic/RegisterProtected 属正常路径）。配置非法（弱密钥、非法时长、release 缺
// 密钥）时 panic 快速失败（启动期配置错误，与 response.Register 同一模式）。
func ensureWired(db *gorm.DB, rdb *redis.Client) {
	appDeps.mu.Lock()
	defer appDeps.mu.Unlock()
	if appDeps.wired {
		return
	}
	// 真实装配必须带 Redis 会话存储（plan §7.2 双轨依赖；redis.enabled=false 无法支撑认证）。
	// db==nil 仅存在于测试装配（T0 路由装配断言用例），豁免。
	if db != nil && rdb == nil {
		panic("auth 装配失败: Redis 会话存储不可用（redis.enabled=false 不支撑认证；plan §7.2 JWT+会话双轨依赖 Redis）")
	}
	cfg, err := resolveRuntimeConfig()
	if err != nil {
		panic("auth 装配失败: " + err.Error())
	}
	store, guard, repo, svc := buildDeps(db, rdb, cfg)
	appDeps.cfg = cfg
	appDeps.store = store
	appDeps.guard = guard
	appDeps.repo = repo
	appDeps.svc = svc
	appDeps.wired = true
}

// setWiredForTest 测试专用：以给定配置与替身直接装配（绕过环境变量解析与 panic 路径）。
func setWiredForTest(cfg runtimeConfig, repo Repository, rdb redisStore) {
	appDeps.mu.Lock()
	defer appDeps.mu.Unlock()
	if repo == nil {
		repo = NewRepository(nil)
	}
	store := newSessionStore(rdb, cfg)
	guard := &loginGuard{rdb: rdb, maxFailures: cfg.maxLoginFailures, window: cfg.lockDuration}
	appDeps.cfg = cfg
	appDeps.store = store
	appDeps.guard = guard
	appDeps.repo = repo
	appDeps.svc = NewService(repo, store, guard, cfg)
	appDeps.wired = true
}

// resetWiredForTest 测试专用：复位装配态（逐字段清空——禁止整体替换结构体，
// 那会连带替换处于锁定状态的 mu，触发 "Unlock of unlocked RWMutex"）。
func resetWiredForTest() {
	appDeps.mu.Lock()
	defer appDeps.mu.Unlock()
	appDeps.wired = false
	appDeps.cfg = runtimeConfig{}
	appDeps.store = nil
	appDeps.guard = nil
	appDeps.repo = nil
	appDeps.svc = nil
	appDeps.checker = nil
}

// snapshotWired 取当前装配快照（中间件请求路径使用；未装配返回 wired=false）。
func snapshotWired() (runtimeConfig, *sessionStore, *loginGuard, Repository, *Service, WarehouseChecker, bool) {
	appDeps.mu.RLock()
	defer appDeps.mu.RUnlock()
	if !appDeps.wired {
		return runtimeConfig{}, nil, nil, nil, nil, nil, false
	}
	return appDeps.cfg, appDeps.store, appDeps.guard, appDeps.repo, appDeps.svc, appDeps.checker, true
}
