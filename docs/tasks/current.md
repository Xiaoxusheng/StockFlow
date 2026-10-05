# StockFlow 当前任务状态

> 唯一状态文件 ｜ 中断后先读本文件 + docs/plans/，继续原任务，禁止重新设计

## 当前任务

**后端主线（M1–M3）全量交付完毕，后端收尾轮已完成（2026-10-04：遗留债务清偿 + 000015/000016 迁移修复 + 阶段 20 CI 关卡 + swag 汇总 + 服务器部署升级与真库回归）**——MT1–MT7 门禁记录见下「2026-10-04 后端：M3 平台能力全量交付」节；开发计划余量均为**待环境项**：阶段 16 真机验收（PDA/扫码枪深度接入与多终端、StockFlow Scan 现场验收场景 9）与阶段 21 性能优化（索引/缓存/慢查询治理）待环境后启动；阶段 22 生产部署的环境配置/发布流程已随本轮推进（里程碑 M4 的 7 验收场景走通待现场）；CI 的 go test -race 关卡待 push 后首次 Actions 运行验证（main 领先 origin/main 9 提交未推送）。汇总见 docs/changelog.md 同日「后端收尾轮」条目与下文同日节。2026-10-04 清偿轮执行核对员债务清单（F3–F21 代码修复 + F5/F16/NEW-1 文档回写：guard-inventory 守卫、department_id fail-fast、seed 残留幂等、迁移 000015、release 密钥测试、BindErrorDetails 收敛、panic 日志截断、swag 269 路由注解 + `make swag`、数据权限集成测试等；同日复核修正轮：guard-inventory 补 _test.go fixture 豁免、F20 补收敛 printing/returns/stockops 14 处 bind 路径、迁移 000015 down 补齐 transfer 索引、F16 补注 m1-plan/analysis 三处），销项记录见 docs/changelog.md 同日条目。

### 历史任务：前端基础平台（2026-10-02，已交付）

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

## 2026-10-02 并行交付：前端页面第二批（五组并行开发 + 集成收口）

| 模块 | 内容 | 状态 |
|---|---|---|
| 认证域真实接通 | api/auth.ts / api/client.ts / stores/auth.ts / types/permission.ts / LoginPage / PcLayout 全量对齐后端 auth 冻结契约：LoginResult/MeResult、refresh 请求体、sessions 分页信封、snake_case 字段、must_change_password 强改闭环、PASSWORD_RULE 共享、canAccess fail-closed（current.md:47 清单销项，见上） | ✅ 静态验证通过，端到端链路待后端环境 |
| F6 库存中心余量 | 批次库存/序列号/库存转移/库存追溯 四页（api/inventory.ts 纯追加 batches/serials/transfers/trace 四端点，值域对齐迁移 000005 + business-flow §10.1 + inventory_ledgers；/inventory/analytics 本轮裁剪、/inventory/count 禁做由盘点中心承载） | ✅ 已接入路由 |
| F8 任务中心 | 我的工作台（待办/审批/任务/异常四计数）+ 我的任务列表（api/task.ts 前端先行契约：GET /api/workbench/summary、GET /api/tasks；待办/审批无菜单路径仅展示计数） | ✅ 已接入路由 |
| P0 出库作业 | 拣货/复核/打包/发货 四页（api/outbound.ts 纯追加四端点 /api/outbound/picking-tasks 等，对齐 business-flow §8.2–8.5） | ✅ 已接入路由 |
| P0 单据域 | 销售订单/销售出库/销售退货 三页 + 采购收货/采购退货 两页（api/sales.ts 新建、api/purchase.ts 纯追加 receipts/returns，对齐 business-flow §6.2/§7.2/§9.1/§2.1/§9.2） | ✅ 已接入路由 |

集成收口（router/index.tsx）：注册 15 条 lazy 路由（/workbench /tasks /inventory/batches /inventory/serials /inventory/transfers /inventory/trace /picking /checking /packing /shipment /purchases/receipts /purchases/returns /sales /sales/outbounds /sales/returns），IMPLEMENTED_PATHS 同步增补 15 条，程序化比对集合与 children 静态路由 39↔39 完全一致、无重复注册；菜单路径与各页路由逐条对齐，config/menu.tsx 零改动。跨文件一致性：15 页均 default export、六个 api 模块导出标识符无重名、页面调用与 api 方法定义逐一比对通过；SfStatusTag 未知状态沿用注册表优先 + label/semantic 三参兜底（本轮沿用前一轮决策：后端未冻结的前端先行枚举暂不补录 types/status.ts，列入 changelog 遗留清单）。后端任务/单据/outbound 域未交付，相关页面呈统一错误态（预期行为）；先行契约与遗留清单见 changelog 同日集成记录。验证：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0、`npx eslint src/router/index.tsx` EXIT=0（全局构建留门禁阶段）。

## 2026-10-02 并行交付：前端页面第三批（五组并行开发 + 集成收口）

| 模块 | 内容 | 状态 |
|---|---|---|
| 质量中心 | 质检单/不合格品/质量追溯 三列表页（api/quality.ts 前端先行契约：检验方式三值、处理结果九值、去向六值，对齐 business-flow §4.1–§4.3 + QC- 前缀 §13.1） | ✅ 已接入路由 |
| 调拨单 | /transfers 两维度 + 7 态状态机列表页（api/transfer.ts 对齐 §10.1 TR- 前缀；与库存转移 /inventory/transfers 注释明确语义区分） | ✅ 已接入路由 |
| 异常中心 | /exceptions 九类异常 + 生命周期 7 态列表页（api/exception.ts 对齐 §11.2） | ✅ 已接入路由 |
| 库存分析 + 库存详情 | /inventory/analytics（§10.1 口径 + @ant-design/plots）+ /inventory/stock/:skuCode 六页签详情（§10.3 头部五指标 + §10.4 层级下钻）；StockListPage 行点击进入、筛选/分页 sessionStorage 持久化（§26.3）；api/inventory.ts 纯追加三端点 + 四 Query 增 skuCode 筛选 | ✅ 已接入路由 |
| 单据详情 F7 | 入库/出库/采购订单 三详情页（新组件 SfDetailHeader/SfTimeline；Timeline 状态三规则推演，§13.4 环节时间；api/inbound.ts、api/outbound.ts、api/purchase.ts 各纯追加 GET /{id}；三个列表页最小改动加「详情」列） | ✅ 已接入路由 |
| 盘点中心 F9 | 盘点任务列表 + 详情（api/count.ts 四端点：列表/详情/明细/实盘登记 PUT；七态状态机 + 差异走库存调整单链路提示，无修改库存入口，§10.2/§10.5） | ✅ 已接入路由 |
| 数据中心 F11 | Excel 导入六步向导（multipart 上传/校验逐行定位/预览/确认/初始化库存高危二次确认）+ 导出任务列表（5s 在途轮询、downloadFile 认证下载，excel.md §1–§4/§6）；api/data.ts 前端先行契约 | ✅ 已接入路由 |

集成收口（router/index.tsx）：注册 14 条 lazy 路由（静态 9 条：/quality/inspections /quality/nonconforming /quality/trace /transfers /exceptions /inventory/analytics /counts /data/imports /data/exports；动态段 5 条：/inventory/stock/:skuCode /inbound/:id /outbound/:id /purchases/:id /counts/:id，均无菜单路径不入 IMPLEMENTED_PATHS，与静态段共存静态优先），IMPLEMENTED_PATHS 增补 9 条，程序化检查无重复注册；9 条静态路径与 config/menu.tsx（:47/:65/:78/:106-108/:111/:131-132）逐条一致，menu.tsx 零改动。跨文件一致性：14 页均 default export；28 个新页面 api 调用点与模块定义逐一比对通过；导出标识符全库查重仅 toStatusKey 重复（既有 F6 问题，无同文件双导入，保留）。**types/status.ts 本轮补注册 22 个状态键**（替代上一轮「前端先行枚举暂不补录」决策：调拨 3、异常生命周期 5、锁定 3、序列号 3、executed、counted、质检处置 6，label/semantic 与页面既有兜底逐键一致，渲染行为不变），并修复盘点页 PENDING_REVIEW 走 pending_review 被注册表「待审核」覆盖的显示 bug（改 pending_recheck）。后端质量/调拨/异常/盘点/数据/库存分析域未交付，相关页面呈统一错误态（预期行为）；先行契约与遗留清单见 changelog 同日集成记录。验证：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0、`npx eslint`（本轮修改的 4 文件）EXIT=0（全局构建留门禁阶段）。

## 2026-10-03 并行交付：契约对齐轮（组1 基础资料域）

| 模块 | 内容 | 状态 |
|---|---|---|
| 基础资料域契约对齐 | api/masterdata.ts 全量 snake_case 对齐后端 JSON tag（internal/masterdata/service_*.go）：Item/Payload 字段、关联 ID 提交统一 number（后端 *int64）、ProductQuery.category_id / SkuQuery.product_id+enabled / CategoryQuery.parent_id；删除 categories/units.remove（后端无 DELETE），六组补 setStatus（五域传 {status}、skus 传 {enabled}）；SkuItem/Payload 增 barcodes、ProductItem 增 image_urls；六列表页字段引用同步 + 编辑弹窗移除「状态」假开关与编码可编辑假象；分类/单位删除入口改停用（SfConfirm）、SKU/商品/供应商/客户增启停动作；名称列以真实下拉数据源按 id 兜底映射（后端列表不装配 *_name） | ✅ 静态验证通过，端到端待联调 |

