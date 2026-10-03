package masterdata

// Service 层单元测试（ask 约束：不依赖 PostgreSQL/Redis/网络）。
// 数据访问走内存 fakeRepo；事务与审计写入经假 gorm 方言器运行（no-op 驱动），
// operation_logs 行由 gorm 回调探针捕获断言（真路径：middleware.Audit → tx.Create）。

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// ---- 测试支撑 ----

// newTestService 装配 Service + 内存仓库 + 审计探针。
func newTestService(t *testing.T) (*Service, *fakeRepo, *auditSpy) {
	t.Helper()
	spy := &auditSpy{}
	repo := newFakeRepo(spy)
	return NewService(repo), repo, spy
}

// codeOf 取业务错误码字符串（*response.Error 的 Error() 形如 "CODE: message"）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *response.Error
	require.ErrorAs(t, err, &e, "期望业务错误，得到 %T: %v", err, err)
	full := e.Error()
	idx := strings.Index(full, ": ")
	require.Positive(t, idx, "错误码格式异常: %q", full)
	return full[:idx]
}

func testActor(uid int64) Actor {
	return Actor{UserID: uid, Username: "tester", IsSuper: false,
		RequestID: "req-test", IP: "127.0.0.1", UserAgent: "unit", Method: "POST", Path: "/test"}
}

func num(s string) Number { return Number(s) }
func numPtr(s string) *Number {
	n := Number(s)
	return &n
}
func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func id64(v int64) *int64     { return &v }

// ---- 商品 ----

func TestCreateProductOK(t *testing.T) {
	svc, repo, spy := newTestService(t)
	catID := repo.seedCategory("CAT-01", StatusEnabled, 0)
	unitID := repo.seedUnit("PCS", StatusEnabled)

	in := ProductCreateInput{
		Code: "P001", Name: "矿泉水", ShortName: "水", CategoryID: id64(catID), UnitID: id64(unitID),
		Brand: "SF", Weight: num("0.5"), ImageURLs: []string{"http://img/1.png"},
	}
	v, err := svc.CreateProduct(t.Context(), testActor(7), in)
	require.NoError(t, err)
	require.Equal(t, "P001", v.Code)
	require.Equal(t, StatusEnabled, v.Status) // 创建恒为启用
	require.Equal(t, database.ID(catID), *v.CategoryID)
	require.Equal(t, num("0.5"), v.Weight)
	require.Equal(t, []string{"http://img/1.png"}, []string(v.ImageURLs))

	// 审计：与业务同事务落一行 create（architecture.md §8.1）
	entries := spy.all()
	require.Len(t, entries, 1)
	require.Equal(t, "masterdata", entries[0].Module)
	require.Equal(t, "product", entries[0].ObjectType)
	require.Equal(t, "create", entries[0].Action)
	require.True(t, entries[0].Success)
	require.EqualValues(t, 7, entries[0].UserID)
	require.Contains(t, string(entries[0].AfterSnapshot), `"code":"P001"`)
}

func TestCreateProductDuplicateCode(t *testing.T) {
	svc, repo, spy := newTestService(t)
	repo.seedProduct("P001", StatusEnabled, nil)
	_, err := svc.CreateProduct(t.Context(), testActor(1), ProductCreateInput{Code: "P001", Name: "重复"})
	require.Equal(t, "MASTERDATA_PRODUCT_CODE_EXISTS", codeOf(t, err))
	require.Empty(t, spy.all(), "失败路径不得写成功审计")
}

func TestCreateProductValidation(t *testing.T) {
	svc, repo, _ := newTestService(t)
	disabled := repo.seedCategory("CAT-X", StatusDisabled, 0)

	cases := []struct {
		name string
		in   ProductCreateInput
		want string
	}{
		{"编码为空", ProductCreateInput{Name: "x"}, "COMMON_INVALID_PARAM"},
		{"编码非法字符", ProductCreateInput{Code: "P 01", Name: "x"}, "COMMON_INVALID_PARAM"},
		{"名称为空", ProductCreateInput{Code: "P2"}, "COMMON_INVALID_PARAM"},
		{"分类不存在", ProductCreateInput{Code: "P2", Name: "x", CategoryID: id64(999)}, "MASTERDATA_CATEGORY_NOT_FOUND"},
		{"分类已停用", ProductCreateInput{Code: "P2", Name: "x", CategoryID: id64(disabled)}, "MASTERDATA_CATEGORY_DISABLED"},
		{"单位不存在", ProductCreateInput{Code: "P2", Name: "x", UnitID: id64(999)}, "MASTERDATA_UNIT_NOT_FOUND"},
		{"负重量", ProductCreateInput{Code: "P2", Name: "x", Weight: num("-1")}, "COMMON_INVALID_PARAM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateProduct(t.Context(), testActor(1), tc.in)
			require.Equal(t, tc.want, codeOf(t, err))
		})
	}
}

