// Package printing 打印中心（backend-m3-plan §7，printing.md §1–§6）。
//
// 职责（Scope P，plan §2.2）：
//   - 打印模板：CRUD + 复制 + 启停（printing.md §2；模板不存 HTML——渲染由前端
//     react-to-print 独立打印层承担，后端只管数据与快照，plan §7.1）；
//   - 打印任务：创建（装配在任务创建时同步完成）/ 列表 / 详情（渲染数据包）/
//     执行确认（回填 result/printed_by/printed_at）/ 打印历史（printing.md §1.2 五字段）；
//   - 条码/二维码：GET /api/prints/barcode PNG 生成（boombuler/barcode：
//     CODE128/CODE39/EAN13/EAN8/UPC/QR/DATAMATRIX，printing.md §4.1）；
//   - asynqx render handler：printing:task:render——条码 PNG 预生成与进程内缓存
//     （plan §4.2，PNG 不落 files 表，不重复装配）。
//
// 冻结契约要点：
//   - 单号：docnum PT 前缀（plan §12.3，ResetDay 6 位流水；发放与任务落库同事务）；
//   - 上限：单任务 data_ids ≤ 500（Orchestrator 裁决④，plan §7.2/§18.4）；
//   - 不做服务端 PDF（Orchestrator 裁决③，plan §7.2/§12.4 条 3）：打印产物 =
//     渲染数据包（print_task_rows）+ 条码图片端点；实际打印与"另存 PDF"由前端完成；
//   - 按筛选打印不交付：BY_FILTER 是 datax 导出范围值（excel §2.2），打印任务创建
//     冻结为 {template_id, data_ids, copies}（plan §7.2），波次级全量单据打印需前端
//     按上限分片（plan §18.4）；
//   - CARTON_CODE/PALLET_CODE 承接：托盘/箱码业务域未建（plan §15），由本包内置
//     reader 承接——data_ids 即码值原文，不装配业务字段（plan §7.2）。
//
// 跨域机制（plan §12.2）：ContentReader 窄接口在本包定义（值类型 ContentRow），
// 实现落各域包 printing_content.go（§2.2 冻结清单），router 装配经 WithContentReader
// 注入；本包禁止 import 任何业务域包（plan §2.3 判据 2），库存数据零旁路（本包
// 不读写任何库存族表）。
//
// # 打印内容装配白名单（Orchestrator 裁决 2026-10-03，对 plan §2.2"只读自己域的表"的
// 裁定补充——机械评审对本清单放行，写入与库存族表守卫不变）
//
// 背景：§7.1 字段预设（与前端 PRINT_TEMPLATE_FIELD_PRESETS 冻结同源）含跨域展示
// 字段（INBOUND_ORDER.supplier_name、OUTBOUND_ORDER.customer_name、明细行
// product_name/bin_code 等），printing.md §5 要求预览真实数据渲染；纯本域表无法
// 装配。经 Orchestrator 裁决，各域 printing_content.go 接入文件允许**只读 SELECT
// （含 JOIN）**，对象限定三类：
//
//  1. printing 自有表：print_templates / print_tasks / print_task_rows；
//  2. 模板关联的 M2 单据表（§7.1 预设关联的单据主体与明细行）：purchase 的
//     inbound_orders/inbound_items/purchase_orders/receipts/receipt_items，sales 的
//     outbound_orders/outbound_items/pick_tasks/sales_orders/allocations，
//     stockops 的 count_orders/count_items——只读、按 id 集合装配；
//  3. masterdata/warehouse 的纯展示列（name/code 类）：suppliers.name、
//     customers.name、products.name/spec、skus.code、units.name、warehouses.name、
//     zones.name、shelves.code、bins.code——只读 JOIN，仅取展示列。
//
// 零容忍（越界即违约）：
//   - 任何写操作（INSERT/UPDATE/DELETE 零命中）；
//   - 库存族表（inventory/inventory_ledgers/inventory_locks/batches/serial_numbers）
//     的任何访问（M1 §4.2 库存族守卫不变）；
//   - 业务逻辑判断：装配是纯投影，不得有状态变更或行为分支副作用；
//   - import 其他业务域包（域包间禁止 import——plan §2.3 判据 2；表级只读访问用
//     SQL，不构成代码耦合）。
//
// 依赖面：标准库 + gin/gorm/redis + internal/{auth,response,docnum,database,
// middleware,asynqx,logger(zap)}；asynq 库仅经 asynqx.Queue 接口使用（判据 4）。
package printing
