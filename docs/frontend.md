# StockFlow 前端规范（Web / Pad / Scan）

> 版本：v1.1 ｜ 本文档是前端领域的唯一依据 ｜ 关联文档：[devices](devices.md)（设备与 Scan 应用）、[scanner](scanner.md)（扫码领域）、[permission](permission.md)、[api](api.md)
>
> **与 devices.md 的分工**：devices.md 定义 Scan 应用定位、设备生命周期、离线容错与厂商适配；本文档定义三端的 UI/UX、组件体系、页面结构与工程规范。

---

## 1. 前端总体要求

### 1.1 目标定位

前端覆盖三端：

```text
StockFlow Web   PC 企业管理端
StockFlow Pad   平板仓储作业端
StockFlow Scan  工业扫码端（Android，独立应用，见 devices.md §3）
```

目标不是普通 Admin CRUD，而是：**真正能够用于企业仓库管理、仓库现场作业、工业 PDA 扫码操作的生产级 WMS 前端。**

重点解决：

```text
排版、留白、层级、边界、信息密度
表格、卡片、详情页、状态、操作流程
扫码体验、Pad 触控体验、PDA 小屏体验
```

后端 API 已存在的直接对接真实 API；未完成的可先建类型、Service、状态管理与页面骨架，但**禁止使用假数据冒充已完成业务**。

### 1.2 技术栈基线

- **React 18+ + Ant Design 6**（https://ant.design/index-cn）——使用**最新稳定版**（当前 6.6.5，随官方 release 更新，锁定 minor 版本）；全站唯一组件体系，禁止混用其他 UI 库。
- TypeScript 强制（严格类型）；Vite 构建；react-router + TanStack Query + zustand；axios 统一请求层。
- 组件优先使用 antd 原生能力（Table/Form/Modal/Drawer/Descriptions/Timeline/Badge/Tag 等），通过 ConfigProvider 全局配置 `zhCN` 中文语境与统一 Design Token 主题。
- 时间统一 dayjs；图表 @ant-design/plots；条码/二维码 JsBarcode + qrcode；打印 react-to-print。

### 1.3 三端共享与隔离

三端**共用**：API、类型、权限模型、业务状态、Design Token、基础组件、业务规则展示。

**三端 UI 不允许简单缩放复制**：PC Sidebar/Table/Dashboard 直接缩小到 Pad/Scan 是禁止项（见 §20–21）。

### 1.4 设计风格

现代企业级 SaaS/WMS 风格，关键词：专业、紧凑、克制、高信息密度、现代企业级、长期使用不疲劳。

**禁止**：

```text
AI 风格 Dashboard、大量渐变、玻璃拟态、大面积彩色卡片
巨大留白、过度圆角、大量装饰、页面堆满组件
巨大的 Dashboard 数字、每个模块一个颜色
```

### 1.5 全局格式规范

- 时间：`YYYY-MM-DD HH:mm`（分钟精度，全站统一）；金额：`¥ 1,234.56`；数量：千分位（库存数量最多 4 位小数不补零）。
- 封装统一格式化函数（web/src/utils/format.ts），禁止各页面自己拼格式。

### 1.6 开工前检查（强制）

开始修改前端代码前必须完整检查：

```text
前端目录、路由、Layout、Design Token、主题、组件
表格封装、表单封装、Drawer/Modal、状态管理、API Service
权限、已有页面、响应式、已有移动端代码
```

并判断：哪些可复用、哪些需统一、哪些需重构、哪些只是视觉问题、哪些存在交互问题、哪些需要重新实现。不要无理由更换现有技术栈。

---

## 2. 全局 Design Token 与主题

### 2.1 Token 清单

统一 Design Token（业务层 `--sf-*` 变量，唯一定义于 `web/src/styles/tokens.css`；通过 ConfigProvider theme 与 antd Token 映射（`App.tsx`，映射改动必须与 tokens.css 同 commit 同步）；禁止页面绕过 Token 直接写死样式值）：

```text
--sf-bg                页面背景
--sf-surface           卡片/面板表面
--sf-surface-elevated  浮层表面
--sf-surface-hover     hover 表面档（表格行 / 侧边栏菜单 hover）
--sf-border-width      边框宽度（1px，主题无关，Light/Dark 块同声明）
--sf-border            边框
--sf-border-subtle     弱边框
--sf-text              主文字
--sf-text-secondary    次要文字
--sf-text-muted        弱文字（即文字第三档 tertiary）
--sf-text-disabled     禁用文字
--sf-primary           主色
--sf-primary-hover / --sf-primary-active    主色交互态
--sf-success / --sf-warning / --sf-danger / --sf-info   状态色
--sf-font-family       字体栈（Inter + 系统中文字体回退，不引外部字体文件）
--sf-font-size-caption / -secondary / -body / -section-title / -page-title / -kpi   字号层级（12/13/14/16/20/26px）
--sf-radius-sm / -md / -lg   圆角（6 / 8 / 10px；Card=8、Modal=10）
--sf-shadow-sm / -md         阴影（Light 克制轻阴影；Dark 减淡，以边框+分层替代）
--sf-space-1 ~ --sf-space-7  间距（4/8/12/16/20/24/32px）
--sf-motion-fast / -normal / -slow / -ease   动效（120/180/220ms + cubic-bezier(0.2, 0, 0, 1)）
--sf-motion-spin / -feedback   动效档位外时长（560/480ms；表格动效体系 §31 专用，主题无关，不进 dark 块，TS 镜像见 web/src/styles/motion.ts）
--sf-chart-primary / -secondary / -success / -warning / -danger / -info / -muted / -axis / -grid / -tooltip-bg / -tooltip-border / -tooltip-text / -legend-text   图表配色（SfChart 主题唯一取值来源，Light/Dark 各一套，共 13 枚同名同集合）
--sf-header-height / --sf-sider-width / --sf-sider-collapsed-width   布局尺寸
```

统一：圆角、边框、阴影、字体、间距、状态色、按钮高度、表格密度、输入框高度。**禁止每个页面自己写一套。**

### 2.2 主题

支持 Light / Dark，PC、Pad、Scan 全部共用主题 Token。

Dark 模式要求：

```text
不要纯黑、不要纯白文字、不要发光过度、不要大片深灰空白
卡片之间需要层级、边界清楚、表格可读
```

---

## 3. 全局信息密度

采用**紧凑型企业软件布局**，默认基准：

```text
页面 padding：20~24px
Card padding：16~20px
表格行高：44~52px
输入框高度：36~40px
按钮高度：36~40px
```

（具体按 antd 6 组件参数微调。）禁止 100px 上下大留白、超大 Card、超大标题、超大数字。

---

## 4. PC Layout 与 Sidebar

### 4.1 Layout

```text
┌───────────────────────────────────────────────┐
│ Header                                        │
├──────────────┬────────────────────────────────┤
│ Sidebar      │ Breadcrumb / Page Header       │
│              ├────────────────────────────────┤
│              │ Toolbar / Filters              │
│              ├────────────────────────────────┤
│              │ Main Content                   │
└──────────────┴────────────────────────────────┘
```

要求：Sidebar 稳定；Header 不过高；页面主体紧凑；内容最大宽度合理（页面不要无限拉宽）；表格页面优先利用横向空间；详情页面保留呼吸感。

### 4.2 Sidebar 菜单（按业务领域划分）

