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

