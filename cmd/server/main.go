// StockFlow 后端入口：进程装配与生命周期（backend-m1-plan §2）。
// 流程：配置 → 日志 → 依赖（DB/Redis）→ 迁移（可选）→ 首次启动安全初始化 → 路由 → 优雅退出。
// 禁止业务逻辑、禁止写 SQL（plan §2：cmd 只做装配）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/cache"
	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/logger"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/router"
)

// defaultConfigFile 默认配置文件路径；不存在时降级为“仅默认值+环境变量”。
const defaultConfigFile = "config.yaml"

// maxHeaderBytes 请求头（含请求行）上限（安全审查 S6：与全局 body 上限配套的入口防护，
// 防超长头部耗尽连接资源；64KB 为 Nginx 默认量级）。
const maxHeaderBytes = 64 << 10

func main() {
	configPath := flag.String("config", defaultConfigFile,
		`配置文件路径；传 "" 仅用默认值+环境变量`)
	flag.Parse()

	// 默认路径不存在则降级（便于 go run ./cmd/server 直接冒烟）；显式指定路径缺失则快速失败
	if _, statErr := os.Stat(*configPath); statErr != nil && *configPath == defaultConfigFile {
		*configPath = ""
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	logs, err := logger.New(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化日志失败:", err)
		os.Exit(1)
	}
	defer logs.Sync()
	middleware.SetLoggers(logs)
	response.SetErrorLogger(logs.Error)

	// 启动告警（非阻断，安全审查 S12）：config.Validate 先于日志装配执行，告警在此
	// 以 zap warn 输出（如 release 模式下 sslmode=disable 的加密缺失提醒）。
	for _, w := range cfg.Warnings() {
		logs.Error.Warn(w)
	}

	gin.SetMode(cfg.Server.Mode)

	// 依赖：PostgreSQL(pgx+GORM)、Redis（可关闭）
	db, err := database.Connect(cfg.Database)
	if err != nil {
		logs.Error.Fatal("初始化数据库失败", zap.Error(err))
	}
	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		logs.Error.Fatal("初始化 Redis 失败", zap.Error(err))
	}

	// 数据库迁移（database.md §9；迁移文件归 DB 工程师，交付于 db/migrations）。
	// 默认走 make migrate-up；auto_migrate=true 时随启动执行（生产建议保持 false、
	// 由发布流程显式执行迁移，deployment.md §7）。
	if cfg.Database.AutoMigrate {
		if err := database.MigrateUp(cfg.Database.DSN(), "db/migrations"); err != nil {
			logs.Error.Fatal("执行数据库迁移失败", zap.Error(err))
		}
		logs.Business.Info("数据库迁移完成（auto_migrate）")
	}

	// 首次启动安全初始化（database.md §8.1、plan §7.5）：仅空库执行、幂等，
	// 实现于 internal/database/seed.go（Orchestrator 裁决落位）。
	// 管理员初始密码经 SF_ADMIN_INITIAL_PASSWORD 注入，空库且缺失/弱密码时启动失败；
	// 密码与哈希绝不写入日志（architecture.md §6 日志红线）。
	boot, err := database.BootstrapIfEmpty(db, os.Getenv("SF_ADMIN_INITIAL_PASSWORD"))
	if err != nil {
		logs.Error.Fatal("首次启动安全初始化失败", zap.Error(err))
	}
	if boot.AdminCreated {
		logs.Business.Info("默认管理员已创建（首次启动安全初始化）", zap.String("username", database.AdminUsername))
	}
	logs.Business.Info("启动初始化检查完成",
		zap.Int("roles_seeded", boot.RolesSeeded),
		zap.Int("permissions_seeded", boot.PermissionsSeeded),
		zap.Int("role_bindings_seeded", boot.RoleBindingsSeeded),
		zap.Bool("admin_created", boot.AdminCreated),
		zap.Bool("default_warehouse_created", boot.DefaultWarehouseCreated),
	)

	r := router.New(cfg, db, rdb)

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        r,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MaxHeaderBytes: maxHeaderBytes,
	}

	// HTTP 服务：独立 goroutine + 错误通道（goroutine 生命周期由 main 管理）
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	logs.Business.Info("服务已启动",
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode),
		zap.Bool("redis_enabled", rdb != nil),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		logs.Error.Fatal("HTTP 服务异常退出", zap.Error(err))
	case <-ctx.Done():
		logs.Business.Info("收到退出信号，开始优雅停机")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logs.Error.Error("优雅停机未能在超时内完成", zap.Error(err))
	}

	if rdb != nil {
		_ = rdb.Close()
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	logs.Business.Info("服务已停止")
}