```text
Dashboard
我的工作台

仓储中心
├── 我的任务 ├── 入库管理 ├── 出库管理 ├── 拣货管理
├── 复核管理 ├── 打包管理 ├── 发货管理 └── 异常中心

库存中心
├── 实时库存 ├── 库存流水 ├── 库存锁定 ├── 批次库存
├── 序列号  ├── 库存调整 ├── 库存转移 ├── 库存盘点
├── 库存预警 ├── 库存追溯 └── 库存分析

仓库中心
├── 仓库 ├── 库区 ├── 货架 ├── 库位 ├── 库位地图 └── 调拨

采购中心
├── 采购订单 ├── 收货 └── 采购退货

销售中心
├── 销售订单 ├── 出库 └── 销售退货

质量中心
├── 质检 ├── 不合格品 └── 质量追溯

盘点中心

基础资料
├── 商品 ├── SKU ├── 商品二维码 ├── 分类 ├── 单位 ├── 供应商 └── 客户

报表中心

数据中心
├── Excel 导入 ├── Excel 导出 ├── 打印中心 └── 文件中心

设备中心
├── 扫码设备 ├── PDA 设备 ├── Pad 设备 └── 打印设备

系统管理
├── 用户 ├── 角色 ├── 权限 ├── 部门 ├── 通知 ├── 日志
├── 定时任务 ├── 系统配置 └── 系统监控
```

菜单根据权限动态显示（permission.md §5）。

---

## 5. Dashboard

不要做四个巨大 KPI 卡片。采用四层结构：

```text
第一层  今日业务指标（今日入库 12,380 / 今日出库 9,320 / 待处理任务 38 / 库存预警 17）
第二层  入库 / 出库 / 库存趋势
第三层  任务 + 预警（库存预警、临期商品、积压库存）
第四层  仓库 + 库存分析（仓库库存、库位利用率、操作效率）
```

数据来源与角色视图要求见 requirements.md §2.1。

---

## 6. 表格系统

### 6.1 统一 DataTable

所有列表页面必须统一使用高质量 DataTable（SfTable，§23），支持：

```text
分页、排序、筛选、列显示控制、列拖拽
固定列、横向滚动、批量选择、批量操作
密度（紧凑/默认/宽松）、全屏、导出、打印、刷新
```

**不要让每个页面自行造表格。**

`SfTable` 两种形态（variant）：

- `page`（默认）：独立列表页，含工具栏/三档密度（默认紧凑）/列显示隐藏/全屏/刷新/统一分页（`共 x 条` + 条/页 + 快跳）。
- `nested`：详情页/抽屉内嵌表——无工具栏无分页（除非显式传 pagination），仍统一密度、空态、错误态与首载骨架（loading 且无数据时以骨架条占位保持表头结构；dataSource 传入时首载与刷新不再叠加 Spin 蒙层，统一走 data-loading 压暗/淡入，见 §31）。

**分页条规范（2026-10-06，全站统一）**：SfTable 内嵌分页（`共 x 条` + 页码 ‹ 1 › + 条/页 + 快跳）为**紧凑靠右单行**布局——antd 默认「total 贴左、页码/条每页贴右」靠 `margin-inline-end: auto` 分离两端，宽表格下中间会空出上千像素（实测 1170px），观感松散。global.css 以 `.sf-table .ant-pagination { justify-content: flex-end }` 收敛为**整组紧凑靠右**：「共 x 条」与页码之间固定 `--sf-space-3` 间距、「共 x 条」用 `--sf-text-secondary` 降权、「条/页」选择器圆角统一 `--sf-radius-sm`（与页码按钮同风格）。作用于 `.sf-table` 作用域（全站表格统一，含 nested 显式分页），**禁止各页面自行覆盖分页样式**。

**受控列 API（作业效率提升层一期，2026-10-06）**：SfTable 新增 `hiddenColumns?: string[]` + `onHiddenColumnsChange?: (hidden: string[]) => void`——受控优先/非受控回退：不传新 props 的既有消费页零行为变化（storageKey 照旧持久化）；保存视图应用=页面将视图 hidden 列集经受控 prop 生效（不写 localStorage，避免覆盖用户手动列偏好）；用户手动改列=onChange 回传 + storageKey 照旧持久化。

**保存视图（SfViewBar）范围披露（一期）**：视图保存 filters_json（SfSearchForm cleanValues 产物）+ columns_json（hidden 列键数组）+ page_size；`sort_json` 仅为服务端排序通道预留（一期恒空、不采集——SfTable 排序为 antd 非受控，服务端排序超一期范围）；列拖拽排序维持既有裁定不做。视图应用一律经 usePagedList 公开 API（urlSync 页展开写入 URL query、旧形态页置 params，SfViewBar 以 `mode: 'url' | 'state'` 声明形态），禁止绕过 hook 直改 URL 或页面 state——URL 参数为最终事实源，`view=<id>` 仅作当前视图名标记，用户改动任一筛选即清除。

### 6.2 表格视觉

```text
边界清晰、文字对齐、数字右对齐、状态统一、操作列固定
```

库存、数量、金额、重量、体积等数字类字段使用统一数字格式（§1.5），文字不要全部靠左堆在一起。默认使用紧凑/中等密度，符合仓库高信息量场景。

- 主题映射（App.tsx Table 组件 token，值源 tokens.css `--sf-table-*`）：表头文字 `--sf-table-header-text`（比正文弱一档）、hover 行 `--sf-surface-hover`、选中行=主题色低透明（alpha 0.04~0.08，禁止整行深蓝）。
- 统一单元格（web/src/components/table/cells.tsx）：`NumberCell`（数字+tabular-nums，配合列 align:'right'）、`CodeCell`（编码 ellipsis+Tooltip+hover 显复制，Toast「已复制 xx」）、`ProductCell`（主名 500 字重+弱化编码双行）、`DateCell`（统一时间次要色）。
- 操作列形态：高频动作 ≤2 个 link 按钮 + 「更多 ▾」Dropdown；删除永远在更多内且 danger；整列可见按钮（含更多）≤3 个；fixed:'right'（有横向滚动时）+ nowrap。参照 SkuListPage buildRowMenu 先例。
- 操作列色彩层级（双色）：首个动作（编辑/详情等 link 按钮）保持链接主色（= 品牌主色，App.tsx `colorLink` 显式对齐，antd 缺省取 `colorInfo` 青蓝已废弃）；其后次级动作（停用/更多/记录等非 danger link）一律中性次要色（global.css 作用域规则，hover/按压加深为正文色）——「更多」下拉触发统一 `aria-label="更多操作"`（含 icon-only）无论位置恒为中性。主次分明，danger 红色语义不变。
- 表格默认全边框：SfTable `bordered` 默认开启（外框+内格线，颜色走 antd `colorBorderSecondary`，App.tsx 已映射 `--sf-border` 同值，Dark 自适应）；页面传 `bordered={false}` 可回退无边框形态。
- 横向滚动阈值自适应：全部可见列均显式声明 width 时，SfTable 自动按「列宽总和+10」推导 `scroll.x`——表格经 min-width:100% 撑满容器，仅内容真实放不下才出横向滚动条（能不出现就不出现）；页面声明的 `scrollX` 仅在存在未声明 width 的弹性列时作宽度预留，列设置隐藏列后阈值随可见列收缩。列宽声明应贴近内容自然宽度（反例：仓库列表曾虚高至 1480 致常规视口必出滚动条，已收紧至 1320）。
- 筛选状态提示：SfSearchForm 提交后显示「已筛选 N 项 · 清除全部」（弱化行，重置归零）。

### 6.3 列表工具栏

统一模板：

```text
┌──────────────────────────────────────────────┐
│ 商品 / SKU 搜索       [筛选] [重置]          │
│ [新建] [导入] [导出] [打印]      [列设置]   │
└──────────────────────────────────────────────┘
```

按钮不要五颜六色，主操作只有一个主要视觉强调。核心页面工具栏统一入口：`[新建] [导入] [导出] [打印] [扫码] [更多]`——不是所有页面都必须全部出现，根据业务显示。

---

## 7. 详情页

禁止把所有信息堆成一个巨大 Card。结构：

```text
Header（单号 + 状态 + 关键操作）
→ 状态 + 关键指标 → 基础信息 → 明细
→ 业务流程（Timeline）→ 关联单据
→ 库存影响 → 附件 → 操作记录
```

