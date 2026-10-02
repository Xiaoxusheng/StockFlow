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

## 下一步（按 frontend.md §29 顺序）

1. **F6 库存中心全量页面**：库存锁定、批次库存、序列号、库存调整、库存转移、库存盘点、库存追溯、库存分析（复用 T7 组件与 T10 模式）
2. **F7 入库/出库流程**：入库单列表+详情（Timeline）、出库单列表+详情、收货/质检/上架状态接入
3. **F8 任务中心**：我的工作台、我的任务
4. **后端阶段 3–8**（server/）：数据库 → 基础设施 → 认证 → 基础资料 → 仓库 → 库存核心（前端 API 层已按 api.md 对齐，后端就绪即通）
5. F9 盘点、F10 库位地图、F11 Excel、F12 打印、F13 设备、F14 Pad、F15–F16 Scan

## 关键上下文

- 后端不存在：所有数据页为统一错误态（预期行为）；DEV 旁路 `VITE_AUTH_BYPASS=1` 仅开发构建。
- 权限点模型已定义（types/permission.ts），当前无后端 → 权限集为空 → 菜单按"未配置即可见基础菜单"策略展示，后端就绪后切换为真实权限过滤。
