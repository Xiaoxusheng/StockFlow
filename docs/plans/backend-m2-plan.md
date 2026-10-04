# StockFlow 后端 M2 实施方案（development-plan 阶段 9–13）

> 日期：2026-10-03 ｜ 范围：Go 后端里程碑 M2——采购/入库/质检/上架、销售/出库/拣货/复核/打包/发货、调拨/盘点、退货/异常/追溯 ｜
> 依据：development-plan §1/§3/§5、business-flow §2–§13、inventory-rules §4/§5/§9/§10、api §1/§2/§4/§6/§7、database §2/§6/§7、architecture §3–§5、testing §2/§3/§4、permission §1/§6、[backend-m1-plan](backend-m1-plan.md)（冻结契约）、[backend-analysis](backend-analysis.md) ｜
> 本方案与 M1 计划同构：冻结 **包所有权契约、库存契约包边界、迁移 DDL 契约、状态机与原语消费映射、幂等键构成、权限点清单**，多工程师并行时以本文为合同，跨包修改视为违约（§2.3）。
> 写作前提的基线核验（2026-10-03 实测）：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；库存族写守卫 grep 在 `cmd`+`internal`（除 internal/inventory）零命中——M1 冻结契约成立，M2 在其上叠加，不重开已决事项。

---

## 1. 目标与验收

**M2 = development-plan §5**："业务闭环可用，requirements.md 场景 1–4、7 可演示"（场景 1 采购入库、2 销售出库、3 仓库调拨、4 盘点、7 追溯；requirements.md §6）。按 development-plan §3 九项 DoD 映射：

| DoD 项 | M2 交付 | 验证方式 |
|---|---|---|
| 数据库 | 000006–000010 共 5 组成对迁移（§5），全部单据表 + 编号基础设施 | `migrate up/down` 双向验证（database.md §9） |
| 后端 | 4 个单据域包 + internal/docnum + internal/stock 完整链路（architecture §1 分层） | 单元 + 集成测试（**实际机制：`//go:build integration` + SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控的外部 PG/Redis 实例，未设置即 Skip；testcontainers 为备选方案未落地**——testing.md §3） |
| API | 全部单据域接口 + swaggo OpenAPI + 后端完整校验（api §3/§4） | `make swag` + 接口测试 |
| 前端 | 不在本方案范围（前端页面已先行交付呈错误态，见 docs/tasks/current.md；后端冻结契约后由前端对齐轮回对） | — |
| 权限 | M2 全部接口挂 RequirePermission + 数据权限过滤（api §6、permission §2/§4） | 越权测试（testing §8） |
| 校验/错误处理 | 后端完整校验（api §4：状态/业务关系/库存）+ 新域错误码 `PURCHASE_*`/`SALES_*`/`STOCKOPS_*`/`RETURNS_*`/`DOCNUM_*` | 错误码响应测试 |
| 日志 | 全部状态迁移与敏感操作（审批/执行/取消）经 middleware.Audit 写 operation_logs（与业务同事务，architecture §4） | 日志断言 |
| 测试 | 状态机单测 + 四链路集成 + 并发/幂等/恒等式（testing §2/§3/§4 M2 子集，§11） | `go test -race` 全绿 |

**全局技术约定**沿用 M1 冻结值（backend-m1-plan §1）：module 根 = 仓库根、ID 字符串化、numeric(18,4)、`YYYY-MM-DD HH:mm:ss`、统一信封与分页、8080 端口——本文不再重复，违反即违约。

## 2. 包所有权划分（并行开发合同）

### 2.1 四个业务域 + 两个平台包

```text
internal/purchase    采购域：以采购订单为源头、入库单为骨架的"收货 → 质检 → 上架"主链路，
                     批次/效期/序列号三开关分支采集，交付业务闭环"采购入库"（business-flow §2–§5、场景 1）
internal/sales       销售域：以销售订单为源头的"审核预占 → 分配 → 拣货 → 复核 → 打包 → 发货"出库执行链，
                     正式扣减发生在发货完成（business-flow §6–§8、场景 2）
internal/stockops    库存作业域：跨仓调拨单（在途两端流水）、盘点单（冻结—实盘—差异—调整）与库存调整审批流
                     （business-flow §10、§11.1，场景 3/4）
internal/returns     退货域：销售退货（收货—质检—入正常/不良）、采购退货（审核—退货出库）、
                     异常中心（九类异常生命周期与异常冻结）、库存追溯查询（business-flow §9、§11.2、inventory-rules §10，场景 7）
internal/docnum      （新，平台）单据编号引擎：business-flow §13.1 统一编号规则，唯一单号发放入口
internal/stock       （新，平台）库存原语值类型契约包：域包消费 inventory 原语的唯一合法 import 面（§3）
```

### 2.2 Scope 划分与独占文件

| Scope | 工程师 | 独占文件/目录 | 迁移 DDL 确认义务 | 交付 |
|---|---|---|---|---|
| **P 采购入库** | 工程师 P | `internal/purchase/**` | 000007 内容确认 | 采购/入库/收货/质检/上架全链路 + `/api/purchases`、`/api/inbounds`、`/api/receipts`、`/api/putaway`、`/api/quality` |
| **S 销售出库** | 工程师 S | `internal/sales/**` | 000008 内容确认 | 销售/分配/出库/拣货/复核/打包/发货全链路 + `/api/sales`、`/api/outbounds`、`/api/allocations`、`/api/picks`、`/api/checks`、`/api/packing`、`/api/shipments`（路径严格对齐 api.md §1 既有命名） |
| **O 库存作业** | 工程师 O | `internal/stockops/**` + `internal/inventory/transfer.go`、`internal/inventory/adjustflow.go`（仅此两个新增文件，§8） | 000009 内容确认 | 调拨/盘点/调整审批 + `/api/transfers`、`/api/counts`、`/api/inventory/adjustments`（列表与审批执行）、`/api/inventory/moves`（仓内移库；后两组为 api.md §1 缺项，按 §13 回写补录） |
| **R 退货追溯** | 工程师 R | `internal/returns/**` | 000010 内容确认 | 退货/异常/追溯 + `/api/returns`（销售退货）、`/api/purchase-returns`、`/api/exceptions`、`GET /api/inventory/trace`（后三组为 api.md §1 缺项，按 §13 回写补录） |
| **I 集成平台** | 集成工程师 | `internal/docnum/**`、`internal/stock/**`、`db/migrations/000006–000010.*`、`internal/router/**`、`internal/database/seed.go`、**`internal/auth/permissions.go`（M2 权限点常量唯一写入人，MT0 一次性追加，见 §9.2）**、Makefile、`cmd/**`（如需）；以及 **MT0 期间对 `internal/inventory` 既有文件的一次性最小改动**（§8.3，改动清单冻结，此后该包既有文件冻结） | 全部 5 组迁移产出（域负责人确认制，沿 M1 §4.1 scope B 模式） | 契约包/编号引擎/迁移/router 装配/seed 收编（权限点+菜单+角色映射）/守卫扩展/api.md 回写 |

> **`GET /api/batches`、`GET /api/serials` 无 M2 缺口**：两接口 M1 已实际交付（internal/inventory/inventory.go RegisterRoutes 注册注释与 internal/inventory/handler.go:336/:375；权限点 inventory:batch:list / inventory:serial:list 已在 M1 种子与 internal/auth/permissions.go:118-119），backend-m1-plan §9"随 M2 交付"的预告由 M1 复核补录提前兑现。M2 不重复交付，仅约束其数据正确性（§6.1 收货采集、§6.7 盘点序列号口径）；`/api/inventory/locks`、`/api/inventory/adjustments` 列表为 M2 新增（§8.3 条 5）。

### 2.3 违约判据（验收时逐条机械可查，M1 §4.2 的 M2 扩展）

1. 修改他人 scope 文件（git diff 按目录归属判定）；**MT0 后 `internal/inventory` 既有文件出现任何改动**（唯一例外：工程师 O 的新增文件 transfer.go / adjustflow.go）。
2. 域包 import 另一个业务域包（`internal/stock` 与 `internal/auth` 是仅有的两个域间合法 import，§3）。
3. `internal/inventory` 之外任何代码对库存族六表出现写 SQL，或引用库存族 GORM 模型（M1 §4.2 判据 3 沿用，CI 守卫不变）；**工程师 O 的两个新增文件因位于包内而豁免 grep，但评审须核对其只经包内 helper（applyDelta/buildLedger 等）落库，不得另写旁路 SQL**。
4. 单据状态迁移绕过状态机守卫：任何代码出现 `UPDATE ... SET status = <目标态>` 而无 `WHERE status = <前置态>` 行数判定（business-flow §13.2 硬性规则；CI 守卫纳入 `make ci`，§10.4）。
5. 绕过 internal/docnum 生成任何业务单号/流水号（rg `newBusinessNo` 在 internal/inventory 外零命中；M1 过渡实现随 MT0 删除）。
6. 单据域 handler 绕过 `internal/response`；审计绕过 `middleware.Audit`；事务边界出现在 handler/repository 层（M1 判据 2/7 沿用）。
7. 跨域数据访问未走 §3.1 消费方窄接口 + router 注入（无"授权只读 SELECT 他人表"旁路）。
8. 域包自建迁移文件（迁移统一由集成工程师在预留编号内交付）。
9. seed 中出现 M2 计划 §9 清单外的权限点/动作词。