详情页顶部示例（出库单）：

```text
OUT-20261002-0001    待拣货

客户：XXX   仓库：A仓   商品：28 SKU   数量：320   金额：¥ xxx

[拣货] [打印] [导出] [更多]
```

状态必须明显，但不能用巨大 Banner。

**Timeline**：入库、出库、调拨、盘点、质检等页面使用 Timeline（创建 → 审核 → 收货 → 质检 → 上架 → 完成），每一步显示时间、操作人、状态、备注。

**Drawer / Modal 原则**：简单编辑用 Drawer；复杂业务用独立页面（商品编辑 → Drawer；采购订单创建 → 完整页面；盘点 → 专门工作流页面）。

---

## 8. 表单规范

复杂业务表单：分区、字段分组、步骤、实时校验。不要一个 Form 塞 50 个字段。

例如采购单：

```text
基础信息（供应商、仓库）→ 商品明细 → 付款/备注 → 附件
```

---

## 9. 状态与反馈

### 9.1 异步状态体系

所有页面统一处理：Loading、Skeleton、Empty、Error、Success、Disabled、Pending。

### 9.2 空状态

Empty 必须根据上下文说明，不要只显示"暂无数据"：

```text
当前筛选条件下没有库存
当前没有待处理任务
暂无异常
```

### 9.3 操作完成反馈

重要操作完成后给出结构化结果（如入库成功：单号/SKU 数/数量/库存 +320），支持查看详情、打印、继续操作（见 requirements.md 场景示例）。

### 9.4 错误页

必须建立统一风格的错误页：403、404、500、Network Error、Offline。

### 9.5 加载状态（作业端特殊要求）

扫码业务**不能用巨大 Loading 覆盖整个屏幕**，使用按钮 Loading、当前任务局部 Loading、状态条、轻量 Skeleton。员工在现场需要知道"正在提交"，而不是看一片灰。

---

## 10. 库存中心 UI

### 10.1 模块结构

```text
实时库存（SKU库存 / 仓库库存 / 库位库存 / 库存状态）
库存流水 ｜ 库存锁定 ｜ 批次库存 ｜ 序列号
库存调整 ｜ 库存转移 ｜ 库存盘点
库存预警（低库存 / 超储 / 临期 / 过期 / 积压）
库存追溯 ｜ 库存分析（库存金额 / 周转率 / 周转天数 / ABC分析 / 库存趋势）
```

### 10.2 实时库存页面

```text
库存统计（SKU、库存总量、可用、锁定、冻结、临期、异常）
→ 筛选栏
→ 库存表格（SKU/商品/仓库/库区/库位/批次/总库存/可用/锁定/冻结/效期/状态）
```

点击 SKU 进入库存详情。

### 10.3 库存详情

```text
SKU001  iPhone XX 256G
总库存 500 ｜ 可用 420 ｜ 锁定 50 ｜ 冻结 20 ｜ 待检 10
```

Tabs：库存分布、批次库存、序列号、库存流水、库存锁定、追溯、盘点。

### 10.4 库存分布（层级视图）

```text
A仓
 ├── A-01-03-05  100
 ├── A-01-03-06  150
B仓
 └── B-02-01-03  250
```

支持点击下钻。

### 10.5 盘点前端

**盘点任务列表**：盘点单号、仓库、范围、类型、负责人、创建/开始/完成时间、状态（草稿/待执行/盘点中/待复核/待审核/已完成/已取消）。

**盘点详情**：盘点范围、系统库存、实盘库存、差异、完成率；明细（SKU/库位/批次/系统数量/实盘数量/差异/状态）；支持扫码盘点、手工录入、导入、导出、打印。

**盘点扫码页面（Pad/Scan）**：

```text
盘点任务 CK-001
当前库位：A-01-03-05
系统数量：100    实盘：98    差异：-2
[ 扫描商品 ]
最近扫描：SKU001 ✓  SKU001 ✓  SKU002 ✓
```

支持连续扫码；差异必须进入原因/备注/照片/提交流程（devices.md §10.3）。

盘点页面**不能做成普通 CRUD 表格**。

---

## 11. 库位地图

仓库中心：仓库 → 库区 → 库位地图。库位状态：空闲、部分占用、满载、锁定、冻结、异常。点击库位打开库位详情（内容见 requirements.md §2.6）。

---

## 12. Excel 前端

数据中心 → 导入 / 导出。

**导入页面**按步骤流程呈现：

```text
1 下载模板 → 2 上传文件 → 3 数据校验 → 4 预览 → 5 确认导入 → 6 导入结果
```

错误定位展示（第 12 行：SKU 不存在）+ 下载错误 Excel。

**导出页面**：当前页 / 选中 / 全部 / 筛选结果；导出任务展示进度（导出中 35% → 完成下载）。

业务规则见 excel.md。

---

## 13. 打印前端

打印中心功能：打印模板、打印任务、打印历史、打印预览。

**模板页面**：模板名称、业务类型（SKU 标签/库位标签/箱码标签/托盘标签/入库单/出库单/拣货单/盘点单/发货单）、纸张、状态、修改时间。

**打印预览**：必须有独立 Preview，支持缩放、上一页/下一页、打印、下载 PDF。**禁止**点击打印直接打印当前网页。

**打印组件清单（SFQR 闭环新增，2026-10-05）**：

```text
utils/qrPayload.ts           SFQR 全站唯一前端构造点（buildSfqrSku / tryBuildSfqrSku；
                             另导出 parseSfqrPayload / isSfqrPayload 纯格式辅助——预览核对
                             展示段含义与前缀探测，非业务解析。实现逐字对照 docs/qr-code.md
                             黄金向量表；前端不做业务解析，扫码识别唯一入口 =
                             POST /api/scanner/resolve）
SfQrPreviewDrawer            二维码详情抽屉（SKU 列表/二维码中心共用「详情快捷入口」）
SfQrPrintModal               标签打印配置弹窗（单打/批打共用）
```

**QR 内容口径**：模板 SKU_LABEL 且 qrcode_enabled 时标签 QR = SFQR 载荷（`SFQR|1|SKU|<sku_code>`，由 values.sku_code 构造）；旧任务快照无 sku_code 值时维持主条码原文（扫码走条码匹配器，行为不变）。协议格式、黄金向量、纸张布局唯一依据见 [qr-code.md](qr-code.md)；打印规则见 printing.md。

### 13.1 二维码中心（/qr-codes，F12 扩展，基础资料组菜单）

- 页面骨架：SfPageHeader + SfSearchForm（keyword / enabled）+ SfTable；数据源复用 SKU 列表接口（列表已批量装配条码），数据缺失走统一 Loading/Empty/Error，**不造假数据**。
- 列定义：SKU 编码、商品名、主条码、状态（SfStatusTag）、二维码预览（QrCodeView 48px，值 = SFQR 载荷）、操作（详情抽屉 + 打印标签）。
- 行选择批量打印：rowSelection 经 SfTable 透传 + 批量栏（已选 N · 不可打印 K · 批量打印）。
- 详情抽屉（SfQrPreviewDrawer）：QrCodeView 预览（约 180px）+ 载荷明文 + 复制 + SKU 编码/商品名/主条码/状态；「打印标签」按钮经 canAccess('printing:task:create') fail-closed，触发 SfQrPrintModal。
- 支持 URL `?code=` 直达自动开抽屉（承接扫码识别后跳转）；Pad 触控目标 ≥44px。

### 13.2 标签打印配置弹窗与历史重打（F12 扩展）

