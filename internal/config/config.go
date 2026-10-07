// Package config 加载后端配置：config.yaml + SF_ 前缀环境变量。
// 优先级：环境变量 > 配置文件 > 内置默认值（deployment.md §1：配置不写死，
// 采用环境变量 + 配置文件 + Secret）。启动完整性校验失败快速报错（deployment.md §3）。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 后端全部配置。扩展新配置项时三处同步：结构体字段 + defaults() + config.example.yaml。
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Log      LogConfig      `mapstructure:"log"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	CORS     CORSConfig     `mapstructure:"cors"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Queue    QueueConfig    `mapstructure:"queue"`
	Datax    DataxConfig    `mapstructure:"datax"`
	Sysops   SysopsConfig   `mapstructure:"sysops"`

	warnings []string // 非阻断启动告警（Validate 收集，main 在日志装配后以 zap warn 输出）
}

// StorageConfig 文件中心本地存储（backend-m3-plan §3.2；OSS 留接口不做）。
type StorageConfig struct {
	// Root 文件中心本地存储根（deployment §1"文件存储（本地/OSS）"）。
	Root string `mapstructure:"root"`
	// UploadMaxBytes 上传上限（字节）：/api/imports、/api/files 路由组局部中间件覆盖全局
	// 1MB（api.md §5 文件大小上限）。
	UploadMaxBytes int64 `mapstructure:"upload_max_bytes"`
}

// QueueConfig 异步任务队列（backend-m3-plan §3.2；asynq 复用 redis.* 连接配置，
// redis.enabled=false 时队列走 inline 同步实现——plan §4.1）。
type QueueConfig struct {
	// Concurrency asynq 并发 worker 数。
	Concurrency int `mapstructure:"concurrency"`
	// MaxRetry asynq 最大重试（指数退避为 asynq 内建策略）。
	MaxRetry int `mapstructure:"max_retry"`
}

// DataxConfig Excel 导入导出参数（backend-m3-plan §3.2、excel §6）。
type DataxConfig struct {
	// ImportMaxRows 单次导入行数上限，超限 4xx DATAX_TOO_MANY_ROWS。
	ImportMaxRows int `mapstructure:"import_max_rows"`
	// BatchSize 导入分批事务批大小（excel §6.2：批内行级原子，批间独立）。
	BatchSize int `mapstructure:"batch_size"`
	// ExportBatchSize 导出流式批次（keyset 游标，禁止 offset 深翻页——plan §6.3）。
	ExportBatchSize int `mapstructure:"export_batch_size"`
	// FileRetentionDays 任务产物/上传文件有效期（files.expires_at 推导，file_cleanup 清理）。
	FileRetentionDays int `mapstructure:"file_retention_days"`
}

// SysopsConfig 平台运维参数（backend-m3-plan §3.2）。
type SysopsConfig struct {
	// BackupRetentionDays 备份文件/记录保留期（file_cleanup 执行清理——plan §10.5）。
	BackupRetentionDays int `mapstructure:"backup_retention_days"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Mode            string        `mapstructure:"mode"` // debug | release | test（透传 gin.SetMode）
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	// TrustedProxies 受信任反向代理网段（IP 或 CIDR，gin SetTrustedProxies）。
	// 空 = 不信任任何代理，ClientIP 直取直连 RemoteAddr（安全审查 S5：gin 默认信任
	// 全部代理，会放行 X-Forwarded-For 伪造，必须显式覆盖）。
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// MaxBodyBytes 全局请求体上限（字节，http.MaxBytesReader；安全审查 S6）。
	MaxBodyBytes int64 `mapstructure:"max_body_bytes"`
	// DeviceActivateRatePerMinute /api/devices/activate 免凭证写端点的 IP 维度滑动窗口
	// 限流（次/分钟；安全渗透修复——一次性激活码即凭证，属可暴力尝试的暴露面）。
	DeviceActivateRatePerMinute int `mapstructure:"device_activate_rate_per_minute"`
	// PublicBaseURL 对外公布的服务基础地址（如 https://wms.example.com；安全渗透修复）。
	// 非空时固定设备激活二维码的 server_url——缺省按请求 scheme://host 推导可被伪造
	// Host 操纵，使设备扫码指向攻击者端点；空 = 完全沿用请求推导（本机/直连部署）。
	PublicBaseURL string `mapstructure:"public_base_url"`
}

// AuthConfig 认证入口防护（限流中间件实现于 internal/middleware，用户名维度的
// 严格限流归 internal/auth 域 guard——config 只承载平台层参数）。
type AuthConfig struct {
	// RateLimitIPPerMinute /api/auth 公开组 IP 维度滑动窗口限流（次/分钟，安全审查 S4）。
	RateLimitIPPerMinute int `mapstructure:"rate_limit_ip_per_minute"`
}

// LogConfig 日志配置（分类日志由 internal/logger 构建）。
type LogConfig struct {
	Level  string `mapstructure:"level"`  // debug | info | warn | error
	Format string `mapstructure:"format"` // json | console
}

// DatabaseConfig PostgreSQL(pgx) 连接与连接池（architecture.md §11.4：池参数显式可配，禁止默认值上线）。
type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"` // 禁止写死，用 SF_DATABASE_PASSWORD 注入
	Name            string        `mapstructure:"name"`
	SSLMode         string        `mapstructure:"sslmode"`
	AutoMigrate     bool          `mapstructure:"auto_migrate"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time"`
	// StatementTimeout 单条 SQL 服务端执行超时（f31 慢查询闸门；pgx 启动参数
	// statement_timeout，毫秒）。默认 2 分钟：正常 OLTP 远低于此值；无超时的慢报表
	// 查询此前可无限占住连接池直至拖垮全站。0 = 关闭（显式禁用口）。
	StatementTimeout time.Duration `mapstructure:"statement_timeout"`
}

