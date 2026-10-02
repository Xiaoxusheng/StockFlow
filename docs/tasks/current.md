# StockFlow 当前任务状态

> 唯一状态文件 ｜ 中断后先读本文件 + docs/plans/，继续原任务，禁止重新设计

## 当前任务

**前端基础平台**（docs/plans/2026-10-02-frontend-foundation.md，F1–F5 + 库存中心示范）

| Task | 内容 | 状态 |
|---|---|---|
| T1 | 仓库初始化（git/.gitignore/AGENTS.md/计划/状态文件） | ✅ 362c1bb |
| T2 | 前端脚手架 web/ | ✅ |
| T3 | Design Token + Light/Dark 主题 + 格式化 | ✅ |
| T4 | API 层与类型（信封/分页/错误映射/Token 注入） | ✅ |
| T5 | 认证（登录页/auth store/守卫/DEV 旁路） | ✅ |
| T6 | PC Layout（Sidebar/Header/面包屑） | ✅ |
| T7 | 统一组件（SfTable/SfToolbar/SfSearchForm/SfPageHeader/SfStatusTag） | ✅ |
| T8 | 路由表 + 403/404/500 + 模块占位页 | ✅ |
| T9 | Dashboard 四层结构 | ✅ |
| T10 | 库存中心示范页（实时库存/库存流水/库存预警） | ✅ |
| T11 | 验证（build/lint/浏览器实测 Light+Dark+1440+768） | ✅ |

## 2026-10-02 并行交付：前端页面第一批（六组并行开发 + 集成收口）

| 模块 | 内容 | 状态 |
|---|---|---|
| 基础资料 | 商品/SKU/分类/单位/供应商/客户 六页 CRUD（api/masterdata.ts + views/masterdata/，对齐 backend-m1-plan §5.4） | ✅ 已接入路由 |
| 仓库中心 | 仓库/库区/货架/库位 四页 CRUD + 库位地图网格页（api/warehouse.ts + views/warehouse/，对齐迁移 000004） | ✅ 已接入路由 |
| 系统管理 | 用户/角色/权限/部门 四页（api/user.ts + api/rbac.ts），并修正 auth.ts 契约：profile→GET /api/auth/me、改密→PUT /api/auth/password，新增 refresh/sessions 封装 | ✅ 已接入路由 |
| 库存中心扩展 | 库存锁定、库存调整 两列表页（api/inventory.ts 增 locks/adjustments，值域对齐迁移 000005 + inventory-rules §4） | ✅ 已接入路由 |
| 仓储作业骨架 | 入库/出库列表骨架（api/inbound.ts、api/outbound.ts，对齐 business-flow §3/§7） | ✅ 已接入路由 |
| 采购订单骨架 | 采购订单列表骨架（api/purchase.ts，对齐 business-flow §2） | ✅ 已接入路由 |

集成收口（router/index.tsx）：注册 20 条 lazy 路由（/products /skus /categories /units /suppliers /customers /warehouses /zones /shelves /bins /warehouse-map /system/users /system/roles /system/permissions /system/departments /inventory/locks /inventory/adjustments /inbound /outbound /purchases），IMPLEMENTED_PATHS 同步增补；菜单路径与各页路由全部对齐，config/menu.tsx 零改动。后端各域仍为 stub，页面呈统一错误态（预期行为）；前端先行契约与遗留对齐清单见 changelog 同日集成记录。验证：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0、`npx eslint src/router/index.tsx` EXIT=0（全局构建留门禁阶段）。

## 下一步（按 frontend.md §29 顺序）

1. **F6 库存中心全量页面（余量）**：批次库存、序列号、库存转移、库存盘点、库存追溯、库存分析（库存锁定、库存调整已交付）
2. **F7 入库/出库流程（余量）**：入库单/出库单详情（Timeline）、收货/质检/上架状态接入；采购单详情同理（列表骨架已交付）
3. **F8 任务中心**：我的工作台、我的任务
4. **后端阶段 3–8**（仓库根 Go module，T0 脚手架已交付：`go build ./...` 在仓库根执行）：数据库迁移 → 认证 → 基础资料 → 仓库 → 库存核心（前端 API 层已按 api.md 对齐，后端就绪即通；实现时需核对前端先行契约，清单见 changelog 2026-10-02 集成记录）
5. F9 盘点、F11 Excel、F12 打印、F13 设备、F14 Pad、F15–F16 Scan（库位地图已交付）

## 关键上下文

- 后端不存在：所有数据页为统一错误态（预期行为）；DEV 旁路 `VITE_AUTH_BYPASS=1` 仅开发构建。
- 权限点模型已定义（types/permission.ts），当前无后端 → 权限集为空 → 菜单按"未配置即可见基础菜单"策略展示，后端就绪后切换为真实权限过滤。