- SfQrPrintModal：模板下拉（SKU_LABEL + 启用，空态如实引导去打印中心建模板）；不可打印清单 Alert 逐条「{code}：商品已停用」+「仅打印可用」降级按钮；份数 1~100；总张数 = 可用数 × 份数加粗明示；可打印 >500 禁用（后端上限）；提交 loading 防重复；成功跳转打印预览页。data_ids 恒传 SKU 数字 ID 十进制文本（qr-code.md §8 通道纪律）。
- 打印历史「重打」（SKU_LABEL 行、有权限时）：重打 = 新任务不碰历史；重打前校验任务行快照——任一行缺 data_id 即 fail-closed 中止并如实提示「该任务创建于身份快照能力之前，无法自动重打，请到商品二维码中心按 SKU 重选打印」，不发创建请求；全部行有 data_id 方可创建新任务（all-or-nothing，禁止部分行静默重打）。
- 预览页 A4/A5 网格分片渲染（每片一张纸、分页不截断标签），热敏纸一码一页；头部明示总张数（qr-code.md §7.3）。

业务规则见 printing.md。

---

## 14. 设备管理前端

### 14.1 设备中心

设备类型：PC、Pad、PDA、Scanner、Printer。列表字段：设备名称、设备类型、品牌、型号、仓库、绑定用户、在线状态、最后在线、App 版本、最后扫码。

### 14.2 设备详情

基础信息、在线状态、软件版本、扫码信息、使用记录、异常记录、操作记录。

### 14.3 设备二维码激活页面

PC：新建设备 → 生成激活二维码。Pad/Scan：扫描二维码 → 获取服务器地址/设备编号/仓库配置。

前端负责：二维码展示、二维码扫描 UI、激活状态、成功/失败反馈。

业务规则见 devices.md §6–7。

---

## 15. 工作台、搜索、通知与附件

### 15.1 我的工作台

```text
PC：         我的待办、我的审批、我的任务、我的异常
Pad / Scan： 我的仓库任务、我的进行中、我的完成记录、我的异常
```

**PC 工作台『我现在该做什么』改版（作业效率提升层一期，2026-10-06）**：进页面即回答"该干什么"——

```text
待我处理（/api/tasks?status=in_progress）  超时  异常（/exceptions）  今日已完成    ← 四计数（后端 summary 增量字段，additive）
优先处理（GET /api/workbench/priorities 四组 count+TopN：超时收货/库位异常/临期库存/待复核订单——真实 SQL 排序，组头直达列表页真实筛选）
最近操作（本人 operation_logs 尾 N 条，SfTimeline/SfTable 渲染）
快捷入口（扫码作业/收货/上架/拣货/复核/盘点/库存查询七项，MENU_TREE 权限码同源 + canAccess 过滤）
```

**落地增量（效率层一期工作台改版波次，2026-10-06）**：「优先处理」由计划中的 `/api/tasks/next` 复用升级为专用只读端点 `GET /api/workbench/priorities`（四组口径、组头直达与 limit 约束见 api.md §9 同日节）；四计数中「超时」「今日已完成」无直达链接（任务列表无超时/完成日期筛选参数，不做伪装筛选）；快捷入口落点：扫码作业→`/pad/home`、收货→`/purchases/receipts`、上架→`/tasks?task_type=putaway`（无 PC 独立页面，真实筛选直达）、拣货→`/picking`、复核→`/checking`、盘点→`/counts`、库存查询→`/inventory/stock`。

仅 PC 改版；Pad 首页一期维持不动（PadHomePage 不在改版范围）。

### 15.2 全局搜索

PC Header 提供 `搜索 SKU / 单据 / 库位 / SN / 箱码`：输入 SKU001 显示商品、库存、相关订单、相关单据。支持快捷搜索、高级筛选、保存筛选条件（落地为列表页保存视图，SfViewBar，见 §6.1）。

**落地（作业效率提升层一期，2026-10-06）**：Header 菜名 AutoComplete 升级为 `GlobalSearchModal` 命令面板（Ctrl/Cmd+K 呼出，组件清单见 §23）：

- 数据源 `GET /api/search`（契约见 api.md §9 效率层节）：按 type 分组返回（SKU/商品/条码/批次/SN/库位/仓库/客户/供应商/单据/物流），状态展示复用 SfStatusTag。
- 权限先行：端点只挂认证，组内按用户权限集逐 type 过滤——无对应 list 权限码的 type 不查询不出组（超管直通语义与 RequirePermission 同构）；`warehouse_id` 为收窄过滤器，越界仓库按该 type 无结果处理（不 403，防范围探测）。前端跳转映射（`config/searchTargets.ts`）同样经 canAccess 校验，无权限项置灰。
- 交互：防抖 300ms；↑↓/Enter/Esc/Tab 键盘导航，点击直达详情；跳转路由由前端映射（后端不编码前端路由）。
- 原 Header 菜名搜索降级为面板『页面』分组（候选源仍走 MENU_TREE + canAccess）。
- 禁止第二套：全局搜索是全站唯一跨域搜索入口，业务页面不得自建搜索浮层。

### 15.3 通知

Header 通知入口（未读数量实时更新），内容：审批、库存预警、任务、异常、系统通知。

**通知条目视觉规范（2026-10-06 定稿）**：通知抽屉（`NotificationDrawer`）采用**列表式**条目
（antd List 默认细分隔线；**无卡片、无底色、无左侧色条、无彩色图标**——避免"AI 界面"式的彩色装饰）。
层级完全由**排版**承担：① 标题（14px；未读 600 `--sf-text` / 已读 400 `--sf-text-secondary`，
**独占一行完整展示不截断**——单号是定位通知的关键信息）；② 正文（13px、行高 1.6，未读
`--sf-text-secondary` / 已读 `--sf-text-muted`）；③ 时间（12px、`--sf-text-muted`、**末行右对齐**
作元信息锚点）。未读仅以 **6px 主色圆点**指示（已读留同宽透明占位，保证所有标题左缘对齐）；
类型识别由标题文案自身承担（「库存异常待处理」「待审批：」等），不额外加图标。条目上下内边距 12px。
**约束**：通知条目不得引入左侧色条 / 淡彩底 / 彩色图标——该组合为典型 AI 界面特征，用户已明确否决。

### 15.4 文件附件

支持上传、预览、下载、删除；图片支持缩略图、预览、全屏。

---

## 16. 批量操作与危险操作

### 16.1 批量操作

列表支持：批量审核、批量打印、批量导出、批量作废、批量分派。点击后显示"已选择 28 条"；危险操作必须明确数量、明确影响、二次确认，并展示失败列表（requirements.md §2.8）。

批量结果统一经 `BatchResultDrawer` 展示（作业效率提升层一期，2026-10-06）：计数条（total/success/failed/skipped）+ 逐条列表 + SfStatusTag 三态 + 【仅重试失败】按钮（重试=以失败 ids 重新发起同一批量端点，成功项绝不重跑）；契约见 api.md §9。**禁止各页面自写结果弹窗。**

### 16.2 危险操作

统一二次确认：库存调整、报损、作废、强制出库、盘点差异、批量删除。必须显示影响数量、影响库存、影响单据。

### 16.3 删除 / 作废 UI

已产生业务的对象（商品、SKU、供应商、客户、仓库、库位）不要显示简单"删除"，应根据后端能力展示：停用、归档、作废（database.md §5）。

---

## 17. 权限 UI

前端必须支持：页面权限、按钮权限、仓库权限、数据权限。例如没有库存调整权限就看不到"调整库存"按钮。**前端权限仅是体验优化，后端 API 仍然必须校验**（permission.md §5）。

---

## 18. 状态管理、缓存与实时同步

### 18.1 状态分域

状态分为：用户状态、设备状态、仓库状态、任务状态、库存查询状态、扫码状态、通知状态。**不要所有东西塞进一个巨大 Store**（zustand 分片 + TanStack Query 管服务端状态）。

### 18.2 缓存策略

适合缓存：当前仓库、用户配置、权限、字典、Scanner 配置、设备配置。

库存等实时数据必须按业务刷新，不能出现"Pad 显示库存 100、实际后端已经 50"且长时间不刷新。

