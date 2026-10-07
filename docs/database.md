# StockFlow 数据库设计规范

> 版本：v1.0 ｜ 数据库围绕业务模型设计，不围绕页面设计 ｜ 关联文档：[inventory-rules](inventory-rules.md)、[deployment](deployment.md)

---

## 1. 设计总则

1. 数据库围绕**业务模型**设计，不围绕页面设计。
2. 所有上线数据库变更必须走 **Migration**（迁移，工具基线 golang-migrate，见 architecture.md §11.2），禁止手工 `CREATE TABLE xxx`；迁移必须支持升级与回滚/兼容。
3. 生产环境第一次启动执行安全初始化；**禁止每次启动重置业务数据**。

---

## 2. 核心实体清单

具体表结构根据项目架构调整，但至少覆盖以下实体：

```text
—— 权限与组织 ——
users, roles, permissions, departments

—— 仓库空间 ——
warehouses, zones, shelves, bins

—— 商品 ——
products, skus, product_categories, units, barcodes

—— 往来单位 ——
suppliers, customers

—— 采购 ——
purchase_orders, purchase_order_items

—— 入库 ——
inbound_orders, inbound_items, receipts, putaway_tasks

—— 销售 ——
sales_orders, sales_order_items

—— 出库 ——
outbound_orders, outbound_items, allocation_records
pick_tasks, check_tasks, packing_records, shipments

—— 库存核心 ——
inventory, inventory_locks, inventory_ledgers, inventory_adjustments

—— 批次与序列号 ——
batches, serial_numbers

—— 调拨 ——
transfer_orders, transfer_items

—— 盘点 ——
count_orders, count_items, count_differences

—— 质量 ——
quality_orders, quality_items, defective_inventory

—— 异常 ——
exceptions

—— 共享单据平台 ——
doc_number_counters, document_approvals

—— 平台支撑 ——
notifications, attachments
print_templates, print_tasks, export_tasks, import_tasks
operation_logs, login_logs
system_configs, dictionaries, scheduled_jobs

—— 扫码与容器（条码设备接入） ——
devices（终端设备：PDA/Pad/PC 工作站，含注册激活/绑定仓库/扫码配置）, scan_logs（扫码日志）
boxes（箱码）, box_items（箱码-商品绑定）
pallets（托盘）, pallet_items（托盘-箱/SKU 绑定）

—— 设备管理（多终端，见 devices.md §6–7） ——
device_logs（设备日志）, device_configs（配置下发）, app_versions（App 版本/升级）

—— 用户态（2026-10-06 效率层一期，见下方注） ——
user_saved_views（用户保存视图：page_key/name/filters_json/sort_json/columns_json/page_size/is_default）
user_preferences（用户偏好 kv：pref_key/pref_value jsonb）

—— 幂等（2026-10-06 效率层一期集成收口追加，见下方注） ——
idempotency_keys（端点级幂等键：key/user_id/endpoint/request_hash/response_snapshot jsonb/status）
```

> **注（M2 落地口径，backend-m2-plan §13 / 迁移 000006–000010）**：
> - `defective_inventory` **不建表**——不良品量以 inventory 行的 defective_qty 状态列承载（六状态同表，恒等式见 inventory-rules §2），质检处理结果经 inventory 流水追溯；
> - `doc_number_counters`（单号计数表：PK(prefix, period)，docnum 引擎发放）与 `document_approvals`（审批记录 append-only，仅 INSERT 权限）随 000006 共享单据表交付，已补录实体清单（见上"共享单据平台"组）。

