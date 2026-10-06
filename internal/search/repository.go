package search

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
)

// 只读跨域 SQL 直查（workbench.go 同口径：平台包禁 import 各域包，全部为参数化
// SELECT，零写语句；guard-readonly 红线）。排序契约：精确匹配置顶 > 前缀 > 包含，
// updated_at DESC 兜底（efficiency-layer-phase1 §2.1）。
//
// 列名均经 db/migrations/000003/000004/000005/000007/000008/000009/000010 DDL 复核：
// skus 无 name 列（SKU 名称在 products.name，JOIN 关联匹配）；batches 无仓库列
// （批次维度不适用仓库过滤）；exceptions 无仓库列；transfer_orders 双仓
// （from/to 任一命中即视为范围内）；shipments 匹配列含物流单号 tracking_no
// （计划 §2.1"若有物流单号列则一并"核验成立）。
// 软删一致性：products/skus/customers/suppliers/warehouses/bins/purchase_orders/
// inbound_orders 域模型含 gorm.DeletedAt（000016 注），SQL 显式 deleted_at IS NULL
// 与列表页可见集对齐；其余表无软删列，不加过滤。

// unit 一个可查询单元（普通 type 一对一；doc 为 7 分支共用 type="doc"，
// 各分支权限码/仓库列独立，服务层逐分支判定权限后合并分组）。
type unit struct {
	typ        string   // 对外 type 值
	groupTitle string   // 组标题（同 type 首个 unit 生效）
	branch     string   // doc 分支名（写入 summary；其余为空）
	perm       string   // 组内 type 权限码（既有读码，permissions.go）
	sel        string   // SELECT 列段（id/title/code/status/summary/updated_at）
	from       string   // FROM/JOIN 段（含别名）
	where      string   // 基础条件（软删过滤等；空=无附加条件）
	match      []string // 匹配列（ILIKE）
	scopeCols  []string // 仓库数据权限列（多列=OR；空=该 unit 不适用仓库过滤）
	updCol     string   // updated_at 限定列（JOIN 歧义消解）
	navKind    string   // navigation.kind（空=回落 typ；doc 七分支/物流为实体名）
}

// navKindOrType navigation.kind 取值（未显式声明的普通 type 与 typ 同值）。
func (u unit) navKindOrType() string {
	if u.navKind != "" {
		return u.navKind
	}
	return u.typ
}