### 18.3 实时同步

有 WebSocket/SSE 能力时用于：任务更新、库存更新、通知、设备状态；没有时用轮询，但不要为实时数据疯狂请求。

---

## 19. 响应式与多设备

### 19.1 断点覆盖

至少适配五类：Desktop、Laptop、Tablet Landscape、Tablet Portrait、PDA。不能只做 PC/Mobile 两个断点。

重点优化尺寸：

```text
PC 1920×1080、PC 1440×900
Pad 横屏、Pad 竖屏
工业 PDA 5~7 英寸
```

不要为某一个尺寸写死像素。

### 19.2 PWA

技术栈允许时支持：PWA / Web App、缓存基础资源、启动画面、离线提示、安装到桌面。

**硬性边界**：不能因为 PWA 就假装拥有原生扫码能力——工业 PDA 的原生扫码能力最终由 StockFlow Scan 负责（devices.md §3）。

---

## 20. Pad 端 UI

### 20.1 定位

Pad 是**现场管理 + 中等强度作业终端**：任务管理、收货、质检、上架、库存、调拨、盘点、异常、拍照。

### 20.2 独立设计要求

```text
更大点击区域、更大字体、更少弹窗、更少表格
卡片列表、Bottom Action Bar、扫码优先、拍照优先
横屏优先、竖屏可用
```

### 20.3 横屏布局

```text
┌───────────────────────────────────────────┐
│ Header                                    │
├──────────────┬──────────────┬──────────────┤
│ 当前任务      │ 商品信息      │ 操作区       │
│ A-01-03-05   │ SKU001       │ [扫码]       │
│ 8 项待处理    │ 数量 20      │ [确认] [异常]│
└──────────────┴──────────────┴──────────────┘
```

### 20.4 竖屏布局

顶部当前任务 → 中部商品信息/库位/数量 → 底部固定 [扫描] [异常] [确认]。

### 20.5 首页

不要做 PC Dashboard，采用：任务、快速作业、预警、最近操作（如待收货 12 / 待上架 18 / 待拣货 25 / 待盘点 6）。

### 20.6 拍照

质检、收货异常、破损、盘点差异、物流异常场景直接支持：拍照、预览、删除、重新拍、上传；图片显示缩略图。

### 20.7 底部操作栏

Pad/Scan 高频业务固定底部：`[返回] [扫码] [异常] [暂停] [完成]`。不要挡住系统手势区域。

### 20.8 横竖屏切换

监听 orientation，根据方向切换 Landscape UI / Portrait UI，不要简单把同一套布局压缩。

### 20.9 触摸体验

按钮 ≥ 合理触摸尺寸、列表可点击范围足够、间距合理、滑动区域明确；避免小到无法点击的图标按钮。

---

## 21. Scan 端 UI

> Scan 应用定位、菜单、离线容错见 devices.md §3、§8；本节定义 UI 细节。

### 21.1 独立性

Scan 需要独立：Navigation、Task UI、Scan UI、Inventory UI、Error UI、Login UI。可以共享：Token、Icon、Typography、业务类型、API 类型。**不能依赖 PC Layout。**

### 21.2 首页与导航

首页见 devices.md §3.2。底部导航只保留现场真正需要的：首页、任务、扫码、库存、异常、我的。**不要塞**采购、供应商管理、角色管理、复杂报表、系统配置。

### 21.3 任务页面

按紧急程度排序：待处理、进行中、异常、已完成。卡片显示：任务编号、类型、仓库、数量、优先级、创建时间、负责人。点击进入任务。

### 21.4 业务作业页面要点

| 页面 | 界面围绕 | 操作序列 |
|---|---|---|
| 收货 | 当前采购单、当前 SKU、要求/已收/待收数量 | 扫采购单 → 扫商品 → 自动累计 → 批次 → 效期 → 确认 |
| 上架 | 应该商品、应该库位、要求数量、已上架 | 扫商品 → 扫库位 → 系统校验 → 完成 |
| 拣货（最重要页面） | 当前库位、商品、SKU、要求/已拣/剩余 | 实体 Scan 键 → 扫描 → 自动累计 → 成功反馈 → 下一项 |
| 复核 | 出库单、待复核数量、最近扫描 | 极简连续扫描，错误立即阻止 |
| 盘点 | 当前库位、系统/实盘/差异 | 连续扫码 → 差异进入原因/备注/照片/提交 |
| 移库 | 源库位、SKU、数量、目标库位 | 扫源库位 → 扫商品 → 扫目标库位 → 确认 |
| 库存查询 | 扫 SKU/库位/批次/SN/箱码/托盘码 | 扫码后直接显示对应对象 |

### 21.5 通用扫码中心

Scan 底部中央 [扫码] 进入通用扫码中心，可扫描任意 StockFlow 条码，解析后按类型直达：

```text
SKU → 库存        库位 → 库位库存     SN → 序列号
箱码 → 包裹       托盘码 → 托盘       单据 → 单据详情    任务 → 任务详情
```

### 21.6 扫码体验

```text
自动聚焦、高亮当前输入、扫码成功立即反馈
扫码成功自动清空、自动准备下一次扫码
```

连续扫码节奏：扫码 → Beep → 震动 → 数量 +1 → 等待下一次。**不能**扫码 → 鼠标点击 → 确认 → 鼠标点击 → 下一步。

进入任何高频扫码页面自动准备扫码，操作结束自动继续等待扫码，禁止员工不停手动点输入框（scanner.md §6.3）。

### 21.7 成功 / 错误视觉

成功（短暂反馈，立即回到扫码状态，不能弹巨大 Modal 阻断连续作业）：

```text
✓ SKU001   +1   17 / 20
```

错误必须高可见，显示具体原因：

```text
┌──────────────────────┐
│          ✕           │
│    商品不匹配        │
│ 当前任务：SKU001     │
│ 扫描结果：SKU999     │
│ [重新扫描]           │
└──────────────────────┘
```

禁止只显示"操作失败"（scanner.md §6.1）。

### 21.8 状态显示

Scan 顶部固定：扫码设备状态（设备正常/设备异常）+ 网络状态（● 在线 / ⚠ 网络不稳定 / × 离线 + 最后同步时间）（devices.md §8）。

### 21.9 登录与用户切换

登录页非常简单：设备（SF-SCAN-001）、仓库（A仓）、用户名、密码、[登录]。设备已注册时服务器地址/设备编号/仓库不需要再次输入。

支持锁定、切换用户、退出（共享设备场景，devices.md §6.4–6.5）。

---

## 22. 扫码前端接入（Web/Pad）

- PC 扫码页面支持键盘、USB HID 扫码枪、Bluetooth HID 扫码枪：自动聚焦、快速识别、Enter 结束、连续扫码。
- 业务组件不要自己重复处理扫码逻辑，统一走 ScannerService（scanner.md §3）；组件族（ScanInput/ScanField/ScanStatus/ScanResult/ScanHistory）与能力清单见 scanner.md §3.2–3.3。
- 键盘操作：Enter/Esc/Tab/方向键/快捷键（scanner.md 与 frontend 作业页适用）。
- 库存与扫码联动：

```text
扫描 SKU → 库存详情        扫描库位 → 库位库存
扫描 SN → 序列号追溯       扫描箱码 → 包裹详情
扫描盘点任务 → 盘点页面
```