验证：`npx tsc --noEmit -p tsconfig.app.json` 本轮修改 7 文件零错误（全仓余 4 错误在 inventory 域他组在途文件，与本轮无关）；grep 证实分类/单位页无 DELETE 调用、六页无 camelCase 字段残留。遗留与契约限制详见 changelog 2026-10-03 同日条目（商品编辑清空分类/单位不生效为后端指针三态限制；SKU 条码编辑入口、image_urls 表单入口待后续）。

## 2026-10-03 并行交付：第四批路由、菜单与全局体验收口（组6 集成收口位，依赖组5 页面文件就绪）

| 模块 | 内容 | 状态 |
|---|---|---|
| 路由收口 | router/index.tsx 注册静态 /reports（报表中心）+ 组5 无菜单路由 /sales/new、/sales/:id、/counts/new（静态段先于动态段：/sales → /sales/new → 静态子段 → /sales/:id 殿后；/counts → /counts/new → /counts/:id）；IMPLEMENTED_PATHS 增补 /reports（49 条），/sales/new、/counts/new 为无菜单静态路径不入 Set；程序化比对：children 57 条路由（静态 51 + 动态 6，动态段新增 /sales/:id）无重复注册、IMPLEMENTED_PATHS ↔ 静态路由一一对应、静态与占位路由零重叠 | ✅ 已接入路由 |
| 菜单脱节修正 | config/menu.tsx 删除「库存盘点」/inventory/count 子项（此前落占位页，按既有裁决由 /counts 承载）；其余菜单零改动；程序化断言 /inventory/count 从菜单/路由/IMPLEMENTED_PATHS/占位四处消失 | ✅ |
| 报表中心 | views/reports/ReportsPage.tsx（聚合入口：链到已交付真实 /inventory/analytics、/inventory/ledger、/data/exports，无写死数据）+ api/reports.ts 前端先行契约（GET /api/reports；后端报表域属阶段 17–18，页面呈统一错误态/空态，SfLoading/SfError/SfEmpty 三态完整） | ✅ 已接入路由，目录数据待后端交付 |
| 全局体验 | PcLayout.tsx：全局搜索由装饰性（onPressEnter 仅清空）改为菜单直达——AutoComplete + MENU_TREE 叶子标签匹配（分组容器不作直达目标，与侧边栏展开行为一致），点选/回车均真实 navigate、未命中真实提示，候选源与侧边栏共用 canAccess fail-closed 过滤；通知铃铛接真实 unread-count（Badge 未读数，后端未交付自动隐藏，关抽屉 refetch） | ✅ |

集成收口说明：组6 开工时组5 三页面文件（SalesOrderCreatePage/SalesOrderDetailPage/CountCreatePage）尚未落盘，会话中途落盘后完成注册（列表页新建/详情入口已由组5 接线，本组仅注册路由）；本轮仅改 fileScope 内 5 个 web/src 文件（router/index.tsx、config/menu.tsx、layouts/PcLayout.tsx、views/reports/ReportsPage.tsx、api/reports.ts）。验证：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0、`npx eslint`（5 文件）EXIT=0、node 程序化路由比对 5/5 断言 PASS；浏览器运行时实测未执行（无浏览器自动化工具链），待门禁/联调阶段；全局构建留门禁阶段。

## 2026-10-03 并行交付：契约对齐轮集成收口（组2–组5 交付归档 + 跨组一致性检查）

| 模块 | 内容 | 状态 |
|---|---|---|
| 组2 仓库空间域 | api/warehouse.ts + 仓库五页全量 snake_case 对齐 dto.go、关联 ID 提交 number、UpdatePayload 拆分、WarehouseMap 改 occupancy_status 四值（service_map.go）；types/status.ts 库位占用键修正 | ✅ 静态验证通过，端到端待联调 |
| 组3 系统权限域 | api/user.ts + api/rbac.ts 对齐 UserView/RoleView JSON tag、请求体 number 形态、assignRoles/Permissions 全量替换语义（先取详情预选防静默清空）、修复 pageSize 超 MaxPageSize 运行时阻断、状态枚举修正 ENABLED/DISABLED；销项「api/user.ts 对齐 UserView」遗留（见关键上下文） | ✅ 静态验证通过，端到端待联调 |
| 组4 封装组件与库存域 | 新建 SfExportButton（按钮级权限 fail-closed + 跳转导出中心）/SfConfirm（危险操作确认封装）；api/inventory.ts 已交付四域（stock/ledger/batches/serials）对齐 handler.go View；StockListPage 导出接线、StockDetailPage 改库存行 id 语义；库存三页+作业/采购五页死按钮 11 个移除；ExportTaskPage 消费 URL query | ✅ 静态验证通过 |
| 组5 销售与盘点创建/流转 | api/sales.ts + api/count.ts 纯追加（orders.create/detail、create/updateStatus）；/sales/new、/sales/:id、/counts/new 三新页；CountDetailPage 七态状态机流转（Modal.confirm 二次确认）；新建/流转按钮 canAccess fail-closed | ✅ 静态验证通过，后端 M2 冻结后回对 |

集成收口动作（本轮）：① 路由与菜单核验——组6 已注册 /reports、/sales/new、/sales/:id、/counts/new（静态先于动态、无重复），/inventory/count 菜单项已删除、/counts 指向实现，本轮 router/menu 零改动；② 波及项销项——CountCreatePage real_name 已由组5 修复、销售两页导出死按钮已由组5 移除；③ 死按钮程序化复查（views/layouts/components 全部 Button 元素块，导出/新建类缺失 onClick）=0；④ api/views/components 层重复导出=0；⑤ 22 个 Api 命名空间方法与页面调用点比对全部匹配、无直连 axios；⑥ 小修 api/warehouse.ts 四组 setStatus 返回类型 `*Item`→`{status}`（后端 handler.go:236/346/446/565 返回 gin.H{"status"}，消费方均不消费返回值）；⑦ 裁决 CountDetailPage 流转确认保留 Modal.confirm（SfConfirm 为 danger 固定的危险操作封装，与业务流转确认语义不匹配，cancel 已单独 danger），过时注释已更新。前端先行契约与遗留清单见 changelog 同日集成收口条目。验证：`npx tsc --noEmit -p tsconfig.app.json` 全量 EXIT=0、`npx eslint`（本轮修改 2 文件）EXIT=0；全局构建留门禁阶段。

## 2026-10-03 并行交付：后端安全修复轮（S1–S15，三工作包）

| 工作包 | 内容 | 状态 |
|---|---|---|
| 认证域修复（internal/auth） | S1 授予侧特权边界（非超管禁授 super_admin/自身不持有权限点/越 data_scope，AssignPermissions 补 is_system 保护）、S8 锁定/停用统一 AUTH_CREDENTIALS_INVALID（真实原因仅 login_logs，不回显 locked_until）、S9 用户目录按数据范围过滤+列表裁剪 last_login_ip、S10 UpdateUser 变更即踢目标用户会话（销项 plan §13.3 漂移）、S14 CreateUser 置 must_change_password=true、S15 改密原密码失败计数与短期锁定、S4 认证域部分（失败计数 username+IP 双键）、S7 认证域部分（login_logs 写失败记 error） | 🔄 并行实施中（完成与门禁结论以安全修复报告/独立复审为准） |
| 平台层修复（internal/config·middleware·router·health·database/seed.go·cmd·config.example.yaml） | S2 server.mode 默认 release（本地开发显式设 debug）、S3 初始管理员密码 ≥12 位三类字符、S4 /api/auth IP 限流（SF_AUTH_RATE_LIMIT_IP_PER_MINUTE 默认 30）、S5 SetTrustedProxies（SF_SERVER_TRUSTED_PROXIES，默认不信任任何代理）、S6 请求体上限（SF_SERVER_MAX_BODY_BYTES 默认 1MB）+ MaxHeaderBytes、S7 入口截断 request_id/UA/IP 对齐审计列宽、S9 viewer 映射剔除 auth:user:list/read、S12 release 下 sslmode=disable 启动告警、S13 /ready 就绪结果 1s 缓存 | 🔄 并行实施中（同上） |
| 部署文档（S11） | docs/deployment.md 升 v1.1：§1.1 环境变量清单（作用/默认值/必填/生成方式）、§2.1 grants 执行步骤与纪律（新审计表人工重跑、生产禁 auto_migrate=true、app_grants.sql 尾注悬空指向勘误）、§7.1 发布检查单、§9 反向代理与网络安全基线（XFF 覆写/可信代理/8080 仅内网/HTTPS 反代终结/探针仅 LB 内网）；§3/§6 与实现对齐（/ready 检 DB+Redis、初始管理员强口令与首启立即改密必做）；决策记录见 changelog 同日条目，plan §7.3 已按防枚举决策修订 | ✅ 已交付（环境变量名/默认值与代码逐项核对） |

## 2026-10-03 并行交付：Pad 端 F14（地基 + 三作业页 + 三查询移库页 + 盘点异常页，四组并行开发 + 集成收口）

