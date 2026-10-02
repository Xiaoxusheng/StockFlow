# 前端基础平台开发计划（F1–F5 + 库存中心示范）

> 日期：2026-10-02 ｜ 范围：StockFlow Web（PC 管理端）基础平台 ｜ 依据：docs/frontend.md（§1–§9、§23–§26、§29 F1–F5）、docs/api.md、docs/permission.md、docs/architecture.md §11.3

## 1. 背景

- 仓库当前**只有文档、没有任何代码**（无后端、无前端）。
- 用户指令："你来做前端"。
- docs/frontend.md §1.1 明确允许：后端未完成时先建**类型、Service、状态管理与页面骨架**，但禁止假数据冒充业务。

## 2. 目标

建立 PC Web 前端工程基础平台，使后续 F6–F13 的每个业务页面都能按统一模式快速接入真实 API：

1. 工程脚手架：Vite + React 18 + TypeScript strict + Ant Design 6（最新稳定版）+ react-router + TanStack Query + zustand + axios + dayjs。
2. Design Token（`--sf-*`）+ Light/Dark 主题 + 全局格式化（时间/金额/数量，frontend.md §1.5）。
3. 统一 API 层：响应信封 `{code,message,data,request_id}`、分页 `page/pageSize/total/items`、错误码映射、Token 注入、401/403 处理（api.md §2）。
4. 认证与权限骨架：登录页、auth store、路由守卫、权限点模型（permission.md §5）。
5. PC Layout：Sidebar 完整菜单树（frontend.md §4.2）+ Header（全局搜索位/通知/仓库/用户/主题切换）+ 面包屑，紧凑企业级信息密度（§3、§4）。
6. 统一组件族：SfPageHeader、SfTable、SfToolbar、SfSearchForm、SfStatusTag、SfEmpty/SfError、SfDetailSection（§6、§9、§23、§24）。
7. 页面：登录、403/404/500 错误页、Dashboard（四层结构 §5）、实时库存/库存流水/库存预警三个示范列表页（完整接 Service，含筛选/分页/Loading/Empty/Error）、未接入模块统一占位页。
8. 后端未就绪的诚实呈现：所有数据页调用真实 API，失败显示统一错误态（含"重试"），不做假数据。

## 3. 明确不做什么

- 不做 Pad 端（F14）、Scan 端（F15–F16）、扫码业务组件族（F16）。
- 不做 Excel 导入向导、打印预览、库位地图、设备管理页面（F10–F13）。
- 不做 Go 后端、数据库、Mock 后端服务。
- 不做假数据演示模式（登录开发旁路只建立本地会话壳，不伪造业务数据）。
- 不引入 docs 基线之外的 UI 库 / 状态库 / 请求库。

## 4. Task 拆分（每个 Task 一个 commit）

| # | Task | 交付 | 验证 |
|---|---|---|---|
| T1 | 仓库初始化 | .gitignore、AGENTS.md、本计划、任务状态文件 | 文档齐全 |
| T2 | 前端脚手架 | web/：package.json、vite.config、tsconfig(strict)、ESLint 基础、index.html、入口 | npm run build 通过 |
| T3 | Token 与主题 | styles/tokens.css、ThemeProvider（Light/Dark）、format 工具 | Dark 下无纯黑纯白 |
| T4 | API 层与类型 | api/client.ts（信封/分页/错误映射/Token 注入/401 跳转）、api 模块（auth/dashboard/inventory 等）、types/ | tsc 通过 |
| T5 | 认证 | stores/auth、登录页、路由守卫、DEV 旁路（VITE_AUTH_BYPASS=1） | 登录页可交互 |
| T6 | PC Layout | layouts/：Sidebar（菜单树+折叠+权限位）、Header（搜索/通知/仓库/用户/主题）、面包屑 | 路由切换正常 |
| T7 | 统一组件 | components/common、components/table（SfTable/SfToolbar/SfSearchForm） | tsc 通过 |
| T8 | 路由与错误页 | router/（全部菜单路由+守卫）、403/404/500、ModulePlaceholder | 全菜单可达 |
| T9 | Dashboard | 四层结构（指标条/趋势图/任务+预警/仓库分析），接真实 API，错误/空态完整 | 四层布局正确 |
| T10 | 库存中心示范页 | 实时库存、库存流水、库存预警三个列表页（SfTable + 筛选 + 分页 + 状态） | 三页接 Service |
| T11 | 验证与提交 | typecheck/build、浏览器截图检查（Light/Dark、桌面宽度）、changelog、任务状态更新 | 全部通过 |

## 5. 技术决策

- **React 18.3**（文档基线"React 18+"，避开与 @ant-design/plots 的生态兼容风险）。
- **AntD 6 最新稳定版**，ConfigProvider 全局 zhCN + Design Token 映射，Dark 用 `theme.darkAlgorithm`。
- **API base**：开发环境 Vite proxy `/api → http://localhost:8080`（可用 `VITE_API_PROXY` 覆盖）；文档未约定端口，后端落地时在 deployment.md 固化。
- **状态分域**（frontend.md §18.1）：zustand 仅放 auth/theme/ui 分片；服务端状态一律 TanStack Query。
- **表格列状态保留**（§26.3）：SfTable 内置列设置/密度持久化到 localStorage。
- **DEV 旁路边界**：仅 `import.meta.env.DEV && VITE_AUTH_BYPASS=1` 时显示，本地伪造会话仅含"未授权"空权限集，生产构建无此代码路径。

## 6. 风险

- AntD 6 较新，个别 API 与 5.x 不同 → 以官方文档为准，遇到问题按 antd 6 文档修正。
- 后端未就绪 → 页面长期处于错误态属预期；验收看组件状态是否完整，而非数据。
- 范围风险 → 严格按 §3 不做清单执行，其余记入任务状态文件。

## 7. 验收标准

- `npm run build`（含 tsc）零错误。
- 登录页 →（DEV 旁路）→ Layout → 全部菜单可达；403/404/500 可达。
- Dashboard 四层结构完整，Loading/Error/Empty 状态可演示。
- 库存中心三个示范页：筛选/分页/表格密度/列设置/错误重试可用。
- Light/Dark 两套主题切换正常，Token 统一，无 AI 风格装饰。
