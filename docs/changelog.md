# StockFlow 变更日志

本文件记录项目文档与系统的重大变更。每完成一个模块在"模块交付记录"中追加。

格式约定：

```text
## [日期] 类型：标题
- 变更内容
- 影响范围
```

---

## 文档记录

## [2026-10-02] 文档：frontend.md 升级 v1.1（前端完整规范，105 章重组）

- 将"前端完整开发提示词"（105 章）并入 frontend.md 并整体重写为三端前端规范：总体要求与开工检查、全局 Design Token（--sf-*）与 Light/Dark 主题、信息密度基准值、PC Layout 与 Sidebar 菜单树（新增设备中心）、Dashboard 四层结构、表格系统（能力/视觉/密度/工具栏）、详情页与 Timeline、表单分区、状态与反馈（含错误页、作业端轻量 Loading）、库存中心 UI（实时库存/库存详情 Tabs/层级分布/盘点前端与扫码页）、库位地图、Excel 前端、打印前端（预览：缩放/翻页/PDF）、设备管理前端（设备中心/设备详情/二维码激活）、工作台/全局搜索/通知/附件、批量与危险操作、权限 UI、状态分域与缓存与实时同步、五断点响应式与 PWA 边界、Pad UI（横竖屏/拍照/底部操作栏/触摸）、Scan UI（作业页面要点/扫码中心/登录/成功错误视觉）、扫码接入与组件联动、Sf* 组件封装清单、视觉状态与动画规范、工程规范与目录结构、页面交付清单（PC/Pad/Scan）、验收标准、最终禁止清单、实施阶段 F1–F20。
- 技术栈一致性修正：原提示词中 Vue 术语（@scan 示例、composables、"Vue Admin"）统一为 React 体系（onScan、hooks、Admin 模板）；组件基于 React + Ant Design 6 封装。
- 同步更新：requirements.md（页面清单补设备中心并注明明细见 frontend.md §4.2）、development-plan.md（领域子阶段说明：D1–D20 / F1–F20）、README.md（文档地图与前端阅读指引）。
- 影响范围：前端领域以 frontend.md v1.1 为唯一依据；devices.md 管 Scan 应用定位与设备体系，scanner.md 管扫码领域，分工不变。

## [2026-10-02] 文档：新增设备与多终端体系规范 v1.0；Ant Design 升级至 6

- 将"真实工业扫码终端与扫码枪完整开发任务"（69 章）整理为 [devices.md](devices.md)：PC/Pad/StockFlow Scan 三端产品结构、真实工业设备设计原则、StockFlow Scan 独立应用（技术方案：React Native 主选/Capacitor 备选，必须直达 Android 扫描能力与实体 Scan 键）、Scanner 抽象层与厂商 Adapter（Zebra DataWedge/Honeywell/UROVO/Camera）、设备注册激活（二维码）/绑定仓库/共享设备/自动锁屏、设备管理后台与健康监控/配置下发/App 更新、网络与离线容错（最终库存操作必须服务端确认）、Scan 端交互与审计、三端数据统一、实施子阶段 D1–D20、真实设备测试与最终验收。
- **Ant Design 全局升级为最新稳定版 6（当前 6.6.5，随官方 release 更新）**：architecture.md §11.3（React 18+ 满足 antd 6 适配）、frontend.md §1、README.md、devices.md §15；dayjs 跟随 antd 6 官方适配要求。
- scanner.md 同步：接入工业 PDA 内置扫描头（PDA 主方式）、Adapter 清单扩展厂商实现、ScanResult.source 统一为 hid/serial/zebra/honeywell/urovo/camera、codeType 更名 symbology、与 devices.md 明确分工（scanner=扫码领域，devices=设备与多终端）。
- 同步更新：api.md（新增 /api/devices 领域）、database.md（devices/device_logs/device_configs/app_versions 实体，scanner_devices 并入 devices）、requirements.md（三端页面清单、设备管理页、验收场景 9）、development-plan.md（阶段 16 对接 D1–D20，里程碑 M3）、frontend.md（§9 多终端三端定位）、testing.md（§10 设备与真实终端测试，验收级测试改为 9 场景）、README.md（文档地图）。
- 影响范围：设备与多终端领域以 devices.md 为唯一依据；扫码领域仍以 scanner.md 为准。

## [2026-10-02] 文档：确定技术栈基线（Go + React/Ant Design）

- 后端确定 **Go**，按高并发、低延迟导向确定库基线（architecture.md §11.2）：Gin、PostgreSQL/pgx（备选 MySQL 8）、GORM+关键路径原生 SQL、go-redis、zap、sonic（可选）、ants、asynq、robfig/cron、viper、validator、golang-jwt、excelize、boombuler/barcode、swaggo、golang-migrate、testify+testcontainers。
- 前端确定 **React 18 + Ant Design 5**（https://ant.design/index-cn）（architecture.md §11.3、frontend.md §1）：Vite/TS/react-router/TanStack Query/zustand/axios/@ant-design/plots/dayjs/JsBarcode+qrcode/react-to-print，ConfigProvider zhCN 统一主题。
- 新增性能配置底线（architecture.md §11.4）：连接池显式配置、高频接口缓存与索引、异步任务可观测。
- 同步更新：README（技术栈行）、scanner.md（SerialScanner 采用 Chrome Web Serial API、组件族基于 React+antd 封装）、database.md（迁移工具基线）、api.md（Swagger 工具基线）。
- 影响范围：全项目技术选型；替换基线中任何一项需评估并在本文档记录。

