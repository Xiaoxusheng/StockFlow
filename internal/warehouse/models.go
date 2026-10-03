package warehouse

import (
	"github.com/stockflow/server/internal/database"
	"gorm.io/gorm"
)

// 状态常量（000004 迁移 chk_*_status CHECK 约束同源：仅 ENABLED/DISABLED）。
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)

// 默认类型值（000004 列默认值同源）。类型值域开放（迁移注释：随业务扩展由应用层
// 校验），应用层仅做格式校验（validateTypeCode），不擅自圈死业务枚举。
const (
	WarehouseTypeNormal = "NORMAL"
	ZoneTypeStorage     = "STORAGE"
	BinTypePick         = "PICK"
)

// Warehouse 仓库（business-flow §1.6 第一级；000004 warehouses）。
// 软删除对象（database.md §5.1），嵌入 database.BaseModel（含 DeletedAt）。
// area/capacity 为 numeric(18,4)：以 float64 承载——物理量纲（面积/容量）而非货币，
// module 依赖清单由 scope A 冻结（go.mod 归属脚手架），本域不自行引入 decimal 依赖。
type Warehouse struct {
	database.BaseModel
	Code          string      `gorm:"column:code;size:64" json:"code"`
	Name          string      `gorm:"column:name;size:255" json:"name"`
	Address       string      `gorm:"column:address;size:512" json:"address"`
	Contact       string      `gorm:"column:contact;size:64" json:"contact"`
	Phone         string      `gorm:"column:phone;size:32" json:"phone"`
	Area          float64     `gorm:"column:area" json:"area"`
	Capacity      float64     `gorm:"column:capacity" json:"capacity"`
	Type          string      `gorm:"column:type;size:32" json:"type"`
	Status        string      `gorm:"column:status;size:16" json:"status"`
	ManagerUserID database.ID `gorm:"column:manager_user_id" json:"manager_user_id"` // 用户表逻辑引用，跨域不建 FK（000004 注释）
}

// TableName 显式表名。
func (Warehouse) TableName() string { return "warehouses" }

// baseCols 无软删除实体（zones/shelves）的通用字段（database.md §3 但书：按业务实际
// 取字段——zones/shelves 无 deleted_at 列，不能嵌入 database.BaseModel，否则 GORM 会在
// SELECT 拼出不存在的 deleted_at 条件）。钩子语义与 database.BaseModel 一致。
type baseCols struct {
	ID        database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt database.JSONTime `json:"created_at"`
	UpdatedAt database.JSONTime `json:"updated_at"`
	CreatedBy database.ID       `json:"created_by"`
	UpdatedBy database.ID       `json:"updated_by"`
}

// BeforeCreate 兜底填充创建/更新时间（GORM autoCreateTime 之外的双保险）。
func (b *baseCols) BeforeCreate(tx *gorm.DB) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = database.Now()
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.CreatedAt
	}
	return nil
}

// BeforeUpdate 兜底刷新更新时间。
func (b *baseCols) BeforeUpdate(tx *gorm.DB) error {
	b.UpdatedAt = database.Now()
	return nil
}

// Zone 库区（business-flow §1.6 第二级；000004 zones）。无删除通路，仅启停。
type Zone struct {
	baseCols
	WarehouseID database.ID `gorm:"column:warehouse_id" json:"warehouse_id"`
	Code        string      `gorm:"column:code;size:64" json:"code"`
	Name        string      `gorm:"column:name;size:255" json:"name"`
	ZoneType    string      `gorm:"column:zone_type;size:32" json:"zone_type"`
	Capacity    float64     `gorm:"column:capacity" json:"capacity"`
	Status      string      `gorm:"column:status;size:16" json:"status"`
}

// TableName 显式表名。
func (Zone) TableName() string { return "zones" }

// Shelf 货架（business-flow §1.6 第三级；000004 shelves）。无删除通路，仅启停。
type Shelf struct {
	baseCols
	WarehouseID database.ID `gorm:"column:warehouse_id" json:"warehouse_id"`
	ZoneID      database.ID `gorm:"column:zone_id" json:"zone_id"`
	Code        string      `gorm:"column:code;size:64" json:"code"`
	Layers      int         `gorm:"column:layers" json:"layers"`
	Columns     int         `gorm:"column:columns" json:"columns"` // COLUMNS 为 PG 非保留字，可直接作列名（000004 注释）
	Capacity    float64     `gorm:"column:capacity" json:"capacity"`
	Status      string      `gorm:"column:status;size:16" json:"status"`
}

// TableName 显式表名。
func (Shelf) TableName() string { return "shelves" }

// Bin 库位（business-flow §1.6 第四级；000004 bins）。软删除对象。
// current_capacity 由上架/移库业务维护（000004 列注释），本域任何接口不得直改。
type Bin struct {
	database.BaseModel
	WarehouseID     database.ID `gorm:"column:warehouse_id" json:"warehouse_id"`
	ZoneID          database.ID `gorm:"column:zone_id" json:"zone_id"`
	ShelfID         database.ID `gorm:"column:shelf_id" json:"shelf_id"`
	Layer           int         `gorm:"column:layer" json:"layer"`
	ColumnNo        int         `gorm:"column:column_no" json:"column_no"`
	Code            string      `gorm:"column:code;size:64" json:"code"`
	BinType         string      `gorm:"column:bin_type;size:32" json:"bin_type"`
	MaxCapacity     float64     `gorm:"column:max_capacity" json:"max_capacity"`
	CurrentCapacity float64     `gorm:"column:current_capacity" json:"current_capacity"`
	Status          string      `gorm:"column:status;size:16" json:"status"`
}

// TableName 显式表名。
func (Bin) TableName() string { return "bins" }