## 3. 依赖方向与库存契约包 internal/stock（M2 关键基建决策）

**问题**：M1 规则"域包之间禁止互相 import"（backend-m1-plan §3），但 M2 四个域全部要消费 `internal/inventory.Service` 的九个原语（M1 §8.2 冻结面：Putaway/Deduct/Lock/ReleaseLock/MoveBin/InspectResult/Adjust + EnsureBatch/SerialEvent）。原语签名携带 `PutawayOp` 等 inventory 包类型——域包直接 import inventory 即引入"域间依赖 + 库存模型类型暴露"两个违约面。

**决策：引入叶子契约包 `internal/stock`**（不依赖任何内部包，仅标准库 + database）：

```text
internal/stock/            值类型契约（M1 已冻结的形状原样搬迁）：
  qty.go                   Qty（numeric(18,4) 精确十进制）
  actor.go                 Actor、Source、RowKey
  state.go                 StockState、StateColumn、锁定类型常量（lockTypes 语义）
  ops.go                   PutawayOp/DeductOp/LockOp/ReleaseLockOp/MoveBinOp/InspectResultOp/AdjustOp/BatchOp/SerialOp
  result.go                MutationResult、LedgerRef
internal/inventory/        类型别名承接（零成本，M1 全部既有代码与测试不改逻辑）：
  type Qty = stock.Qty      （其余 op/结果类型同理）
  Service 方法集不变—— *inventory.Service 结构化满足各域消费接口
```

各域在**自己包内**定义最小消费接口（消费方窄接口原则不变），方法签名只引用 `stock` 值类型：

```go
// internal/purchase/ports.go —— 示例：purchase 需要的原语子集
type StockGateway interface {
    Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error)
    EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (batchID int64, created bool, err error)
    SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (serialID int64, created bool, err error)
    InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error)
}
// router 装配：purchase.RegisterRoutes(protected, db, rdb, purchase.WithStock(inventory.NewService(db, rdb)))
```

规则：① 域包唯一允许的两个域间 import = `internal/stock` + `internal/auth`（后者是 M1 既有公共契约先例，internal/inventory/handler.go:10 等）；② `internal/inventory` 的 GORM 模型仍仅可被本包命名（M1 §4.2 判据 3 的类型级防线不放松）；③ `stock` 包禁止出现任何 SQL/IO；④ inventory.RegisterRoutes 冻结签名不动（backend-m1-plan §5"冻结签名，M2 亦不得变更"）——router 为四域各自构造 `inventory.NewService(db, rdb, …)` 经 Option 注入，多实例无共享状态（Service 仅持 db/rdb/checker，internal/inventory/service.go:27-50），不构成第二套库存入口。

### 3.1 跨域窄接口清单（M2 全量；消费方包内定义、router 装配注入、最小化）

机制沿用 M1 §4.3 唯一跨域机制；**接口定义在消费方**，实现方读自己的表（M1 先例：internal/warehouse/ports.go:41-85 的 Checker 导出模式）。除 StockGateway（签名引用 `stock` 值类型）外，其余接口签名一律内建类型，不携带任何域类型：

| 消费方定义 | 实现方（router 注入） | 方法（最小形态） | 用途 |
|---|---|---|---|
| purchase.StockGateway / sales.StockGateway / stockops.StockGateway / returns.StockGateway | `inventory.NewService(db, rdb)`（§3） | 各域所需原语子集（Putaway/Deduct/Lock/ReleaseLock/InspectResult/EnsureBatch/SerialEvent/MoveBin/Adjust 按域裁剪） | §7 映射的全部库存动作 |
| purchase.SKUAttrReader、sales.SKUAttrReader | masterdata（新增导出 `NewSKUFlagReader(db)`） | `GetFlags(ctx, skuID) (Flags{Enabled, BatchManaged, ExpiryManaged, SerialManaged}, error)` | 收货/上架/拣货的批次/效期/序列号三开关分支（business-flow §1.2） |
| purchase.SupplierChecker | masterdata（新增导出） | `ExistsActive(ctx, supplierID) (bool, error)` | 采购订单业务关系校验（api §4） |
| sales.CustomerChecker | masterdata（新增导出） | `ExistsActive(ctx, customerID) (bool, error)` | 销售订单业务关系校验 |
| purchase.BinChecker / sales.BinChecker / stockops.BinChecker | `warehouse.NewBinChecker(db)`（既有，internal/warehouse/ports.go:71） | `ExistsActive(ctx, warehouseID, binID) (bool, error)` | 入库/出库/调拨/盘点的库位校验（消费方各自定义接口，结构化满足） |
| returns.SalesOrderReader | sales（新增导出） | `FindReturnable(ctx, soNo) (soID, warehouseID int64, lines []struct{ LineNo, SKUID, QtyShipped int64 }, found bool, err error)` | 销售退货校验销售单存在与可退数量（逐行退货量由 returns 域自己的流水累计比对） |
| returns.PurchaseOrderReader | purchase（新增导出） | 同形（poNo → lines QtyReceived） | 采购退货校验采购单存在与已收数量（退量 ≤ 收量） |
| returns.QCCreator | purchase（新增导出） | `CreateQC(ctx, sourceType, sourceNo string, qcType string, lines …) (qcNo string, err error)` | 退货质检复用 purchase 质检单（§4 质检单一套实现，禁止 returns 另造） |
| purchase/sales/stockops.ExceptionCreator | returns（新增导出） | `Create(ctx, excType, sourceType, sourceNo string, detail …) (exceptionNo string, err error)` | 收货/拣货/复核/盘点异常登记统一进异常中心（§11.2 九类） |
| masterdata.DocReferenceChecker | router 闭包桥接（sales/purchase/returns 各自导出 `HasSKUReference/HasCustomerReference/HasSupplierReference` 等只读实现） | `HasReference(ctx, kind string, id int64) (bool, error)` | SKU/供应商/客户删除前引用校验（business-flow §1.4"已产生业务记录不可删"；M1 计划 §6.2 suppliers 行预告的 M2 义务，沿 binOccupancyBridge 先例 internal/router/router.go:96） |

规则：每个接口 1–2 个方法、缺省 fail-closed（未注入即启动失败或业务拒绝，沿 M1 §4.3 规则①）；新增跨域需求必须先进本表评审再实现（§2.3 判据 7）。

## 4. 单据编号引擎 internal/docnum（business-flow §13.1）+ M1 流水号缺口

### 4.1 引擎设计

- **格式**：`{前缀}-{YYYYMMDD}-{6位流水}`（§13.1 示例口径）；前缀与流水位宽为代码内冻结规则注册表值，"仓库编码段/日期段/前缀可配置"预留为规则结构体字段（`Rule{Prefix, DateSeg, SeqWidth, Reset}`），M2 不开放运行时配置（system_configs/dictionaries 表属阶段 14+/19，M1 计划 §9 已明确缓引）。
- **发放机制**：计数表 `doc_number_counters(prefix, period, next_no)`，发放 = 事务内 `INSERT ... ON CONFLICT DO NOTHING` + `UPDATE doc_number_counters SET next_no = next_no + 1 WHERE prefix=? AND period=? RETURNING next_no`（行锁原子；period='YYYYMMDD' 按日重置、period='ALL' 永不重置）。流水号发放必须与单据创建同事务，事务回滚则号码作废出现空洞——§13.1 不要求连续，接受。
- **前缀注册表（M2 冻结全量）**：PO 采购单、IN 入库单、RC 收货单、QC 质检单、PW 上架任务、SO 销售订单、OUT 出库单、PK 拣货任务、CH 复核任务、BP 包裹、SH 发货单、TR 调拨单、CK 盘点单、RT 退货单、EX 异常单（§13.1 已列 PO/IN/OUT/TR/CK/QC，其余为本方案补录，随 §13 回写 §13.1）；**LED 流水、ADJ 调整单为 M1 存量前缀承接**（repository.go:48 `newBusinessNo` 现生成 LED-/ADJ-，随 MT0 切换引擎、格式规则不变），与新增前缀同受判据 9 约束。
- **M1 缺口关闭**：`internal/inventory/repository.go:44-52` 的 `newBusinessNo`（随机 8 位 hex 后缀 + 唯一索引重试，M1 迁移 000005 未交付 sequence 的过渡方案）在 MT0 切换为 `docnum.Next(ctx, tx, Rule{Prefix:"LED"/"ADJ", Reset:"ALL"})`，`LED-YYYYMMDD-NNNNNN` 满足 M1 计划 §8.4"日期段 + 独立流水"语义；存量 `LED-*`/`ADJ-*` 旧格式行只读不受影响。
- **为何不用裸 PG sequence**：§13.1 示例流水按日从 000001 起，PG sequence 无法按日重置（需 cron，属阶段 19）；计数表发放与 nextval 同为单语句原子操作，代价同级。**此为对 ask 字面"PG sequence"的有意偏离**，理由如上，列入 §13 开放问题——若拍板必须用 PG sequence，则接受跨日流水连续，000006 改建每前缀一条 sequence，引擎接口不变。