## [2026-10-02] 文档：新增扫码枪深度接入规范 v1.0

- 将"StockFlow 扫码枪/条码设备深度接入开发要求"（55 章）整理为 [scanner.md](scanner.md)：设备接入层（USB HID/蓝牙/串口/摄像头 + Scanner Adapter）、前端 Scanner 架构与组件族、扫码设备管理与测试、统一条码解析（BarcodeResolver + /api/scanner/resolve）、扫码交互规范（错误码表/声音震动/三种模式/风险分级/幂等/库存安全）、16 类业务场景深度接入、箱码/托盘管理、打印与 Excel 联动、覆盖矩阵与三级优先级、现场验收标准。
- 同步更新：README（文档地图/阅读顺序）、api（新增 /api/scanner 领域与幂等约定）、database（新增 scanner_devices/scan_logs/boxes/pallets 等实体）、printing（码制扩展至 7 种）、frontend（§10 扫码章节补充接入要点）、testing（新增扫码枪专项测试 §9）、development-plan（阶段 16 调整 + 里程碑 M3）、requirements（支撑平台表 + 验收场景 8）、business-flow（关联引用）。
- 实施优先级基线：第一优先级收货/上架/拣货/复核/盘点/移库；第二优先级调拨/质检/退货/打包/发货；第三优先级库存查询/库位查询/序列号追溯/箱码/托盘管理。
- 影响范围：扫码领域以 scanner.md 为唯一依据；涉及业务流程的扫码要求以 business-flow.md 流程为准、scanner.md 作业细节为准。

## [2026-10-02] 文档：建立开发文档体系 v1.0

- 将 StockFlow 开发提示词（123 章）整理为结构化开发文档体系，建立需求基线。
- 文档清单：README、requirements、business-flow、inventory-rules、architecture、database、api、permission、excel、printing、frontend、deployment、testing、development-plan、changelog（共 15 篇）。
- 影响范围：全项目；inventory-rules 与 business-flow 为业务正确性基线文档。

---

## 模块交付记录

（按 development-plan.md 的 22 个阶段，每阶段完成后在此追加记录）

## [2026-10-02] 前端：第三批五组并行页面集成收口（质量/调拨/异常/库存分析详情/单据详情/盘点/数据导入导出）

- 五组并行开发的 14 个页面 + 3 个新 api 模块 + 3 个既有 api 模块追加 + 2 个新公共组件统一接入路由（web/src/router/index.tsx），注册 **14 条 lazy 路由**（静态 9 条 + 动态段 5 条）：
  - 质量中心（/quality/inspections /quality/nonconforming /quality/trace；api/quality.ts 前端先行契约：检验方式三值、处理结果九值、去向六值对齐 business-flow §4.1–§4.3，QC- 前缀 §13.1，未知值经 label/semantic 三参兜底不崩溃）；
  - 调拨单（/transfers；api/transfer.ts 两维度 + 7 态状态机对齐 §10.1，TR- 前缀 §13.1；与库存转移 /api/inventory/transfers 在 api 注释中明确语义区分）；
  - 异常中心（/exceptions；api/exception.ts 九类异常 + 生命周期 7 态对齐 §11.2，类型按分类渲染、状态走 SfStatusTag）；
  - 库存分析 + SKU 库存详情（/inventory/analytics、/inventory/stock/:skuCode；api/inventory.ts 纯追加 stockDetail/stockDistribution/analytics 三端点 + LedgerQuery/InventoryLockQuery/BatchQuery/SerialQuery 增 skuCode 可选筛选，既有 10 端点零改动；详情页六页签复用既有列表端点固定 skuCode 过滤，§10.3 头部五指标 + §10.4 层级下钻；StockListPage 行点击进入 + §26.3 sessionStorage 持久化）；
  - 单据详情 F7（/inbound/:id /outbound/:id /purchases/:id；api/inbound.ts、api/outbound.ts、api/purchase.ts 各纯追加 GET /{id} 详情契约与 InboundDetail/OutboundDetail/PurchaseDetail 类型；新组件 SfDetailHeader（单号+状态+actions/summary 插槽）、SfTimeline（状态/时间/操作人/备注，状态由「有时间→已完成、命中状态映射→单据状态、其余→未开始」三规则推演）；入库 Timeline 覆盖创建→审核→收货→质检→上架，出库覆盖分配→拣货→复核→打包→发货（§7.2/§8），采购含部分收货进度（§2.3）；三个列表页仅最小改动追加右侧「详情」列）；
  - 盘点中心 F9（/counts /counts/:id；api/count.ts 四端点：GET /api/counts、/{id}、/{id}/items、PUT /{id}/items/{itemId} 实盘登记（差异原因/备注必填、不带库存修改语义），七态状态机 + CK- 前缀对齐 §10.2/§13.1；详情页四要素：Steps 状态机进度 + COUNT_FREEZE 冻结说明 + 差异走库存调整单链路提示 + 业务流程 Timeline，无任何修改系统库存入口，frontend.md §10.5）；
  - 数据中心 F11（/data/imports /data/exports；api/data.ts 导入六步向导 multipart 上传/校验逐行定位/预览/确认 + 初始化库存高危二次确认（excel.md §6.1–§6.3）+ 导出任务列表 5s 在途轮询进入终态自动停止 + downloadFile 认证 blob 下载，绝不生成假文件；/data/printing、/data/files 保持占位）。
