package auth

import (
	"os"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// BootstrapIfEmpty 空库安全初始化（database.md §8.1、plan §7.5 冻结签名）。
//
// 落位说明（Orchestrator 裁决 2026-10-02，见 internal/database/seed.go 文件头）：
// 生产安全初始化实现于 internal/database.BootstrapIfEmpty 并由 cmd/server/main.go 接线；
// 本冻结签名按裁决委托该入口，禁止出现第二套种子逻辑。管理员初始密码经环境变量
// SF_ADMIN_INITIAL_PASSWORD 注入（与 cmd 同一通道）；密码与哈希绝不写入日志
// （architecture.md §6 日志红线）。rdb 参数为 plan §5.1 冻结形态保留，当前无需缓存预热。
func BootstrapIfEmpty(db *gorm.DB, rdb *redis.Client) error {
	_ = rdb // 冻结签名参数：种子过程无缓存交互
	_, err := database.BootstrapIfEmpty(db, os.Getenv("SF_ADMIN_INITIAL_PASSWORD"))
	return err
}
