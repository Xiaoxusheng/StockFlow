# 演示数据补全轮二（2026-10-07）：页面级空数据点收尾

## 背景

用户要求「整个系统加上测试数据，还没有数据的都加上」。真库盘点（stockflow，2026-10-07）显示主数据与单据状态分布已由 §0–§17 覆盖（6 仓 / 40 SKU / 各单据全状态命中），但仍有 4 个**页面级空数据点**：

| # | 空缺点 | 真库证据 | 受影响页面 |
|---|---|---|---|
| 1 | 效期批次为零 | batches: expired=0、30 天内=0（效期 SKU 最早到期 2026-11-10，窗口外） | 库存预警（临期/过期）、工作台「临期库存」组、Dashboard 预警卡 |
| 2 | 待复核队列为空 | check_tasks 全部 DONE | 复核中心 `?status=PENDING`、工作台「待复核订单」组、任务中心 |
| 3 | 超储预警零候选 | Σ(仓,SKU) 存量均 < max_stock | 库存预警 `level=overstock` |
| 4 | 流水类型缺口 | inventory_ledgers 仅 INBOUND/OUTBOUND/INSPECT_PASS | 流水页按类型筛选（TRANSFER_*/MOVE/ADJUST/LOCK/RELEASE 全空）、流水统计图 |

## 做什么（§18 新段，四件）

1. **效期批次 6 条**（挂批次+效期双开且非序列号的 SKU——序列号管理 SKU 灌普通库存会破坏一物一行守卫，过期档由 D005-01 改用 D008-01；到期日用 `CURRENT_DATE ± n` 表达式——灌数时点相对计算，演示数据不随时间腐烂）：
   - SKU-E010-01：+5 天（临期近档）、+29 天（30 天窗口边缘）
   - SKU-E003-01：+12 天、+45 天（窗口外=正常批次对照）
   - SKU-D004-01：+25 天；SKU-D008-01：−3 天（已过期）
   - 配套 inventory 行 6 条（新五维键，复用已有库位码；total/available 两列，§8 期初口径）+ 期初流水 6 行 1:1 成对（`DEV-SEED-OPEN-*`）
2. **待复核任务**：check_tasks PENDING 1 条挂唯一 PICKED 出库单 OUT-20261006-000004（WH-E03，行 1，SKU-E013-01 qty 12，与 §11 DONE 任务同形状）；计数器 `('CH','20261007',2)` GREATEST 推进。
3. **超储预警**：inventory 新行 WH-D02/S-01-22/SKU-D001-02（三关全关，避开序列号管理的 D006-01）qty 520 > max_stock 500（仓+SKU 合计 530 触发 overstock），配期初流水。
4. **演示流水段（净零对）**：TRANSFER_OUT/TRANSFER_IN（±30，WH-E01↔WH-E02，业务号回指既有调拨单）、MOVE（±10，WH-E01 REEL-01-11→REEL-02-12）、ADJUST（±2 盘盈亏）、LOCK/RELEASE（±5）——Σqty_change=0，不扰动现存量锚点（沿 §10 补影红线）。

## 不做什么

- **不写 system_configs**：`TestDevSeedNoProductionInitPollution` 硬性禁止（生产初始化表，internal/sysops 专职）；系统参数页空态属既定边界（ensureSystemConfigKeys 现无调用方，功能侧走代码缺省值不受影响）。
- **不写审计表**（operation_logs/login_logs，既有硬性约束）；**不做 UPDATE/DELETE**（INSERT-only 门禁；11 条 NULL 效期批次挂非效期 SKU 属合理形状，不补改）。
- 不新增仓库/商品/SKU 主数据（已齐）；不动 §0–§17 既有行；不跑迁移。

## 修改文件

- `db/seed/dev_seed.sql`：文尾追加 §18（18.1 效期批次+库存+期初流水；18.2 待复核任务+计数器；18.3 超储行并入 18.1；18.4 净零演示流水）
- `internal/database/dev_seed_test.go`：冻结契约同步——counts map（batches 2→3、inventory 3→4、inventory_ledgers 5→7、check_tasks 2→3、doc_number_counters 2→3）；成对测试（期初库存 INSERT 2→3、期初流水 3→4、补影 2→3）
- `db/seed/dev_seed_verify.sql`：追加 §18 对应只读断言（效期三档有行、PENDING 任务在、超储触发、净零段存在）

## 验证

1. `go test ./internal/database/ -run TestDevSeed -count=1`（静态契约全绿）
2. `SF_ENV=dev make seed-demo`（等价 psql 灌库，单事务幂等）+ `make seed-demo-verify`（只读自检）
3. API 实测：`/api/inventory/alerts?level=near_expiry|expired|overstock` total>0；`/api/workbench/priorities` near_expiry_stock.count>0、pending_checks.count>0；`/api/checks?status=PENDING` 有行
4. 浏览器实测：库存预警页三档、复核中心待处理队列、工作台优先处理四组

## 风险

- 灌库与运行期取号撞号：新单号走 20261007 新日期段 + 计数器 GREATEST 推进（§11.9 先例）；batches/inventory 用空闲 9xxx 段显式主键（现库 max：batch 9617 / inventory 9848 / ledger 9906）
- 效期批次挂靠必须 is_expiry_managed=true SKU，否则预警口径不认（repository_alerts 按批次效期判定，SKU 开关仅业务语义）——已核对 5 个效期 SKU
