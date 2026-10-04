# StockFlow 后端 M3 实施方案（development-plan 阶段 14–19）

> 日期：2026-10-03 ｜ 范围：Go 后端里程碑 M3——Excel 导入导出与文件中心、打印中心、设备管理/统一扫码解析、报表与智能能力、定时任务/日志/监控/备份 ｜
> 依据：development-plan §1/§3/§5、excel §1–§7、printing §1–§6、devices §6–§7/§13/§16（D1–D20 中后端相关子阶段）、scanner §5–§7、requirements §2.4/§2.5/§4、architecture §8/§9/§10/§11.2、api §1/§5/§6/§7、database §2/§6/§7、inventory-rules §7/§11、deployment §3/§4/§5、permission §1/§6、[backend-m2-plan](backend-m2-plan.md)（冻结契约与 §3 stock 契约包先例）｜
> 本方案与 M1/M2 计划同构：冻结 **包所有权契约、平台契约包边界、迁移 DDL 契约、任务状态机与幂等口径、权限点清单、窄接口清单**，多工程师并行时以本文为合同，跨包修改视为违约（§2.3）。
> 修订记录（2026-10-03 独立评审轮）：回写 Orchestrator 四项裁决——① gopsutil 不引入、系统指标豁免至阶段 21；② 备份=登记+外部执行混合模式（§10.5 全面改写，backup_daily 移出应用内注册表）；③ 服务端 PDF 不做（前端对齐清单记录，§12.4 条 3）；④ 打印上限/白名单按方案默认（§7.2/§18.4）。同时收敛独立评审 17 项意见：guard-readonly 白名单化（§2.3/§13.7）、log_cleanup 移出应用内注册表改部署侧维护脚本（§4.3/§15）、notifications dedup_key 复合收件人（§4.3/§10.6）、CH/SH 分派补全（§12.2）、devices activation 查询端点补录（§8.1）、设备令牌 TTL 365d（§8.2）等，详见 §18。
> 写作前提的基线核验（2026-10-03 实测）：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；M2 已交付（四单据域 + docnum + 迁移 000006–000010 + router 装配/seed 收编，docs/tasks/current.md）。M3 在 M1/M2 冻结契约上叠加，不重开已决事项；`internal/stock` 与 `internal/docnum` 的 M2 冻结面零改动（docnum 仅按 §4.4 追加 M3 前缀）。

---

## 1. 目标与验收

**M3 = development-plan §5**："平台能力完整（Excel/打印/扫码枪/设备管理），场景 5、6、8 可演示，扫码第一优先级场景与 StockFlow Scan 真机验收（场景 9）可用，监控备份就绪"。其中**后端承担**：场景 5（Excel 全向导）、6（打印模板/任务/条码）、8（`/api/scanner/resolve` 统一解析 + 扫码审计 + 设备 API，前端 HID 适配与真机部分属前端/scan 域）、监控备份就绪；场景 9 的 Scan Android 应用（devices.md D3–D7/D17–D18）不在后端范围（§15），后端以其设备 API 契约冻结 + 心跳/配置/解析联调为验收前提。按 development-plan §3 九项 DoD 映射：

| DoD 项 | M3 交付 | 验证方式 |
|---|---|---|
| 数据库 | 000011–000014 共 4 组成对迁移（§5）：平台支撑表 + 扫码设备表 | `migrate up/down` 双向验证（database.md §9） |
| 后端 | 5 个新包（datax/printing/devices/reports/sysops）+ internal/asynqx 基座；域包内接入文件仅按 §2.2 冻结清单追加 | 单元（无 PG/Redis/网络）+ 集成测试（**实际机制：`//go:build integration` + SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控外部实例，testcontainers 为备选未落地**，§14） |
| API | /api/imports /api/exports /api/files /api/prints /api/devices /api/scanner /api/reports /api/logs /api/system /api/notifications + swaggo OpenAPI | `make swag` + 接口测试 |
| 前端 | 不在本方案范围（web/ 打印/文件/设备/系统/报表页已先行交付呈错误态，前端先行契约回对清单见 §12.4） | — |
| 权限 | 全部新接口挂 RequirePermission（scanner:resolve 见 §11）+ 数据权限过滤（导出/报表按仓库范围） | 越权测试（testing §8） |
| 校验/错误处理 | 导入校验管线（excel §1.3 全量规则）、上传安全（api §5）、新域错误码 `DATAX_*`/`PRINT_*`/`DEVICE_*`/`SCANNER_*`/`REPORT_*`/`SYSTEM_*` | 错误码响应测试 |
| 日志 | 任务/打印/下载/配置修改/备份等敏感操作经 middleware.Audit；扫码审计落 scan_logs（devices §13.2） | 日志断言 |
| 测试 | 管线状态机单测 + 异步任务替身测试 + 集成链路 + `go test -race` | `make ci` 全绿 |

**全局技术约定**沿用 M1/M2 冻结值：module 根 = 仓库根、ID 字符串化、numeric(18,4)、`YYYY-MM-DD HH:mm:ss`、统一信封与分页（page/pageSize/total/items）、8080 端口、审计同事务（architecture §4）、迁移成对 up/down、生产禁 auto_migrate。本文不再重复，违反即违约。

## 2. 包所有权划分（并行开发合同）

### 2.1 五个新业务/平台包 + 两个平台基建扩展

```text
internal/datax      Excel 导入导出与文件中心：导入向导状态机、校验管线、流式导出、
                    异步任务编排（经 asynqx）、files 文件中心登记/上传/下载/清理（excel §1–§7）
internal/printing   打印中心：模板 CRUD/复制/启停、打印任务装配与队列、Code128/QR 后端
                    条码生成（printing §1–§6）
internal/devices    设备管理与扫码：设备注册/二维码激活/绑定仓库/心跳/配置下发/App 版本/
                    设备日志，POST /api/scanner/resolve 统一解析（只识别不执行业务），
                    scan_logs 扫码审计（devices §6–§7/§13、scanner §5）
internal/reports    报表与智能能力：报表目录与汇总查询（只读聚合）、补货建议/积压识别
                    （只读建议，不自动改库存）（requirements §2.4/§2.5、inventory-rules §11）
internal/sysops     平台运维面：robfig/cron 定时任务框架与预警扫描（低库存/效期/积压/超时）、
                    /api/logs 审计日志查询、/api/system 配置/定时任务/监控、备份登记与状态核对
                    （Orchestrator 裁决②：pg_dump 由部署侧执行）、站内通知
                    （architecture §8–§10、deployment §4–§5）
internal/asynqx     （新，平台基建）asynq 队列基座：Queue 接口 + asynq/inline 双实现、
                    任务类型注册、优雅关闭；不含任何业务 handler
```

既有平台包的 M3 一次性扩展（由集成工程师执行，改动清单冻结，§2.2）：`internal/auth/permissions.go`（M3 权限点常量段）、`internal/docnum/docnum.go`（frozenRules 追加 IMP/EXP/PT 三前缀，引擎逻辑零改动）、`internal/config`（§3.2 新配置键，三处同步）、`internal/health`（/ready 文件系统检查——M1 挂账销项，deployment §3）、`internal/router`（新组装配 + 指标中间件挂载）、`internal/database/seed.go`（M3 权限点/菜单/角色映射收编）、`Makefile`（守卫扩展）、`cmd/server/main.go`（asynq Server + cron 生命周期接线，主进程内启动与优雅关闭）。

### 2.2 Scope 划分与独占文件（含既有包内"仅新增"文件冻结清单）

| Scope | 工程师 | 独占文件/目录 | 迁移 DDL 确认义务 | 交付 |
|---|---|---|---|---|
| **I 集成平台** | 集成工程师 | `internal/asynqx/**`、`db/migrations/000011–000014.*`、`db/grants/app_grants.sql`、`internal/router/**`、`internal/database/seed.go`、`internal/auth/permissions.go`（M3 段，MT0 一次性追加）、`internal/docnum/docnum.go`（frozenRules 三行）、`internal/config/**`、`internal/health/**`、`internal/logger/**`（如需）、`Makefile`、`cmd/**`、`config.example.yaml` | 全部 4 组迁移产出（各域负责人确认制，沿 M1 §4.1 scope B 模式） | 队列基座/迁移/grants（含维护角色段）/router 装配/seed 收编/守卫扩展/配置基线/文档回写 |
| **X 数据中心** | 工程师 X | `internal/datax/**`；既有包内**仅新增**：`internal/masterdata/datax_import.go`、`datax_export.go`、`internal/warehouse/datax_import.go`、`datax_export.go`、`internal/purchase/datax_import.go`、`datax_export.go`、`internal/sales/datax_import.go`、`datax_export.go`、`internal/inventory/datax_import.go`、`datax_export.go`、`internal/stockops/datax_export.go`、`internal/returns/datax_export.go`（§6.1 注册表逐文件对应，实现 datax 定义的窄接口） | 000011 确认 | 导入 9 类全向导 + 导出 16 模块 + 文件中心 + /api/imports /api/exports /api/files |
| **P 打印中心** | 工程师 P | `internal/printing/**`；既有包内**仅新增**：`internal/masterdata/printing_content.go`、`internal/warehouse/printing_content.go`、`internal/purchase/printing_content.go`、`internal/sales/printing_content.go`、`internal/stockops/printing_content.go`（§7.2 内容装配窄接口实现） | 000012 确认 | 模板 CRUD/复制/启停 + 打印任务队列 + 条码生成 + /api/prints |
| **D 设备扫码** | 工程师 D | `internal/devices/**`；既有包内**仅新增**：`internal/masterdata/devices_resolve.go`、`internal/warehouse/devices_resolve.go`、`internal/purchase/devices_resolve.go`、`internal/sales/devices_resolve.go`、`internal/stockops/devices_resolve.go`、`internal/returns/devices_resolve.go`、`internal/inventory/devices_resolve.go`（§8.6 解析窄接口实现） | 000013 确认 | 设备注册/激活/绑定/心跳/配置/App 版本/设备日志 + /api/devices /api/scanner（resolve + scan-logs） |
| **R 报表智能** | 工程师 R | `internal/reports/**`；既有包内**仅新增**：`internal/inventory/reports_reader.go`、`internal/sales/reports_reader.go`、`internal/purchase/reports_reader.go`、`internal/stockops/reports_reader.go`（§9.5 聚合窄接口实现） | — | 报表目录/汇总/周转/积压 + 补货建议 + /api/reports 与 /api/inventory/summary、/api/inventory/alerts |
| **S 平台运维** | 工程师 S | `internal/sysops/**`；既有包内**仅新增**：`internal/inventory/sysops_reader.go`（低库存/效期/末次移动聚合）、`internal/sales/sysops_reader.go`、`internal/purchase/sysops_reader.go`（任务超时扫描） | 000014 确认 | cron 框架 + 预警扫描 + /api/logs /api/system /api/notifications + 备份登记与状态核对（裁决②） |

**规则（对 M2 §2.2 的 M3 扩展）**：① 既有域包（masterdata/warehouse/inventory/purchase/sales/stockops/returns）在 M3 期间**既有文件零改动**（M2 §2.3 判据 1 沿用）；各工程师在上述冻结清单内的"仅新增文件"是唯一例外——命名以消费方包为前缀（`datax_*`/`printing_*`/`devices_*`/`reports_*`/`sysops_*`），只实现消费方包定义的窄接口、只读自己域的表（datax_import.go 的导入写入器除外，其写路径必须经域内既有 Service/Repository 通路，不得另写旁路 SQL），评审逐一核对 diff 为纯新增。② 接入文件全部在 MT0 契约评审时冻结（接口签名 + 列定义 + 错误码），五线并行期无等待。③ **导入写入器承载性核验（MT0 强制项）**：既有域 Service/Repository 方法按 HTTP 单笔 payload 设计，MT0 逐域核验 §6.1 九类导入的既有通路是否满足批量导入语义（批量逐行结果、存在即更新、返回行定位 ID/行号供 error_file 定位）；不满足处由集成工程师仿 M2 §8.3 列**「MT0 一次性最小改动清单」**（文件/改动点/唯一执行人=集成工程师），与 §2.2 冻结清单同表公示后一次性执行——未列入清单的既有文件改动仍按判据 1 违约。