- IMPLEMENTED_PATHS 增补 9 条（动态段无菜单路径不参与占位匹配、不入 Set）；程序化检查 children 路由 path 无重复、IMPLEMENTED_PATHS 无重复条目；9 条静态路径与 config/menu.tsx（:47/:65/:78/:106-108/:111/:131-132）逐条一致，menu.tsx **零改动**；/inventory/count（menu.tsx:62）维持占位（盘点由 /counts 承载）；/purchases/:id 与 /purchases/receipts、/purchases/returns 静态段共存，react-router 静态段优先，无冲突。
- 跨文件一致性检查（集成阶段实际执行）：14 个新页面均 default export；全库 api/types/components/utils/hooks/stores/router/config + views 导出标识符 `grep | sort | uniq -d` 查重仅 toStatusKey 重复（api/masterdata.ts:14 与 api/warehouse.ts:15，既有 F6 问题，无同文件双导入，保留并列入遗留）；28 个新页面 api 调用点与模块定义逐一比对通过（qualityApi.inspections/nonconforming/trace、transferApi.list、exceptionApi.list、inventoryApi.analytics/stockDetail/stockDistribution/batches/serials/ledger/locks/trace、inboundApi/outboundApi/purchaseApi.get、countApi.list/detail/items/registerItem、dataApi.imports.templates/upload/validate/preview/confirm/list、dataApi.exports.list/create）。
- **types/status.ts 本轮补注册 22 个状态键**（本轮集成收口要求，替代上一批「前端先行枚举暂不补录」决策）：调拨 pending_outbound/transferring/pending_inbound、异常生命周期 discovered/created/assigned/pending_recheck/resolved、库存锁定 active/released/consumed、序列号 in_stock/outbound/returned、调整单 executed、盘点明细行 counted、质检处置 return_supplier/scrap/rework/downgrade/to_defective_warehouse/special_release——label/semantic 与各页面既有三参兜底逐键比对一致，补录后渲染行为不变；数据中心任务状态（QUEUED/PROCESSING/SUCCESS/PARTIAL_SUCCESS/FAILED）为 excel.md §4 冻结大写枚举，页面显式指定 label/semantic 不查注册表，不补录（避免注册表大小写双风格）；采购 partially_received 恒走 SfTimeline label 兜底分支（STATUS_NODE_STATE 不传 status），不补录。
- **修复盘点页状态显示 bug**：CountTaskListPage.tsx:34 与 CountDetailPage.tsx:54 的 PENDING_REVIEW（待复核）原映射 key 'pending_review'——SfStatusTag 注册表优先（SfStatusTag.tsx:30-32），命中注册表「待审核」，页面 label 覆盖「待复核」无效；改走本轮新注册的 'pending_recheck' 后显示修正为「待复核」。PENDING_APPROVAL（待审核）保持 'pending_review' 不变。
- 验证：`npx tsc --noEmit -p tsconfig.app.json` 全项目 EXIT=0；`npx eslint src/router/index.tsx src/types/status.ts src/views/count/CountTaskListPage.tsx src/views/count/CountDetailPage.tsx`（本轮实际修改的 4 个文件）EXIT=0。按要求未运行全局构建（`npm run build` 留门禁阶段统一执行）。
- 影响范围：路由接线、状态注册表补录、盘点页 2 处 key/注释修正与文档；未改动各页面其余实现与公共组件。
- 遗留对齐清单（后端对应域落地时处理）：
  - 后端质量/调拨/异常/盘点/数据中心/库存分析域均未交付，相关页面呈统一错误态为预期行为（requirements.md §10）；
  - 前端先行契约冻结回对：/api/quality/inspections、/nonconforming、/trace 与 /api/transfers、/api/exceptions 的枚举/字段（检验方式、处理结果九值、去向六值、质量记录类型；异常单号前缀 §13.1 未定义；不合格品以 QC- 为锚点未设独立单号，若后端定义 NCR- 需回对 qcNo 字段语义）；GET /api/inventory/{skuCode}（及 /distribution）、/api/inventory/analytics 三端点——后端路由注册须保证静态段（/summary、/locks、/batches 等）优先于 {skuCode} 参数段，且五列表端点需支持 skuCode 可选筛选；/api/counts 四端点（summary 结构、范围/类型/七态大写枚举、差异原因枚举、ownerName）；/api/imports|/api/exports（multipart 字段名 importType/file、progress 0-100、errorFileUrl、filters 键值、ImportType/ExportScope）；GET /api/{inbounds|outbounds|purchases}/{id} 的 items 明细与各环节 *At/*By 字段名——均以后端 Go JSON tag 为准；
  - 质检单列表未设独立「单据状态」列（检验结果承担呈现，§13.2 若后端定义质检单状态值域需补列）；出库/入库明细行行级状态未展示（行状态值域后端未冻结）；
  - StockDetailPage 因并行组文件范围受限复制了 SerialListPage/LocksPage/TracePage 同款状态映射，后端契约冻结后应收敛至 types/status.ts 注册表消除重复；SfTable 统一错误态未透传 description（SfError 支持），缺失端点信息暂标注于页面注释与 api 模块注释；
  - toStatusKey 重复导出（api/masterdata.ts:14 返回 ''、api/warehouse.ts:15 返回 undefined，空值行为不同）为既有 F6 遗留，使用方无同文件双导入，建议后续合并至 utils/ 并回归 10 个引用页面；
  - 数据中心：导入确认契约当前为同步返回 ImportConfirmResult，若后端改异步任务需补第 6 步任务轮询；导出「当前页/选中」范围暂以页码/ID 输入表达，各列表页导出按钮跳转预填待后续任务；盘点差异照片仅 Upload 占位（上传接口未交付）、无「新建盘点」入口（无 POST /api/counts 契约，创建为专门工作流页待后端交付后另开任务）；导入任务记录表未做自动轮询。

## [2026-10-02] 前端：第二批五组并行页面集成收口（认证对齐/库存余量/任务中心/出库作业/销售采购单据）

- **认证域真实接通**（共享文件唯一持有组，六文件全量对齐后端 auth 冻结契约，current.md:47 对齐清单销项）：types/permission.ts UserInfo 对齐 UserView/MeResult（snake_case、id 字符串形态）、canAccess 删空权限放行分支改 fail-closed（保留 is_super/super_admin/'*' 直通）、菜单两段码↔后端冻结三段码归一映射集中 RESOURCE_ALIASES（仅 stock→inventory、department→dept，config/menu.tsx 零改动）；api/auth.ts 改 LoginResult `{access_token,token_type,expires_in,refresh_token,must_change_password,user}`、新增 MeResult、改密入参 old_password/new_password、refresh 请求体 `{refresh_token}`、AuthSession 改 session_id/user_agent/login_at/last_active_at/current、sessions 返回分页信封、导出 PASSWORD_RULE（对齐 internal/auth/password.go:54-75）；stores/auth.ts 持久化结构同步（token/refreshToken/user/permissions/isSuper/mustChangePassword）并兜底旧/脏数据、新增 setMustChangePassword；api/client.ts 失败信封字符串错误码（AUTH_*/COMMON_ 等）提取进 ApiError.code，403+AUTH_PASSWORD_CHANGE_REQUIRED 特判不走通用无权限提示、置强改标志；LoginPage 登录后先 GET /api/auth/me 组装完整权限会话，must_change_password=true 先落强制改密表单（PASSWORD_RULE 校验）；PcLayout real_name 对齐 + 用户菜单新增「修改密码」Modal（强改模式不可关闭，成功后刷新 me 补全权限快照）。
- 五组并行 **15 页 + 5 个 api 模块** 统一接入路由（web/src/router/index.tsx）：
  - 任务中心 F8（/workbench /tasks；api/task.ts 前端先行契约：GET /api/workbench/summary、GET /api/tasks；工作台「我的待办/我的审批」无既有菜单路径，仅展示计数不跳转）；
  - 库存中心余量 F6（/inventory/batches /inventory/serials /inventory/transfers /inventory/trace；api/inventory.ts 纯追加 batches/serials/transfers/trace 四端点，字段对齐迁移 000005 batches/serial_numbers、调拨状态机对齐 business-flow §10.1、追溯对齐 inventory_ledgers 列）；
  - 出库作业 P0（/picking /checking /packing /shipment；api/outbound.ts 纯追加 /api/outbound/picking-tasks、/checking-tasks、/packing-tasks、/shipments 四端点，列对齐 business-flow §8.2–8.5）；
  - 单据域 P0（/sales /sales/outbounds /sales/returns；api/sales.ts 新建 GET /api/sales-orders、/api/sales/outbounds、/api/sales/returns；/purchases/receipts /purchases/returns；api/purchase.ts 纯追加两组端点，状态筛选对齐 business-flow §6.2/§7.2/§9.1/§2.1/§9.2）。