| 模块 | 内容 | 状态 |
|---|---|---|
| Pad 布局地基 | web/src/layouts/pad/ 九原语：PadLayout（Header 在线点/主题/退出 + PAD_NAV_ITEMS 十页导航）+ PadPageShell（横竖屏结构性切换三栏/堆叠）+ PadActionBar（五槽底栏，disabled 点按 Toast 门禁）+ PadTaskCard/PadInfoCard/PadScanStub + usePadOrientation + pad.css（--sf-pad-* 局部 Token 作用域 .sf-pad-root，颜色/间距/圆角仍引用 --sf-*） | ✅ 已交付并接入路由 |
| Pad 首页/任务页 | /pad/home 四块（工作台四计数真契约 + 快速作业宫格 10 入口 + 预警「-」占位 + 最近操作 SfEmpty 占位）；/pad/tasks chip 筛选 + 状态分组任务卡列表 | ✅ 已交付并接入路由 |
| Pad 作业三页 | /pad/receive 收货（ReceiptTaskCard 列表 + 应收/已收/待收大字 + 数量步进 + 批次效期录入占位）、/pad/quality 质检（InspectionTaskCard + 合格/部分合格/不合格三大按钮占位）、/pad/putaway 上架（三步扫商品→扫库位→校验序列，Steps 指示） | ✅ 已交付并接入路由 |
| Pad 查询/移库/调拨三页 | /pad/inventory 库存（stockSummary 七计数 + stock/ledger 真实端点 + SKU/库位本地 Map 补显）、/pad/stockmove 移库（三步大按钮流 + [确认移库] 占位）、/pad/transfer 调拨（TransferDocCard + 七态流转只读卡 + 出入库占位） | ✅ 已交付并接入路由 |
| Pad 盘点/异常两页 | /pad/count 盘点（CountTaskCard/明细行 + RegisterPanel 实盘登记——countApi.registerItem 本轮唯一真实写端点 + 差异前端计算 + [暂停] sessionStorage 草稿）、/pad/exception 异常（九类×七态大 chip 筛选 + 认领/处理/关闭/拍照占位） | ✅ 已交付并接入路由 |
| 集成收口 | router/index.tsx 注册 /pad 独立顶层段（挂 PadLayout，不挂 PcLayout）：index→/pad/home + 10 条 lazy 子路由，path 与 PAD_NAV_ITEMS 逐字一致；config/menu.tsx 零改动、/pad 不入 IMPLEMENTED_PATHS（Pad 不进 PC 菜单） | ✅ 全绿 |

集成收口说明（本轮实测）：四组共 26 个新文件（layouts/pad 9 + views/pad 17），仅 PadHomePage.tsx:9 深路径 import 修为统一出口 '@/layouts/pad' 一处小修；任务书草案 /pad/stock、/pad/move 按各组 integrationNeeds 与 PAD_NAV_ITEMS（PadLayout.tsx:36,38）修正为 /pad/inventory、/pad/stockmove，否则导航 chip 不高亮；死按钮复查——PadActionBar 原语与各页自建占位（收货确认/质检判定/移库/调拨出入库/异常认领等）均 disabled + 外包 span 点按 Toast 原因，0 静默死按钮；路由无重复（与 /login、/、/403、/500、* 平级）。后端任务/采购收货/质量/盘点/异常域未交付，Pad 页面呈统一 Loading/Error 态（预期行为，禁止假数据）；countApi.registerItem 为本轮唯一真实写端点。前端先行契约与遗留清单（首页计数口径差异、TaskStatus 无异常值、TaskItem 无优先级、真机横竖屏未实测等 9 项）见 changelog 同日 Pad 集成收口条目。验证：`npx tsc --noEmit -p tsconfig.app.json` 全量 EXIT=0、`npx eslint src/router/index.tsx src/views/pad/home/PadHomePage.tsx` EXIT=0（全局构建留门禁阶段）。

## 2026-10-03 并行交付：打印/文件/设备/系统扩展轮（组A–E 页面 + 组F 集成收口）

| 模块 | 内容 | 状态 |
|---|---|---|
| 打印中心（F12） | views/printing/PrintingCenterPage.tsx 聚合页 + PrintPreviewPage.tsx 独立预览（真实数据渲染/缩放/翻页/react-to-print 打印/下载 PDF 后端未交付呈统一错误态）+ components/print/ 五组件（SfPrintButton 统一封装，禁用 window.print）+ api/printing.ts（/api/prints 前端先行契约） | ✅ 已交付并注册路由 |
| 文件中心（F11 余量） | views/data/FileCenterPage.tsx 附件中心（上传/预览/下载/删除）+ components/common/SfAttachment.tsx + api/file.ts 前端先行契约 | ✅ 已交付并注册路由 |
| 设备中心（F13） | views/device/DeviceListPage.tsx 四类型列表复用组件（deviceType 按 pathname 解析，api/device.ts DEVICE_TYPE_BY_PATH）+ DeviceCreatePage.tsx（新建 + 激活二维码）+ DeviceDetailPage.tsx + components/device/SfDeviceStatus.tsx + api/device.ts（/api/devices 前端先行契约） | ✅ 已交付并注册路由 |
| 系统管理余量 | views/system/ LogPage/JobPage/SettingsPage/MonitorPage 四页 + api/system.ts（/api/logs + /api/system 前端先行契约）；/system/notifications 无交付页面仍占位 | ✅ 已交付并注册路由 |
| Dashboard 增强 | DashboardPage 拆分 DashboardCharts/DashboardLists/DashboardMetricStrip/dashboardView + api/dashboard.ts 对齐 + 新增 SfInventorySummary/SfInventoryTable 组件 + usePagedList/NotificationDrawer/CountCreatePage/库存三页/ReportsPage 波及修改 | ✅ 已交付 |
| 集成收口（组F） | web/src/router/index.tsx（唯一归属文件）注册 13 条路径：10 条菜单静态入 IMPLEMENTED_PATHS（49→59）消除占位、/data/printing/preview 与 /devices/new 无菜单静态、/devices/:id 殿后（静态先行）；config/menu.tsx 零改动；程序化门禁全绿：路由比对 10/10 断言（无重复/一一对应/占位零重叠/new 先于 :id/lazy import 目标全存在）、死按钮 0（101 文件）、window.print() 调用 0、跨文件重复导出 0、api 符号比对 115 语句 470 符号 0 不匹配、直连 axios 0 | ✅ 全绿（tsc / eslint EXIT=0；浏览器运行时实测未执行，待联调） |

集成收口复核（组F 后二次独立验证）：路由比对脚本重跑 ALL PASS（68 lazy 目标文件存在、IMPLEMENTED_PATHS=59、children 静态 63 条无重复、13 条新路径逐条在位、/devices/new 先于 /devices/:id）；`npx tsc --noEmit -p tsconfig.app.json` EXIT=0（组A 报告的 DeviceDetailPage/LogPage 3 个 tsc 错误已由并行组修复归零）；`npx eslint` 本批 27 文件 EXIT=0；死按钮复查（145 Button 块）真实死按钮=0（2 处初报均误报：PrintingCenterPage:559 为非 SUCCESS 态 disabled 占位、SfConfirm.tsx 内部透传 onConfirm）；api 调用比对（115 语句/470 符号/119 调用点）=0 不匹配；menu.tsx 与路由逐条一致零改动。修正记录：跨文件同名导出复查=1（toStatusKey，api/masterdata.ts:20 与 api/warehouse.ts:21，第三批已记录既有项、无双导入、保留），组F 报告「同名导出=0」系扫描口径未含既有 api 域。前端先行契约与遗留清单见 changelog 同日集成收口条目。

## 2026-10-03 后端：M3 平台基座（backend-m3-plan MT0 基座段，平台基座工程师）

| 交付物 | 内容 | 状态 |
|---|---|---|
| 迁移 000011–000014 | datax（import_tasks/import_task_rows/export_tasks/files）/printing（print_templates/print_tasks/print_task_rows）/devices（devices/device_configs/scan_logs/device_logs/app_versions）/sysops（scheduled_jobs/scheduled_job_runs/system_configs/notifications/backup_records）17 表成对 up/down；无跨域外键、状态 CHECK 与方案 §5 同源、uk_notifications_dedup/uk_backup_records_inflight 部分唯一幂等索引、IMP/EXP/PT 单号唯一 | ✅ 结构自查全绿（本机无 PG，migrate up/down 实测留待具备 PG 环境） |
| grants 追加 | app_grants.sql：scan_logs/device_logs 入审计分层（仅 SELECT+INSERT）+ 可选清理维护角色段（\if :{?maint_user}，持审计四表 SELECT+DELETE，log_cleanup 外置部署侧依据） | ✅ |
| internal/asynqx | Queue 接口 + asynqQueue/inlineQueue 双实现（redis.enabled=false 同步降级）+ 任务类型冻结注册表（datax:import:commit / datax:export:run / printing:task:render）+ Server/Inspector 生命周期封装 + TaskID 经自定义头透传 | ✅ 单测全绿（asynq 真队列属 //go:build integration 面） |
| internal/sysops cron 基座 | robfig/cron 装配、冻结五任务注册表（handler 空实现位，MT5/各域经 RegisterJobHandler 注册）、进程内 single-flight + pg advisory lock 双闸防重入、执行日志落 scheduled_job_runs + last_run_* 回填、SetEnabled 热更新 | ✅ 单测全绿（内存替身注入，不依赖 PG） |
| internal/storage | files 表 GORM 模型 + 扩展名/嗅探 MIME 白名单交叉校验 + 服务端重命名（uuid.ext）/yyyyMM 路径生成 + 路径穿越防御 + 大小上限 LimitReader 复核 + 临时文件原子落盘；storage.root 可配置 | ✅ 单测全绿 |
| docnum M3 前缀 | frozenRules 追加 IMP/EXP/PT（ResetDay 6 位流水，有意避开 PRT），引擎零改动，注册表测试扩至 20 前缀 | ✅ |
| config §3.2 | storage/queue/datax/sysops 九键三处同步（结构体 + defaults + config.example.yaml）+ Validate 快速失败 | ✅ |
| cmd/server 生命周期 | asynq Server（Redis 模式）/cron Scheduler main 启动 + 优雅停机顺序：HTTP → cron → 队列 → 连接 | ✅ 编译通过（运行时联调待 PG/Redis 环境） |

