package database

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file" // 注册 file:// 源
	_ "github.com/jackc/pgx/v5/stdlib"                   // 注册 database/sql "pgx" 驱动
)

// MigrateUp golang-migrate 自动迁移入口（backend-m1-plan §6.1：迁移 SQL 文件归 DB 工程师，
// 交付于 db/migrations；cmd 在 cfg.Database.AutoMigrate=true 时调用）。
//
//   - 使用独立短连接执行，与 GORM 连接池隔离；
//   - 目录不存在直接报错；目录为空时由 migrate 报 "no migrations found"（迁移未交付属预期失败）；
//   - 无新迁移（已应用到最新）返回 ErrNoChange，视为成功。
func MigrateUp(dsn, migrationsDir string) error {
	if _, err := os.Stat(migrationsDir); err != nil {
		return fmt.Errorf("迁移目录不可用 %s: %w（迁移文件由 DB 工程师交付于 db/migrations）", migrationsDir, err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开迁移连接失败: %w", err)
	}
	defer sqlDB.Close()

	driver, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("初始化 migrate 数据库驱动失败: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.ToSlash(migrationsDir), "pgx", driver)
	if err != nil {
		return fmt.Errorf("初始化 migrate 失败: %w", err)
	}
	_, _ = m.Close() // 关闭 source 与 migrate 持有的 database 实例（sqlDB 本身的 Close 由 defer 负责，重复关闭安全）

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行迁移失败: %w", err)
	}
	return nil
}