func TestProductStatusDisableCascadesSKUs(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	repo.seedSKU("SKU-A", pid, true, nil)
	repo.seedSKU("SKU-B", pid, true, nil)
	repo.seedSKU("SKU-C", pid, false, nil) // 已停用，不计级联
	other := repo.seedProduct("P002", StatusEnabled, nil)
	repo.seedSKU("SKU-D", other, true, nil) // 其他商品不受影响

	cascaded, err := svc.UpdateProductStatus(t.Context(), testActor(7), pid, StatusDisabled)
	require.NoError(t, err)
	require.EqualValues(t, 2, cascaded)

	p, _ := repo.FindProductByID(t.Context(), pid)
	require.Equal(t, StatusDisabled, p.Status)
	for _, code := range []string{"SKU-A", "SKU-B"} {
		s, _ := repo.FindSKUByCode(t.Context(), code)
		require.False(t, s.IsEnabled, "级联停用 %s", code)
	}
	d, _ := repo.FindSKUByCode(t.Context(), "SKU-D")
	require.True(t, d.IsEnabled, "不波及其他商品的 SKU")

	entries := spy.all()
	require.Len(t, entries, 1)
	require.Equal(t, "status", entries[0].Action)
	require.Contains(t, string(entries[0].AfterSnapshot), `"cascade_disabled_skus":2`)
}

func TestDeleteProductBlockedBySKU(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	repo.seedSKU("SKU-A", pid, true, nil)

	err := svc.DeleteProduct(t.Context(), testActor(1), pid)
	require.Equal(t, "MASTERDATA_PRODUCT_HAS_SKU", codeOf(t, err))
	require.Empty(t, spy.all())

	// 停用可替代删除（business-flow §1.4 精神：被引用对象走停用）
	_, err = svc.UpdateProductStatus(t.Context(), testActor(1), pid, StatusDisabled)
	require.NoError(t, err)
}

func TestDeleteProductOK(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)

	require.NoError(t, svc.DeleteProduct(t.Context(), testActor(7), pid))
	got, err := repo.FindProductByID(t.Context(), pid)
	require.NoError(t, err)
	require.Nil(t, got, "软删后默认作用域不可见")

	entries := spy.all()
	require.Len(t, entries, 1)
	require.Equal(t, "delete", entries[0].Action)
	require.Contains(t, string(entries[0].BeforeSnapshot), `"code":"P001"`)
}

func TestUpdateProductSnapshot(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)

	_, err := svc.UpdateProduct(t.Context(), testActor(7), pid, ProductUpdateInput{
		Name: strPtr("矿泉水(新)"), Weight: numPtr("1.5"),
	})
	require.NoError(t, err)

	p, _ := repo.FindProductByID(t.Context(), pid)
	require.Equal(t, "矿泉水(新)", p.Name)
	require.Equal(t, num("1.5"), p.Weight)

	entries := spy.all()
	require.Len(t, entries, 1)
	require.Equal(t, "update", entries[0].Action)
	require.Contains(t, string(entries[0].BeforeSnapshot), `"name":"商品P001"`)
	require.Contains(t, string(entries[0].AfterSnapshot), `"name":"矿泉水(新)"`)
}

func TestListProductsPagination(t *testing.T) {
	svc, repo, _ := newTestService(t)
	for _, code := range []string{"A", "B", "C", "D", "E"} {
		repo.seedProduct(code, StatusEnabled, nil)
	}
	items, total, err := svc.ListProducts(t.Context(), ProductListFilter{Page: 2, PageSize: 2})
	require.NoError(t, err)
	require.EqualValues(t, 5, total)
	require.Len(t, items, 2)
	require.Equal(t, "C", items[0].Code) // id 升序稳定分页
}

// ---- SKU 与条码 ----

