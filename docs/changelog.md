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

## [2026-10-04] 修复：部署回归实测修复——000013 CHECK 错置、000015 非法索引、文件中心容器存储权限

- **db/migrations/000013**：`chk_device_logs_level CHECK (level IN ('INFO','WARN','ERROR'))` 误置于 `device_configs` 表（该表无 `level` 列，任何 PostgreSQL 上迁移必然失败——生产首启实测复现 `pq: column "level" does not exist`）；按 internal/devices/models.go 值域注释移回 `device_logs` 建表语句。
- **db/migrations/000015**：移除 `idx_inventory_ledgers_creator_created (created_by, created_at)`——`inventory_ledgers` 无 `created_by` 列（操作者列为 `operator_id`，000005），且流水列表 `LedgerQuery`（internal/inventory/query.go）无创建人筛选；其余 8 张主单据表 `created_by` 索引核实有效保留。down 同步。
- **部署物**：Dockerfile 预建 `/app/data` 并 `chown app:app`（M3 文件中心 storage.root 默认 `./data/files`，非 root 容器内 mkdir 失败导致启动 panic）；docker-compose.yml app 服务新增命名卷 `filesdata:/app/data` 持久化文件中心与 sysops 备份物理文件。
- 本地未暴露的原因：集成测试走 GORM AutoMigrate 建表，不执行 SQL 迁移文件；门禁 `go build/vet/test` 不加载迁移 SQL。两处缺陷均由服务器真库迁移首启暴露。
- 同步更新：deployment.md §10（filesdata 卷与 bind mount 属主要求）。
- 影响范围：全新库首启迁移路径修复；已应用 000013/000015 的环境不存在（生产库首启即本次部署）。

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

## [2026-10-04] 构建：阶段 20 CI 关卡（GitHub Actions 与本地门禁同源，补 race 缺口）

- 新增 `.github/workflows/ci.yml`：push 与 pull_request 触发，ubuntu-latest 三个并行 job——**lint**（gofmt -l cmd internal 有输出即失败（Makefile fmt-check 同款）+ go vet ./... + go vet -tags integration ./...）；**test**（go test -count=1 ./... 与 go test -race -count=1 ./...——补齐 2026-10-03 M3 基座起挂账的"本机无 gcc 缺 race 关卡"复验项）；**guards**（make guard-status guard-docnum guard-inventory guard-asynq guard-readonly guard-datax guard-devices，守卫命令单一来源在 Makefile、与清偿轮 F3 接线一致，workflow 不复刻防漂移；guard-status 沿 Makefile 语义仅输出人工复核清单不失败）。
- Go 版本读 go.mod（setup-go go-version-file）；go mod download 缓存经 setup-go cache: true（go.sum 键控）；permissions 收敛 contents: read；单测零外部依赖（testing.md 约定）故 CI 无需 services。
- 本机验证：actionlint v1.7.12 通过；js-yaml 解析 + 结构断言全过；gofmt -l cmd internal 空；go vet ./... 与 go vet -tags integration ./... 全过；go test -count=1 ./... 24 包全 ok；六项失败型守卫零命中，guard-status 输出 7 行既有人工复核清单（抽查 internal/datax/service_recover.go:58 WHERE 守卫在下一行，属 Makefile 注释所述跨行情形，报告不失败）。go test -race 本机不可跑（无 gcc、CGO_ENABLED=0），由 ubuntu-latest 承载——**待 push 后首次 Actions 运行验证**。
- 影响范围：仅新增 .github/workflows/ci.yml 与文档回写（deployment.md §7 升 v1.3、本文件、tasks/current.md）；业务代码零改动。

## [2026-10-04] 后端：清偿轮（核对员债务清单 F3–F21 逐项处置 + 文档回写，T6 正式收口）

- **F3 守卫接线**：Makefile 新增 `guard-inventory`（backend-m1-plan §8.6 第一道——库存族六表写 SQL 仅限 internal/inventory 的非测试代码，_test.go fixture 豁免同 guard-readonly/guard-datax 口径，实测 inventory 外非测试代码零命中。同日复核修正：初版未豁免 _test，被同轮 F7 集成测试 fixture 的 INSERT 命中，已补豁免与注释）并接入 `ci`；rg 14.1 前缀排除 glob 在 Windows 反斜杠路径失配（`rg --debug` 证实），落地为 grep+grep -v 可执行形态、等价 rg 命令与豁免说明（docs/、db/）写入注释。
- **F4**：删除仓库根 Windows 保留名杂散文件 `nul`（0 字节，已 gitignore 未跟踪）。
- **F7**：新增 `internal/auth/integration_datascope_test.go`（`//go:build integration`，testing.md §8「越权访问他人仓库数据→数据权限过滤生效」专项）：双仓 + 双 SPECIFIED_WAREHOUSE 账号 + B 仓库存/调整单数据，经真实登录→Redis 会话→AuthRequired→ApplyWarehouseScope→真实 PG 全链路断言 A 仓账号仅见 A 仓、指名 B 仓查询为空集；复用 integrationEnv fixture。编译/vet 通过，执行需 SF_TEST_PG_* 测试库（本机无，未运行——诚实标注）。
- **F8 swag 收口（T6 最后一项）**：全部 handler doc 注释补最小集注解（@Summary/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Router）；go.mod 引入 github.com/swaggo/swag v1.16.6（apidocs 编译依赖）；Makefile 新增 `swag` 目标（go run 固定版本 CLI，`--parseDependency --parseDepth 3`）；新增 `apidocs/`（docs.go + swagger.json/yaml，操作数 269 与 gin 运行时路由表 r.Routes() 逐一核对一致：GET 118/POST 88/PUT 55/DELETE 8，不编造路由）；response 包新增 Envelope 文档形态结构体（api.md §2 信封，仅注解引用）；api.md §3 补生成机制说明；生成器脚本 scripts/gen_swag_annotations.py 留档。
- **F9**：GET /api/users 的 department_id 非法值由静默归 0 改为 fail-fast——auth 域新增 queryID 助手（对齐 warehouse 模式），非法值返回 COMMON_INVALID_PARAM。
- **F11**：seed.go 权限种子循环抽为 seedPermissions（permissionSeedGateway 注入）；残留场景（users 空但 permissions 有残留父行）改为幂等跳过并回读 id 回填 code→id 映射，子权限不再误判「父级未就绪」；新增 TestSeedPermissionsResidualRowsIdempotent 与 TestSeedPermissionsMissingParentFails（内存假网关，零 PG）。
- **F12+F15**：新增成对迁移 000015_tighten_ledger_and_indexes——inventory_ledgers.zone_id/shelf_id 收紧 NOT NULL（PG SET NOT NULL 全表扫描断言，遇 NULL 整体失败回滚；文件头注明写入恒填与生产首启前执行安全）+ 九张关键业务表 created_by 复合索引（database.md §6.1「创建人」筛选面）；database.md §6 补两条注记。
- **F18**：新增 internal/auth/config_test.go 表驱动测试——release 模式缺 SF_AUTH_JWT_SECRET 报错、<16 字节弱密钥三种模式全报错、debug 缺省降级进程内随机密钥且两次解析互异（t.Setenv 控制环境）。
- **F20**：internal/response 新增 BindErrorDetails 助手（validator.ValidationError → 字段级固定中文文案 map；JSON 语法错误 → 固定「请求体格式错误」，BindErrorReason 常量）——auth 15 处、sales 12 处、devices 7 处直出点与 masterdata/purchase bindJSON、warehouse bindInput、stockops 直写共 38 个 ShouldBindJSON 绑定路径收敛，不再透传 err.Error() 内部错误串；同日复核修正补收敛漏网 bind 路径 14 处（printing handler 5 处、returns handler 8 处、stockops bindJSON 助手 1 处/9 个调用方），合计 52 处全部收敛；reports/sysops 的查询参数解析与各 service 层数量/时间解析等真正非 bind 路径不在本项范围、保持原状（初稿误将 returns handler 8 处 bind 归入此类，已更正）。
- **F21**：middleware/recovery.go 的 zap.Any("panic", r) 改为 zap.String（fmt.Sprintf("%v") 归一 + 复用 truncateStr 按 2KB 截断，maxPanicLogBytes），堆栈保留 zap.ByteString。
- **文档回写**：current.md 头部「当前任务」指向后端主线交付完毕与清偿轮现状（F5），scope A/T6 待办行与阶段 3–8 行 T6 双双销项（Checker 已装配 router.go:100 + 本轮 swag）；testing.md §3 补集成测试实际机制披露、backend-m2/m3-plan 四处与 backend-m1-plan §2/§8.6、backend-analysis 技术选型表共七处 testcontainers 措辞加注（实际 = SF_TEST_* 门控外部实例，F16；后三处为同日复核修正补齐）；deployment.md 版本头升 v1.2 并补 §10 修订行（NEW-1）；backend-m2-plan §8.2 补录九原语清单（F6 重验落位）；backend-m2-plan §6.2 serial_no NOT NULL DEFAULT '' 与 DDL 000009:124 核验一致（F14，无需改动）。
- 影响范围：internal/{response,auth,sales,devices,masterdata,purchase,warehouse,stockops,printing,returns,database,middleware,health} 最小修改、cmd/server/main.go 通用注解、Makefile、go.mod/go.sum（swaggo + tidy 归类）、db/migrations/000015 成对、apidocs/ 新增、scripts/gen_swag_annotations.py、docs/{api,database,deployment,testing,changelog,tasks/current} 与 plans 两份。
- 验证：go build ./... 全过；go test -count=1 ./internal/{database,response,auth,sales,devices,masterdata,purchase,warehouse,stockops,middleware} 全 ok；guard-inventory 零命中（_test 豁免口径）；swag init 重跑稳定 269 操作；go vet -tags integration 过（数据权限集成测试编译在位）。未覆盖：集成测试实际执行与迁移 000015 真库双向验证需服务器 PG+Redis（沿 M3 未覆盖清单）；make 本机不可用，Makefile 语法目视 + 各命令已单独实测。

## [2026-10-04] 后端：清偿轮复核修正（独立复核 5 项——F3 守卫误伤 / F20 漏收敛 / 000015 down 缺口 / 状态过誉 / F16 残留）

- **F3×F7 守卫互斥（high）**：guard-inventory 未豁免 _test.go，被同轮 F7 集成测试 fixture（internal/auth/integration_datascope_test.go:69-78 四处 inventory/inventory_adjustments INSERT）命中致 make ci 必挂——补 `grep -v "_test"` 豁免（对齐 guard-readonly/guard-datax 口径）并在 Makefile 注释说明；复测 inventory 外非测试代码零命中。
- **F20 补收敛 14 处（medium）**：printing handler 5 处、returns handler 8 处（paramError("body", err.Error()) 直透）改经 response.BindErrorDetails；stockops bindJSON 助手去 `"error": err.Error()` 直写改 BindErrorDetails（9 个调用方同享）；合计 52 个绑定路径全收敛。reports/sysops 查询参数解析等真正非 bind 路径维持原状（初稿对 returns 8 处的归类有误，已更正）。
- **000015 down 补齐（medium）**：补 `DROP INDEX IF EXISTS idx_transfer_orders_creator_created`（up 建 9/down 删 8 的缺口），九索引回滚成对；再修（low）：列注释以 `COMMENT .. IS NULL` 移除（000005 本无 zone_id/shelf_id 列注释，原 down 重设『库区 ID/货架 ID』为残留），up→down 循环后与 000005 原态完全一致。
- **F16 补注（low）**：backend-m1-plan §2/§8.6 与 backend-analysis 技术选型表三处 testcontainers 措辞加注，与 testing.md §3 披露对齐。
- **状态文件更正（low）**：current.md 清偿轮摘要补记本修正轮；changelog F3/F20 条目更正完成口径。
- 复验：go build ./...、go vet ./...、go vet -tags integration ./...、go test ./... 全量全绿；guard-inventory 等价管道通过；gofmt -l 干净。

## [2026-10-04] 后端：M3 平台能力全量交付（阶段 14–19：Excel 数据中心/打印中心/设备与扫码/报表与系统运维 + 迁移与异步基座）

