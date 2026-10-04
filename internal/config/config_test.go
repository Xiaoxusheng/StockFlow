package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)

	// 默认 release（安全审查 S2：gin 默认值收紧；本地开发显式设 SF_SERVER_MODE=debug）
	require.Equal(t, "release", cfg.Server.Mode)
	require.Equal(t, 8080, cfg.Server.Port) // 默认端口与前端代理对齐（backend-m1-plan §1）
	require.Equal(t, "info", cfg.Log.Level)
	require.Equal(t, "json", cfg.Log.Format)
	require.Equal(t, "stockflow", cfg.Database.Name)
	require.False(t, cfg.Database.AutoMigrate)
	require.Equal(t, 50, cfg.Database.MaxOpenConns)
	require.True(t, cfg.Redis.Enabled)
	require.Equal(t, 50, cfg.Redis.PoolSize)
	require.Equal(t, []string{"http://localhost:5173"}, cfg.CORS.AllowedOrigins)
	// 新增平台防护默认值（安全审查 S4/S5/S6）
	require.Empty(t, cfg.Server.TrustedProxies) // 空 = 不信任任何代理
	require.Equal(t, int64(1)<<20, cfg.Server.MaxBodyBytes)
	require.Equal(t, 30, cfg.Auth.RateLimitIPPerMinute)
	// M3 平台基座默认值（backend-m3-plan §3.2 冻结清单）
	require.Equal(t, "./data/files", cfg.Storage.Root)
	require.Equal(t, int64(20971520), cfg.Storage.UploadMaxBytes)
	require.Equal(t, 10, cfg.Queue.Concurrency)
	require.Equal(t, 3, cfg.Queue.MaxRetry)
	require.Equal(t, 5000, cfg.Datax.ImportMaxRows)
	require.Equal(t, 200, cfg.Datax.BatchSize)
	require.Equal(t, 1000, cfg.Datax.ExportBatchSize)
	require.Equal(t, 30, cfg.Datax.FileRetentionDays)
	require.Equal(t, 14, cfg.Sysops.BackupRetentionDays)
}

func TestLoadFromYAMLAndEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "server:\n  port: 9090\ndatabase:\n  password: from-file\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	t.Setenv("SF_SERVER_PORT", "9091") // 环境变量优先于配置文件（deployment.md §1）
	t.Setenv("SF_DATABASE_PASSWORD", "from-env")
	t.Setenv("SF_CORS_ALLOWED_ORIGINS", "https://a.example.com, https://b.example.com")
	t.Setenv("SF_SERVER_TRUSTED_PROXIES", "127.0.0.1/32, 10.0.0.0/8") // 列表类：逗号分隔（deployment.md §1.1）
	t.Setenv("SF_SERVER_MAX_BODY_BYTES", "4096")
	t.Setenv("SF_AUTH_RATE_LIMIT_IP_PER_MINUTE", "120")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 9091, cfg.Server.Port)
	require.Equal(t, "from-env", cfg.Database.Password)
	require.Equal(t, []string{"https://a.example.com", "https://b.example.com"}, cfg.CORS.AllowedOrigins)
	require.Equal(t, []string{"127.0.0.1/32", "10.0.0.0/8"}, cfg.Server.TrustedProxies)
	require.Equal(t, int64(4096), cfg.Server.MaxBodyBytes)
	require.Equal(t, 120, cfg.Auth.RateLimitIPPerMinute)
}

func TestLoadMissingFileFails(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "not-exist.yaml"))
	require.Error(t, err)
}

func TestValidateFailsFast(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)

	cfg.Database.Name = ""
	cfg.Server.Mode = "prod"                             // 非法模式
	cfg.CORS.AllowedOrigins = []string{"localhost:5173"} // 缺 scheme
	err = cfg.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "database.name")
	require.ErrorContains(t, err, "server.mode")
	require.ErrorContains(t, err, "cors.allowed_origins")
}

func TestValidatePoolParams(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)

	cfg.Database.MaxIdleConns = 100 // > max_open_conns
	require.ErrorContains(t, cfg.Validate(), "连接池参数非法")
}

// 新增平台防护参数的取值校验（安全审查 S4/S5/S6）。
func TestValidatePlatformGuards(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)

	cfg.Server.MaxBodyBytes = 0
	require.ErrorContains(t, cfg.Validate(), "max_body_bytes")

	cfg, err = Load("")
	require.NoError(t, err)
	cfg.Auth.RateLimitIPPerMinute = 0
	require.ErrorContains(t, cfg.Validate(), "rate_limit_ip_per_minute")

	cfg, err = Load("")
	require.NoError(t, err)
	cfg.Server.TrustedProxies = []string{"not-a-cidr"}
	require.ErrorContains(t, cfg.Validate(), "trusted_proxies")

	// 合法形态：单 IP（gin 兼容）与 CIDR 混用
	cfg, err = Load("")
	require.NoError(t, err)
	cfg.Server.TrustedProxies = []string{"127.0.0.1", "10.0.0.0/8"}
	require.NoError(t, cfg.Validate())
}

func TestValidateSSLModeEnum(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)

	cfg.Database.SSLMode = "verfy-full" // 拼错：静默失效比启动失败更危险
	require.ErrorContains(t, cfg.Validate(), "sslmode")

	cfg.Database.SSLMode = "verify-full"
	require.NoError(t, cfg.Validate())
}

// S12：release 模式 + sslmode=disable → 非阻断启动告警；debug 模式不告警。
func TestWarningsReleaseSSLDisable(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "release", cfg.Server.Mode) // 默认即 release
	require.Equal(t, "disable", cfg.Database.SSLMode)
	warns := cfg.Warnings()
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "sslmode=disable")
	require.Contains(t, warns[0], "verify-full") // 告警携带修复指引

	cfg.Server.Mode = "debug"
	cfg.Database.SSLMode = "disable"
	require.NoError(t, cfg.Validate())
	require.Empty(t, cfg.Warnings()) // debug 不告警（本地开发形态）

	cfg.Server.Mode = "release"
	cfg.Database.SSLMode = "verify-full"
	require.NoError(t, cfg.Validate())
	require.Empty(t, cfg.Warnings())
}

func TestDSNQuoting(t *testing.T) {
	dsn := DatabaseConfig{
		Host: "127.0.0.1", Port: 5432, User: "sf", Password: `p@ss'word`, Name: "stockflow", SSLMode: "disable",
	}.DSN()
	require.Contains(t, dsn, "password='p@ss'word'")
	require.Contains(t, dsn, "dbname='stockflow'")
}