门禁：`go build ./...`、`go vet ./...`、`go test ./...` 全绿（全包）；`gofmt -l` 空；asynq 库 import 仅 internal/asynqx（guard-asynq 口径）。新依赖：hibiken/asynq v0.26.0、robfig/cron/v3 v3.0.1（直接）；xuri/excelize/v2 v2.11.0、boombuler/barcode v1.1.0（已入 go.mod，MT1/MT2 接入前为 indirect，`go mod tidy` 会移除——接入时重取即可）。`go test -race` 本机不可用（CGO_ENABLED=0 且无 gcc），列入具备工具链环境的复验项。MT0 余量（非本工位 ask）：permissions.go M3 段、router 装配、seed 收编、Makefile 守卫（guard-readonly 白名单模式等）、health /ready 存储检查、§16 契约类文档回写。

## 下一步（按 frontend.md §29 顺序）

1. **F6 库存中心全量页面**：✅ 批次库存、序列号、库存转移、库存追溯（第二批）+ ✅ 库存分析、SKU 库存详情（2026-10-02 第三批，三个新端点为前端先行契约待后端 M1/M2 冻结回对）；库存盘点按既有裁决由盘点中心 /counts 承载
2. **F7 单据详情**：✅ 入库单/出库单/采购订单三详情页已交付（2026-10-02 第三批，SfDetailHeader/SfTimeline 新组件；详情端点为前端先行契约）；行级状态列、打印/导出真实动作待后端单据域契约冻结后补
3. **F8 任务中心**：✅ 我的工作台、我的任务已交付（2026-10-02 第二批，前端先行契约待后端任务域对齐）；「我的待办」「我的审批」入口待审批/待办菜单（如 /approvals）落地后补 path
4. **后端阶段 3–8**（仓库根 Go module）：✅ 迁移（T1）→ ✅ **认证权限（T2，2026-10-02 交付：/api/auth 全套 + /users、/roles、/permissions、/departments 管理 API，JWT+Redis 会话双轨、登录保护、RBAC/数据权限助手、敏感操作审计，详见 changelog 当日条目）** → ✅ **基础资料（T3）/ 仓库（T4）/ 库存核心（T5），2026-10-02 交付（三域 62 条路由 + 九个库存变更原语，详见 changelog 当日三条交付记录）** → ✅ **T6 已销项（2026-10-04 复核：WithWarehouseChecker 已装配于 internal/router/router.go:100；swag 汇总与 Makefile `swag` 目标由同日清偿轮 F8 完成，269 条路由注解与 gin 运行时路由表逐一核对一致，见 changelog 同日条目）**
5. ✅ F9 盘点、F11 Excel 导入导出与文件中心（2026-10-02 第三批 + 2026-10-03 本轮文件中心交付）、✅ F12 打印、F13 设备（PC 端，2026-10-03 本轮交付并注册路由）、✅ **F14 Pad（2026-10-03 本轮交付并注册路由：/pad 独立段 + 十页；打印/文件/设备/系统/Pad 运行域端点为前端先行契约待后端交付回对）**；剩 F15–F16 Scan（库位地图已交付）

## 关键上下文

- **后端 auth 域已可用**（需 PG15+Redis7、迁移后启动；运行前按 docs/deployment.md §1.1 环境变量清单注入——SF_AUTH_JWT_SECRET、SF_ADMIN_INITIAL_PASSWORD（空库首启）、SF_SERVER_MODE=release（2026-10-03 安全修复轮起默认 release，本地开发显式设 debug；release 缺 JWT 密钥启动失败））。登录响应：`{access_token, token_type:"Bearer", expires_in, refresh_token, must_change_password, user}`；/api/auth/me 返回 `permissions` 权限点集（编码见 internal/auth/permissions.go）。
- **前端 auth.ts 对齐清单**（后端契约已定，前端适配）——✅ **已完成（2026-10-02 并行轮认证域组交付，集成轮补记）**：`token→access_token`、`refreshToken→refresh_token`、`oldPassword/newPassword→old_password/new_password`、会话字段 `id→session_id、userAgent→user_agent、createdAt→login_at`、GET /api/auth/sessions 为分页信封（items 取数组）；另含 MeResult 会话组装、must_change_password 强改闭环（403 AUTH_PASSWORD_CHANGE_REQUIRED 特判 + 不可关闭强改 Modal）、PASSWORD_RULE 与后端 password.go 对齐导出、canAccess fail-closed（空权限不放行）与菜单码归一映射（types/permission.ts RESOURCE_ALIASES）。~~遗留：api/user.ts（用户管理域 camelCase 自有类型）对齐后端 UserView JSON tag~~ ✅ **已完成（2026-10-03 契约对齐轮组3 交付，集成轮销项）**：UserView 全量 snake_case + department_id/role_ids 请求体 number 形态，见 changelog 同日条目；localStorage `sf.auth` 旧格式会话被拒绝，联调发布需重登一次。
- ~~**待办（scope B）**：000001 迁移缺 uk_roles_code/uk_permissions_code/uk_departments_code 唯一索引~~ ✅ 已修复（2026-10-02 复核：up 补齐三表 code 唯一索引 + permissions/departments parent_id 反查索引，down 原引用即对齐；权限点同轮补录 inventory:batch/serial 至 106 个，见 changelog 复核修复条目）。
- ~~**待办（scope A/T6）**：router 注入 `auth.WithWarehouseChecker(warehouse.NewChecker(db))`（plan §4.3 规则①真实装配 fail-fast）与 swag 汇总、Makefile swag 目标~~ ✅ **T6 已全部销项**：WithWarehouseChecker 已装配（internal/router/router.go:100）；swag 汇总与 Makefile `swag` 目标于 2026-10-04 清偿轮 F8 完成（269 条路由最小集注解，`make swag` 生成 apidocs/，操作数与 gin 运行时路由表逐一核对一致）。
- 后端 T3/T4/T5 三域已交付（2026-10-02）；✅ **M2 业务单据域已交付（2026-10-03：采购/销售/库存作业/退货追溯四域全链路 + docnum 编号引擎 + 迁移 000006–000010 + router 装配/seed 收编；独立评审修复轮同日完成——采购退货序列号逐件核销、取消/收货并发互斥、退货退量咨询锁防超、purchase 详情数据权限 fail-closed、make ci 守卫扩展，详见 changelog 2026-10-03 M2 交付与修复轮条目）**；前端作业页的「M2 冻结后接线」disabled 占位与前端先行契约（/api/inventory/summary、/api/inventory/analytics、任务域 /api/tasks 等）待前端对齐轮消化；DEV 旁路 `VITE_AUTH_BYPASS=1` 仅开发构建。
- 权限点模型已定义（types/permission.ts）→ auth 域交付后 /api/auth/me 的 permissions 即为菜单/按钮过滤真实数据源。

## 2026-10-04 后端：M3 平台能力全量交付（backend-m3-plan MT1–MT7 + 集成装配 + 评审修复两轮）

| 交付项 | 内容 | 状态 |
|---|---|---|
| MT1 Excel 数据中心 | internal/datax 21 文件：九类导入向导状态机、16 模块流式导出、文件中心（可见性规则）、12 个域接入文件 | ✅ |
| MT2 打印中心 | internal/printing：模板/任务/七码制条码/渲染数据包 + 5 个 printing_content.go 装配接入（Orchestrator 裁决只读白名单） | ✅ |
| MT3 设备与扫码 | internal/devices 15 文件：激活码防重放/设备令牌可撤销/心跳/配置下发/resolve 六级解析+去重窗口 + 7 个 devices_resolve.go | ✅ |
| MT4 报表与运维 | internal/reports + internal/sysops：七端点报表/只读智能建议/日志查询//api/system/预警扫描任务/备份登记 | ✅ |
| 集成装配 | router 五域注册、41 权限点收编单一来源、种子 63 MENU+229 动作点（探针对齐）、Makefile 四守卫、main.go asynq/cron 生命周期 | ✅ |
| 迁移 | 000011–000014 四组成对迁移 17 平台表（72 表封闭断言通过） | ✅ |
| 评审修复 | 第一轮 8 项全修复 + 第二轮 4 项（启动清扫 RecoverStaleTasks/设备日志 jsonb 校验/任务元数据 FileScope 收口等）已落盘验证 | ✅ |
| 门禁 | go build/vet/vet -tags integration 全过；go test -count=1 24 包全 ok；gofmt 干净；六项守卫零命中 | ✅ |