## 5. 迁移划分（000006–000010，成对 up/down；集成工程师产出、域负责人确认）

通用规则沿用 M1 §6.4：**不建跨域外键**，跨域引用一律 `*_no varchar(64)` 逻辑单号或裸 ID + Service 层校验；数量/金额 `numeric(18,4)`；业务表通用字段按 database.md §3；状态列全部挂 CHECK 约束（与 §6 状态机同源，迁移是状态机的第二道防线）；每迁移本地 up+down 双向验证后提交。

### 000006_create_doc_shared_tables.up.sql（集成工程师）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| doc_number_counters | prefix varchar(16), period varchar(8), next_no bigint | **PK(prefix, period)**；发放走 UPDATE RETURNING |
| document_approvals | target_type varchar(32), target_no varchar(64), action varchar(16)(SUBMIT/APPROVE/REJECT/CANCEL), result, opinion text, operator_id/name, created_at | idx(target_type, target_no)；**纯审计 append-only**：无 updated_*，应用层无 UPDATE/DELETE 通路，`db/grants/app_grants.sql` 追加该表仅 INSERT（database.md §7.2） |

> 审批记录落位说明：business-flow §12.2 要求审批记录（审批人/时间/意见/结果）不可修改删除——统一落本表；各单据只存冗余的 approved_by/at 便于列表展示。

### 000007_create_purchase_tables.up.sql（工程师 P 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| purchase_orders | po_no UNIQUE, supplier_id, **warehouse_id（收货仓，必填——数据权限过滤与到货校验依据）**, status CHECK(DRAFT/PENDING_APPROVAL/APPROVED/PARTIAL_RECEIVED/RECEIVED_ALL/COMPLETED/CANCELLED)（与 business-flow §2.2 状态机同源，无 CLOSED；差额关闭走 COMPLETED+审计原因）, 原始金额, business 时间组（approved_at 等 §13.4） | idx(supplier_id)、idx(warehouse_id, status) |
| purchase_order_items | po_id, line_no, sku_id, qty_ordered, **qty_received, qty_rejected, qty_putaway**（§2.3 四量约束的落列） | UNIQUE(po_id, line_no)；idx(sku_id) |
| inbound_orders | inbound_no UNIQUE, source_type(PURCHASE/OTHER), source_no（PO 单号，逻辑引用）, warehouse_id, status CHECK(DRAFT/RECEIVING/AWAITING_QC/AWAITING_PUTAWAY/COMPLETED/CANCELLED/CLOSED), received_at/inspected_at/putaway_at/completed_at | idx(warehouse_id, status)、idx(source_type, source_no) |
| inbound_items | inbound_id, line_no, sku_id, qty, qty_received, qty_inspected, qty_putaway | UNIQUE(inbound_id, line_no) |
| receipts | receipt_no UNIQUE, inbound_no, warehouse_id, batch 采集列（batch_no/expiry_date/production_date）, **idempotency_key varchar(128) NULL** | **部分唯一索引 uk_receipts_idempotency ON (idempotency_key) WHERE idempotency_key IS NOT NULL**（architecture §3.2 收货幂等）；idx(inbound_no) |
| receipt_items | receipt_id, line_no, qty_good, qty_rejected, exception_ref（异常单号，逻辑引用） | UNIQUE(receipt_id, line_no) |
| putaway_tasks | putaway_no UNIQUE, inbound_no, source receipt_no, sku_id, qty, from_state(QC 结果：直接 available/经 pending_inspect), target bin 四维, status CHECK(PENDING/IN_PROGRESS/COMPLETED/CANCELLED), claimed_by/at, completed_at | idx(status)、idx(inbound_no) |
| quality_orders | qc_no UNIQUE, source_type(INBOUND/RETURN), source_no, **warehouse_id（质检作业仓，数据权限过滤）**, inspection_type(免检/抽检/全检 §4.1), qty_inspected/qualified/defective, result CHECK(合格/部分合格/不合格/退供应商/报废/返工/降级/转不良品仓/特批放行 §4.3), inspector/at, image_refs jsonb（文件中心阶段 14 前仅占位，不提供上传接口） | idx(source_type, source_no)、idx(warehouse_id, status) |
| quality_items | qc_id, line_no, sku_id, batch_no, qty_inspected/qualified/defective | UNIQUE(qc_id, line_no) |

> **不建 `defective_inventory` 表**：database.md §2 是"至少覆盖"清单（表结构按项目架构调整），不良品库存状态已由 `inventory.defective_qty` 列承载（backend-analysis §5 决策一以贯之），不良品**库位**由 zone_type=不良品区的库位表达。随 §13（MT0 批次）回写 database.md 注记。

### 000008_create_sales_tables.up.sql（工程师 S 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| sales_orders | so_no UNIQUE, customer_id, warehouse_id, 收货地址/配送方式, status CHECK(DRAFT/PENDING_APPROVAL/APPROVED/REJECTED/PARTIAL_SHIPPED/SHIPPED_ALL/COMPLETED/CANCELLED), approved_at/shipped_at/completed_at | idx(customer_id)、idx(warehouse_id, status) |
| sales_order_items | so_id, line_no, sku_id, qty, price, amount, **qty_allocated/qty_shipped** | UNIQUE(so_id, line_no) |
| outbound_orders | outbound_no UNIQUE, so_no, type CHECK(销售出库/其他出库…§7.1), warehouse_id, status CHECK(PENDING_ALLOCATE/ALLOCATED/PICKING/PICKED/CHECKED/PACKED/PARTIAL_SHIPPED/SHIPPED_ALL/CANCELLED/CLOSED)（终态：SHIPPED_ALL 全部发货完成 / CLOSED 差额关闭 / CANCELLED，与 §6.5 迁移行同源）, picked_at/checked_at/packed_at/shipped_at | idx(so_no)、idx(warehouse_id, status) |
| outbound_items | outbound_id, line_no, sku_id, qty, qty_picked/qty_checked/qty_packed/qty_shipped | UNIQUE(outbound_id, line_no) |
| allocation_records | outbound_no, line_no, sku_id, batch_id, warehouse_id/bin_id, qty, strategy CHECK(FIFO/FEFO/指定批次/指定仓库/指定库位 §8.1 五值；"指定仓库"落订单级选仓或在 allocation_records 记人工指定), reason jsonb（分配理由：命中原因+可用量快照，§8.1 展示义务）, lock_id | idx(outbound_no)、idx(sku_id, batch_id) |
| pick_tasks | pick_no UNIQUE, outbound_no, outbound_line_no, sku_id, batch_id, **来源库位四维（warehouse/zone/shelf/bin——§8.2 任务内容"SKU→来源库位→数量"）**, qty, picked_qty, status CHECK(PENDING/CLAIMED/PICKING/PICKED/EXCEPTION/CANCELLED), assignee, claimed_at/picked_at, scan 校验列, **warehouse_id（创建时自出库单冗余，数据权限过滤）** | idx(outbound_no)、idx(assignee, status)、idx(warehouse_id, status) |
| check_tasks | check_no UNIQUE, outbound_no, outbound_line_no, sku_id, batch_id, qty, serial 核验记录（序列号 SKU 逐件确认 §8.3）, status CHECK(PENDING/DONE/EXCEPTION), result 列（§8.3 五类复核异常）, **warehouse_id（同上冗余）** | idx(outbound_no)、idx(warehouse_id, status) |
| packing_records | package_no UNIQUE, outbound_no, 包装材料/长宽高/重/体积/快递公司/快递单号（§8.4 全列）, **warehouse_id（同上冗余）**, **idempotency_key varchar(128) NULL** | uk_packing_idempotency（部分唯一）；idx(outbound_no) |
| packing_items | package_id, outbound_id, line_no, qty（**包裹×明细多对多**，§8.4 拆包） | UNIQUE(package_id, outbound_id, line_no) |
| shipments | shipment_no UNIQUE, outbound_no, 物流公司/单号/发货仓 warehouse_id/发货人/包裹数（§8.5 发货仓落列）, status CHECK(PENDING/SHIPPED/IN_TRANSIT/SIGNED/ABNORMAL §8.5), shipped_at, **idempotency_key varchar(128) NULL** | uk_shipments_idempotency（部分唯一）；idx(outbound_no)、idx(warehouse_id, status) |