- 扫码输入三模式（作业效率提升层一期，2026-10-06；扫码作业优化同日升级为可切换偏好）：`components/scanner/ScanInput.tsx` + `useScanBuffer`——`normal`（常规，扫后即解析）/ `fast`（快速，短间隔连扫自动推进）/ `continuous`（连续，同对象累计计数）。三模式为**用户偏好**：`stores/scan.ts` `useScanStore`（localStorage `sf.scan.mode`）为唯一模式源，`ScanInput` 未传 `mode` prop 时内置 Segmented 切换器读写 store（跨页面一致），显式传 `mode` 则页面受控并隐藏切换器。**智能下一步只对查询/定位类低风险动作自动执行**（prop `autoAdvance: 'none' | 'safe'`），确认收货/扣减类必须显式按键，禁止自动提交。
- 智能下一步（扫码作业优化，2026-10-06）：`hooks/useSmartScanNext.ts`——统一调 `POST /api/scanner/resolve`（前端不做业务解析）识别，按作业上下文（receive/quality/putaway/count/stockmove/transfer/inventory/global）映射为 locate-task / locate-item / locate-bin / navigate 指令，页面执行「继续」步；单号前缀→作业页冻结路由表收口在 `DOC_KIND_ROUTES`（收货任务→收货确认 / 上架→上架 / 拣货→PC /picking / SKU·库位→库存页定位）。resolve 失败不伪造结果（返回 resolve-failed 携带真实错误，页面可走手输兜底）。devices 2s 去重窗口与既有 ScanDirectCard 不动。
- 扫→判→继续收口（Pad）：`PadActionBar` 扫码弹窗统一托管 `ScanInput`——页面传 `onScanSubmit` 时按页面上下文处理，未传时走 global 智能路由（识别命中→进入对应作业页并收窗）；收货/质检/移库弹窗扫码后**保持打开**可连扫，把「扫描→关弹窗→点确定→回列表→找下一条→再扫」压缩为「扫→判→继续」。原 `PadScanStub` 占位原语全员下线删除（pad.css `.sf-pad-scan-stub` 样式随之移除）；盘点（盘盈亏）、移库等高风险/库存变动动作仍一律显式按键提交，扫码仅承担定位。
- 一期临时口径：ScanInput 键盘缓冲解析限定在组件自身受控输入元素焦点内承接（ScanDirectCard HID 先例），不做页面级 window keydown 监听（与 scanner.md §3.1 冻结约束不冲突，输入框带 `[data-sf-scan-input]` 标记并入 §32 输入态抑制）；完整 ScannerManager→Event 总线架构归 F15/F16 立项，不在本期。

---

## 23. 组件封装清单

业务页面优先组合以下统一组件，禁止重复造：

```text
SfTable            统一表格（§6）
SfRowActions       行操作区包裹（§31 #2：hover 渐显/触摸端恒显；「操作」列由 SfTable 自动注入，无需包裹）
SfSearchForm       查询表单
SfToolbar          列表工具栏
SfPageHeader       页面头
SfStatusTag        状态标签（§24 状态色统一）
SfDetailHeader     详情页头
SfDetailSection    详情分区
SfTimeline         业务时间线
SfEmpty / SfLoading / SfConfirm   空态/加载/确认
SfBatchAction      批量操作
SfExportButton / SfPrintButton   导出/打印
SfScanInput / SfScanStatus       扫码输入/状态（scanner.md §3.2）
SfDeviceStatus     设备状态
SfTaskCard         任务卡片（Pad/Scan）
SfInventorySummary / SfInventoryTable   库存汇总/表格
```

**作业效率提升层一期新增（2026-10-06）**——统一组合使用，禁止第二套实现（不建第二套搜索/偏好/视图/批量/快捷键体系，样式一律 `--sf-*` Token + 既有 Sf 组件族）：

```text
GlobalSearchModal  全局搜索命令面板（Ctrl/Cmd+K 呼出；Header 菜名搜索并入『页面』分组，§15.2）
SfViewBar          保存视图栏（应用/保存/重命名/删除/设为默认/恢复默认；置 SfToolbar extra 区，§6.1；
                   mode:'url'|'state' 双形态，应用一律经 usePagedList 公开 API）
ShortcutProvider   全站唯一 keydown 监听与快捷键分发（含 shortcuts.ts 集中注册表，§32；PC only）
ShortcutHelpDrawer 快捷键帮助面板（? 呼出，注册表驱动零手写文档，§32.4）
RecentVisitsDropdown  最近访问下拉（读 user_preferences.recent_visits，Header 通知按钮旁）
SfPreferenceDrawer 偏好设置抽屉（默认仓库/清除最近数据；PC 用户菜单『偏好设置』入口；
                   读写走 usePreferences——读+防抖写，localStorage 先行渲染防闪变）
BatchResultDrawer  批量结果统一抽屉（计数条+逐条三态+仅重试失败；打印/批量领取共用，§16.1）
SfCompleteNextButton  「完成并处理下一条」（配合 useNextTask 调 /api/tasks/next，claim 已被领取冲突视为可进入；
                   收货/上架作业页先行，PC 出库四作业页随其接线轮）
SfRelationNav      详情页关联业务导航（SfDetailSection 内 chip 组，config/relations.tsx 注册表驱动，
                   canAccess fail-closed 过滤，点击优先开 Drawer/带 query 预填跳转；
                   接入 SKU/流水/入库/采购单/出库/销售单/盘点 七详情页）
SfAutoRefreshSelect  自动刷新选择器（关/10/30/60 秒，document.hidden 暂停，连续失败退避停轮；
                   配合 useAutoRefresh，任务型列表页工具栏）
ScanInput / useScanBuffer  扫码输入三模式 normal/fast/continuous（可切换偏好 useScanStore）
                           + 智能下一步 useSmartScanNext（§22）
useIdempotentMutation / utils/idempotency.ts  幂等提交包装（自动附 Idempotency-Key 头；
                   isPending 期间按钮 loading+disabled；服务端消费面按 api.md §7 逐端点口径）
```

---

## 24. 视觉状态规范

状态颜色全局统一：正常、成功、处理中、待处理、警告、危险、禁用、异常。**不要每个页面自己决定颜色**（通过 SfStatusTag + Token 状态色）。

控件高度统一走 ConfigProvider `token.controlHeight: 32` 下限（App.tsx 映射，任务书 §24 Form 控件 32~36px）；状态色取值仅维护于 tokens.css 的 `--sf-*`，SfStatusTag 与侧边栏选中态经 `color-mix(var(--sf-*))` 派生、改 Token 值自动跟随，组件无需回调。

---

## 25. 动画规范

只使用必要动画：页面进入、卡片出现、Drawer、Modal、Toast、数字变化、状态变化、Skeleton。

禁止：大量漂浮、大幅缩放、无意义渐变、持续动画。扫码成功可短暂视觉反馈，但不要影响下一次扫码。

---

## 26. 前端工程规范

### 26.1 强制要求

```text
TypeScript 严格类型、组件复用、统一 API Service
统一错误处理、统一权限、统一表格、统一表单、统一状态
```

禁止：

```text
any 泛滥、重复组件、重复样式
页面里直接写大量请求逻辑
页面里直接写硬件厂商逻辑（devices.md §4.2）
```

### 26.2 生产级细节清单

需要处理：网络慢、接口超时、重复点击、重复扫码、页面返回、任务过期、权限失效、Token 过期、设备失联、扫码枪失联、空数据、大数据、长文本、超长 SKU、超长商品名称、多语言字符。

### 26.3 页面状态保留

从详情页返回列表：尽量保留筛选、分页、排序、列配置，不要全部丢失（如库存列表 → SKU001 详情 → 流水 → 订单 → 返回后恢复原列表状态）。

### 26.4 目录结构建议（Web）

```text
src/
├── api/              统一 API Service
├── assets/
├── components/
│   ├── common/  table/  form/  scanner/
│   ├── search/  shortcut/  batch/  inventory/  task/  device/
├── layouts/          PC Layout / Pad Layout
├── router/           路由 + 权限守卫
├── stores/           zustand 分片（§18.1）
├── hooks/            通用 hooks
├── utils/  types/  styles/（Design Token）
└── views/
    ├── dashboard/  warehouse/  inventory/  inbound/
    ├── outbound/  picking/  checking/  packing/
    ├── shipment/  count/  quality/  purchase/
    ├── sales/  reports/  imports/  exports/
    ├── printing/  devices/  system/
```