- IMPLEMENTED_PATHS 增补 15 条；程序化比对集合与 children 静态路由 **39↔39 完全一致**、无重复注册；15 条路径与 config/menu.tsx（:34/:40/:43-46/:58-59/:61/:64/:87-88/:96-98）逐条一致，menu.tsx 零改动。
- 跨文件一致性检查（集成阶段实际执行）：15 个新页面均 default export 且组件名唯一；六个 api 模块（auth/inventory/outbound/purchase/sales/task）导出标识符 `grep | sort | uniq -d` 无重名；15 页 fetch 调用与 api 方法定义逐一比对通过（salesApi.orders/outbounds/returns.list、purchaseApi.receipts/returns.list、outboundApi.pickingTasks/checkingTasks/packingTasks/shipments、inventoryApi.batches/serials/transfers/trace、taskApi.list/summary）；SfStatusTag 沿用「注册表优先 + label/semantic 三参兜底」，调拨 pending_outbound/transferring/pending_inbound 与序列号大写 DB 值（IN_STOCK/LOCKED/OUTBOUND/RETURNED/FROZEN）由页面映射正确渲染、未知值中性灰兜底。
- 验证：`npx tsc --noEmit -p tsconfig.app.json` 全项目 EXIT=0（并行期他组观察到的 client.ts:5 TS1127 与 PcLayout.tsx:293 报错均为半写入瞬时态，最终态已消除）；`npx eslint src/router/index.tsx` EXIT=0。按要求未运行全局构建（`npm run build` 留门禁阶段统一执行）。
- 影响范围：路由接线与文档；未改动各页面/组件/api 模块实现。
- 遗留对齐清单（后端任务/单据/outbound/库存查询域落地时处理）：
  - 后端任务域（workbench/tasks）、outbound 作业域、单据域（sales/采购收退）、库存四查询端点均未交付，相关 15 页呈统一错误态为预期行为（requirements.md §10）；
  - 前端先行契约待 M2 冻结回对：端点路径（/api/sales-orders、/api/sales/outbounds、/api/purchases/receipts、/api/outbound/picking-tasks 等与 docs/api.md 领域前缀 /api/sales、/api/picks、/api/receipts 口径不一，冻结时统一）、筛选参数（keyword/status/warehouseCode/taskType/transferType）、字段名（soNo/outNo/returnNo/receiptNo/pickTaskNo/packageNo/shipmentNo、totalQty/shippedQty/receivedQty/returnedQty/completedQty、poNo/soNo/outboundNo 关联单号）、WorkbenchSummary 四计数与各状态/任务类型枚举，均以后端 Go JSON tag 为准；
  - types/status.ts 暂不补录前端先行枚举（沿用上一轮集成决策）：调拨 pending_outbound/transferring/pending_inbound、序列号 in_stock/outbound/returned 等由页面三参兜底，后端枚举冻结后统一补注册；
  - 会话管理页未建：authApi.sessions/kickSession 已按新字段就绪，后续建页按 session_id/user_agent/login_at/last_active_at/current 消费并挂 auth:session:list/auth:session:kick 权限点（菜单码建议 system:session:view）；
  - 认证端到端链路（登录→403 AUTH_PASSWORD_CHANGE_REQUIRED 拦截→强制改密→重进系统）未实测（本环境无 PG15/Redis7/后端，5432/6379/8080 均不通），待联调环境补跑；localStorage `sf.auth` 旧格式会话会被 loadSession 拒绝，发布联调需重登一次；canAccess 的 view 动作映射为「资源可见」语义（持任意动作即放行菜单），如需严格 list/read 语义改 permission.ts view 分支一行；
  - 单据五页页头「导出/新建」为无 handler 骨架按钮（与既有 InboundPage/OutboundPage/PurchaseListPage 骨架一致），接详情/新建页时补真实动作。