func TestCreateSKUBatchExpiryFlags(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)

	// 效期必须依赖批次（inventory-rules §6/§7：效期是批次属性）
	_, err := svc.CreateSKU(t.Context(), testActor(1), SKUCreateInput{
		Code: "SKU-1", ProductID: pid, IsExpiryManaged: true,
	})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	require.Empty(t, spy.all())

	v, err := svc.CreateSKU(t.Context(), testActor(1), SKUCreateInput{
		Code: "SKU-1", ProductID: pid,
		IsBatchManaged: true, IsExpiryManaged: true, IsSerialManaged: true,
		CostPrice: num("2.5"), SalePrice: num("3.8"), SafetyStock: num("10"),
		MaxStock: num("100"), MinReplenishQty: num("5"),
		Barcodes: []BarcodeInput{{Barcode: "6901234567892", IsPrimary: true}, {Barcode: "6901234567893"}},
	})
	require.NoError(t, err)
	require.True(t, v.IsBatchManaged && v.IsExpiryManaged && v.IsSerialManaged)
	require.True(t, v.IsEnabled) // 缺省启用
	require.Len(t, v.Barcodes, 2)
	require.True(t, v.Barcodes[0].IsPrimary)

	s, _ := repo.FindSKUByCode(t.Context(), "SKU-1")
	require.True(t, s.IsBatchManaged)
	bcs, _ := repo.ListBarcodesBySKUs(t.Context(), []int64{s.ID.Int64()})
	require.Len(t, bcs, 2)
	require.Equal(t, CodeTypeDefault, bcs[0].CodeType) // 类型缺省 CODE128
}

func TestCreateSKUProductGuards(t *testing.T) {
	svc, repo, _ := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	disabled := repo.seedProduct("P002", StatusDisabled, nil)
	repo.seedSKU("SKU-EXIST", pid, true, nil)

	cases := []struct {
		name string
		in   SKUCreateInput
		want string
	}{
		{"商品不存在", SKUCreateInput{Code: "SKU-X", ProductID: 999}, "MASTERDATA_PRODUCT_NOT_FOUND"},
		{"商品已停用", SKUCreateInput{Code: "SKU-X", ProductID: disabled}, "MASTERDATA_PRODUCT_DISABLED"},
		{"编码重复", SKUCreateInput{Code: "SKU-EXIST", ProductID: pid}, "MASTERDATA_SKU_CODE_EXISTS"},
		{"最大库存小于安全库存", SKUCreateInput{Code: "SKU-X", ProductID: pid, SafetyStock: num("50"), MaxStock: num("10")}, "COMMON_INVALID_PARAM"},
		{"负成本", SKUCreateInput{Code: "SKU-X", ProductID: pid, CostPrice: num("-0.01")}, "COMMON_INVALID_PARAM"},
		{"主条码多于一个", SKUCreateInput{Code: "SKU-X", ProductID: pid,
			Barcodes: []BarcodeInput{{Barcode: "B1", IsPrimary: true}, {Barcode: "B2", IsPrimary: true}}}, "COMMON_INVALID_PARAM"},
		{"列表内条码重复", SKUCreateInput{Code: "SKU-X", ProductID: pid,
			Barcodes: []BarcodeInput{{Barcode: "B1"}, {Barcode: " B1 "}}}, "COMMON_INVALID_PARAM"},
		{"条码为空", SKUCreateInput{Code: "SKU-X", ProductID: pid, Barcodes: []BarcodeInput{{Barcode: "  "}}}, "COMMON_INVALID_PARAM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateSKU(t.Context(), testActor(1), tc.in)
			require.Equal(t, tc.want, codeOf(t, err))
		})
	}
}

func TestCreateSKUBarcodeGlobalUnique(t *testing.T) {
	svc, repo, _ := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	other := repo.seedProduct("P002", StatusEnabled, nil)
	repo.seedSKU("SKU-B", other, true, nil)
	repo.seedBarcode(other, "6900000000001", false)

	// 条码已被其他 SKU 占用（api.md §4 唯一性校验；uk_barcodes_barcode 兜底）
	_, err := svc.CreateSKU(t.Context(), testActor(1), SKUCreateInput{
		Code: "SKU-A", ProductID: pid, Barcodes: []BarcodeInput{{Barcode: "6900000000001"}},
	})
	require.Equal(t, "MASTERDATA_BARCODE_EXISTS", codeOf(t, err))
	var bizErr *response.Error
	require.ErrorAs(t, err, &bizErr)
	details, ok := bizErr.Details.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "6900000000001", details["barcode"]) // details 携带冲突条码（api.md §4）
}

