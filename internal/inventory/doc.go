// Package inventory 库存引擎域（backend-m1-plan §2/§8，scope F：库存核心工程师实现）。
//
// 职责：全系统库存与流水的唯一变更入口（plan §8.2：Putaway/Deduct/Lock/ReleaseLock/
// MoveBin/InspectResult/Adjust，签名面向 M2 业务单据域复用；另有 EnsureBatch/SerialEvent
// 批次与序列号台账入口）、append-only 流水（§8.4）、幂等（§8.5）、并发原子 UPDATE（§8.3）、
// 库存/流水/批次/序列号查询 API。
//
// M1 HTTP 面只读（§8.7）：GET /api/inventory（列表/详情）、GET /api/inventory-ledgers、
// GET /api/batches、GET /api/serials；写操作以 Service 层为边界，被 M2 消费，不暴露 HTTP。
//
// 核心不变量（inventory-rules §2，三道防线）：
//  1. Service 每条 UPDATE 成对改列、和不变（构造保证，identity.go/StockState）；
//  2. PostgreSQL CHECK chk_inventory_identity（迁移 000005）——被破坏的写库无法提交；
//  3. StockState.ValidateIdentity 显式校验（测试与调用方断言，testing.md §4）。
//
// 分层（architecture.md §1）：handler.go → service.go/query.go → repository.go（原生 SQL）
// → database；模型仅定义/导出于本包 models.go（plan §4.2 判据 3）。
//
// 导出面速览：
//   - 入口：RegisterRoutes（§5.2 冻结签名）、NewService、BinOccupancy（§4.3 ②）；
//   - 变更：Putaway/Deduct/Lock/ReleaseLock/MoveBin/InspectResult/Adjust/EnsureBatch/SerialEvent；
//   - 策略：AllocateFEFO/AllocateFIFO（纯函数，inventory-rules §6/§7.2）；
//   - 不变量：Qty（numeric(18,4) 精确十进制）、StockState.ValidateIdentity/AvailableOf。
package inventory