## [2026-10-02] 前端：基础平台 F1–F5 + 库存中心示范页（阶段 4 前端部分）

- 新建 `web/`（StockFlow Web PC 管理端）：Vite 6 + React 18.3 + TypeScript strict + Ant Design 6.6.5（锁定 minor）+ react-router 7 + TanStack Query 5 + zustand 5 + axios + dayjs；`npm run dev/build/lint/typecheck`，Vite 代理 `/api → http://localhost:8080`（`VITE_API_PROXY` 可覆盖，端口 `VITE_PORT`）。
- Design Token（frontend.md §2.1）：`--sf-*` 全量变量（表面/边框/文字/状态色/圆角/阴影/间距）+ `[data-theme='dark']` 暗色映射（非纯黑、有层级）；ConfigProvider 全局 zhCN + antd Token 双向对齐（styles/tokens.css 与 App.tsx 同步维护）；Light/Dark 切换持久化。
- API 层（api.md §2）：axios 单例 + 统一信封 `{code,message,data,request_id}` 解包 + 分页 `page/pageSize/total/items` 类型 + ApiError（错误码/request_id）+ 401 清会话跳登录 / 403/404/5xx 用户可读文案 + 网络错误/超时识别；`src/api/` 按领域建模块（auth/dashboard/inventory/notifications）。
- 状态分域（frontend.md §18.1）：zustand 仅 auth/theme/ui 三分片；服务端状态全部 TanStack Query（retry=1、staleTime 30s）。
- PC Layout（frontend.md §4）：Sidebar 完整菜单树（§4.2 全部 12 组）+ 折叠 + <992px 自动折叠 + 权限点过滤预留；Header（面包屑、全局搜索占位、通知抽屉、主题切换、用户菜单）；页面头/内容紧凑密度。
- 统一组件族（frontend.md §23 首批）：SfTable（分页/密度/列设置持久化/全屏/刷新/上下文空态/统一错误态）、SfToolbar、SfSearchForm、SfBatchBar、SfPageHeader、SfStatusTag（§24 状态色唯一映射表 types/status.ts）、SfEmpty/SfError/SfLoading/SfDetailSection/SfSummaryBar。
- 页面：登录页（DEV 旁路 `VITE_AUTH_BYPASS=1` 仅开发构建生效）、403/404/500 统一错误页、Dashboard 四层结构（指标条/趋势图表/任务+预警/仓库分析，全部真实 API + Loading/Error/Empty）、库存中心示范页三张（实时库存含统计条、库存流水、库存预警，均接 Service 与分页筛选）、其余菜单路由全部落到诚实占位页；全局格式化工具（时间/金额/数量/百分比/文件大小）。
- 验证：`tsc -b` + `vite build` + ESLint 零错误；浏览器实测 1440/768 宽度、Light/Dark、错误态/空态/404（发现并修复 antd 6 空 Flex 无盒模型导致的工具栏布局问题、窄屏侧边栏折叠与用户名换行）。
- 影响范围：前端工程基线就绪，后续 F6–F13 按"usePagedList + SfTable + SfStatusTag + api 模块"模式接入；后端未就绪期间数据页显示真实错误态为预期行为。


