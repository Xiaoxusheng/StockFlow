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
