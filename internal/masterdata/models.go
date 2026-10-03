package masterdata

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stockflow/server/internal/database"
)

// masterdata 域 GORM 模型——与 db/migrations/000003_create_masterdata_tables.up.sql
// 列结构一一对应（backend-m1-plan §6.2 冻结 DDL；判据 9：本域表模型仅定义/导出于本包）。
//
// 通用字段经 database.BaseModel（database.md §3）；软删除 deleted_at 仅限
// database.md §5.1 清单内的 products/skus/suppliers/customers——分类/单位/条码
// 不含 deleted_at，生命周期终点为停用/解绑（迁移文件头注释同口径）。

// 通用启用/停用状态（chk_product_categories_status / chk_units_status /
// chk_products_status / chk_suppliers_status / chk_customers_status 同枚举）。
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)

// CodeTypeDefault 条码类型缺省值（barcodes.code_type 列 DEFAULT 'CODE128'）。
const CodeTypeDefault = "CODE128"

// ProductCategory 商品分类（树形，business-flow §1.1；组织级下拉数据源）。
type ProductCategory struct {
	database.BaseModel
	ParentID *database.ID `json:"parent_id"`
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Sort     int          `json:"sort"`
	Status   string       `json:"status"`
}

// TableName 显式指定表名。
func (ProductCategory) TableName() string { return "product_categories" }

