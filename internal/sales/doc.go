// Package sales 销售出库域（backend-m2-plan §2.1 工程师 S scope：internal/sales/** 独占）。
//
// 业务链路（business-flow §6–§8、plan §6.4–§6.5）：以销售订单为源头的
// "审核预占 → 库存分配 → 拣货 → 复核 → 打包 → 发货" 出库执行链。
// 库存预占发生在订单审核（inventory.Lock，ORDER_HOLD→locked，inventory-rules §4），
// 正式扣减发生在发货完成（inventory.Deduct 核销锁，business-flow §7.2）——
// 中间全部通过锁定库存保证一致性。
//
// 状态机（plan §6.4–§6.5 与迁移 000008 CHECK 同源）：
//
//	sales_orders    DRAFT→PENDING_APPROVAL→APPROVED→(PARTIAL_SHIPPED)→SHIPPED_ALL→COMPLETED
//	                PENDING_APPROVAL→REJECTED；DRAFT/PENDING_APPROVAL→CANCELLED；
//	                APPROVED→CANCELLED（未发货，释放全部 ORDER_HOLD）
//	outbound_orders PENDING_ALLOCATE→ALLOCATED→PICKING→PICKED→CHECKED→PACKED
//	                →(PARTIAL_SHIPPED)→SHIPPED_ALL；PENDING_ALLOCATE..PACKED→CANCELLED（未发货）；
//	                PARTIAL_SHIPPED→CLOSED（差额关闭，释放剩余锁）
//	pick_tasks      PENDING→CLAIMED（原子抢占）→PICKED | EXCEPTION；→CANCELLED（单据取消/重分配联动）
//	check_tasks     PENDING→DONE | EXCEPTION（领取为原子指派，不迁移状态——值域无 CLAIMED）
//	shipments       PENDING→SHIPPED（库存动作仅发生在本迁移，plan §6.5）
//	                →IN_TRANSIT→SIGNED / →ABNORMAL（纯记录流转，不接物流 API）
//
// 硬性约束（docs/ 唯一真相来源）：
//   - 库存变化只能经 internal/inventory 的 InventoryService 原语；本域通过
//     StockGateway 窄接口（ports.go）消费 Lock/ReleaseLock/Deduct/SerialEvent，
//     禁止任何直接写库存族六表的代码（plan §2.3 判据 3）；
//   - 对 inventory/batches/serial_numbers 仅限只读 SELECT（分配候选、序列号校验），
//     自带扫描结构体、不引用库存族 GORM 模型（plan §2.3 判据 3 的读侧口径）；
//   - 批次 SKU 分配走 internal/inventory.AllocateFEFO/AllocateFIFO 纯函数
//     （plan §6.4 字面口径：inventory.AllocateFEFO/FIFO 纯函数命中批次）；
//   - 单号一律经 internal/docnum（SO/OUT/PK/CH/BP/SH 前缀，plan §2.3 判据 5）；
//   - 状态迁移全部经业务方法守卫（WHERE status=前置态，影响行数 0 即冲突，
//     business-flow §13.2、plan §2.3 判据 4）；确认类动作幂等（状态守卫 +
//     packing/shipments 的 idempotency_key 部分唯一索引 + 库存原语幂等键，plan §7）；
//   - 关键写操作经 middleware.Audit 与业务同事务写 operation_logs（before/after 快照）；
//   - 事务边界在 Service 层（plan §2.3 判据 6）：每个业务动作 = 单外层事务，
//     单据状态迁移 → 库存原语（传 tx）→ document_approvals/异常联动 → Audit → COMMIT。
//
// 依赖方向（plan §3）：本域只允许 import internal/stock（契约包，MT0 交付——
// 未交付前以 ports.go 本地镜像承载同形冻结值类型，单文件切换）与 internal/auth；
// internal/inventory 仅在 allocation.go 引用其无 IO 纯函数（plan §6.4 字面要求）。
package sales