func TestUpdateSKUReplaceBarcodes(t *testing.T) {
	svc, repo, _ := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	sid := repo.seedSKU("SKU-A", pid, true, nil)
	repo.seedBarcode(sid, "OLD-1", true)

	empty := []BarcodeInput{}
	_, err := svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{
		SalePrice: numPtr("9.9"),
		Barcodes:  &empty,
	})
	require.NoError(t, err)
	bcs, _ := repo.ListBarcodesBySKUs(t.Context(), []int64{sid})
	require.Empty(t, bcs, "空切片=清空条码")

	_, err = svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{
		Barcodes: &[]BarcodeInput{{Barcode: "NEW-1", IsPrimary: true}, {Barcode: "NEW-2"}},
	})
	require.NoError(t, err)
	bcs, _ = repo.ListBarcodesBySKUs(t.Context(), []int64{sid})
	require.Len(t, bcs, 2)

	// 换成他人占用的条码 → 冲突
	other := repo.seedProduct("P002", StatusEnabled, nil)
	oid := repo.seedSKU("SKU-B", other, true, nil)
	repo.seedBarcode(oid, "TAKEN-1", false)
	taken := []BarcodeInput{{Barcode: "TAKEN-1"}}
	_, err = svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{Barcodes: &taken})
	require.Equal(t, "MASTERDATA_BARCODE_EXISTS", codeOf(t, err))

	// 保留自身现有条码（替换列表含自身码）→ 合法
	self := []BarcodeInput{{Barcode: "NEW-1", IsPrimary: true}, {Barcode: "NEW-2"}}
	_, err = svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{Barcodes: &self})
	require.NoError(t, err)
}

func TestUpdateSKUFlagsAndNotFound(t *testing.T) {
	svc, repo, _ := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	sid := repo.seedSKU("SKU-A", pid, true, nil)

	// 非批次 SKU 开启效期 → 拒绝
	_, err := svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{IsExpiryManaged: boolPtr(true)})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))

	// 批次+效期同请求开启 → 允许
	_, err = svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{
		IsBatchManaged: boolPtr(true), IsExpiryManaged: boolPtr(true),
	})
	require.NoError(t, err)
	s, _ := repo.FindSKUByID(t.Context(), sid)
	require.True(t, s.IsBatchManaged && s.IsExpiryManaged)

	// 不存在/已软删
	_, err = svc.UpdateSKU(t.Context(), testActor(7), 999, SKUUpdateInput{})
	require.Equal(t, "MASTERDATA_SKU_NOT_FOUND", codeOf(t, err))
	require.NoError(t, svc.DeleteSKU(t.Context(), testActor(7), sid))
	_, err = svc.UpdateSKU(t.Context(), testActor(7), sid, SKUUpdateInput{SalePrice: numPtr("1")})
	require.Equal(t, "MASTERDATA_SKU_NOT_FOUND", codeOf(t, err))
}

func TestSKUStatusAndDelete(t *testing.T) {
	svc, repo, spy := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	sid := repo.seedSKU("SKU-A", pid, true, nil)
	repo.seedBarcode(sid, "KEEP-1", false)

	require.NoError(t, svc.UpdateSKUStatus(t.Context(), testActor(7), sid, false))
	s, _ := repo.FindSKUByID(t.Context(), sid)
	require.False(t, s.IsEnabled)
	entries := spy.all()
	require.Len(t, entries, 1)
	require.Equal(t, "status", entries[0].Action)
	require.Equal(t, "sku", entries[0].ObjectType)

	require.NoError(t, svc.DeleteSKU(t.Context(), testActor(7), sid))
	got, _ := repo.FindSKUByID(t.Context(), sid)
	require.Nil(t, got, "软删后不可见")
	// 条码命名空间保留（扫码追溯安全，见 service_sku.go 文件头注释）
	b, _ := repo.FindBarcode(t.Context(), "KEEP-1")
	require.NotNil(t, b)

	entries = spy.all()
	require.Len(t, entries, 2)
	require.Equal(t, "delete", entries[1].Action)
}