### 000009_create_stockops_tables.up.sql（工程师 O 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| transfer_orders | transfer_no UNIQUE, type CHECK(WAREHOUSE/BIN §10.1), from/to warehouse_id, status CHECK(DRAFT/PENDING_APPROVAL/APPROVED/TRANSFERRING/AWAITING_RECEIPT/COMPLETED/CANCELLED)（六态对齐 §10.1：APPROVED=待出库、AWAITING_RECEIPT=待入库，§6.6）, outbound_at/received_at | idx(from_warehouse_id, status)、idx(to_warehouse_id, status)（database.md §6.1：两端仓库均为列表检索条件） |
| transfer_items | transfer_id, line_no, sku_id, batch_id, from/to bin 四维, qty, **qty_out(源仓已出)/qty_in(目标已收)**，在途 = qty_out - qty_in（§4 在途口径） | UNIQUE(transfer_id, line_no) |
| count_orders | count_no UNIQUE, scope jsonb（全盘/按仓/区/架/位/SKU §10.2 范围快照）, warehouse_id, status CHECK(DRAFT/COUNTING/PENDING_REVIEW/COMPLETED/CANCELLED), frozen_at/reviewed_at/completed_at | idx(warehouse_id, status) |
| count_items | count_id, inventory_row_id, sku_id/bin 四维, qty_system(冻结时快照), qty_counted(实盘登记), counted_by/at, **serial_no varchar(128) NOT NULL DEFAULT ''（序列号 SKU 逐件登记一行 qty=1，inventory-rules §8.2"盘点必须逐个序列号操作"；非序列号 SKU 为空串）** | UNIQUE(count_id, inventory_row_id, serial_no)；idx(count_id, status) |
| count_differences | count_id, line_no, sku_id/bin/batch, qty_system/qty_counted, diff_qty(盘盈为正), adjust_no（差异批准后生成的调整单单号，逻辑引用）, status(PENDING/APPROVED/REJECTED/EXECUTED) | UNIQUE(count_id, line_no)；idx(count_id, status) |

> **在途库存不加列**：inventory 六列恒等式 CHECK（000005）冻结，在途不属于任何库位、不入恒等式；在途数量 = `transfer_items` 中 `status=TRANSFERRING` 单据的 `qty_out - qty_in` 聚合（§10.1"跨仓调拨在途数量通过在途库存体现"的 M2 落地口径），随 §13（MT0 批次）回写 inventory-rules §2。

### 000010_create_returns_tables.up.sql（工程师 R 确认）

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| return_orders | return_no UNIQUE, type CHECK(SALES/PURCHASE), source_no（销售/采购单号，逻辑引用）, customer_id 或 supplier_id, warehouse_id, status CHECK(DRAFT/PENDING_APPROVAL/APPROVED/RECEIVING/IN_QC/COMPLETED/CANCELLED 及退货出库态 SHIPPED), approved_at/received_at/qc_at/completed_at | idx(type, status)、idx(source_no) |
| return_items | return_id, line_no, sku_id, qty_return, qty_received/qty_inspected/qty_defective, reason | UNIQUE(return_id, line_no) |
| exceptions | exception_no UNIQUE, type CHECK(收货/质检/上架/库存/拣货/复核/物流/盘点/系统异常 九值 §11.2), source_type/source_no, sku_id/bin/serial 可空定位, status CHECK(OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW/RESOLVED/CLOSED), assignee/责任人与处理记录 jsonb, image_refs jsonb（占位同上）, freeze 相关（freeze_lock_id 可空——异常冻结锁回指） | idx(type, status)、idx(source_type, source_no) |

> **追溯（场景 7）不建新表**：inventory-rules §10 明确追溯数据来源 = 库存流水 + 业务单据 + 操作日志，三者 M1/M2 均已 append-only 落库；追溯是查询编排接口（§2.2 R 交付 `GET /api/inventory/trace`），任何"追溯中间表"都是第二真相来源，禁止。

**幂等键部分唯一索引口径**（architecture §3.2 三方式之落位）：库存变更类动作的幂等最终准绳仍是 `inventory_ledgers.idempotency_key`（000005 已建）；HTTP 事件型表（receipts/packing_records/shipments）加 `idempotency_key` 部分唯一索引承接"用户连续点击两次不能执行两次"的第一层；任务类（pick/check/putaway/盘点实盘）以状态机守卫（`WHERE status='<前置态>'` 影响行数判定）与原子抢占天然幂等，不加键。键构成见 §7。

## 6. 各单据类型状态机（business-flow §13.2：所有状态变化必须经业务方法守卫）

统一实现形态：`repository.UpdateStatus(tx, id, from, to, extra...)` 生成 `UPDATE ... SET status=?, <环节时间列>=now() WHERE id=? AND status=?`，影响行数 0 = 状态冲突（`COMMON_CONFLICT` 语义，各域注册域内码）；同事务写 `middleware.Audit`（before/after 含 status，即 §13.2"变化前后/操作者/时间/原因"的落位，**不另建状态历史表**——避免与 operation_logs 构成第二套审计，回写 §13）。以下各表"事务内容"均指与状态迁移同事务的完整步骤（architecture §4）。

### 6.1 采购订单 purchase_orders（§2.1–§2.3、§12.1）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| DRAFT→PENDING_APPROVAL | 明细 SKU 启用、数量>0、单价≥0 | 状态+submit 审批记录 | 校验失败 4xx，无副作用 |
| PENDING_APPROVAL→APPROVED | 审批权限 | 状态+APPROVE 审批记录 | 同上 |
| PENDING_APPROVAL→DRAFT | — | 状态+REJECT 审批记录（含意见） | — |
| APPROVED→PARTIAL_RECEIVED / RECEIVED_ALL | **收货服务强校验：原始数量 ≥ 已到货 + 本次；累计收货（合格+不合格待定）≤ 原始数量；拒收单独记录（§2.3）** | 收货落 receipts（幂等键）→ 重算四量 → 推进状态 | 超量收货 4xx（域错误码 + details 行号），事务回滚 |
| PARTIAL_RECEIVED→RECEIVED_ALL | 收齐判定 | 同上 | — |
| RECEIVED_ALL / PARTIAL_RECEIVED→COMPLETED | 上架任务全部完成（或差额关闭填原因） | 状态+close 审批记录 | — |
| DRAFT/PENDING_APPROVAL/APPROVED→CANCELLED | 无任何收货（qty_received=0） | 状态+CANCEL 审批记录 | 有收货即拒绝：只能走关闭/反向冲正（§13.3） |

### 6.2 入库单 inbound_orders（§3.2；类型 M2 实际入口=采购入库/其他入库，退货/调拨入库由对应域单据承载，回写 §13）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| DRAFT→RECEIVING | 首次收货 | receipts 幂等落库 + 异常登记（经 §3.1 异常创建窄接口） | 重复幂等键 → 返回既有结果 |
| RECEIVING→AWAITING_QC | 收齐或差额关闭 | 状态推进 | — |
| AWAITING_QC→AWAITING_PUTAWAY | 全部明细质检完成或免检（免检直通） | 质检结果汇总 | — |
| AWAITING_PUTAWAY→COMPLETED | 全部上架任务完成 | 上架落账（§7 映射表）触发 | 任一行失败整体回滚 |
| DRAFT→CANCELLED | 无收货 | 状态 | — |
| RECEIVING→CLOSED | 差额关闭+原因 | 状态+审计 | — |

### 6.3 收货 receipts：**事件型，无状态机**（一次性生效、幂等键防重；状态语义由入库单承载，回写 §13 说明）。质检 quality_orders：PENDING→INSPECTING→COMPLETED（COMPLETED 即 §4.3 处理结果落定并触发 §7 库存映射）。上架任务 putaway_tasks：PENDING→IN_PROGRESS（**原子抢占** `UPDATE ... SET assignee=?, status='IN_PROGRESS' WHERE id=? AND status='PENDING'`，architecture §5.2，0 行=领取冲突）→COMPLETED（Putaway 落账）；→CANCELLED（入库单取消联动）。

### 6.4 销售订单 sales_orders（§6、§12.1）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| DRAFT→PENDING_APPROVAL | 客户启用、SKU 启用、数量/价格校验 | 状态+submit 记录 | 4xx 无副作用 |
| PENDING_APPROVAL→APPROVED | — | **审核即预占**：默认策略分配（FIFO/FEFO，inventory.AllocateFEFO/FIFO 纯函数命中批次，逐 bin 选行）→ 逐行 `Lock(ORDER_HOLD)`（source=sales:{so_no}）→ allocation_records 落分配理由 → 状态推进 | **任一行可用不足 → 整体回滚，订单停留 PENDING_APPROVAL**，返回 INVENTORY_NOT_ENOUGH（§6.2"预占失败则订单无法进入出库"） |
| PENDING_APPROVAL→REJECTED | — | 状态+REJECT 记录 | — |
| APPROVED→PARTIAL_SHIPPED / SHIPPED_ALL | 发货事件推进（§6.5） | 出库域联动 | — |
| PARTIAL_SHIPPED→COMPLETED | 差额关闭+原因（审计） | 状态（销售订单终态，与出库单 CLOSED 对应） | — |
| DRAFT/PENDING_APPROVAL→CANCELLED | — | 状态+审计 | — |
| APPROVED→CANCELLED | 未发货（qty_shipped=0） | **ReleaseLock 释放全部 ORDER_HOLD 锁（逐锁）+ 状态迁移，同事务**（inventory-rules §4.2 释放须业务动作触发） | 任一锁已核销（部分发货）→ 拒绝，走差额关闭 |