## [2026-10-02] 后端：认证权限域全量交付（后端阶段 4，T2/scope C）

- `internal/auth` 整包（backend-m1-plan §5.1/§7 冻结契约，导出签名未变）：公开路由 POST /api/auth/login、POST /api/auth/refresh；受保护路由 /auth/logout、/auth/me、/auth/password、GET/DELETE /auth/sessions（在线会话管理/踢下线）与 /users（CRUD+启停/重置密码/解锁/绑定角色）、/roles（CRUD+启停/绑定权限）、/permissions、/departments（CRUD+启停），共 27 个接口，全部挂 RequirePermission、列表强制分页（api.md §2.1）、统一信封输出。
- 认证双轨（plan §7.2）：Access JWT（golang-jwt/v5，HS256，claims 含 uid/sid，算法白名单+exp 必填+issuer 校验）+ Refresh Token（256bit 不透明随机串，Redis 会话 sf:session:{sid}，滑动 TTL 默认 7d，刷新轮换 sid 并作废旧会话）；踢下线即刻 401 AUTH_SESSION_INVALID；刷新时重载数据范围快照（缩小 plan §13.3 漂移窗口）。
- 登录保护（permission.md §3.2、plan §7.3）：连续失败计数 Redis `sf:loginfail:{username}`（TTL=锁定窗口，默认 5 次锁 15 分钟、SF_AUTH_MAX_LOGIN_FAILURES/SF_AUTH_LOCK_DURATION 可配），锁定落库 users.locked_until（进程重启不丢锁），PUT /api/users/{id}/unlock 提前解锁并清计数；每次登录/刷新/登出写 login_logs（§3.3）；未知用户与密码错误统一 AUTH_CREDENTIALS_INVALID + 恒定代价假 bcrypt 比较防枚举；bcrypt cost 12；强密码策略（≥8 位含字母数字、≤72 字节防 bcrypt 截断）；管理员首登 must_change_password 强制改密门禁（403 AUTH_PASSWORD_CHANGE_REQUIRED，白名单 password/logout/me，后两者为交付披露的放宽）。
- RBAC 与数据权限（permission.md §2/§4）：AuthRequired 校验 JWT+会话并注入 UserContext/会话快照；RequirePermission 走 Redis 权限缓存（sf:perms:{uid}，TTL 10min）未命中回源 DB，RBAC 变更路径定向失效，缓存/DB 故障 fail-closed（503 拒绝而非放行）；权限点常量清单 80 个动作点与 plan §5.4.1 冻结清单及权限种子同源（permissions.go）；WarehouseScope/ApplyWarehouseScope/SelfScope/DepartmentScope 数据权限过滤助手供三个业务域使用（ALL/超管=全部、SPECIFIED_WAREHOUSE=绑定仓库集、DEPARTMENT/SELF/SELF_IN_CHARGE 按 plan §7.4 M1 返回空集不放大）。
- 敏感操作审计（permission.md §6、architecture.md §8）：用户创建/更新/启停/重置密码/解锁/绑定角色/改密、角色变更/权限绑定、部门变更/启停、强制下线全部经共享 helper 在业务事务内写 operation_logs（before/after 快照不含任何密码值）；`internal/middleware/audit.go`（§4.4 共享审计 helper：AuditEntry/Audit + login_logs 模型与 WriteLoginLog）属 scope A 漏交文件，经 Orchestrator 裁决由本域按冻结形态代交（AuditEntry 增补 UserAgent/Method/Path 三字段以对齐 000002 迁移列与 architecture §8.1"设备/请求"要求）。
- 运行配置（SF_AUTH_* 环境变量，auth 不 import config 的落位说明见 doc.go）：SF_AUTH_JWT_SECRET（release 缺失启动失败/debug 降级进程内随机密钥）、SF_AUTH_ACCESS_TTL（2h）、SF_AUTH_REFRESH_TTL（168h）、SF_AUTH_MAX_LOGIN_FAILURES（5）、SF_AUTH_LOCK_DURATION（15m）；BootstrapIfEmpty 按裁决委托 internal/database.BootstrapIfEmpty。
- 测试：57 个用例/48 断言点全绿（JWT 签发/过期/篡改/算法混淆/缺 claims、bcrypt 往返与策略矩阵、权限判定与 fail-closed、登录保护计数与锁定、会话轮换/踢下线、改密闭环、RBAC 校验、超管保护、自停用保护、部门级联与成环），全部经内存 redisStore/fakeRepo/假 gorm 方言器替身实现、零外部依赖；真实 PG+Redis 集成测试置于 //go:build integration（SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控，未在本环境执行）。
- 已知限制与披露：①users/roles/departments/permissions 的 code 唯一性中，除 users（部分唯一索引）外 roles/permissions/departments 的唯一索引在 000001 迁移中缺失（down 脚本却引用 uk_roles_code 等），Service 层以查询前置校验兜底、并在 seed 的 ON CONFLICT (code) 处存在真库首启失败风险，需 scope B 补迁移索引；②WithWarehouseChecker（plan §4.3 用户绑定仓库校验）需 router T6 注入，未注入时真实装配启动 fail-fast、测试装配豁免；③swag 注释随 T6 汇总补齐；④go test -race 因本机无 C 编译器（CGO）未执行，待 CI 补跑。
- 影响范围：认证权限域为 M1 三业务域提供唯一鉴权入口与数据权限助手；go.mod 新增直接依赖 golang-jwt/jwt/v5 v5.2.0。