// DSN pgx keyword/value 连接串。gorm.io/driver/postgres 默认即 pgx/v5 驱动；
// password/dbname 单引号包裹，允许包含空格等特殊字符。
// StatementTimeout>0 时追加 statement_timeout 启动参数（pgx 将未知键作为服务端
// 会话参数下发，PG 对每条语句到时取消并回滚——reports/datax 全链路无应用层
// context 超时的兜底闸门）。
func (d DatabaseConfig) DSN() string {
	dsn := fmt.Sprintf("host=%s port=%d user='%s' password='%s' dbname='%s' sslmode=%s TimeZone=UTC",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode)
	if d.StatementTimeout > 0 {
		dsn += fmt.Sprintf(" statement_timeout=%d", d.StatementTimeout.Milliseconds())
	}
	return dsn
}

// RedisConfig go-redis v9 连接与连接池。
type RedisConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Addr         string `mapstructure:"addr"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	PoolSize     int    `mapstructure:"pool_size"`
	MinIdleConns int    `mapstructure:"min_idle_conns"`
}

// CORSConfig 白名单（architecture.md §6）。
type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

// defaults 内置默认值：未提供 config.yaml 时服务仍可启动（CI/冒烟场景），
// 生产环境必须显式提供配置。键路径同时用于环境变量推导（SF_ 前缀）。
func defaults() map[string]any {
	return map[string]any{
		// server.mode 默认 release（安全审查 S2）：gin 默认值收紧，本地开发请显式设
		// SF_SERVER_MODE=debug；release 下 auth 的 JWT 密钥缺失 fail-fast 自然生效。
		"server.mode": "release",
		"server.port": 8080,
		// f21：WriteTimeout 覆盖整个响应体写出——200MB 导出产物（datax.ExportMaxFileBytes）
		// 与 inline 同步导出在常规带宽下需 8~80s+，15s 会强制中断下载/导出；
		// ReadTimeout 给 ≤20MB 上传留出弱网余量。
		"server.read_timeout":     60 * time.Second,
		"server.write_timeout":    300 * time.Second,
		"server.shutdown_timeout": 10 * time.Second,
		"server.trusted_proxies":  []string{},     // 空 = 不信任任何代理（安全审查 S5）
		"server.max_body_bytes":   int64(1) << 20, // 1MB（安全审查 S6）
		// /api/devices/activate 免凭证写端点 IP 限流（安全渗透修复，middleware.AuthIPRateLimit）
		"server.device_activate_rate_per_minute": 30,
		"server.public_base_url":                 "", // 空 = 激活二维码 server_url 沿用请求 Host 推导（旧行为）
		"auth.rate_limit_ip_per_minute":          30, // /api/auth 公开组 IP 限流（安全审查 S4）
		"log.level":                              "info",
		"log.format":                             "json",
		"database.host":                          "127.0.0.1",
		"database.port":                          5432,
		"database.user":                          "postgres",
		"database.password":                      "",
		"database.name":                          "stockflow",
		"database.sslmode":                       "disable",
		"database.auto_migrate":                  false,
		"database.max_open_conns":                50,
		"database.max_idle_conns":                10,
		"database.conn_max_lifetime":             time.Hour,
		"database.conn_max_idle_time":            10 * time.Minute,
		// f31：单条 SQL 服务端超时（2 分钟）——慢报表/慢查询占住连接池的兜底闸门。
		"database.statement_timeout": 2 * time.Minute,
		"redis.enabled":              true,
		"redis.addr":                 "127.0.0.1:6379",
		"redis.password":             "",
		"redis.db":                   0,
		"redis.pool_size":            50,
		"redis.min_idle_conns":       5,
		"cors.allowed_origins":       []string{"http://localhost:5173"},
		// M3 平台基座键（backend-m3-plan §3.2 冻结清单）
		"storage.root":                 "./data/files",
		"storage.upload_max_bytes":     int64(20971520), // 20MB（api.md §5 文件大小上限）
		"queue.concurrency":            10,
		"queue.max_retry":              3,
		"datax.import_max_rows":        5000,
		"datax.batch_size":             200,
		"datax.export_batch_size":      1000,
		"datax.file_retention_days":    30,
		"sysops.backup_retention_days": 14,
	}
}

// envKey 键路径 → 环境变量名：database.password → SF_DATABASE_PASSWORD。
func envKey(key string) string {
	return "SF_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// Load 读取配置。path 为空时仅用默认值 + 环境变量；否则文件必须存在。
// 列表类配置（cors.allowed_origins、server.trusted_proxies）环境变量用逗号分隔
// （viper 环境变量对 []string 不生效，显式处理）：
// SF_CORS_ALLOWED_ORIGINS=https://a,https://b、SF_SERVER_TRUSTED_PROXIES=127.0.0.1/32,10.0.0.0/8
func Load(path string) (*Config, error) {
	v := viper.New()
	for key, val := range defaults() {
		v.SetDefault(key, val)
		_ = v.BindEnv(key, envKey(key)) // 显式绑定，保证 Unmarshal 时环境变量生效
	}
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if raw := strings.TrimSpace(os.Getenv("SF_CORS_ALLOWED_ORIGINS")); raw != "" {
		parts := strings.Split(raw, ",")
		origins := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				origins = append(origins, p)
			}
		}
		cfg.CORS.AllowedOrigins = origins
	}
	if raw := strings.TrimSpace(os.Getenv("SF_SERVER_TRUSTED_PROXIES")); raw != "" {
		parts := strings.Split(raw, ",")
		proxies := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				proxies = append(proxies, p)
			}
		}
		cfg.Server.TrustedProxies = proxies
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate 启动完整性校验（deployment.md §3：失败快速报错退出，禁止带病启动）。
func (c *Config) Validate() error {
	var errs []error

	switch c.Server.Mode {
	case "debug", "release", "test":
	default:
		errs = append(errs, fmt.Errorf("server.mode 必须是 debug/release/test，当前 %q", c.Server.Mode))
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port 越界: %d", c.Server.Port))
	}
	if c.Server.ReadTimeout <= 0 || c.Server.WriteTimeout <= 0 || c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server 超时参数必须 > 0"))
	}
	if c.Server.MaxBodyBytes < 1 {
		errs = append(errs, fmt.Errorf("server.max_body_bytes 必须 > 0，当前 %d", c.Server.MaxBodyBytes))
	}
	for _, p := range c.Server.TrustedProxies {
		// 接受 IP 或 CIDR（与 gin SetTrustedProxies 同语义：不带 "/" 的按单 IP 处理）。
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				errs = append(errs, fmt.Errorf("server.trusted_proxies 含非法网段 %q（需形如 10.0.0.0/8 或单 IP）", p))
			}
		}
	}
	if c.Auth.RateLimitIPPerMinute < 1 {
		errs = append(errs, fmt.Errorf("auth.rate_limit_ip_per_minute 必须 >= 1，当前 %d", c.Auth.RateLimitIPPerMinute))
	}
	if c.Server.DeviceActivateRatePerMinute < 1 {
		errs = append(errs, fmt.Errorf("server.device_activate_rate_per_minute 必须 >= 1，当前 %d", c.Server.DeviceActivateRatePerMinute))
	}
	if c.Server.PublicBaseURL != "" {
		// 非空即要求形如 scheme://host 的合法基础地址（激活二维码 server_url 的固定值，
		// 拼错会使设备扫码指向不可达/错误端点）。
		u, err := url.Parse(c.Server.PublicBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("server.public_base_url 非法 %q（需形如 https://wms.example.com）", c.Server.PublicBaseURL))
		}
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level 必须是 debug/info/warn/error，当前 %q", c.Log.Level))
	}
	switch c.Log.Format {
	case "json", "console":
	default:
		errs = append(errs, fmt.Errorf("log.format 必须是 json/console，当前 %q", c.Log.Format))
	}

	if c.Database.Host == "" {
		errs = append(errs, errors.New("database.host 不能为空"))
	}
	if c.Database.Port < 1 || c.Database.Port > 65535 {
		errs = append(errs, fmt.Errorf("database.port 越界: %d", c.Database.Port))
	}
	if c.Database.User == "" {
		errs = append(errs, errors.New("database.user 不能为空"))
	}
	if c.Database.Name == "" {
		errs = append(errs, errors.New("database.name 不能为空"))
	}
	switch c.Database.SSLMode { // pgx/gorm 合法取值（防拼错静默失效）
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		errs = append(errs, fmt.Errorf("database.sslmode 非法 %q（disable/allow/prefer/require/verify-ca/verify-full）", c.Database.SSLMode))
	}
	if c.Database.MaxOpenConns < 1 || c.Database.MaxIdleConns < 1 || c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		errs = append(errs, fmt.Errorf("数据库连接池参数非法: max_open_conns=%d max_idle_conns=%d",
			c.Database.MaxOpenConns, c.Database.MaxIdleConns))
	}

	if c.Redis.Enabled {
		if c.Redis.Addr == "" {
			errs = append(errs, errors.New("redis.enabled=true 时 redis.addr 不能为空"))
		}
		if c.Redis.PoolSize < 1 || c.Redis.MinIdleConns < 0 || c.Redis.MinIdleConns > c.Redis.PoolSize {
			errs = append(errs, fmt.Errorf("redis 连接池参数非法: pool_size=%d min_idle_conns=%d",
				c.Redis.PoolSize, c.Redis.MinIdleConns))
		}
	}

	// M3 平台基座键（backend-m3-plan §3.2）：存储/队列/导入导出/备份保留期
	if c.Storage.Root == "" {
		errs = append(errs, errors.New("storage.root 不能为空"))
	}
	if c.Storage.UploadMaxBytes < 1 {
		errs = append(errs, fmt.Errorf("storage.upload_max_bytes 必须 > 0，当前 %d", c.Storage.UploadMaxBytes))
	}
	if c.Queue.Concurrency < 1 {
		errs = append(errs, fmt.Errorf("queue.concurrency 必须 >= 1，当前 %d", c.Queue.Concurrency))
	}
	if c.Queue.MaxRetry < 0 {
		errs = append(errs, fmt.Errorf("queue.max_retry 不得为负，当前 %d", c.Queue.MaxRetry))
	}
	if c.Datax.ImportMaxRows < 1 {
		errs = append(errs, fmt.Errorf("datax.import_max_rows 必须 >= 1，当前 %d", c.Datax.ImportMaxRows))
	}
	if c.Datax.BatchSize < 1 {
		errs = append(errs, fmt.Errorf("datax.batch_size 必须 >= 1，当前 %d", c.Datax.BatchSize))
	}
	if c.Datax.ExportBatchSize < 1 {
		errs = append(errs, fmt.Errorf("datax.export_batch_size 必须 >= 1，当前 %d", c.Datax.ExportBatchSize))
	}
	if c.Datax.FileRetentionDays < 1 {
		errs = append(errs, fmt.Errorf("datax.file_retention_days 必须 >= 1，当前 %d", c.Datax.FileRetentionDays))
	}
	if c.Sysops.BackupRetentionDays < 1 {
		errs = append(errs, fmt.Errorf("sysops.backup_retention_days 必须 >= 1，当前 %d", c.Sysops.BackupRetentionDays))
	}

	for _, origin := range c.CORS.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("cors.allowed_origins 含非法来源 %q（需形如 https://example.com）", origin))
		}
	}

	// 生产取值告警（安全审查 S12）：不阻断启动——同机 loopback 部署（应用与 PG 同机、
	// 流量不出内核）下 disable 属可接受部署形态，交由部署规范裁决；跨机生产必须
	// verify-full（deployment.md §1.1）。Validate 先于 main 的日志装配执行，无法直接
	// 持有 zap 实例，故收集后由 main 以 zap warn 输出（Warnings()）。
	c.warnings = nil
	if c.Server.Mode == "release" && c.Database.SSLMode == "disable" {
		c.warnings = append(c.warnings,
			"database.sslmode=disable 且 server.mode=release：数据库连接未加密（同机 loopback 场景可接受；生产跨机部署应改用 verify-full，见 config.example.yaml）")
	}

	return errors.Join(errs...)
}

// Warnings 启动告警（非阻断）：Validate 收集，main 在分类日志装配后以 zap warn 逐条输出。
func (c *Config) Warnings() []string { return c.warnings }