未覆盖（诚实清单）：迁移 000011–000014 与 asynq 真队列/cron 未在真实 PG+Redis 执行（//go:build integration 就位待服务器环境）；go test -race 本机无 gcc 未跑；设备真机联调属阶段 16 现场；工作流第二轮复查的修复清单第 5 项以后若存在未完成条目无从确证（运行死于模型创建失败，可见 1–4 项已验证）。后端主线阶段 3–19 全部交付完毕；余阶段 16 现场验收与 20–22。

## 2026-10-04 构建：阶段 20 CI 关卡（GitHub Actions，CI 工程师）

- `.github/workflows/ci.yml` 落盘：push 与 pull_request 触发，ubuntu-latest 三个并行 job——**lint**（gofmt -l cmd internal 有输出即失败（Makefile fmt-check 同款）+ go vet ./... + go vet -tags integration ./...）、**test**（go test -count=1 ./... 与 go test -race -count=1 ./...，补齐上表"本机无 gcc 未跑 race"的遗留复验项）、**guards**（make 七守卫目标，命令单一来源在 Makefile、与清偿轮 F3 接线一致，workflow 不复刻防漂移）；Go 版本读 go.mod（setup-go go-version-file），模块缓存 setup-go cache: true。详见 changelog 同日条目。
- 本机已验：actionlint v1.7.12 过、js-yaml 结构断言全过、gofmt -l cmd internal 空、vet±integration 全过、go test -count=1 ./... 24 包全 ok、六项失败型守卫零命中（guard-status 按设计仅报告，7 行既有人工复核清单）。go test -race 本机不可跑（无 gcc、CGO_ENABLED=0），由 ubuntu-latest 自带 gcc 承载——**待 push 后首次 Actions 运行验证**。
- 阶段 20 CI 关卡本项交付完毕；阶段 20–22 余量：**阶段 21 性能优化与阶段 16 真机验收为待环境项**（需性能压测/真机环境后启动），阶段 22 生产部署已随 2026-10-04 服务器部署升级与真库回归推进（验收演示待现场）——汇总见 docs/changelog.md 同日「后端收尾轮」条目与下文同日节。

## 2026-10-04 后端：收尾轮汇总（清偿 + 迁移修复 + CI + swag + 服务器真库回归）

| 事项 | 结果 | 状态 |
|---|---|---|
| 遗留债务清偿（F3–F21） | guard-inventory 守卫（含 _test 豁免修正）、department_id fail-fast、seed 残留幂等、BindErrorDetails 52 处收敛、panic 日志截断、release 密钥测试、数据权限集成测试落盘 + 复核修正轮 5 项 | ✅（F7 集成测试需 SF_TEST_PG_*，本机未执行——诚实标注） |
| 000015 迁移 | ledger zone_id/shelf_id 收紧 NOT NULL + 主单据表 created_by 索引；真库回归移除非法索引后终态 8 索引 up/down 成对（撰写时 grep 核实） | ✅ |
| 000016 迁移 | purchase 域 9 表补 deleted_at（真库回归暴露 42703）；down 1 / up 双向真库验证通过 | ✅ |
| 部署物升级 | Dockerfile 预建 /app/data（app 属主）、compose filesdata 卷、SF_DEVICES_JWT_SECRET 三处接入 | ✅ |
| 真库回归 | 服务器部署升级后回归首次暴露 000013/000015/MigrateUp/000016 四缺陷并全部修复 | ✅ |
| CI 流水线（阶段 20） | .github/workflows/ci.yml 三 job 与 make ci 同源；本机 actionlint/结构断言/vet±integration/go test/守卫全过 | ✅ 交付；-race 关卡待 push 首跑验证 |
| swag 汇总 | 269 操作注解 + Makefile swag 目标 + apidocs/（撰写时核实 swagger.json summary 计数 269） | ✅ |
| 撰写时门禁复验 | go build / go vet / go vet -tags integration / go test -count=1 ./...（22 含测试包全 ok） | ✅ 本会话实测 |

**待环境项（后续启动，非本轮可完成）**：**阶段 16 真机验收**——PDA/扫码枪深度接入与多终端、StockFlow Scan 现场验收（requirements.md 验收场景 9）；**阶段 21 性能优化**——索引、缓存、慢查询治理（architecture.md §7）。两者均需真机/压测环境到位后启动。挂账与未完成：CI Actions 首跑验证待 push（main 领先 origin/main 9 提交）；集成测试契约缺口与隔离改造待立项（SF_TEST_REDIS 密码、auth 相对路径迁移目录、并发扣减断言矛盾、单据域夹具隔离，见 changelog 同日条目）；阶段 22 生产部署的环境配置/发布流程已推进，7 验收场景走通（M4）待现场。

细节与证据见 docs/changelog.md 同日「后端收尾轮」条目。

## 2026-10-05 联调：真联调交付轮（本地便携环境 + 12 域簇契约联调 + Dashboard/分析接真数据 + 终局门禁）

**做什么**：① 本地 Windows 便携开发环境——PG 16.10 + Redis 5.0.14.1 便携件落 `.local-env/`（零安装、零管理员权限、不动注册表/系统服务），docs/dev-environment.md 全程实测手册（启动/建库/种子/账号/踩坑 8 条/从零重建速查）+ scripts/ci-local.sh 无 make 门禁脚本；② 前后端真联调——后端 :8080 + vite dev 起真环境，12 域簇 SmokeCheck 19 代表端点 + 写路径 + 会话链路 HTTP 实测，修复三类契约断裂（items:null→[]、reports SKUID 列映射、报表时间形态）与 batch_id 缺省语义行为修复；③ 后端补齐前端先行挂账契约——Dashboard/分析域 6 端点（internal/reports/dashboard.go：日末趋势「现存量锚点回推」、ABC 分档后端计算）；④ 000018 迁移（质检 result CHECK 空串逃生门，修手动建单 500）；⑤ 演示种子 §8.5 趋势形态（期初流水分散近 7 天 + 4 笔净零对补影流水）；⑥ 前端 106 文件（87 改 + 19 新增）契约对齐 + 单据域表单/抽屉 + 路由接线 + Dashboard/AnalyticsPage 接真数据 + vite 代理故障可见化 + 草稿清理（web/src/api 两个 txt 对照稿删除）；⑦ .gitignore 覆盖 .local-env/ 与 data/（运行时上传件）。逐项细节见 docs/changelog.md 同日「真联调轮」「前端收口轮」「000018 迁移」「联调终局汇总」四条目。

**做到哪（终局）**：全部交付并按 feat(server)/feat(web)/chore(dev)/docs 四提交落库。终局门禁全绿——`go build ./... && go vet ./... && go test ./...` 退出码 0（22 包 ok）、`cd web && npm run build`（tsc -b && vite build）退出码 0；独立复测 5/5 通过。过程未过项均已收敛：收口轮 npm run build 曾 9 报错（并行簇在途文件）终局归零；5173/5174 端口被外部进程占用为环境项（vite 落 5174 走 127.0.0.1 验证）。

**遗留什么**：阶段 16 真机验收与阶段 21 性能优化（待环境，见 2026-10-04 收尾轮节）；main 领先 origin/main 未推送、CI Actions 首跑验证待 push；本地便携环境仅限开发库（trust 认证 + 固定开发密钥，严禁用于部署环境，dev-environment.md §4 有红线标注）；上轮同属联调工作的前端认证改动（stores/auth.ts is_super 归一、LoginPage 先落 token 再调 me 死循环修复、PcLayout 全局搜索/通知接线）已随本轮 feat(web) 提交收口。

## 2026-10-05 构建收口：基建阶段共享改动落实（styles/docs）

- **global.css `.sf-page` 页面进入动画**：opacity 0→1 + translateY(4px)→0，时长 `--sf-motion-normal`（180ms）+ `--sf-motion-ease`（frontend.md §25 允许「页面进入」；任务书 §56/§59）；`@media (prefers-reduced-motion: reduce)` 整段关闭兜底。fill-mode 用 backwards——动画结束回退计算值、不在元素常驻 transform，避免劫持 fixed/sticky 子孙包含块。
- **docs/frontend.md 文档收口**：§2.1 `--sf-chart-*` 清单 11→13 枚（补 `-tooltip-text`/`-legend-text`）、Border 类目补 `--sf-border-width`（1px，主题无关；chart.css:33 已在消费）；§24 补记 App.tsx ConfigProvider `controlHeight: 32`（App.tsx:39 已实现）与状态色经 `color-mix(var(--sf-*))` 自动跟随的约定。
- **阶段 4 图表 themeContract（两位并行图表工程师逐字遵守）**：① 主题信号唯一——`useThemeStore((s)=>s.mode)` 订阅驱动重渲（stores/theme.ts）；② 图表取值唯一——`getComputedStyle(document.documentElement).getPropertyValue('--sf-chart-*')`，13 枚 token Light/Dark 双块已就位（本轮 awk 断言 light=13 dark=13 集合相等）；③ 禁止读 `localStorage('sf.theme')`/matchMedia/`dataset.theme`/传 theme prop/写色值字面量——时序保证 theme.ts `setMode` 先 localStorage→applyToDocument→再 `set({mode})`，订阅者重渲时 DOM 属性与 CSS 变量必已就位。
- **App.tsx ConfigProvider 无需改动的评估结论（已核实）**：antd 6.6.5 `es/config-provider/hooks/useTheme.js:27-32` mergedComponents 按组件 key 合并，PcLayout/SfTable 嵌套主题继承算法与暗色；后续 `--sf-warning`/`--sf-primary` 等 Token 调值时 SfStatusTag（SfStatusTag.tsx:44-45）与侧边栏选中态（PcLayout.tsx:48）经 color-mix 自动跟随，无需回调。
- 验证：`cd web && npm run build` 退出码 0（门禁实测见下）。