**MT6 一次性最小改动清单（2026-10-04，唯一执行人=集成工程师，按本规则③公示执行；纯新增/重命名，零逻辑改动）**：
1. `internal/inventory/datax_import.go`：`warehouseBinRef` 导出重命名为 `WarehouseBinRef`——原稿未导出导致 `WarehouseCodes` 窄接口包外不可实现（router 无法命名返回类型），期初库存导入不可接线；
2. `internal/sales/datax_import.go`：新增导出构造器 `NewSOImportService(db, stock, skus, customers, bins)`——本域 repo 构造器未导出，SALES_ORDER 写入器承载 Service 不可构造（依赖与 RegisterRoutes 同源，校验面一致）；
3. `internal/warehouse/datax_import.go`：新增导出构造器 `NewWarehouseImportService(db)`——同上，WAREHOUSE/LOCATION 写入器承载（CreateWarehouse/CreateBin 本域内聚校验，无跨域注入项）。

### 2.3 违约判据（验收时逐条机械可查，M2 §2.3 的 M3 扩展）

1. 修改他人 scope 文件（git diff 按目录归属判定）；**M3 期间既有域包既有文件出现任何改动**（唯一例外：§2.2 冻结清单内的新增文件）。
2. 业务域包 import 另一个业务域包；平台契约包 import 业务域包。合法 import 集 = `internal/stock`、`internal/auth` + **M3 新增平台契约包 `internal/datax`/`internal/printing`/`internal/devices`/`internal/reports`/`internal/sysops`/`internal/asynqx`**（仅类型与接口定义方向：域包 → 平台包；平台包反向 import 域包即违约）。`internal/asynqx` 仅依赖标准库 + redis + asynq + logger，禁止依赖任何业务/平台包。
3. **写 SQL 越界**：`internal/reports` 出现任何写 SQL（INSERT/UPDATE/DELETE 零命中判据）；`internal/sysops` 出现**白名单外表**写 SQL（白名单 = 自有表 scheduled_jobs/scheduled_job_runs/system_configs/notifications/backup_records + files 清理通路——§13.7 guard-readonly 白名单模式，同 datax/devices 判据先例；审计日志/扫码日志无任何写通路之外的 UPDATE/DELETE，grants 红线见 §4.3）；`internal/datax` 写 SQL 出现白名单外表（白名单 = import_tasks/import_task_rows/export_tasks/files）；`internal/devices` 对业务表出现写 SQL（scan_logs/device_logs 除外）。
4. 绕过 internal/asynqx 直接使用 asynq 库（rg `asynq.NewClient|asynq.NewServer` 在 asynqx 外零命中）；绕过 Queue 接口在单测中依赖真实 Redis。
5. 绕过 internal/docnum 生成任务单号（M2 §2.3 判据 5 沿用，IMP/EXP/PT 前缀同样受约束）。
6. 新包 handler 绕过 `internal/response`；敏感操作（导出/下载/删除/确认导入/配置修改/备份/设备绑定停用）绕过 `middleware.Audit`；事务边界出现在 handler/repository 层。
7. 跨域数据访问未走 §12 消费方窄接口 + router 注入（无"授权只读 SELECT 他人表"旁路）。
8. 域包自建迁移文件（迁移统一由集成工程师在 000011–000014 内交付）。
9. seed 中出现 §11 清单外的权限点/动作词/菜单编码。
10. 导入写入器直接写库存族六表（M1 §4.2 库存族守卫不变；唯一合法路径 = 包内 datax_import.go 经 inventory 原语落库）。

## 3. 新增依赖与配置扩展（architecture §11.2 基线内的既有选型落地）

### 3.1 新增依赖（全部为 architecture §11.2 清单内选型，无基线变更）

| 依赖 | 用途 | 引入位置 |
|---|---|---|
| `github.com/hibiken/asynq` | 异步任务队列（Redis）：导入确认执行/导出执行/打印条码预生成（excel §3–§4、architecture §11.2） | internal/asynqx（唯一封装点） |
| `github.com/xuri/excelize/v2` | Excel 读写：模板生成、流式导出（StreamWriter）、错误 Excel（excel §2/§3） | internal/datax |
| `github.com/boombuler/barcode` | Code128/Code39/EAN-8/EAN-13/UPC/QR/DataMatrix PNG 生成（printing §4.1） | internal/printing |
| `github.com/robfig/cron/v3` | 定时任务框架（architecture §9/§11.2） | internal/sysops |

**gopsutil 不引入（Orchestrator 裁决①，2026-10-03）**：系统资源指标（CPU/内存/磁盘）豁免至阶段 21，§10.4 监控矩阵仅交付 DB/Redis/连接池/队列/定时任务/版本/运行时长指标；原开放问题 §18.1 就此关闭，豁免入 §15。

### 3.2 配置扩展（三处同步：internal/config 结构体 + `defaults()` + config.example.yaml；deployment §1.1 清单同步追加）

| 配置键（SF_ 环境变量按键路径推导） | 默认值 | 说明 |
|---|---|---|
| `storage.root` | `./data/files` | 文件中心本地存储根（deployment §1"文件存储（本地/OSS）"；OSS 留接口不做） |
| `storage.upload_max_bytes` | 20971520（20MB） | 上传上限：/api/imports、/api/files 路由组局部中间件覆盖全局 1MB（api §5 文件大小上限；deployment §1.1 已预留"大包上传按需调大"） |
| `datax.import_max_rows` | 5000 | 单次导入行数上限，超限 4xx `DATAX_TOO_MANY_ROWS` |
| `datax.batch_size` | 200 | 导入分批事务批大小（excel §6.2） |
| `datax.export_batch_size` | 1000 | 导出流式批次（keyset 游标） |
| `datax.file_retention_days` | 30 | 任务产物/上传文件有效期（excel §3/§5） |
| `queue.concurrency` | 10 | asynq 并发 worker 数 |
| `queue.max_retry` | 3 | asynq 最大重试 |
| `sysops.backup_retention_days` | 14 | 备份文件/记录保留期（file_cleanup 执行清理，§10.5） |

asynq 复用 `redis.*` 连接配置；`redis.enabled=false` 时队列走 inline 同步实现（§4.1），开发/单测环境无 Redis 可用。

**有意不设的键**：`sysops.pg_dump_path`（裁决②——应用进程不执行 pg_dump）；日志/扫码日志保留期键（审计日志清理外置部署侧维护脚本，应用账号无 DELETE 通路，保留期参数属部署侧脚本环境变量——§4.3/§15，回写 deployment.md §1.1 注记）。

## 4. 异步任务基座 internal/asynqx + 定时任务框架（excel §3–§4、architecture §9）

### 4.1 Queue 接口与双实现

```go
// internal/asynqx/queue.go —— 平台基座，不含业务 handler
type Task struct {
    Type    string          // 任务类型（注册表值，§4.2）
    Payload []byte          // 消费包序列化的 payload（JSON）
    TaskID  string          // 业务任务行号（IMP-/EXP-/PT-），handler 用于状态守卫与进度
}
type Queue interface {
    Enqueue(ctx context.Context, t Task, opts ...Option) error
}
```

- **asynqQueue**：`asynq.NewClient(redisOpt)`，队列名 `datax`/`printing`（独立并发与优先级）；MaxRetry=`queue.max_retry`，指数退避；`asynq.Server` 由 cmd/server main 启动（goroutine 生命周期：main 启动 → `srv.Run()` → 优雅退出 `srv.Shutdown()`，§2.1）。
- **inlineQueue**：`redis.enabled=false` 时启用——Enqueue 即在请求 goroutine 内同步执行已注册 handler（任务行照常落库、进度照常更新），保证无 Redis 开发环境与场景 5/6 演示可用；生产必须 Redis（deployment §1.1）。
- **可见性**：`asynq.Inspector` 提供 `datax`/`printing` 队列 pending/active 统计 → /api/system/monitor `queued_tasks`（deployment §5）；任务进度真相源是任务表（export_tasks.progress 列；导入任务无 progress 列，由 success_rows/failed_rows 推导——000011/000012 000013 均无该列，仅 export_tasks 有），不依赖 asynq 内存进度 API（architecture §11.4.3 异步任务可观测）。

### 4.2 任务类型注册表（M3 冻结全量；handler 归属各消费包）

| 类型 | 队列 | handler 归属 | 职责 |
|---|---|---|---|
| `datax:import:commit` | datax | datax | 导入确认执行：按行断点分批调用 ImportWriter.Commit（§6.4） |
| `datax:export:run` | datax | datax | 导出执行：Count → 流式 StreamWriter → files 登记（§6.5） |
| `printing:task:render` | printing | printing | 条码 PNG 预生成与进程内缓存（数据装配已在任务创建时同步完成，render **不重复装配**，§7.2；PNG 不落 files 表） |

asynqx 提供 `RegisterHandler(type, func(ctx, Task) error)`；handler 幂等要求：进入即以任务行状态守卫确认执行权（`UPDATE ... SET status='PROCESSING' WHERE no=? AND status=<前置态>`，0 行 = 已被处理，直接返回 nil 静默丢弃重投递）。重试语义：业务失败返回 error → asynq 重试 → 重试重入必须能从断点续跑（导入按行状态、导出整文件重生成覆盖、打印条码预生成覆盖——均为纯函数幂等）；重试耗尽 → 终态 FAILED + error_message + 站内告警通知（deployment §5.1，接收人解析见 §10.6）。

### 4.3 定时任务框架（sysops 内，architecture §9）

- **注册表冻结（M3 五任务，代码内注册）**：

| code | 名称 | 默认 cron | 职责 | 幂等机制 |
|---|---|---|---|---|
| `inventory_low_stock_scan` | 库存预警扫描 | `*/10 * * * *` | available ≤ safety_stock 行 → 通知（去重） | dedup_key=日+SKU+仓（落库追加 `:{user_id}`，见下注） |
| `inventory_expiry_scan` | 效期预警扫描 | `0 6 * * *` | 临期 30/15/7/3 天 + 已过期（阈值 system_configs）→ 通知 | dedup_key=日+批次+级别（同上） |
| `inventory_stagnant_scan` | 长期库存（积压）扫描 | `0 6 * * *` | 30/60/90 天未动 → 通知 + 供 /api/reports 复核 | dedup_key=日+SKU+仓+档位（同上） |
| `task_timeout_scan` | 作业任务超时检测 | `0 * * * *` | 拣货/上架任务超时（创建时间 + 阈值）→ 通知 | dedup_key=任务号（同上） |
| `file_cleanup` | 过期文件清理 | `30 3 * * *` | files.expires_at 已过期 → 标记删除 + 物理删除；备份文件超 `sysops.backup_retention_days` → 删文件与记录；backup_records REQUESTED/RUNNING 超时（30 分钟）→ 标记 FAILED + 告警（§10.5） | 状态守卫天然幂等 |

- **dedup_key 复合收件人（uk_notifications_dedup 全表部分唯一的必然要求）**：dedup_key 落库值 = 上表对象键 + `:{user_id}` 收件人后缀（如 `lowstock:20261003:SKU-001:WH-A:42`）——预警按收件人逐行落库（§10.6 接收人解析），同一对象同一收件人同日只通知一次；不含 user_id 的键会使第二个收件人 INSERT 撞唯一索引（独立评审 2026-10-03 收敛项）。
- **不进本注册表的两类任务**：① `log_cleanup`（审计日志/扫码日志清理）——db/grants/app_grants.sql 对 operation_logs/login_logs（M3 追加 scan_logs/device_logs）REVOKE UPDATE/DELETE（database §7.2 审计红线：业务运行账号仅 SELECT+INSERT），应用进程任何连接不得持审计表删除权；清理由**部署侧维护脚本以独立维护角色**按保留期执行（脚本随 deployment.md runbook 交付，grants 追加维护角色段，database.md §7.3 注记同步）。② `backup_daily`——Orchestrator 裁决②：pg_dump 由部署侧（宿主机 cron/独立 cron 容器）执行，应用侧只做 backup_records 登记/状态核对（§10.5）。