- **平台基座**：db/migrations 000011–000014 成对迁移共 17 张平台表（datax/printing/devices/sysops 四组，无跨域外键、状态 CHECK、幂等键部分唯一索引、grants 审计分层追加）；internal/asynqx 异步任务基座（Queue 接口 + asynq/inline 双实现 + 任务类型冻结注册表 + FailureFinalizer 终态契约）；internal/sysops cron 基座（robfig/cron、五任务注册表、single-flight+advisory lock 双闸防重入、执行日志落 scheduled_jobs）；internal/storage 文件存储层（扩展名/MIME 白名单交叉校验、服务端重命名、路径穿越防护）；internal/docnum 扩展 IMP/EXP/PT 前缀；config 九键 + main.go asynq/cron 生命周期接线。
- **Excel 数据中心（internal/datax，21 文件）**：九类导入向导状态机（模板现场生成/流式解析/结构+业务双层校验管线含行列定位与错误 Excel/预览/确认二次守卫/断点续跑/进度推导）、16 模块异步导出（keyset 游标 StreamWriter 流式写、合计行、每批进度回写）、文件中心（白名单上传/流式下载+审计/预览/软删/过期拒绝/仓库范围可见性）、12 个域包接入文件（9 ImportWriter + 15 ExportSource，全部经各域 Service 通路写库）。
- **打印中心（internal/printing）**：模板 CRUD/复制/启停、任务创建+模板快照冻结+500 行上限、渲染数据包（react-to-print 消费，无服务端 PDF——Orchestrator 裁决③）、render 状态机接 asynqx、/api/prints/barcode 七码制 PNG+有界缓存、5 个域包 printing_content.go 内容装配接入（经裁决启用只读装配白名单：单据表+名称展示列 JOIN，库存族表零访问）。
- **设备与扫码（internal/devices，15 文件）**：设备注册/一次性激活码（SHA-256 落库、15 分钟时效、单条守卫 UPDATE 防重放）、设备令牌（HS256 JWT did/typ/tver + token_version 撤销）、绑定/心跳（3 分钟窗口）/配置下发（version 单调）/App 版本、/api/scanner/resolve 统一解析管线（docnum 前缀分派→SKU 条码→库位→序列号→批次→UNKNOWN，scan_logs 审计写失败降级放行、§6.6 去重窗口）、7 个域包 devices_resolve.go 只读匹配器。
- **报表与系统运维（internal/reports + internal/sysops）**：/api/reports 七端点 + /api/inventory/summary|alerts（现场只读聚合、366 天上限、仓库数据权限过滤）、补货建议/积压识别只读建议附计算依据（裁决：不做汇总表）、操作/登录日志只读查询、/api/system 监控（DB/Redis/连接池/版本/运行时长——gopsutil 豁免至阶段 21，裁决①）、备份登记+pg_dump 模板（外部宿主执行，裁决②）、预警扫描三任务+task_timeout+file_cleanup 全部接线（阈值经 system_configs 冻结键，与 reports 同键同值）。
- **集成装配**：router 静态装配五域（asynqx Runtime 注入、跨域窄接口闭包桥接、设备端免 AuthRequired 组、MetricsMiddleware、上传路由 route-aware 限宽）；41 个 M3 权限点收编 auth/permissions.go 单一来源；seed.go 扩至 63 MENU + 229 动作点（探针测试对齐）；Makefile 追加 guard-asynq/readonly/datax/devices 四守卫；backend-m3-plan §11.4 收编记录与偏差注记（文档先行）。
- 影响范围：internal/{asynqx,datax,printing,devices,reports,sysops,storage} 新增、inventory/masterdata/sales/purchase/returns/stockops/warehouse 按冻结清单仅新增接入文件、router/main.go/config/Makefile/db+grants、backend-m3-plan 与本文件；技术栈新增依赖均在 architecture.md §11.2 基线清单内（asynq/excelize/barcode/cron），无基线变更。
- 验证：go build / go vet / gofmt / go vet -tags integration 全过；go test -count=1 ./... 24 包全 ok（datax 26 单测、printing 26、devices 24、returns 14 等全内存替身零 PG/Redis）；六项守卫 grep 零命中；迁移结构自查扩至 72 表封闭断言。未覆盖：迁移/asynq 真队列/cron 未在真实 PG+Redis 执行（//go:build integration 待服务器环境）、-race 本机无 gcc 未跑、设备真机联调属阶段 16 现场。

## [2026-10-04] 后端：M3 独立评审修复轮（11 项复核——2 项高危安全/契约 + 异步任务终态闭环，backend-m3-plan §4.2/§4.3/§6.4/§13.6）

- **异步任务重试耗尽终态闭环（评审①/④，high/medium）**：asynqx 新增 FailureFinalizer 失败终态回调注册表（RegisterFailureFinalizer）——asynq Server mux 在 handler 失败且已重试次数 ≥ MaxRetry（asynq GetRetryCount/GetMaxRetry，重试耗尽即将归档）时触发回调；inlineQueue 同步降级在 handler error 即触发（inline 无重试，本次失败即最终失败）。datax（FinalizeImportFailure/FinalizeExportFailure：任务行守卫落 FAILED + error_message + 审计 + sysops.NotifyTaskFailure 站内告警）与 printing（FinalizeRenderFailure）三类任务全部注册——修复重试耗尽/Redis worker 故障后 import_tasks 永久卡 EXECUTING、export_tasks/print_tasks 永久卡 PROCESSING、用户侧永远"处理中"且无告警的契约违约（plan §4.2"重试耗尽 → 终态 FAILED + error_message + 站内告警"落地；inline 静默吞任务同步修复——任务行不再悬挂）。
- **设备单点端点数据权限（评审②，high）**：devices 新增 WarehouseScope（visible fail-closed）与 loadVisibleDevice 统一闸口——详情/激活查询/激活码重生成/绑定/解绑/停用/配置下发/设备日志全部按 auth.WarehouseScope 仓库范围校验设备 warehouse_id，不可见按 DEVICE_NOT_FOUND 处理（防跨仓 ID 枚举）；与列表过滤同源口径（permission.md §4 数据权限在 Service 层过滤），跨仓 warehouse_manager 无法再枚举停用/改配置他仓设备。
- **导入断点续跑计数双算（评审③，medium）**：RunImportCommit 计数种子修正——failedRows 从 0 起算（本轮重试清单含全部 FAILED 行，行面必然收敛），successRows 取执行前 SUCCESS 行数；重试行再失败不再双倍（50→100）、改成功不再 success+failed>total_rows；新增回归测试 TestImportCommitFailedRowsNotDoubleCounted。
- **导出真流式落盘（评审⑤，medium）**：exportWorkbook.FinalizeTo(io.Writer) 经 excelize WriteTo 逐部件流式写出——RunExportRun 落临时文件后按 os.Stat 校验 ExportMaxFileBytes 再经 store.Save 原子转存，废除 WriteToBuffer 整簿物化为内存 []byte 的路径（excel.md §3 流式口径在序列化阶段达成，并发导出内存不随簿大小成倍放大）。
- **文件中心数据权限（评审⑥，medium）**：FileScope 冻结可见性规则（全量范围=全部；范围受限=本人上传 ∪ datax 导出产物经 export_tasks.params 仓库范围快照相交 ∪ 导入任务产物经创建人——import_tasks 无仓库快照列按创建人收敛）——列表 SQL 侧过滤（分页 total 一致）+ 下载/预览/删除/导出产物/导入错误明细单文件校验，不可见按不存在处理；修复任何持 datax:file:list/read 者跨仓浏览/下载他人业务附件（plan §6.4"file 列表 Service 层过滤"落地）。
- **task_timeout_scan 落地（评审⑧，low）**：sysops 内收口拣货/上架任务超时扫描（pick_tasks/putaway_tasks 只读聚合，dedup=tasktimeout:{任务号}，预警类收件人）；补 plan §10.2 冻结而缺失的 seed 键 task.timeout.pick_hours=4 / task.timeout.putaway_hours=4——注册表五任务全部接线，MT6 注记②"空实现位"偏差消除。
- **reports 阈值/补货参数接线（评审⑨，low）**：sysops 导出只读桥 ReadConfigValue/ReadConfigInt/ReadConfigIntList + 冻结键常量，router 经 WithReplenishmentParams/WithAlertThresholds 注入 system_configs（行缺失回退 7/3、30,15,7,3、30,60,90 冻结缺省）——/api/inventory/alerts、/api/reports/stagnant-stock 与 sysops 预警扫描同键同值，两套口径不再漂移。
- **INITIAL_INVENTORY 仓库范围（评审⑦，low）**：期初库存导入写入器 commitRow 按 Actor.AllWarehouses/WarehouseIDs 快照校验行目标仓库（越界行级失败 INVENTORY_WAREHOUSE_SCOPE_DENIED 403，fail-closed）——plan §13.6"写入器据此构建数据权限 Scope"落地，仓库受限用户无法向任意仓库导入期初库存。
- **预警扫描 N+1（评审⑩，low）**：低库存/效期/积压/任务超时/备份超时扫描收件人解析提到循环外（resolveRecipients 一次 + notifyRecipients 逐行落库），数百预警行不再产生数百次重复角色→用户查询；notifyRoles 注释限定为单条/低频入口。
- **notifications/watch 小缺陷（评审⑪，low）**：truncateContent 按字节限长回退 UTF-8 rune 边界（不再切断多字节字符产生非法序列）；file_cleanup 备份超时核对与过期文件清理的 SELECT...FOR UPDATE 包进事务（行锁持有至回写/删除完成，防并发核对名存实亡）。
- **测试**：新增 TestFailureFinalizer（asynqx 终态回调机制）、TestDeviceScopeVisibility（devices 跨仓 404）、TestFileScopeVisibility（文件中心范围规则）、TestImportCommitFailedRowsNotDoubleCounted（计数收敛）；go build/vet/test 全绿，guard-asynq/readonly/datax/devices 守卫不受影响。
- 影响范围：后端 internal/asynqx、internal/datax、internal/devices、internal/printing、internal/inventory、internal/sysops、internal/reports（无）、internal/router、db 无迁移变更（system_configs 新键经 ensureSystemConfigKeys 幂等补行）。

## [2026-10-04] 前端：共享层 API 契约回对（14 个 api 文件对齐 M2/M3 已挂载后端 + options 映射助手 + 状态注册表键位修正 + 通知抽屉字段回对）