## 2026-10-05 商品二维码闭环（SFQR 协议）——全栈实施收尾完成（docs → backend → fe-foundation → fe-center → fe-integration 五阶段交付完毕，门禁全绿）

**做什么**：为 SKU 建立协议化二维码身份并形成「生成 → 预览 → 批量打印 → 扫码识别 → 直达」闭环。协议定案 `SFQR|1|SKU|<sku_code>` 管道式 4 段（含黄金向量表、错误码、版本策略、barcodes 命名空间保留、纸张布局、data_ids 通道纪律），唯一契约 **docs/qr-code.md 已落盘（本轮 docs 阶段交付）**；printing/scanner/api/requirements/frontend/changelog 六文档增量已回写（见 changelog 同日「文档先行」条目）。**本节记录实施蓝图要点供中断恢复；协议/行为细节一律以 qr-code.md 为准，禁止重新设计。**

**不做什么（已裁决，勿翻案）**：SKU 独立详情路由页（/skus/:id，详情快捷入口由二维码抽屉承载，待 SKU 详情页立项时复用组件）；type=BIN/BOX/PALLET 落地（解析显式 SFQR_TYPE_UNSUPPORTED，待各域立项）；打印历史日期范围筛选（SfSearchForm 无 daterange 控件，单独小任务）；物理份数 DOM 倍乘（份数沿用 PrintTask 语义）；模板新增字段预设（fields.go 冻结顺序与迁移边界）；api.md 打印域全量契约补账（单独文档轮）；BARCODE 二维码 PNG 服务端预生成内容对齐 SFQR（无消费者，不改）；print_task_rows.data_id 存量回填（旧任务 fail-closed 不可自动重打，设计如此非缺陷）；BIN_LABEL 等其他标签类 SFQR 化；存量 `SFQR|` 前缀条码清洗（仅新注册被拒）；Scan/Pad 端扫码业务流程与扫码组件族（F15–F16 待环境/待立项）；引入 vitest（qrPayload.ts 约 30 行纯函数，tsc strict + 文档向量对照 + 扫码往返实测兜底，避免无必要依赖）。

**阶段归属（✅ 全部完成；各阶段开工前 git status 比对，用户未提交改动零触碰）**：

| 阶段 | 范围 | 关键产出 | 状态 |
|---|---|---|---|
| ① docs | qr-code.md 新建 + printing/scanner/api/requirements/frontend/changelog/current 增量 | 协议冻结、蓝图实施依据 | ✅ |
| ② backend | internal/devices sfqr.go 唯一解析点 / 三错误码 / ports.go SfqrSkuReader + WithSfqrSkus / handler.go nil panic fail-fast / service_device.go sfqrSkus 接线 / routes_test FailFast 同步、service_scan.go 管线第 0 段（失败不落回）、masterdata FindBySkuCode / buildBarcodes 拒 `SFQR|` / printing_content 停用拒绝 + sku_code 恒产出、printing PRINT_SKU_DISABLED 409 / PrintTaskRow.data_id / ListTasks TemplateID、迁移 000019 成对、router.go 装配 | 后端全链路 + sfqr_test.go 黄金向量表驱动测试（14+3 边界） | ✅ |
| ③ fe-foundation | web/src/utils/qrPayload.ts（构造唯一点 + parseSfqrPayload/isSfqrPayload 纯格式辅助）、QrCodeView margin 0→4 + variant='print'、PrintContentRenderer 标签 mm 布局与 PrintLabelGrid 网格组件、labelSheet.ts 网格分片（与 qr-code.md §7.3 同文）、SfQrPreviewDrawer/SfQrPrintModal、PrintPreviewPage A4/A5 分片渲染 | 协议构造与标签渲染 | ✅ |
| ④ fe-center | QrCodeCenterPage /qr-codes + 路由注册 + config/menu.tsx「商品二维码」（sku:view）+ ScanDirectCard type==='sku' 直达 /qr-codes?code= | 闭环页面与扫码直达入口 | ✅ |
| ⑤ fe-integration | api/printing.ts 类型增量（template_id/data_id）、SkuListPage「二维码」入口（操作列 [编辑][二维码][更多▾]）、PrintingCenterPage 历史模板筛选 + 重打 fail-closed（任一行缺 data_id 即中止不发请求，全部有 data_id 方 all-or-nothing 创建新任务） | 集成收口 | ✅ |

**门禁结果（实施收尾脚本两轮，全绿）**：web-build=PASS ｜ web-lint=PASS ｜ go-build=PASS ｜ go-vet=PASS ｜ go-test=PASS。文档收尾会话抽验复跑：`go test ./internal/devices -run TestParseSfqr -count=1` 黄金向量 14 条 + 3 边界全 PASS、`go test ./internal/masterdata ./internal/printing ./internal/router -count=1` 三包 ok（本会话实测）。零新增端点（swag 269 路由不变）、零新增权限码、零新增 npm 依赖。逐项细节见 docs/changelog.md 同日「商品二维码闭环（SFQR 协议）全栈实施收尾」条目。

**待环境验收项（非本轮可完成，如实挂账）**：000019 up/down 真库验证需 SF_TEST_PG_* 集成环境（默认 skip）；40×30/60×40 实纸扫码往返、A4 网格分页不截断、中文字体渲染（testing.md §6/§9 口径，阶段 16 真机项同口径）；旧任务重打 fail-closed 网络面板核验待真环境。本轮实施中的既有缺陷修复与行为变化点（gorm SKUID→sk_uid 列映射修复使主条码装配回归、停用 SKU 打印 409、QrCodeView margin 0→4）均已在 changelog 同日条目注明。

## 2026-10-05 六域图表轮收口：路由/菜单/状态注册表落实 + 图表挂账立项

**落实（六域并行图表改造的共享文件改动，收口位逐条核验后落盘）**：
- **router/index.tsx**：三域分析页接入——lazy 登记 WarehouseAnalyticsPage/OutboundAnalyticsPage/InboundAnalyticsPage（目标文件实存：views/warehouse|outbound|inbound/ *AnalyticsPage.tsx）+ 静态路由 `warehouses/analytics`（warehouses 后，无 warehouses/:id 动态段、静态嵌套无冲突）、`inbound/analytics`（inbound/new 与 inbound/:id 之间）、`outbound/analytics`（outbound 与 outbound/:id 之间，静态优先压过动态段）+ IMPLEMENTED_PATHS 增 `/inbound/analytics`、`/outbound/analytics`、`/warehouses/analytics` 三条（漏加则 placeholderRoutes 按菜单路径生成同路径占位路由永久遮蔽真页）。
- **config/menu.tsx**：① 库存分析菜单码 `inventory:analytics:view`→`inventory:stock:view`（原码资源段 analytics 后端冻结码零命中——`grep -rn "analytics:view|inventory:analytics" internal/ db/` 零输出，matchBackendPermission 按末两段永不命中→非超管恒隐身 fail-closed；数据端点挂 inventory:inventory:list，与实时库存/库存预警同码同源，注释随码入册）；② 仓储中心组新增「入库分析」「出库分析」，菜单码 `reports:report:view`（数据端点 inbound-stats/outbound-stats 挂 reports:report:read；末两段 report:view 命中冻结码 reports:report:list/read——permissions.go:343-344，全库 :report: 仅此两码——可见 ⟺ 数据可达；域码会 403、analytics 假码恒隐身）；③ 仓库中心组新增「仓库分析」，码 `inventory:stock:view`（端点 warehouse-stock 挂 inventory:inventory:list，routes.go:60）。
- **types/status.ts 补键（收口裁决：统一销售域多数派口径）**：`partial_shipped{部分发货,processing}`、`shipped_all{全部发货,success}`、`in_qc{质检中,processing}`（值域出处 internal/sales/models.go:265-276 出库单十态、internal/returns/models.go:27-36 退货八态，本会话实读核实）。销售域 salesStatusMeta 与采购退货 RETURN_STATUS_TAG 既有兜底与新键逐键同值→行为零变化；**出库域 OutboundPage 随注册表切换：PARTIAL_SHIPPED warning→processing、SHIPPED_ALL『已发货』→『全部发货』**（多数派裁决依据：采购 PARTIAL_RECEIVED、销售订单、退货映射均为 processing）。收敛随件：OutboundPage 本地兜底表两键对齐注册表防漂移、状态筛选文案『已发货』→『全部发货』与标签一致；OutboundPage/salesStatusMeta/PurchaseReturnListPage 三处「未注册」过时注释更新。
- 销售域 6 页注册表优先改造由该域自行交付（其报告：node 脚本校验 22 共享键 label/semantic 全 SAME、4 未注册键走兜底 PARITY_OK——域内自验，收口位未复跑该脚本；收口位验证以补键前后值一致性实读 + 全局构建为准）。销售域确认零路由/菜单改动：/outbound/:no 详情路由已注册（router/index.tsx OutboundDetailPage lazy + 动态段）、/sales/outbounds 菜单归属销售组未动，本轮核验属实。