### 6.5 出库单 outbound_orders + 任务族（§7–§8）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| PENDING_ALLOCATE→ALLOCATED | — | 销售订单审核事务内联动创建并分配；**重新分配** = ReleaseLock(旧)+Lock(新)+allocation_records 替换（同事务） | 库存不足回滚 |
| ALLOCATED→PICKING | 拣货任务生成（整单一张或多行任务） | 任务落库 | — |
| PICKING→PICKED | 全部拣货任务 PICKED | 状态推进；缺货/少货上报 → 异常中心 + 该行任务转 EXCEPTION（可重新分配） | — |
| PICKED→CHECKED | 复核任务全部 DONE；复核异常（错货/少货/多货/批次错/序列号错 §8.3）→ 异常单+任务 EXCEPTION | 状态推进 | — |
| CHECKED→PACKED | 全部明细已入包裹（多包裹多对多 packing_items） | packing_records 落库（幂等键） | 重复提交 → 幂等返回 |
| PACKED→SHIPPED_ALL / PARTIAL_SHIPPED | 包裹就绪 | **发货确认 = architecture §4 标准事务**：shipments 落库（幂等键）→ 逐行 `Deduct`（携带 LockID 核销锁定，正式扣减）→ 序列号 SKU 逐件 `SerialEvent(OUTBOUND, bin=0)` → 状态推进 | 任一行失败整体回滚；幂等键重放返回既有结果 |
| PENDING_ALLOCATE..PACKED→CANCELLED | 未发货 | 释放全部锁+状态 | — |
| PARTIAL_SHIPPED→CLOSED | 差额关闭+原因（审计） | 状态 | — |

拣货/复核任务：PENDING→CLAIMED（原子抢占）→PICKED|DONE / EXCEPTION；打包/发货单见 §6.5 表内；shipments 自身物流态 PENDING→SHIPPED→IN_TRANSIT→SIGNED / ABNORMAL（§8.5，**库存动作仅发生在 PENDING→SHIPPED**；后续物流态为纯记录流转，M2 不接物流公司 API）。

### 6.6 调拨单 transfer_orders（§10.1 状态机逐一对应：DRAFT 草稿→PENDING_APPROVAL 待审核→APPROVED 待出库→TRANSFERRING 调拨中→AWAITING_RECEIPT 待入库→COMPLETED 已完成）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| DRAFT→PENDING_APPROVAL | 目标库位存在（BinChecker） | 状态+submit | 4xx |
| PENDING_APPROVAL→APPROVED（待出库） | — | 审批记录 + 源仓逐行 `Lock(ORDER_HOLD)`（source=transfer:{no}） | 源仓不足整体回滚 |
| APPROVED→TRANSFERRING（调拨中） | — | 逐行 `TransferOut`（§8.1：源仓 total/locked 同减 + TRANSFER_OUT 流水 + 核销锁）+ 序列号转在途（wh=0）→ qty_out 落行 | 整体回滚，单据留 APPROVED |
| TRANSFERRING→AWAITING_RECEIPT（待入库） | — | 目标仓**到货登记**（实物/差异记录，无库存动作）——与"调拨中"的区分：货已登记抵达目标仓、待收货确认落账 | — |
| AWAITING_RECEIPT→COMPLETED | — | 逐行 `TransferIn`（目标仓 total/available 同增 + TRANSFER_IN 流水）+ 序列号定位目标库位 → qty_in 落行 | 整体回滚（货仍计在途） |
| APPROVED 及之前→CANCELLED | 未出库 | ReleaseLock 全部+CANCEL 记录 | — |
| TRANSFERRING/AWAITING_RECEIPT→取消 | **禁止直接取消**（货已离源仓）——只能创建反向调拨单冲正（§13.3） | — | — |

两端流水齐备（§10.1 源减目标增）；在途 = TRANSFERRING 明细聚合（§5 000009 注）。

### 6.7 盘点单 count_orders（§10.2 硬性规则：差异必须走调整单+审批链路，禁止直接改库存）

| 迁移 | 守卫 | 事务内容 | 失败处理 |
|---|---|---|---|
| DRAFT→COUNTING | 范围非空 | 范围快照落 scope → 范围内库存行**按 id 升序**逐行 `Lock(COUNT_FREEZE)`（available→frozen）+ qty_system 快照入 count_items；**序列号 SKU 逐件 `SerialEvent(FROZEN)`**（inventory-rules §8.2"盘点必须逐个序列号操作"，与行冻结同事务） | 任一行/件失败整体回滚 |
| COUNTING→PENDING_REVIEW | 实盘登记完成（count_items 按 row+serial 幂等 **PUT 覆盖**；序列号 SKU 逐件一行 qty=1） | 系统对比生成 count_differences（行级汇总差异 + 序列号级差异明细） | — |
| PENDING_REVIEW→COMPLETED（差异审核通过） | — | **单事务逐差异**：先 `ReleaseLock`（释放该行全部盘点冻结，序列号件同步 `SerialEvent(IN_STOCK)` 回正常）→ `Adjust(盘盈/盘亏)`（经 §11.1 审批链：自动生成 inventory_adjustments 并直接 EXECUTED，adjustment_no 回写差异行）→ **序列号联动**：盘亏缺失件 `SerialEvent(OUTBOUND, source=调整单)` 台账核销；盘盈实物多出件逐件采集 `SerialEvent(IN_STOCK, source=调整单)` 建档 → 状态推进 | 整体回滚——解冻与调整原子，中间态不可见；序列号事件与库存原语同事务（service.go:1136-1137 契约），台账与库存行永不脱钩 |
| PENDING_REVIEW→CANCELLED（驳回） | — | 仅解冻（ReleaseLock + 序列号件 `SerialEvent(IN_STOCK)`），不调整，差异行 REJECTED | — |
| DRAFT/COUNTING→CANCELLED | 未出差异 | 解冻（COUNTING 时含序列号件回正常）+状态 | — |

### 6.8 库存调整 inventory_adjustments（§11.1/§12.1；状态值域已冻结于 000005 CHECK）

DRAFT→PENDING_APPROVAL（submit，reason 必填）→APPROVED（approve 记录）→EXECUTED（执行=`Adjust` 原语 + ADJUST 流水，调整单号即来源单据）；PENDING_APPROVAL→REJECTED；DRAFT/PENDING_APPROVAL→CANCELLED。**执行必须经审批**（M1 的"执行即落账"入口保留 Service 级但 M2 HTTP 面只暴露审批链，§9 角色映射配合）。

### 6.9 退货单 return_orders（§9）

| 类型 | 迁移链 | 库存动作 |
|---|---|---|
| 销售退货 | DRAFT→PENDING_APPROVAL→APPROVED→RECEIVING→IN_QC→COMPLETED（↔CANCELLED 在未收货前） | 收货：逐行 `Putaway(RequireInspect=true)`（入待检）+ 序列号 `RETURNED`；质检：经 §3.1 QCCreator 窄接口创建质检单，完成触发 `InspectResult`（合格→available，不合格→defective，§9.1 质检决定去向） |
| 采购退货 | DRAFT→PENDING_APPROVAL→APPROVED→SHIPPED→COMPLETED（↔CANCELLED 在未出库前） | 退货出库 = 单事务 `Lock(ORDER_HOLD)`→`Deduct`（business_type=PURCHASE_RETURN），**不建 outbound_order**——采购退货闭环在 returns 域内，追溯经流水 business_no，回写 §13 |

### 6.10 异常 exceptions（§11.2）

OPEN→ASSIGNED（分派）→PROCESSING→PENDING_REVIEW（待复核）→RESOLVED→CLOSED；处理记录追加式（jsonb 数组，不覆盖历史）。**异常冻结联动**：创建时可对定位到的库存行 `Lock(EXCEPTION_FREEZE)`（可选），RESOLVED/CLOSED 时必须 `ReleaseLock`（inventory-rules §4.2）。九类异常 type 由各域经 §3.1 的 ExceptionCreator 窄接口创建，异常中心 CRUD 归 returns 域。

## 7. 库存原语消费映射（每个业务动作 → 原语 / 锁定类型→状态列 / 幂等键）

锁定类型→状态列沿用 M1 冻结映射（internal/inventory/service.go:130-138 `lockTargetState`）：**ORDER_HOLD→locked；COUNT_FREEZE/QC_FREEZE/MANUAL_FREEZE/EXCEPTION_FREEZE→frozen**。M2 实际使用 ORDER_HOLD（销售/调拨/采购退货预占）、COUNT_FREEZE（盘点）、EXCEPTION_FREEZE（异常）；QC_FREEZE/MANUAL_FREEZE 值域保留、无业务入口（§10.2 不做清单）。