> **注（2026-10-06 作业效率提升层一期，迁移 000020–000023 + 000024 集成收口追加，计划 docs/plans/2026-10-06-efficiency-layer-phase1.md）**：
> - `user_saved_views`（000020）：用户隔离由应用层强制 `user_id=当前用户`；唯一索引 `uk_user_saved_views_name (user_id, page_key, name)` + 部分唯一索引 `uk_user_saved_views_default (user_id, page_key) WHERE is_default`（每页至多一个默认视图，服务层「设为默认」仍在单事务内清除旧默认）；`page_key` CHECK `^[a-z0-9._-]{1,64}$`、`page_size` CHECK 1–100；jsonb 三列应用层各限 ≤8KB；无跨域 FK（000011 起冻结口径）。
> - `user_preferences`（000021）：复合主键 (user_id, pref_key) 即全量索引、无冗余索引；`pref_key` CHECK `^[a-z0-9_.]{1,64}$`；无 created_by（个人高频态，不挂审计——api.md §9 同日节口径）。
> - **幂等键不建新表**（2026-10-06 集成收口起部分废止，见下条）：沿用 `inventory_ledgers.idempotency_key` 唯一索引兜底 + receipts/packing/shipments 既有幂等键列；新增「行级幂等键 × Idempotency-Key 头合成规则」（api.md §7）。
> - **idempotency_keys（000024，集成收口追加——实现批 ask 升级覆盖原「通用幂等结果缓存表不做」裁决）**：端点级「执行权仲裁 + 响应快照回放」，与上述行级唯一索引两层正交（api.md §7）。唯一索引 `uk_idempotency_keys (key, user_id, endpoint)` 即并发仲裁真相源（INSERT ... ON CONFLICT 恰一方占用）+ `idx_idempotency_keys_created_at`（孤儿 PROCESSING 行清理作业支撑，sysops 定时清理挂账）；`key` CHECK `^[A-Za-z0-9._:-]{1,64}$`、`status` CHECK IN ('PROCESSING','COMPLETED')；无 deleted_at（幂等行无软删语义，清理为物理删除）；中间件挂载端点清单与灰度口径见 api.md §7/§9 同日集成收口披露。
> - **最近活动零新表**：最近操作读 `operation_logs` 尾 N 条（写入仍仅 middleware.Audit 既有链路）；最近访问属导航态，存 `user_preferences.pref_value`（key=recent_visits，服务端裁剪至 20 条），不建新审计表。
> - **复用既有表扩展**：000023 为 `putaway_tasks`/`pick_tasks`/`check_tasks` 各加 `priority smallint NOT NULL DEFAULT 0`（CHECK 0–9）+ 部分索引 `idx_*_tasks_next (status, priority DESC, created_at) WHERE 活动态`（inbound_orders/exceptions 不扩列——/api/tasks/next 两分支无 priority 排序层，api.md §9 同日节披露）；000022 为全局搜索建 pg_trgm GIN 索引批（见 §6 补录）。000020–000023 均已落盘（000022/000023 随 B2/B3 波次交付），000024 随集成收口追加（以 db/migrations 实际为准）。

---

## 3. 通用字段规范

业务表统一包含：

```text
id
created_at
updated_at
created_by
updated_by
deleted_at
```

需要状态追踪的数据增加 `status` 字段，并遵循 business-flow.md §13 的状态机规范。

**注意**：不要所有表都强行使用相同字段，应结合业务实际设计。例如：

- 库存表（inventory）还需要 `total / available / locked / frozen / pending_inspect / defective` 等状态数量字段。
- 单据表需要各业务环节时间字段（审核时间、收货时间、拣货时间等，见 business-flow.md §13.4）。
- 纯日志类表（operation_logs）不需要 updated_by/deleted_at。

---

## 4. 多仓模型

底层数据库从第一版开始支持多仓库：

```text
库存维度：warehouse_id + zone_id + shelf_id + bin_id（+ batch_id + serial_id）
```

禁止把库存设计成 `sku_id + quantity`。

组织与数据权限依赖：用户-仓库、用户-部门关系表支撑数据权限（见 permission.md §4）。

---

## 5. 软删除与业务对象生命周期

### 5.1 软删除对象

涉及历史业务的实体使用软删除（deleted_at）：

```text
商品、SKU、供应商、客户、仓库、库位、用户
```

### 5.2 停用/归档/作废

已有业务数据的对象不允许物理删除，使用：

```text
停用、归档、作废
```

优先保证历史数据完整性。已影响库存的单据不能删除（business-flow.md §13.3）。

---

## 6. 索引与性能约束

1. 所有列表查询条件字段（单号、状态、时间、仓库、SKU、创建人）建立索引。
   - **000015 补录（2026-10-04 清偿项 F15）**：M1–M3 各单据主表（purchase_orders/inbound_orders/quality_orders/sales_orders/outbound_orders/transfer_orders/count_orders/inventory_adjustments）与 inventory_ledgers 的 `created_by` 筛选面缺失，已由迁移 000015 以 `(created_by, created_at)` 复合索引补齐（M3 新表 000011/000012 既有同形索引）。
2. 库存表、库存流水表是最高频读写的表，索引设计优先评审。
   - **000015 收紧（2026-10-04 清偿项 F12）**：inventory_ledgers.zone_id/shelf_id 由可空收紧为 NOT NULL（写入侧 insertLedger 恒填，与 inventory 主表 000005 对齐；PostgreSQL SET NOT NULL 全表扫描遇 NULL 即失败回滚，生产首启前执行安全）。
