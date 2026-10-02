# StockFlow AGENTS.md

> 本文件是 Agent 会话的项目记忆。任何开发任务开始前必须先读本文件与 [docs/README.md](docs/README.md)。

## 项目概述

**StockFlow（库流智能仓储管理系统）**：生产级 WMS，围绕完整业务闭环构建（基础资料 → 采购/销售 → 入库/收货/质检/上架 → 库存 → 调拨 → 盘点 → 出库/拣货/复核/打包/发货 → 退货 → 追溯 → 报表 → 审计）。

三端体系：**StockFlow Web**（PC 管理端）+ **StockFlow Pad**（平板作业端）+ **StockFlow Scan**（Android 工业 PDA，独立应用）。

## 技术栈（文档基线，不得擅自更换）

| 层 | 选型 |
|---|---|
| 后端 | Go + Gin + PostgreSQL(pgx) + GORM(库存关键路径原生 SQL) + Redis + asynq（未开始） |
| 前端 Web/Pad | React 18 + TypeScript(strict) + Vite + **Ant Design 6** + react-router + TanStack Query + zustand + axios + dayjs |
| 图表/条码/打印 | @ant-design/plots、JsBarcode + qrcode、react-to-print |
| 业务规则唯一依据 | docs/business-flow.md + docs/inventory-rules.md（修改业务代码前必读） |

## 目录约定

```text
docs/            项目文档（唯一真相来源，先改文档再改代码）
docs/plans/      开发计划
docs/tasks/      任务状态（current.md）
web/             StockFlow Web + Pad 前端（React）
scan/            StockFlow Scan（Android，未开始）
server/          Go 后端（未开始）
```

前端 `web/src` 内部结构遵循 [docs/frontend.md §26.4](docs/frontend.md)：api/ components/ layouts/ router/ stores/ hooks/ utils/ types/ styles/ views/。

## 常用命令（web/）

```bash
cd web
npm install
npm run dev        # Vite 开发服务器 http://localhost:5173，/api 代理到 http://localhost:8080
npm run build      # tsc -b && vite build
npm run preview    # 预览生产构建
```

后端未实现时页面显示统一的 Loading/Empty/Error 状态——这是预期行为，**禁止用假数据冒充业务**（requirements.md §10）。

开发模式（仅 DEV 构建生效）：设置环境变量 `VITE_AUTH_BYPASS=1` 时登录页提供"开发模式进入"入口，用于后端未就绪时查看页面骨架；生产构建不含该入口。

## 硬性规则

1. **先读后改**：任何任务先读 docs/ 对应领域文档 + 本文件，确认已有实现再动手。
2. **文档先行**：业务规则变更先改 docs/ 再改代码；每次模块交付更新 docs/changelog.md。
3. **设计 Token 唯一**：UI 样式统一走 `--sf-*` Design Token（web/src/styles/tokens.css）+ antd ConfigProvider 映射，禁止页面写死颜色/间距/圆角。
4. **组件复用**：列表页统一用 SfTable/SfToolbar/SfSearchForm，禁止页面自造表格（frontend.md §6/§23）。
5. **状态颜色统一**：业务状态一律经 SfStatusTag（frontend.md §24）。
6. **API 层统一**：所有请求走 src/api/（axios 单例 + 统一信封 `{code,message,data,request_id}` + 分页 `page/pageSize/total/items`，api.md §2）。
7. **权限**：前端权限仅是体验优化，后端必须校验（permission.md §5）。
8. **禁止假功能**：按钮只弹 Toast、前端算库存、写死 Dashboard 数据等一律禁止（requirements.md §10）。
9. **Git**：Conventional Commits 中文实践（git-commit-standard），一个完整任务一个 commit；禁止 `git reset --hard` / `git clean -fd` 等破坏性操作。
10. **任务状态**：进行中的长任务状态记录在 docs/tasks/current.md，中断后从那里恢复，不重新设计。

## 当前状态

- [x] 需求/架构/领域文档（docs/，基线 v1.x）
- [x] 前端基础平台（web/：Token 主题、API 层、认证、PC Layout、统一表格组件、Dashboard 骨架、库存中心示范页）
- [ ] Go 后端（server/，开发计划阶段 3–8）
- [ ] 前端全量页面（frontend.md F6–F13）、Pad 端（F14）、Scan 端（F15–F16）