- **管理要求（architecture §9.2）**：cron 表达式与启停持久化于 `scheduled_jobs` 表（代码注册表为缺省，DB 行覆盖）；PUT 状态后进程内 cron 热更新（RemoveEntry/AddFunc，单实例语义，多副本属 M4 部署议题）；每次执行落 `scheduled_job_runs`（开始/结束/结果/耗时/触发方式）；失败记 FAILED + 站内告警，下一周期自然重试（扫描类幂等由 dedup_key 保证不重复通知），不做同周期内重试——与 asynq 重试分工明确。
- **多副本防重**：每次执行前 `pg_try_advisory_lock(hashtext('sfjob:'||code))`，拿不到直接跳过（记 run 记录 SKIPPED）；执行完解锁。任务执行日志与预警写入不同事务（执行日志不回滚业务结果）。
- **MT6 装配注记（2026-10-04 集成工程师）**：① **启动开关口径**——§3.2 冻结配置清单无全局 cron 开关键，落位为 §4.3 本条既有机制：`scheduled_jobs.enabled`（代码注册表缺省 enabled=true → 生产默认开；管理端 PUT 启停热更新）；单测不触 main 不启动调度器（测试关）。未另设键（§3.2"有意不设的键"同口径）。② **task_timeout_scan 现状（2026-10-04 修复轮已消除）**——原偏差：sales/purchase 侧 `sysops_reader.go`（§2.2 Scope S 接入文件）未交付，handler 空实现位不调度（启动日志 warn 可见）。修复轮在 sysops 内收口超时扫描执行体（pick_tasks/putaway_tasks 只读聚合，与预警扫描同数据面口径；dedup=任务号+收件人），并补齐 §10.2 冻结 seed 键 `task.timeout.pick_hours=4`/`task.timeout.putaway_hours=4`——五任务全部接线执行体。

## 5. 迁移划分（000011–000014，成对 up/down；集成工程师产出、各 Scope 负责人确认）

通用规则沿用 M1/M2：**不建跨域外键**（devices.warehouse_id/bound_user_id、files.business_no 等一律裸 ID/单号 + Service 层校验）；状态列挂 CHECK（与状态机同源）；纯日志表无 updated_by/deleted_at（database §3 但书）；每迁移本地 up+down 双向验证后提交；生产执行后重跑 grants（deployment §2.1）。

### 000011_create_datax_tables.up.sql（工程师 X 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| import_tasks | import_no UNIQUE（docnum IMP），import_type CHECK(PRODUCT/SKU/SUPPLIER/CUSTOMER/WAREHOUSE/LOCATION/PURCHASE_ORDER/SALES_ORDER/INITIAL_INVENTORY——excel §1.1 九值)，status CHECK(PARSED/VALIDATED/EXECUTING/SUCCESS/PARTIAL_SUCCESS/FAILED)，source_file_id，total_rows/valid_rows/error_rows/success_rows/failed_rows，error_file_id，error_message，started_at/finished_at，created_by | idx(import_type, created_at)、idx(created_by, created_at)、idx(status) |
| import_task_rows | task_id，row_no（数据行号，1 起），raw jsonb（原行单元格），parsed jsonb（类型化解析结果），status CHECK(RAW/VALID/INVALID/QUEUED/SUCCESS/FAILED)，errors jsonb（`[{row,column,message}]`），batch_no int | UNIQUE(task_id, row_no)；idx(task_id, status)、idx(task_id, batch_no)（断点续跑依据） |
| export_tasks | export_no UNIQUE（docnum EXP），module CHECK(16 值 §6.1)，scope CHECK(CURRENT_PAGE/SELECTED/ALL/BY_FILTER/TIME_RANGE——excel §2.2 五值；TIME_RANGE=按创建时间区间，params 标准键 time_from/time_to)，params jsonb（ids/page/filters/时间范围），status CHECK(QUEUED/PROCESSING/SUCCESS/PARTIAL_SUCCESS/FAILED)，progress int 0–100，total_rows，file_id，error_message，started_at/finished_at，created_by | idx(module, created_at)、idx(created_by, created_at)、idx(status) |
| files | file_name（原始名，仅展示），stored_name（服务端生成 uuid.ext），storage_path（相对根路径，服务端生成），mime_type，file_type（扩展名白名单值），size_bytes，module，business_no（逻辑引用），uploader_id/uploader_name，expires_at NULL，deleted_at NULL | idx(module, business_no)、idx(expires_at) WHERE expires_at IS NOT NULL、idx(uploader_id, created_at)；**下载记录复用 operation_logs**（module=file, action=download，§6.6），不建独立下载记录表（database §2 无此表，避免第二真相来源） |

### 000012_create_printing_tables.up.sql（工程师 P 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| print_templates | name，object_type CHECK(SKU_LABEL/BIN_LABEL/CARTON_CODE/PALLET_CODE/INBOUND_ORDER/OUTBOUND_ORDER/PICK_ORDER/COUNT_ORDER/SHIPMENT_ORDER——printing §1.1 中 F12 首批 9 值，与 web/src/api/printing.ts 先行枚举一致)，paper CHECK(A4/A5/THERMAL_40_30/THERMAL_60_40/THERMAL_100_50)，barcode_symbology CHECK(CODE128/CODE39/EAN13/EAN8/UPC) NULL，qrcode_enabled bool，fields jsonb（字段绑定键值，创建时校验为预设键子集），header_text，status CHECK(ENABLED/DISABLED)，remark，created_by | idx(object_type, status)、idx(created_by) |
| print_tasks | print_no UNIQUE（docnum PT），object_type（同上），template_id，template_snapshot jsonb（模板快照，任务创建时冻结——后续模板修改不影响已建任务），paper，copies int，total_count，status CHECK(QUEUED/PROCESSING/SUCCESS/FAILED)，result CHECK(SUCCESS/FAILED) NULL（执行确认后回填，打印历史来源），printed_by/printed_at，error_message，created_by | idx(object_type, created_at)、idx(status)、idx(created_by, created_at) |
| print_task_rows | task_id，seq int，code（主码内容：SKU 条码/库位编码/箱码/托盘码/单据号），values jsonb（字段绑定取值快照），lines jsonb（单据明细行） | UNIQUE(task_id, seq) |

### 000013_create_device_tables.up.sql（工程师 D 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| devices | code UNIQUE（如 SF-SCAN-001，管理端命名），name，type CHECK(pc/pad/pda/scanner/printer)，brand/model/os，warehouse_id（绑定仓库，裸 ID + Service 校验），bound_user_id，status CHECK(ENABLED/DISABLED)，activation_status CHECK(PENDING/ACTIVATED)，activation_token_hash（一次性激活 token 哈希），activation_expires_at，activated_at/activated_by，app_version，last_online_at，last_scan_at，battery_level，ip，token_version int（停用/重置即递增，旧设备令牌全失效），remark，created_by | idx(type, status)、idx(warehouse_id)、idx(last_online_at)（离线判定扫描） |
| device_configs | device_id UNIQUE，config jsonb（devices §7.3 下发项：扫码模式/声音/震动/自动聚焦/连续扫码/扫码超时/默认仓库/任务刷新间隔/自动锁屏），version int（单调递增，心跳响应携带） | idx(version) |
| scan_logs | device_id，device_code，user_id/username（操作者，共享设备硬性规则 devices §6.4），warehouse_id，raw_code，symbology，resolve_type，resolve_id，resolve_code，page，business_no，success，error_code，created_at | **append-only 审计表**（devices §13.2"谁、在哪台设备、什么时候、扫了什么、执行了什么"；无 updated_by/deleted_at，应用层无 UPDATE/DELETE 通路，`app_grants.sql` 追加仅 INSERT）；idx(device_id, created_at)、idx(user_id, created_at)、idx(created_at)、idx(raw_code) |
| device_logs | device_id，level CHECK(INFO/WARN/ERROR)，event_type，message，context jsonb，occurred_at，created_at | **append-only**（grants 仅 INSERT）；idx(device_id, occurred_at)、idx(level, occurred_at) |
| app_versions | platform CHECK(android)，version_code int，version_name，release_notes，file_url（架构预留，M3 不做上传/OTA），min_supported_code，force_update bool，status CHECK(DRAFT/PUBLISHED/DEPRECATED)，published_at，created_by | UNIQUE(platform, version_code)；devices §7.4"接口与数据结构留出来"，升级执行不做（§15） |

### 000014_create_sysops_tables.up.sql（工程师 S 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| scheduled_jobs | code UNIQUE，name，cron_expr，enabled bool，last_run_at/last_run_status CHECK(success/failed/running/never)/last_run_duration_ms，next_run_at，remark | 代码注册表为缺省值源，DB 行仅存覆盖项（MT0 启动时 upsert 注册表行） |
| scheduled_job_runs | job_code，trigger CHECK(SCHEDULED/MANUAL/SKIPPED)，start_at，end_at NULL，success bool，duration_ms，message | idx(job_code, start_at)、idx(start_at)；执行日志非审计数据（允许 UPDATE 回填结束状态，不入 grants 严格清单） |
| system_configs | key PK（如 `inventory.alert.expiry_days`），value（统一字符串），name，group，type CHECK(text/number/boolean/enum)，options jsonb，remark，readonly bool，updated_by | 前端 system.ts SystemConfigItem 契约对齐；修改逐项 middleware.Audit（permission §6） |
| notifications | user_id（M3 预警/告警均解析为具体收件人逐行落库，无 NULL 广播行——NULL 仅为后续演进保留；接收人解析规则 §10.6），type CHECK(SYSTEM/APPROVAL/STOCK_ALERT/EXPIRY_ALERT/EXCEPTION/TASK——architecture §10 六值)，title，content，dedup_key varchar(128) NULL（=对象键+`:{user_id}` 复合收件人，§4.3），read bool，read_at | **部分唯一索引 uk_notifications_dedup ON (dedup_key) WHERE dedup_key IS NOT NULL**（预警扫描幂等：同一对象同一收件人同日只通知一次——dedup_key 必须复合收件人，否则第二个收件人 INSERT 撞唯一索引）；idx(user_id, read, created_at) |
| backup_records | file_name，file_path（storage 根内相对路径），size_bytes，trigger CHECK(AUTO/MANUAL)，status CHECK(REQUESTED/RUNNING/SUCCESS/FAILED——混合模式状态机：REQUESTED=应用侧已登记待部署侧执行器拾取)，message，started_at/finished_at，created_by | idx(status, created_at)；**部分唯一索引 uk_backup_records_inflight ON (trigger) WHERE status IN ('REQUESTED','RUNNING')**（在途记录唯一——并发手动触发 409 拒绝，§13.5）；备份文件不入 files 表（运维资产与业务文件分域，下载走 /api/system/backups/{id}/download） |

**grants 纪律（deployment §2.1）**：`app_grants.sql` 追加两段——① 仅 SELECT+INSERT 清单：`scan_logs`、`device_logs`（M3 新审计表）；② **维护角色段**：定义独立清理维护角色（持 operation_logs/login_logs/scan_logs/device_logs 的 DELETE，仅供部署侧清理脚本使用；应用运行账号仍无 UPDATE/DELETE——database §7.2 审计红线不破，log_cleanup 不在应用内执行的通路依据见 §4.3/§15）。发布检查单第 4 项同步勾验（重跑 grants 含维护角色创建与授权）。

## 6. Excel 导入导出与文件中心 internal/datax（excel §1–§7、api §5）

### 6.1 模板注册表（M3 冻结全量；ImportWriter/ExportSource 实现落 §2.2 冻结文件）