// units type→表/列/权限映射清单（efficiency-layer-phase1 §2.1；顺序即组输出顺序）。
var units = []unit{
	{
		typ: "sku", groupTitle: "SKU", perm: auth.PermSKUList,
		sel:   "s.id, p.name AS title, s.code AS code, CASE WHEN s.is_enabled THEN 'ENABLED' ELSE 'DISABLED' END AS status, '' AS summary, s.updated_at",
		from:  "skus s JOIN products p ON p.id = s.product_id AND p.deleted_at IS NULL",
		where: "s.deleted_at IS NULL",
		match: []string{"s.code", "p.name"}, updCol: "s.updated_at",
	},
	{
		typ: "product", groupTitle: "商品", perm: auth.PermProductList,
		sel:   "p.id, p.name AS title, p.code AS code, p.status AS status, '' AS summary, p.updated_at",
		from:  "products p",
		where: "p.deleted_at IS NULL",
		match: []string{"p.code", "p.name"}, updCol: "p.updated_at",
	},
	{
		typ: "barcode", groupTitle: "条码", perm: auth.PermSKUList,
		sel:   "b.id, b.barcode AS title, b.barcode AS code, '' AS status, b.code_type AS summary, b.updated_at",
		from:  "barcodes b",
		match: []string{"b.barcode"}, updCol: "b.updated_at",
	},
	{
		typ: "batch", groupTitle: "批次", perm: auth.PermBatchList,
		sel:   "bt.id, bt.batch_no AS title, bt.batch_no AS code, '' AS status, '' AS summary, bt.updated_at",
		from:  "batches bt",
		match: []string{"bt.batch_no"}, updCol: "bt.updated_at",
	},
	{
		typ: "serial", groupTitle: "序列号", perm: auth.PermSerialList,
		sel:   "sn.id, sn.serial_no AS title, sn.serial_no AS code, sn.status AS status, '' AS summary, sn.updated_at",
		from:  "serial_numbers sn",
		match: []string{"sn.serial_no"}, scopeCols: []string{"sn.warehouse_id"}, updCol: "sn.updated_at",
	},
	{
		typ: "bin", groupTitle: "库位", perm: auth.PermBinList,
		sel:   "bn.id, bn.code AS title, bn.code AS code, bn.status AS status, '' AS summary, bn.updated_at",
		from:  "bins bn",
		where: "bn.deleted_at IS NULL",
		match: []string{"bn.code"}, scopeCols: []string{"bn.warehouse_id"}, updCol: "bn.updated_at",
	},
	{
		typ: "warehouse", groupTitle: "仓库", perm: auth.PermWarehouseList,
		sel:   "w.id, w.name AS title, w.code AS code, w.status AS status, '' AS summary, w.updated_at",
		from:  "warehouses w",
		where: "w.deleted_at IS NULL",
		match: []string{"w.code", "w.name"}, updCol: "w.updated_at",
	},
	{
		typ: "customer", groupTitle: "客户", perm: auth.PermCustomerList,
		sel:   "cu.id, cu.name AS title, cu.code AS code, cu.status AS status, '' AS summary, cu.updated_at",
		from:  "customers cu",
		where: "cu.deleted_at IS NULL",
		match: []string{"cu.code", "cu.name"}, updCol: "cu.updated_at",
	},
	{
		typ: "supplier", groupTitle: "供应商", perm: auth.PermSupplierList,
		sel:   "sp.id, sp.name AS title, sp.code AS code, sp.status AS status, '' AS summary, sp.updated_at",
		from:  "suppliers sp",
		where: "sp.deleted_at IS NULL",
		match: []string{"sp.code", "sp.name"}, updCol: "sp.updated_at",
	},
	// doc 七分支（UNION 语义由服务层合并；各自域 list 码，efficiency-layer-phase1 §2.1）。
	{
		typ: "doc", groupTitle: "单据", branch: "采购订单", perm: auth.PermPurchaseList, navKind: "purchase_order",
		sel:   "o.id, o.po_no AS title, o.po_no AS code, o.status AS status, '采购订单' AS summary, o.updated_at",
		from:  "purchase_orders o",
		where: "o.deleted_at IS NULL",
		match: []string{"o.po_no"}, scopeCols: []string{"o.warehouse_id"}, updCol: "o.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "入库单", perm: auth.PermInboundList, navKind: "inbound_order",
		sel:   "io.id, io.inbound_no AS title, io.inbound_no AS code, io.status AS status, '入库单' AS summary, io.updated_at",
		from:  "inbound_orders io",
		where: "io.deleted_at IS NULL",
		match: []string{"io.inbound_no"}, scopeCols: []string{"io.warehouse_id"}, updCol: "io.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "销售订单", perm: auth.PermSalesList, navKind: "sales_order",
		sel:   "so.id, so.so_no AS title, so.so_no AS code, so.status AS status, '销售订单' AS summary, so.updated_at",
		from:  "sales_orders so",
		match: []string{"so.so_no"}, scopeCols: []string{"so.warehouse_id"}, updCol: "so.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "出库单", perm: auth.PermOutboundList, navKind: "outbound_order",
		sel:   "ob.id, ob.outbound_no AS title, ob.outbound_no AS code, ob.status AS status, '出库单' AS summary, ob.updated_at",
		from:  "outbound_orders ob",
		match: []string{"ob.outbound_no"}, scopeCols: []string{"ob.warehouse_id"}, updCol: "ob.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "调拨单", perm: auth.PermTransferList, navKind: "transfer_order",
		sel:       "t.id, t.transfer_no AS title, t.transfer_no AS code, t.status AS status, '调拨单' AS summary, t.updated_at",
		from:      "transfer_orders t",
		match:     []string{"t.transfer_no"},
		scopeCols: []string{"t.from_warehouse_id", "t.to_warehouse_id"}, updCol: "t.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "盘点单", perm: auth.PermCountList, navKind: "count_order",
		sel:   "c.id, c.count_no AS title, c.count_no AS code, c.status AS status, '盘点单' AS summary, c.updated_at",
		from:  "count_orders c",
		match: []string{"c.count_no"}, scopeCols: []string{"c.warehouse_id"}, updCol: "c.updated_at",
	},
	{
		typ: "doc", groupTitle: "单据", branch: "异常单", perm: auth.PermExceptionList, navKind: "exception",
		sel:   "e.id, e.exception_no AS title, e.exception_no AS code, e.status AS status, '异常单' AS summary, e.updated_at",
		from:  "exceptions e",
		match: []string{"e.exception_no"}, updCol: "e.updated_at",
	},
	{
		typ: "logistics", groupTitle: "物流", perm: auth.PermShipmentList, navKind: "shipment",
		sel:       "sh.id, sh.shipment_no AS title, sh.shipment_no AS code, sh.status AS status, sh.carrier AS summary, sh.updated_at",
		from:      "shipments sh",
		match:     []string{"sh.shipment_no", "sh.tracking_no", "sh.outbound_no"},
		scopeCols: []string{"sh.warehouse_id"}, updCol: "sh.updated_at",
	},
}

// validTypes type 参数白名单（units 全集）。
func validTypes() map[string]bool {
	m := make(map[string]bool, len(units))
	for _, u := range units {
		m[u.typ] = true
	}
	return m
}

