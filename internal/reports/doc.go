// Package reports 报表与智能能力（backend-m3-plan §9，requirements §2.4/§2.5、
// inventory-rules §11）。
//
// 交付面：
//   - GET /api/reports                          报表目录（代码内冻结注册表，plan §9.1）
//   - GET /api/reports/inventory-summary        库存汇总（按仓/SKU 分组 + 金额）
//   - GET /api/reports/inbound-stats            入库统计（按日：单数/数量/金额）
//   - GET /api/reports/outbound-stats           出库统计（同形）
//   - GET /api/reports/inventory-turnover       库存周转（出库量/平均库存，按仓/SKU）
//   - GET /api/reports/stagnant-stock           积压识别（30/60/90 天未动，inventory-rules §11）
//   - GET /api/reports/replenishment-suggestions 智能补货建议（plan §9.3 公式 + 计算依据字段）
//   - GET /api/inventory/summary|alerts         Dashboard 聚合（reports 实现、inventory 前缀挂载）
//
// 现场聚合决策（plan §9.2）：带索引的只读聚合 SQL + 时间范围上限（366 天）+ 强制分页，
// 不做汇总表（汇总表是第二真相来源；architecture §7 允许"汇总表或异步任务"，本包取现场聚合）。
//
// 只读红线（plan §2.3 判据 3 / inventory-rules §11）：本包零写 SQL（INSERT/UPDATE/DELETE
// 零命中）；智能建议不自动生成任何单据、不改任何库存与状态，建议必附计算依据字段。
//
// 数据访问口径（本次交付的偏离记录，plan §2.3 判据 7 的替代实现）：plan 原设计为域包侧
// reports_reader.go（inventory/sales/purchase/stockops）实现消费方窄接口——该接入文件不在
// 本工程师独占清单（ask：禁止改其他包），故本包以只读 SELECT 聚合直接读取库存族/基础资料/
// 单据流水表（全部参数化、命中既有索引、仓库数据权限过滤），库存族零写通路不变。
package reports
