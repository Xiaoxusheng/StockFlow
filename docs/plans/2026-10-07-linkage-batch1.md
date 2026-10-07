# 计划：跨模块联动第一批（L1/L2/L3/L5/L9）

日期：2026-10-07 ｜ 来源：docs/plans 联动盘点（工作流报告 2026-10-06，用户挑选第一批）

## 目标

打通「分析/监控 → 行动」五条高频联动，全部纯前端实现（后端契约已实证就绪）：

| 编号 | 联动 | 形态 |
|---|---|---|
| L1 | 补货建议行「去补货」→ 采购新建预填 SKU+建议量+仓库 | 带参新建 |
| L2 | 补货建议勾选行批量「生成采购单」（同仓聚合多 items） | 带参新建 |
| L3 | 库存预警行「去补货」（仅低库存）+「查看库存」 | 带参新建 / 跳转预筛 |
| L5 | 采购单详情（已审核/部分收货/收齐）「新建入库单」带 source_no+仓库预填 | 带参新建 |
| L9 | 我的任务行「关联单号」→ 入库/出库列表按单号预筛 | 跳转预筛 |

## 前置疑点裁决（已实证）

- 补货页注释称「sku_id 现网恒 0（GORM naming）」——2026-10-07 连后端实测
  `GET /api/reports/replenishment-suggestions`：sku_id 全部有值（9406/9414/…）。
  注释过时（后端显式 column tag 已修复），本轮顺手更正注释；预填直接用 sku_id。

## URL 预填契约（frontend.md §33 固化，后续联动复用）

1. **采购新建**：`/purchases/new?warehouse_id=<id>&items=<sku_id>:<qty>[,<sku_id>:<qty>…]`
   - qty 可省略 → 行数量留空待人工填（前端禁止推算库存量）
   - 仅 create 模式消费；非法/空参数忽略、回退空白表单；预填后 message 告知行数
2. **入库新建**：`/inbound/new?source_type=PURCHASE&source_no=<单号>&warehouse_id=<id>`
   - source_type 值域校验（PURCHASE/OTHER）后预填
3. **任务行单号映射**：putaway→`/inbound?source_no=`（inbound:view）；
   picking/checking→`/outbound?outbound_no=`（outbound:view）——workbench.go:746/760/771
   实证 source_no 语义；两个列表页筛选参数已存在（relations.tsx 白名单注释）
4. 原则：URL 为唯一事实源（刷新/分享可还原）；预填 ≠ 提交，仍走人工确认；
   权限不足不渲染动作（canAccess，permission.md §5）

## 修改文件（全部 web/src，零后端改动）

1. `views/reports/ReportReplenishment.tsx`：+操作列（去补货，suggested_qty>0 才显示）；
   +rowSelection +批量按钮（extraActions，MyTasksPage 模式；跨仓聚合 → message 拦截）；
   更正 sku_id 过时注释；gating PURCHASE_CREATE_PERMISSION
2. `views/purchase/PurchaseOrderFormPage.tsx`：create 模式解析 query 预填（契约 1）
3. `views/inventory/AlertsPage.tsx`：+操作列（查看库存全级别 / 去补货仅 low_stock）；
   gating inventory:stock:view 与 PURCHASE_CREATE_PERMISSION
4. `views/purchase/PurchaseOrderActions.tsx`：+「新建入库单」
   （APPROVED/PARTIAL_RECEIVED/RECEIVED_ALL × INBOUND_CREATE_PERMISSION，进早退链）
5. `views/inbound/InboundFormPage.tsx`：create 模式解析 query 预填（契约 2）
6. `views/task/MyTasksPage.tsx`：source_no 列改单号链接（契约 3，无权限回退纯文本）

## 不做什么

- 不做 L4/L6/L7/L12/L13/L14/L15（后端字段/方案设计依赖，等下一批）
- 不新增/修改任何后端端点；不新增依赖
- 不做「预警行自动算建议补货量」（硬性规则 8：禁止前端算库存）
- 不引入 sessionStorage 中转（破坏 URL 唯一事实源，盘点结论明示）

## 测试与验收

- `npx tsc -b` 通过（唯一预存报错 TransferFormModal 属用户 WIP，不属本任务）
- 浏览器实测五条链路截图：补货行去补货预填 / 批量 2 行预填 / 预警行双动作 /
  采购详情→入库预填 / 任务行预筛跳转；含无权限/跨仓/空参数回退路径
- 文档：frontend.md §33 + changelog 条目；独立 commit（与背景色改动分开）
