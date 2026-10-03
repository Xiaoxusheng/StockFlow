package warehouse

import (
	"github.com/stockflow/server/internal/database"
)

// 视图 DTO（Model/DTO 分离）：ID/FK 统一 database.ID（JSON 序列化为字符串，防 JS
// 2^53 精度丢失，plan §1 全局约定）；时间经 database.JSONTime 统一格式（api.md §2）。
// current_capacity 允许出现在视图（可视化需要），但任何输入 DTO 均不含该字段
// （000004 列注释：由上架/移库业务维护，禁止直改）。

// WarehouseView 仓库视图。
type WarehouseView struct {
	ID            database.ID       `json:"id"`
	Code          string            `json:"code"`
	Name          string            `json:"name"`
	Address       string            `json:"address"`
	Contact       string            `json:"contact"`
	Phone         string            `json:"phone"`
	Area          float64           `json:"area"`
	Capacity      float64           `json:"capacity"`
	Type          string            `json:"type"`
	Status        string            `json:"status"`
	ManagerUserID database.ID       `json:"manager_user_id"`
	CreatedAt     database.JSONTime `json:"created_at"`
	UpdatedAt     database.JSONTime `json:"updated_at"`
}

// viewWarehouse 模型 → 视图。
func viewWarehouse(w *Warehouse) WarehouseView {
	return WarehouseView{
		ID:            w.ID,
		Code:          w.Code,
		Name:          w.Name,
		Address:       w.Address,
		Contact:       w.Contact,
		Phone:         w.Phone,
		Area:          w.Area,
		Capacity:      w.Capacity,
		Type:          w.Type,
		Status:        w.Status,
		ManagerUserID: w.ManagerUserID,
		CreatedAt:     w.CreatedAt,
		UpdatedAt:     w.UpdatedAt,
	}
}

// ZoneView 库区视图。
type ZoneView struct {
	ID          database.ID       `json:"id"`
	WarehouseID database.ID       `json:"warehouse_id"`
	Code        string            `json:"code"`
	Name        string            `json:"name"`
	ZoneType    string            `json:"zone_type"`
	Capacity    float64           `json:"capacity"`
	Status      string            `json:"status"`
	CreatedAt   database.JSONTime `json:"created_at"`
	UpdatedAt   database.JSONTime `json:"updated_at"`
}

func viewZone(z *Zone) ZoneView {
	return ZoneView{
		ID:          z.ID,
		WarehouseID: z.WarehouseID,
		Code:        z.Code,
		Name:        z.Name,
		ZoneType:    z.ZoneType,
		Capacity:    z.Capacity,
		Status:      z.Status,
		CreatedAt:   z.CreatedAt,
		UpdatedAt:   z.UpdatedAt,
	}
}

// ShelfView 货架视图。
type ShelfView struct {
	ID          database.ID       `json:"id"`
	WarehouseID database.ID       `json:"warehouse_id"`
	ZoneID      database.ID       `json:"zone_id"`
	Code        string            `json:"code"`
	Layers      int               `json:"layers"`
	Columns     int               `json:"columns"`
	Capacity    float64           `json:"capacity"`
	Status      string            `json:"status"`
	CreatedAt   database.JSONTime `json:"created_at"`
	UpdatedAt   database.JSONTime `json:"updated_at"`
}

