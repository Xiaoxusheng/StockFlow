// Package purchase 采购入库域（backend-m2-plan §2.2 工程师 P 独占包）：
// 以采购订单为源头、入库单为骨架的"收货 → 质检 → 上架"主链路，
// 批次/效期/序列号三开关分支采集，交付业务闭环"采购入库"
// （business-flow §2–§5、requirements.md 场景 1）。
//
// 交付面：
//   - 采购订单状态机与审核（business-flow §2.2/§12.1、plan §6.1）与 §2.3 四量约束
//     （原始数量 ≥ 累计收货，累计收货含合格与不合格待定，拒收单独记录——防超量收货）；
//   - 入库单 + 收货：完整/部分/异常收货（§3.2–§3.4），收货确认幂等
//     （幂等键 + uk_receipts_idempotency 部分唯一索引，architecture §3.2）；
//   - 质检单：检验方式与九类处理结果（§4），执行事务调库存原语 InspectResult
//     （pending_inspect → available/defective）；
//   - 上架任务：PENDING→IN_PROGRESS（原子抢占 UPDATE ... WHERE status='PENDING'，
//     architecture §5.2）→COMPLETED（inventory.Putaway 落账，BinChecker 校验库位）；
//   - API：/api/purchases、/api/inbounds、/api/receipts、/api/quality、/api/putaway
//     （api.md §1 领域划分），全部挂 auth.RequirePermission（权限点常量本包导出，
//     供集成工程师收编 internal/auth/permissions.go——plan §9.2）。
//
// 分层（architecture.md §1）：Handler（参数/响应）→ Service（状态机/事务/校验）→
// Repository（数据访问）→ Database；库存变化只能经 StockGateway 消费接口
// （internal/stock 值类型 + inventory.Service 实现，router 装配注入，plan §3）。
// 事务边界在 Service（plan §2.3 判据 6）：单据状态迁移 + 库存原语 + document_approvals
// + middleware.Audit 同事务（architecture §4），任何步骤失败整体回滚。
package purchase
