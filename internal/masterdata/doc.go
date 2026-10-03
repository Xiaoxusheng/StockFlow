// Package masterdata 基础资料域（backend-m1-plan §2，scope D：基础资料工程师交付）。
//
// 职责：商品/SKU（含条码）/分类/单位/供应商/客户的完整链路 API→Service→Repository→GORM
// （architecture.md §1；api.md §1/§2/§4、business-flow §1.1–§1.5、database.md §3/§5）。
//
// 交付形态（RegisterRoutes 签名冻结于 plan §5.2，不得变更）：
//   - 路由（plan §5.4）：/api/products、/api/skus、/api/product-categories、/api/units、
//     /api/suppliers、/api/customers——CRUD + 启停 + 软删除（分类/单位无删除，停用即终点），
//     每条路由挂 auth.RequirePermission（权限点清单 internal/auth/permissions.go，与
//     plan §5.4.1 冻结清单及权限种子同源）；
//   - 完整校验（api.md §4）：编码格式/唯一、删除被引用（商品↔SKU 级联约束）、
//     停用被引用（分类/单位）、条码全局唯一（一码一 SKU）、SKU 三开关一致性
//     （效期依赖批次，inventory-rules §6–§8）、数量/金额 numeric(18,4) 非负；
//   - 审计（architecture.md §8.1、plan §4.4）：增/改/删/启停全部经 middleware.Audit
//     与业务同事务写 operation_logs，关键更新携带 before/after 快照；
//   - 跨域消费（plan §4.3）：NewSKUChecker 导出 SKU 可用性校验（inventory 域
//     WithSKUChecker 注入消费）。
//
// 数据权限（permission.md §4、plan §7.4）：masterdata 为组织级数据（不分仓），
// 不做仓库级行过滤。
//
// 文件结构：models.go（GORM 模型 + jsonb 载体）、number.go（numeric(18,4) 载体）、
// errors.go（MASTERDATA_* 错误码）、repository.go（窄接口 + GORM 实现）、
// service*.go（业务层）、handler.go（HTTP 层）、masterdata.go（路由装配 + SKUChecker）。
package masterdata
