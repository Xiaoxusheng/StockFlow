# 全局表格设计系统统一（2026-10-05）

## 目标

全站表格统一为一套紧凑、克制、高信息密度的 WMS 表格设计系统：所有表格（含详情页/抽屉嵌套表）共享同一 Token、同一密度档、同一分页/空态/错误态、同一操作列形态与数字对齐契约。高级感来自排版/间距/层级/一致性，不做特效。

## 现状（Explore 结论）

- 体系已存在：`components/table/`（SfTable 已含密度三档（默认紧凑）/列显示隐藏/全屏/刷新/统一分页（共 x 条 + 条/页 + 快跳）/统一空态 SfEmpty/错误态 SfError/storageKey 持久化；SfToolbar/SfSearchForm/SfBatchBar 就位）；SfStatusTag 语义色走 Token。
- 覆盖面：58 个视图文件已用 SfTable；45 文件有 `align:'right'`、53 文件用 `sf-num`（tabular-nums）。
- 缺口：
  1. 10 个文件仍用裸 antd `<Table>`（全部是详情页/抽屉嵌套表）；
  2. 选中行/表头文字色/hover 未走 Token 映射；
  3. 无统一 Cell 组件（CodeCell/NumberCell/ProductCell/DateCell）；
  4. SfSearchForm 无「已筛选 N 项」提示；
  5. Loading 首载无骨架（antd Spin 空表闪 Empty）；
  6. `formatDateTime` 带秒，任务书要求统一 `YYYY-MM-DD HH:mm`；
  7. 操作列平铺（编辑/停用/删除一排）仍存在于多数列表页，仅 SkuList/UserList 已收敛 buildRowMenu 模式。

## 方案

- **T1 Token 层**：tokens.css 增 `--sf-table-header-text` / `--sf-table-selected-bg` / `--sf-table-selected-hover-bg`（Light/Dark）；App.tsx Table 组件 token 补 `headerColor / rowHoverBg(=--sf-surface-hover) / rowSelectedBg / rowSelectedHoverBg`（选中行=主题色 alpha 0.04~0.08）。
- **T2 SfTable 增强**：`variant?: 'page' | 'nested'`（nested=详情/抽屉嵌套表：无工具栏无分页，保留密度/空态/错误态；分页 props 转可选）；首载 `loading && 无数据` 时 emptyText 渲染骨架条（表头保持，任务书 §31）。
- **T3 SfSearchForm**：提交后显示「已筛选 N 项 · 清除全部」弱化提示行（§50），重置归零。
- **T4 统一 Cell**：新增 `components/table/cells.tsx`——NumberCell（右对齐+sf-num）、CodeCell（ellipsis+Tooltip+hover 显复制，Toast「已复制 xx」）、ProductCell（主名 500 + 弱化编码 12px）、DateCell（formatDateTime，13px 次要色）；`formatDateTime` 改为分钟精度。
- **T5 嵌套表迁移**：10 个裸 Table 文件迁 `SfTable variant="nested"`。
- **T6 页面收敛（并行）**：列表页操作列统一为「高频动作 ≤2 个 link 按钮 + 更多 ▾ Dropdown；删除永远在 More 内（danger）」——沿用 SkuListPage buildRowMenu + 声明式 Modal 确认先例，保留全部既有确认语义；数字列补 `align:'right'` + NumberCell/sf-num；状态一律 SfStatusTag；长文本 ellipsis。
- **T7 验证**：`npm run lint` / `npm run build`；浏览器实测 1920/1440/1280/1024 + Dark。
- **T8 文档**：frontend.md §6 增补（nested variant/cells/筛选提示/时间格式）+ changelog + current.md。

## 不做什么

- 不改 API / 数据库 / 业务状态机 / 权限 / 库存与打印逻辑（§55）。
- 不引入新依赖、不换 UI 框架、不做第二套 Table 封装（§3/§4：完善现有 SfTable）。
- 不做列拖拽排序（列显示/隐藏已满足 §27，SfTable 既有裁定维持）。
- 不重写已合规页面（SkuList/QrCodeCenter 等只做缺项修补）。
- 不动用户在途未提交改动（web/docs 大量未提交文件属二维码/主题轮交付，增量编辑不回退）。

## 验收标准

- 任务书 §60 全部 19 类页面抽查通过：字体/行高/筛选/Toolbar/操作/分页/Loading/Empty/Hover/边框/颜色统一。
- §61 浏览器四档宽度实测无横向溢出/固定列正常/筛选区不换行错位。
- lint / typecheck(build) 通过；无 Math.random/假数据引入。