**导入 9 类**（excel §1.1 全量，模板列定义在 MT0 契约评审冻结；模板下载 = datax 按列定义用 excelize 现场生成，无静态文件）：

| importType | 写入器（域包新增文件） | 写路径（禁止旁路） | 业务校验要点（excel §1.3） |
|---|---|---|---|
| PRODUCT | masterdata/datax_import.go | masterdata 既有创建/更新通路 | 编码与库内重复、分类/单位存在、名称必填 |
| SKU | masterdata/datax_import.go | 同上 | 商品存在、条码表 barcodes 唯一（uk_barcodes_barcode）、三开关/安全库存合法 |
| SUPPLIER / CUSTOMER | masterdata/datax_import.go | 同上 | 编码重复、联系人/电话格式 |
| WAREHOUSE | warehouse/datax_import.go | warehouse 既有通路 | 编码重复、启用状态 |
| LOCATION（库位） | warehouse/datax_import.go | warehouse 既有通路 | 仓库/库区/货架存在（Checker 复用）、库位编码仓内唯一 |
| PURCHASE_ORDER | purchase/datax_import.go | purchase 既有创建通路（落 DRAFT） | 供应商存在、SKU 存在启用、数量>0、单价≥0、行号连续 |
| SALES_ORDER | sales/datax_import.go | sales 既有创建通路（落 DRAFT） | 客户存在、SKU 存在启用、数量/价格合法 |
| INITIAL_INVENTORY | inventory/datax_import.go | **inventory 原语**：EnsureBatch + Putaway（RequireInspect=false），Source{Type:"initial_import", No: import_no} → INBOUND 流水（excel §6.1） | 仓库/库位/SKU 存在（既有 Checker）、批次号 + 效期格式、数量>0；**高危**：仅 error_rows=0 可确认 + 前端二次确认 + operation_logs 审计 |

**导出 16 模块**（excel §2.1 全量；ExportSource 由域包实现，datax 只认通用行列模型）：

| module | 来源包 | module | 来源包 |
|---|---|---|---|
| PRODUCT / SKU / SUPPLIER / CUSTOMER | masterdata | INVENTORY / INVENTORY_LEDGER | inventory |
| WAREHOUSE / LOCATION | warehouse | TRANSFER / COUNT | stockops |
| PURCHASE_ORDER / PURCHASE_INBOUND / QUALITY | purchase | EXCEPTION | returns |
| SALES_OUTBOUND | sales | REPORT | reports（报表数据集复用导出，requirements §2.4"每张报表支持导出"） |

### 6.2 导入向导状态机（excel §1.2 完整流程；每步一个端点，状态守卫防重放）

```text
GET  /api/imports/templates                              模板清单与下载（按 §6.1 列定义现场生成 xlsx，
        无静态文件；对齐前端 data.ts ImportTemplate.downloadUrl）
POST /api/imports（multipart：import_type + file）       上传+解析：类型白名单→扩展名/MIME/大小校验→
        excelize 逐行解析落 import_task_rows（raw）→ status=PARSED，返回 {id, total_rows}
        行数 > datax.import_max_rows → 4xx DATAX_TOO_MANY_ROWS
POST /api/imports/{id}/validate                          校验：结构层（datax：必填/类型/日期/数字/
        金额/文件内重复）→ 业务层（Writer.Validate：存在性/库内重复/数量金额规则）
        → 逐行 errors 落行 + status=VALIDATED；error_rows>0 时生成错误 Excel（原数据+错误原因列，
        files 登记）→ 返回汇总 + errorFileUrl（excel §1.4）
GET  /api/imports/{id}/preview                           预览：前 100 行 parsed 结构化数据（列定义+行）
POST /api/imports/{id}/confirm                           确认导入：状态守卫 VALIDATED（0 行=409 冲突）
        → status=EXECUTING → 入队 datax:import:commit（inline 模式同步执行）
        INITIAL_INVENTORY 附加守卫：error_rows=0 且请求体 confirmed=true（excel §6.1 二次确认）
GET  /api/imports                                        导入任务列表（excel §4 数据中心；筛选 status/module）
```

- 任务单号：`docnum.Next`，前缀 `IMP`（§4.4 新前缀，ResetDay）。
- **导入执行幂等（excel §6.3）**：confirm 状态守卫保证任务只执行一次；asynq 重投递时以 `import_task_rows.status` 断点续跑——仅重试 QUEUED/FAILED 行，SUCCESS 行跳过；分批事务批大小 `datax.batch_size`，单批失败不影响已完成批次（excel §6.2），失败行落 FAILED + errors。
- 终态：全部成功 SUCCESS；部分失败 PARTIAL_SUCCESS（前端先行契约已含该值）；confirm 执行与结果回写均记 operation_logs（module=datax）。

### 6.3 导出流式（excel §2/§3；POST /api/exports、GET /api/exports）

- 创建任务：模块/scope/params 校验（SELECTED 必须带 ids≤1000、BY_FILTER 透传各域列表筛选键白名单、TIME_RANGE 必带 time_from/time_to）→ export_no（前缀 `EXP`）→ status=QUEUED → 入队 `datax:export:run`。
- 执行：ExportSource.Count 定总数 → 表头 meta 区（报表名/导出时间/导出人/查询条件，excel §2.3）→ `excelize StreamWriter` 按 keyset 游标（每批 `datax.export_batch_size`，游标=排序键最后值，禁止 offset 深翻页）逐批写 → **内存不驻留整表**（单测以内存峰值断言）→ 尾部合计行（ExportSource 可选 `Summary` 声明的数值列合计，未声明则不生成——excel §2.3"总计/合计"落位；分页/分组口径见 §16 excel.md 注记）→ 落 storage → files 登记（expires_at=retention_days）→ SUCCESS + fileUrl。进度 = 已写行数/total 每 batch 更新 progress（"处理中 35%"）。
- 单元格类型：数值列 numeric、日期列 `YYYY-MM-DD HH:mm:ss` 日期类型、金额列千分位格式——**禁止全字符串导出**（excel §2.3），由通用 CellType（TEXT/NUMBER/DATE/MONEY）驱动。
- 导出属敏感操作（permission §6）：创建任务与下载均记 operation_logs。

### 6.4 文件中心（excel §7、api §5；/api/files）

- 上传：multipart（module 必填、business_no 选填）→ 白名单校验（扩展名 + `http.DetectContentType` MIME 交叉校验 + 大小 ≤ `storage.upload_max_bytes`）→ **服务端生成存储名**（uuid + 白名单扩展名，路径 = `storage.root/yyyyMM/`，禁止拼接用户输入，api §5）→ files 登记 → 返回 FileItem（对齐 web/src/api/file.ts 先行字段，回对见 §12.4）。
- 下载：GET /api/files/{id}/download，`http.ServeContent` 流式（不整读内存）+ operation_logs 审计（module=file, action=download——excel §3"下载记录"落位）。
- 预览：GET /api/files/{id}/preview（图片原样流式返回；不做缩略图生成——无图像处理基线依赖，前端已有降级方案 file.ts fetchFileObjectURL）。
- 删除：软删（deleted_at）+ 审计；权限按 module 过滤可见（数据权限沿用仓库范围，file 列表 Service 层过滤）。
- 清理：sysops `file_cleanup` 定时任务（excel §5"过期自动清理"+ 手动删除；兼做过期备份文件/记录清理与 backup_records RUNNING 超时核对，§10.5）。
- **/ready 文件系统检查**（M1 挂账销项，deployment §3）：health.Readiness 增加 storage 根目录可写探测（集成工程师 MT0 一并交付）。

## 7. 打印中心 internal/printing（printing §1–§6）

### 7.1 模板（/api/prints/templates）

- CRUD + `POST /api/prints/templates/{id}/copy`（后端生成副本，名称追加副本标识）+ `PUT .../status`（停用即不可被新任务选用，printing §2）。
- fields 绑定校验：object_type 预设键子集（预设清单与 web/src/api/printing.ts PRINT_TEMPLATE_FIELD_PRESETS 冻结同源）；symbology 仅标签类必填。
- 模板不存 HTML（printing §2"禁止把打印 HTML 写死"——渲染由前端独立打印层 react-to-print 承担，后端只管数据与快照）。

### 7.2 打印任务（/api/prints/tasks；printing §1.2/§3）

```text
POST /api/prints/tasks {template_id, data_ids[], copies}
  → 校验：模板 ENABLED、data_ids 非空且 ≤500（防失控批量，Orchestrator 裁决④：上限/白名单按方案默认冻结，
     批量打印前影响数量由前端展示并确认；调参路径见 §18.4）
  → 装配读取（ContentReader 窄接口按 object_type 分派，域包 printing_content.go 实现：
     逐对象校验存在 + 按模板 fields 绑定装配 values/lines + 主码内容）——装配在任务创建时同步完成
  → print_tasks 落库（print_no=PT 前缀，template_snapshot 冻结）+ rows 落 print_task_rows
  → 入队 printing:task:render（仅异步预生成条码 PNG 至进程内缓存，降低大批量首次预览延迟；
     不重复装配，PNG 不落 files 表）→ status 流转
GET  /api/prints/tasks（列表，支持 id 精确过滤——前端预览页回对）
GET  /api/prints/tasks/{id}（详情含 rows/template 快照；前端先行以列表 id 过滤近似，回对为详情端点）
POST /api/prints/tasks/{id}/execute {result: SUCCESS|FAILED, message?}   打印执行确认（M3 新增端点，
  前端回对补接）：回填 printed_by/printed_at/result + operation_logs（printing §1.2/§6 打印日志：
  谁、何时、用什么模板、打了什么）
GET  /api/prints/history（打印历史列表 = print_tasks 已确认子集；printing.md §1.2 五字段）
GET  /api/prints/barcode?text=&symbology=&width=&height=  条码 PNG（boombuler，Code128 默认；
  Code39/EAN13/EAN8/UPC/QR/DataMatrix；供 Scan 端/调试/外部系统引用；参数上限防滥用）
```

- **打印任务幂等**：asynq 重投递以 print_tasks 状态守卫 + rows 整体重写覆盖（装配为纯函数，幂等天然成立）。
- **不做服务端 PDF**（Orchestrator 裁决③，2026-10-03：M3 不做，前端对齐清单记录该差异，§12.4 条 3——对前端先行 `/api/prints/:id/pdf` 的有意偏离）：打印产物 = 渲染数据包（print_task_rows）+ 条码图片端点；实际打印与"另存 PDF"由前端打印渲染层完成（architecture §11.3 前端选型既有 react-to-print）。若后续拍板必须后端 PDF，属新增 PDF 库的基线变更，须先改 architecture §11.2 再立项。
- **PALLET_CODE 承接**：托盘域未建（§15），由 printing 包内置 reader 承接——dataIds 即码值原文（管理端手工录入的托盘码/箱码打印），不装配业务字段；真实托盘域随业务需求立项后替换。

## 8. 设备管理与统一扫码解析 internal/devices（devices §6–§7、scanner §5）

### 8.1 管理端 API（用户 JWT + 权限，对齐 web/src/api/device.ts 先行契约并冻结）

```text
GET  /api/devices                    列表（type/warehouse_id/online/keyword 筛选 + 分页）
GET  /api/devices/{id}               详情（激活状态/心跳/扫码统计/配置）
GET  /api/devices/{id}/activation    激活状态查询（返回 §8.2 激活 payload/状态——前端待激活轮询依赖，
                                     web/src/api/device.ts:176；§12.4 条 4 回对差异补录）
POST /api/devices                    新建设备 → 返回激活二维码 payload（§8.2）
PUT  /api/devices/{id}/activation    重新生成激活码（旧 token 作废，token_version+1）
POST /api/devices/{id}/bind          绑定用户（devices §6.4 共享设备：业务操作记录真实操作者）
POST /api/devices/{id}/unbind        解绑
POST /api/devices/{id}/disable       停用（token_version+1，设备端全部拒绝）
PUT  /api/devices/{id}/config        配置下发（device_configs upsert，version+1；devices §7.3）
GET  /api/devices/{id}/logs          设备日志分页
```

