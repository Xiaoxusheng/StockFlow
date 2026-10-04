package stockops

// devices_resolve 设备扫码域 resolve 匹配器 1 的实现侧（backend-m3-plan §2.2 D 冻结清单
// "仅新增"文件——本域读自己的表，实现消费方 internal/devices 定义的窄接口；
// router 装配经 devices.WithStockopsDocs 注入；plan §12.2 前例 masterdata/m2readers.go 同款）。
//
// 前缀分派（plan §8.3 条 1 / §12.2 单一映射表 devices.docPrefixOwners）：
// TR 调拨单 / CK 盘点单。
//
// 只读 SELECT，无任何写通路；签名只含内建类型与 devices 包值类型（devices.Hit），
// 不携带本域类型（域包 → 平台包单向依赖，plan §2.3 判据 2）。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/devices"
)

// docTables 本域可扫单据注册表（前缀 → 表/列/标签/仓库列；000009 DDL 列结构）。
type docTable struct {
	table     string
	noCol     string
	whCol     string
	statusCol string
	label     string
}

var docTables = map[string]docTable{
	"TR": {table: "transfer_orders", noCol: "transfer_no", whCol: "from_warehouse_id", statusCol: "status", label: "调拨单"},
	"CK": {table: "count_orders", noCol: "count_no", whCol: "warehouse_id", statusCol: "status", label: "盘点单"},
}

// DocResolveService 本域单据号解析读取实现（resolve 匹配器 1 的 stockops 槽位）。
type DocResolveService struct {
	db *gorm.DB
}

// NewDocResolveFinder 构建单据号读取器（router 装配：devices.WithStockopsDocs）。
func NewDocResolveFinder(db *gorm.DB) *DocResolveService {
	return &DocResolveService{db: db}
}

// FindByNo 按单号取存在性与摘要；found=false 表示单号不存在（含非本域前缀——
// 分派由消费方按 devices.docPrefixOwners 完成，本实现按前缀二次确认防误挂）。
func (s *DocResolveService) FindByNo(ctx context.Context, docNo string) (devices.Hit, bool, error) {
	prefix := docNo
	if i := strings.IndexByte(docNo, '-'); i > 0 {
		prefix = docNo[:i]
	}
	t, ok := docTables[prefix]
	if !ok {
		return devices.Hit{}, false, nil
	}
	var row struct {
		ID          int64
		Code        string
		Status      string
		WarehouseID int64
	}
	err := s.db.WithContext(ctx).
		Table(t.table).
		Select("id, "+t.noCol+" AS code, status AS status, "+t.whCol+" AS warehouse_id").
		Where(t.noCol+" = ?", docNo).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return devices.Hit{}, false, nil
		}
		return devices.Hit{}, false, fmt.Errorf("按单号 %s 查询 %s 失败: %w", docNo, t.label, err)
	}
	return devices.Hit{
		ID:          row.ID,
		Code:        row.Code,
		Name:        t.label + " " + row.Code,
		WarehouseID: row.WarehouseID,
		Status:      row.Status,
		DocKind:     prefix,
	}, true, nil
}

// 编译期断言：实现消费方窄接口（plan §12.2 冻结签名）。
var _ devices.DocFinder = (*DocResolveService)(nil)