// Unit 计量单位（business-flow §1.1）。
type Unit struct {
	database.BaseModel
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TableName 显式指定表名。
func (Unit) TableName() string { return "units" }

// Product 商品（软删除对象；business-flow §1.1 全量字段。image_urls 为文件中心
// 占位——上传接口随阶段 14 交付，M1 仅承载 URL 列表字段本身，backend-m1-plan §9）。
type Product struct {
	database.BaseModel
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	ShortName   string       `json:"short_name"`
	CategoryID  *database.ID `json:"category_id"`
	Brand       string       `json:"brand"`
	Model       string       `json:"model"`
	Spec        string       `json:"spec"`
	UnitID      *database.ID `json:"unit_id"`
	Weight      Number       `json:"weight"`
	Length      Number       `json:"length"`
	Width       Number       `json:"width"`
	Height      Number       `json:"height"`
	Volume      Number       `json:"volume"`
	ImageURLs   StringList   `json:"image_urls"`
	Description string       `json:"description"`
	Remark      string       `json:"remark"`
	Status      string       `json:"status"`
}

// TableName 显式指定表名。
func (Product) TableName() string { return "products" }

// SKU SKU（软删除对象；business-flow §1.2。批次/效期/序列号三开关决定 M2 入库、
// 库存、出库分配的业务分支，inventory-rules §6–§8；效期依赖批次承载——batches
// 表持有 expiry_date，故 is_expiry_managed=true 必须 is_batch_managed=true）。
type SKU struct {
	database.BaseModel
	Code            string      `json:"code"`
	ProductID       database.ID `gorm:"column:product_id" json:"product_id"`
	SpecAttrs       AttrMap     `gorm:"column:spec_attrs;type:jsonb" json:"spec_attrs"`
	CostPrice       Number      `json:"cost_price"`
	SalePrice       Number      `json:"sale_price"`
	SafetyStock     Number      `json:"safety_stock"`
	MaxStock        Number      `json:"max_stock"`
	MinReplenishQty Number      `json:"min_replenish_qty"`
	IsBatchManaged  bool        `json:"is_batch_managed"`
	IsExpiryManaged bool        `json:"is_expiry_managed"`
	IsSerialManaged bool        `json:"is_serial_managed"`
	IsEnabled       bool        `json:"is_enabled"`
}

// TableName 显式指定表名。
func (SKU) TableName() string { return "skus" }

// Barcode SKU 条码（一码一 SKU 全局唯一即业务键，uk_barcodes_barcode；扫码解析热点）。
// 无 deleted_at：条码是 SKU 的从属属性且全局唯一，SKU 软删后条码仍占用命名空间，
// 保证扫码追溯（scan_logs/流水按条码定位）永不出现一码两 SKU。
type Barcode struct {
	database.BaseModel
	SKUID     database.ID `gorm:"column:sku_id" json:"sku_id"`
	Barcode   string      `json:"barcode"`
	CodeType  string      `json:"code_type"`
	IsPrimary bool        `json:"is_primary"`
}

// TableName 显式指定表名。
func (Barcode) TableName() string { return "barcodes" }

// Supplier 供应商（软删除对象；business-flow §1.4：已产生业务记录不可删只停用，
// 引用校验随 M2 采购单据落地——M1 无单据表可查，见 backend-m1-plan §6.2）。
type Supplier struct {
	database.BaseModel
	Code    string `json:"code"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
	Address string `json:"address"`
	Status  string `json:"status"`
	Remark  string `json:"remark"`
}

// TableName 显式指定表名。
func (Supplier) TableName() string { return "suppliers" }

// Customer 客户（软删除对象；business-flow §1.5；历史订单/历史出库为 M2 关联
// 查询视图，不冗余存储）。
type Customer struct {
	database.BaseModel
	Code            string `json:"code"`
	Name            string `json:"name"`
	Contact         string `json:"contact"`
	Phone           string `json:"phone"`
	Email           string `json:"email"`
	Address         string `json:"address"`
	ShippingAddress string `json:"shipping_address"`
	Status          string `json:"status"`
}

// TableName 显式指定表名。
func (Customer) TableName() string { return "customers" }

// ---- jsonb 列载体（image_urls jsonb / spec_attrs jsonb）----

// StringList jsonb 字符串数组载体（products.image_urls，DEFAULT '[]'）。
type StringList []string

// Value nil/空 → []（列 NOT NULL DEFAULT '[]'，禁 NULL）。
func (l StringList) Value() (driver.Value, error) {
	if len(l) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	if err != nil {
		return nil, fmt.Errorf("StringList 序列化失败: %w", err)
	}
	return string(b), nil
}

// Scan 支持驱动返回的 JSON 文本（pgx 对 jsonb 的 driver.Value 为 string）与 nil。
func (l *StringList) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*l = nil
	case []byte:
		return l.parse(string(v))
	case string:
		return l.parse(v)
	default:
		return fmt.Errorf("StringList.Scan 不支持类型 %T", src)
	}
	return nil
}

func (l *StringList) parse(s string) error {
	if strings.TrimSpace(s) == "" {
		*l = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return fmt.Errorf("StringList 反序列化失败: %w", err)
	}
	*l = out
	return nil
}

// normalize 归一为非 nil 切片（API 响应稳定输出 []）。
func (l StringList) normalize() StringList {
	if l == nil {
		return StringList{}
	}
	return l
}

// AttrMap jsonb 对象载体（skus.spec_attrs，DEFAULT '{}'）。
type AttrMap map[string]any

// Value nil/空 → {}（列 NOT NULL DEFAULT '{}'，禁 NULL）。
func (m AttrMap) Value() (driver.Value, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(map[string]any(m))
	if err != nil {
		return nil, fmt.Errorf("AttrMap 序列化失败: %w", err)
	}
	return string(b), nil
}

// Scan 支持驱动返回的 JSON 文本与 nil。
func (m *AttrMap) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = nil
	case []byte:
		return m.parse(string(v))
	case string:
		return m.parse(v)
	default:
		return fmt.Errorf("AttrMap.Scan 不支持类型 %T", src)
	}
	return nil
}

func (m *AttrMap) parse(s string) error {
	if strings.TrimSpace(s) == "" {
		*m = nil
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return fmt.Errorf("AttrMap 反序列化失败: %w", err)
	}
	*m = out
	return nil
}

// normalize 归一为非 nil map（API 响应稳定输出 {}）。
func (m AttrMap) normalize() AttrMap {
	if m == nil {
		return AttrMap{}
	}
	return m
}
