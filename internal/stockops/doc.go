// Package stockops 库存作业域（backend-m2-plan §2.1 scope O）：
// 跨仓调拨单（在途两端流水）、盘点单（冻结—实盘—差异—调整）。
//
// 业务依据：business-flow §10（调拨/盘点状态机）、§13（单据规范）、
// inventory-rules §2（在途口径）/§4（盘点锁定）/§5（流水）/§8（序列号逐件）、
// architecture §4（事务边界）/§5（并发互斥）。
//
// 分层（architecture.md §1）：handler.go → transfer.go/count.go（Service，状态机与
// 事务边界）→ store.go/repo_gorm.go（Repository）。库存变化一律经 StockGateway
// （internal/inventory 原语 Lock/ReleaseLock/TransferOut/TransferIn/Adjust/SerialEvent，
// backend-m2-plan §7 消费映射），本包对库存族六表只读不写（枚举/回查用原生 SELECT，
// 扫描进本包本地结构，不引用库存族 GORM 模型、无任何写通路）。
//
// 单据状态机（business-flow §10.1/§10.2，迁移 000009 CHECK 同源）：
//
//	调拨：DRAFT→PENDING_APPROVAL→APPROVED(待出库)→TRANSFERRING(调拨中)
//	      →AWAITING_RECEIPT(待入库)→COMPLETED；APPROVED 及之前可 CANCELLED，
//	      TRANSFERRING/AWAITING_RECEIPT 禁止直接取消（只能反向调拨冲正，§13.3）。
//	盘点：DRAFT→COUNTING→PENDING_REVIEW→COMPLETED；PENDING_REVIEW 可驳回取消；
//	      差异必须走调整单+审批链路，禁止直接改库存（§10.2 硬性规则）。
//
// 并发互斥（architecture §5）：盘点冻结以 COUNT_FREEZE 锁占住范围内可用库存，
// 第二张同范围盘点单冻结时 available 守卫必然不足或命中既有 COUNT_FREEZE 冻结
// → 整单回滚；调拨两端状态迁移以"守卫 UPDATE（WHERE status=前置态）影响行数判定"
// 串行化，库存侧以 inventory_ledgers.idempotency_key 唯一索引兜底。
//
// 编号：统一经 internal/docnum（business-flow §13.1；TR 调拨单/CK 盘点单，按日重置）。
// 审计：全部状态迁移经 middleware.Audit 与业务同事务写 operation_logs（before/after
// 状态快照，§13.2），审批动作同事务落 document_approvals（business-flow §12.2）。
package stockops
