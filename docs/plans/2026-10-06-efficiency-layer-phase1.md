# StockFlow 作业效率提升层一期实施计划

> 日期：2026-10-06 ｜ 类型：专项批次计划（docs/plans/ 命名规范：YYYY-MM-DD-主题.md）
> 目标：仓库人员日常 500 次操作降到 300 次左右；十项能力 Frontend+API+Service+Database 全栈真实落地，一次成环，不留「后端以后再做」。
> 本计划的行号/路径均为 2026-10-06 本会话实读或实测（命令与结果见 §1.2）；开工前如有并发会话改动，以重新实测为准。

---

## 0. 三路调研结论采纳声明

本计划基于三路调研（backend/frontend/docs）制定，关键事实已在本会话二次核验：

- 迁移最新编号 **000019_add_print_task_row_data_id**（`ls db/migrations` 实测 19 对成对文件），下一个可用编号 **000020**。
- `/api/tasks` 现状：`internal/reports/workbench.go:740-922`，UNION ALL 三任务表、assignee 恒=当前用户、统一五值 status、权限挂 `auth.PermInventoryList`（workbench.go:22-34 有决策注释）；`validTaskType`（workbench.go:861-865）冻结 `putaway|picking|checking` 三值，packing/moving/counting 无独立任务表传值 400——本计划的能力 4 沿用同口径扩展。
- 三任务表均**无 priority 列**：`db/migrations/000007_create_purchase_tables.up.sql:184-206`（putaway_tasks，claimed_by/target_warehouse_id，状态 PENDING/IN_PROGRESS/PAUSED/COMPLETED/CANCELLED）、`000008_create_sales_tables.up.sql:164-190`（pick_tasks，assignee_id/warehouse_id，六态）、`:207-230`（check_tasks，assignee_id/warehouse_id，三态）。
- 审计面：`operation_logs`（`000002_create_audit_log_tables.up.sql:11-30`，含 user_id/module/object_type/object_id/action/success/error_code/request_id），写入点 `internal/middleware/audit.go:33-146`（业务同事务、append-only）。
- datax 行级结构：`import_task_rows`（`000011_create_datax_tables.up.sql:54-70`，raw/parsed jsonb + status RAW/VALID/INVALID/QUEUED/SUCCESS/FAILED）；错误 Excel 已是「原数据+错误原因列」（`internal/datax/excel.go:210-213` buildErrorWorkbook），下载端点已有（`internal/datax/handler.go:114,280-285`）。
- 打印任务创建 `POST /api/prints/tasks` 现为整体语义（`internal/printing/handler.go:370-382` 返回单个 view；停用 SKU 走 409 PRINT_SKU_DISABLED + details.disabled_ids）——**无逐条结果**，能力 7 需语义演进（§2.7）。
- 幂等现状：`Idempotency-Key` 头优先已在 purchase（`internal/purchase/handler.go:489-502`）与 sales（`internal/sales/handler.go:94-96`）落地；inventory_ledgers.idempotency_key 唯一索引兜底（api.md §7）。
- 前端：PcLayout Header 右区菜单名 AutoComplete（`web/src/layouts/PcLayout.tsx:307-323`）；usePagedList 只持久化分页（`web/src/hooks/usePagedList.ts:14-19`）；SfTable storageKey 已持久化 density + hidden-columns 到 localStorage（`web/src/components/table/SfTable.tsx:176-180,370-383`）；web 无测试脚本（`node -e require('web/package.json')` 实测 scripts 仅 dev/build/lint/typecheck/preview）。
- 权限冻结清单 `internal/auth/permissions.go`（370 行全量实读）：本计划仅新增 3 个权限码，动词 assign 在 M2 冻结枚举内（permissions.go:131-132 注释）；「个人收件箱认证即可用，无权限点」先例（permissions.go:295-296 notifications 注释）是 /api/user/* 端点的权限口径依据。
- seed 双清单：`internal/database/seed.go:92`（permResources）、`:160`（menuSeeds），新增权限码必须与 `seed_test.go` 字面清单同源。

### 1.2 门禁基线（漂移轨迹——本节仅作证据，**不作 W0 行动清单**）

工作树存在并行会话实时改动，四轮实测结果各不相同即为证据，任何静态文件清单都会迅速失真：

| 轮次 | 后端 `go build/vet/test` | 前端 `typecheck/lint` | 工作树 `git status --porcelain` |
|---|---|---|---|
| ① 计划撰写时 | build 通过；vet EXIT=1；test FAIL 五包（purchase/auth/masterdata/sales/datax） | 未跑 | 未跟踪 11 个测试文件 |
| ② 第一次评审修订时 | build 通过；vet EXIT=0；test FAIL 仅 internal/stockops | 未跑 | 未跟踪清单再变（新增 inventory/fakedb_test.go 等）；已修改扩至 menu/PcLayout/masterdata 六页等约 19 个 |
| ③ 第二次评审轮（评审方实跑） | vet 数秒后 devices 又现新错（在途实时写入）；test FAIL internal/devices [build failed]+23 包 ok | typecheck 3 错、lint 3 errors（PackingPage/PickingPage/ShipmentPage TS6133） | 未跟踪测试文件 20 个 |
| ④ 本轮修订时实跑 | **build/vet/test 全绿（EXIT=0×3）** | **typecheck EXIT=0、lint EXIT=0（0 错误 2 警告）** | 未跟踪 25 项（其中 21 个测试文件 + web/src/hooks/useCrudPermissions.ts 等新文件）；outbound 五页在途修改 |

**W0 作业定义（以此为准，取代一切文件清单）**：开工时当场执行 `git status --porcelain` + `go build ./... && go vet ./... && go test ./...`（web 侧 `npm run typecheck && npm run lint`），**按包逐个清点**在途未跟踪/已修改文件——同包测试一起编译，他人半成品会卡死该包自身门禁（后端 reports/stockops/printing/datax/devices 为在途测试活跃区，恰为 B3/B4 归属包；web 侧 usePagedList.ts/SfSearchForm.tsx/StockListPage.tsx/outbound 五页为 urlSync 改造活跃区，见 §2.2 B5 裁决）；与作者协调——已被作者修绿的文件原样保留，仍在途的等待或协调收敛，禁止删除他人工作；门禁全绿后进入 B/F 波次，当场快照记入 docs/tasks/current.md。

---

## 1. 范围与非目标

### 1.1 范围

十项能力（全局搜索/保存视图/用户偏好/自动下一条/最近操作与访问/上下文导航/批量结果中心/Excel 失败行修复/快捷键/幂等防护）+ 附加项（工作台「我现在该做什么」、列表页效率操作、当前视图导出、任务页自动刷新、扫码三模式、紧凑 UI）全栈落地。

### 1.2 明确不做（防 scope 失控，逐条有理由）

| 不做 | 理由 |
|---|---|
| ScannerManager 完整 HID 流架构（scanner.md §3.1 全量事件总线） | 一期交付 ScanInput 组件族（USB/蓝牙 HID 枪=键盘输入即用），完整架构属 F15/F16 立项 |
| 表格分页大小/密度的服务端同步 | SfTable storageKey（localStorage）已内建（SfTable.tsx:176-180,370-383），为单点偏好新建服务端体系违反任务要求「不为单点偏好新建重复体系」 |
| 通知实时通道（WebSocket/SSE） | 与十项能力无关，现有 30s 轮询维持 |
| counting/packing/moving 的自动下一条映射 | 无独立任务表（workbench.go:861-865 既有裁决），沿袭 400 invalidParam；counting 走盘点单自身流转 |
| 导出当前视图的「排序/列」维度 | 16 个导出行源列固定（internal/datax contract），一期严格透传筛选+仓库，排序/列口径在 api.md 披露（风险 §8.7） |
| 任务优先级管理页面 | 一期交付列+排序+API+行内设置入口，不做独立管理页 |
| 新的第二套审计/搜索/偏好/批量体系 | 铁律：复用 operation_logs、复用 SfTable/SfToolbar/SfBatchBar、单一 user_preferences 表 |
| 更换技术栈、改 tokens.css 既有值、大范围重构稳定模块 | AGENTS.md 硬性规则 |

---

## 2. 十项能力逐项决定

### 2.1 全局业务搜索（Ctrl/Cmd+K）

- **复用**：统一信封与分页契约（docs/api.md §2）；权限逐 type 映射 `internal/auth/permissions.go` 既有读码；数据权限 `scopeOf` 会话仓库快照（workbench.go:33 同口径）；SfStatusTag 状态展示；`web/src/api/client.ts` 统一请求。
- **新建（后端）**：新包 `internal/search/`（doc.go / handler.go / repository.go / search_test.go）——只读跨域 SQL 直查（workbench.go:29 「平台包禁 import 各域包、SQL 直连」同口径），**零写语句**。
  - `GET /api/search?q=&types=&warehouse_id=&limit=`：按 type 分组返回 `{groups:[{type,title,count,items:[{id,type,title,code,status,summary,updated_at}]}]}`；跳转 path 由前端映射（后端不编码前端路由）。
  - type→表/列/权限映射（列名已按 DDL 核验）：`sku`→skus(code,name)@masterdata:sku:list；`product`→products(code,name)@masterdata:product:list；`barcode`→barcodes(barcode)@masterdata:sku:list；`batch`→batches(batch_no)@inventory:batch:list；`serial`→serial_numbers(serial_no)@inventory:serial:list；`bin`→bins(code)@warehouse:bin:list；`warehouse`→warehouses(code,name)@warehouse:warehouse:list；`customer`→customers(code,name)@masterdata:customer:list；`supplier`→suppliers(code,name)@masterdata:supplier:list；`doc`→purchase_orders.po_no+inbound_orders.inbound_no+sales_orders.so_no+outbound_orders.outbound_no+transfer_orders.transfer_no+count_orders.count_no+exceptions.exception_no（各自域 list 码）；`logistics`→shipments（单号列名以 000008 DDL 为准，shipment_no/outbound_no；若有物流单号列则一并，实现者核验）。
  - 端点级**只挂认证**（auth.AuthRequired 保护组），组内按用户权限集逐 type 过滤（无对应 list 码的 type 不查询不出组）；若 auth 包无编程式判定 helper，则在 internal/auth 新增导出 `HasPermission(c, code) bool`——语义必须与 RequirePermission 完全同构：`uc.IsSuper || 权限快照含 code`（超管直通是 RequirePermission 语义的一部分，internal/auth/middleware.go:126-128；遗漏则超管在全局搜索看不到任何分组），数据源同 RequirePermission 的权限快照，零语义新增。
  - `q` 长度上限 64（超长 400 invalidParam，防超长 ILIKE 与日志噪音）；空查询/长度<2 直接返回 `{groups:[]}`，不做任何 SQL；`limit` 为每组条数（缺省 5，上限 20）；`warehouse_id` 为**收窄过滤器**：与 scopeOf 求交，越界仓库按该 type 无结果处理（不 403，防范围探测）。
- **新建（前端）**：`web/src/api/search.ts`（searchApi.search）；`web/src/components/search/GlobalSearchModal.tsx`（Modal 命令面板：防抖 300ms、类型分组图标+SfStatusTag、↑↓/Enter/Esc/Tab 键盘导航、点击直达）；`web/src/config/searchTargets.ts`（type+payload→路由映射，含权限码校验，无权限项置灰）；挂载点 PcLayout Header（替换 PcLayout.tsx:307-323 菜名 AutoComplete 为「搜索按钮+输入框」，菜单名搜索降级为 GlobalSearch 的『页面』分组，候选源仍走 MENU_TREE+canAccess）。
- **不做**：拼音/模糊语义搜索、跨字段权重排序（ILIKE 前缀优先+精确匹配置顶即可）、搜索历史服务端存储。

### 2.2 保存视图

- **复用**：SfSearchForm cleanValues 产物（filters）、SfTable hidden-columns localStorage 结构（columns_json 形态=hidden 列键数组，对齐 SfTable.tsx:179-180）、usePagedList params/pagination。
- **新建（后端）**：表 `user_saved_views`（迁移 000020，§3.1）+ 新包 `internal/userpref/`（与 2.3 共包）：
  - `GET /api/user/views?page_key=`、`POST /api/user/views`、`PUT /api/user/views/:id`（改名/改内容/is_default）、`DELETE /api/user/views/:id`。
  - **严格按用户隔离**：全部查询强制 `user_id=当前用户`；PUT/DELETE 先按 (id,user_id) 查归属，非本人一律 404（防探测）。
  - `is_default` 部分唯一索引保证每 (user_id,page_key) 至多一个默认；服务层「设为默认」在单事务内清除旧默认；「恢复默认」= `PUT is_default=false`。
  - page_key 服务端正则校验 `^[a-z0-9._-]{1,64}$`；三个 json 列校验合法 JSON 且各 ≤8KB；name ≤64 字符且 (user_id,page_key,name) 唯一（重复名 409）。
  - 权限：认证即可（notifications §10.6 先例，permissions.go:295-296 注释），无权限点。
- **新建（前端）与 urlSync 双形态裁决（B5；前提已实测：在途改造 usePagedList.ts +149 行——`urlSync?:boolean`/`params?` 可选/applyFilters API/URL 读写，StockListPage.tsx 已删 useState+persistKey 改 `urlSync:true`，SfSearchForm.tsx +43 行 initialValues 回填）**：
  - `web/src/hooks/useSavedViews.ts`（TanStack Query CRUD + 失效）；`web/src/components/table/SfViewBar.tsx`（视图下拉：应用/保存当前/重命名/删除/设为默认/恢复默认；放 SfToolbar extra 区）。
  - **视图应用一律经 usePagedList 公开 API，禁止绕过 hook 直改 URL 或页面 state**。双形态：urlSync 页（URL 为筛选/分页唯一事实源）→ 视图应用=调 applyFilters/分页 API 把 filters_json/sort/page_size **展开写入 URL query**（刷新/分享/前进后退天然还原）；非 urlSync 旧形态页 → 置 params+pagination（原设计）。
  - **URL 参数为最终事实源**：`view=<id>` 仅作当前视图名标记，用户改动任一筛选即清除标记（状态变自定义）；刷新以 URL 展开参数还原，不重新拉视图定义渲染（避免首屏依赖网络）。
  - **F2/F3 均不修改 usePagedList.ts 与 SfSearchForm.tsx**——二文件属在途 urlSync 改造（W0 协调面），其完成态 API（applyFilters 签名/URL 编码格式/initialValues 回填）由 W0 与作者确认后冻结为消费契约；SfViewBar 以 prop `mode:'url'|'state'` 声明形态，urlSync 覆盖面扩大时逐页自动升级。
- **列/排序的保存与应用边界（B6 裁决；应用路径断裂已实核：SfTable hiddenKeys 为内部 useState（SfTable.tsx:179）仅经 storageKey 写 localStorage，props 面仅 showColumnSetting（:104）无受控列 API；usePagedList 公开面无 sort/columns；PageQuery 仅 page/pageSize（types/api.ts:22-25））**：
  - ① `columns_json`=hidden 列键数组，应用路径=F2 给 SfTable 新增受控 props `hiddenColumns?: string[]` + `onHiddenColumnsChange?: (hidden: string[]) => void`（**受控优先/非受控回退**：不传新 props 的既有消费页零行为变化；视图应用=页面将视图的 hidden 集经受控 prop 生效，不写 localStorage 以免覆盖用户列偏好；用户手动改列=onChange 回传+storageKey 照旧持久化）。
  - ② `sort_json`：schema 保留（000020 已含列，避免未来二次迁移）但 SfViewBar 一期**不采集（恒 '{}'）**，api.md/frontend.md 披露为排序通道预留——PageQuery 无 sort 字段、SfTable 排序为 antd 非受控，服务端排序通道超一期范围，不采集即不构成半交付。
  - ③ 列序（拖拽排序）维持既有裁定不做（current.md 表格轮遗留②），api.md/frontend.md 同点披露。
- **接入页面清单**（F3 波次执行，均已有 SfTable+SfSearchForm）：StockListPage、LedgerPage、BatchListPage、SerialListPage、PurchaseListPage、ReceiptListPage、InboundPage、SalesOrderListPage、SalesOutboundListPage、TransferListPage、CountTaskListPage、ExceptionCenterPage、PickPage/CheckPage（PickingPage/CheckingPage）。
- **不做**：视图共享/角色默认视图（个人视图即止）、视图导入导出。

### 2.3 用户偏好

- **复用**：SfTable storageKey（密度/列设置/每表分页大小，前端本地，见 §1.2 不做表第 2 行）；`['options','warehouses']` 既有选项接口（偏好设置里的默认仓库下拉）。
- **新建（后端）**：表 `user_preferences`（迁移 000021，§3.2）+ internal/userpref 包：
  - `GET /api/user/preferences?keys=a,b`（缺省全部）、`PUT /api/user/preferences/:key`。
  - **key 服务端白名单**（Go 常量表，api.md 冻结）：`default_warehouse_id`、`last_warehouse_id`、`last_business_type`、`last_printer_id`、`last_print_template_id`、`recent_visits`、`recent_filters`。白名单外 400 invalidParam；value 为 jsonb，单值 ≤16KB（recent_visits 由服务端裁剪至 20 条）。
  - 不挂 Audit 中间件（recent_visits 高频写会刷屏审计日志，operation_logs 只记业务动作——在 api.md 披露）。
- **新建（前端）**：`web/src/hooks/usePreferences.ts`（读+防抖写，localStorage 先行渲染防闪变）；偏好设置入口=PC 用户菜单（PcLayout Dropdown，原 :340 一带，行号随在途改动漂移以实测为准）新增『偏好设置』项 → `web/src/components/common/SfPreferenceDrawer.tsx`（默认仓库/清除最近数据，可修改即任务要求的「个人设置可修改」）；最近访问写入器并入 usePreferences（路由变化防抖 3s 写 `recent_visits`，上限 20 条 {path,title,visited_at}）。
- **不做**：偏好版本迁移、多 profile。

### 2.4 完成后自动下一条

- **复用**：`/api/tasks` 的 UNION ALL 三分支**结构**（字段映射与五值映射，workbench.go:742-778；候选池 WHERE 必须按 B7 改造，见下）、scopeOf、`auth.PermInventoryList`、sysops `task.timeout.*` 配置（`internal/sysops/task_timeout.go` 的读法）、前端任务领取既有 API（api/putaway.ts:81-82 claim、sales picks/checks claim）。
- **新建（后端）**：internal/reports/workbench.go 增 `GET /api/tasks/next?task_type=&warehouse_id=&current_task_id=`：
  - task_type 白名单扩展为 `putaway|picking|checking|receipt|exception`（前三者**改造复用**三分支；`receipt`→inbound_orders 状态 `RECEIVING` 候选池（000007:96 状态 CHECK），`exception`→exceptions 候选池（000010:77-105，status OPEN/处理中）；其余 400 invalidParam 沿 validTaskType 裁决注释扩展）。
  - **候选池（B7 裁决——/api/tasks 三分支 WHERE 硬过滤 assignee=当前用户（workbench.go:756/767/778 本轮实测；行号随在途漂移），「原样复用」会使池内只有本人任务、排序首层恒真、未领取的任务 002 永不可达、场景 3 必挂）**：三分支仅复用字段映射与五值映射，WHERE 必须改造为 `(本人已领取且进行中) OR (PENDING 未领取)`——putaway：`(pt.claimed_by=me AND pt.status IN ('IN_PROGRESS','PAUSED')) OR (pt.claimed_by=0 AND pt.status='PENDING')`；picking：`(pk.assignee_id=me AND pk.status IN ('CLAIMED','PICKING')) OR (pk.assignee_id=0 AND pk.status='PENDING')`；checking：check_tasks 无 CLAIMED 态（000008:229 三态），`= (ck.assignee_id=me AND ck.status='PENDING') OR (ck.assignee_id=0 AND ck.status='PENDING')`。receipt 候选=inbound_orders status='RECEIVING'；exception 候选=status OPEN 或处理中。恒排除 current_task_id。
  - 排序（真实 SQL ORDER BY，**严禁前端推算**）：`本人已领取(进行中) DESC > 其余候选按 priority DESC > 超时 > created_at ASC`。**超时层适用面（一期裁决）**：超时阈值现有仅 `task.timeout.pick_hours / putaway_hours` 两枚（internal/sysops/configs.go:51-54 实读；task_timeout_scan 不扫 check）——一期**不新增 task.timeout.check_hours**（新增会连带超时通知扫描与收件人规则扩展，影响面超出一期）：checking 分支排序披露为 `mine > priority > created_at`（无超时层）。**receipt/exception 分支无 priority 列（B8 裁决——inbound_orders/exceptions DDL grep priority 零命中，000023 不扩列，不引入无处取数的排序层）**：两分支排序披露为 `created_at ASC`（先来先办语义）；exception 分支另加 `assignee=me 处理中 > OPEN` 前置层，且 **exceptions 表无仓库列**（000010:77-105 DDL 实读），`warehouse_id` 过滤对 exception 分支不生效（api.md 明示，验收不得当 bug）。以上口径差异全部写入 api.md §9 新节。
  - 响应 `{has_next: boolean, task: myTaskItemDTO|null}`（task 形状复用 workbench.go:719-738 myTaskItemDTO，receipt/exception 分支字段映射 task_no=新单号）。
  - 只读：纯 SELECT，guard-readonly 红线内。
- **新建（后端，priority 写入者——否则高优先级层无数据来源属假能力）**：迁移 000023（§3.4）加 priority 列后——
  - `PUT /api/putaway/:id/priority`（internal/purchase，权限新码 `purchase:putaway:assign`）、`PUT /api/picks/:id/priority`、`PUT /api/checks/:id/priority`（internal/sales，新码 `sales:pick:assign`/`sales:check:assign`）。实现：单列 UPDATE + 状态守卫（完成/取消态拒绝 409）+ middleware.Audit 审计；priority 值域 CHECK 0–9。
- **新建（前端）**：`web/src/hooks/useNextTask.ts`（queryFn 调 /api/tasks/next；返回 task 后由页面调用既有 claim 端点再导航——claim 已被领取返回冲突时视为可进入）；`web/src/components/task/SfCompleteNextButton.tsx`（`完成并处理下一条` primary 按钮，与既有确认按钮并排，复用调用方 onComplete 回调成功后触发）。
- **接入点（一期真实接线）**：`web/src/views/pad/receive/PadReceivePage.tsx`（收货确认提交点=POST /api/receipts 调用处，api/purchase.ts:317-318 域）、`web/src/views/pad/putaway/PadPutawayPage.tsx`（上架 execute）；PC 出库四作业页（PickingPage/CheckingPage/PackingPage/ShipmentPage）——**开工重验**：本轮实测 outbound 五页均在途修改中（git diff 各 16 行，疑似接线/urlSync 改造），router/index.tsx:66-67 注释仍写「待接线轮」；若开工时已接线完成，SfCompleteNextButton/useNextTask 直接接 PC 四页并销 §8.8 挂账，未接线则组件化交付随作业交互轮——以开工实测为准，如实挂账，不伪造。
- **不做**：跨仓库抢任务、任务自动分配。

### 2.5 最近操作 / 最近访问

- **复用**：operation_logs 表与写入链路（零新表零新写入点——写入仍只发生在既有 middleware.Audit）；GET /api/logs/operations 既有查询面（sysops）不动。
- **新建（后端）**：`GET /api/workbench/recent-operations?limit=10`（internal/reports/workbench.go，只读 SELECT：`WHERE user_id=当前 ORDER BY id DESC LIMIT n`，返回 {items:[{time,action,module,object_type,object_id,success,error_code,request_id}]}；权限挂 auth.PermInventoryList 与 workbench 同码）。
- **新建（前端）**：顶部『最近访问』=`web/src/components/common/RecentVisitsDropdown.tsx`（读 user_preferences.recent_visits，点击回详情）挂 PcLayout Header；工作台『最近操作』=WorkbenchPage 改版内嵌区块（读新端点，SfTimeline/SfTable 复用渲染）。
- **不做**：新审计表、页面浏览量统计、操作日志写接口（最近访问属导航态，走 2.3 偏好存储而非审计——避免污染 append-only 业务审计，api.md 披露该口径）。

### 2.6 上下文导航（详情页关联业务）

- **复用**：既有列表/详情端点的过滤参数与路由（SfQrPreviewDrawer 的抽屉下钻先例、frontend.md §10.3-10.4 六页签先例）。
- **新建（前端）**：`web/src/config/relations.tsx`（实体→关联项注册表：{label,目标路由或 Drawer 配置,所需参数,权限码}）+ `web/src/components/common/SfRelationNav.tsx`（SfDetailSection 内 chip 组，canAccess fail-closed 过滤，点击优先开 Drawer/带 query 预填跳转，不强制离开当前页）。
- **接入点**：StockDetailPage（SKU→库存/批次/序列号/入库/出库/调拨/盘点/追溯）、LedgerPage 行、InboundDetailPage（入库→收货/质检/上架/库存）、PurchaseOrderDetailPage（采购单→入库/收货/质检/上架/库存）、OutboundDetailPage（出库→拣货/复核/打包/发货/物流）、SalesOrderDetailPage、CountDetailPage。
- **后端扩展（仅当既有列表端点缺过滤参数，逐项核验后最小追加）**：如 inbound 列表按 source_no(=采购单号) 过滤、receipt 列表按 inbound_no 过滤——实现者开工时 grep 各域 handler 的 query 白名单，缺什么补什么参数（域包内小改），**禁止**为上下文导航新建聚合端点。
- **不做**：全站反向索引表、关联推荐算法。

### 2.7 批量结果中心

- **复用**：SfToolbar/SfBatchBar 发起侧（frontend.md §31.2 已内建）、行级领取服务（sales service_outbound.go ClaimPickTask、purchase service_putaway.go claim）。
- **新建（契约，api.md §9 新日期节冻结）**：`{total, success_count, failed_count, skipped_count, results:[{id, status: 'success'|'failed'|'skipped', reason}]}`。语义冻结：`skipped`=幂等命中/已处目标态；`failed.reason` 用既有错误码字符串。
- **新建（后端）**：`POST /api/putaway/batch-claim`（internal/purchase，权限复用 PermPutawayClaim）、`POST /api/picks/batch-claim`、`POST /api/checks/batch-claim`（internal/sales，复用 PermPickClaim/PermCheckClaim，零新权限码）。实现：逐条调用既有 claim 服务函数，PENDING→success；已被本人领取→skipped；被他人领取/状态非法→failed(reason=既有冲突码)；逐条审计；**批量不整体回滚**。
- **语义演进（打印）**：`POST /api/prints/tasks` 响应改为批量结果形态（200 + BatchResult；全部参数合法但部分对象不可打印时逐条 failed，reason=PRINT_SKU_DISABLED 等；整请求参数错误仍 400）。现 409+details.disabled_ids 整体拒绝语义废止——这是对验收场景 4（100 张→96/3/1）的硬前提。改动面：internal/printing/service.go CreateTask + handler.go + printing 测试；前端消费点 `web/src/api/printing.ts`、`PrintingCenterPage.tsx`、`SfQrPrintModal.tsx` 同步适配（api.md §9 披露演进，旧节不改写）。
- **新建（前端）**：`web/src/components/batch/BatchResultDrawer.tsx`（统一结果抽屉：计数条+逐条列表+SfStatusTag 三态+【仅重试失败】按钮；props={open,result,onRetry(failedIds),retrying}——重试语义=调用方以失败 ids 重新发起同一批量端点，成功项绝不重跑；**禁止各页面自写结果弹窗**）。打印场景 onRetry=以失败 data_ids 重建任务；批量领取 onRetry=仅对 failed ids 再调 batch-claim。
- **不做**：批量结果持久化表（同步返回即止）、批量审批/取消（无业务诉求，不做假功能）。

### 2.8 Excel 失败行修复

- **复用（已存在，零重建）**：错误 Excel「原数据+错误原因列」（internal/datax/excel.go:210-213）、`GET /api/imports/:id/error-file`（handler.go:114）、import_task_rows.raw+status 行级持久化（000011:54-70）、导入分批断点续跑。
- **新建（后端）**：`POST /api/imports/:id/retry-failed`（internal/datax/service_import.go + handler.go，权限 PermImportCreate）：读源任务 status IN ('INVALID','FAILED') 的行 → 以同一 ImportWriter 管线创建**新导入任务**（走正常校验+确认流程，复用 ImportWizard 前端既有六步向导语义）。
  - **前置核验项（实现者开工第一件事）**：核验 `service_import.go` 分批事务边界——FAILED 行必须保证无部分落库（逐行/逐批事务回滚）；若存在非事务落库路径，则 retry-failed 仅限 INVALID 行并在 api.md 披露。
  - 幂等依据：仅重导上次失败行（上次成功行不入集），不重复成功数据；PO/SO 写入器经既有单号引擎与状态守卫。
- **新建（前端）**：`web/src/api/data.ts` 增 retryFailed()；ImportWizardPage/任务列表对含失败行的任务展示【重新导入失败行】入口（携带源任务 id 走新任务流程）；错误 Excel 下载入口已有，不动。
- **不做**：行级编辑后回写（用户下载修复后走新导入）、断点重试队列。

### 2.9 快捷键系统

- **复用**：scanner.md §3.1「业务页禁自监听键盘」约束（本能力即其 PC 落地）、MENU_TREE（G 系跳转目标与 canAccess 校验）、TanStack QueryClient（Ctrl+R=invalidate active queries）。
- **新建（前端）**：`web/src/components/shortcut/` 三件：
  - `ShortcutProvider.tsx`：**全站唯一 keydown 监听点**（Context 注册表 + 分发）；注册表含 {combo, scope, description, handler, enabled}；输入态自动抑制（target 为 INPUT/TEXTAREA/SELECT/isContentEditable 或带 `[data-sf-scan-input]` 时页面级快捷键全部禁用）；G 系 chord（800ms 序列窗口）；Ctrl+R preventDefault→queryClient.invalidateQueries({refetchType:'active'})；Esc 仅处理自有层（GlobalSearch/帮助面板），antd Modal/Drawer 的 Esc 归 antd 自身，不双抢。
  - `shortcuts.ts`：集中注册表（Ctrl+K 全局搜索、Ctrl+F 当前列表搜索聚焦、Ctrl+R 刷新、Esc、Enter 确认、Alt+→ 下一条/Alt+← 上一条（经 useNextTask 注册，仅在任务作业页 enabled）、G+I→/inventory/stock、G+P→/purchases、G+S→/sales、G+T→/tasks；G 系跳转前 canAccess 校验，无权限 message 提示不跳转）。
  - `ShortcutHelpDrawer.tsx`：按 ? 打开的帮助面板（注册表驱动，零手写文档）。
- **挂载**：ShortcutProvider 包裹 PcLayout 内容树（PC 专用；Pad 不挂——Pad 卡片可达性层已有自身 keydown（PadTaskCard.tsx:29-33 等），一期不动）。
- **不做**：用户自定义键位映射、快捷键持久化。

### 2.10 幂等防护

- **复用（骨架已在，不新造机制）**：api.md §7 幂等键通式与 Idempotency-Key 头优先；inventory_ledgers.idempotency_key 唯一索引（DB 层兜底，所有库存原语过 ledger——调整/调拨/移库/盘点调整天然被兜底）；receipts/packing/shipments 幂等键列；`internal/asynqx` 任务幂等；devices 扫码 2s 去重窗口。
- **补齐（后端）——行级幂等键合成规则（一期冻结，api.md §7 补行）**：产生库存流水的 stockops 写路径（transfer approve/execute、moves、count 完成产生的调整；原语调用点 internal/stockops/transfer.go:566-577、count.go、moves.go）的行级键已按 api.md §7 通式逐行派生（`{动作}:{单据号}:{行号}:{bin/sku/batch}`，同请求 N 行键互不相同，不会自撞 uk_inventory_ledgers_idempotency_key，000005:162）。`Idempotency-Key` 头**不得直接**灌给每行原语（同请求会撞唯一索引、批量中断）：
  - 头键存在且非空 → 行级键 = `{头键}:{原行级通式}`（新命名空间：同头键重试=同行键=命中部分唯一索引→原语重放既有结果，不重复扣加库存/流水）；
  - 头键缺省 → 键形态与存量完全一致（零行为变化）；
  - 头键 ≤32 字符（超长 400 invalidParam）；**落笔前核验**最长行级通式+头键拼接 ≤ inventory_ledgers.idempotency_key 列宽 varchar(128)（000005:145），超限则下调头键上限（可降至 24）。
  - 适用面：**仅产生 ledger 流水的写路径**；单据创建类端点（POST /transfers、POST /counts）一期不引入头键（无单据级幂等键存储列，引入需迁移超范围）——api.md 披露该边界；验收场景 6（不重复扣库存/流水）由上述行级规则承担。
  - handler 侧与 purchase handler.go:489-502 同款 `c.GetHeader("Idempotency-Key")` 读取；缺键的请求体键位按 api.md §7 通式补文档行。
- **新建（前端）**：`web/src/utils/idempotency.ts`（newIdempotencyKey：crypto.randomUUID→crypto.getRandomValues→时间戳+计数器三级回退，PadReceivePage.tsx:349 已有先例）、`web/src/hooks/useIdempotentMutation.ts`（useMutation 包装：自动附 Idempotency-Key 头；isPending 期间按钮 loading+disabled 约定）。**切换清单与服务端头键消费面对齐（防「已防护」错觉）**：服务端实际消费头键的端点=收货（internal/purchase/handler.go:502）、打包/发货（internal/sales/handler.go:730,783）+ B4 补齐的 stockops 流水路径——这些提交点接 useIdempotentMutation 获得真实重放；上架 execute/拣货确认/复核/批量领取/盘点登记**不读头键**（防重来源=派生键通式或状态机原子抢占）——其提交点仍接该 hook，但收益仅为按钮防双击，服务端防重按既有机制；api.md 逐端点标注头键是否被消费（Wave I 清单）。
- **不做**：通用幂等结果缓存表（重放=键唯一索引命中回读既有结果，沿用 inventory 模式）、GET 请求幂等。

### 2.11 附加项

| 项 | 决定 |
|---|---|
| 工作台『我现在该做什么』 | 改版 `web/src/views/workbench/WorkbenchPage.tsx`：待我处理（/api/tasks?status=in_progress）+ 超时（summary 增量）+ 异常（summary exception_count 已有，task.ts:67）+ 今日已完成（summary 增量）+ 优先处理 TopN（/api/tasks/next 排序复用取 N 条）+ 最近操作（2.5 新端点）+ 快捷入口（MENU_TREE 驱动）。后端 summary 增量字段（timeout_count/mine_count/today_completed_count，键名对齐 workbench_summary.go 既有命名）为 additive，api.md §9 披露 |
| 列表页效率操作 | 统一由 SfTable/SfToolbar/SfSearchForm 既有能力（快速搜索/高级筛选/批量/列控制/刷新/空态 CTA——全部已内建）+ 新增 SfViewBar（2.2）+ 快捷键提示（2.9 帮助面板）构成；页面接入清单见 §6 F3 |
| 导出当前视图 | 复用 SfExportButton（scopeParams 跳导出中心预填，SfExportButton.tsx:46-59 机制）——各接入页把**当前 filters+仓库**真实传入 scopeParams；排序按行源默认序、列为模块固定列，api.md/前端副标题披露（§1.2 不做表）；严禁导出全库（无筛选时要求至少一项筛选或显式确认） |
| 任务页自动刷新 | `web/src/hooks/useAutoRefresh.ts`（refetchInterval 函数形式：关/10/30/60 秒；document.hidden 暂停；连续失败 ≥2 次退避停轮）+ SfAutoRefreshSelect；默认关；接入 picks/checks/putaway/exceptions/datax 任务列表页 |
| 扫码三模式 | `web/src/components/scanner/ScanInput.tsx` + `hooks/useScanBuffer.ts`（normal/fast/continuous；fast 模式智能下一步仅对查询/定位类低风险自动执行，确认收货/扣减类必须显式按键——prop `autoAdvance: 'none'|'safe'`；复用 POST /api/scanner/resolve 与 devices 2s 去重）。**与 scanner.md §3.1 冻结约束的关系（一期临时口径，Wave I 修订 docs/scanner.md 落档）**：ScanInput 不做页面级 window keydown 监听（该条禁止的形态），键盘缓冲解析限定在组件自身受控输入元素焦点内（ScanDirectCard 的 HID 承接先例，web/src/components/device/ScanDirectCard.tsx）；完整 ScannerManager→Event 总线归 F15/F16。一期接入示范页 PadPutawayPage；ScanDirectCard 不动 |
| UI 紧凑 | 复用 tokens.css 既有 --sf-* 与 SfTable density small 默认；新组件一律走 Token+Sf 组件族；动画只用于反馈（遵守 frontend.md §31：搜索结果出现/Drawer 打开/批量结果/任务切换/成功失败反馈，零 transition:all） |

---

## 3. 数据库迁移清单

编号规则：6 位零填充、up/down 严格成对、本地升+回滚双向验证后提交（docs/database.md:206-211）。000020 起可用（000019 实测为最新）。四个迁移分属三个波次，编号互不冲突（B1：000020+000021；B2：000022；B3：000023）。

### 3.1 `db/migrations/000020_create_user_saved_views.{up,down}.sql`（B1）

```sql
CREATE TABLE user_saved_views (
    id           bigserial    PRIMARY KEY,
    user_id      bigint       NOT NULL,
    page_key     varchar(64)  NOT NULL,
    name         varchar(64)  NOT NULL,
    filters_json jsonb        NOT NULL DEFAULT '{}'::jsonb,
    sort_json    jsonb        NOT NULL DEFAULT '{}'::jsonb,
    columns_json jsonb        NOT NULL DEFAULT '[]'::jsonb,
    page_size    integer      NOT NULL DEFAULT 20,   -- 上限对齐 API 分页 1-100（workbench.go:883 口径），存 200 会导致应用视图时列表端点 400
    is_default   boolean      NOT NULL DEFAULT FALSE,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint       NOT NULL DEFAULT 0,
    updated_by   bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_user_saved_views_page_key CHECK (page_key ~ '^[a-z0-9._-]{1,64}$'),
    CONSTRAINT chk_user_saved_views_page_size CHECK (page_size BETWEEN 1 AND 100)
);
CREATE UNIQUE INDEX uk_user_saved_views_name    ON user_saved_views (user_id, page_key, name);
CREATE UNIQUE INDEX uk_user_saved_views_default ON user_saved_views (user_id, page_key) WHERE is_default;
```
down：`DROP TABLE user_saved_views;`（部分唯一索引随表级联删除）。通用字段遵循 database.md §3；user_id 不加 FK（与既有表惯例一致，全库无跨域 FK——000011 起冻结口径）。

### 3.2 `db/migrations/000021_create_user_preferences.{up,down}.sql`（B1）

```sql
CREATE TABLE user_preferences (
    user_id    bigint      NOT NULL,
    pref_key   varchar(64) NOT NULL,
    pref_value jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pref_key),
    CONSTRAINT chk_user_preferences_key CHECK (pref_key ~ '^[a-z0-9_.]{1,64}$')
);
```
down：`DROP TABLE user_preferences;`。复合主键即全量索引，无冗余索引；无 created_by（个人高频态，不挂审计——§2.3）。

### 3.3 `db/migrations/000022_search_trgm_indexes.{up,down}.sql`（B2）

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- 名称/编码类（列名已按 DDL 核验；实现者落笔前对 000003/000004/000005 DDL 逐列复核一遍）
CREATE INDEX idx_products_name_trgm        ON products        USING gin (name gin_trgm_ops);
CREATE INDEX idx_skus_code_trgm            ON skus            USING gin (code gin_trgm_ops);
CREATE INDEX idx_skus_name_trgm            ON skus            USING gin (name gin_trgm_ops);
CREATE INDEX idx_barcodes_barcode_trgm     ON barcodes        USING gin (barcode gin_trgm_ops);
CREATE INDEX idx_batches_batch_no_trgm     ON batches         USING gin (batch_no gin_trgm_ops);
CREATE INDEX idx_serial_numbers_no_trgm    ON serial_numbers  USING gin (serial_no gin_trgm_ops);
CREATE INDEX idx_bins_code_trgm            ON bins            USING gin (code gin_trgm_ops);
CREATE INDEX idx_warehouses_name_trgm      ON warehouses      USING gin (name gin_trgm_ops);
CREATE INDEX idx_customers_name_trgm       ON customers       USING gin (name gin_trgm_ops);
CREATE INDEX idx_suppliers_name_trgm       ON suppliers       USING gin (name gin_trgm_ops);
-- 单据号类
CREATE INDEX idx_purchase_orders_no_trgm   ON purchase_orders USING gin (po_no gin_trgm_ops);
CREATE INDEX idx_inbound_orders_no_trgm    ON inbound_orders  USING gin (inbound_no gin_trgm_ops);
CREATE INDEX idx_sales_orders_no_trgm      ON sales_orders    USING gin (so_no gin_trgm_ops);
CREATE INDEX idx_outbound_orders_no_trgm   ON outbound_orders USING gin (outbound_no gin_trgm_ops);
CREATE INDEX idx_transfer_orders_no_trgm   ON transfer_orders USING gin (transfer_no gin_trgm_ops);
CREATE INDEX idx_count_orders_no_trgm      ON count_orders    USING gin (count_no gin_trgm_ops);
CREATE INDEX idx_exceptions_no_trgm        ON exceptions      USING gin (exception_no gin_trgm_ops);
CREATE INDEX idx_shipments_no_trgm         ON shipments       USING gin (shipment_no gin_trgm_ops);
```
down：按逆序 `DROP INDEX ...` + `DROP EXTENSION IF EXISTS pg_trgm;`。风险：pg_trgm 扩展需安装权限（§8.5）。

### 3.4 `db/migrations/000023_add_task_priority.{up,down}.sql`（B3）

```sql
ALTER TABLE putaway_tasks ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE pick_tasks     ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE check_tasks    ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE putaway_tasks ADD CONSTRAINT chk_putaway_tasks_priority CHECK (priority BETWEEN 0 AND 9);
ALTER TABLE pick_tasks     ADD CONSTRAINT chk_pick_tasks_priority     CHECK (priority BETWEEN 0 AND 9);
ALTER TABLE check_tasks    ADD CONSTRAINT chk_check_tasks_priority    CHECK (priority BETWEEN 0 AND 9);
CREATE INDEX idx_putaway_tasks_next ON putaway_tasks (status, priority DESC, created_at)
  WHERE status IN ('PENDING', 'IN_PROGRESS', 'PAUSED');
CREATE INDEX idx_pick_tasks_next ON pick_tasks (status, priority DESC, created_at)
  WHERE status IN ('PENDING', 'CLAIMED', 'PICKING');
CREATE INDEX idx_check_tasks_next ON check_tasks (status, priority DESC, created_at)
  WHERE status = 'PENDING';
```
down：DROP 三索引 + 三约束 + 三列。状态值域出处：pick 六态=000008:190、check 三态=000008:229、putaway 五态（含 PAUSED）=**000017_putaway_task_paused.up.sql:15-18**（000007:208 为原四态 CHECK，经 000017 放宽）。

---

## 4. 后端 API 清单

信封/分页/时间/ID 形态逐条对齐 api.md §2 冻结约定（信封 {code,message,data,request_id}；时间 YYYY-MM-DD HH:mm:ss；业务 ID 字符串形态；items 空为 []）。「权限标识」均引用 internal/auth/permissions.go 既有常量（新码仅 §4.4 三枚）。

### 4.1 新增端点

| 方法+路径 | 权限 | 所在包 | 说明 |
|---|---|---|---|
| GET /api/search | 认证 + 逐 type 权限过滤（§2.1） | internal/search（新） | q/types/warehouse_id/limit；q<2 不做 SQL |
| GET /api/user/views | 认证（无权限点，notifications 先例） | internal/userpref（新） | ?page_key= 必填 |
| POST /api/user/views | 同上 | internal/userpref | 重名 409 |
| PUT /api/user/views/:id | 同上 | internal/userpref | 非本人 404 |
| DELETE /api/user/views/:id | 同上 | internal/userpref | 非本人 404 |
| GET /api/user/preferences | 同上 | internal/userpref | ?keys= 逗号分隔可选 |
| PUT /api/user/preferences/:key | 同上 | internal/userpref | key 白名单外 400；值 ≤16KB |
| GET /api/tasks/next | auth.PermInventoryList | internal/reports | task_type 白名单五值；候选池=(mine 进行中)∪(PENDING 未领取)（§2.4 B7）；scopeOf 交集；has_next=false 形态；超时层仅 putaway/picking、receipt/exception 仅 created_at 排序（B8）、exception 不受 warehouse_id 过滤（§2.4 口径，api.md 披露） |
| GET /api/workbench/recent-operations | auth.PermInventoryList | internal/reports | 本人 operation_logs 尾 N 条 |
| POST /api/putaway/batch-claim | auth.PermPutawayClaim | internal/purchase | 批量结果契约 |
| POST /api/picks/batch-claim | auth.PermPickClaim | internal/sales | 同上 |
| POST /api/checks/batch-claim | auth.PermCheckClaim | internal/sales | 同上 |
| PUT /api/putaway/:id/priority | **新码 purchase:putaway:assign** | internal/purchase | 状态守卫 409 + 审计 |
| PUT /api/picks/:id/priority | **新码 sales:pick:assign** | internal/sales | 同上 |
| PUT /api/checks/:id/priority | **新码 sales:check:assign** | internal/sales | 同上 |
| POST /api/imports/:id/retry-failed | auth.PermImportCreate | internal/datax | 仅 INVALID/FAILED 行建新任务 |

### 4.2 复用扩展端点（additive，旧节不改写）

| 端点 | 扩展 | 所在包 |
|---|---|---|
| POST /api/prints/tasks | 响应演进为批量结果形态（§2.7）；409 整体拒绝语义废止 | internal/printing |
| GET /api/workbench/summary | 增量字段 timeout_count / mine_count / today_completed_count（键名对齐 workbench_summary.go 既有命名） | internal/reports |
| 既有列表端点（inbound/receipt 等） | 按需补 source_no/inbound_no 等过滤 query 参数（§2.6 核验后最小追加） | 各域包 |
| stockops 写端点 | 补 Idempotency-Key 头透传（§2.10） | internal/stockops |

### 4.3 零改动复用端点

claim（PUT /api/picks/:id/claim 等）、execute、POST /api/receipts、GET /api/tasks、GET /api/logs/operations、GET /api/scanner/resolve、导出（/api/exports）、GET /api/imports/:id/error-file——能力 4/5/6/7/8 直接消费。

### 4.4 权限码增量（全计划唯一一处）

新增 3 码（动词 assign 在冻结枚举）：`purchase:putaway:assign`、`sales:pick:assign`、`sales:check:assign`。同步四处：`internal/auth/permissions.go`（M2 段尾追加常量）、`internal/database/seed.go` permResources（:92）、`seed_test.go` 字面清单、`make swag` 注解。**不新增域码、不触发 DOMAIN_BOUND_RESOURCES fail-closed 面**。归 Wave I 独占。

---

## 5. 前端组件与模块清单

### 5.1 新增 api 模块

| 文件 | 内容 |
|---|---|
| web/src/api/search.ts | searchApi.search(params)→SearchResult（groups 契约） |
| web/src/api/userpref.ts | SavedView/Preference 类型 + views/preferences CRUD |
| web/src/api/task.ts（增） | taskApi.next(params) |
| web/src/api/data.ts（增） | dataApi.retryFailed(importId) |
| web/src/api/printing.ts（增改） | BatchResult 类型；createTask 返回类型演进 |

### 5.2 新增 hooks

`hooks/useSavedViews.ts`、`hooks/usePreferences.ts`（含 useRecentVisits 写入器）、`hooks/useNextTask.ts`、`hooks/useAutoRefresh.ts`、`hooks/useIdempotentMutation.ts`、`components/scanner/useScanBuffer.ts`。

### 5.3 新增组件与挂载点

| 组件 | 挂载点 |
|---|---|
| components/search/GlobalSearchModal.tsx | PcLayout Header（替换菜名 AutoComplete，原 :307-323 一带，在途漂移以开工实测为准；菜单搜索=『页面』分组） |
| components/shortcut/ShortcutProvider.tsx + shortcuts.ts + ShortcutHelpDrawer.tsx | 包裹 PcLayout 内容树（PC only） |
| components/common/RecentVisitsDropdown.tsx | PcLayout Header 右区（通知按钮旁） |
| components/common/SfPreferenceDrawer.tsx | PcLayout 用户菜单『偏好设置』项 |
| components/table/SfViewBar.tsx | 各接入列表页 SfToolbar extra 区 |
| components/batch/BatchResultDrawer.tsx | 打印中心/批量领取调用页（统一，禁自写） |
| components/task/SfCompleteNextButton.tsx | PadReceivePage、PadPutawayPage（PC 作业页待其接线轮） |
| components/common/SfRelationNav.tsx + config/relations.tsx | 七个详情页 SfDetailSection（§2.6 清单） |
| components/common/SfAutoRefreshSelect.tsx | 任务型列表页工具栏 |
| components/scanner/ScanInput.tsx | PadPutawayPage（示范） |
| utils/idempotency.ts | 收货/上架/拣货/复核/打包/发货/盘点/批量提交点 |

### 5.4 页面接入清单（F3）

- 列表页（SfViewBar+导出当前视图+自动刷新按需）：StockListPage、LedgerPage、BatchListPage、SerialListPage、PurchaseListPage、ReceiptListPage、InboundPage、SalesOrderListPage、SalesOutboundListPage、TransferListPage、CountTaskListPage、ExceptionCenterPage、PickingPage、CheckingPage。
- 打印消费点适配（2.7 演进波及）：PrintingCenterPage.tsx、SfQrPrintModal.tsx、api/printing.ts。
- 工作台改版：views/workbench/WorkbenchPage.tsx（含 PadHomePage？**不动**——Pad 首页一期维持，改版仅 PC）。
- 详情页：§2.6 七处。

---

## 6. 实施波次与文件所有权边界

并行实现者**互不碰同一文件**；下表未列出的文件默认不碰。共享文件一律单点归属。

| 波次 | 职责 | 独占文件 | 前置 |
|---|---|---|---|
| **W0 门禁解锁**（集成工程师） | 与在途会话/作者**按包逐个清点**处置在途未跟踪/已修改文件（清单以当场 git status 为准，§1.2；已被作者修绿的保留，禁止删除他人工作）。**特别协调面**：① 后端测试活跃包 reports/stockops/printing/datax/devices（恰为 B3/B4 归属包，同包测试一起编译，半成品卡死自身门禁）；② web urlSync 改造（usePagedList.ts/SfSearchForm.tsx/StockListPage.tsx/outbound 五页——B5 前提），冻结其完成态 API（applyFilters 签名/URL 编码格式/initialValues 回填）为 F2/F3 消费契约；恢复 `make ci` 绿 + `npm run build` 绿，基线记入 current.md | 无（协调位） | — |
| **B1 用户态**（后端） | 迁移 000020/000021 + internal/userpref 全包 + 包内测试 | db/migrations/000020_*、000021_*；internal/userpref/** | W0 |
| **B2 搜索**（后端） | 迁移 000022 + internal/search 全包 + auth.HasPermission helper（如需）+ 包内测试 | db/migrations/000022_*；internal/search/**；internal/auth/has_permission.go（新文件） | W0 |
| **B3 任务流**（后端） | 迁移 000023 + reports workbench.go/workbench_summary.go（next/recent-ops/summary 增量）+ sales/purchase 的 batch-claim 与 priority（新文件 service_batch.go + 各自 handler/routes/routes_test 增量） | db/migrations/000023_*；internal/reports/workbench.go、workbench_summary.go 及新测试文件；internal/sales/service_batch.go、routes.go、routes_test.go、handler.go 增量；internal/purchase/service_batch.go、purchase.go、routes_test.go、handler.go 增量 | W0 |
| **B4 批量与导入**（后端） | printing CreateTask 批量结果演进 + datax retry-failed + stockops 行级幂等键合成（§2.10）+ 各包测试 | internal/printing/**、internal/datax/service_import.go、handler.go、新测试、internal/stockops/handler.go、transfer.go、count.go、moves.go（幂等增量，键合成在原语调用点） | W0 |
| **F1 全局层**（前端） | GlobalSearch + ShortcutProvider 族 + RecentVisitsDropdown + SfPreferenceDrawer + api/search.ts + api/userpref.ts + usePreferences + global.css 增补段 | web/src/layouts/PcLayout.tsx、web/src/styles/global.css、components/search/**、components/shortcut/**、api/search.ts、api/userpref.ts | W0 |
| **F2 表格与批量**（前端） | SfViewBar（双形态 mode，§2.2）+ **SfTable 受控列 API（hiddenColumns/onHiddenColumnsChange，受控优先/非受控回退，§2.2 B6）** + BatchResultDrawer + SfCompleteNextButton + SfRelationNav+relations.tsx + SfAutoRefreshSelect + ScanInput/useScanBuffer + useSavedViews/useNextTask/useAutoRefresh/useIdempotentMutation + utils/idempotency.ts + api/task.ts/data.ts/printing.ts 增量 | 上列新文件 + **web/src/components/table/SfTable.tsx（仅限受控列 API 增量：+2 props 与受控分支，不动 applyAlignDefaults/动效/骨架/既有 storageKey 逻辑）**；**禁改 usePagedList.ts/SfSearchForm.tsx（urlSync 协调面，W0 产）** | W0 |
| **F3 页面接入**（前端） | §5.4 清单页面接线 + WorkbenchPage 改版 + 打印消费点适配；**每页接线前核验其 usePagedList 形态**（grep urlSync：urlSync 页经 applyFilters 写 URL、旧形态页置 params，声明 SfViewBar mode，禁双源混用） | views/**（除 F1/F2 新组件文件；urlSync 在途页与其作者协调接线时点） | F1、F2 组件就绪 + W0 urlSync 契约冻结 |
| **I 集成收口**（集成工程师） | router.go 装配 search/userpref（RegisterRoutes + With* 模式，router.go:98-262 先例）；permissions.go 三码 + seed.go + seed_test.go；api.md §1 加行+§9 新日期节+§7 幂等键通式补行（头键前缀合成规则）；database.md §2 实体清单+补录注记；frontend.md §23 登记+新§快捷键规范+§15.2/§31 增量；**scanner.md §3.1 增量（ScanInput 一期临时口径：焦点内承接+与 ScannerManager 的关系+F15/F16 归宿——§2.11，文档先行红线）**；testing.md 新域节；requirements.md §2.2/2.7/2.8 交付口径批注；changelog.md『作业效率提升层一期』条目；current.md 新节；`make swag` | internal/router/router.go、internal/auth/permissions.go、internal/database/seed.go、seed_test.go、docs/* | B1–B4、F1–F3 |

并行关系：B1/B2/B3/B4 四路并行（文件零交集）；F1/F2 并行，F3 依赖两者组件；I 收尾。每波次完成即跑各自门禁（后端 `go build ./... && go vet ./... && go test ./...`；前端 `npm run typecheck && npm run lint`），不通过不交接。

---

## 7. 测试矩阵

### 7.1 后端单元测试（零外部依赖，fakedb/fakeRepo 项目约定，先例 internal/purchase/fakedb_test.go）

| # | 用例 | 断言要点 | 归属 |
|---|---|---|---|
| T1 | 搜索权限过滤 | 无 masterdata:sku:list 的用户查 "SKU001"→groups 不含 sku 组；有码则含且 items 形状齐 | B2 |
| T2 | 搜索数据权限 | warehouse_id 越用户 scope→该组空结果；scope 交集生效 | B2 |
| T3 | 搜索短路 | q 长度<2 零 SQL 执行（repo 调用计数=0）；limit 缺省 5 上限 20 | B2 |
| T4 | 视图用户隔离 | A 建视图，B GET/PUT/DELETE→404；A 列表不见 B 的行 | B1 |
| T5 | 视图默认唯一 | 设新默认后旧默认自动清除；并发设默认最终恰一个（部分唯一索引） | B1 |
| T6 | 偏好白名单 | 白名单外 key→400；GET ?keys= 过滤生效；>16KB→400 | B1 |
| T7 | next 排序与候选池 | 构造 mine进行中/priority/timeout/created 四层候选（putaway/picking 分支，超时阈值注入），逐层序断言；**候选池含未领取 PENDING 断言**——池内仅存在他人未领取 PENDING 任务时 has_next=true 且按 priority/created_at 排序（B7：WHERE 必须为 (mine 进行中) OR (PENDING 未领取)，照抄 /api/tasks 原 WHERE 会恒 has_next=false）；checking 分支断言无超时层（mine>priority>created_at）；receipt/exception 分支断言仅 created_at 排序（B8）；current_task_id 被排除；无候选 has_next=false；task_type=packing→400 | B3 |
| T8 | 批量领取结果 | PENDING→success；本人已领→skipped；他人已领→failed(reason)；混合批次计数与 results 逐条对账 | B3 |
| T9 | 并发双确认 | 同一任务两 goroutine 并发 claim（fake repo 原子 UPDATE 抢占）恰一 success 一 failed；批量场景对齐 TestConcurrentLockNoOversell 模式（inventory/integration_test.go:132） | B3 |
| T10 | priority 守卫 | 完成态任务设优先级→409；值域外→400；审计写入（middleware.Audit 断言先例 sales claim 审计） | B3 |
| T11 | 打印批量结果 | disabled ids→failed(reason=PRINT_SKU_DISABLED)；96/3/1 形状断言；全成功无 409 | B4 |
| T12 | retry-failed | 仅 INVALID/FAILED 行进入新任务；重跑不再产生重复单据（fake writer 计数=失败行数）；源任务行状态不被篡改 | B4 |
| T13 | 幂等重放 | 同 Idempotency-Key 二次提交返回首次结果且库存/流水/单据不变（行级键=头键前缀合成规则 §2.10；先例锚定**已跟踪文件** internal/inventory/integration_test.go 的 TestIdempotencyReplayDoesNotDoubleApply / TestConcurrentLockNoOversell——purchase handler_test.go 为未跟踪在途文件，不作锚点；inventory 既有用例保持绿） | B4 |
| T14 | routes 冻结端点集 | search/userpref/next/recent-ops/batch-claim×3/priority×3/retry-failed 全部进入各包 routes_test 冻结清单（漏注册即测试红） | B1–B4 + I |
| T15 | 迁移双向 | 000020–000023 本地 `make migrate-up` + `make migrate-down 1` 往返验证（database.md:211 硬要求；无 PG 环境时如实挂账不入「已验证」） | B1/B2/B3 |

### 7.2 前端测试策略

- **维持既有现状**：web 无自动化测试设施（实测 package.json scripts），且引入 vitest 已有明确裁决先例不做（current.md:220，SFQR 轮）。一期门禁=`npm run typecheck`（tsc -b）+ `npm run lint` + `npm run build`。
- **人工验收矩阵**（写入 docs/testing.md 新域节，交付时逐项执行并记录）：8 个验收场景（§9）+ 快捷键全表走查（含输入态抑制/G 系 chord/？帮助）+ Light/Dark 两主题 + 1440/1024/768 三宽度 + reduced-motion 下新增动效归零核验（frontend.md §31 硬约束）。

---

## 8. 风险与回避

| # | 风险 | 回避 |
|---|---|---|
| 8.1 | **工作树在途并发会话实时漂移**：两轮实测 vet 从红转绿、FAIL 包从五个缩至 stockops、未跟踪/已修改文件集持续变化（§1.2）——任何静态文件清单都会失真 | W0 以**开工当场** git status + 全量门禁重测为准（§1.2 W0 作业定义）；已被作者修绿的文件原样保留；每波次开工前重跑门禁比对；共享文件（PcLayout/global.css/routes_test/api.md）按 §6 单点归属 |
| 8.2 | api.md §9 历史节不可改写 | 打印响应演进、summary 增量、搜索契约全部以**新日期小节**追加 + §1 域清单加行；旧节零改动 |
| 8.3 | 权限双源漂移（常量/seed/seed_test/routes_test/swag 五处） | 仅 3 个新码且集中 Wave I 一次收编；新增域码为零 |
| 8.4 | reports 只读红线 | next/recent-ops/summary 全 SELECT；批量写入全部落各 owning 域包（sales/purchase），reports 零写 |
| 8.5 | pg_trgm 扩展不可用（生产 app 账号无 CREATE EXTENSION 权限，迁移由超级用户执行——deployment.md grants 流程） | down 成对；若目标库拒绝扩展，回退方案=去索引保 ILIKE（功能等价、性能降级，挂账阶段 21 性能优化）；本地便携 PG16 为超管可装 |
| 8.6 | receipt/exception 的「本人已领取」层级语义缺失；checking 无超时阈值；exception 无仓库列 | 口径已在 §2.4 冻结披露：receipt 无领取语义、checking 无超时层（不新增 task.timeout.check_hours，避免连带超时通知扫描与收件人规则扩展）、exception 不受 warehouse_id 过滤——api.md §9 明示；不伪造数据层 |
| 8.7 | 导出当前视图的排序/列维度与任务要求存在范围差 | 一期严格透传筛选+仓库，排序/列口径披露（§1.2）；如验收坚持全维度，需 ExportSource 增 columns/order 能力，另行立项 |
| 8.8 | PC 出库四作业页接线状态在途漂移（本轮实测 outbound 五页均在途修改，router/index.tsx:66-67 注释仍写待接线） | §2.4 开工重验：已接线则直接接 SfCompleteNextButton/useNextTask 并销账；未接线则组件化交付+Pad 收货/上架先行，changelog 如实记录 |
| 8.9 | retry-failed 依赖「FAILED 行无部分落库」 | B4 开工第一项核验 service_import.go 事务边界；不满足则缩窄为仅 INVALID 行并披露 |
| 8.10 | 打印响应语义演进破坏既有消费点 | 演进与前端适配（PrintingCenterPage/SfQrPrintModal）同波次交付、同 commit；routes_test 断言同步 |
| 8.11 | GORM 列名陷阱（SKUID→sk_uid 教训，api.md:443-446） | userpref/search 模型显式 `gorm:"column:..."` tag；jsonb 列名用 pref_value 等下划线形态 |
| 8.12 | Idempotency-Key 头+行级通式拼接超 inventory_ledgers.idempotency_key 列宽 varchar(128)（000005:145） | §2.10 落笔前核验最长键长；超限下调头键上限（≤32 起步可降至 24）；超长一律 400 |
| 8.13 | 在途 urlSync 改造与保存视图双形态并存（urlSync 页 URL 事实源 vs 旧页页面 state），视图应用接错形态=双源打架、状态互相覆盖 | §2.2 B5 裁决：SfViewBar 声明 mode，一律经 usePagedList 公开 API（不直改 URL/state）；W0 冻结 urlSync 完成态契约后才进 F2/F3；F3 逐页接线前 grep urlSync 核验形态 |
| 8.14 | SfTable 受控列 API（B6）触达 66 个既有消费文件，回归面广 | 受控优先/非受控回退——不传新 props 零行为变化；F2 改动仅限 +2 props 与受控分支（不动 align/动效/骨架/storageKey 既有逻辑）；F2 自验=typecheck + 抽查非受控页（列设置/密度/隐藏列行为不变） |

---

## 9. 验收场景映射

| 场景 | 依赖交付 | 验证方式 |
|---|---|---|
| 1 Ctrl+K 输入 SKU001 直达 SKU/库存/批次 | 2.1 + F1 | 人工：键盘操作+三分组点击直达 |
| 2 保存『仓库A临期补货』视图次日复用 | 2.2 + 000020 + SfViewBar + SfTable 受控列 API | 人工：保存→登出重登→应用还原筛选/列显示/分页（排序与列序一期不采集不做，api.md/frontend.md 披露） |
| 3 收货任务 001 完成→进 002 | 2.4 + /api/tasks/next + PadReceivePage 接线 | 人工+T7/T8 |
| 4 批量打印 100→96/3/1，仅重试失败 3 张 | 2.7 打印演进 + BatchResultDrawer | 人工+T11 |
| 5 Excel 1000 行 980/20，只重导 20 行 | 2.8 retry-failed | 人工+T12 |
| 6 连点确认收货 10 次仅一次库存变化+一条流水 | 2.10 + useIdempotentMutation | T13 + 人工连点 |
| 7 SKU 详情一屏看全关联业务 | 2.6 SfRelationNav | 人工：七关联项可达 |
| 8 操作员进工作台即知该干什么 | 2.11 工作台改版 + 2.5 | 人工：四计数+优先处理+最近操作齐备 |

---

## 10. 文档交付清单（Wave I，一次收口）

- docs/api.md：§1 加 search/userpref 域行与各端点行；§9 新增「2026-10-06 作业效率提升层一期」节（搜索契约、userpref 契约含 user_saved_views 字段披露——sort_json 恒空为排序通道预留/columns_json=hidden 列键/列序不做、next 候选池与排序口径披露（候选池含未领取 PENDING——B7；receipt/exception 仅 created_at 排序、checking 无超时层、exception 不受 warehouse_id 过滤——§2.4/B8）、批量结果契约、打印语义演进、summary 增量、retry-failed、幂等键行补录）。
- docs/database.md：§2 实体清单补 user_saved_views/user_preferences 两组；000020–000023 行内补录注记。
- docs/frontend.md：§23 登记 10 个新组件；新增「快捷键规范」节（注册表/作用域/冲突规则/帮助面板/输入态抑制）；§15.2 全局搜索落地更新；§6.1 SfTable 受控列 API 增量登记（hiddenColumns/onHiddenColumnsChange，受控优先/非受控回退）+ 保存视图范围披露（排序不采集/列序不做）；§31 动效增量。
- docs/testing.md：新增效率层域节（7.1 矩阵收编 + 前端人工验收矩阵）。
- docs/requirements.md：§2.2/§2.7/§2.8 交付口径批注（不改写原需求句）。
- docs/changelog.md：「作业效率提升层一期」条目（背景/交付/验证/口径披露/影响范围）。
- docs/tasks/current.md：新任务节（本计划路径、波次表、状态）。