| 业务动作 | 原语（op） | 锁定/列变化 | 幂等键构成（冻结） |
|---|---|---|---|
| 销售订单审核预占 | Lock(LockOp) ×分配行 | ORDER_HOLD：available→locked | `{lock}:{so_no}:{line_no}:{bin_id}:{sku_id}:{batch_id}`；双保险：同 source+type 活跃锁幂等返回（service.go:510-531 既有机制） |
| 重新分配 | ReleaseLock + Lock | locked→available→locked | `relock:{so_no}:{line_no}:{seq}`（seq=分配轮次） |
| 订单/调拨/退货出库取消 | ReleaseLock ×n | locked→available | `release:{source_no}:{lock_id}` |
| 销售发货确认（正式扣减） | Deduct(DeductOp) ×行，携带 LockID | locked↓+total↓ | `ship:{outbound_no}:{line_no}:{bin}:{sku}:{batch}` |
| 上架（免检） | Putaway(PutawayOp{RequireInspect=false}) | total↑+available↑ | `putaway:{inbound_no}:{task_no}:{bin}:{sku}:{batch}` |
| 上架（经检/退货收货） | Putaway{RequireInspect=true} → InspectResult | total↑+pending_inspect↑ → pending_inspect→available/defective | `putaway:…` + `inspect:{qc_no}:{line_no}:{pass\|defect}` |
| 质检不合格转不良品仓 | Putaway{RequireInspect=true} + InspectResult(pass=false) 同事务组合 | 入待检再转 defective（两段流水保留追踪细节） | 如上两键 |
| 调拨出库确认 | TransferOut（§8.1 新原语） | locked↓+total↓（源仓），TRANSFER_OUT 流水 | `trout:{transfer_no}:{line_no}:{bin}:{sku}:{batch}` |
| 调拨到货确认 | TransferIn（§8.1 新原语） | total↑+available↑（目标仓），TRANSFER_IN 流水 | `trin:{transfer_no}:{line_no}:{bin}:{sku}:{batch}` |
| 采购退货出库 | Lock→Deduct | ORDER_HOLD→locked→total↓ | `prt:{return_no}:{line_no}:…` 两段 |
| 盘点冻结/解冻 | Lock(COUNT_FREEZE) / ReleaseLock | available→frozen / frozen→available；序列号 SKU 同事务逐件 `SerialEvent(FROZEN/IN_STOCK)`（§6.7） | `cfreeze:{count_no}:{row_id}` / `release:{count_no}:{lock_id}` |
| 盘点差异调整（盘盈/盘亏） | Adjust(AdjustOp)（解冻后同事务） | total±+available±，ADJUST 流水；序列号 SKU 同事务 `SerialEvent(OUTBOUND 核销缺失件 / IN_STOCK 建档多出件)` | `adjust:{adjustment_no}` |
| 库存调整执行 | Adjust（ExistingAdjustmentID 复用调整单行，§8.3） | 同上 | `adjust:{adjustment_no}` |
| 批次采集（收货） | EnsureBatch(BatchOp) | 无库存变化 | 唯一键 sku_id+batch_no 天然幂等（service.go:1066-1116） |
| 序列号事件（收货/上架/锁定/出库/在途/退货/盘点/调整） | SerialEvent(SerialOp) | 台账状态变化，不改数量；必须与对应库存原语同事务组合（service.go:1131-1137 契约） | serial_no 唯一 + last_source 指针；与同事务库存原语共享其幂等键前缀 |

键长度上限 128（`validateIdempotencyKey`，service.go:198-203），构成规则为"动作:单号:行:五维尾缀"确定性拼接——重试必然命中同键；HTTP 层 `Idempotency-Key` 头仅在覆盖单据表部分唯一索引（§5）与透传给原语（事件型单据收货/打包/发货）两处使用，不另造第二套键规则。

## 8. 跨仓调拨原语与 inventory 既有文件最小改动（工程师 O 独占 / MT0 一次性完成）

### 8.1 新增 `internal/inventory/transfer.go`（工程师 O 独占文件）

```go
// TransferOutOp：源仓出库（核销调拨预占锁）。语义 = Deduct 的 TRANSFER_OUT 口径：
type TransferOutOp struct {
    Key RowKey; Qty Qty; LockID int64            // 必填：APPROVED 阶段的预占锁
    Source Source; Actor Actor; IdempotencyKey, Remark string
}
func (s *Service) TransferOut(ctx, tx, op TransferOutOp) (MutationResult, error)
// 事务内容（复用包内既有 helper）：locateRowForUpdate → 校验 locked≥n →
// applyDelta(total=-n, locked=-n, guard locked≥n) → consumeLock →
// buildLedger(TRANSFER_OUT, status_from=locked, status_to=locked) → writeLedger → auditStock

// TransferInOp：目标仓入库（total、available 同增，行可创建）。
type TransferInOp struct {
    Key RowKey（needZoneShelf=true）; Qty Qty
    Source Source; Actor Actor; IdempotencyKey, Remark string
}
func (s *Service) TransferIn(ctx, tx, op TransferInOp) (MutationResult, error)
// ensureRow（checkSKUAndBin）→ applyDelta(total=+n, available=+n) →
// buildLedger(TRANSFER_IN, available, available) → writeLedger → auditStock
```

`TRANSFER_OUT/TRANSFER_IN` 已在 000005 change_type CHECK 值域内（db/migrations/000005:151）与 `changeTypes` 映射内（service.go:157-161），零迁移改动。不设 TransferOneStep 合并原语：出库与到货是两个物理时点、两个状态迁移（§6.6），强行合并会制造无中生有的中间态。

### 8.2 新增 `internal/inventory/adjustflow.go`（工程师 O 独占文件）

库存调整审批流（§6.8）：`SubmitAdjustment / ApproveAdjustment / RejectAdjustment / ExecuteAdjustment`——单据行 UPDATE（inventory_adjustments，状态守卫）+ document_approvals + 执行时调 `s.Adjust`（op 携带 ExistingAdjustmentID，见下）+ `middleware.Audit`。位于包内故可合法写 inventory_adjustments（守卫 grep 按路径豁免 internal/inventory），评审核对：只经包内既有 helper 与本文件自带的单据行 UPDATE，不触碰其他五表。

> **九方法清单补录（2026-10-04 清偿轮 F6 重验落位）**：M1 §8.2 冻结的九个库存变更原语为
> `Putaway / Deduct / Lock / ReleaseLock / MoveBin / InspectResult / Adjust / EnsureBatch / SerialEvent`
> （冻结原文与签名见 backend-m1-plan §8.2，backend-m2-plan §3 为其引用处）；本方案 §8.1
> 另新增 `TransferOut / TransferIn` 两个调拨原语——`internal/inventory.Service` 的库存变更
> 原语面随 M2 扩至 11 个（2026-10-04 实测 grep 导出方法一致）。各域 `StockGateway` 的消费
> 子集按 §3.1 表落位（internal/{purchase,sales,stockops,returns}/ports.go），子集之外方法
> 域包不可见。

### 8.3 MT0 对 `internal/inventory` 既有文件的一次性最小改动清单（集成工程师执行，此后冻结）

1. **buildLedger 补 serial_no**（M2 追溯接入点，ask 项 5）：`service.go:335-362` buildLedger 增加第 11 参 `serialNo string` 赋给 `InventoryLedger.SerialNo`（模型字段与 `insertLedger` 的 serial_no 列早已存在，repository.go:245-251，只缺构造赋值）；`PutawayOp/DeductOp/InspectResultOp` 增加可选 `SerialNo string` 字段（序列号 SKU 单件操作恒 qty=1），调用点约 7 处同步传参。批次操作传 ""，流水不变。
2. **writeLedger/writeAdjustment 切换 docnum**：`service.go:364-379` 与 `repository.go:361-384` 的 `newBusinessNo` 调用替换为 `docnum.Next`，删除 `newBusinessNo`（repository.go:44-52），M1 流水号缺口关闭（§4.1）。
3. **AdjustOp 增加可选 `ExistingAdjustmentID int64`**：>0 时不再 insertAdjustment 而按 ID 更新既有调整单为 EXECUTED（adjustflow 复用），≤0 行为与 M1 完全一致。
4. **类型别名承接 stock**（§3）：新增 `aliases.go`，原定义文件中的类型改为别名。
5. **只读查询小扩展**：`GET /api/inventory/locks`、`GET /api/inventory/adjustments`（路径对齐前端先行调用 web/src/api/inventory.ts:424/:426；adjustments 列表权限 stockops:adjustment:list）、`GET /api/inventory/trace`（returns 包实现、inventory 前缀挂载）、`POST /api/inventory/moves`（MoveBin HTTP 化，stockops 包注册）——均在本条一次交付。

除上述 5 条外，`internal/inventory` 既有文件 MT0 后冻结（§2.3 判据 1）。

## 9. 权限点清单与角色映射扩展（api.md §1 领域命名；seed 收编归集成工程师）

### 9.1 动作词扩展（冻结；M1 §5.4.1 基础六动作 + 以下作业/审批动作）

`submit 提交审核 ｜ approve 审核（通过/驳回共用资源点，操作语义由请求区分）｜ cancel 取消 ｜ close 关闭 ｜ execute 作业执行（收货/质检/上架/拣货/复核/打包/发货/调拨两端/盘点登记/差异调整）｜ claim 任务领取 ｜ assign 异常分派`