- **API 契约回对（后端形态以本次亲读的 Go JSON tag 为准）**：sales（/api/sales 三路径 + /api/returns 退货视图）、outbound（/api/outbounds + {no} 任务族详情 + /api/picks|checks|packing|shipments 四作业与 claim/confirm/exception/reopen/pack/ship/status 动作）、purchase（/api/purchases + /api/receipts + /api/purchase-returns，收货行载荷按 ReceiptLineInput 全字段含 serials/异常字段/target_bin_id）、inbound（/api/inbounds 五态 AWAITING_QC/AWAITING_PUTAWAY 回对）、count（五态状态机 + 批量实盘登记 {items:[{InventoryRowID,SerialNo,Qty}]} + start/finish/complete/reject/cancel 五动作 + scope 对象载荷，权限码 stockops:count:*）、quality（/api/quality，inspection_type/result 中文值域）、transfer（TransferOrderView 七态含 APPROVED=待出库/AWAITING_RECEIPT=待入库 + 动作端点 + POST /api/inventory/moves 仓内移库）、inventory（StockSummary/StockAlertItem 回对 reports 域 snake_case）、data（datax TemplateSpec/校验/预览/确认/任务记录 snake_case + 模板与错误文件与导出产物下载封装 + /api/data-tasks/{import,export}/{id}）、file（FileItem snake_case + /{id}/preview）、printing（TemplateView/TaskView/HistoryItem snake_case + tasks/{id} 详情 + execute 确认 + /api/prints/barcode PNG；删除对不存在端点 GET /api/prints/{id}/pdf 的回退）、device（DeviceView 裸模型 + online，激活双状态字段，warehouse_name/bound_user_name 改经 options 映射）、system（logs/configs/jobs/monitor 逐字段回对 sysops 契约，monitor 全量重写 snake_case）、notifications（created_at 回对）；全部权限码常量改为后端冻结三段式（如 stockops:count:create、sales:sales:create、system:log:list）。
- **新增 api/options.ts**：仓库/客户/供应商/SKU/库位/用户 options 一次取全（分页取全 + 防御上限）与 id→编码/名称 Map 构建助手（对齐 PadInventoryPage 既有映射模式），供后端 ID 形态视图本地映射展示。
- **types/status.ts 键位修正**：调拨段清理前端虚构值域 pending_outbound/pending_inbound，增补 awaiting_receipt（approved/pending_approval 复用通用键）；盘点段清理 pending_execute；通用段增补 pending_approval。
- **NotificationDrawer**：item.createdAt→item.created_at 字段回对，类型色映射键同步后端大写枚举值域（不改交互）。
- 影响范围：web/src/api/、web/src/types/status.ts、web/src/layouts/NotificationDrawer.tsx——各业务视图对旧字面量的引用由各模块页面实现轮承接更新；本层不含任何 mock 数据，dashboard.ts 不动（五个 /api/reports/dashboard/* 端点仍未注册）。

## [2026-10-03] 后端：M3 平台基座交付（MT0 基座段：迁移 000011–000014 + asynqx 队列基座 + cron 框架 + 文件中心存储层，backend-m3-plan §3–§6）

- **迁移 000011–000014 成对交付（17 表，backend-m3-plan §5 冻结 DDL）**：000011 datax（import_tasks 六态向导状态机/import_task_rows 行级断点续跑/export_tasks 16 模块五范围/files 文件中心登记）；000012 printing（print_templates 九对象模板/print_tasks 模板快照冻结与执行确认回填/print_task_rows 渲染数据包）；000013 devices（devices 激活协议与 token_version 撤销语义/device_configs 版本化配置/scan_logs·device_logs append-only 审计/app_versions 架构预留）；000014 sysops（scheduled_jobs 注册表 DB 覆盖层/scheduled_job_runs 执行日志/system_configs 自然主键配置/notifications dedup_key 复合收件人/backup_records 混合模式在途唯一）。通用规则：无跨域外键、状态 CHECK 与方案同源、uk_notifications_dedup（WHERE dedup_key IS NOT NULL）与 uk_backup_records_inflight（WHERE status IN REQUESTED/RUNNING）部分唯一幂等索引、IMP/EXP/PT 单号唯一、files 软删除。
- **grants 追加（只追加纪律）**：scan_logs/device_logs 并入审计分层（应用账号仅 SELECT+INSERT——database.md §7.2 红线）；新增可选清理维护角色段（`-v maint_user` 传入时执行，幂等建角色并仅授审计四表 SELECT+DELETE）——审计日志/扫码日志清理由部署侧维护脚本执行、应用进程无删除通路的 grants 依据（plan §4.3/§15；log_cleanup 有意不进应用内 cron 注册表）。
- **internal/asynqx（新平台基建包）**：Queue 接口（plan §4.1 冻结签名）+ asynqQueue/inlineQueue 双实现——redis.enabled=false 时 Enqueue 即在请求 goroutine 同步执行并记日志（任务终态由 handler 落任务行，业务失败不向调用方传播）；任务类型冻结注册表（datax:import:commit/datax:export:run/printing:task:render → datax/printing 两队列）；Server（Start/Shutdown，mux 按注册 handler 构建）/Inspector（队列 pending/active 统计，/api/system/monitor 数据源）/Runtime 生命周期封装；TaskID 经自定义头透传（asynq v0.26 handler 侧不暴露 msg.ID）并以 asynq task ID 提供在途重入第二道防线。asynq 库 import 收敛至本包（判据 4）。
- **internal/sysops cron 基座（按 orchestrator 指令落位，API 面归 MT5）**：robfig/cron v3 装配；冻结五任务注册表（inventory_low_stock_scan/inventory_expiry_scan/inventory_stagnant_scan/task_timeout_scan/file_cleanup，handler 为空实现位，各域经 RegisterJobHandler 在装配期注册，未注册不调度）；防重入双闸（进程内 single-flight + pg_try_advisory_lock(hashtext('sfjob:'||code)) 专用连接持锁，锁检查失败 fail-closed 跳过），跳过落 trigger=SKIPPED 执行日志；执行日志落 scheduled_job_runs（CTE RETURNING 原子取 runID，FinishRun 回填，与业务写入不同事务）+ scheduled_jobs last_run_* 汇总；Start 同步注册表行（缺省补行保留 enabled 覆盖）；SetEnabled 启停热更新（Remove/AddFunc 单实例语义）。
- **internal/storage（新，文件中心存储层，datax 消费）**：files 表 GORM 模型（软删除 gorm.DeletedAt）；api.md §5 全清单落地——扩展名白名单（xlsx/pdf/图片/csv/txt/zip，xlsx 嗅探为 OOXML zip 容器、有意不收无可靠嗅探的 .xls）+ http.DetectContentType MIME 交叉校验 + 大小上限（storage.upload_max_bytes，LimitReader 存储侧复核双保险）+ 服务端重命名（uuid.ext，原始名绝不参与路径）+ yyyyMM 相对路径生成 + 路径穿越防御（清洗后强制收敛存储根内）+ 临时文件原子落盘；ServeContent 流式打开/幂等删除；storage.root 可配置。
- **配套**：internal/docnum 追加 IMP/EXP/PT 前缀（backend-m3-plan §12.3：ResetDay 6 位流水；有意避开 PRT——api §7 prt: 幂等键动作前缀），引擎零改动，注册表测试扩至 20 前缀；internal/config §3.2 九键三处同步（storage.root/upload_max_bytes、queue.concurrency/max_retry、datax 四键、sysops.backup_retention_days）+ Validate 快速失败；cmd/server main 接线 asynq Server 与 cron Scheduler 生命周期（启动 fail-fast；停机顺序 HTTP → cron → 队列 → 连接）。
- **验证与边界**：`go build ./...`、`go vet ./...`、`go test ./...` 全绿（全包，含扩展后的迁移结构自查——M1+M2+M3=72 表封闭、M3 CHECK/幂等索引/成对对称/append-only 与自然主键分类）；迁移结构自查测试扩展（m3Files 清单/TestM3NoCrossDomainForeignKeys/TestM3StatusValueDomains/TestM3IdempotencyAndPlatformInfra/grants 维护角色段断言）。本机无 PG/Redis：migrate up/down 未实测（database.md §9 双向验证留待具备 PG 环境）、asynq 真队列与 cron 真实调度属 //go:build integration 面未执行；`go test -race` 本机不可用（CGO_ENABLED=0 无 gcc）。新依赖（architecture §11.2 基线内）：hibiken/asynq v0.26.0、robfig/cron/v3 v3.0.1（直接），xuri/excelize/v2 v2.11.0、boombuler/barcode v1.1.0（已入 go.mod，MT1/MT2 接入前标注 indirect）。MT0 余量（归集成工程师后续批）：permissions.go M3 权限段、router 装配、seed 收编、Makefile 守卫扩展、health /ready 存储检查、§16 契约类文档回写。

## [2026-10-03] 后端：M2 业务单据域交付 + 独立评审修复轮（采购/销售/库存作业/退货追溯四域 + MT0/MT5 契约收编 + 13 项评审问题逐条处置）

- **M2 四域交付归档**（本条目补记，域代码此前已在库）：采购入库域（internal/purchase：采购订单/入库/收货/质检/上架全链路，PO/IN/RC/QC/PW 编号）、销售出库域（internal/sales：销售订单/分配/出库/拣货/复核/打包/发货，SO/OUT/PK/CH/BP/SH 编号；短拣分配重同步、复核异常重开、发货分配量双向守卫）、库存作业域（internal/stockops：调拨/盘点/调整审批/仓内移库，TR/CK 编号；盘点冻结期 ErrRowCountFrozen 守卫覆盖 Deduct/Putaway/Adjust/MoveBin 四原语）、退货追溯域（internal/returns：销售退货/采购退货/异常中心/库存追溯，RT/EX 编号）。共享平台：internal/docnum 编号引擎、internal/stock 契约包、迁移 000006–000010。
- **MT0/MT5 契约收编核实**（backend-m2-plan §8.3/§13 逐项）：inventory 别名承接（aliases.go，Qty/Op 等类型恒等 stock 契约包，qty.go 移除）、LED/ADJ 单号切 docnum（newBusinessNo 删除）、AdjustOp.ExistingAdjustmentID + MutationResult.AdjustmentNo、PutawayOp/DeductOp/InspectResultOp/AdjustOp.SerialNo 流水序列号、GET /api/inventory/locks|adjustments、POST /api/inventory/moves、GET /api/inventory/trace 全部落地；api.md §1 补录退货组与 inventory 缺项路由、§7 补幂等键构成规则（逐类核对代码实际构成）；business-flow §2.1/§3.1/§5.3/§9.2/§10.1/§13.1/§13.2 注记、inventory-rules §2 在途口径/§8.2 逐件口径注记、database.md §2 defective_inventory 不建表注记与 doc_number_counters/document_approvals 补录（文档先行义务补齐）。
- **独立评审修复轮**（13 项问题逐条复核：6 项驳回——均已有"M2 修复轮"防线在位；7 项属实修复）：
  - 修复·采购退货序列号核销（高优先）：ShipPurchaseReturn 对序列号管理 SKU 逐件采集 Serials → StockGateway.SerialStates（新增窄接口，事务内读台账）校验"存在+属该 SKU+IN_STOCK+位于指定库位" → SerialEvent(OUTBOUND) 同事务核销；stock 包新增 SerialState 值类型、inventory 新增 serialread.go 导出查询（既有冻结文件零改动）、returns 新增 SKUFlagReader 窄接口（masterdata 桥接注入，缺省 fail-closed）。
  - 修复·取消/收货并发互斥：confirmReceiptTx 事务内 SELECT FOR UPDATE 重读采购订单并校验状态（仅 APPROVED/PARTIAL_RECEIVED 可收货），CancelPO 事务内持锁重读复查 qty_received——两方向均不再产生"已取消单据继续收货/上架"。
  - 修复·退货退量并发防超：returns 创建事务内 pg_advisory_xact_lock(hashtext) 串行化同来源"读累计-校验-插入"窗口（替代已删的"已知边界"注释），杜绝并发创建累计超退。
  - 修复·purchase 详情数据权限：GetPO/GetInbound/GetReceiptByID/GetReceiptByNo/GetQC/GetTask 增加 WarehouseScope fail-closed 校验（仓库不可见按 404 处理，与 sales/stockops/returns 对齐，permission.md §4）。
  - 修复·make ci 守卫扩展：新增 guard-status（判据 4：UPDATE+SET status 无 WHERE 同串命中→人工复核清单输出，注释行排除）与 guard-docnum（判据 5：newBusinessNo 在 internal/inventory 外零命中，命中即失败）两条守卫命令并纳入 ci。
  - 驳回·盘点冻结算错账（count 快照/锁定期变动）：inventory 原语层 rejectCountFrozen 守卫在 Deduct（service.go:639）/Putaway（:400）/MoveBin（:772）/Adjust（:919）全覆盖，冻结期 total 恒定，差异=实盘−冻结快照即真实盈亏；count.go:470 注释披露与实现一致。
  - 驳回·发货按分配全额扣（短拣消失 4 件）：ConfirmPick 事务内 resyncAllocationAfterShortPick（service_outbound.go:372，释放差额锁+收缩分配记录+SO 预占进度）+ Ship 分配量双向守卫（allocTotal≠qty 即拒绝，service_ship.go:186/:196），账实恒等。
  - 驳回·CloseSalesOrder 跳状态：canTransition(soTransitions,·,COMPLETED) 守卫已在（service.go:648，仅 PARTIAL_SHIPPED 可差额关闭）。
  - 驳回·复核异常死锁：ReopenCheckTask（EXCEPTION→PENDING，PUT /api/checks/:id/reopen，routes.go:103）恢复通路已在，出库单不再永久卡死。
  - 驳回·plan §8.3 四项未落地：①②④（newBusinessNo/ExistingAdjustmentID/aliases.go）与 ③（locks/adjustments/moves 三路由）经代码逐项核实均已交付（changelog:76 所列 MT6 遗留已闭环）。
  - 驳回·Op 缺 SerialNo：stock/ops.go 四 Op 均有 SerialNo 字段且 buildLedger 在 Putaway/Deduct/InspectResult/Adjust 全部接线（service.go:416/:662/:863/:955）。
  - 知晓·stockops/sales 只读 SELECT 与 sales import inventory（评审 #12，low）：sales 用 inventory.AllocateFEFO/FIFO 纯函数为 plan §6.4 字面要求（与判据 2 自相矛盾，plan 内部冲突待编排方裁决）；stockops.StockGateway 已按 stock 类型定义（ports.go 收编完成）；只读 SELECT 旁路已在代码注释披露且无写路径，本轮不动（扩大重构需单独立项）。
- 影响范围：internal/{stock,inventory,returns,purchase,router}、Makefile、docs/{api,business-flow,inventory-rules,database,changelog}.md、docs/tasks/current.md；全库 go build/vet/test 通过。

## [2026-10-03] 前端：Pad 端 F14 并行轮集成收口（四组页面归档 + /pad 独立路由段 + 跨组一致性检查）

- **PadLayout 地基 + 十页并行交付归档**（四组，全部只新增 `web/src/layouts/pad/**` 与 `web/src/views/pad/**` 共 26 个文件，未触碰 PC 侧任何文件）：**地基组** 9 个布局原语（PadLayout/PAD_NAV_ITEMS 十页导航、PadPageShell 三栏横竖屏结构性切换、PadActionBar 五槽底栏 + disabled 点按 Toast 死按钮门禁、PadTaskCard/PadInfoCard/PadScanStub、usePadOrientation matchMedia+useSyncExternalStore、pad.css `--sf-pad-*` 局部 Token 作用域 .sf-pad-root）+ 首页（工作台四计数真契约 + 快速作业宫格 + 预警「-」占位不拖垮整页 §9.5 + 最近操作 SfEmpty 占位）与任务页（taskType/status 大触摸 chip 筛选 + 状态分组列表）；**作业页组** 收货/质检/上架三页（任务卡列表 + PadInfoCard 大字 + 大按钮操作区，触摸目标 ≥44px，主操作 disabled 占位注明「M2 冻结后接线」）；**查询与移库组** 库存/移库/调拨三页（库存页走已交付 inventoryApi.stock/ledger 真实端点 + SKU/库位编码本地 Map 补显；移库三步大按钮流；调拨单七态状态流转只读卡）；**盘点与异常组** 盘点（countApi.registerItem 唯一真实写端点 + 差异前端计算 + 实盘录入）与异常（九类×七态大 chip 筛选 + 认领/处理/关闭/拍照占位）。
- **集成收口（router/index.tsx，本轮唯一代码归属文件）**：注册 `/pad` 独立顶层路由段（与 PC `/` 段平级，`<RequireAuth><PadLayoutLazy/></RequireAuth>` + `errorElement: <RouteError />`，**不挂 PcLayout**——Pad 为独立终端入口），index 重定向 `/pad/home`，10 条子路由 `home/tasks/receive/quality/putaway/inventory/count/stockmove/transfer/exception` 全部 lazy（页面均 default export，import 目标文件逐一核对存在）；**子路由 path 与 layouts/pad/PadLayout.tsx:30-41 PAD_NAV_ITEMS 逐字一致**（任务书草案中 /pad/stock、/pad/move 以各组 integrationNeeds 实际为准修正为 /pad/inventory、/pad/stockmove，否则导航 chip 无法高亮）。config/menu.tsx 零改动、/pad 不加入 IMPLEMENTED_PATHS（Pad 不进 PC 侧边栏菜单，MENU_TREE 无 /pad 路径故不生成占位路由，与 PC 占位机制天然隔离）。
- **跨组一致性检查**：① 布局原语引用比对——10 页全部经 `@/layouts/pad` 统一出口消费（index.ts:9-20 导出齐全），各页 PadPageShell（tasksSlot/contentSlot/actionSlot/ratios/reserveActionBar）与 PadActionBar（actions/hideSlots/onPause/onFinish/onScanSubmit）用法与原语 props 定义逐一吻合；小修一处：PadHomePage.tsx:9 深路径 `@/layouts/pad/PadLayout` import 改为统一出口 `@/layouts/pad`（对齐 layouts/pad/index.ts:5 约定）。② 路由无重复——/pad 段与既有顶层段（/login、/、/403、/500、*）平级无冲突，段内 index + 10 静态子路由互不重复。③ 死按钮门禁复查——PadActionBar 原语内置 disabled 包装点按 Toast（PadActionBar.tsx:81-83/90-107）；页面自建占位（收货确认/质检三判定/上架完成/移库确认/调拨出入库/异常认领处理关闭拍照/盘点拍照）均按同一口径外包 span 接管点按弹 disabledReason，无静默死按钮。④ 任务卡形状差异属设计决策：PadTaskCard 原语绑定 api/task.ts TaskItem，盘点/异常/调拨/收货等字段不同，各页按同一 pad.css 卡片基元本地实现卡片（复用类名与 64px 热区口径），未硬套造假映射。
- **前端先行契约清单**（后端未交付，页面呈统一 Loading/Error 态，禁止补假数据）：GET /api/tasks、GET /api/workbench/summary（任务域）、GET /api/inventory/summary 与 /api/inventory/transfers（已交付 stock/ledger 两端点为真实数据）、/api/purchases/receipts 收货确认与批次效期登记（采购域 M2）、/api/quality/inspections 质检结果提交（质量域 M2）、上架执行确认与推荐库位（入库域 M2）、移库提交（internal/inventory/transfer.go 在途）、调拨出库/入库执行、GET /api/counts 系列与 CountSummary 字段（盘点域；countApi.registerItem 为本轮唯一真实写端点）、异常认领/处理/关闭（异常域）。
- **遗留清单**：① 首页任务计数卡口径差异——任务书要求「待收货/待质检/待上架/待盘点」分项计数，但 taskApi.summary 真实契约为 WorkbenchSummary 四字段（api/task.ts:49-58），已按真实字段渲染并在卡内注明口径，分项计数待任务域交付（是否回改 frontend.md §20.5 待编排方定夺）；② TaskStatus 仅 pending/in_progress/completed/cancelled 四值，任务页未造假「异常」分组；TaskItem 无优先级字段，任务卡未展示优先级；③ PadActionBar [异常] 槽与各页底部 [异常] 跳转 /pad/exception 已随本轮注册生效（不再 404）；④ 收货/质检/上架主操作、移库提交、调拨出入库、异常认领/处理/关闭、拍照取证均为 disabled 占位，M2 各域冻结后替换真实 mutation（接线点已收敛为各页顶部常量与 ActionPlaceholder）；⑤ 上架「应上架数量」取明细 receivedQty（回退 totalQty）、「目标库位」取 binCode 为前端先行提案，M2 入库域冻结后回对；⑥ 库存页 SKU/库位编码经 masterdata/bin 两个 options 请求本地映射（超 200 条降级显示 ID），后端下发联表字段后可移除；⑦ 盘点 [暂停] 为 sessionStorage 本机草稿（键 sf.pad.count.draft.*），非服务端暂停；⑧ 横竖屏切换/安全区/触摸热区未做真机实机核验（路由本轮才注册，无浏览器环境），待门禁/联调阶段；⑨ 全局构建（npm run build）按任务约束未跑，留门禁阶段。
- 验证（本会话实测）：`npx tsc --noEmit -p tsconfig.app.json` 全量 **EXIT=0**；`npx eslint src/router/index.tsx src/views/pad/home/PadHomePage.tsx`（本轮修改 2 文件）EXIT=0；26 个 Pad 文件逐一核对 default export 与 import 目标存在。

## [2026-10-03] 后端：M2 集成装配轮（router 四域接线 + 权限点/种子收编 + 全库门禁）

- **router 装配**（internal/router/router.go + m2_bridges.go）：静态导入 purchase/sales/stockops/returns 四域并注册到 /api 保护组（AuthRequired + 各域 RequirePermission，backend-m2-plan §9.4）；库存原语网关按 plan §3 口径为各消费点构造 inventory.NewService（多实例无共享状态，均注入 SKU/库位校验）；跨域窄接口桥接：StockGateway（purchase/sales/returns 以 stock 值类型定义，inventory 别名承接未落地前按各域 ports.go 预留的"收编前形态"逐字段桥接；stockops 直传）、SKU 三开关/供应商/客户校验（masterdata 侧新增导出 NewSKUFlagReader/NewSupplierChecker/NewCustomerChecker，§3.1）、异常中心（returns.Service.Create 直接结构化满足 purchase 接口、sales 域类型 detail 经 JSON 闭包桥接）、退货质检（purchase.QCCreatorService 桥接 returns.QCCreator，operator 按系统级 0 记账）、来源单可退读取（sales/purchase 未导出 §3.1 Reader 且域包禁止 import returns——按冻结签名在 router 只读桥接，经域导出 GORM 模型）、追溯读（inventory QueryLedgers/QueryInventory/QuerySerials 桥接 returns.LedgerReader/StockStateReader）。
- **权限点收编**（internal/auth/permissions.go + 四域 permissions.go 改别名）：M2 段 21 资源 / 106 动作点按 §9.2 冻结清单一次性并入（动作词枚举按 §9.1 追加 submit/approve/cancel/close/execute/claim/assign），四域包内常量改为 auth 单一来源别名（字符串值不变，路由挂载零改动）。
- **种子收编**（internal/database/seed.go）：权限点 + 25 个 M2 菜单节点（采购/销售/库存作业/退货与异常四组，叶子=资源编码）+ §9.3 十二角色映射（compileRoleGrant 资源粒度编译，菜单随资源带出）；全库权限种子 49 MENU + 188 动作点 = 237 行，三级分布 API 71 / BUTTON 117；auth 常量与种子编码经实测逐一对齐（双源无漂移）；seed_test.go 字面冻结清单与角色绑定断言同步扩列（用例保留并扩展，未删减）。
- **文档**：backend-m2-plan §9.3.1 补 MT5 收编记录（权限点总数变化与角色映射落位口径，文档先行）。
- 验证：`go build ./... && go vet ./... && go test ./...` 全绿（含 router 全量装配冒烟、seed 结构自查）。
- 影响范围：仅集成装配与权限/种子收编，未改动四域业务逻辑与 M1 既有行为。
- 遗留（随 M2 验收 MT6 跟进）：inventory 别名承接（plan §3/§8.3 条 4）、LED/ADJ 编号切换 docnum（§8.3 条 2）、AdjustOp.ExistingAdjustmentID 与 /api/inventory/locks|adjustments|moves 查询面（§8.3 条 3/5）——当前以桥接与域内回查承接，功能正确、形态待收敛。

## [2026-10-03] 前端：打印/文件/设备/系统扩展并行轮集成收口（组A–E 页面归档 + 组F 路由注册与跨组一致性门禁）

- 组A–E 并行页面交付归档（按域；后端打印/文件/设备/系统运行域未交付，均为前端先行契约、统一错误态，无假数据）：**打印中心（F12）** views/printing/PrintingCenterPage.tsx + PrintPreviewPage.tsx（独立预览页：真实数据渲染、缩放、上一页/下一页、react-to-print 打印、下载 PDF 后端未交付呈统一错误态）与 components/print/ 五组件（SfPrintButton react-to-print 统一封装——禁用 window.print、PrintPaperSheet、PrintContentRenderer、BarcodeView、QrCodeView）+ api/printing.ts（/api/prints 域）；**文件中心（F11 余量）** views/data/FileCenterPage.tsx + components/common/SfAttachment.tsx + api/file.ts；**设备中心（F13）** views/device/ 三页（DeviceListPage 四类型列表复用一组件、DeviceCreatePage 新建 + 激活二维码、DeviceDetailPage 详情）+ components/device/SfDeviceStatus.tsx + api/device.ts（DEVICE_TYPE_BY_PATH 菜单路径→类型映射，/api/devices 域）；**系统管理余量** LogPage/JobPage/SettingsPage/MonitorPage + api/system.ts（/api/logs + /api/system 域）；**Dashboard 增强** 拆分 DashboardCharts/DashboardLists/DashboardMetricStrip/dashboardView + api/dashboard.ts 对齐，新增 SfInventorySummary/SfInventoryTable 组件，usePagedList/NotificationDrawer/CountCreatePage/库存三页/ReportsPage 波及修改。
- 组F 集成收口（本轮唯一归属文件 web/src/router/index.tsx）：全量注册 13 条路径——10 条菜单静态路径（/data/printing、/data/files、/devices/scanners、/devices/pda、/devices/pads、/devices/printers、/system/logs、/system/jobs、/system/settings、/system/monitor）入 IMPLEMENTED_PATHS（49→59 条），经 placeholderRoutes 消除对应 ModulePlaceholder 占位；无菜单静态路径 /data/printing/preview（打印预览）、/devices/new（设备新建）；动态段 /devices/:id 殿后注册（静态段先于动态段，沿用既有注释规则）；设备四类型列表复用 DeviceListPage（deviceType 由 pathname 解析）。config/menu.tsx 零改动（10 条菜单路径已在既有菜单树）。/system/notifications 无交付页面，维持占位。
- 跨组一致性程序化门禁全绿：① 路由比对 10/10 断言 PASS——children 63 条静态路径无重复、IMPLEMENTED_PATHS 与静态路由一一对应、静态与占位路由零重叠（占位仅剩菜单分组容器与 /system/notifications）、/devices/new 先于 /devices/:id、68 个 lazy import 目标文件全部存在；② 死按钮扫描（views/components/layouts 共 101 文件，新建/导出/下载/打印/上传类 Button 缺 onClick）=0；③ window.print() 全库调用=0（仅 3 处「禁止 window.print」规范注释）；④ 跨文件同名导出=0；⑤ 页面 api 导入符号与 api 模块导出比对（115 条 import 语句、470 个符号）=0 不匹配；⑥ 直连 axios=0（仅 api/client.ts 单例）。
- 验证：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0、`npx eslint src/router/index.tsx` EXIT=0；react-to-print/jsbarcode/qrcode 三依赖 require.resolve 全部可解析（tsc 全量覆盖其 import 类型）；浏览器运行时实测未执行（无浏览器自动化工具链，待门禁/联调阶段）；全局构建留门禁阶段。
- 集成收口复核（组F 后二次独立验证，口径与结论）：① 路由比对脚本重跑 ALL PASS——68 个 lazy import 目标文件全部存在、IMPLEMENTED_PATHS=59、children 静态 63 条无重复、13 条新注册路径逐条在位、`/devices/new`(idx=60) 先于 `/devices/:id`(idx=61)、占位仅剩菜单分组容器与 `/system/notifications`；② `npx tsc --noEmit -p tsconfig.app.json` EXIT=0（组A 开工时报告的 DeviceDetailPage/LogPage 3 个 tsc 错误已由并行组修复，全仓归零）；③ `npx eslint`（本批 27 个改动/新增文件集合）EXIT=0；④ 死按钮复查（145 个 Button 元素块，动作词含新建/导出/下载/打印/上传/删除/启停等 23 词）真实死按钮=0——2 处脚本初报均定性为误报（PrintingCenterPage.tsx:559 下载按钮为非 SUCCESS 态 disabled 占位，SUCCESS 态 :553 有真实 onClick；SfConfirm.tsx:21 内部 Popconfirm 经 {...rest} 透传 onConfirm），SfConfirm/Popconfirm 触发元素归口 onConfirm 检查均无缺失；⑤ api 调用比对（115 条 import 语句、470 符号、119 调用点）=0 不匹配；⑥ 跨文件同名导出复查=1：`toStatusKey`（api/masterdata.ts:20 与 api/warehouse.ts:21，签名微差 string vs string | undefined）——系第三批已记录的既有 F6 问题，无任何文件双导入，保留不动；组F 报告「同名导出=0」应为其扫描口径未含既有 api 域，此处如实修正。
- 前端先行契约清单（后端交付后需按各 api 模块头注释回对字段名与枚举，页面零改动切换真实数据）：**/api/prints**（api/printing.ts：模板 list/create/PUT :id/copy/status、任务 create/list、GET history、GET :id/pdf 认证 Blob 下载；冻结回对要点——update 走 PUT /api/prints/templates/:id、预览页取单任务经 GET /api/prints/tasks?id= 过滤可回对为详情端点、任务详情需返回 template 快照与 rows 内容行、打印历史由后端落库前端只读）；**/api/files**（api/file.ts：list multipart 上传/下载/删除，FileItem 对齐 excel.md §7；回对要点——business_no 参数名、FormData 字段 file/module/business_no、缩略图地址形态须为可直访签名直链（`<img>` 无法携带 Authorization 头））；**/api/devices**（api/device.ts：list/:id/创建返回 DeviceActivation 二维码 payload/:id/activation/bind/unbind/disable；回对要点——activation 状态机 pending/activated/expired/disabled、qr_content 组装规则、列表是否补设备编号列、停用反向 enable 接口与激活码有效期）；**/api/logs + /api/system**（api/system.ts：operations/logins 日志分页、configs 读写、jobs 列表/启停/run-logs、monitor 指标；字段对齐审计 DDL 000002，requestCount1h 等为可选提案字段未返回显示「-」）；**/api/dashboard**（api/dashboard.ts：五端点增 DashboardScopeParams{view,from,to}，TodayMetrics 扩展管理层 9 字段与仓库人员 7 字段，view 判定 fail-closed 经 canAccess 归一匹配）；**通知已读** notificationApi.markRead（api/notifications.ts:21 首次被真实消费）；**盘点 SKU 选择** GET /api/skus + OPTIONS_PAGE_SIZE。
- 遗留清单：① usePagedList persistKey 仅持久化 page/pageSize，列表筛选 params 不恢复（frontend.md §26.3 为「尽量保留」口径；如需恢复需把 params 纳入 persistKey 并给页面暴露 initialParams）；② dashboardView.ts MANAGEMENT_CODES 白名单待后端 reports 域权限码冻结后精确化（如独立 dashboard:view 码）；③ @types/qrcode 未安装，components/print/qrcode.d.ts 与 components/device/qrcode.d.ts 两处局部声明兜底，统一安装后删除；④ jsbarcode SVG 绘制需 DOM，浏览器渲染未实测，联调阶段用真实扫码枪抽检（printing.md §6）；⑤ 系统管理启停/保存配置门控暂用 view 级权限码（system:job:view/system:config:view），后端冻结 job:manage/config:update 级操作码后升级，后端必须自行强校验（permission.md §5）；⑥ 日志 module 筛选暂为文本输入，待后端字典冻结换下拉；⑦ /system/notifications 无交付页面维持 ModulePlaceholder 占位；⑧ 浏览器运行时实测未执行（无浏览器自动化工具链），待门禁/联调阶段。