根据现有项目调整，不要强制照抄。

### 26.5 Scan 项目结构

```text
scan/
├── auth/  home/  tasks/  scanner/  inventory/
├── exception/  settings/  device/  network/  shared/
```

`scanner/` 独立封装（devices.md §4）。

---

## 27. 页面交付清单（最终必须完成）

**PC**：Dashboard、库存、库存流水、库存锁定、批次、序列号、库存调整、库存转移、库存盘点、库存预警、库存追溯、库存分析、入库、出库、拣货、复核、打包、发货、调拨、质检、异常、仓库、库位地图、采购、销售、基础资料、报表、Excel、打印、设备、系统管理。

**Pad**：首页、任务、收货、质检、上架、库存、盘点、移库、调拨、异常。

**Scan**：首页、我的任务、扫码中心、收货、上架、拣货、复核、盘点、移库、调拨、库存查询、异常、我的。

---

## 28. 验收标准

**PC**：桌面端操作完整、表格高信息密度、Excel、打印、权限、Dashboard、库存、报表。

**Pad**：横屏、竖屏、触摸、扫码、拍照、任务、盘点、异常。

**Scan**（真实设备环境：工业 Android PDA + 内置扫描头 + 实体 Scan 键 + Wi-Fi + 触摸屏）：登录 → 查看任务 → 按实体 Scan 键 → 扫库位 → 扫 SKU → 完成任务。

---

## 29. 前端实施阶段（F1–F20）

前端开发按以下子阶段推进（与 development-plan.md 主阶段映射：F1–F13 对应阶段 4–17 的前端部分，F14–F16 对应阶段 16，F17–F20 为横向专项）：

```text
F1  检查当前前端            F2  统一 Design Token
F3  重构 Layout             F4  统一 Table / Form / Toolbar
F5  Dashboard               F6  库存中心
F7  入库/出库流程           F8  任务中心
F9  盘点                    F10 仓库地图
F11 Excel UI               F12 打印 UI
F13 设备管理               F14 Pad UI
F15 StockFlow Scan UI      F16 扫码业务流程
F17 响应式                 F18 Loading / Empty / Error
F19 动画与微交互           F20 完整回归测试
```

开工前先输出：当前前端现状、存在问题、可复用组件、需要重构的部分、页面改造顺序、三端架构建议（development-plan.md §2）。

每完成一组页面必须检查：类型检查、构建、路由、响应式、交互、错误处理、权限——确保不是只把页面"做出来"，而是真的能够继续接入真实业务。

---

## 30. 最终视觉目标与三端体验

```text
企业级 SaaS + 专业 WMS + 紧凑高信息密度 + 优秀表格
+ 清晰业务状态 + 高效仓库作业 + 工业扫码终端体验
```

不是：普通 Admin 模板 + 几个卡片 + 一些渐变 + 扫码按钮。

三端核心体验：

```text
PC    看得全、管得住、分析清楚
Pad   看得清、点得准、现场处理方便
Scan  拿起来就能扫、扫完马上继续下一步
```

三端视觉统一，但交互完全针对设备场景优化。

### 30.1 最终禁止清单

```text
为了 UI 重写所有后端          为了响应式简单缩放 PC
用假数据模拟扫码              扫码成功后只 Toast
扫码页面没有任务上下文        盘点页面只是普通 CRUD 表格
Pad 只是 PC 页面缩放          Scan 只是 H5 页面
Excel 按钮下载假文件          打印按钮直接 window.print
设备页面只有静态卡片          所有状态颜色随便定义
所有页面使用巨大 Card         大量留白、大量渐变、复杂动画
```

---

## 31. 表格动效体系（Global Table Motion System，2026-10-05）

> 8 类核心动画全部内建于 SfTable/SfToolbar/SfSearchForm/SfEmpty + global.css 作用域类（「表格动效体系」节）。
> 全站 66 个 SfTable/SfInventoryTable 消费文件**零改动自动获得 #1/2/3/5/6/8**；
> #7 行反馈、删除行动效、批量工具栏为组件能力，页面按需接入（API 见 31.4）——页面接入（阶段B）
> 已于 2026-10-05 完成（四批并行，逐批清单与接入形态见 docs/tasks/current.md 同日节）。

### 31.1 令牌（CSS + TS 双轨）

- tokens.css 动效块：既有 `--sf-motion-fast/normal/slow = 120/180/220ms`（slow 由 240ms 收敛至规格 ≈220ms）+ `--sf-motion-ease` 不变；
  新增 `--sf-motion-spin: 560ms`（刷新图标 360° 自旋周期，规格 500~700ms）、
  `--sf-motion-feedback: 480ms`（行反馈淡色底时长，规格 400~700ms）——均主题无关，不进 dark 块。
- TS 镜像 `web/src/styles/motion.ts`（SF_MOTION_MS）：仅 JS 定时器消费——refreshMinHold=450（刷新图标最短自旋
  保持）、feedbackCleanup=540（反馈类清除）、rowRemoveCollapse=260（删除行过滤 DOM）、rowRemoveFallback=2500
  （删除行兜底自清）；CSS 一律 `var(--sf-motion-*)`，改时长与 tokens.css 同 commit 同步。

### 31.2 类名体系（global.css）

| 类名 | 挂载点 | 用途 |
|---|---|---|
| `.sf-table` | SfTable 根 div | 全部子样式作用域锚点 |
| `.sf-table-data-loading` | `.sf-table` 修饰符，loading 为真即挂 | #1 数据返回淡入 + #6 Body 压暗（首载骨架与刷新共用） |
| `.sf-table-row` + `--selected` / `--feedback-success` / `--feedback-error` / `--removing` | rowClassName 注入每个 tr | 行级语义：选中镜像（底色仍归 antd token）/ 行反馈 / 删除行 |
| `.sf-table-actions-cell` | 「操作」列 td 自动注入（注入先于 align 早退，显式 `align:'right'` 同样生效） | #2 操作区 hover 渐显 |
| `.sf-table-actions` | SfRowActions 显式包裹 div | 同上，页面显式声明时用 |
| `.sf-table-toolbar` + `--selected`（含 `__panes` / `__pane(--hidden)` / `__batch`） | SfToolbar 根与左区双面板 | #4 常规 ⇄ 批量面板切换 |
| `.sf-table-empty` / `.sf-table-refresh` / `.sf-table-refresh__icon--spin` | SfEmpty / 刷新 Button / Reload 图标 | #8 空态进场 / #5 图标自旋 |
| `.sf-search-form__applied` | SfSearchForm「已筛选 N 项」行 | 附带进场 |

### 31.3 8 类动画落点