## [2026-10-02] 数据库：M1 迁移全集 + 生产安全初始化（后端阶段 3，T1/schema 冻结点）

- `db/migrations/` 五组成对迁移（000001–000005，up/down 对称可回滚，PostgreSQL 15 方言，golang-migrate）：auth 域（users/roles/permissions/departments + user_roles/role_permissions/user_warehouses，users 软删部分唯一索引）、审计（operation_logs/login_logs，纯日志无 updated_by/deleted_at）、masterdata（product_categories/units/products/skus/barcodes/suppliers/customers，business-flow §1.1–§1.5 全量字段）、warehouse（warehouses/zones/shelves/bins 四级结构域内 FK）、inventory（batches/serial_numbers/inventory/inventory_locks/inventory_ledgers/inventory_adjustments），共 26 表，严格按 backend-m1-plan §6 冻结契约（system_configs/dictionaries/notifications 按 §9 裁决不建，随 M2+ 域迁移交付）。
- 库存硬约束落库：inventory 六列恒等式 + 非负由 `chk_inventory_identity` CHECK 强制（inventory-rules §2）；五维唯一索引 `uk_inventory_location (warehouse_id, bin_id, sku_id, batch_id)`（batch_id NOT NULL DEFAULT 0=非批次，serial 经 serial_numbers 一物一行）；流水幂等键部分唯一索引；流水 append-only 无更新字段；数量/金额一律 numeric(18,4)；本域无跨域外键（plan §6.4）。
- `db/grants/app_grants.sql`：审计数据账号分层（database.md §7.2）——业务运行账号对 inventory_ledgers/operation_logs/login_logs 仅 SELECT+INSERT，无 UPDATE/DELETE/TRUNCATE。
- `db/seed/dev_seed.sql` + `make seed-demo`（database.md §8.2）：演示数据与生产初始化完全分离——Makefile 要求 `SF_ENV=dev`，SQL 内 `is_dev` 门禁双保险，幂等可重跑；覆盖仓库四级结构/商品/SKU/条码/供应商/客户/批次/期初库存/序列号演示数据。
- `internal/database/seed.go`：生产安全初始化 `BootstrapIfEmpty`——仅 users 空库执行、事务 + pg_advisory_xact_lock 防并发首启、全量 ON CONFLICT 幂等不覆盖；种子：16 内置角色（permission.md §1，is_system 禁删）、102 个权限点（plan §5.4.1 冻结的 80 个动作点 + 22 个 MENU 菜单，MENU/BUTTON/API 三级）、四角色权限映射（plan §7.1）、默认管理员（bcrypt cost 12、must_change_password=TRUE，密码经 SF_ADMIN_INITIAL_PASSWORD 注入，缺失/弱密码启动失败且绝不写日志）、默认仓库示例；`cmd/server/main.go` 调用位接线（auth/warehouse 的冻结 stub 签名未动）。
- 结构自查测试（不依赖 PG/Redis/网络）：迁移成对与名称对称、down 与 up 建表逆序对称、26 表契约、通用字段与软删除范围（§3/§5.1）、恒等式 CHECK/五维唯一/部分唯一索引、timestamptz 方言、命名规约（uk_/idx_/chk_/fk_）、grants/seed 文件存在性与门禁；种子侧冻结清单逐字核对、菜单父先子后、角色映射规则、密码策略与 nil-db 快速失败、bcrypt 往返。
- 已知限制：本环境无 PostgreSQL，迁移未真实执行升/降级验证（database.md §9 的 `migrate up + down 1` 双向验证须在具备 PG 的环境补做）；运行期行为（advisory lock/ON CONFLICT/部分唯一索引推断）依赖 PG 15 语义，未经真库回归。