## [2026-10-03] 运维：Docker Compose 部署物交付（单机 /home/stockflow 目标）

- 新增仓库根部署物：`Dockerfile`（后端多阶段构建：golang:1.27-alpine 静态编译 CGO_ENABLED=0 → alpine:3.20 非 root 运行，内含 db/migrations 供兜底迁移，GOPROXY=goproxy.cn）；`.dockerignore`；`docker-compose.yml`（postgres:15-alpine + redis:7-alpine（requirepass）+ 一次性 migrate 容器（migrate/migrate v4.20.1 挂载 db/migrations，与 Makefile 固定版本一致）+ app，数据卷 pgdata/redisdata，健康检查与启动依赖编排，8080 默认仅绑 127.0.0.1）；`.env.example`（4 项必填 + 全部 SF_* 生产变量）。
- 前端容器（可选 profile web）：`web/Dockerfile`（node:20-alpine npm ci + vite 构建 → nginx:1.27-alpine 托管 dist）与 `web/nginx.conf`（SPA 回退 + /api 反代 app:8080，X-Forwarded-For 覆写为 $remote_addr，遵循 deployment.md §9 纪律）。本地已实测 `npm ci && npm run build` 通过。
- docs/deployment.md 升 v1.2：新增 §10「Docker Compose 部署（单机，/home/stockflow）」——前置条件（Docker 26+/Compose v2、国内镜像加速）、首次部署步骤（.env 必填 → build → up -d → health/ready 验证 → 立即改密纪律）、日常运维（升级即补迁移、pg_dump 备份、演示数据灌入）、常见问题（migrate dirty、镜像加速、.env 变更生效）。
- 设计取舍：迁移按 §7 纪律由独立 migrate 容器显式执行（应用 SF_DATABASE_AUTO_MIGRATE=false）；compose 内网数据库流量不出主机故 sslmode=disable（跨机须 verify-full）；受信代理在启用 web 容器时设 172.16.0.0/12。
- 影响范围：仓库根 Dockerfile/.dockerignore/docker-compose.yml/.env.example、web/Dockerfile、web/nginx.conf、docs/deployment.md §10 与本文件；不改动任何 Go 代码与既有配置默认值。