### 9.2 资源与权限点全量（域:资源:动作）

| 资源 | 权限点 |
|---|---|
| purchase:purchase | list、read、create、update、submit、approve、cancel、close |
| purchase:inbound | list、read、create、update、cancel、close |
| purchase:receipt | list、read、execute（收货确认） |
| purchase:putaway | list、read、claim、execute |
| purchase:quality | list、read、create、execute（质检结果提交） |
| sales:sales | list、read、create、update、submit、approve、cancel、close |
| sales:outbound | list、read、create、cancel、close |
| sales:allocation | list、read、create、execute（重新分配） |
| sales:pick | list、read、claim、execute |
| sales:check | list、read、claim、execute |
| sales:packing | list、read、execute |
| sales:shipment | list、read、execute |
| stockops:transfer | list、read、create、update、submit、approve、execute、cancel、close |
| stockops:count | list、read、create、execute（冻结/实盘登记）、approve（差异审核）、cancel、close |
| stockops:adjustment | list、read、create、approve、execute、cancel |
| stockops:move | list、execute（仓内移库 POST /api/inventory/moves） |
| inventory:lock | list（GET /api/inventory/locks 锁定记录查询；锁的创建/释放无独立权限点，随产生它的业务动作授权） |
| returns:salesreturn / returns:purchasereturn | list、read、create、update、submit、approve、execute、cancel、close |
| returns:exception | list、read、create、assign、execute、close |
| returns:trace | list |

清单外动作词禁止发明（§2.3 判据 9）；权限点常量由**集成工程师在 MT0 一次性追加**到 `internal/auth/permissions.go`（M2 段，§2.2 独占文件）——MT1–MT4 并行开始前全部 M2 常量已存在，各域 handler 只引用不定义，杜绝四人同改共享文件与 seed 双源漂移（常量与 §13 的 seed 种子同源，seed_test.go 字面清单同步核验）。

### 9.3 角色映射扩展（集成工程师在 `internal/database/seed.go` 收编：permResources/menuSeeds 追加 M2 资源与菜单节点、rolePermissionCodes 补 12 个角色映射；buildPermissionSeeds 机制不变，seed_test.go 字面清单同步）

| 角色（permission §1，seed.go:142-159 已种子化） | M2 映射 |
|---|---|
| super_admin / sys_admin | 全量（沿用） |
| purchaser | purchase:* 全部 + 相关只读（inbound/quality/inventory/trace list） |
| receiver / putaway_operator / inspector | 各自作业域 execute/claim + 列表读 + 异常创建 |
| salesperson | sales:sales 全部 + outbound/分配只读 |
| picker / checker / packer / shipper | 各自任务域 claim/execute + 列表读 + 异常创建 |
| stocktaker | stockops:count 全部 + stockops:adjustment:create/list |
| warehouse_manager | 四域全部列表读 + 各域 approve（审批权）+ 调拨/盘点全量 |
| warehouse_operator | M1 基础上追加四域 list/read + stockops:move:execute |
| viewer | 追加全部 M2 资源 list（保持只读口径，M1 S9 决策继续生效） |
| user | 仅菜单与各自经手任务（同 M1，不配业务映射） |

#### 9.3.1 MT5 收编记录（集成工程师，2026-10-03 实施落位）

权限点/菜单/角色映射已按 §9.2/§9.3 冻结清单收编进 `internal/auth/permissions.go`（M2 常量段，单一来源；四域包内同名常量改为 auth 别名，字符串值不变）与 `internal/database/seed.go`（permResources/menuSeeds/rolePermissionCodes），`seed_test.go` 字面清单同步核验。**权限点总数变化**：

| 项 | M1 | M2 新增 | 合计 |
|---|---|---|---|
| 资源 | 19 | 21 | 40 |
| MENU 菜单节点 | 24 | 25（采购/销售/库存作业/退货与异常四组，含 inventory:lock 叶子） | 49 |
| 动作点（API 71 + BUTTON 117） | 82 | 106 | 188 |
| 权限种子总行数 | 106 | 131 | 237 |

M2 菜单分组：menu:purchase（5 叶）、menu:sales（7 叶）、menu:stockops（4 叶 + inventory:lock）、menu:returns（4 叶）；叶子编码 = 资源编码（与 §5.4.1 既有口径同源）。动作词枚举按 §9.1 追加 submit/approve/cancel/close/execute/claim/assign 七值，未发明清单外动作词。

一致性核验（MT5 实测）：auth 权限常量 188 个与 buildPermissionSeeds 动作点编码逐一相等，种子额外 49 项恰为 MENU 节点——双源（常量/种子）无漂移。

角色映射落位口径（§9.3 表格的机械展开，seed.go `compileRoleGrant`）：

- 授权粒度 = 资源（full=冻结动作全集 / read=list+read / list=list / pick=显式子集），菜单可见性（叶子 + 顶级 + Dashboard）随资源自动带出——菜单与动作同源，不逐条罗列。
- "各自作业域"按 §2.2 交付面取全量：receiver=purchase:receipt、putaway_operator=purchase:putaway、inspector=purchase:quality、picker/checker/packer/shipper=sales:pick|check|packing|shipment；"异常创建"= returns:exception:create（最小授权，不含异常中心列表）。
- warehouse_manager："四域全部列表读"= §9.2 全部 21 资源 list+read；"各域 approve"= purchase:purchase / sales:sales / stockops:adjustment / returns:salesreturn / returns:purchasereturn 五点（调拨/盘点 approve 含于其全量）；"调拨/盘点全量"= transfer/count 资源全集；另含 stockops:move:execute 与 M1 库存查看基线（inventory:inventory/ledger list——调拨/盘点全量以库存可视性为前提，解读记录）。
- warehouse_operator：M1 字面清单（19 点）原样保留 + 21 资源 list/read + stockops:move:execute。
- viewer/user：既有分支逻辑不变，M2 资源随只读规则自动纳入/排除（S9 PII 口径继续生效）。

### 9.4 router 装配与 seed 收编（归集成工程师）

router 新增四组注册 + Option 注入（各域 StockGateway/Checker 等窄接口，§3/§3.1 机制）；`/api/inventory/trace` 由 returns 包提供 handler、路由组挂 inventory 前缀；swag 汇总；Makefile `ci` 追加 §2.3 判据 4/5 两条守卫命令（M1 计划 §8.6 已规划 ci 纳入守卫，现 Makefile ci 目标尚未含 grep，MT5 一并补齐）。

## 10. 事务、并发与守卫要点

1. **事务边界**：每个业务动作 = 单外层事务（architecture §4）：单据状态迁移（守卫 UPDATE）→ 库存原语（传 tx 组合，service.go:25-26 契约）→ document_approvals/异常联动 → `middleware.Audit` → COMMIT；任何步骤失败整体回滚。多行库存变更按行 id 升序（M1 §8.3）。
2. **任务领取**：统一 `UPDATE ... SET assignee=?, status='CLAIMED' WHERE id=? AND status='PENDING'`，0 行=409 领取冲突（architecture §5.2）；盘点实盘登记 PUT 幂等覆盖。
3. **盘点解冻+调整原子性**（§6.7）：ReleaseLock 与 Adjust 同事务，行锁串行化保证无"已解冻未调整"可见窗口；差异为 0 的行只解冻不调整。
4. **CI 守卫扩展**（`make ci`）：状态守卫 grep（`UPDATE` + `SET status` 无 `WHERE` 同串命中人工复核清单）+ `newBusinessNo` 残留检查 + M1 库存族守卫沿用。
5. **数据权限**：四域列表/详情按 `auth.WarehouseScope` 过滤。落列口径（§5 迁移已按此定稿）：主单据直接携带 warehouse_id（purchase_orders 收货仓、inbound/receipt/quality/transfer 双端/count/return）；作业类从表（pick_tasks/check_tasks/packing_records/shipments）创建时自主出库单**冗余 warehouse_id 落列**，列表过滤免 join、且可独立建 (warehouse_id, status) 索引（database.md §6.1）；例外源数据（allocation_records.inventory_ledgers 追溯）经 business_no 查询不走列表权限面。SELF/SELF_IN_CHARGE 范围在 M2 落地行级过滤（经手任务按 assignee/created_by，M1 §7.4 的遗留承诺）。

## 11. 测试策略（testing §2/§3/§4 M2 子集）

1. 状态机单测：每单据类合法迁移全通过、非法迁移 409（testing §2）。
2. 集成链路（**机制注记：`//go:build integration` + SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控外部 PG/Redis，testcontainers 为备选未落地**）：采购入库（含批次/效期/序列号三开关分支、部分收货、超量收货拒绝）、销售出库（预占→分配→拣→复→包→发→扣减、部分发货、取消释放）、调拨（两端流水+在途聚合+在途取消冲正）、盘点（冻结→实盘→差异→审批→调整→解冻）、退货（销售退货入正常/不良、采购退货出库）、质检（九类处理结果的库存映射）（testing §3）。
3. 并发：并发收货不超量（§2.3）；两路并发审核同一订单/调拨单，恰一路预占成功；任务并发领取恰一成功；盘点冻结期间并发销售预占被 available 守卫拒绝（testing §4）。
4. 幂等：幂等键重放（收货/打包/发货/调整执行）库存与单据只变化一次。
5. 恒等式与流水一一对应：沿用 M1 §8.7 口径断言，覆盖新 change_type 组合（TRANSFER_OUT/TRANSFER_IN 已有，重点组合场景）。
6. 权限：新增权限点越权 403、数据权限过滤、审批操作审计断言（testing §8）。