**【图表挂账·库存域】两聚合端点缺（2026-10-05 库存域图表轮提出，立项后销项）**：
① SKU 库存 TOP10 排序聚合参数/专用端点；② 库存周转时序端点（现有 GET /api/reports/inventory-turnover 为快照行集，非时序）。未来立项口径参照 /inventory/analytics 先例（internal/reports/routes.go——reports 实现 + inventory 前缀挂载 + inventory:inventory:list 域列表读权限）。

**【图表挂账·采购域】三聚合端点全缺（2026-10-05 采购域图表轮核验：internal/purchase/purchase.go:40-72 全部为单据操作型端点，无聚合；internal/reports/routes.go 全部 15 端点中无采购口径——inbound-stats 为 INBOUND 流水口径非采购单口径不可挪用冒充，无按供应商分组端点，dashboard 五端点无采购状态维度）**：
① 采购金额按日聚合 ② 供应商采购排行聚合 ③ 采购订单状态占比聚合。未建 web/src/api/analytics-purchase.ts 空壳（§54）、未建分析页（§64）。未来立项口径：按 /inventory/analytics 先例（internal/reports/routes.go——reports 实现+数据域前缀挂载+域列表读权限）建 /api/purchases/analytics* 挂 purchase:purchase:list 同资源段；前端同构参照 web/src/views/inventory/AnalyticsPage.tsx 构建（蓝图原文「InboundAnalyticsPage」在采购域核验时点不存在，六域图表轮收口后 views/inbound/InboundAnalyticsPage.tsx 已落盘，同构基准仍以 inventory/AnalyticsPage.tsx 为准）；菜单挂采购中心分组（menu.tsx /purchase-center children，建议 `{ path: '/purchases/analytics', label: '采购分析', permission: 'purchase:view' }`，与组内既有条目权限码同规则）。

**门禁证据（收口位本会话实测）**：① node 程序化路由一致性检查 17 断言——PC 静态子路由无重复、三新路径菜单/IMPLEMENTED_PATHS/静态路由三处在位（9 断言）、inbound|outbound analytics 静态先于 :id 动态段（2）、placeholderRoutes 展开殿后（1）、已实现菜单路径均有静态真路由（1）、IMPLEMENTED_PATHS 均有静态路由（1）=14 PASS；lazy 声明与目标文件存在 6 断言因检查脚本拼串缺陷两次误报 FAIL，改以 `grep -n "AnalyticsPage = lazy"` 实锤（router/index.tsx:37/50/87 三行原文）+ ls 文件存在补证，脚本缺陷非代码缺陷；② `cd web && npm run build`（tsc -b && vite build）npm 自身退出码 0（built in 39.55s）。后端零改动，Go 门禁未涉及。改动未提交（无提交指令，留提交阶段）。

## 2026-10-05 P2 轮收口：零新增共享改动核实 + antd 动效接入 Token/减少动效开关（App.tsx）

- **P2 五域零新增共享改动（核实成立，收口位零动作）**：MonitorPage 复用既有 /system/monitor（menu.tsx:197 system:monitor:view + router/index.tsx:236 IMPLEMENTED_PATHS、:638 静态路由）；ReportFlowStats 为 ReportsPage 页内子视图（ReportsPage.tsx:21,75,77 经 ?report=<key> 挂载、:122 视图模式），未新增路由/菜单/资源段码；sharedChanges 三项（router/index.tsx、IMPLEMENTED_PATHS、config/menu.tsx 含库存分析菜单码修正）维持六域轮收口原状。
- **SfChart reduced-motion/Resize 完成态核实（与 P2 报告一致，零动作）**：useSfChartTheme.ts matchMedia('(prefers-reduced-motion: reduce)') 读取与 reducedMotion 出参、SfChart.tsx finalOptions reducedMotion→animate:false、ResizeObserver+forceFit 兜底、chart.css .sf-chart width:100% 全部实读吻合。
- **可选增强采纳（收口裁定，App.tsx 落实）**：antd 组件内部动效此前不经 --sf-motion-* token、reduced-motion 归零只覆盖 token 驱动层（global.css:85-98 :root 三档归零 + .sf-page；dashboard.css:20-23 KPI 卡 hover）——本收口在 ConfigProvider 映射层补齐：① `token.motionDurationFast/Mid/Slow = 0.12/0.18/0.24s` 显式对齐 --sf-motion-fast/-normal/-slow（antd 默认由 motionUnit 0.1s 派生 0.1/0.2/0.3s 与 Token 不一致；genCommonMapToken.js:11-13 派生关系、Button/Modal style 消费 motionDurationMid 均实读核实）；② `motion: !reducedMotion`（seed.motion 官方开关，seeds.d.ts:229-234「为 false 时则关闭动画」；alias.js:34-42 motion:false → 三档时长覆写 0s 且优先于显式时长）——Modal/Drawer/Menu/Tabs/Splitter 等组件内部动效在系统减少动效偏好下全站归零。reducedMotion 挂载时 matchMedia 读一次不做监听（OS 偏好极少会话中途切换，中途变更刷新生效；图表侧 useSfChartTheme 独立实时读取不受影响）；已知边界：组件内硬编码时长的关键帧（如 Spin/Skeleton 自转/闪烁）不经三档 token，不在本次覆盖面。useMemo deps 增 reducedMotion。
- 门禁：`npx eslint src/App.tsx` EXIT=0；`cd web && npm run build`（tsc -b && vite build）npm 自身退出码 0（built in 36.20s，本会话实测）。改动未提交（留提交阶段）。
- 影响范围：web/src/App.tsx、docs/tasks/current.md、docs/changelog.md。

## 2026-10-05 终局门禁修复：Math.random 红线归零 + SFQR 后端独立成 commit 落库

- **Math.random 检查（views/components 真实数据红线）**：终局报告三处命中为陈旧快照——修复时点前域内已自行改毕（DashboardPage.tsx:73/InboundAnalyticsPage.tsx:50 注释改「禁止随机数伪造」措辞、PadReceivePage.tsx newIdempotencyKey 幂等键改 crypto.randomUUID→crypto.getRandomValues→时间戳+会话内计数器三级回退）。收口位复跑门禁同款检查：`grep -rn "Math.random" web/src/views web/src/components` 零命中、全 `web/src` 亦零命中。
- **后端未提交改动（26 文件：23 M + 6 未跟踪）**：逐项核实纯 SFQR 交付——diff 内 SFQR/data_id/PRINT_SKU_DISABLED/WithSfqrSkus 标记 108 处、UI 轮标记（dashboard/theme/sf-page/analytics）0 处，零回退；提交前实测后端门禁三连 `go build ./... && go vet ./... && go test ./...` 全绿（24 包 ok、零 FAIL）。按既有裁决独立成 commit：**50b56ad** `feat(server): SFQR 二维码闭环后端全链路——协议解析/打印停用拒绝/000019 data_id/路由装配`（29 文件，+765/−47；git-commit-standard 规范、仅暂存 internal/ + db/migrations/，docs/web 未混入）。提交后 `git status` 后端全净（internal/ db/ 零残留）。web/ 与 docs/ 在途改动属 UI 升级交付，未在本 ask 范围、保持未提交留待其归属阶段。

## 2026-10-05 进行中：全局表格设计系统统一（docs/plans/2026-10-05-table-design-system.md）

- 任务：全站表格 UI/UX 系统级统一（完善 SfTable 体系，不换栈不改业务）。
- 状态：T1 Token 层 → T2 SfTable（nested variant + 首载骨架）→ T3 SfSearchForm 筛选提示 → T4 cells.tsx + 时间格式统一 → T5 十个裸 Table 迁 nested → T6 操作列收敛（并行）→ T7 lint/build/浏览器四宽度 → T8 文档与报告。
- 注意：web/ 与 docs/ 在途未提交改动（二维码前端轮 + 主题轮）只增量编辑、禁止回退；后端 SFQR 已以 50b56ad 落库与本任务无关。
- 收口位复核（T5 迁移期间）：typecheck 门禁曾瞬态红——OutboundDetailPage（8 错：SfTable 未导入 + Table/SfEmpty 残留）→ ImportWizardPage → FileCenterPage/RoleListPage/WarehouseListPage/BinListPage 逐文件在途，错误集随迁移单调推进；收口位未触碰任何在途文件（两轮重试循环跟踪），随迁移收敛终局双绿：`npm run typecheck`（tsc -b）退出码 0、`npm run build` 退出码 0（built in 52.59s）。OutboundDetailPage 8 错由迁移位自行接线修复（收口首探后 45s 内消失），非收口位改动。

## 2026-10-05 设计规范检查员五项修复：logo Token 化收敛 + 通知/视图标签去 antd 预设色 + 页面背景回归任务书 §6