## [2026-10-03] 后端：安全修复轮（S1–S15）——认证/RBAC/平台加固与部署文档固化

- **背景**：安全审查确认 15 条发现（3 必修 + 12 建议；另有 7 条知晓级按范围决策不修复——已披露取舍或属 M2 边界）。本轮按**认证域（internal/auth）/ 平台层（internal/config、middleware、router、health、database/seed.go、cmd、config.example.yaml）/ 部署文档（docs/deployment.md）**三个文件所有权互斥的工作包并行修复，修复后经独立复审逐条核实与全量门禁。本条目为决策记录；环境变量名/默认值已与 internal/config/config.go、internal/auth/config.go、internal/database/seed.go 逐项核对。
- **认证域（S1/S4/S7/S8/S9/S10/S14/S15）**：S1 授予侧特权边界——非超管不得授予 super_admin、不得授予自身不持有的权限点、不得授予超出自身 data_scope 的范围，AssignPermissions 补 is_system 保护（新增授权限界类错误码，如 AUTH_ROLE_ESCALATION_DENIED / AUTH_PERM_ESCALATION_DENIED，经 internal/response 注册，以代码落地为准）；S8 登录防枚举——锁定/停用与"账号不存在/密码错误"对客户端统一 AUTH_CREDENTIALS_INVALID、不回显 locked_until，真实原因仅写 login_logs（行为变更③）；S9 用户目录收敛——非 super_admin 操作者按部门/数据范围过滤用户列表，列表响应裁剪 last_login_ip；S10 UpdateUser 在角色/数据范围/仓库绑定变更时对目标用户 kickUserSessions（行为变更⑤，backend-m1-plan §13.3 数据权限快照漂移对该路径销项）；S14 CreateUser 置 must_change_password=true（与 bootstrap/重置密码路径一致）；S15 改密原密码校验失败接入计数与短期锁定（username+IP 维度）并写 login_logs；S4 认证域部分——登录失败计数从单用户名键扩展为 username+IP 双键（含未知用户名的 IP 维度，防跨用户名喷洒）；S7 认证域部分——login_logs 写失败记 zap error（含 request_id/用户名），不再静默丢弃。
- **平台层（S2/S3/S4/S5/S6/S7/S9/S12/S13）**：S2 server.mode 默认值 debug→release（行为变更①），config.example.yaml 同步并注明本地开发显式设 debug；S3 初始管理员密码策略 ≥8 位字母+数字 → **≥12 位且至少含大小写字母/数字/符号中三类**（行为变更②）；S4 平台层部分——/api/auth 公开组 IP 维度 Redis 滑动窗口限流（SF_AUTH_RATE_LIMIT_IP_PER_MINUTE，默认 30，故障 fail-open 记 error）；S5 gin SetTrustedProxies 按配置（SF_SERVER_TRUSTED_PROXIES，默认空=不信任任何代理）；S6 全局请求体上限 http.MaxBytesReader（SF_SERVER_MAX_BODY_BYTES，默认 1MB，超限统一 413）+ http.Server MaxHeaderBytes；S7 平台层部分——入口统一截断 request_id（64）/User-Agent（512）/IP（64）对齐审计表列宽，超长值不再使审计写入 500；S9 平台层部分——viewer 角色权限映射剔除 auth:user:list/read（行为变更④，用户目录含 PII）；S12 release 模式下 sslmode=disable 启动 zap warn（不阻断，同机 loopback 场景放行）；S13 /ready 就绪结果 1 秒本地缓存（防探针高频穿透真实依赖探测）。
- **部署文档（S11）**：docs/deployment.md 升 v1.1——新增 §1.1 环境变量清单（作用/默认值/是否必填/生成方式，含本轮新增 SF_SERVER_TRUSTED_PROXIES、SF_SERVER_MAX_BODY_BYTES、SF_AUTH_RATE_LIMIT_IP_PER_MINUTE 与既有 SF_AUTH_ACCESS_TTL/REFRESH_TTL/MAX_LOGIN_FAILURES/LOCK_DURATION）、§2.1 grants 执行步骤与纪律（新审计表须人工重跑 grants、生产禁 auto_migrate=true；修正 app_grants.sql 尾注悬空指向的勘误说明）、§7.1 发布检查单（10 项）、§9 反向代理与网络安全基线（XFF 覆写非追加、可信代理网段声明、8080 仅内网可达、HTTPS 反代终结、/health /ready 仅 LB/内网放行）；§3 /ready 检查项对齐实现（M1 检 DB+Redis）；§6 初始管理员强口令与"首启立即改密、公网放行前确认已改密"必做步骤。
- **行为变更（部署与前端可见）**：① `SF_SERVER_MODE` 默认 release——本地开发需显式 `SF_SERVER_MODE=debug`；② 初始管理员口令策略收紧为 ≥12 位三类字符（`SF_ADMIN_INITIAL_PASSWORD` 仅空库首启消费，无存量库受影响）；③ 登录锁定/停用响应统一 AUTH_CREDENTIALS_INVALID——permission.md §3.2 的可操作提示（账户已锁定等）改由管理端经 login_logs/用户管理承担，PUT /api/users/{id}/unlock 解锁入口不变；④ viewer（财务/查看人员）角色不再拥有用户列表/详情权限点；⑤ UpdateUser 权限相关变更即踢目标用户会话（替代原"最长 2h 快照漂移 + 管理员手动踢下线兜底"）。
- 影响范围：internal/auth、internal/config、internal/middleware、internal/router、internal/health、internal/database/seed.go、cmd/server/main.go、config.example.yaml、docs/deployment.md、docs/plans/backend-m1-plan.md §7.3、docs/tasks/current.md 与本文件；行为变更均为安全默认值/响应语义收紧，无存量生产库需要迁移。
- 验证（文档轮实测）：**既有**环境变量名与默认值对照 internal/config/config.go `defaults()`、internal/auth/config.go 环境变量常量与 release fail-fast（config.go:104）、internal/database/seed.go/cmd/server/main.go 注入点逐项核对一致；**本轮新增/变更项**（SF_SERVER_TRUSTED_PROXIES、SF_SERVER_MAX_BODY_BYTES、SF_AUTH_RATE_LIMIT_IP_PER_MINUTE、server.mode 默认 release、初始口令 ≥12 位三类）按本轮冻结决策记载——文档撰写时点对应代码尚在认证域/平台层工作包并行落地，最终以安全修复报告与独立复审为准（若代码落地与本文档默认值不一致，以代码为准并回改文档）。代码工作包的 go build/vet/test 门禁与独立复审结论由安全修复工作流统一收口，不在本条目重复声称。