func TestSKUDetailAssemblesProductAndBarcodes(t *testing.T) {
	svc, repo, _ := newTestService(t)
	pid := repo.seedProduct("P001", StatusEnabled, nil)
	sid := repo.seedSKU("SKU-A", pid, true, nil)
	repo.seedBarcode(sid, "B-1", false)

	v, err := svc.GetSKU(t.Context(), sid)
	require.NoError(t, err)
	require.Equal(t, "P001", v.ProductCode)
	require.Equal(t, "商品P001", v.ProductName)
	require.Len(t, v.Barcodes, 1)
	require.Equal(t, "B-1", v.Barcodes[0].Barcode)

	_, err = svc.GetSKU(t.Context(), 999)
	require.Equal(t, "MASTERDATA_SKU_NOT_FOUND", codeOf(t, err))
}

// ---- 分类 ----

func TestCategoryTreeRules(t *testing.T) {
	svc, repo, _ := newTestService(t)
	root := repo.seedCategory("ROOT", StatusEnabled, 0)
	child := repo.seedCategory("CHILD", StatusEnabled, root)
	_ = child

	// 上级不存在/停用
	_, err := svc.CreateCategory(t.Context(), testActor(1), CategoryCreateInput{Code: "C1", Name: "x", ParentID: id64(999)})
	require.Equal(t, "MASTERDATA_CATEGORY_NOT_FOUND", codeOf(t, err))

	// 环：把 ROOT 挂到 CHILD 下（CHILD 是 ROOT 的下级）
	_, err = svc.UpdateCategory(t.Context(), testActor(1), root, CategoryUpdateInput{ParentID: &child})
	require.Equal(t, "MASTERDATA_CATEGORY_CYCLE", codeOf(t, err))

	// 自引用
	self := root
	_, err = svc.UpdateCategory(t.Context(), testActor(1), root, CategoryUpdateInput{ParentID: &self})
	require.Equal(t, "MASTERDATA_CATEGORY_CYCLE", codeOf(t, err))

	// 提升为顶级（parent_id=0）
	_, err = svc.UpdateCategory(t.Context(), testActor(1), child, CategoryUpdateInput{ParentID: id64(0)})
	require.NoError(t, err)
	c, _ := repo.FindCategoryByID(t.Context(), child)
	require.Nil(t, c.ParentID)

	// 编码唯一
	dup := child
	_, err = svc.CreateCategory(t.Context(), testActor(1), CategoryCreateInput{Code: "CHILD", Name: "x", ParentID: &dup})
	require.Equal(t, "MASTERDATA_CATEGORY_CODE_EXISTS", codeOf(t, err))
}

func TestCategoryDisableGuards(t *testing.T) {
	svc, repo, _ := newTestService(t)
	root := repo.seedCategory("ROOT", StatusEnabled, 0)
	child := repo.seedCategory("CHILD", StatusEnabled, root)

	// 启用中的子分类阻止停用
	err := svc.UpdateCategoryStatus(t.Context(), testActor(1), root, StatusDisabled)
	require.Equal(t, "MASTERDATA_CATEGORY_HAS_ENABLED_CHILDREN", codeOf(t, err))

	// 子分类停用后，启用中的商品引用阻止停用
	require.NoError(t, svc.UpdateCategoryStatus(t.Context(), testActor(1), child, StatusDisabled))
	pid := repo.seedProduct("P001", StatusEnabled, func(p *Product) {
		id := database.ID(root)
		p.CategoryID = &id
	})
	err = svc.UpdateCategoryStatus(t.Context(), testActor(1), root, StatusDisabled)
	require.Equal(t, "MASTERDATA_CATEGORY_IN_USE", codeOf(t, err))

	// 商品停用后（引用仍在但非启用）→ 允许停用分类
	_, err = svc.UpdateProductStatus(t.Context(), testActor(1), pid, StatusDisabled)
	require.NoError(t, err)
	require.NoError(t, svc.UpdateCategoryStatus(t.Context(), testActor(1), root, StatusDisabled))
}

// ---- 单位 ----