3. 流水表只增不改，按时间分区或归档策略在设计中预留。
4. 禁止无条件 `SELECT *`，禁止在循环中查询数据库（见 architecture.md §7）。
5. **效率层一期补录（2026-10-06，迁移 000020–000023 + 000024 集成收口，计划 docs/plans/2026-10-06-efficiency-layer-phase1.md）**：
   - 000022：全局搜索中缀匹配 pg_trgm GIN 索引批（`idx_*_trgm`，覆盖商品/SKU 名称与编码、条码、批次号、序列号、库位编码、仓库名称、往来单位名称、七类单据号与物流单号共 17 列，清单以迁移文件为准；skus 表无 name 列——名称在 products.name，sku 搜索匹配 skus.code + products.name，不建 idx_skus_name_trgm，见 api.md §9 同日交付披露）；pg_trgm 扩展需安装权限，生产由超级用户执行迁移（deployment.md grants 流程），目标库拒绝扩展时回退=去索引保 ILIKE（功能等价、性能降级）。
   - 000023：三任务表部分索引 `idx_putaway_tasks_next` / `idx_pick_tasks_next` / `idx_check_tasks_next`（`(status, priority DESC, created_at)` WHERE 活动态）支撑 `/api/tasks/next` 真实 SQL 排序。
   - 000020/000021：user_saved_views 两条唯一索引（含 is_default 部分唯一）与 user_preferences 复合主键即全量索引，见 §2 效率层注记。
   - 000024（集成收口追加）：idempotency_keys 唯一索引 `uk_idempotency_keys (key, user_id, endpoint)`（并发占用仲裁真相源）+ `idx_idempotency_keys_created_at`（孤儿行清理支撑——2026-10-07 起改为幂等键惰性回收，见 idempotency/doc.go），见 §2 效率层注记；真库 up/down 往返验证已随集成收口完成（本地一次性库 up→24→down 全级→0 表→再 up 通过，验证后即删库；000020–000023 同法随实现波次验证）。
6. **安全性能加固补录（2026-10-07，渗透检查修复轮，计划 docs/plans/2026-10-07-security-perf-hardening-round2.md）**：
   - 000025：报表/流水检索索引批（inventory_ledgers 等大表按 reports 域实际查询形态补索引，清单以迁移文件为准）。
   - 000026：operation_logs 检索 pg_trgm GIN 索引（username/ip，比照 000022 口径；pg_trgm 扩展沿用 000022，IF NOT EXISTS 幂等），支撑系统管理-操作日志 ILIKE '%kw%' 检索避免审计大表顺序扫描。

---

## 7. 审计数据保护（不可破坏）

以下数据属于审计数据，普通管理员（包括系统管理员）不可直接修改：

```text
库存流水（inventory_ledgers）
审批记录
关键操作日志（operation_logs）
登录日志（login_logs）
```

目标：**可查询、可追溯、不可随意篡改**。

实现要求：

1. 应用层不提供这些表的 UPDATE/DELETE 接口。
2. 数据库账号权限分层：业务运行账号无审计表的写权限（INSERT 除外）、无 UPDATE/DELETE 权限。
3. 审计数据保留策略由定时任务统一执行（日志清理），人工不可触发删除。

---

## 8. 初始化数据与演示数据分离

### 8.1 初始化数据（生产安全初始化，首次启动执行）

```text
默认管理员、默认角色、默认权限、默认菜单
默认字典、默认仓库示例
```

要求：

- 仅在系统首次启动（空库）时执行。
- 默认管理员必须强制修改初始密码（强密码策略）。
- 幂等设计：重复启动不重复插入、不覆盖已有数据。

### 8.2 演示数据（仅开发环境）

```text
商品、SKU、仓库、库位、供应商、客户
采购订单、入库单、库存、销售订单、出库单、调拨、盘点
```

**硬性要求**：演示数据与生产初始化逻辑完全分离（独立的 seed 命令/环境开关），生产环境不可能注入演示数据。

---

## 9. 数据库变更流程

```text
需求 → 修改 Migration 脚本 → 本地验证（升/降级）→ Code Review
→ 测试环境执行 → 生产执行（低峰期，先备份，见 deployment.md §4）
```

任何 Migration 必须在本地完成升级 + 回滚验证后才能提交。