### 8.2 激活协议（devices §6.1–§6.2，M3 冻结；细节随 MT0 回写 devices.md §6.2 注）

- 二维码内容（后端组装 JSON 字符串）：`{"server_url":"...","device_code":"SF-SCAN-001","token":"<一次性随机 token>"}`；token 存哈希、15 分钟有效、单次消费（激活成功即失效）。
- 设备端（**设备令牌认证**，非用户 JWT）：`POST /api/devices/activate {device_code, token, device_info{brand,model,os,app_version}}` → 校验 PENDING + token 哈希 + 未过期 → activation_status=ACTIVATED → 返回 `{device_token(JWT: did/code/type/token_version, TTL 365d), warehouse, config}`（devices §6.2"自动获取服务器地址、设备编号、仓库、初始配置"）。**TTL 取 365 天而非 30 天**（独立评审 2026-10-03 收敛项）：30 天无刷新端点会使全部设备每月集体离线、须管理员逐台重扫激活二维码（devices §6.2 流程被周期性打断）；失效语义由既有机制承担——设备端每次请求经 DeviceAuthRequired 查 DB（status=ENABLED AND activated AND token_version 匹配），停用/解绑重置/重新生成激活码即 token_version+1 全量失效；无独立刷新端点，到期轮换随 scan 域演进（devices.md §6.2 注记同步，§16）。
- 设备端续接（DeviceAuthRequired 中间件：验签 + devices 行 status=ENABLED AND activated + token_version 匹配，每次请求查 DB——设备端点低频，可接受）：
  `POST /api/devices/heartbeat {battery_level?, app_version?, last_error?}` → 更新 last_online_at/版本 → 响应携带 config_version（有新版则设备端拉取）；在线判定 = last_online_at 在 3 分钟内（列表 online 字段实时计算）。
  `GET /api/devices/self/config`（配置拉取）；`POST /api/devices/self/logs`（批量上报设备日志 ≤100 条/次）；`GET /api/devices/app/versions/latest?platform=android`（devices §7.4 版本检查，仅接口预留）。

### 8.3 统一条码解析 POST /api/scanner/resolve（scanner §5，只识别不执行业务）

- 请求 `{code}`；响应 `{type, id, code, name}`（scanner §5.3 形态）+ details；多命中（如库位码跨仓同码）返回 items 列表由前端选择。
- **匹配器管线（顺序冻结）**：
  1. **单据号**：按 docnum frozenRules 前缀（PO/IN/RC/QC/PW/SO/OUT/PK/CH/BP/SH/TR/CK/RT/EX——业务前缀全 15 值，与 §12.2 DocFinder 分派映射一一对应，单一映射表 MT0 冻结；LED/ADJ 不可扫）分派 DocFinder 窄接口（各域 devices_resolve.go：按单号查存在性与摘要；CH=复核任务、SH=发货单归 sales 域分派——docnum.go:116/:118，与 §12.2 一致）。箱码 BP- → sales 域 packing_records（**复用 M2 包裹，不建 boxes/box_items 第二套箱码表**，回写 database.md 注记）。
  2. **SKU 条码**：barcodes 表唯一索引精确命中（masterdata）。
  3. **库位码**：bin code 匹配（warehouse，多仓命中返回列表）。
  4. **序列号**：serial_numbers 唯一命中（inventory）。
  5. **批次码**：batch_no 匹配（inventory，跨 SKU 命中返回列表）。
  6. 以上全未命中 → `UNKNOWN_BARCODE`（scanner §6.1 错误码字面冻结；命中类型但对象不存在/停用 → SKU_NOT_FOUND/BIN_NOT_FOUND/ORDER_NOT_FOUND/TASK_NOT_FOUND，api.md 错误码清单回写）。
- **硬性约束（scanner §5.3）**：resolve 只做对象识别，业务执行走 M2 既有各域业务 API；resolve 无状态不做去重——重复扫码去重属业务 API 幂等体系（scanner §6.6 与 api §7 协同），在 §12.4 向前端明确。
- **扫码审计**：resolve 命中或失败均写 scan_logs（同一请求内 INSERT；写失败记 error 日志并降级放行——resolve 为只读识别非业务事务，业务执行侧审计仍强制同事务，architecture §4 口径不变）。**设备归属口径**：请求体仅 {code}——Scan/Pad 设备端调用经 DeviceAuthRequired 中间件，携带设备上下文（device_id/device_code 落值）；PC Web HID 场景以用户 JWT 调用、无设备行，scan_logs.device_id/device_code **可空**，审计以 user_id/username/ip 归因（devices §13.2"谁、在哪台设备"在 Web 场景先落"谁"，设备归属随设备登记接入补全；devices.md §13.2 注记见 §16）。
- **托盘码**：托盘域未建（§15）——P- 前缀/托盘码命中返回 UNKNOWN_BARCODE，前端提示托盘功能未启用。

## 9. 报表与智能能力 internal/reports（requirements §2.4/§2.5、inventory-rules §11）

### 9.1 报表端点（M3 最小集；目录 GET /api/reports 为代码内冻结注册表，无目录表）

| 端点 | 内容 | 数据来源（窄接口 → 实现包） |
|---|---|---|
| GET /api/reports | 报表目录（key/name/category/description） | 代码注册表 |
| GET /api/reports/inventory-summary | 库存汇总（按仓/SKU 分组：total/available/locked/frozen/pending_inspect/defective + 金额） | inventory/reports_reader.go |
| GET /api/reports/inbound-stats | 入库统计（时间范围按日：单数/数量/金额趋势） | purchase/reports_reader.go |
| GET /api/reports/outbound-stats | 出库统计（同形） | sales/reports_reader.go |
| GET /api/reports/inventory-turnover | 库存周转（出库量/平均库存，按仓/SKU） | inventory + sales readers |
| GET /api/reports/stagnant-stock | 积压识别（30/60/90 天未动清单，inventory-rules §11） | inventory/reports_reader.go |
| GET /api/reports/replenishment-suggestions | 智能补货建议（§9.3） | inventory + sales + purchase + stockops readers |
| GET /api/inventory/summary ｜ GET /api/inventory/alerts | Dashboard 聚合（M2 §15.2 前端先行契约承接；reports 实现、inventory 前缀挂载——GET /api/inventory/trace 先例） | inventory/reports_reader.go |

通用约束：列表类报表强制分页（api §2.1）；时间范围上限 366 天（超限 4xx `REPORT_RANGE_TOO_LARGE`）；数据权限按 `auth.WarehouseScope` 注入各 reader。

### 9.2 现场聚合 vs 汇总表（明确取舍）

**M3 决策：现场聚合**（带索引的只读聚合 SQL + 时间上限 + 强制分页），**不做汇总表**。理由：① 汇总表是第二真相来源，与流水的一致性需要额外对账机制（architecture §7"报表走汇总表**或**异步任务"允许现场聚合）；② M3 数据量级（演示与单仓场景）现场聚合可满足性能底线；③ 定时任务框架（§4.3）已就位，阶段 21 性能优化如需汇总表可平滑引入（报表 reader 接口不变，实现换源）。规避手段：聚合 SQL 禁止全表扫描模式（命中 idx_inventory_ledgers_sku_created 等既有索引）、`stagnant/turnover` 类查询先聚合后分页。

### 9.3 智能能力（阶段 18，最后做；inventory-rules §11 只读红线）

- **补货建议**（/api/reports/replenishment-suggestions）：`建议补货量 = max(0, 目标库存 − 可用库存 − 在途)`，其中 `目标库存 = max(安全库存, 日均销量 × (采购周期天数 + 安全缓冲天数))`；日均销量 = 近 30 天 OUTBOUND 流水量/30（inventory ledger 聚合）；在途 = 采购在途（PO 已审核未收齐，purchase reader）+ 调拨在途（transfer_items qty_out−qty_in，stockops reader）。**响应必须携带计算依据字段**（daily_avg_sales/safety_stock/lead_time_days/incoming_qty/current_available/suggested_qty——inventory-rules §11"建议必须可追溯计算依据"）。参数（采购周期/缓冲天数）走 system_configs。
- **积压识别**：末次移动时间 = inventory_ledgers 该 SKU+仓最后一次 INBOUND/OUTBOUND/TRANSFER/MOVE/ADJUST 时间；30/60/90 天分档清单（与预警扫描复用同一 reader）。
- **只读红线**：reports 包零写 SQL（§2.3 判据 3 CI 守卫）；建议不自动生成任何单据、不改任何库存与状态（M2 §12 智能能力口径延续）；库位推荐/库存预测（requirements §2.5 另两项）M3 不做（§15，M2 已交付上架基础规则版）。

## 10. 日志查询/系统监控/备份/通知 internal/sysops（architecture §8/§10、deployment §4–§5）

### 10.1 审计日志查询（/api/logs，只读）

- GET /api/logs/operations、GET /api/logs/logins：分页 + keyword（username/ip ILIKE）+ module + success 筛选（对齐 000002 索引与 web/src/api/system.ts 先行契约）；详情含三快照 jsonb 原样返回。**无任何写接口**（database §7 审计不可篡改）；快照展示侧脱敏由写入方保证（architecture §6 日志红线）。

### 10.2 系统配置（/api/system/configs）

- GET 全量（量级有限不分页）；PUT 批量保存（仅提交变更项，逐项 UPDATE + middleware.Audit，permission §6）；readonly 项服务端拒绝写入。
- **M3 seed 键（冻结，消费方：预警扫描/文件清理/智能能力）**：`inventory.alert.expiry_days=30,15,7,3`（inventory-rules §7.1 可配置阈值）、`inventory.alert.low_stock_enabled=true`、`inventory.alert.stagnant_days=30,60,90`、`task.timeout.pick_hours=4`、`task.timeout.putaway_hours=4`、`insight.replenishment.lead_time_days=7`、`insight.replenishment.buffer_days=3`、`datax.file_retention_days`（与 §3.2 配置默认值同源的运行时可调覆盖，仅示范机制，实际取值优先级 system_configs > 环境变量默认）。dictionaries 表不做（§15）。

### 10.3 定时任务管理（/api/system/jobs，对齐 system.ts 先行契约）

GET /api/system/jobs（分页 + keyword/enabled）；PUT /api/system/jobs/{id}/status（启停，进程内热更新，§4.3）；GET /api/system/jobs/{id}/run-logs（执行日志分页）。cron 修改 M3 不开放（冻结表达式在注册表，避免任意 cron 注入风险；需求出现再立项）。

### 10.4 系统监控（GET /api/system/monitor；deployment §5）

指标矩阵（**Orchestrator 裁决①**：CPU/内存/磁盘系统资源指标不采集，gopsutil 不引入，豁免至阶段 21——§3.1/§15）：数据库连接（sql.DBStats：InUse/Idle/MaxOpen + Ping 健康）与 Redis 连通；API 请求/错误率与 24h 趋势（**进程内分钟级环形采样器**，router 挂 sysops.MetricsMiddleware，重启清零——局限记录在响应 remarks，不做持久化指标表）；队列任务（asynq Inspector pending/active）；定时任务（scheduled_jobs 最近状态）；版本与运行时长（uptime + 构建信息）。响应 remarks 注明系统资源指标缺口（阶段 21 演进），磁盘容量告警由部署侧宿主机监控承担（deployment.md §5 注记，§16）。监控属只读平台端点，挂 system:monitor:list。

### 10.5 备份与恢复（deployment §4；**Orchestrator 裁决②：登记 + 外部执行混合模式**，2026-10-03）