func TestUnitRules(t *testing.T) {
	svc, repo, _ := newTestService(t)
	uid := repo.seedUnit("PCS", StatusEnabled)
	repo.seedProduct("P001", StatusEnabled, func(p *Product) {
		id := database.ID(uid)
		p.UnitID = &id
	})

	// 编码唯一
	_, err := svc.CreateUnit(t.Context(), testActor(1), UnitCreateInput{Code: "PCS", Name: "件"})
	require.Equal(t, "MASTERDATA_UNIT_CODE_EXISTS", codeOf(t, err))

	// 被商品引用阻止停用（任何未删商品）
	err = svc.UpdateUnitStatus(t.Context(), testActor(1), uid, StatusDisabled)
	require.Equal(t, "MASTERDATA_UNIT_IN_USE", codeOf(t, err))

	// 商品软删后引用解除 → 允许停用
	require.NoError(t, svc.DeleteProduct(t.Context(), testActor(1),
		func() int64 {
			p, _ := repo.FindProductByCode(t.Context(), "P001")
			return p.ID.Int64()
		}()))
	require.NoError(t, svc.UpdateUnitStatus(t.Context(), testActor(1), uid, StatusDisabled))

	// 非法状态值
	err = svc.UpdateUnitStatus(t.Context(), testActor(1), uid, "PAUSED")
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
}

// ---- 供应商 / 客户 ----

func TestSupplierLifecycle(t *testing.T) {
	svc, repo, spy := newTestService(t)
	v, err := svc.CreateSupplier(t.Context(), testActor(7), SupplierCreateInput{
		Code: "SUP-01", Name: "华东饮水", Contact: "李四", Phone: "138-0000-0000", Email: "li@sup.cn",
	})
	require.NoError(t, err)
	require.Equal(t, StatusEnabled, v.Status)

	_, err = svc.CreateSupplier(t.Context(), testActor(7), SupplierCreateInput{Code: "SUP-01", Name: "重复"})
	require.Equal(t, "MASTERDATA_SUPPLIER_CODE_EXISTS", codeOf(t, err))

	_, err = svc.UpdateSupplier(t.Context(), testActor(7), v.ID.Int64(), SupplierUpdateInput{
		Email: strPtr("bad-email"),
	})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))

	_, err = svc.UpdateSupplier(t.Context(), testActor(7), v.ID.Int64(), SupplierUpdateInput{
		Contact: strPtr("李四(新)"), Remark: strPtr("年度框架"),
	})
	require.NoError(t, err)

	require.NoError(t, svc.UpdateSupplierStatus(t.Context(), testActor(7), v.ID.Int64(), StatusDisabled))
	require.NoError(t, svc.DeleteSupplier(t.Context(), testActor(7), v.ID.Int64()))
	got, _ := repo.FindSupplierByID(t.Context(), v.ID.Int64())
	require.Nil(t, got, "软删后不可见")

	// 停用+删除 → 恰两行审计；软删后编码可复用（部分唯一索引语义）
	entries := spy.all()
	require.Len(t, entries, 4) // create + update + status + delete
	statuses := []string{entries[0].Action, entries[1].Action, entries[2].Action, entries[3].Action}
	require.Equal(t, []string{"create", "update", "status", "delete"}, statuses)

	v2, err := svc.CreateSupplier(t.Context(), testActor(7), SupplierCreateInput{Code: "SUP-01", Name: "复用编码"})
	require.NoError(t, err)
	require.NotEqual(t, v.ID, v2.ID)
}

func TestCustomerLifecycle(t *testing.T) {
	svc, repo, _ := newTestService(t)
	v, err := svc.CreateCustomer(t.Context(), testActor(7), CustomerCreateInput{
		Code: "CUS-01", Name: "便利连锁", ShippingAddress: "广州市天河区 1 号",
	})
	require.NoError(t, err)
	require.Equal(t, "广州市天河区 1 号", v.ShippingAddress)

	_, err = svc.CreateCustomer(t.Context(), testActor(7), CustomerCreateInput{Code: "CUS-01", Name: "重复"})
	require.Equal(t, "MASTERDATA_CUSTOMER_CODE_EXISTS", codeOf(t, err))

	_, err = svc.UpdateCustomer(t.Context(), testActor(7), v.ID.Int64(), CustomerUpdateInput{
		Phone: strPtr("020-88886666"), Address: strPtr("珠海市"),
	})
	require.NoError(t, err)
	require.NoError(t, svc.UpdateCustomerStatus(t.Context(), testActor(7), v.ID.Int64(), StatusDisabled))
	require.NoError(t, svc.DeleteCustomer(t.Context(), testActor(7), v.ID.Int64()))
	got, _ := repo.FindCustomerByID(t.Context(), v.ID.Int64())
	require.Nil(t, got)

	_, err = svc.GetCustomer(t.Context(), 999)
	require.Equal(t, "MASTERDATA_CUSTOMER_NOT_FOUND", codeOf(t, err))
}