func viewShelf(s *Shelf) ShelfView {
	return ShelfView{
		ID:          s.ID,
		WarehouseID: s.WarehouseID,
		ZoneID:      s.ZoneID,
		Code:        s.Code,
		Layers:      s.Layers,
		Columns:     s.Columns,
		Capacity:    s.Capacity,
		Status:      s.Status,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
}

// BinView 库位视图。
type BinView struct {
	ID              database.ID       `json:"id"`
	WarehouseID     database.ID       `json:"warehouse_id"`
	ZoneID          database.ID       `json:"zone_id"`
	ShelfID         database.ID       `json:"shelf_id"`
	Layer           int               `json:"layer"`
	ColumnNo        int               `json:"column_no"`
	Code            string            `json:"code"`
	BinType         string            `json:"bin_type"`
	MaxCapacity     float64           `json:"max_capacity"`
	CurrentCapacity float64           `json:"current_capacity"`
	Status          string            `json:"status"`
	CreatedAt       database.JSONTime `json:"created_at"`
	UpdatedAt       database.JSONTime `json:"updated_at"`
}

func viewBin(b *Bin) BinView {
	return BinView{
		ID:              b.ID,
		WarehouseID:     b.WarehouseID,
		ZoneID:          b.ZoneID,
		ShelfID:         b.ShelfID,
		Layer:           b.Layer,
		ColumnNo:        b.ColumnNo,
		Code:            b.Code,
		BinType:         b.BinType,
		MaxCapacity:     b.MaxCapacity,
		CurrentCapacity: b.CurrentCapacity,
		Status:          b.Status,
		CreatedAt:       b.CreatedAt,
		UpdatedAt:       b.UpdatedAt,
	}
}

// —— 创建入参（handler 绑定 JSON 后交 Service 做权威校验，api.md §4）——

// WarehouseCreateInput 创建仓库。
type WarehouseCreateInput struct {
	Code          string   `json:"code" binding:"required"`
	Name          string   `json:"name" binding:"required"`
	Address       string   `json:"address"`
	Contact       string   `json:"contact"`
	Phone         string   `json:"phone"`
	Area          *float64 `json:"area"`
	Capacity      *float64 `json:"capacity"`
	Type          string   `json:"type"`
	ManagerUserID int64    `json:"manager_user_id"`
}

// WarehouseUpdateInput 更新仓库（指针字段 = 提供才更新；status 不在更新面，
// 走启停接口——状态机守卫与审计单列）。
type WarehouseUpdateInput struct {
	Code          *string  `json:"code"`
	Name          *string  `json:"name"`
	Address       *string  `json:"address"`
	Contact       *string  `json:"contact"`
	Phone         *string  `json:"phone"`
	Area          *float64 `json:"area"`
	Capacity      *float64 `json:"capacity"`
	Type          *string  `json:"type"`
	ManagerUserID *int64   `json:"manager_user_id"`
}

// ZoneCreateInput 创建库区（warehouse_id 为层级锚点，创建后不可变更）。
type ZoneCreateInput struct {
	WarehouseID int64    `json:"warehouse_id" binding:"required"`
	Code        string   `json:"code" binding:"required"`
	Name        string   `json:"name" binding:"required"`
	ZoneType    string   `json:"zone_type"` // 缺省 STORAGE
	Capacity    *float64 `json:"capacity"`
}

// ZoneUpdateInput 更新库区（warehouse_id 不可变更）。
type ZoneUpdateInput struct {
	Code     *string  `json:"code"`
	Name     *string  `json:"name"`
	ZoneType *string  `json:"zone_type"`
	Capacity *float64 `json:"capacity"`
}

// ShelfCreateInput 创建货架。zone_id 为层级锚点；warehouse_id 可选提供用于交叉校验
// （服务端以 zone 归属为准，不一致拒绝——api.md §4 业务关系）。
type ShelfCreateInput struct {
	ZoneID      int64    `json:"zone_id" binding:"required"`
	WarehouseID int64    `json:"warehouse_id"` // 可选：提供时必须与 zone 归属一致
	Code        string   `json:"code" binding:"required"`
	Layers      *int     `json:"layers"` // 缺省 1
	Columns     *int     `json:"columns"`
	Capacity    *float64 `json:"capacity"`
}

// ShelfUpdateInput 更新货架（zone_id/warehouse_id 不可变更）。
type ShelfUpdateInput struct {
	Code     *string  `json:"code"`
	Layers   *int     `json:"layers"`
	Columns  *int     `json:"columns"`
	Capacity *float64 `json:"capacity"`
}

// BinCreateInput 创建库位。shelf_id 为层级锚点；zone_id/warehouse_id 可选提供用于
// 交叉校验（服务端以 shelf 归属为准）。
type BinCreateInput struct {
	ShelfID     int64    `json:"shelf_id" binding:"required"`
	ZoneID      int64    `json:"zone_id"`
	WarehouseID int64    `json:"warehouse_id"`
	Code        string   `json:"code" binding:"required"`
	BinType     string   `json:"bin_type"` // 缺省 PICK
	Layer       *int     `json:"layer"`    // 缺省 1
	ColumnNo    *int     `json:"column_no"`
	MaxCapacity *float64 `json:"max_capacity"`
}

// BinUpdateInput 更新库位（zone_id/shelf_id/warehouse_id/current_capacity 不可变更）。
type BinUpdateInput struct {
	Code        *string  `json:"code"`
	BinType     *string  `json:"bin_type"`
	Layer       *int     `json:"layer"`
	ColumnNo    *int     `json:"column_no"`
	MaxCapacity *float64 `json:"max_capacity"`
}

// StatusInput 启停入参（PUT */status）。
type StatusInput struct {
	Status string `json:"status" binding:"required"`
}
