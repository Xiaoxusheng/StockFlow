package warehouse

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stockflow/server/internal/database"
)

// errSeedDBNil db 参数缺失错误：种子写入必须显式传入 db 句柄（不静默兜底）。
var errSeedDBNil = errors.New("warehouse.EnsureDefaultWarehouse: db 句柄为 nil")

// EnsureDefaultWarehouse 默认仓库示例种子化（database.md §8.1"默认仓库示例"、
// plan §7.5；签名冻结，plan §5.2）。
//
// 语义与 internal/database.BootstrapIfEmpty 的默认仓库种子保持一致：
//   - 仅当无任何未删除仓库时插入（幂等：重复启动不重复插入、不覆盖已有数据）；
//   - ON CONFLICT DO NOTHING 兜底并发与历史软删行（部分索引不含软删行，可重建）；
//   - created_by/updated_by = 0（系统操作，database.BaseModel 注释约定）。
//
// database.BootstrapIfEmpty（首次启动安全初始化）已在空库时创建默认仓库，
// 本入口为其独立等价通路：供初始化程序/运维工具单独调用。
func EnsureDefaultWarehouse(db *gorm.DB) error {
	if db == nil {
		return errSeedDBNil
	}
	var count int64
	// GORM 对软删除模型自动追加 deleted_at IS NULL（与 database/seed.go 的
	// COUNT(*) 全量口径差异：已全部软删的库视为"无仓库"，允许重建默认仓库）。
	if err := db.Model(&Warehouse{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	row := Warehouse{
		Code:   database.DefaultWarehouseCode,
		Name:   "默认仓库",
		Type:   WarehouseTypeNormal,
		Status: StatusEnabled,
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}
