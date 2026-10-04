package auth

// resolveRuntimeConfig 的 fail-fast 分支覆盖（清偿项 F18）：
//   - release 模式缺 SF_AUTH_JWT_SECRET → 报错拒绝启动（deployment.md §1/§3 禁止带病启动）；
//   - 弱密钥（< 16 字节）→ 任何模式都报错；
//   - debug/test 模式缺省 → 降级为进程内随机密钥（每次不同）。
//
// 环境经 t.Setenv 控制用例间隔离；gin 模式用例内切换、defer 还原（init 已设 TestMode）。

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestResolveRuntimeConfigJWTSecretBranches 密钥分支表驱动用例。
func TestResolveRuntimeConfigJWTSecretBranches(t *testing.T) {
	t.Run("release_mode_missing_secret_fails_fast", func(t *testing.T) {
		gin.SetMode(gin.ReleaseMode)
		defer gin.SetMode(gin.TestMode)
		t.Setenv(envJWTSecret, "")

		_, err := resolveRuntimeConfig()
		if err == nil {
			t.Fatal("release 模式缺 SF_AUTH_JWT_SECRET 必须报错（禁止以未知密钥启动）")
		}
		if !strings.Contains(err.Error(), envJWTSecret) || !strings.Contains(err.Error(), "release") {
			t.Fatalf("错误应指明环境变量与 release 模式，实际: %v", err)
		}
	})

	t.Run("weak_secret_rejected_in_any_mode", func(t *testing.T) {
		for _, mode := range []string{"debug", "test", "release"} {
			setGinModeByName(t, mode)
			t.Setenv(envJWTSecret, "short") // 5 字节 < minJWTSecretLen(16)

			_, err := resolveRuntimeConfig()
			if err == nil {
				t.Fatalf("%s 模式弱密钥必须报错", mode)
			}
			if !strings.Contains(err.Error(), "弱密钥") {
				t.Fatalf("%s 模式错误应指明弱密钥，实际: %v", mode, err)
			}
		}
		gin.SetMode(gin.TestMode)
	})

	t.Run("valid_secret_accepted", func(t *testing.T) {
		t.Setenv(envJWTSecret, "prod-secret-0123456789abcdef")

		cfg, err := resolveRuntimeConfig()
		if err != nil {
			t.Fatalf("合法密钥不应报错: %v", err)
		}
		if string(cfg.jwtSecret) != "prod-secret-0123456789abcdef" {
			t.Fatalf("jwtSecret 应原样采用环境变量值，实际 %q", cfg.jwtSecret)
		}
	})

	t.Run("debug_mode_missing_secret_falls_back_to_random", func(t *testing.T) {
		gin.SetMode(gin.DebugMode)
		defer gin.SetMode(gin.TestMode)
		t.Setenv(envJWTSecret, "")

		cfg, err := resolveRuntimeConfig()
		if err != nil {
			t.Fatalf("debug 模式缺省应降级随机密钥而非报错: %v", err)
		}
		if len(cfg.jwtSecret) == 0 {
			t.Fatal("降级密钥不应为空")
		}

		// 随机性：两次解析应产生不同密钥（重启即失效的语义载体）。
		cfg2, err := resolveRuntimeConfig()
		if err != nil {
			t.Fatalf("第二次解析不应报错: %v", err)
		}
		if string(cfg.jwtSecret) == string(cfg2.jwtSecret) {
			t.Fatal("两次降级随机密钥不应相同")
		}
	})
}

// setGinModeByName 按名切换 gin 模式（表驱动用例辅助）。
func setGinModeByName(t *testing.T, name string) {
	t.Helper()
	switch name {
	case "debug":
		gin.SetMode(gin.DebugMode)
	case "test":
		gin.SetMode(gin.TestMode)
	case "release":
		gin.SetMode(gin.ReleaseMode)
	default:
		t.Fatalf("未知 gin 模式: %s", name)
	}
}