## 12. M2 明确不做（阶段 14+ 平台能力与其他，均附原因）

| 不做 | 原因 |
|---|---|
| 采购申请（business-flow §2.1 流程首环节） | api.md §1 无对应路由资源、前端无页面，requirements §6 场景 1 自"创建采购订单"起步；M2 以采购订单为流程源头，申请单随真实需求出现再立项（回写 §13 注记） |
| Excel 导入导出、异步任务 asynq、协程池 ants、sonic（阶段 14） | 平台能力，依赖文件中心与任务队列运维面；M2 单据域无导入导出需求（M1 §9 决策延续） |
| 打印中心/条码生成（阶段 15） | 场景 1/2 的"打印入库单/发货单"属 M3 打印中心；M2 单据数据结构已为模板预留（单号/明细完整落库） |
| 扫码 /api/scanner、设备/箱码/托盘、PDA（阶段 16，devices D1–D20） | 依赖业务场景接口就绪（M2 交付后才具备接入条件）；M2 作业接口按"扫码友好"设计（逐件序列号、原子抢占）但不实现扫码解析 |
| 报表中心 /api/reports 聚合（阶段 17） | 前端先行页已声明"目录数据待后端交付"；M2 只交付追溯查询（阶段 13 范围），报表聚合依赖流水积累与汇总表 |
| 智能能力：完整库位推荐、补货建议、预测、积压识别（阶段 18） | inventory-rules §11 属增强模块且"建议不自动改变库存"；M2 上架推荐仅基础规则（同 SKU 集中+剩余容量+库位启用，§5.3 的简化交付，回写说明分级） |
| 通知中心、定时任务框架（robfig/cron）、效期/库存预警扫描（阶段 14+/18/19） | 审批待办以列表页呈现；预警依赖定时任务基础设施，M2 不引入 cron |
| 文件上传/图片接口（质检图片、异常拍照） | 文件中心随阶段 14（M1 §9 同因）；M2 落 image_refs jsonb 占位列，不提供上传校验 |
| system_configs/dictionaries 表、编号规则运行时配置 | 需求未出现（§4.1 预留规则结构体），避免提前引入配置面 |
| /api/logs 审计日志查询中心、备份恢复、系统监控（阶段 19） | 审计已落库可查（SQL 级），查询中心属平台阶段 |
| 批量/波次/分区拣货（§8.2 其余三模式）、统一任务中心聚合（/api/workbench、/api/tasks） | 单订单拣货已覆盖场景 2 且是 DoD 路径；多模式与任务聚合属作业效率优化，依赖波次引擎，M3+；前端先行契约列入对齐清单 |
| MANUAL_FREEZE/QC_FREEZE 业务入口、物流公司 API 对接 | 无业务流来源（质检冻结场景由 pending_inspect 列承载）；物流状态机仅人工流转记录 |
| git 内多域并行以外的 DevOps 全量（生产编排完善、性能优化） | 阶段 20–22 |

## 13. 文档回写清单（文档先行——**业务语义与路由契约类随 MT0 schema 冻结提交**，实现早于文档的窗口期为零；纯记录性回写留 MT5）

**MT0 批次（与 000006–000010 迁移同 commit，changelog 同步记录）：**

1. `api.md §1`：补录退货组（/api/returns、/api/purchase-returns）、/api/inventory/adjustments、/api/inventory/locks、/api/inventory/moves、/api/inventory/trace；§7 补幂等键构成规则。前端先行契约与本冻结路由的偏差（sales.ts `/api/sales-orders`、purchase.ts `/api/purchases/receipts`、outbound.ts `/api/outbound/picking-tasks` 等，见 §15.2）由前端对齐轮消化，api.md §1 不回退。
2. `business-flow.md`：§2.1 注（M2 以采购订单为流程源头，采购申请不做）、§3.1 注（调拨/退货入库由对应域单据承载，不重复建入库单）、§5.3 注（推荐库位分级：M2 基础规则/阶段 18 完整算法）、§9.2 注（采购退货不建出库单）、§10.1 注（状态命名对应：APPROVED=待出库、AWAITING_RECEIPT=待入库，六态逐一保留）、§13.1 注（编号引擎计数表实现与前缀补录，含 LED/ADJ 存量承接）、§13.2 注（状态变化记录经 operation_logs 快照，不建独立历史表）。
3. `inventory-rules.md §2`：在途=调拨单聚合口径注记；§8.2 注（盘点序列号逐件口径：count_items serial 级明细 + SerialEvent 联动，§6.7）。
4. `database.md §2`：defective_inventory 不建表注记、doc_number_counters/document_approvals 补录。

**MT5 批次（纯记录性）：**

5. `docs/changelog.md`、`docs/tasks/current.md`：M2 交付与状态记录。

## 14. 任务拆分与并行安排（每 Task 一个 commit，Conventional Commits 中文实践）

| Task | Scope | 内容 | 依赖 | 验证 |
|---|---|---|---|---|
| MT0 | I | internal/stock 契约包 + inventory 别名/serial_no/docnum/AdjustOp 扩展/查询扩展（§8.3 清单）+ docnum 包 + **internal/auth/permissions.go M2 权限点常量一次性追加** + 000006–000010 迁移 + grants/seed-demo 扩充 + 守卫扩展 + **§13 业务语义/路由契约回写（api.md/business-flow/inventory-rules/database.md）** → **schema 冻结点** | — | build/vet/test 全绿；migrate up/down 双向；守卫 grep 零命中 |
| MT1 | P | purchase 全量 | MT0 | §11.2 采购链路测试；swag |
| MT2 | S | sales 全量 | MT0 | §11.2 销售链路 + 并发预占测试 |
| MT3 | O | stockops + transfer.go/adjustflow.go | MT0（transfer.go 接口冻结于 MT0 评审） | §11.2 调拨/盘点/调整测试 + 并发领取 |
| MT4 | R | returns 全量 | MT0（QCCreator/SalesOrderReader 契约冻结于 MT0 评审） | §11.2 退货/异常/追溯测试 |
| MT5 | I | router 装配 + seed 收编（权限点种子/菜单/角色映射——常量已在 MT0 就位）+ §13 纯记录性回写（changelog/current.md）+ swag 汇总 | MT1–MT4 | 全接口冒烟；越权 403 |
| MT6 | 全体 | §11 全量测试跑通 + requirements 场景 1–4、7 后端走查（打印/导出步骤除外，属 M3）+ changelog + tasks/current.md | MT5 | development-plan §5 M2 验收 |

并行图：`MT0 → { MT1 ∥ MT2 ∥ MT3 ∥ MT4 } → MT5 → MT6`。跨域窄接口契约（§3.1）与库存原语映射（§7）在 MT0 评审会一次性冻结（签名+错误码），四线并行期无等待。

## 15. 风险与开放问题

1. **编号机制拍板**：ask 字面"PG sequence"与本方案"计数表按日重置"的偏离需确认（§4.1）；引擎接口两种实现均可承载，返工成本低但需在 MT0 前定。
2. **前端先行契约偏差**：前端 8 个 api 模块的路径与 api.md §1 存在已核实偏差（sales.ts `/api/sales-orders`、purchase.ts `/api/purchases/receipts`、outbound.ts `/api/outbound/picking-tasks` 等，api.md §1 对应 `/api/sales`、`/api/receipts`、`/api/picks`）；**两类处理**：库存中心 5 端点直接采纳前端先行路径为冻结路由（/api/inventory/locks、/api/inventory/adjustments，web/src/api/inventory.ts:424/:426，§8.3 条 5），其余偏差以回写后 api.md §1 为准、由前端对齐轮消化，不阻塞后端验收（M1 §13.5 同口径）；另 /api/inventory/summary、/api/inventory/alerts（Dashboard 聚合）M2 不做（§12 报表口径），前端错误态为预期行为。
3. **审批人自审**：business-flow §12 未禁止提交人=审批人；若需强制分离须改规范后再加校验，M2 不擅自增加。
4. **盘点解冻+调整原子窗口**：依赖行锁串行化，极端高并发下冻结行的事务等待时间上升；测试覆盖，必要时拆分差异执行为分批事务（保持每差异原子）。
5. **在途口径**：在途不入 inventory 恒等式（§5 000009 注），若后续要求"在途进库存表"须改 CHECK 与恒等式文档，属重大变更需先改 inventory-rules。
6. **docnum 计数表热点**：同一前缀同一天单行计数，发放串行；单号发放微秒级、且在业务事务内，实测无瓶颈则不优化（已有风险记录，不预设缓存方案）。
