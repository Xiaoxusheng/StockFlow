package masterdata

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// 路由装配与跨域校验器（backend-m1-plan §5.2 冻结签名：三参形态 + 可选 Option，
// 导出签名不得变更）。
//
// 路由总表（plan §5.4，路径对齐 api.md §1）：/api/products、/api/skus、
// /api/product-categories、/api/units、/api/suppliers、/api/customers——
// 全部挂 auth.RequirePermission 权限点（plan §5.4.1 冻结清单，种子同源
// internal/auth/permissions.go），列表强制分页，统一经 internal/response 输出。
//
// 分类/单位无 DELETE 路由（权限清单无 delete 动作，停用即生命周期终点）。
// rdb（Redis）M1 暂无本域缓存消费点：基础资料为组织级低频数据，保留入参对齐
// 冻结签名，引入热点缓存（如扫码解析）时启用。

// Option RegisterRoutes 的可选注入项：仅用于跨域消费接口注入（plan §4.3/§5.2），
// 不得携带业务配置。2026-10-04 起承载往来单位删除引用校验读取器（refreaders.go）；
// 商品/SKU/分类/单位/条码仍全部为本域表。
type Option func(*options)

type options struct {
	supplierRefs SupplierRefReader
	customerRefs CustomerRefReader
}

// RegisterRoutes 基础资料域路由。约定：
//   - rg 已挂 auth.AuthRequired()；域内对每个路由挂 auth.RequirePermission(...)；
//   - 列表接口强制分页（api.md §2.1），统一经 internal/response 输出；
//   - db 为 nil 属装配错误，启动期 fail-fast（deployment.md §3 禁止带病启动）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	_ = rdb // 见文件头：rdb 暂无消费点
	if db == nil {
		panic("masterdata 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	repo := NewRepository(db)
	svc := NewService(repo)
	// 跨域消费注入（refreaders.go；未注入时删除引用校验跳过——fail-open 口径见该文件注）。
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	svc.supplierRefs = o.supplierRefs
	svc.customerRefs = o.customerRefs

	// —— 商品 ——
	rg.GET("/products", auth.RequirePermission(auth.PermProductList), func(c *gin.Context) { handleProductList(c, svc) })
	rg.POST("/products", auth.RequirePermission(auth.PermProductCreate), func(c *gin.Context) { handleProductCreate(c, svc) })
	rg.GET("/products/:id", auth.RequirePermission(auth.PermProductRead), func(c *gin.Context) { handleProductDetail(c, svc) })
	rg.PUT("/products/:id", auth.RequirePermission(auth.PermProductUpdate), func(c *gin.Context) { handleProductUpdate(c, svc) })
	rg.PUT("/products/:id/status", auth.RequirePermission(auth.PermProductStatus), func(c *gin.Context) { handleProductStatus(c, svc) })
	rg.DELETE("/products/:id", auth.RequirePermission(auth.PermProductDelete), func(c *gin.Context) { handleProductDelete(c, svc) })

	// —— SKU（含条码，随 SKU create/update 全量维护）——
	rg.GET("/skus", auth.RequirePermission(auth.PermSKUList), func(c *gin.Context) { handleSKUList(c, svc) })
	rg.POST("/skus", auth.RequirePermission(auth.PermSKUCreate), func(c *gin.Context) { handleSKUCreate(c, svc) })
	rg.GET("/skus/:id", auth.RequirePermission(auth.PermSKURead), func(c *gin.Context) { handleSKUDetail(c, svc) })
	rg.PUT("/skus/:id", auth.RequirePermission(auth.PermSKUUpdate), func(c *gin.Context) { handleSKUUpdate(c, svc) })
	rg.PUT("/skus/:id/status", auth.RequirePermission(auth.PermSKUStatus), func(c *gin.Context) { handleSKUStatus(c, svc) })
	rg.DELETE("/skus/:id", auth.RequirePermission(auth.PermSKUDelete), func(c *gin.Context) { handleSKUDelete(c, svc) })

	// —— 商品分类（无删除：masterdata:category 权限清单无 delete，plan §5.4.1）——
	rg.GET("/product-categories", auth.RequirePermission(auth.PermCategoryList), func(c *gin.Context) { handleCategoryList(c, svc) })
	rg.POST("/product-categories", auth.RequirePermission(auth.PermCategoryCreate), func(c *gin.Context) { handleCategoryCreate(c, svc) })
	rg.GET("/product-categories/:id", auth.RequirePermission(auth.PermCategoryRead), func(c *gin.Context) { handleCategoryDetail(c, svc) })
	rg.PUT("/product-categories/:id", auth.RequirePermission(auth.PermCategoryUpdate), func(c *gin.Context) { handleCategoryUpdate(c, svc) })
	rg.PUT("/product-categories/:id/status", auth.RequirePermission(auth.PermCategoryStatus), func(c *gin.Context) { handleCategoryStatus(c, svc) })

	// —— 计量单位（无删除：masterdata:unit 权限清单无 delete，plan §5.4.1）——
	rg.GET("/units", auth.RequirePermission(auth.PermUnitList), func(c *gin.Context) { handleUnitList(c, svc) })
	rg.POST("/units", auth.RequirePermission(auth.PermUnitCreate), func(c *gin.Context) { handleUnitCreate(c, svc) })
	rg.GET("/units/:id", auth.RequirePermission(auth.PermUnitRead), func(c *gin.Context) { handleUnitDetail(c, svc) })
	rg.PUT("/units/:id", auth.RequirePermission(auth.PermUnitUpdate), func(c *gin.Context) { handleUnitUpdate(c, svc) })
	rg.PUT("/units/:id/status", auth.RequirePermission(auth.PermUnitStatus), func(c *gin.Context) { handleUnitStatus(c, svc) })

	// —— 供应商 ——
	rg.GET("/suppliers", auth.RequirePermission(auth.PermSupplierList), func(c *gin.Context) { handleSupplierList(c, svc) })
	rg.POST("/suppliers", auth.RequirePermission(auth.PermSupplierCreate), func(c *gin.Context) { handleSupplierCreate(c, svc) })
	rg.GET("/suppliers/:id", auth.RequirePermission(auth.PermSupplierRead), func(c *gin.Context) { handleSupplierDetail(c, svc) })
	rg.PUT("/suppliers/:id", auth.RequirePermission(auth.PermSupplierUpdate), func(c *gin.Context) { handleSupplierUpdate(c, svc) })
	rg.PUT("/suppliers/:id/status", auth.RequirePermission(auth.PermSupplierStatus), func(c *gin.Context) { handleSupplierStatus(c, svc) })
	rg.DELETE("/suppliers/:id", auth.RequirePermission(auth.PermSupplierDelete), func(c *gin.Context) { handleSupplierDelete(c, svc) })

	// —— 客户 ——
	rg.GET("/customers", auth.RequirePermission(auth.PermCustomerList), func(c *gin.Context) { handleCustomerList(c, svc) })
	rg.POST("/customers", auth.RequirePermission(auth.PermCustomerCreate), func(c *gin.Context) { handleCustomerCreate(c, svc) })
	rg.GET("/customers/:id", auth.RequirePermission(auth.PermCustomerRead), func(c *gin.Context) { handleCustomerDetail(c, svc) })
	rg.PUT("/customers/:id", auth.RequirePermission(auth.PermCustomerUpdate), func(c *gin.Context) { handleCustomerUpdate(c, svc) })
	rg.PUT("/customers/:id/status", auth.RequirePermission(auth.PermCustomerStatus), func(c *gin.Context) { handleCustomerStatus(c, svc) })
	rg.DELETE("/customers/:id", auth.RequirePermission(auth.PermCustomerDelete), func(c *gin.Context) { handleCustomerDelete(c, svc) })
}

// ---- 跨域消费接口实现（plan §4.3：本域为被消费方，结构化满足消费方窄接口）----

// SKUCheckService inventory 域（plan §4.3 示例接口 inventory.SKUChecker）消费的
// SKU 可用性校验实现：读本域 skus 表（GORM 软删作用域自动排除已删行）。
// router 装配：inventory.WithSKUChecker(masterdata.NewSKUChecker(db))。
type SKUCheckService struct {
	db *gorm.DB
}

// NewSKUChecker 构建 SKU 可用性校验器（签名冻结于 backend-m1-plan §4.3）。
func NewSKUChecker(db *gorm.DB) *SKUCheckService { return &SKUCheckService{db: db} }

// ExistsActive 判断 SKU 存在且启用（未软删）：库存变更与出库分配前的业务关系校验
// （api.md §4）。db 故障时返回错误（fail-closed 由消费方决定拒绝路径）。
func (s *SKUCheckService) ExistsActive(ctx context.Context, skuID int64) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&SKU{}).
		Where("id = ? AND is_enabled = ?", skuID, true).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("校验 SKU %d 可用性失败: %w", skuID, err)
	}
	return n > 0, nil
}