| # | 动画 | 实现 |
|---|---|---|
| 1 | Skeleton→数据 | 骨架在 emptyText 路径（表头保留），行数=min(当前 pageSize, 20)；`sf-table-data-loading` 首载即挂（tbody 0.8），数据到达摘除 → 0.8→1 过渡 180ms；无 translate/scale |
| 2 | Row Hover | 行底色 antd rowHoverBg（浅灰）+ td `background-color 120ms` 过渡，行级禁 transform；操作区 opacity 0.72→1（120ms，`focus-within` 兜键盘，`@media (hover:none)` 恒 1） |
| 3 | Checkbox 选中 | `.sf-table .ant-checkbox-checked`（antd 6.6.5 `-checked` 挂 rc 根 span，无 `-inner`）一次性 scale 0.96→1（180ms，禁弹跳）；选中底 antd rowSelectedBg（Light alpha 0.06；Dark alpha 0.08 合规 0.04~0.08 上限，hover 同相 0.12）+ 选中态 td 过渡 180ms 与勾选 pop 同节奏 |
| 4 | 批量 Toolbar | SfToolbar grid 双面板恒占 `grid-area:1/1`（容器高度=max(两态) 不跳变）；进入 180ms / 退出 220ms（--sf-motion-slow），opacity+translateY(-4px→0)，visibility 延迟退场；selectedRowKeys>0 自动切换，页面只传 `bulkActions` |
| 5 | Refresh | 仅 Reload 图标 `sf-rotate` 560ms linear infinite；TS 绑定归一化 loading 起停 + 450ms 最短保持；整按钮零旋转 |
| 6 | 数据更新 | Header/Toolbar/分页静态；请求中 tbody 压暗 0.8（规格 0.72~0.85）+ usePagedList keepPreviousData 不清空，180ms 过渡与 #1 复用；排序图标色过渡 120ms（覆盖 antd 自带 240ms 慢档）；禁逐行 stagger |
| 7 | Row Feedback | `useTableRowFeedback.trigger`（仅 onSuccess/onError 后调）→ `--feedback-success/error` → td animation 480ms from `color-mix(--sf-success/--sf-danger 12%, transparent)` 回落；先 API 后反馈 |
| 8 | Empty State | `.sf-table-empty` fade-up 180ms（backwards，不常驻 transform）+ 图标 scale 0.98→1；图标+标题+说明+操作，高度由既有 padding 24px + SIMPLE 插图自然撑出，`min-height:180px` 兜底（实测纯文案空态 166px 低于规格下限） |
| 删除行 | fade→收缩→移除 | 两段式：`sf-row-fade` 0~120ms（内容+边线 opacity 归零）→ `sf-row-shrink` 120~240ms（padding/行高/边线收拢）→ 260ms 后 SfTable 过滤 DOM；多行并行删除逐 key 独立定时互不中断；服务器 total/分页不动，refetch 自愈提前清 / hook 2.5s 兜底恢复 |

### 31.4 页面接入 API

```tsx
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfRowActions } from '@/components/table/cells'

const fb = useTableRowFeedback()

<SfTable
  rowKey="id"                        // 全站约定；行反馈/删除动效按此匹配
  dataSource={list.items}            // 必传：内部据此区分 骨架首载/压暗刷新
  loading={list.isFetching}          // 语义不变；loading 为真即压暗，数据到达淡入
  bulkActions={<Button ... />}       // 可选：选中>0 工具栏自动切批量面板（需受控 rowSelection）
  emptyAction={<Button ... />}       // 可选：空态 CTA
  feedbackRowKey={fb.rowKey}         // 可选：行反馈 key（API 成功/失败后）
  feedbackTone={fb.tone}             // 可选：'success'（默认）| 'error'
  removingRowKeys={fb.removingRowKeys} // 可选：删除行 fade→收缩→隐藏（多行并行安全，传数组）
  onClearSelection={...}             // 可选：批量面板「清空」覆盖实现（缺省调 rowSelection.onChange([], [], { type: 'none' })，
                                     //   antd 6 RowSelectMethod 含 'none'；SfTable 与 SfToolbar 均有同名 prop）
/>

// 删除场景：onSuccess 里 fb.triggerRemove(id) + invalidate；onError 里 fb.trigger(id, 'error')
// 停用/启用等状态切换：onSuccess 里 fb.trigger(record.id, 'success')
// 行操作区（可选显式包裹；「操作」列已自动注入，无需包裹）：
render: (_, r) => <SfRowActions><Button>编辑</Button><Button>删除</Button></SfRowActions>
```

### 31.5 硬性约束

- **零 `transition: all`**：逐属性声明（background-color/color/opacity/transform 等）；时长/缓动一律
  `var(--sf-motion-*)`；颜色一律 Token / antd token，零新增颜色令牌（行反馈色由既有语义色 color-mix 派生）。
- **reduced-motion（CSS 媒体查询，非 JS）**：三档时长归零使 transition/一次性 animation 即时完成；
  无限循环动画（自旋）与进场/反馈类动画在媒体查询内显式 `animation: none`（0ms×infinite 会高频闪烁）；
  hover/选中/压暗/操作区 opacity 即时切换，保留必要 color/opacity 反馈；TS 定时器照常运行无副作用。
- **Dark Mode**：行反馈色随 dark 块语义色自动切换，无白闪；Body 压暗 0.8 主题无关；删除 antd Spin 蒙层叠加后暗色更干净。
- **Pad/触摸端**：体系作用域限 `.sf-table` / `.sf-table-toolbar` / `.sf-search-form__applied`，Pad 卡片流零影响；
  SfEmpty 新 props 全可选（Pad 11 个引用文件渲染与现状一致）；`@media (hover:none)` 操作区恒 1。
- **时序纪律**：先 API 后反馈（trigger/triggerRemove 仅在回调内调用）；动画只在视觉层，不阻塞业务操作；
  动效三层：微交互 120~180ms / 组件 180~250ms / 反馈 220~700ms，无 1s 以上长动画。

### 31.6 效率层一期动效增量（2026-10-06）

新增组件动画**只用于反馈**：全局搜索结果出现、Drawer 打开、批量结果揭示、任务切换（Alt+→ 下一条）、
成功/失败反馈；仍遵守 §31.5 全部硬性约束——零 `transition: all`、时长一律 `var(--sf-motion-*)`、
reduced-motion 媒体查询内归零、无 1s 以上长动画；快捷键驱动的界面切换不添加额外过渡（键盘操作要求即时响应）。

---

## 32. 快捷键规范（作业效率提升层一期，2026-10-06）

> PC 专用：ShortcutProvider 包裹 PcLayout 内容树。Pad 不挂载——Pad 卡片可达性层已有自身 keydown，一期不动。

### 32.1 集中管理

- `components/shortcut/ShortcutProvider.tsx` 是**全站唯一 keydown 监听点**（Context 注册表 + 统一分发）；
  业务页面禁止自监听键盘（scanner.md §3.1 冻结约束的 PC 落地；扫码输入组件焦点内承接除外，见 §22）。
- `components/shortcut/shortcuts.ts` 集中注册表：每条 `{combo, scope, description, handler, enabled}`；
  帮助面板由注册表驱动渲染，零手写文档；新增快捷键必须入注册表，禁止组件内散写。

### 32.2 一期键位表

```text
Ctrl/Cmd+K       全局搜索命令面板（§15.2）
Ctrl+F           当前列表搜索聚焦（列表页作用域）
Ctrl+R           刷新（preventDefault → invalidate active queries）
Esc              关闭自有浮层（全局搜索/帮助面板）；antd Modal/Drawer 的 Esc 归组件自身，不双抢
Enter            确认
Alt+→ / Alt+←    下一条 / 上一条（经 useNextTask 注册，仅任务作业页 enabled）
G+I / G+P / G+S / G+T   跳转 实时库存 / 采购 / 销售 / 我的任务（G 系 chord，800ms 序列窗口）
?                快捷键帮助面板（ShortcutHelpDrawer）
```

G 系跳转前经 canAccess 校验，无权限 message 提示不跳转。

### 32.3 输入态抑制与冲突规则

- target 为 INPUT / TEXTAREA / SELECT / isContentEditable，或焦点元素带 `[data-sf-scan-input]`（扫码输入框）
  时，**页面级快捷键全部禁用**——快捷键不与人工输入、扫码枪 HID 连续输入冲突。
- Esc 仅处理自有层浮层，不拦截 antd Modal/Drawer 自身的关闭行为；其余键位不占用 antd/浏览器既有交互。
- 快捷键不代替业务确认：扣减/确认类动作必须显式按键或点击（与 §22 智能下一步 `autoAdvance:'safe'` 同口径），
  高风险操作不设单键直达。

### 32.4 帮助面板

`ShortcutHelpDrawer` 按 ? 打开：分组展示全部已启用快捷键（combo + description + 作用域），
数据源=注册表；未注册或被禁用（enabled=false）的键位不展示，保证面板与实际行为一致。