- **职责划分（冻结，覆盖方案原进程内 exec pg_dump 设计）**：pg_dump 由**部署侧**执行——宿主机 cron 或独立 cron 容器（执行脚本 + crontab 样例随 deployment.md §4 交付，集成工程师维护，MT5 联调）；应用进程不执行、不依赖 pg_dump 二进制，**Dockerfile 不追加 postgresql-client**。应用侧（internal/sysops）只做 backup_records 登记、列表/下载 API、状态核对与过期清理。
- **混合契约（000014 状态机 REQUESTED/RUNNING/SUCCESS/FAILED）**：手动触发 = `POST /api/system/backups`（system:backup:create，middleware.Audit）——登记 trigger=MANUAL、status=REQUESTED 记录并返回"已登记，等待部署侧执行器拾取"（真实能力，非假按钮：外部执行器轮询 REQUESTED 记录 `FOR UPDATE SKIP LOCKED` 拾取执行）；定时备份 = 执行器自身 cron 调度直接落 AUTO 记录；执行器回写 file_name/file_path/size_bytes/status/started_at/finished_at/message。应用侧核对 = file_cleanup 兼做（§4.3）：RUNNING 超 30 分钟标记 FAILED + 站内告警。
- 备份下载：GET /api/system/backups/{id}/download（system:backup:read，审计，ServeContent 流式）；备份文件落 `storage.root/backups/`，过期清理（`sysops.backup_retention_days`）并入 file_cleanup。
- **恢复不做应用内接口**：恢复是 DBA 离线操作（deployment §4.2 恢复方案文档 + 演练记录，M3 交付 runbook 一份入 docs/deployment.md 附录），任何"一键恢复"接口都是高危假能力。

### 10.6 站内通知（/api/notifications，architecture §10；对齐 web/src/api/notifications.ts 先行契约）

- 写入方：预警扫描（低库存/效期/积压）、任务超时、任务失败告警（asynq 重试耗尽/定时任务 FAILED）、备份失败（REQUESTED 超时未拾取/RUNNING 超时/外部回写 FAILED——deployment §5.1 告警接入通知中心）。dedup_key 部分唯一索引保证扫描幂等不重复通知（§4.3 复合收件人键/§5 000014）。
- **接收人解析规则（M3 冻结；独立评审 2026-10-03 收敛项，MT0 随 000014 冻结）**：
  - 低库存/效期/积压预警、作业任务超时 → super_admin、sys_admin、warehouse_manager 角色全量用户（不按仓库细分——M3 演示量级可接受，仓库细分随需求演进）；
  - asynq 重试耗尽任务失败、定时任务 FAILED、备份失败告警 → super_admin、sys_admin；
  - 扫描时将收件人解析为具体用户**逐行落库**（user_id 非空，M3 无 NULL 广播行）——同名预警对不同收件人以 `对象键:{user_id}` 复合 dedup_key 各通知一次（§4.3）。
- 个人收件箱（认证即可用，无权限点——个人数据自见原则，同 /api/auth/me 先例）：GET /api/notifications/unread-count、GET /api/notifications（read 筛选分页）、POST /{id}/read、POST /read-all。
- 实时推送（SSE/WebSocket，architecture §11.2）M3 不做——前端未读数轮询已覆盖演示，推送属阶段 21+ 演进（§15）。

## 11. 权限点清单与角色映射扩展（api.md §1 领域命名；seed 收编归集成工程师）

### 11.1 资源与权限点（M3 新增 14 资源 / 41 动作点；动作词沿用 M1/M2 冻结枚举，零新增动词——前端先行 `system:*:view` 编码由前端对齐轮回对为 `:*:list`，见 §12.4）

| 资源 | 权限点 | 说明 |
|---|---|---|
| datax:import | list、read、create、execute | create=上传+校验；execute=确认导入（高危二次确认与审计） |
| datax:export | list、read、create | create=创建导出任务（permission §6 敏感操作）；read=下载产物/错误文件 |
| datax:file | list、read、create、delete | 文件中心 |
| printing:template | list、read、create、update、status | copy 复用 create |
| printing:task | list、read、create、execute | execute=打印执行确认 |
| devices:device | list、read、create、update、status | bind/unbind/config 复用 update；disable 复用 status |
| devices:scanlog | list、read | 扫码日志查询（设备管理后台 devices §7.1） |
| scanner:resolve | list | 统一解析；映射全量角色授权（扫码是全角色基础输入），保留可关闭能力 |
| reports:report | list、read | 报表与智能建议 |
| system:log | list、read | 操作/登录日志查询（audit 只读） |
| system:job | list、read、status | 定时任务管理 |
| system:config | list、update | 系统配置 |
| system:monitor | list | 系统监控 |
| system:backup | list、read、create | 备份记录/下载/手动触发（触发=登记 REQUESTED 记录，裁决②混合模式 §10.5） |

### 11.2 菜单种子扩展（menuSeeds 追加，编码与 web/src/config/menu.tsx 现状逐叶对齐，seed_test.go 字面核验）

数据中心组（imports/exports/prints/files 4 叶）、设备中心组（scanners/pda/pads/printers 4 叶）、报表中心（1 叶）、系统管理增叶（审计日志/登录日志/定时任务/系统配置/系统监控/设备管理 6 叶）——约 +15 MENU 节点；叶子编码 = 资源编码（既有口径）。

### 11.3 角色映射扩展（集成工程师 seed.go rolePermissionCodes 收编；授权粒度 = 资源，机制沿 M2 compileRoleGrant）

| 角色 | M3 映射 |
|---|---|
| super_admin / sys_admin | 全量 M3 资源 |
| warehouse_manager | datax/printing/reports/devices 全 list+read + datax:export:create + printing:template 全量 + devices:device:update |
| purchaser / salesperson | 各自域既有映射 + datax:export:list/read/create + datax:file + reports:report:list/read |
| 仓内作业角色（receiver/putaway_operator/picker/checker/packer/shipper/stocktaker/inspector） | 各自域既有映射 + scanner:resolve + datax:file:list/read（附件查看）+ reports:report:list |
| warehouse_operator | M1/M2 基线 + scanner:resolve + datax:file + reports:report:list |
| viewer | 全部 M3 资源 list（只读口径延续，含 reports:report:list）；不含 system:config:update、system:job:status、system:backup:create |
| user | 仅个人通知与 scanner:resolve（经手任务需要） |

### 11.4 MT6 权限收编记录（集成工程师，2026-10-04 实施落位）

1. **常量单源**：§11.1 全量 14 资源 / 41 动作点已并入 `internal/auth/permissions.go`（M3 段，M2 §9.2 同款收编模式）；五个域包内 permissions.go 改为引用 auth 同名常量别名（`internal/returns/permissions.go` 先例），字符串值不变、路由挂载零改动。
2. **种子落位**：`internal/database/seed.go` permResources 追加 14 资源、menuSeeds 追加 14 节点（合计 63 MENU + 229 动作点 = 292 行）；`m3MenuParents` 并入 compileRoleGrant 菜单可见性编译；角色映射按 §11.3 逐行落位（m2RoleGrants 扩展 + viewer/user 分支）。
3. **落位口径（对 §11.3 表格的实现解释，机械可查）**：① 裸资源名（warehouse_operator/purchaser/salesperson 行的 `datax:file`）按 compileRoleGrant 语法落位为**资源全动作**（含 delete——审计敏感操作，收紧属权限策略调整）；② viewer "全部 M3 资源 list" 落位为仅 `:list`、**不含 `:read` 明细点**（M1/M2 资源 read 照旧），`不含 system:*:update|status|create` 为冗余防线；③ user 落位为仅 `scanner:resolve:list`（个人通知无权限点；不配 dashboard 菜单，沿 M1/M2 零映射口径）。
4. **菜单落位偏差（对 §11.2 的修正，§15 同口径记录）**：叶子编码 = 资源编码 + 叶编码唯一（seed_test 判重）约束下——设备中心组落 **2 叶**（devices:device/devices:scanlog；前端 scanners/pda/pads/printers 四页共用 devices:device:list，页面级拆分由前端对齐轮消化）；系统管理增叶落 **4 叶**（system:log/job/config/monitor；登录日志与审计日志共用 system:log 资源点，设备管理落设备中心组不重复建叶）；合计 +14（原稿"约 +15"为约数）。scanner:resolve/system:backup 无独立菜单叶（能力点/挂系统管理页签）。
5. **探针**：`internal/database/seed_m3_probe_test.go`（外部测试包 `database_test`——auth import database，in-package 测试引用 auth 构成测试导入环）断言 auth M3 常量值==种子编码（双向）且 M1/M2/M3 全量常量均被种子收录；seed_test.go 字面清单同步扩展（41 动作点 + 14 菜单 + 三级分布 96 API/133 BUTTON，零删用例）。

## 12. router 装配、跨域窄接口清单与前端回对（集成工程师 MT6 收口）

### 12.1 路由组装配（全部挂 protected 组；Upload 组局部放宽 body 上限）

```text
/api/imports /api/exports /api/files          ← datax（import/file 路由挂 storage.upload_max_bytes 中间件）
/api/prints                                    ← printing
/api/devices（管理端） /api/devices/activate|heartbeat|self/*（设备端，DeviceAuthRequired）
                                              /api/scanner/resolve /api/scanner/logs ← devices
/api/reports/*  + /api/inventory/summary /api/inventory/alerts（reports 实现、inventory 前缀挂载）
/api/logs /api/system/* /api/notifications     ← sysops
中间件：sysops.MetricsMiddleware()（请求计数环形采样）挂 /api 组
生命周期：asynq.Server（datax/printing handler 注册）+ sysops.CronScheduler 在 cmd/server main 启动/优雅关闭
```

**MT6 落位注记（2026-10-04 集成工程师）**：① **上传限宽实现形态**——gin 引擎级中间件先于路由组中间件执行，子组中间件无法"覆盖"已执行的全局 1MB 检查；故全局链落位为 **route-aware 单点检查**（gin 在调用中间件链前已完成路由匹配，`c.FullPath()` 先于 `c.Next()` 可用）：命中 `POST /api/imports`/`POST /api/files` 取 `storage.upload_max_bytes`，其余路由维持全局上限，启动期 `verifyRelaxedRoutes` 核验放宽路由确已注册（路由漂移 fail-fast）。非上传路由 1MB 防线不外溢（S6 语义等价）。② **装配顺序**——asynqx Runtime 先于 router.New 创建（Queue 注入 datax/printing、datax handler 经路由装配注册），`Server.Start` 在 router.New 返回后执行（mux 按已注册 handler 构建）；③ 设备端挂载组为不含 AuthRequired 的 `/api` 子组，resolve 双轨认证在 devices 域 `resolveAuth` 单中间件内实现（设备令牌探测 iss=stockflow-devices 分派，其余走 AuthRequired+RequirePermission）。

### 12.2 跨域窄接口清单（M3 全量；机制沿用 M2 §3.1——消费方包内定义、router 注入、缺省 fail-closed、最小化；实现落 §2.2 冻结新增文件）

