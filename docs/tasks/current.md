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

## 下一步（按 frontend.md §29 顺序）

1. **F6 库存中心全量页面**：✅ 批次库存、序列号、库存转移、库存追溯（第二批）+ ✅ 库存分析、SKU 库存详情（2026-10-02 第三批，三个新端点为前端先行契约待后端 M1/M2 冻结回对）；库存盘点按既有裁决由盘点中心 /counts 承载
2. **F7 单据详情**：✅ 入库单/出库单/采购订单三详情页已交付（2026-10-02 第三批，SfDetailHeader/SfTimeline 新组件；详情端点为前端先行契约）；行级状态列、打印/导出真实动作待后端单据域契约冻结后补
3. **F8 任务中心**：✅ 我的工作台、我的任务已交付（2026-10-02 第二批，前端先行契约待后端任务域对齐）；「我的待办」「我的审批」入口待审批/待办菜单（如 /approvals）落地后补 path
4. **后端阶段 3–8**（仓库根 Go module）：✅ 迁移（T1）→ ✅ **认证权限（T2，2026-10-02 交付：/api/auth 全套 + /users、/roles、/permissions、/departments 管理 API，JWT+Redis 会话双轨、登录保护、RBAC/数据权限助手、敏感操作审计，详见 changelog 当日条目）** → ✅ **基础资料（T3）/ 仓库（T4）/ 库存核心（T5），2026-10-02 交付（三域 62 条路由 + 九个库存变更原语，详见 changelog 当日三条交付记录）**；T6 补 router 跨域 Checker 注入与 swag 汇总（实现时需核对前端先行契约，两批清单均见 changelog 2026-10-02 集成记录；注意 /api/inventory 静态段须先于 {skuCode} 参数段注册）
5. ✅ F9 盘点、F11 Excel 导入导出（2026-10-02 第三批交付；/data/printing 打印中心、/data/files 文件中心仍为占位）；剩 F12 打印、F13 设备、F14 Pad、F15–F16 Scan（库位地图已交付）

## 关键上下文

- **后端 auth 域已可用**（需 PG15+Redis7、迁移后启动；运行前按 docs/deployment.md §1.1 环境变量清单注入——SF_AUTH_JWT_SECRET、SF_ADMIN_INITIAL_PASSWORD（空库首启）、SF_SERVER_MODE=release（2026-10-03 安全修复轮起默认 release，本地开发显式设 debug；release 缺 JWT 密钥启动失败））。登录响应：`{access_token, token_type:"Bearer", expires_in, refresh_token, must_change_password, user}`；/api/auth/me 返回 `permissions` 权限点集（编码见 internal/auth/permissions.go）。
- **前端 auth.ts 对齐清单**（后端契约已定，前端适配）——✅ **已完成（2026-10-02 并行轮认证域组交付，集成轮补记）**：`token→access_token`、`refreshToken→refresh_token`、`oldPassword/newPassword→old_password/new_password`、会话字段 `id→session_id、userAgent→user_agent、createdAt→login_at`、GET /api/auth/sessions 为分页信封（items 取数组）；另含 MeResult 会话组装、must_change_password 强改闭环（403 AUTH_PASSWORD_CHANGE_REQUIRED 特判 + 不可关闭强改 Modal）、PASSWORD_RULE 与后端 password.go 对齐导出、canAccess fail-closed（空权限不放行）与菜单码归一映射（types/permission.ts RESOURCE_ALIASES）。~~遗留：api/user.ts（用户管理域 camelCase 自有类型）对齐后端 UserView JSON tag~~ ✅ **已完成（2026-10-03 契约对齐轮组3 交付，集成轮销项）**：UserView 全量 snake_case + department_id/role_ids 请求体 number 形态，见 changelog 同日条目；localStorage `sf.auth` 旧格式会话被拒绝，联调发布需重登一次。
- ~~**待办（scope B）**：000001 迁移缺 uk_roles_code/uk_permissions_code/uk_departments_code 唯一索引~~ ✅ 已修复（2026-10-02 复核：up 补齐三表 code 唯一索引 + permissions/departments parent_id 反查索引，down 原引用即对齐；权限点同轮补录 inventory:batch/serial 至 106 个，见 changelog 复核修复条目）。
- **待办（scope A/T6）**：router 注入 `auth.WithWarehouseChecker(warehouse.NewChecker(db))`（plan §4.3 规则①真实装配 fail-fast）与 swag 汇总、Makefile swag 目标。
- 后端 T3/T4/T5 三域已交付（2026-10-02）；M2 业务单据域（入库/出库/盘点等）未交付：相关作业页面为统一错误态（预期行为）；DEV 旁路 `VITE_AUTH_BYPASS=1` 仅开发构建。
- 权限点模型已定义（types/permission.ts）→ auth 域交付后 /api/auth/me 的 permissions 即为菜单/按钮过滤真实数据源。
