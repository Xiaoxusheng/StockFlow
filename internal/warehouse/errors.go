package warehouse

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// warehouse 域业务错误码（api.md §2、architecture.md §2；命名空间 WAREHOUSE_*，
// 禁止跨域借用他域错误码）。构造于 Service 层，handler 经 response.Err 写出统一信封。
var (
	ErrWarehouseNotFound        = response.Register("WAREHOUSE_NOT_FOUND", "仓库不存在", http.StatusNotFound)
	ErrWarehouseCodeExists      = response.Register("WAREHOUSE_CODE_EXISTS", "仓库编码已存在", http.StatusConflict)
	ErrWarehouseHasZones        = response.Register("WAREHOUSE_HAS_ZONES", "仓库下存在库区，无法删除", http.StatusConflict)
	ErrWarehouseHasEnabledZones = response.Register("WAREHOUSE_HAS_ENABLED_ZONES", "仓库下存在启用中的库区，无法停用", http.StatusConflict)

	ErrZoneNotFound          = response.Register("WAREHOUSE_ZONE_NOT_FOUND", "库区不存在", http.StatusNotFound)
	ErrZoneCodeExists        = response.Register("WAREHOUSE_ZONE_CODE_EXISTS", "库区编码在仓库内已存在", http.StatusConflict)
	ErrZoneHasEnabledShelves = response.Register("WAREHOUSE_ZONE_HAS_ENABLED_SHELVES", "库区下存在启用中的货架，无法停用", http.StatusConflict)

	ErrShelfNotFound       = response.Register("WAREHOUSE_SHELF_NOT_FOUND", "货架不存在", http.StatusNotFound)
	ErrShelfCodeExists     = response.Register("WAREHOUSE_SHELF_CODE_EXISTS", "货架编码在库区内已存在", http.StatusConflict)
	ErrShelfHasEnabledBins = response.Register("WAREHOUSE_SHELF_HAS_ENABLED_BINS", "货架下存在启用中的库位，无法停用", http.StatusConflict)

	ErrBinNotFound   = response.Register("WAREHOUSE_BIN_NOT_FOUND", "库位不存在", http.StatusNotFound)
	ErrBinCodeExists = response.Register("WAREHOUSE_BIN_CODE_EXISTS", "库位编码在仓库内已存在（含历史软删除库位，编码永久占用）", http.StatusConflict)
	ErrBinOccupied   = response.Register("WAREHOUSE_BIN_OCCUPIED", "库位仍有占用容量，无法删除", http.StatusConflict)

	ErrParentMismatch = response.Register("WAREHOUSE_HIERARCHY_MISMATCH", "上级层级归属不匹配", http.StatusBadRequest)
	ErrParentDisabled = response.Register("WAREHOUSE_PARENT_DISABLED", "上级层级已停用，无法在该层级下创建", http.StatusBadRequest)
)