// row 搜索单元行（match_rank/match_total 为组装列，不出契约）。
type row struct {
	ID         int64             `gorm:"column:id"`
	Title      string            `gorm:"column:title"`
	Code       string            `gorm:"column:code"`
	Status     string            `gorm:"column:status"`
	Summary    string            `gorm:"column:summary"`
	UpdatedAt  database.JSONTime `gorm:"column:updated_at"`
	MatchRank  int               `gorm:"column:match_rank"`
	MatchTotal int64             `gorm:"column:match_total"`
}

// Repository 搜索只读仓储（接口形态供测试替身替换）。
type Repository interface {
	SearchUnit(ctx context.Context, u unit, f filter) ([]row, int64, error)
}

// repository 搜索只读查询（Handler→Service→Repository 分层，architecture §1）。
type repository struct {
	db *gorm.DB
}

// newRepository 构建仓储。
func newRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// filter 查询过滤（服务层从 gin 上下文装配，禁止仓储直读请求）。
type filter struct {
	q     string  // 关键词（TrimSpace 后）
	all   bool    // 数据权限：全部仓库
	whIDs []int64 // 数据权限：可见仓库集
	wh    int64   // 收窄过滤器 warehouse_id（0=不过滤）
	limit int
}

// escapeLike ILIKE 通配符转义（%/_/\ 字面化，防结果集污染；PG 默认转义符为反斜杠）。
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

// joinCols 列表达式拼接（suffix 为 " ILIKE ?" / " = ?" 形态）。
func joinCols(cols []string, suffix string) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, c+suffix)
	}
	return strings.Join(parts, " OR ")
}

// containsInt64 集合含值判定。
func containsInt64(ids []int64, v int64) bool {
	for _, id := range ids {
		if id == v {
			return true
		}
	}
	return false
}

// scopeCond 仓库数据权限条件：收窄过滤器 warehouse_id 与 scopeOf 求交
// （efficiency-layer-phase1 §2.1）。ok=false 表示交集为空——该 unit 无结果，
// 不执行 SQL（越界仓库按无结果处理，不 403，防范围探测；空范围 fail-closed 同口径）。
func (u unit) scopeCond(f filter) (string, []any, bool) {
	if len(u.scopeCols) == 0 {
		return "", nil, true // 该 unit 不适用仓库维度（基础资料/批次/异常单等）
	}
	ids, all := f.whIDs, f.all
	if f.wh > 0 {
		if !all && !containsInt64(ids, f.wh) {
			return "", nil, false // 越界仓库：交集为空，零结果
		}
		ids, all = []int64{f.wh}, false
	} else if all {
		return "", nil, true
	}
	if len(ids) == 0 {
		return "", nil, false // 指定仓库范围未绑定/部门等范围 → 不可见任何仓（fail-closed）
	}
	parts := make([]string, 0, len(u.scopeCols))
	args := make([]any, 0, len(u.scopeCols))
	for _, col := range u.scopeCols {
		parts = append(parts, col+" IN ?")
		args = append(args, ids)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}

// SearchUnit 单元搜索：ILIKE 参数化匹配 + 精确/前缀/包含三档排名 + updated_at DESC；
// match_total 经 COUNT(*) OVER () 同查询取匹配总数（组 count 口径，免二次 COUNT）。
func (r *repository) SearchUnit(ctx context.Context, u unit, f filter) ([]row, int64, error) {
	scopeCond, scopeArgs, ok := u.scopeCond(f)
	if !ok {
		return nil, 0, nil
	}
	pat := "%" + escapeLike(f.q) + "%"
	pre := escapeLike(f.q) + "%"
	conds := make([]string, 0, 3)
	if u.where != "" {
		conds = append(conds, u.where)
	}
	conds = append(conds, "("+joinCols(u.match, " ILIKE ?")+")")
	if scopeCond != "" {
		conds = append(conds, scopeCond)
	}
	sql := "SELECT " + u.sel +
		", CASE WHEN (" + joinCols(u.match, " = ?") + ") THEN 0 WHEN (" + joinCols(u.match, " ILIKE ?") + ") THEN 1 ELSE 2 END AS match_rank" +
		", COUNT(*) OVER () AS match_total" +
		" FROM " + u.from +
		" WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY match_rank, " + u.updCol + " DESC LIMIT ?"
	args := make([]any, 0, 3*len(u.match)+len(scopeArgs)+1)
	for range u.match {
		args = append(args, pat)
	}
	args = append(args, scopeArgs...)
	for range u.match {
		args = append(args, f.q)
	}
	for range u.match {
		args = append(args, pre)
	}
	args = append(args, f.limit)
	rows := make([]row, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("search: %s(%s) 查询失败: %w", u.typ, u.branch, err)
	}
	var total int64
	if len(rows) > 0 {
		total = rows[0].MatchTotal
	}
	return rows, total, nil
}
