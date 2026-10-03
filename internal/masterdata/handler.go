package masterdata

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，backend-m1-plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。
//
// handler 均为纯函数 (c, svc)，由 RegisterRoutes 以闭包挂接到路由（无包级可变状态）。

// actorOf 从 gin 上下文提取操作者归因（审计用；auth.CurrentUser 为冻结契约 plan §5.1）。
func actorOf(c *gin.Context) Actor {
	uc, _ := auth.CurrentUser(c)
	return Actor{
		UserID:    uc.UserID,
		Username:  uc.Username,
		IsSuper:   uc.IsSuper,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
	}
}

// pathID 解析路径 ID（api.md §2：ID 一律字符串形态，路由参数解析为正整数）。
func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为正整数",
		}))
		return 0, false
	}
	return id, true
}

// queryInt64 解析可选正整数查询参数（空→(0,true)）。
func queryInt64(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": name, "reason": "必须为非负整数",
		}))
		return 0, false
	}
	return n, true
}

// bindJSON 统一请求体绑定（错误统一 COMMON_INVALID_PARAM + 原因）。
func bindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{"reason": err.Error()}))
		return false
	}
	return true
}

// pageOf 统一分页解析（api.md §2.1；response.ParsePage 全站唯一入口）。
func pageOf(c *gin.Context) (int, int, bool) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return 0, 0, false
	}
	return page, pageSize, true
}

// ---- 商品（masterdata:product:*）----

func handleProductList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	categoryID, ok := queryInt64(c, "category_id")
	if !ok {
		return
	}
	items, total, err := svc.ListProducts(c.Request.Context(), ProductListFilter{
		Keyword:    normalizeKeyword(c.Query("keyword")),
		CategoryID: categoryID,
		Status:     c.Query("status"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleProductDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleProductCreate(c *gin.Context, svc *Service) {
	var req ProductCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateProduct(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleProductUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req ProductUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateProduct(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// StatusRequest 通用启停请求体。
type StatusRequest struct {
	Status string `json:"status" binding:"required"`
}

func handleProductStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StatusRequest
	if !bindJSON(c, &req) {
		return
	}
	cascaded, err := svc.UpdateProductStatus(c.Request.Context(), actorOf(c), id, req.Status)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status, "cascade_disabled_skus": cascaded})
}

func handleProductDelete(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := svc.DeleteProduct(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ---- SKU 与条码（masterdata:sku:*）----

// SKUStatusRequest SKU 启停请求体（is_enabled 布尔开关）。
type SKUStatusRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

func handleSKUList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	productID, ok := queryInt64(c, "product_id")
	if !ok {
		return
	}
	f := SKUListFilter{
		Keyword:   normalizeKeyword(c.Query("keyword")),
		ProductID: productID,
		Page:      page,
		PageSize:  pageSize,
	}
	if raw := c.Query("enabled"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "enabled", "reason": "必须为 true 或 false",
			}))
			return
		}
		f.Enabled = &b
	}
	items, total, err := svc.ListSkus(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleSKUDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetSKU(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSKUCreate(c *gin.Context, svc *Service) {
	var req SKUCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateSKU(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSKUUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req SKUUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateSKU(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSKUStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req SKUStatusRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.UpdateSKUStatus(c.Request.Context(), actorOf(c), id, *req.Enabled); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"is_enabled": *req.Enabled})
}

func handleSKUDelete(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := svc.DeleteSKU(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ---- 商品分类（masterdata:category:*；无删除接口）----

func handleCategoryList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	f := CategoryListFilter{
		Keyword:  normalizeKeyword(c.Query("keyword")),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	}
	switch raw := c.Query("parent_id"); raw {
	case "":
		// 不筛选上级
	case "0":
		f.RootOnly = true // 0 约定为顶级（parent_id IS NULL）
	default:
		pid, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || pid <= 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "parent_id", "reason": "必须为正整数或 0（顶级）",
			}))
			return
		}
		f.ParentID = &pid
	}
	items, total, err := svc.ListCategories(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleCategoryDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetCategory(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCategoryCreate(c *gin.Context, svc *Service) {
	var req CategoryCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateCategory(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCategoryUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req CategoryUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateCategory(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCategoryStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StatusRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.UpdateCategoryStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

// ---- 计量单位（masterdata:unit:*；无删除接口）----

func handleUnitList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	items, total, err := svc.ListUnits(c.Request.Context(), UnitListFilter{
		Keyword:  normalizeKeyword(c.Query("keyword")),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleUnitDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetUnit(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleUnitCreate(c *gin.Context, svc *Service) {
	var req UnitCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateUnit(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleUnitUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req UnitUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateUnit(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleUnitStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StatusRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.UpdateUnitStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

// ---- 供应商（masterdata:supplier:*）----

func handleSupplierList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	items, total, err := svc.ListSuppliers(c.Request.Context(), PartnerListFilter{
		Keyword:  normalizeKeyword(c.Query("keyword")),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleSupplierDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetSupplier(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSupplierCreate(c *gin.Context, svc *Service) {
	var req SupplierCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateSupplier(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSupplierUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req SupplierUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateSupplier(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleSupplierStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StatusRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.UpdateSupplierStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

func handleSupplierDelete(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := svc.DeleteSupplier(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// ---- 客户（masterdata:customer:*）----

func handleCustomerList(c *gin.Context, svc *Service) {
	page, pageSize, ok := pageOf(c)
	if !ok {
		return
	}
	items, total, err := svc.ListCustomers(c.Request.Context(), PartnerListFilter{
		Keyword:  normalizeKeyword(c.Query("keyword")),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

func handleCustomerDetail(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := svc.GetCustomer(c.Request.Context(), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCustomerCreate(c *gin.Context, svc *Service) {
	var req CustomerCreateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.CreateCustomer(c.Request.Context(), actorOf(c), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCustomerUpdate(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req CustomerUpdateInput
	if !bindJSON(c, &req) {
		return
	}
	v, err := svc.UpdateCustomer(c.Request.Context(), actorOf(c), id, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

func handleCustomerStatus(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StatusRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := svc.UpdateCustomerStatus(c.Request.Context(), actorOf(c), id, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": req.Status})
}

func handleCustomerDelete(c *gin.Context, svc *Service) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := svc.DeleteCustomer(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}