- **①② logo 三拷贝收敛（SfLogo 新组件，components/common/SfLogo.tsx）**：LoginPage/PcLayout/ModulePlaceholder 三份同款 SVG 收敛为一份实现（frontend.md §26.1），primary 变体底 `var(--sf-primary)` + 描边 **新增 token `--sf-on-primary: #ffffff`**（tokens.css Light/Dark 两块同声明，AGENTS.md 规则 3 硬编码 stroke="#fff" 清零）；muted 变体保留 ModulePlaceholder 原有 `--sf-border`/`--sf-text-muted` 占位写法。
- **③ NotificationDrawer 通知类型色**：antd 预设色表（blue/orange/gold/red/cyan/default）改为 `NOTIFICATION_TYPE_SEMANTIC` 语义映射（APPROVAL/TASK=processing、STOCK_ALERT/EXPIRY_ALERT=warning、EXCEPTION=danger、SYSTEM=neutral、未知=neutral），裸 `<Tag>` 改经 **SfStatusTag**（label 直传 item.title，语义色经 --sf-* Token color-mix 派生，AGENTS.md 规则 5）。
- **④ DashboardPage 视图徽标**：`geekblue`/`green` 预设色改 Token 派生——`viewAccent`（管理层=`var(--sf-primary)`/仓库人员=`var(--sf-success)`）+ SfStatusTag 同款 color-mix 12%/24% 公式；非业务状态不经 SfStatusTag 语义集（其无 primary 语义），色值仍全走 Token。
- **⑤ 页面背景回归任务书 §6**：`--sf-bg` 由 UI 升级轮引入的 `#ffffff` 恢复 **`#f6f7f9`**（页面/Surface 两级分层），App.tsx `colorBgLayout` 映射副本同 commit 同步；Dark 侧 #101418 不动。pad.css 等引用 `var(--sf-bg)` 自动跟随。
- 门禁（收口位实测）：`npx eslint`（7 个触及文件）EXIT=0；`npm run build` npm 自身退出码 **0**（built in 41.52s）；复扫 `stroke="#` 全 src 零命中、antd 预设色词零命中。过程备注：全量构建曾三次被并行 masterdata 域在途文件阻塞（ProductListPage→SupplierListPage 的 SfConfirm 接线），坚持不触碰他人在途文件、以「错误文件清单单调收敛」+ 行号两轮不变后再变为其完成信号，终局随其接线完成转绿——设计修复文件本身从未出现在错误清单。改动未提交（留提交阶段）。
- 影响范围：web/src/components/common/SfLogo.tsx（新增）、web/src/views/login/LoginPage.tsx、web/src/layouts/PcLayout.tsx、web/src/views/placeholder/ModulePlaceholder.tsx、web/src/layouts/NotificationDrawer.tsx、web/src/views/dashboard/DashboardPage.tsx、web/src/styles/tokens.css、web/src/App.tsx、docs/tasks/current.md、docs/changelog.md。

## 2026-10-05 已完成：全局表格设计系统统一（changelog 同日条目）

- T1–T8 全部落地：表格 Token（--sf-table-*，Light/Dark）、SfTable nested variant + 首载骨架、SfSearchForm 筛选状态提示、统一 Cell（NumberCell/CodeCell/ProductCell/DateCell）、formatDateTime 分钟精度、10 文件 13 表裸 antd Table 清零、操作列收敛（文件中心/角色/部门/仓库/库位 + masterdata 4 页；删除一律入「更多」声明式 Modal 确认）。
- 门禁：typecheck/lint(0 错误)/build 全绿；浏览器实测 Light 1680 / Dark / 1024×768（商品管理/文件中心/仓库管理）。
- 遗留（如实挂账）：① 库存/销售/采购/系统等其余列表页的 Cell 级换装（CodeCell/DateCell 替换手写 render）未逐页做完——契约已由组件层统一兜底，逐页精修留下一轮；② 列设置拖拽排序维持既有裁定不做（显示/隐藏已覆盖任务书 §27）；③ 实机 Pad 触控验收待环境。
- 并行会话注意：同期另一会话完成「设计规范检查员五项」（SfLogo/通知色/背景色回归 #F6F7F9），两轮改动在同工作区叠加，提交时按归属拆分。

## 2026-10-05 独立设计复审两项处置：§18 对齐兜底进 SfTable + 采购/销售分析范围差如实挂账

- **② §18 对齐规则未落地（已修复，组件层中央兜底）**：复审实证 29 文件操作列（title「操作」）±4 行窗口 align 0 命中、状态列普遍左对齐，而数字列 align:'right' 已 142 处——契约缺口在「SfTable 注释自称由页面声明」与页面实际行为之间。修复：SfTable.tsx 新增 `applyAlignDefaults`（visibleColumns 链路内生效）——**未显式声明 align 时**按列语义补默认：操作列（title「操作」或 dataIndex 'operation'）→ right；状态列（title 以「状态」结尾或 dataIndex 以 status 结尾，如 status/occupancy_status/qc_status）→ center；**页面显式 align 永不覆盖**。T5 迁移已把裸 Table 清零（views 全域 `<Table` 计数 0），故中央兜底覆盖全部列表页，无需 29 文件逐页改。抽查命中实证：SkuListPage.tsx:384-387 操作列、ProductListPage.tsx:330-334 状态列均无显式 align、均落兜底。浏览器渲染实测未执行（无浏览器自动化工具链，与既往口径一致）。
- **① 采购分析/销售分析页面缺失（范围差，如实上报挂账，不建假页）**：复核属实——`find web/src/views -name '*Analytics*'` 仅 4 页（inbound/inventory/outbound/warehouse），menu.tsx 采购/销售中心组无 analytics 项，router 仅 4 条分析路由；frontend.md §27 交付清单亦不含二者。**不做假页**（采购三聚合端点已挂账缺位、销售口径端点经 grep 复核 internal/reports/routes.go 零命中——建页即整页错误态或假数据，违 requirements.md §10）。挂账口径：待任务书 §42/§43 立项后按 /inventory/analytics 先例（reports 实现 + 数据域前缀挂载 + 域列表读权限）补端点，前端同构 AnalyticsPage、菜单挂采购/销售中心组（权限码立项时按入库/出库分析先例裁决）；销售域需先补后端销售口径聚合端点（sales/orders/returns 维度）。
- 门禁：`npx eslint src/components/table/SfTable.tsx` EXIT=0；`npm run typecheck`（tsc -b）退出码 0；`npm run build` 退出码 0（built in 45.39s）。改动未提交（留提交阶段）。
- 影响范围：web/src/components/table/SfTable.tsx、docs/tasks/current.md、docs/changelog.md。

## 2026-10-05 终局提交轮：全站 UI/UX/ECharts 升级交付落库（feat(web)×4 + docs×1，门禁全绿）

- **范围**：同工作区叠加的多股前端交付按归属拆分落库——① Token/主题基座（tokens.css 扩容含 --sf-chart-* 13 枚/--sf-motion-*/--sf-on-primary/--sf-table-*、主色 #2563eb、背景回归 #f6f7f9、index.html 防 FOUC 首帧预置、App.tsx ConfigProvider 全量映射 + motion:!reducedMotion）；② SfChart 家族基建（components/charts/ 13 文件 + chart.css，themeContract 三红线）；③ Dashboard 范式重做与六域分析图（MetricStrip→KpiCards/Movements、warehouse/inbound/outbound 三新分析页、ReportFlowStats/MonitorPage 图表化、菜单码归一、status.ts 补键）；④ 全局表格设计系统（SfTable nested/骨架/applyAlignDefaults、统一 Cell、formatDateTime 分钟精度、23 页换装、操作列收敛）；⑤ SFQR 前端闭环（配套 50b56ad 后端：qrPayload/标签网格打印/二维码中心/扫码直达/重打 fail-closed + qr-code.md 等契约文档）；⑥ 设计规范五项（SfLogo 收敛/通知与视图标签去预设色/背景回归）与 P2 收口（App.tsx 动效 Token、PadReceivePage 幂等键 crypto 化红线修复）。
- **提交拆分（5 枚，git-commit-standard 规范；共享文件 router/menu/SkuListPage/frontend.md 为多轮叠加改动，随主导意图落库并在提交体注明）**：feat(web) 设计系统基座（Token/主题/SfChart 家族/统一表格组件）→ feat(web) SFQR 前端闭环（+ 契约文档四件）→ feat(web) Dashboard 范式与六域分析图（router/menu/status 随此提交，含 /qr-codes 登记）→ feat(web) 表格设计系统全站落地 → docs(changelog+tasks+frontend) 本轮终局记录。后端 internal/db/cmd 零未提交改动（SFQR 后端已以 50b56ad 独立落库），web/ 与 docs/ 之外无触及。
- **门禁（交付提交工程师本会话实测，工作区终态含并行会话收尾的 SfInventorySummary/StockListPage）**：后端 `go build ./... && go vet ./... && go vet -tags integration ./... && go test -count=1 ./...` 退出码 0（24 包 ok 零 FAIL）；前端 `npm run lint` 0 错误（1 条 PadReceivePage.tsx:349 exhaustive-deps 存量警告）、`npm run build`（tsc -b && vite build）退出码 0（built in 37.92s）；复扫 `grep -rn "Math.random" web/src` 零命中。终局门禁全绿。
- **真实空态清单（随 changelog 同日终局条目入册）**：库存分析 TOP10 与周转趋势、仓库分析作业量、入/出库分析待端点位、出库分析完成率与商品排行、ReportFlowStats 超限降级、MonitorPage 队列未注入、Dashboard 预警空态、SfChart 内核空数据统一 SfEmpty（Sparkline 等高占位）——零假数据零随机数。
- **遗留（非本轮可完成，维持既有挂账）**：采购/销售分析页与库存 TOP10/周转时序、采购三聚合端点（见「六域图表轮收口」挂账节）；其余列表页 Cell 级换装逐页精修、列设置拖拽维持不做、实机 Pad 触控验收待环境（见表格轮遗留）；浏览器渲染实测本轮未新增自动化截图（沿用既有记录点位）；main 领先 origin/main 未推送（本 ask 明确不推送）。
