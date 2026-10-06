// Package batchresult 批量操作结果统一契约（效率层一期计划 §2.7 冻结形状）：
//
//	{total, success_count, failed_count, skipped_count,
//	 results: [{id, status: 'success'|'failed'|'skipped', reason}]}
//
// 语义冻结（计划 §2.7，api.md 同源披露）：
//   - total = len(results) = success_count + failed_count + skipped_count；
//   - status 三态字面量 success/failed/skipped；
//   - skipped = 幂等命中 / 已处目标态（如批量领取时任务已被本人领取）；
//   - failed.reason 使用既有模块错误码字符串（如 PRINT_SKU_DISABLED）；
//     success/skipped 的 reason 恒空；
//   - results 保持入参 ids 顺序（逐条对账）；
//   - 批量不整体回滚：逐条独立成败（计划 §2.7）。
//
// 落位说明（平台契约包，同 internal/datax contract.go 口径）：本包零依赖域包，
// 域包 → 本包 import 方向合法（计划 §2.3 判据 2）。既有批量端点接入进度：
// printing CreateTask 已收敛（集成收口 2026-10-06，JSON 契约零变化）；sales/purchase
// batch-claim ×3 仍持同形状私有定义（ID 为 JSON 数字，收敛即契约变化）——接入
// 方式与步骤见 batchresult.go 尾注，由拥有对应文件的波次/集成执行。
package batchresult