## [2026-10-03] 数据库：演示数据集扩充为服务器部署演示版（期初库存带流水成对 + 灌数自检）

- `db/seed/dev_seed.sql` 全量重写扩充（database.md §8.2、requirements.md §9，M1 可交付部分）：测试账号 5 个（dev_manager/dev_receiver/dev_shipper/dev_stocktaker/dev_viewer，覆盖管理员外 4 种角色——仓库经理/收货员/发货员/盘点员 + 财务查看；dev_receiver 绑一号仓、dev_shipper 绑二号仓、盘点员绑两仓，供验证 SPECIFIED_WAREHOUSE 数据权限；密码 bcrypt cost 12 哈希入库、明文仅以"仅 dev 测试"注释留存）；仓库 2 个 × 每仓库区 2–3 × 每库区货架 2 × 每货架库位 2–6（共 5 库区/10 货架/32 库位）；部门 3；商品 10 / SKU 20（批次/效期/序列号三开关覆盖 全关/批次/批次+效期/序列号 四种组合及 批次+序列号、批次+效期+序列号）/条码 26（20 主 EAN13 校验位有效 + 6 辅 CODE128）；供应商 3 / 客户 3 / 批次 7 / 序列号 23。
- **期初库存与期初流水同文件同事务 1:1 成对**（单 BEGIN/COMMIT）：inventory 24 行只写 total/available 两列（其余四状态列默认 0，恒等式 total = available + locked + frozen + pending_inspect + defective 成立），配对 24 行 inventory_ledgers 流水（change_type='INBOUND'、business_type='期初'、business_no='DEV-SEED-OPEN-*'、remark='DEV SEED'、qty_before=0 → qty_after=n）；序列号 IN_STOCK 数与序列号 SKU 期初库存逐组吻合（一物一行）。生产初始化（internal/database/seed.go BootstrapIfEmpty）行为零改动，其专职表（roles/permissions/role_permissions）演示种子零写入。
- 幂等与可重跑：全部业务行固定显式 9xxx 段主键（9001 部门/9101 仓库/…/9801 期初库存/9851 期初流水/9901 用户，避开 bigserial 自增与生产初始化段）+ 自然键 ON CONFLICT DO NOTHING；ID 段位与 'DEV SEED' 标记使误灌可检出。
- dev 门禁强化为三层（database.md §8.2：生产环境不可能注入）：① `make seed-demo` 强制 SF_ENV=dev；② SQL 内 `-v is_dev=1` psql 变量门禁（未传时 \if 按假值跳过整个数据块，PG15 \if 语义）；③ 新增前置守卫——目标库必须已存在生产安全初始化写入的内置角色，否则 RAISE EXCEPTION 拒绝（全新空库=生产首次部署形态直接失败）。Makefile 新增 `seed-demo-verify` 目标执行新增的 `db/seed/dev_seed_verify.sql`（只读自检：实体覆盖/恒等式复核/期初库存-流水双向成对与口径/序列号吻合，任一断言失败非 0 退出）。
- `internal/database/dev_seed_test.go` 静态自查 12 项（不依赖 PG）：门禁与单事务、9xxx 显式主键与写入表白名单（生产初始化/审计表零写入）、账号-角色-仓库绑定覆盖、仓库四级结构与层级一致性、SKU 四组合与 EAN13 校验位、期初库存-流水成对（键集合与数量全等、0→n 口径）、序列号吻合、bcrypt 哈希与注释明文互验、启动路径（cmd/、internal/ 生产代码）零引用演示数据、Makefile 门禁在位、verify 脚本只读、SQL 括号/引号配平。
- 影响范围：db/seed/**、Makefile seed 目标块、internal/database 测试文件、本变更日志；internal/database/seed.go 与迁移 DDL 未动。已知限制：本机无 PostgreSQL/psql，dev_seed.sql 与 dev_seed_verify.sql 未在真库执行（含 migrate up/down 双向验证），部署时按 dev_seed.sql 头注的命令级步骤执行并以 seed-demo-verify 收口；旧版 dev_seed 注入过的演示数据（WH-DEV 等，无配套流水）不在清理范围（seed 禁 DELETE），旧开发库重跑会新旧并存，需要干净演示集请用新库；演示账号 must_change_password=FALSE（与生产管理员强制改密策略不同，仅为演示直登）；M1 四角色外权限映射未配置（plan §7.1，随 M2 补配），演示账号可登录验证数据权限但菜单权限点暂少。

## [2026-10-03] 前端：契约对齐轮集成收口（组2–组5 交付归档 + 跨组一致性检查与销项）

- **组2 仓库空间域契约对齐归档**（api/warehouse.ts + 仓库五页）：四组 Item/Payload 全量 snake_case 对齐 internal/warehouse/dto.go JSON tag；Zone/Shelf/Bin Payload 关联 ID 改 number（dto.go:170/188/207 int64 binding:required）并按 UpdateInput 拆出 UpdatePayload；删除 warehouseName/zoneCode/shelfCode 等后端不返回的幻影字段；WarehouseMap 重写为 service_map.go:44-53 包装结构，库位格颜色/图例/Tooltip/详情抽屉改读 occupancy_status 四值（IDLE/PARTIAL/FULL/LOCKED，经 --sf-* Token 配色）；types/status.ts 库位占用注册键 partially_occupied→partial、删除无引用的 abnormal。
- **组3 系统权限域契约对齐归档**（api/user.ts + api/rbac.ts + 系统四页）：响应类型对齐 UserView/RoleView/PermissionView/DepartmentView JSON tag（internal/auth/service_auth.go:70-163）；请求体 department_id/role_ids/permission_ids 改 JSON number 形态（后端 *int64/[]int64 不接受字符串）；assignRoles/assignRolePermissions 全量替换语义（先取详情 role_ids 预选再全量提交，防静默清空角色）；departmentTree 改分页信封循环拉全量+前端组树（MaxPageSize=100）；修复角色/权限选项 pageSize 200/1000 超 MaxPageSize 必 400 的运行时阻断（分配弹窗「暂无可选角色/权限点」根因）；角色/部门状态枚举修正为 ENABLED/DISABLED（原传 ACTIVE 必 400 且状态列错乱，internal/auth/models.go:27-28 + 迁移 chk_*_status 佐证）；内置角色停用按钮禁用（ErrRoleSystemLocked）。**集成轮销项**：current.md「遗留：api/user.ts 对齐后端 UserView JSON tag」已由组3 本轮完成，销项落档。
- **组4 封装组件与库存域收口归档**（新建 SfExportButton/SfConfirm + api/inventory.ts + 库存六页 + 作业/采购五页 + ExportTaskPage）：batches/serials 端点改 GET /api/batches、GET /api/serials（inventory.go:55-56），四个已交付 Query/Item 按 handler.go:30-41/54-78/107-119/131-144 View JSON tag 重写（本会话 grep 后端 JSON tag 交叉复核一致）；StockListPage 导出接 SfExportButton（module=INVENTORY、permission fail-closed、范围参数随跳转）；StockDetailPage 改库存行 id 语义、汇总条六状态、页签按详情行 sku_id 真实过滤；Locks/Adjustments/Alerts 三页导出死按钮移除；作业/采购五列表页导出+新建死按钮共 10 个移除；ExportTaskPage 消费 URL query 预填模块与范围参数（BY_FILTER 并入 filters）。
- **组5 销售与盘点创建/流转归档**（api/sales.ts + api/count.ts 纯追加 + 销售/盘点六页）：SalesOrderCreatePage（/sales/new，客户/仓库/SKU 下拉走 M1 冻结契约、SKU 选中自动带 sale_price 默认价）、SalesOrderDetailPage（/sales/:id，复用 SfDetailHeader/SfSummaryBar/SfDetailSection/SfTimeline 模式）、CountCreatePage（/counts/new，范围×类型表单、库区/货架/库位按所选仓库惰性加载、负责人取 /api/users）；api 层仅纯追加 orders.create/detail 与 create/updateStatus，既有端点未动；CountTaskListPage 增「新建盘点单」入口、CountDetailPage 按 COUNT_STATUS_TRANSITIONS 状态机渲染合法迁移（每次流转 Modal.confirm 二次确认，裁决见下）；新建/流转按钮全部经 canAccess fail-closed（sales:create / count:create / count:status）。
- **集成收口动作（本会话实测）**：
  - 路由核验：router/index.tsx 已由组6 注册 /reports（:447，入 IMPLEMENTED_PATHS:169）、/sales/new（:377）、/sales/:id（:390）、/counts/new（:438，先于 /counts/:id:442），静态段先于动态段、无重复注册；git 未跟踪新页面 5 个全部有路由覆盖（CountCreatePage/SalesOrderCreatePage/SalesOrderDetailPage/ReportsPage；SfConfirm/SfExportButton/api/reports.ts 为组件与 API 文件无需路由），本轮路由零改动。
  - 菜单脱节核验：config/menu.tsx「库存盘点」/inventory/count 子项已删除（库存中心 children :54-65 无该项），顶级 /counts（:110）指向实际实现路径，其余菜单结构与第三批收口时一致，本轮菜单零改动。
  - 波及项销项：CountCreatePage.tsx:97 item.realName→real_name 已由组5 落盘修复（组3 UserItem 字段改名波及，tsc 全量通过佐证）；组4 披露的 SalesOrderListPage.tsx:89 / SalesOutboundListPage.tsx:95 导出死按钮已由组5 落盘移除（grep 复核销售三页无 ExportOutlined/「导出」残留）。
  - 死按钮复查：node 脚本解析 views/layouts/components 全部 `<Button>` 元素块（跟踪花括号嵌套，防 icon 属性内 `/>` 误截断致误报），匹配 ExportOutlined/DownloadOutlined/PlusOutlined 与「导出/新建/新增」文案者缺失 onClick 为 **0**。
  - 重复导出：api/views/components 层 export 标识符同文件查重为 0，无重复 export default。
  - API 调用比对：22 个 Api 命名空间导出方法与全部页面调用点程序化比对全部匹配；views/layouts/components/stores/hooks 无绕过 api 层的直连 axios（硬性规则 6）。
  - **小修（组2 披露项）**：api/warehouse.ts 四组 setStatus 返回类型 `*Item` → `{ status: ResourceStatus }`——后端 handler.go:236/346/446/565 实际返回 `gin.H{"status": in.Status}`（本会话读后端源码确认）；四个消费方（仓库/库区/货架/库位列表页启停动作）均不消费返回值，改类型安全。
  - **裁决：CountDetailPage 流转确认保留 Modal.confirm，不替换 SfConfirm**——SfConfirm 为危险操作封装（Popconfirm 形态、确认键固定 danger，组件注释定位「删除、停用、强制执行等破坏性动作」），盘点七态流转属业务操作确认且确认文案较长（审批链路提示），仅 cancel 为不可恢复动作需 danger（CountDetailPage.tsx:239 已单独处理）；两处「SfConfirm 就绪前暂用」过时注释更新为裁决说明（COUNT_ACTION_CONFIRM 与 confirmStatusAction 处）。
- **前端先行契约清单**（后端未交付/未冻结，页面呈统一错误态或空态，后端冻结后回对）：POST /api/sales-orders、GET /api/sales-orders/:id，明细行 skuCode 标识与配送方式四值 EXPRESS/LOGISTICS/SELF_PICK/OTHER（组5）；POST /api/counts、PUT /api/counts/:id/status 与 CountStatusAction 五动作/七态迁移表（组5）；GET /api/reports（组6）；GET /api/notifications/unread-count（组6）；/api/inventory/summary 与 alerts/locks/adjustments/trace/transfers/distribution/analytics 端点（api/inventory.ts 对应 Item 保留 camelCase 前端先行形态——已交付的 stock/ledger/batches/serials 四域为 snake_case，两组形态并存是设计状态）；api/data.ts、api/inbound.ts、api/outbound.ts、api/task.ts、api/quality.ts、api/transfer.ts、api/exception.ts（既有前端先行契约不变）；权限码 sales:create / count:create / count:status 为前端先行提案（后端销售/盘点域权限点冻结后回对 internal/auth/permissions.go 与菜单编码）。
- **遗留清单**：① 商品编辑清空分类/单位不生效（后端 ProductUpdateInput 指针三态 nil=不修改、≤0 非法，service_product.go:119-127，需后端支持显式清空语义后前端跟进）；② SKU 条码编辑入口与 image_urls 表单入口未提供（barcodes 省略=更新不修改、列表展示已覆盖；上传接口待阶段 14 文件中心）；③ GET /api/users 列表项不含 role_ids，用户列表角色列恒显示"-"（需后端 ListUsers 填充）；④ 用户表单数据范围 SPECIFIED_WAREHOUSE 提交必 400（service_rbac.go:105-109 要求 warehouse_ids 非空，M1 前端无仓库绑定 UI，需后端/产品定夺：补仓库选择或收敛选项）；⑤ 后端无 AssignRoles/AssignPermissions 全量替换语义行为级测试（仅形态级入参）；⑥ StockListPage 统计条 /api/inventory/summary 后端未注册，维持 SfError；⑦ SfSearchForm 暂无日期控件，流水 created_from/created_to、批次 expiry_from/expiry_to/order 等已认领参数无筛选 UI（Query 类型与 API 层已就绪）；⑧ 库存详情路由段名 /inventory/stock/:skuCode 实际承载库存行 id（组4 遗留命名，页面按旧段名兼容，可后续更名 :id）；⑨ 全部启停/创建/更新契约为静态比对结论，端到端生效待联调环境（本机无 PG15/Redis7，8080/5432/6379 无监听）。
- 验证（本机实测）：`npx tsc --noEmit -p tsconfig.app.json` 全量 **EXIT=0**（各组在途类型错误已收敛，含本轮修改 warehouse.ts/CountDetailPage.tsx 后复跑）；`npx eslint src/api/warehouse.ts src/views/count/CountDetailPage.tsx` EXIT=0；死按钮/重复导出/API 调用比对三项 node 程序化扫描 PASS。**全局构建（npm run build）按任务要求未跑**（门禁阶段统一执行）；`go test ./internal/warehouse/` 由组2 执行通过（ok），本会话未复跑后端测试。

## [2026-10-03] 前端：第四批路由、菜单与全局体验收口（组6 集成收口位）

- **路由收口**（web/src/router/index.tsx）：注册静态 /reports（报表中心，ReportsPage 聚合入口）+ 组5 新页无菜单路由 /sales/new、/sales/:id、/counts/new——静态段先于动态段（/sales → /sales/new → /sales/outbounds → /sales/returns → /sales/:id 殿后；/counts → /counts/new → /counts/:id），沿用 :113 注释共存规则；IMPLEMENTED_PATHS 增补 /reports（共 49 条），/sales/new、/counts/new 为无菜单静态路径不入 Set（同动态段 :114 规则）。程序化比对（node 临时脚本正则提取三集合）：children 57 条路由（静态 51 + 动态 6）无重复注册、IMPLEMENTED_PATHS 与静态路由一一对应、菜单 70 路径无重复、静态路由与占位路由零重叠。
- **菜单脱节修正**（web/src/config/menu.tsx）：删除「库存盘点」/inventory/count 子项（该路径此前落占位页，功能按既有裁决由 /counts 盘点中心承载，见 current.md 2026-10-02 第二批 F6 行）；其余菜单零改动。程序化断言：/inventory/count 已从菜单、路由、IMPLEMENTED_PATHS、占位路由四处消失。
- **报表中心**（新建 web/src/api/reports.ts + web/src/views/reports/ReportsPage.tsx）：api/reports.ts 前端先行契约 GET /api/reports → ReportCatalogItem[]（后端报表域属阶段 17–18，backend-m1-plan.md:479，页面呈统一错误态/空态）；ReportsPage 为聚合入口——「库存分析与数据工具」卡片组链到已交付真实页面 /inventory/analytics、/inventory/ledger、/data/exports（描述与三页实际 SfPageHeader 副标题逐一核对），报表目录区消费真实 API，SfLoading/SfError/SfEmpty 三态完整，无任何写死数据。
- **全局体验收口**（web/src/layouts/PcLayout.tsx）：全局搜索去装饰——原 :191 onPressEnter 仅 setKeyword('')（无真实行为），改为 AutoComplete + MENU_TREE 叶子标签匹配（flattenMenuLeaves：分组容器侧边栏点击本为展开不跳转，不作为直达目标；占位叶子跳占位页与点菜单行为一致），候选点选/回车选中/直接回车三路径均真实 navigate，未命中给 messageApi.warning 真实反馈；候选源 visibleMenu 与侧边栏共用同一 canAccess fail-closed 过滤（单一来源）。通知铃铛（原 :203-205）接真实 GET /api/notifications/unread-count（notificationApi.unreadCount，api/notifications.ts:18；useQuery retry:false/staleTime 30s），Badge 渲染未读数，后端通知域未交付时请求失败无 count 自动隐藏；关闭 NotificationDrawer 时 refetch 刷新未读数。
- 验证（本机实测）：`npx tsc --noEmit -p tsconfig.app.json` EXIT=0；`npx eslint src/router/index.tsx src/config/menu.tsx src/layouts/PcLayout.tsx src/views/reports/ReportsPage.tsx src/api/reports.ts` EXIT=0；node 程序化路由比对 5/5 断言 PASS。浏览器运行时实测未执行（本环境无浏览器自动化工具链），页面行为待门禁/联调阶段实测；全局构建按任务要求未跑（门禁阶段统一执行）。
- 说明：组6 开工时组5 三个页面文件尚未落盘（git status 干净），会话中途落盘后完成注册——SalesOrderCreatePage/SalesOrderDetailPage/CountCreatePage 均组5 交付（列表页入口 navigate('/sales/new')、navigate(`/sales/${id}`)、navigate('/counts/new') 已由组5 接线），本组仅做路由注册与收口；本轮仅修改 fileScope 内 5 个 web/src 文件。

## [2026-10-03] 前端：基础资料域契约对齐（api/masterdata.ts 全量 snake_case + 六列表页字段/动作同步）

- **API 层全量对齐后端 JSON tag**（契约依据 internal/masterdata/service_*.go View/Input 与 handler.go，非猜测）：Item 视图字段统一 snake_case——short_name/category_id/unit_id/product_id/parent_id/cost_price/sale_price/safety_stock/max_stock/min_replenish_qty/is_batch_managed/is_expiry_managed/is_serial_managed/is_enabled/created_at/updated_at/shipping_address；**关联 ID 提交统一 number**（Create/Update Input 外键为 *int64，传字符串即 400 COMMON_INVALID_PARAM；视图出参 database.ID JSON 为字符串，internal/database/model.go:22，MasterdataId 双类型保留）。
- **Query 参数对齐**（internal/masterdata/handler.go:86/193/203/305）：ProductQuery.categoryId→category_id、SkuQuery.productId→product_id 并补 enabled?: boolean（后端 strconv.ParseBool）、CategoryQuery 补 parent_id（0=仅顶级）。
- **删除 categories.remove / units.remove**（后端无 DELETE 路由——分类/单位停用即生命周期终点，internal/masterdata/masterdata.go:22/66/73）；**六组补 setStatus**：products/categories/units/suppliers/customers 传 {status}（handler.go:148 StatusRequest）、skus 传 {enabled:boolean}（handler.go:184 SKUStatusRequest），商品停用返回 cascade_disabled_skus 级联计数。
- **类型增补**：SkuItem/SkuSavePayload 增 barcodes:{barcode,code_type,is_primary}[]（service_sku.go:31 BarcodeView/139 BarcodeInput，列表批量装配返回、更新提供即全量替换）；ProductItem 增 image_urls:string[]（service_product.go:40，上传接口随阶段 14 文件中心）；六域 SavePayload 移除创建即被后端忽略的 status（CategoryCreateInput/UnitCreateInput/ProductCreateInput/SupplierCreateInput/CustomerCreateInput 均无 status，创建恒 ENABLED）。
- **六列表页同步**（web/src/views/masterdata/）：列表读取全量 snake_case（categoryName/updatedAt 等引用不再是 undefined）；ProductListPage 分类筛选参数改 category_id；SkuListPage 增 product_id/enabled 筛选与条码列（主条码「（主）」标注）、启停接 PUT /skus/:id/status；CategoryListPage/UnitListPage 删除入口（DELETE 404）改「停用/启用」动作（SfConfirm 二次确认，真实调 PUT .../status）；Supplier/Customer 页字段同步并增启停动作；六页编辑弹窗移除「状态」假开关（提交即被忽略，requirements.md §10）、编码字段编辑时禁用（后端编码不可改）。
- **名称列兜底映射**：后端列表不装配 category_name/unit_name/product_name（omitempty 仅详情返回，service_product.go:29/34、service_sku.go:43-44）、CategoryView 无 parent_name——列表展示用一次取全的下拉数据源按 id 映射兜底（同一 API 的真实数据，非前端造数；详情字段优先）。
- 验证（本机实测）：`npx tsc --noEmit -p tsconfig.app.json` 本轮修改 7 文件零错误（全仓余 4 错误均在 inventory 域他组在途文件，与本次无关）；grep 证实 categories/units 无 DELETE 调用、六页无 camelCase 字段残留。端到端启停/400 消除待后端环境联调复测。
- 遗留：后端 ProductUpdateInput 语义为 nil=不修改、≤0 非法——商品编辑清空分类/单位不生效（契约限制，前端提交 null=不修改）；SKU 条码编辑入口未提供（barcodes 省略=更新不修改，列表展示已覆盖）；image_urls 表单暂无编辑入口（待阶段 14 文件中心）。

## [2026-10-03] 后端：M1 全量交付（T0–T5：脚手架与基础设施、数据库迁移与初始化、认证与权限、基础资料、仓库空间、库存核心）

- 后端 M1 六个 scope 收口，仓库根 Go module `github.com/stockflow/server`（module 根 = 仓库根，backend-m1-plan §2/§11）。**脚手架与基础设施（T0/scope A）**：cmd/server 启动入口、internal/ 平台八包（config/logger/response/middleware/database/cache/health/router）、config.example.yaml、Makefile（fmt/fmt-check/vet/test/build/run/tidy/ci + migrate-up/down/new + seed-demo）；
- **数据库迁移与初始化（T1/scope B）**：db/migrations 000001–000005 五组成对迁移共 26 表 + db/grants/app_grants.sql 审计账号分层 + db/seed/dev_seed.sql + internal/database.BootstrapIfEmpty 生产安全初始化（schema 冻结点，详见 2026-10-02 数据库条目）；
- **认证与权限（T2/scope C）**：internal/auth 共 27 接口——JWT+Redis 会话双轨、登录保护、RBAC 与数据权限助手、敏感操作审计；**基础资料（T3/scope D）**：internal/masterdata 商品/SKU/分类/单位/供应商/客户六资源 34 条路由；**仓库空间（T4/scope E）**：internal/warehouse 四级结构 23 条路由 + 库位地图；**库存核心（T5/scope F）**：internal/inventory 九个变更原语（原生 SQL 行锁 + 双重数量校验 + 幂等键）+ FEFO/FIFO 分配策略 + M1 只读 HTTP 面五路由——各域均挂 RequirePermission、列表强制分页、同事务 operation_logs 审计（详见 2026-10-02 各域交付条目）；
- 独立评审复核修复一并计入本轮交付：库存首建 RETURNING id / Lock 行锁内幂等查重 / 000001 code 唯一索引补齐 / inventory:batch·serial 权限点补录 / 405 语义（详见 2026-10-02 复核条目）；
- 跨域装配（plan §4.3 规则①）：internal/router/router.go:56-62 已注入 auth.WithWarehouseChecker 与 inventory.WithSKUChecker/WithBinChecker 三组 Checker（必需 Checker 未注入启动期 fail-fast）；
- 验证（本机 2026-10-03 实测，仓库根 go1.27.0）：单元测试 `go test -count=1 ./...` 12 个测试包全部 ok（PG15/Redis 集成测试经 //go:build integration 门控本机未触发，待真库回归），`go build ./...`、`go vet ./...` 通过，`gofmt -l .` 无未格式化文件——单元测试与 go build/vet/gofmt 门禁全绿。
- 影响范围：后端 M1（T0–T5）交付完成，M2 起业务单据域须以 inventory Service 组合事务为唯一库存变更入口（plan §4.2 判据 3）；遗留：swag 注释汇总与 Makefile swag 目标（T6 收口）、docker-compose.dev.yml 与 .golangci.yml 未建（plan §11 T0 行所列、本仓库未交付）、真实 PG15+Redis7 环境集成回归（含 migrate up/down 双向验证）——均见 docs/tasks/current.md 与各域既有条目披露。

## [2026-10-02] 后端：独立评审复核修复（库存首建 RETURNING id / 000001 唯一索引 / Lock 行锁内幂等查重 / batch·serial 权限点补录 / 405 语义 / 杂散文件清理）

- **insertRow 改 `INSERT ... RETURNING id`**（internal/inventory/repository.go）：原 Exec 无 RETURNING，新建库存行 ID=0——Putaway 对全新库位在 buildLedger 解引用 panic（首次上架 500）、Adjust 盘盈到全新库位与 MoveBin 到空库位的 `UPDATE WHERE id=0` 影响 0 行而恒失败。三处同根因一并收敛，与 insertLock/insertBatch 写法对齐。
- **Lock 幂等查重移入库存行锁之后**（internal/inventory/service.go）：原 findLockBySource（无锁读）在 locateRowForUpdate 之前，并发两个同 (source_type,source_no,lock_type) 且无幂等键的 Lock 双方都在对方提交前完成查重，各自落一条 ACTIVE 锁（同单据重复预占双倍可用库存；idx_inventory_locks_source 为普通索引无唯一兜底）。改后并发请求被行锁串行化，后到者可见先到者已提交的 ACTIVE 锁并幂等返回；带幂等键路径行为不变（uk_inventory_ledgers_idempotency_key 部分唯一索引兜底）。
- **000001 迁移补齐 roles/permissions/departments 的 code 唯一索引**与 permissions/departments 的 parent_id 反查索引（db/migrations/000001_create_auth_tables.up.sql）：down 脚本原本就引用这五个索引、BootstrapIfEmpty 种子的 `ON CONFLICT (code)` 依赖之——缺索引时真库首启种子 INSERT 直接报错、服务初始化失败。三表无软删除，采用全量唯一索引（与不带谓词的 ON CONFLICT 推断匹配）；迁移从未在真库执行（T1 条目已披露），原地补齐无存量风险，scope B 待办销项。
- **inventory:batch / inventory:serial 权限点闭环**：常量收编 internal/auth/permissions.go（单一事实来源）、权限种子补录两资源 + 两菜单节点（权限点 102→106：API 30→32、MENU 22→24）、backend-m1-plan §5.4.1 冻结清单同步补行、seed_test.go 字面清单交叉核对同步；/api/batches、/api/serials 对普通角色不再恒 403。
- **NoMethod 响应改 405 语义**（internal/router/router.go + internal/response/errors.go 新增 COMMON_METHOD_NOT_ALLOWED），区别于路径不存在的 404；统一信封结构不变。
- 删除仓库根 Windows 保留名杂散文件 `nul`（psql 误重定向输出，47 字节）。
- **（二轮补充）insertLedger 改 `INSERT ... RETURNING id`**（internal/inventory/repository.go）：原 Exec 无 RETURNING，首调路径 MutationResult.Ledger.ID 恒为 0，与幂等重放路径（replayResult/findLockCreationLedger 从 SELECT 回读真实 ID）返回形态不一致；改后七个变更原语两条路径返回形态归一（与 insertLock/insertBatch/insertRow 同款）。
- **（二轮补充）seed_test.go 三级分布断言的失败文案同步为 API=32**（断言本身上轮已改，文案遗漏）。
- 验证：仓库根 `go build ./... && go vet ./... && go test ./...` 全绿（见本条目交付时点门禁）。

## [2026-10-02] 后端：库存核心域全量交付（后端阶段 7，T5/scope F）

- `internal/inventory` 整包（backend-m1-plan §2/§8 冻结契约）：全系统库存与流水的唯一变更入口。九个变更原语——Putaway/Deduct/Lock/ReleaseLock/MoveBin/InspectResult/Adjust/EnsureBatch/SerialEvent（§8.2 冻结方法集，tx 传 nil 自建事务、传外层 tx 组合 M2 业务单据事务，§8.3）；另有纯函数分配策略 AllocateFEFO/AllocateFIFO（inventory-rules §6/§7.2）与 BinOccupancy 库位占用聚合（§4.3 ②，供 warehouse 库位地图消费）。
- 事务与并发（§8.3 冻结口径）：库存关键路径一律原生 SQL——先 SELECT ... FOR UPDATE 行锁定位、业务层+数据层（WHERE col >= n 影响行数 0 判败）双重数量校验、原子条件 UPDATE 成对改列（恒等式由构造成立）、同事务 append-only 流水 + 操作日志；多行变更（MoveBin）先无锁定位行 id 再按 id 升序加锁防死锁；行创建竞态经 uk_inventory_location 唯一冲突重读收敛。
- 幂等（§8.5）：可选幂等键 + inventory_ledgers.idempotency_key 部分唯一索引为最终准绳（并发同键由数据库裁决），重复请求返回既有结果（Replay=true）不重复变更；Lock 另按来源单据幂等查重；流水号/调整单单号随机后缀碰撞重试。
- 审计与校验：全部变更方法同事务写 operation_logs（before/after 库存快照）；跨域 SKU/库位存在性校验经 WithSKUChecker/WithBinChecker 接口注入，未注入启动期 fail-fast（plan §4.3 规则①）。
- M1 HTTP 面只读（§8.7）：GET /api/inventory（列表/详情）、/api/inventory-ledgers、/api/batches、/api/serials 五条路由全部挂 RequirePermission、强制分页、数据权限仓库范围过滤（ALL/SPECIFIED_WAREHOUSE；序列号 warehouse_id=0=不在库行对所有范围可见——为已出库序列号可追溯的设计取舍，见 repository.go 注释）。
- 测试：纯函数单测（Qty/恒等式/枚举/幂等判定）不依赖外部；PG15 集成测试（并发锁不超卖、并发扣减不超卖、每步恒等式、流水配对、幂等重放、锁生命周期）置于 //go:build integration 门控（SF_TEST_PG_*，本环境无 PG 全部 SKIP，待真库回归）。
- 影响范围：M2 业务单据域（入库/出库/盘点/调整执行）经本 Service 组合事务，禁止任何包直写库存族六表（plan §4.2 判据 3）。

## [2026-10-02] 后端：仓库空间域全量交付（后端阶段 6，T4/scope E）

- `internal/warehouse` 整包（backend-m1-plan §2/§5 冻结契约）：仓库/库区/货架/库位四级结构 CRUD + GET /api/warehouses/{id}/map 库位地图，共 23 条路由，全部挂 RequirePermission、列表强制分页。
- 校验（business-flow §1.6、api.md §4）：编码格式/唯一、删除保护（有子级或被引用拒绝）、warehouses/bins 软删除（database.md §5.1）而 zones/shelves 仅启停无删除（plan §5.4.1 同源）、库位地图按仓库返回区/架/位网格与占用状态。
- 跨域契约（plan §4.3/§5.1 冻结签名）：NewChecker（用户绑定仓库校验，供 auth）、NewBinChecker（库位校验，供 inventory）导出；作为消费方定义 BinOccupancyReader 接口由 router 装配注入 inventory 实现，未注入时地图不带占用字段。
- 审计：增/改/删/启停全部经 middleware.Audit 与业务同事务写 operation_logs；EnsureDefaultWarehouse 委托 internal/database.BootstrapIfEmpty 的默认仓库种子（单一种子入口，无第二套种子逻辑）。
- 测试：service/handler 单测经假 gorm 方言器替身实现零外部依赖；PG 集成测试门控同 T5（本环境 SKIP）。

## [2026-10-02] 后端：基础资料域全量交付（后端阶段 5，T3/scope D）

- `internal/masterdata` 整包（backend-m1-plan §2/§5 冻结契约）：商品/SKU（含条码）/商品分类/计量单位/供应商/客户六资源 34 条路由（CRUD+启停+软删除；分类/单位无删除、停用即终点），全部挂 RequirePermission、列表强制分页（api.md §2.1）。
- 完整校验（api.md §4）：编码格式/唯一、删除被引用（商品↔SKU 级联约束）、停用被引用（分类/单位）、条码全局唯一（一码一 SKU）、SKU 三开关一致性（效期依赖批次，inventory-rules §6–§8）、数量/金额 numeric(18,4) 非负。
- 审计：增/改/删/启停全部经 middleware.Audit 与业务同事务写 operation_logs（architecture.md §8.1）；跨域导出 NewSKUChecker 供 inventory 域 WithSKUChecker 注入消费（plan §4.3）。
- 数据权限（permission.md §4）：基础资料为组织级数据（不分仓），不做仓库级行过滤。
- 测试：service/routes 单测经 fakedb/fakerepo 替身实现零外部依赖；本环境无 PG，真库回归待集成环境。

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