| 消费方定义 | 实现方（router 注入） | 方法（最小形态） | 用途 |
|---|---|---|---|
| datax.ImportWriter ×9 | masterdata×1 / warehouse×1 / purchase / sales / inventory（各域新增 datax_import.go） | `Template() TemplateSpec`；`Validate(ctx, rows) ([]RowError, error)`；`Commit(ctx, actor, taskRef, rows) (CommitResult, error)` | §6.1 九类导入（存在性校验复用既有 Checker：masterdata.NewSKUChecker/SupplierChecker/CustomerChecker、warehouse.NewChecker/NewBinChecker） |
| datax.ExportSource ×16 | §6.1 各域新增 datax_export.go | `Columns() []Column`；`Count(ctx, ExportFilter) (int64, error)`；`Batch(ctx, ExportFilter, Cursor) ([]Row, Cursor, error)`；`Summary() []SummaryRow`（**可选**，数值列合计声明，缺省不生成合计行——excel §2.3 总计/合计落位） | §6.3 流式导出（行=通用 CellValue，零域类型外泄；筛选/数据权限在实现内沿用各域列表查询） |
| printing.ContentReader ×9 | §2.2 各域 printing_content.go | `Assemble(ctx, ids, fields) ([]ContentRow, error)` | 打印数据装配（模板字段绑定填充） |
| devices.SKUBarcodeReader | masterdata/devices_resolve.go | `FindByBarcode(ctx, code) (Hit, bool, error)` | resolve 匹配器 2 |
| devices.BinCodeReader | warehouse/devices_resolve.go | `FindByCode(ctx, code) ([]Hit, error)` | 匹配器 3（多仓命中列表） |
| devices.DocFinder ×4 域 | purchase / sales / stockops / returns 各自 devices_resolve.go | `FindByNo(ctx, docNo) (Hit, bool, error)` | 匹配器 1（docnum 业务前缀全 15 值分派冻结，与 §8.3 可扫集一致：purchase=IN/PO/QC/RC/PW、sales=SO/OUT/PK/CH/BP/SH——CH=复核任务/SH=发货单为 sales 域 M2 既有表（docnum.go:116/:118、service_outbound.go/service_ship.go）、stockops=TR/CK、returns=RT/EX；MT0 以本表为单一前缀→实现映射冻结） |
| devices.SerialReader / BatchReader | inventory/devices_resolve.go | `FindSerial(ctx, sn)` / `FindBatch(ctx, batchNo) ([]Hit, error)` | 匹配器 4/5 |
| reports.StockAggregateReader / LedgerFlowReader | inventory/reports_reader.go | 汇总聚合 / 时序聚合 / 末次移动 | §9.1/§9.3 |
| reports.InboundStatReader / OutboundStatReader / IncomingQtyReader | purchase / sales / stockops 各自 reports_reader.go | 单据统计聚合 / 在途聚合 | §9.1/§9.3 |
| sysops.LowStockReader / ExpiryReader / MovementReader | inventory/sysops_reader.go | 低库存行 / 临期批次行 / 末次移动行 | §4.3 扫描任务 |
| sysops.TimeoutReader ×2 | sales / purchase 各自 sysops_reader.go | 超时任务行 | task_timeout_scan |

规则：每接口 1–3 方法；签名一律内建类型或 datax/printing/devices/reports/sysops 包内值类型（同 stock 先例），不携带域类型；新增跨域需求必须先进本表评审再实现（§2.3 判据 7）。

**MT6 装配状态（2026-10-04 集成工程师）**：ImportWriter ×9 / printing.ContentReader ×7 / devices 匹配器 1–5 八接口 / sysops 队列统计桥接全部经 router 注入完成（实现落各域冻结新增文件）；**ExportSource 落位 11/16**——PRODUCT/SKU/SUPPLIER/CUSTOMER（masterdata datax_export.go）与 REPORT（reports 行源，§17 MT4 遗留）未交付，datax 对未挂模块按 fail-closed 设计创建任务即 409 `DATAX_MODULE_NOT_AVAILABLE`（不虚挂、不造第二实现）；reports 补货参数/预警阈值暂取域内冻结缺省（与 §10.2 seed 键默认同值），`WithReplenishmentParams`/`WithAlertThresholds` 注入点保留，sysops 导出配置读取器后补桥（sysops 未交付读取器，不反向造旁路 SELECT）。

### 12.3 docnum 前缀追加（M3 段，集成工程师 MT0；引擎零改动）

`IMP`（导入任务）/ `EXP`（导出任务）/ `PT`（打印任务），均 ResetDay、DateSeg=true、6 位流水。**有意避开 `PRT`**：api §7 已冻结 `prt:` 为采购退货预占幂等键动作前缀，PRT 打印任务号会在日志/审计中混淆（§4.4）。

### 12.4 前端先行契约回对清单（前端对齐轮消化，后端以本文为冻结契约；M2 §15.2 同口径）

1. data.ts /api/imports /api/exports 路径与字段基本一致；差异：导入任务状态含向导态 PARSED/VALIDATED（前端 DataTaskStatus 需扩展或将非终态映射"处理中"）、ExportCreatePayload.filters 键为各域列表筛选白名单键。
2. file.ts /api/files 四端点采纳；`fileName/fileType/businessNo` 等 camelCase 出参需回对为后端 snake_case JSON tag；预览端点 `GET /api/files/{id}/preview`（先行契约以 downloadUrl 兜底）。
3. printing.ts：模板/任务/历史端点采纳；**`GET /api/prints/:id/pdf` 不交付**（Orchestrator 裁决③：服务端 PDF 不做，本条即前端对齐清单记录——pdfUrl 永不下发，downloadPrintPdf 回退路径将 404，回对为"预览层打印/另存 PDF"）；任务详情回对为 `GET /api/prints/tasks/{id}`；新增 execute 确认端点需前端补接。
4. device.ts 七端点采纳（list/get/create/activation/bind/unbind/disable）；**§8.1 原稿缺 GET /{id}/activation，已补录**（前端待激活轮询依赖，device.ts:176——MT0 冻结 device 域路由表时逐端点核对 device.ts）；激活状态值冻结为大写枚举（PENDING/ACTIVATED；expired/disabled 为派生态）；设备端心跳/配置/日志端点为新增，前端 F13 不涉及；远程注销不在本期（device.ts:183 注释自认，§15）。
5. system.ts：/api/logs 两端点采纳；权限编码 `system:log:view` 等回对为 `system:log:list` 等（RESOURCE_ALIASES 或菜单码修正）；configs/jobs/monitor 字段对齐 snake_case JSON tag。
6. notifications.ts 四端点采纳（字段全对齐）。reports.ts 目录端点采纳，报表数据端点按 §9.1 扩展。

## 13. 事务、并发、幂等与守卫要点

1. **事务边界**：导入 Commit 分批事务（批内行级原子，批间独立——excel §6.2）；导出/打印装配无业务事务（只读 + 任务行单行 UPDATE）；设备激活/绑定/配置为单行事务 + Audit；系统配置批量保存逐项事务。**任务编排（datax）不包业务事务**——业务写入仍由各域既有 Service 通路完成（判据 10）。
2. **状态守卫幂等**：import confirm（VALIDATED→EXECUTING）、asynq 重投递（PROCESSING 守卫）、cron 执行（advisory lock）、打印 execute（SUCCESS/FAILED 回填一次）——全部 `UPDATE ... WHERE status=<前置态>` 行数判定（M2 §10.1 机制沿用）。
3. **进度上报**：export_tasks.progress 每 batch 一次 UPDATE（批外更新，失败不回滚业务批次）——progress 列**仅 export_tasks 有**（000011/000012 无该列）：导入进度由 success_rows/failed_rows 推导，打印任务无进度语义（装配在任务创建时同步完成）；轮询走任务列表（前端先行 5s 轮询已就绪）。
4. **上传安全**（api §5 全清单）：扩展名白名单 + MIME 交叉 + 大小上限 + 服务端重命名 + 路径服务端生成（防路径穿越）+ 元信息入 files。
5. **资源生命周期**：asynq Server/cron/Inspector 由 main 启动并优雅关闭（go-dev-standard 规则 3）；备份登记 REQUESTED 状态守卫（并发触发按唯一在途 MANUAL 记录拒绝，§10.5）；下载 ServeContent 流式。
6. **数据权限**：ExportFilter/报表查询携带 `auth.WarehouseScope`，域内实现过滤；datax 导入为基础资料/单据创建通路，沿用各域既有权限校验；resolve 为全局识别（不携带仓库范围，返回对象身份，业务数据由后续业务接口按权限过滤——scanner §5.3 分工）。
7. **CI 守卫扩展（make ci 追加）**：`guard-datax`（datax 写 SQL 表白名单外零命中）、`guard-readonly`（**reports 包** INSERT|UPDATE|DELETE 零命中 + **sysops 包白名单模式**——写 SQL 仅允许命中自有表 scheduled_jobs/scheduled_job_runs/system_configs/notifications/backup_records 与 files 清理通路，白名单外表命中即违约；rg 提取语句表名比对白名单，人工复核清单模式同 guard-status。独立评审 2026-10-03 收敛：sysops 的 scheduled_jobs upsert/任务回填/系统配置 PUT/backup_records 登记/notifications 写入/文件清理均为必然写路径，全量零命中判据机械不可执行）、`guard-asynq`（asynqx 外 asynq 直用零命中）、`guard-devices`（devices 包对业务表写 SQL 零命中）。M1/M2 既有守卫沿用。
8. **审计清单**：确认导入/创建导出/下载文件/删除文件/模板增改/打印确认/设备创建绑定停用配置下发/系统配置修改/备份手动触发登记（REQUESTED，§10.5）→ middleware.Audit（同事务或任务行事务内）。

## 14. 测试策略（testing §2/§3/§4 M3 子集；单测零外部依赖红线）

1. **单元测试（不依赖 PG/Redis/网络，go-dev-standard 与任务约束）**：
   - asynqx：inlineQueue/queue 接口替身（fakequeue）下 handler 注册、状态守卫重投递丢弃、重试耗尽终态；
   - datax：导入状态机守卫（重放 409）、校验管线结构层规则矩阵、excelize 生成断言（内存打开校验表头/单元格类型/meta 区）、错误 Excel 行定位、导出游标分批推进；
   - printing：模板 fields 校验、快照冻结、条码 PNG 生成（各码制非空 PNG 断言）、装配幂等；
   - devices：激活协议（token 一次性/过期/错码）、匹配器管线顺序与多命中（reader 替身）、scan_logs 降级路径；
   - reports/sysops：补货建议公式与依据字段（reader 替身注入边界值）、dedup_key 复合收件人去重与接收人解析规则（§10.6，reader 替身）、cron 包装器注入 clock、指标环形采样器；
   - seed/permissions 一致性（既有 seed_test 模式扩展 M3 清单）。
2. **集成测试（`//go:build integration`，实际机制 = SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控的外部 PG/Redis 实例、未设置即 Skip；testcontainers 为备选方案未落地，默认不编译）**：九类导入全流程（含 INITIAL_INVENTORY 原语落库与流水断言、分批失败续跑）；10 万行导出内存上限 + 单元格类型 + TIME_RANGE/合计行；打印任务装配与历史；设备激活→心跳→配置下发→停用失效；resolve 真库各类型命中（含 CH-/SH- 单号分派）；预警扫描幂等（重复执行同日同对象**按收件人复合 dedup_key** 不产生重复通知）；备份登记状态机（REQUESTED 守卫/RUNNING 超时核对——pg_dump 执行属部署侧脚本，不在应用集成测试面）。
3. **并发**：`go test -race ./...` 覆盖 inline 队列与指标采样器；导入 confirm 并发重放恰一成功。
4. **验收走查**：requirements 场景 5（Excel 全向导）、6（打印预览链路后端段）、8（resolve + 收货业务 API 组合链路后端段）后端演示脚本。

## 15. M3 明确不做（附原因，M2 §12 同构）