## [2026-10-02] 前端：六组并行页面集成收口（基础资料/仓库中心/系统管理/库存扩展/作业与采购骨架）

- 六组并行开发的 20 个页面 + 6 个 api 模块统一接入路由（web/src/router/index.tsx）：
  - 基础资料（/products /skus /categories /units /suppliers /customers，六页 CRUD 对齐 backend-m1-plan §5.4）；
  - 仓库中心（/warehouses /zones /shelves /bins /warehouse-map，四级结构 + 库位地图网格，对齐迁移 000004；zones/shelves 按契约无删除、走启停）；
  - 系统管理（/system/users /system/roles /system/permissions /system/departments，用户含启停/重置密码/解锁/分配角色，角色含权限树绑定，部门树形 CRUD）；
  - 库存中心扩展（/inventory/locks /inventory/adjustments，api/inventory.ts 增 locks/adjustments 两端点，值域对齐迁移 000005 + inventory-rules §4）；
  - 仓储作业骨架（/inbound /outbound，api/inbound.ts、api/outbound.ts，对齐 business-flow §3/§7）；
  - 采购订单骨架（/purchases，api/purchase.ts，对齐 business-flow §2）。
- IMPLEMENTED_PATHS 同步增补 20 条路径，占位路由自动收敛；config/menu.tsx **零改动**（各菜单 path 与页面路由天然对齐，权限点沿用菜单树既有值）。
- 契约修正确认（api/auth.ts）：profile→GET /api/auth/me、changePassword→PUT /api/auth/password，新增 POST /api/auth/refresh、GET /api/auth/sessions、DELETE /api/auth/sessions/{id}；grep 确认全项目 authApi 调用方仅 PcLayout（logout）与 LoginPage（login），修正无破坏。
- 跨文件一致性检查（集成阶段实际执行）：20 页面均 default export；页面调用的全部 api 方法与模块定义逐一比对通过；toStatusKey 在 api/masterdata.ts 与 api/warehouse.ts 重复导出但无同文件双导入（语义按模块各自约定，保留）；rbac.ts 复用 user.ts 的 CommonStatus 无重复定义；SfStatusTag 三参兜底（status+label+semantic）签名与库存锁定/调整页用法匹配；types/status.ts 已注册库位六态（idle/partially_occupied/full/locked/frozen/abnormal），库位地图语义映射可用；api/types/config 层无反向依赖 views，无循环依赖。
- 验证：`npx tsc --noEmit -p tsconfig.app.json` 全项目 EXIT=0（strict 通过）；`npx eslint src/router/index.tsx` EXIT=0。按要求未运行全局构建（`npm run build` 留门禁阶段统一执行）。
- 影响范围：仅路由接线与文档，未改动各页面/组件/api 模块实现。
- 遗留对齐清单（后端 M1/M2 落地时处理）：
  - 后端各域（masterdata/warehouse/auth/inventory/作业单据）仍为 stub，页面请求呈统一错误态为预期行为；
  - 列表筛选参数名（keyword/status/categoryId、warehouseId/zoneId/shelfId、departmentId/type 等）与启停端点（PUT /api/{warehouses|zones|shelves|bins|users|roles|departments}/{id}/status、PUT /api/users/{id}/reset-password 等）为前端先行提案，后端实现时对齐；
  - GET /api/warehouses/{id}/map 响应结构与 bin 占用状态值域、create/update 响应体、/api/auth/refresh 与 sessions 返回体、SKU spec_attrs 键结构均为前端先行契约，待冻结；
  - types/status.ts 未收录 active/released/consumed/executed/partially_received 等键，页面已按 SfStatusTag 三参兜底（不阻塞），建议后端枚举冻结后统一补注册；
  - 选项下拉 pageSize=200 一次取全（商品/分类/单位/仓库/库区/货架/角色）在后端就绪后需确认分页上限或提供轻量选项端点。
