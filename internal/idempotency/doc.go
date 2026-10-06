// Package idempotency 通用幂等执行权仲裁与响应快照（效率层一期 §2.10 追加交付，
// 迁移 000024 idempotency_keys 表）。
//
// 语义（ask 冻结）：
//   - 首次请求占用执行权（INSERT ... ON CONFLICT DO NOTHING 命中唯一索引
//     uk_idempotency_keys(key,user_id,endpoint) 恰一方成功）→ 真实执行 →
//     落响应快照（status=COMPLETED + response_snapshot）；
//   - 重复提交（同 key+user+endpoint）回放首次响应快照（回放体改写当前
//     request_id，追踪正确性不牺牲）；
//   - 并发同键：未占用方读得 PROCESSING 行 → 409 IDEMPOTENCY_IN_PROGRESS，
//     数据库唯一索引为仲裁真相源，应用层不做二次判定；
//   - 同键换载荷（request_hash 不符）→ 409 IDEMPOTENCY_REQUEST_MISMATCH；
//   - 首次执行产生任何响应（成功或业务错误信封）都落快照并回放——「重复提交
//     返回首次结果」含失败结果；客户端修复数据后重试应生成新键（前端
//     useIdempotentMutation 每次提交意图生成新键，计划 §2.10）；
//   - 执行进程崩溃（panic）→ Release 释放占用，同键重试重新执行（行不滞留
//     PROCESSING 永久卡死；该路径的重复执行风险与无幂等防护的既有基线一致，
//     库存原语另受 inventory_ledgers.idempotency_key 唯一索引兜底，api.md §7）。
//
// 维度隔离：同键不同用户 / 不同端点（method+路由模式）互不干扰。
//
// 集成挂载（auth.AuthRequired 之后、业务 handler 之前；路由清单=计划 §2.10
// 高风险端点 + 本批新增，wiringNotes 逐条给片段）：
// 收货确认、入库确认、上架 execute、库存调整、调拨 approve/execute、拣货确认、
// 复核确认、打包、发货、盘点调整、批量操作（batch-claim×3 / 批量打印）、
// POST /api/imports/:id/retry-failed。
//
// 与既有幂等机制的关系（不重复造、不越权替换）：
//   - 库存原语行级幂等（inventory_ledgers.idempotency_key 唯一索引 + stockops
//     头键合成，b220dea 已交付）仍是库存流水防重的第一道防线；本包提供的是
//     HTTP 端点级「一次执行 + 首次响应回放」，两层正交；
//   - receipts/packing_records/shipments 的幂等键列（000006 部分唯一索引）
//     维持原状；挂载本中间件的端点无需改动业务本体（响应快照由中间件捕获）。
//
// 运维挂账：孤儿 PROCESSING 行（进程崩溃后 Release 也失败等极端场景）由
// created_at 索引支撑的后续清理作业回收（一期不建定时任务，docs 批注）。
package idempotency
