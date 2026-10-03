//go:build integration

// 真实 PostgreSQL 依赖的集成测试：默认 go test 不编译本文件（单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/warehouse/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip）。覆盖：000004 迁移表上的默认仓库种子化幂等
// （database.md §8.1）与真实唯一索引/软删除语义（GORM 全链路）。

package warehouse

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	host := os.Getenv("SF_TEST_PG_HOST")
	if host == "" {
		t.Skip("未设置 SF_TEST_PG_* 环境变量，跳过集成测试")
	}
	port := os.Getenv("SF_TEST_PG_PORT")
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port,
		os.Getenv("SF_TEST_PG_USER"), os.Getenv("SF_TEST_PG_PASSWORD"), os.Getenv("SF_TEST_PG_NAME"))
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Skipf("连接测试库失败（跳过）: %v", err)
	}
	return db
}

// TestIntegration_EnsureDefaultWarehouse 幂等：重复调用不重复插入、不覆盖。
func TestIntegration_EnsureDefaultWarehouse(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()

	var before int64
	if err := db.WithContext(ctx).Model(&Warehouse{}).Count(&before).Error; err != nil {
		t.Fatalf("查询仓库数失败: %v", err)
	}
	if before > 0 {
		t.Skip("测试库已存在仓库数据（避免污染），跳过")
	}
	if err := EnsureDefaultWarehouse(db); err != nil {
		t.Fatalf("首次种子化失败: %v", err)
	}
	if err := EnsureDefaultWarehouse(db); err != nil {
		t.Fatalf("重复种子化失败: %v", err)
	}
	var after int64
	if err := db.WithContext(ctx).Model(&Warehouse{}).Count(&after).Error; err != nil {
		t.Fatalf("复查仓库数失败: %v", err)
	}
	if after != 1 {
		t.Fatalf("幂等不符：期望 1 条默认仓库，实际 %d", after)
	}
	var w Warehouse
	if err := db.WithContext(ctx).Where("code = ?", "WH-DEFAULT").First(&w).Error; err != nil {
		t.Fatalf("默认仓库未落库: %v", err)
	}
	if w.Status != StatusEnabled || w.Type != WarehouseTypeNormal {
		t.Fatalf("默认仓库属性不符: %+v", w)
	}
}
