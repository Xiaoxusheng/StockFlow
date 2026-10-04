// Package returns 退货域（backend-m2-plan §2.1 scope R）：销售退货、采购退货、异常中心与库存追溯。
//
// 业务依据（docs/ 唯一真相来源）：
//   - business-flow §9.1 销售退货：退货申请 → 审核 → 收货 → 质检 → 正常库存/不良品
//     （质检结果决定去向：合格入 available，不合格入 defective）；
//   - business-flow §9.2 采购退货：退货申请 → 审核 → 退货出库 → 供应商（扣减库存并留完整流水，
//     不建 outbound_order——plan §6.9，闭环在本域，追溯经库存流水 business_no）；
//   - business-flow §11.2 / plan §6.10 异常中心：九类异常统一生命周期
//     OPEN→ASSIGNED→PROCESSING→PENDING_REVIEW→RESOLVED→CLOSED，异常冻结（EXCEPTION_FREEZE）
//     在创建时可选触发、RESOLVED/CLOSED 必须释放（inventory-rules §4.2）；
//   - inventory-rules §10 库存追溯：库存当前状态 → 当前库位 → 批次 → 入库来源 → 调拨 →
//     出库 → 退货，数据来源 = 库存流水 + 业务单据 + 操作日志（不建追溯中间表，plan §5 000010 注）。
//
// 状态机（business-flow §13.2：所有状态变化必须经业务方法守卫）：
//   - 销售退货 DRAFT→PENDING_APPROVAL→APPROVED→RECEIVING→IN_QC→COMPLETED（未收货前可 CANCELLED，
//     PENDING_APPROVAL 可驳回回 DRAFT）；采购退货 DRAFT→PENDING_APPROVAL→APPROVED→SHIPPED→COMPLETED
//     （未出库前可 CANCELLED）；每次迁移 = 带前置态 WHERE 的守卫 UPDATE（0 行即冲突）+
//     同事务 document_approvals（submit/approve/reject/cancel）与 middleware.Audit 快照；
//   - 状态迁移时间列按 business-flow §13.4 落 approved_at/received_at/qc_at/completed_at/cancelled_at。
//
// 库存一致性（inventory-rules §1）：本域不写任何库存族表（plan §2.3 判据 3），
// 全部库存变化经 StockGateway 消费 internal/inventory.Service 原语（Putaway/Deduct/Lock/
// ReleaseLock/InspectResult/SerialEvent/EnsureBatch），库存变化与单据状态迁移同事务
// （architecture §4：单外层事务，任一步失败整体回滚）。
//
// 幂等（architecture §3.2 / plan §7）：库存变更类动作的幂等准绳是
// inventory_ledgers.idempotency_key 部分唯一索引——退货收货（putaway:*）、退货质检
// （inspect:*）、采购退货出库（prt:*:lock / :deduct 两段）、异常冻结/解冻（efreeze:* /
// release:*）均为确定性键，重试命中重放路径不重复变更；多行请求的"部分重放"视为矛盾
// 请求整体回滚（ErrPartialReplay）。退货单/异常单 HTTP 面不设独立幂等键列（plan §5 000010
// 冻结 DDL 无该列），收货支持 Idempotency-Key 头透传给原语（plan §5 幂等口径：事件型收货）。
//
// 跨域（plan §3.1 消费方窄接口 + router 装配注入，域包之间禁止 import）：
// 本包定义 StockGateway / SalesOrderReader / PurchaseOrderReader / QCCreator / LedgerReader /
// StockStateReader 六个消费接口（ports.go），并导出 ExceptionCreator 实现（CreateException，
// 供 purchase/sales/stockops 经各自消费接口在 router 闭包桥接调用）。
package returns
