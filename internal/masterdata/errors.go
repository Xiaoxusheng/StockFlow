package masterdata

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// masterdata 域错误码（architecture.md §2 模块命名空间 MASTERDATA_*；api.md §4 校验失败
// 必须带 details）。统一经 internal/response 注册与输出，禁止 handler 直接 c.JSON
// （backend-m1-plan §4.2 判据 2）。
//
// 约定：字段格式/必填问题用 COMMON_INVALID_PARAM + details{field,reason}；
// 业务冲突（编码唯一、删除被引用、停用被引用、状态不允许）用本域错误码。
var (
	// —— 商品 ——
	ErrProductNotFound   = response.Register("MASTERDATA_PRODUCT_NOT_FOUND", "商品不存在", http.StatusNotFound)
	ErrProductCodeExists = response.Register("MASTERDATA_PRODUCT_CODE_EXISTS", "商品编码已存在", http.StatusConflict)
	ErrProductDisabled   = response.Register("MASTERDATA_PRODUCT_DISABLED", "商品已停用，不允许该操作", http.StatusConflict)
	ErrProductHasSKU     = response.Register("MASTERDATA_PRODUCT_HAS_SKU", "商品下存在 SKU，禁止删除；如需下线请使用停用", http.StatusConflict)

	// —— 分类 / 单位（无删除接口，停用即生命周期终点，backend-m1-plan §5.4.1）——
	ErrCategoryNotFound    = response.Register("MASTERDATA_CATEGORY_NOT_FOUND", "商品分类不存在", http.StatusNotFound)
	ErrCategoryCodeExists  = response.Register("MASTERDATA_CATEGORY_CODE_EXISTS", "分类编码已存在", http.StatusConflict)
	ErrCategoryDisabled    = response.Register("MASTERDATA_CATEGORY_DISABLED", "商品分类已停用，不允许该操作", http.StatusConflict)
	ErrCategoryHasChildren = response.Register("MASTERDATA_CATEGORY_HAS_ENABLED_CHILDREN", "存在启用中的子分类，无法停用", http.StatusConflict)
	ErrCategoryInUse       = response.Register("MASTERDATA_CATEGORY_IN_USE", "存在启用中的商品引用该分类，无法停用", http.StatusConflict)
	ErrCategoryCycle       = response.Register("MASTERDATA_CATEGORY_CYCLE", "上级分类不能选择自身或自身的下级分类", http.StatusBadRequest)

	ErrUnitNotFound   = response.Register("MASTERDATA_UNIT_NOT_FOUND", "计量单位不存在", http.StatusNotFound)
	ErrUnitCodeExists = response.Register("MASTERDATA_UNIT_CODE_EXISTS", "单位编码已存在", http.StatusConflict)
	ErrUnitDisabled   = response.Register("MASTERDATA_UNIT_DISABLED", "计量单位已停用，不允许该操作", http.StatusConflict)
	ErrUnitInUse      = response.Register("MASTERDATA_UNIT_IN_USE", "存在商品引用该单位，无法停用", http.StatusConflict)

	// —— SKU 与条码 ——
	ErrSKUNotFound   = response.Register("MASTERDATA_SKU_NOT_FOUND", "SKU 不存在", http.StatusNotFound)
	ErrSKUCodeExists = response.Register("MASTERDATA_SKU_CODE_EXISTS", "SKU 编码已存在", http.StatusConflict)
	ErrBarcodeExists = response.Register("MASTERDATA_BARCODE_EXISTS", "条码已被占用（一码一 SKU，全局唯一）", http.StatusConflict)

	// —— 供应商 / 客户 ——
	ErrSupplierNotFound   = response.Register("MASTERDATA_SUPPLIER_NOT_FOUND", "供应商不存在", http.StatusNotFound)
	ErrSupplierCodeExists = response.Register("MASTERDATA_SUPPLIER_CODE_EXISTS", "供应商编码已存在", http.StatusConflict)
	ErrCustomerNotFound   = response.Register("MASTERDATA_CUSTOMER_NOT_FOUND", "客户不存在", http.StatusNotFound)
	ErrCustomerCodeExists = response.Register("MASTERDATA_CUSTOMER_CODE_EXISTS", "客户编码已存在", http.StatusConflict)
)
