# 计划：跨模块联动第三批（L6/L7/L12/L15，收官批）

日期：2026-10-07 ｜ 前批：batch1（L1/L2/L3/L5/L9）、batch2（L4/L8/L10/L11/L13/L14），本批后 15 项全收口。

## 目标与方案

### L6 采购详情「收货记录」chip（1:N 修正，需后端）
- 现状：relations.tsx purchase_order 实体已有 receipts 项，但按 ctx.inbound_no（1:1 假设）驱动——PO 详情 ctx 只有 po_no，chip 永不渲染。
- 方案：收货列表 `GET /api/purchases/receipts` 增 `po_no` 参数（SQL JOIN inbound_orders ON inbound_no，WHERE source_no=?——一张 PO 的全部入库单的收货记录，天然覆盖 1:1/1:N）；relations receipts 项改按 ctx.po_no 驱动。
- 改动：internal/purchase/repository.go（filter+SQL）、handler.go（读参）、web/src/api/purchase.ts（ReceiptQuery.po_no）、relations.tsx、docs/api.md 收货列表节。

### L7 销售详情四作业 chip 点亮（需后端）
- 现状：relations sales_order 实体已注册 拣货/复核/打包/发货 四 chip（ctx.outbound_no 驱动），销售详情 ctx 只有 so_no/warehouse_id——审核 1:1 派生的出库单号未回传，四 chip 永不渲染。
- 方案：GET /api/sales/{id} 响应增 `outbound_no`（service 增 OutboundNoBySo：复用 repo.ListOutboundOrdersBySO，取最早一张；未审核单为空串）；详情页 ctx 透传；api/sales.ts SalesOrderDetail 补类型。
- 改动：internal/sales/service.go、handler.go、web/src/api/sales.ts、SalesOrderDetailPage.tsx、docs/api.md 销售详情节。

### L15 报表行下钻（纯前端）
- 行字段已核：InventorySummaryRow / TurnoverRow / StagnantRow 均含 warehouse_id+sku_id → 行点击 `/inventory/stock?sku_id=&warehouse_id=`（inventory:stock:view 门控）。
- 接入：ReportInventorySummary、ReportInventoryTurnover、ReportStagnantStock 三页 SfTable onRow。
- 明确不接：ReportFlowStats（入库/出库统计按日，目标列表无日期参数——§33.4 诚实原则）。

### L12 Pad 任务卡单号复制（纯前端，跨端衔接最小实现）
- PadTaskCard 增关联单号行 + 复制按钮（navigator.clipboard，复制态 1.5s 反馈；stopPropagation 防误触整卡）；PC 端用既有全局搜索单号直达承接（searchTargets 前缀映射已存在，不自建新通道）。

## 不做什么
- 不改任何列表筛选白名单语义；不做 Pad 深链跳 PC（复制+全局搜索即闭环，通道已存在）；ReportFlowStats 不硬接。

## 验收
go 门禁 + tsc 全绿；浏览器实测：PO 详情收货记录 chip（1:N PO）、销售详情四 chip（已审核单）、三报表行下钻；api.md 两节同步。
