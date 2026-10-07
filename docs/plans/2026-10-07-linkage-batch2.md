# 计划：跨模块联动第二批（L4/L8/L10/L11/L13/L14）

日期：2026-10-07 ｜ 前批：docs/plans/2026-10-07-linkage-batch1.md（L1/L2/L3/L5/L9 已交付）

## 目标

Dashboard 卡片/行/图表与六个分析页的「看数 → 行动」下钻。原则沿用 frontend.md §33；
**逐图核对「数据字段 → 目标列表筛选白名单」，无真实参数的图不硬接（明确跳过清单见下）**。

## L4 Dashboard 九计数卡（后端 internal/reports/dashboard.go:637-645，计数口径 :166-187）

| 卡 | 计数口径 | 新 Link | 依据 |
|---|---|---|---|
| 待收货 | PO APPROVED/PARTIAL_RECEIVED | /purchases/receipts（不变） | 收货列表无 status 参数（handler.go:447 仅 inbound_no/receipt_no），且 RC 为已完成记录，硬筛会失真 |
| 待上架 | putaway_tasks PENDING/IN_PROGRESS/PAUSED | /inbound?status=AWAITING_PUTAWAY | inbound 白名单有 status |
| 待拣货 | outbound_orders ALLOCATED/PICKING | /picking?status=PENDING | picking 有 status（sales/handler.go:495）；PENDING=未领取拣货任务 |
| 待复核 | outbound_orders PICKED | /checking?status=PENDING | 工作台既有先例 |
| 待打包 | outbound_orders CHECKED | /packing（不变） | packing 无 status 参数（handler.go:804） |
| 待发货 | outbound_orders PACKED | /shipment?status=PENDING | shipment 有 status（handler.go:856）；ShipStatusPending |
| 待盘点 | count_orders COUNTING/PENDING_REVIEW | /counts（不变） | status 单值筛不覆盖双态，硬筛数字对不上 |
| 待审核单据 | 跨单据 approval | /tasks（不变） | 无对应筛参数 |
| 待处理异常 | RESOLVED/CLOSED 之外四态 | /exceptions（不变） | status 单值筛不覆盖四态（工作台同此先例） |

## L8/L10/L11（Dashboard 三列表行点击，canAccess 门控）

- L8 预警行 → `/inventory/alerts?level=<level>&keyword=<sku_code>`（AlertsPage 白名单 level/keyword）
- L10 异动行（business_no 非空）→ `/inventory/ledger?business_no=`（LedgerQuery.business_no；LedgerPage 同单据流水先例）
- L11 库位利用率行 → `/bins?warehouseId=<id>`（warehouse/handler.go:320 camelCase 实证）

## L13 Dashboard 图表（内核 SfChart 增 onPointClick，echarts chart.on('click')）

- 出入库趋势（SfBarChart，series 入库/出库，x=日期）→ `/inventory/ledger?change_type=INBOUND|OUTBOUND&created_from=<日>&created_to=<日>`（ledger 白名单含 change_type/created_from/created_to——handler.go:197-214）
- 仓库库存排行（SfHBarChart）→ `/inventory/stock?warehouse_id=<id>`——**需后端 warehouseStockDTO 补 warehouse_id**（dashboard.go:83/385，兼供 L11 与仓库分析页）
- 跳过：库存趋势（现存量快照，ledger/stock 无对应筛选）、库存状态环形（stock 列表无 status 参数）

## L14 六分析页（逐图核对后）

| 页/图 | 下钻 | 依据 |
|---|---|---|
| 库存分析 SKU TOP（HBar） | /inventory/stock?sku_id= | InventorySkuTopRow.sku_id |
| 库存分析 ABC/状态/趋势 | 跳过 | 无对应参数 |
| 仓库分析 库存容量排行 | /inventory/stock?warehouse_id= | 同 DTO 补字段 |
| 仓库分析 作业量双序列柱 | /inbound?warehouse_id= / /outbound?warehouse_id=（按 seriesName） | WarehouseWorkloadRow.warehouse_id；两列表有 warehouse_id |
| 仓库分析 库位利用率列表 | /bins?warehouseId= | 同 L11 |
| 入库分析 状态构成环形 | /inbound?status=<码>（name→码经 composition 数据反查） | inbound 白名单 status |
| 入库分析 供应商排行 | /purchases?supplier_id= | InboundSupplierRankRow.supplier_id（入库列表无 supplier 参数，落点取其来源 PO 列表） |
| 入库分析 趋势 | 跳过 | /inbound 无日期参数 |
| 出库分析 SKU 出库排行 | /inventory/ledger?sku_id=&change_type=OUTBOUND&created_from=&created_to=（同时间范围） | ledger 白名单；口径与卡脚「OUTBOUND 流水」一致 |
| 出库分析 趋势 | 跳过 | /outbound 无日期参数 |
| 采购分析 供应商排行 | /purchases?supplier_id= | purchaseSupplierRankRow.supplier_id |
| 采购分析 状态构成环形 | /purchases?status= | purchases 白名单 status |
| 采购分析 趋势 | 跳过 | /purchases 无日期参数 |
| 销售分析 趋势 | /sales?created_from=&created_to= | sales 白名单有 created_from/created_to（handler.go:118-125） |
| 销售分析 状态构成环形 | /sales?status= | sales 白名单 status |
| 销售分析 SKU 排行 | 跳过 | /sales 无 sku 参数；ledger OUTBOUND 与销售单金额口径不齐 |

## 修改文件

后端：internal/reports/dashboard.go（九卡 Link + warehouseStockDTO/查询补 warehouse_id）
图表内核：web/src/components/charts/SfChart.tsx（onPointClick，ref 持回调注册一次）+ 五包装组件透传
前端页面：api/dashboard.ts、DashboardLists.tsx、DashboardMovements.tsx、DashboardCharts.tsx、
inventory/AnalyticsPage.tsx、warehouse/WarehouseAnalyticsPage.tsx、inbound/InboundAnalyticsPage.tsx、
outbound/OutboundAnalyticsPage.tsx、purchase/PurchaseAnalyticsPage.tsx、sales/SalesAnalyticsPage.tsx
文档：frontend.md §33.4（图表下钻与跳过清单）、changelog、本计划

## 不做什么

L6/L7/L12/L15（需拍板/后端配合，用户未点名）；不给无参数的图造假筛选；不改任何列表后端筛选白名单。

## 验收

go build/vet/test + tsc 全绿；浏览器实测：九卡新链路（上架/拣货/复核/发货）、预警行、
异动行、库位行、出入库趋势日下钻、仓库排行、库存页 SKU TOP、仓库页作业量、采购/销售页
排行与状态环形；跳过图确认无回归。