| 不做 | 原因 |
|---|---|
| StockFlow Scan Android 应用、Scanner 抽象层、Zebra/Honeywell/UROVO/HID Adapter、离线容错（devices.md D1、D3–D7、D17–D18） | 前端/scan 域职责（scan/ 目录独立应用）；后端仅交付其依赖的设备 API/resolve/扫码审计（D2/D8–D16 的接口支撑面、D19 监控数据源）；真机验收（场景 9）随 scan 域立项 |
| 系统资源指标采集（CPU/内存/磁盘，gopsutil） | **Orchestrator 裁决①**（2026-10-03）：不引入 gopsutil，豁免至阶段 21（§3.1/§10.4）；deployment §5 缺口由 deployment.md §5 注记与部署侧宿主机监控（磁盘容量告警）覆盖 |
| 审计日志/扫码日志应用内清理（log_cleanup） | grants 审计红线：app_grants.sql 对 operation_logs/login_logs（M3 含 scan_logs/device_logs）REVOKE UPDATE/DELETE（database §7.2），应用进程任何连接不得持审计表删除权——应用内任务生产环境每晚 permission denied（独立评审 2026-10-03 收敛）；清理由部署侧维护脚本（独立维护角色）按保留期执行（grants 维护角色段 + runbook，database.md §7.3 注记同步） |
| 数据库备份应用内执行（backup_daily） | **Orchestrator 裁决②**（2026-10-03）：备份=登记+外部执行混合（§10.5）——应用进程不持 pg_dump，Dockerfile 不追加 postgresql-client，备份执行器随 deployment.md §4 交付 |
| 设备远程注销（devices.md §7.1） | 前端先行契约已自注不在本期（web/src/api/device.ts:183）；M3 以停用（token_version+1 全量失效）覆盖安全诉求；远程注销随 scan 域立项（设备端数据清除协议属 scan 域） |
| 设备配置二维码（devices.md §7.5） | 批量部署诉求由激活二维码（含 server_url/device_code）+ 激活后配置自动拉取（§8.2）覆盖；独立配置二维码随真实批量部署需求立项 |
| app_versions 管理写入端点 | devices §7.4"架构预留"：000013 建表但 M3 无写入面（无 APK 上传/OTA，见下行）；latest 查询无已发布行返回 404（DEVICE_APP_VERSION_NOT_FOUND）——管理端点与种子数据随 scan 域立项补齐，避免死表误读 |
| boxes/box_items/pallets/pallet_items 建表 | 箱码语义已由 M2 packing_records（BP 包裹）承载，建表即第二真相来源（回写 database.md 注记）；托盘无业务流来源，无中生有建表违背"围绕业务模型设计"（database §1）——resolve 对托盘码明确返回未启用 |
| dictionaries 表 | 无消费方（system_configs 已覆盖 M3 配置面），避免提前引入配置面（M2 §12 同口径） |
| 报表汇总表与"数据统计/报表汇总"定时任务（architecture §9.1）、慢查询治理、索引专项优化 | 现场聚合决策（§9.2：architecture §7 允许"汇总表**或**异步任务"路径二选一）；阶段 21 性能优化（§9.2 已留演进路径） |
| 异常检测定时任务（architecture §9.1） | M2 各域异常均为业务动作创建（EX 异常单），无独立"检测规则"需求输入；检测需求出现随 inventory-rules 演进立项（architecture §9.1 偏离记录） |
| 服务端 PDF 生成 | **Orchestrator 裁决③**（2026-10-03）：M3 不做，前端对齐清单记录（§12.4 条 3）；需新增 PDF 库属 architecture §11.2 基线变更；前端 react-to-print 已覆盖打印与另存（§7.2） |
| APK 上传/OTA 升级执行 | devices §7.4"架构预留"——表与接口就绪，升级执行随 scan 域 |
| SSE/WebSocket 通知实时推送 | 前端轮询已满足 M3 演示；推送属增强（architecture §11.2 预留选型不变） |
| 库位推荐（完整算法）、库存预测 | requirements §2.5 另两项智能能力依赖数据积累与算法调参，inventory-rules §11 中 M3 只落地补货建议/积压识别最小只读版 |
| cron 表达式运行时修改、多副本任务分片 | architecture §9.2 偏离记录：任意 cron 表达式注入风险，M3 冻结注册表表达式仅开 启停（§10.3/§4.3）；单实例语义，多副本部署属 M4 阶段 22 |
| 采购申请、批量/波次拣货、物流公司 API | M2 §12 既有决策延续 |
| 测试补齐专项、生产部署编排完善（Docker 之外的 K8s/监控告警外部对接） | 阶段 20/22（deployment §10 Docker Compose 已就绪；备份执行脚本随 §10.5 混合模式交付，Dockerfile 无 M3 追加） |

## 16. 文档回写清单（文档先行——业务语义与路由契约类随 MT0 schema 冻结提交，实现早于文档的窗口期为零）

**MT0 批次（与 000011–000014 迁移同 commit，changelog 同步记录）：**

1. `api.md §1`：/api/imports /api/exports /api/prints /api/devices /api/scanner /api/reports /api/logs /api/system /api/notifications 各组端点明细（含 M3 新增端点：prints execute/barcode、devices 设备端组与 GET /{id}/activation、files preview、backups 登记）；`/api/files` 补录（api.md §1 未列独立文件段，采纳前端先行路径）；错误码清单追加 M3 段（DATAX_*/PRINT_*/DEVICE_*/SCANNER_*（UNKNOWN_BARCODE 等按 scanner §6.1 字面）/REPORT_*/SYSTEM_*）。
2. `database.md §2`：M3 落地注记——平台支撑组补 import_task_rows/print_task_rows/backup_records/scheduled_job_runs；扫码与容器组注"boxes/pallets 不建表（箱码=packing_records、托盘未启用）"；文件下载记录复用 operation_logs 注记；**attachments 由 files 表承载**（attachments 不建表，字段对照 file_name/stored_name/storage_path/mime_type/size_bytes/module/business_no/uploader/expires_at/deleted_at，§6.4）；**dictionaries 不建表**（system_configs 覆盖，§15）——两条与 boxes/pallets 注记同批提交；`database.md §7.3` 追加注记：审计日志/扫码日志保留策略由**部署侧维护脚本（独立维护角色）**定时执行，应用运行账号无 UPDATE/DELETE（grants 红线，§4.3/§15）。
3. `excel.md`：§3 注（下载记录=operation_logs 落位）、§5 注（清理任务 code=file_cleanup）；§2.2 注（TIME_RANGE=按创建时间区间导出，params 标准键 time_from/time_to，与 BY_FILTER 并列）；§2.3 注（合计行经 ExportSource.Summary 可选生成；导出为全量数据集，分页信息不适用、分组列 M3 不做）。
4. `printing.md`：§4.2 注（后端条码端点 /api/prints/barcode 与码制覆盖）。
5. `devices.md §6.2`：激活二维码内容协议（JSON 三字段 + token 一次性语义）注记；设备令牌 TTL 365 天与 token_version 撤销语义注记（§8.2）；§13.2 注记：scan_logs.device_id/device_code 可空（Web HID 场景以 user_id/username/ip 归因，设备端调用经 DeviceAuthRequired 携带设备上下文，§8.3）。

**MT-final 批次（纯记录性）：** 6. `docs/changelog.md`、`docs/tasks/current.md`：M3 交付与状态记录；`docs/deployment.md`：§1.1 环境变量清单追加 storage/queue/sysops 键（pg_dump 路径与日志保留期键**不引入**——备份执行与审计日志清理属部署侧脚本，参数为脚本环境变量，§3.2/§10.5/§15）、§4 备份执行器脚本与 crontab 样例（裁决②）、§4.2 附恢复演练 runbook（若交付）、§5 注记（CPU/内存/磁盘指标豁免至阶段 21——裁决①；磁盘容量告警由宿主机监控承担）。

## 17. 任务拆分与并行安排（每 Task 一个 commit，Conventional Commits 中文实践）

| Task | Scope | 内容 | 依赖 | 验证 |
|---|---|---|---|---|
| MT0 | I | 依赖引入（§3.1）+ 配置扩展 + internal/asynqx + docnum 前缀 + **permissions.go M3 段一次性追加** + 000011–000014 迁移 + grants 扩充（含维护角色段）+ health 存储检查 + 守卫扩展（guard-readonly 白名单模式）+ **§16 契约类回写（api.md/database.md/excel.md/printing.md/devices.md）** + MT0 契约评审会（§2.2 接入文件清单 + 规则③导入承载性核验与最小改动清单 + §6.1 列定义 + §12.2 窄接口签名与前缀→实现映射表 + §8.2 激活协议 + §10.6 接收人解析冻结）→ **schema 冻结点** | — | build/vet/test 全绿；migrate up/down 双向；守卫零命中 |
| MT1 | X | internal/datax 全量 + §2.2 域包接入文件（9 写入器 + 15 行源；REPORT 行源归 MT4）+ /api/imports /api/exports /api/files | MT0 | §14 导入导出测试；swag |
| MT2 | P | internal/printing 全量 + 5 个 printing_content.go + /api/prints | MT0 | §14 打印测试；swag |
| MT3 | D | internal/devices 全量 + 7 个 devices_resolve.go + /api/devices /api/scanner | MT0 | §14 设备/解析测试；swag |
| MT4 | R | internal/reports 全量 + 4 个 reports_reader.go + /api/reports + /api/inventory/summary\|alerts | MT0 | §9 报表/建议公式测试；swag |
| MT5 | S | internal/sysops 全量 + 3 个 sysops_reader.go + /api/logs /api/system /api/notifications + cron 接线（五任务）+ 备份登记/状态核对（裁决②，执行器脚本由 I 侧随 deployment.md §4 交付、本任务联调） | MT0 | §14 扫描幂等/监控/备份登记测试；swag |
| MT6 | I | router 全量装配（含 asynq/cron/指标中间件接线）+ seed 收编核验 + swag 汇总 | MT1–MT5 | 全接口冒烟；越权 403 |
| MT7 | 全体 | §14 集成测试 + `-race` + 守卫全绿 + requirements 场景 5/6/8 后端走查 + changelog + tasks/current.md | MT6 | development-plan §5 M3 后端验收 |

并行图：`MT0 → { MT1 ∥ MT2 ∥ MT3 ∥ MT4 ∥ MT5 } → MT6 → MT7`。**优先级建议**：阶段顺序 14（MT1）→ 15（MT2）→ 16 后端支撑（MT3）→ 17（MT4）→ 18（MT4 内智能部分殿后）→ 19（MT5）；智能能力（§9.3）在 MT4 内最后实施（inventory-rules §11 只读、依赖前序数据面）。

## 18. 风险与开放问题

1. **gopsutil 引入——已裁决关闭**（Orchestrator 裁决①，2026-10-03）：不引入 gopsutil，系统资源指标（CPU/内存/磁盘）豁免至阶段 21（§3.1/§10.4/§15）；监控端点交付 DB/Redis/连接池/队列/定时任务/版本/运行时长指标。原选项 A（引入）/B（手写 /proc）不再评审，C（豁免）采纳。
2. **服务端 PDF——已裁决关闭**（Orchestrator 裁决③，2026-10-03）：M3 不做（§7.2），差异记录于前端对齐清单（§12.4 条 3）。若产品后续拍板必须后端 PDF（如 PDA 端直连打印机场景），需先改 architecture §11.2 引入 PDF 库（gofpdi/chromedp/print-robin 类），属基线变更，建议 M3 后评估。
3. **备份执行环境——已裁决关闭**（Orchestrator 裁决②，2026-10-03）：采用"备份记录登记 + 部署侧执行"混合模式（§10.5）——pg_dump 由宿主机/cron 容器执行器承担，应用侧只做 backup_records 登记/状态核对，Dockerfile 不追加 postgresql-client；执行器脚本与混合契约随 deployment.md §4 交付（§16 条 6、§17 MT0/MT5）。
4. **打印任务规模上限——已裁决关闭**（Orchestrator 裁决④，2026-10-03）：按方案默认冻结——单任务 data_ids ≤500、fields 绑定预设键白名单（§7.2）；波次级全量单据打印（数千行）需拆任务，前端批量入口按上限分片；若实际场景超限按 §3.2 类配置键调参，不阻塞。
5. **导出筛选白名单**：BY_FILTER 的 filters 键随各域列表查询冻结（MT0 评审逐模块确认），前端先行 `Record<string, string>` 需按模块收紧——契约回对时逐模块清单化，防止任意键注入查询。
6. **scan_logs 体量**：高频扫码（PDA 连续作业）下 scan_logs 增长快，保留期 180 天由部署侧维护脚本兜底（§15——应用内无删除通路）；若阶段 21 实测写入热点，再评估分区/归档（database §6.3 预留）。
7. **多副本部署下的 cron/queue 语义**：本方案按单实例冻结（advisory lock 防重、cron 热更团单实例）；多副本部署（阶段 22）需复核 asynq 多消费者天然安全、cron 依赖 advisory lock 的正确性——列入 M4 检查项。
