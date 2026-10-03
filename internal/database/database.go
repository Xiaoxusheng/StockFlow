// Package database PostgreSQL(pgx) + GORM 连接、通用模型与迁移入口。
// gorm.io/driver/postgres 底层即 pgx/v5 驱动（architecture.md §11.2 选型）。
package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/stockflow/server/internal/config"
)

// pingTimeout 启动连通性检查超时。
const pingTimeout = 3 * time.Second

// Connect 建立 GORM(pgx) 连接：
//   - 连接池参数显式设置（architecture.md §11.4：禁止默认值上线）；
//   - 立即 Ping 做连通性检查（deployment.md §3：失败快速报错退出，禁止带病启动）。
func Connect(cfg config.DatabaseConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层 sql.DB 失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("PostgreSQL 连通性检查失败: %w", err)
	}
	return db, nil
}
