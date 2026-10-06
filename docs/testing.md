# StockFlow 测试规范

> 版本：v1.0 ｜ 关联文档：[inventory-rules](inventory-rules.md)、[business-flow](business-flow.md)、[excel.md](excel.md)、[printing.md](printing.md)

---

## 1. 测试总要求

- 库存、订单、状态机是测试重点，测试不通过不得声称功能完成。
- 每个模块交付时同步交付对应测试（数据库/后端/前端/权限/校验/错误处理/日志/测试，见 development-plan.md）。

---

## 2. 单元测试

重点覆盖：

```text
库存（状态计算、恒等式、锁定/释放）
订单（数量约束、金额计算）
状态机（合法迁移、非法迁移拒绝）
价格（精度、舍入）
数量（精度、正负、边界）
权限（权限点校验、数据权限过滤）
并发（原子扣减）
幂等（重复请求）
```

---

## 3. 集成测试

覆盖完整业务链路：

```text
采购 → 入库 → 库存
销售 → 出库 → 库存
调拨
盘点
退货
质检
```

每条链路验证：单据状态流转正确、库存变化与流水一致、审计链完整。

**运行机制（2026-10-04 披露，清偿项 F16）**：本仓库集成测试不使用 testcontainers-go——
文件带 `//go:build integration` 构建标签，默认 `go test ./...` 不编译（单元测试零外部依赖
约束）；运行需先准备外部 PostgreSQL 15 + Redis 7 实例并设置环境变量
`SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
SF_TEST_PG_NAME / SF_TEST_REDIS_ADDR`（任一未设置即 `t.Skip` 跳过），再执行
`go test -tags integration ./...`。机制与 backend-m2-plan §1/§8.2、backend-m3-plan §1/§14
的「testcontainers」措辞偏差已在其对应行加注披露（实际 = 环境变量门控外部实例，
testcontainers 为备选方案未落地，go.mod 无该依赖）。§8「越权访问他人仓库数据 → 数据
权限过滤生效」专项已由 internal/auth/integration_datascope_test.go 集成链路覆盖
（2026-10-04 清偿轮 F7）。

---

## 4. 核心库存专项测试（必须专门覆盖）

```text
正常入库        部分入库        重复入库        重复请求
库存不足        并发出库        库存冻结        库存解冻
盘盈            盘亏            调拨            退货
过期商品        批次商品        序列号商品
```

并发测试要求：

- 模拟多用户同时出库同一 SKU，验证不超卖、不产生负库存（inventory-rules.md §9）。
- 模拟任务并发领取，验证只有一个领取成功。
- 模拟重复提交，验证幂等（库存只变化一次）。

一致性测试要求：

- 任意操作后校验恒等式：`总库存 = 可用 + 锁定 + 冻结 + 待检 + 不良品`（inventory-rules.md §2）。
- 每笔库存变化与库存流水一一对应。

---

## 5. Excel 测试

```text
正常导入        错误导入        重复导入        大文件导入
部分成功        全部失败        错误文件导出    大数据导出
异步导出
```

验证点：

- 错误行定位准确（第 N 行 + 原因）。
- 部分成功时成功/失败数量与明细正确。
- 大文件导入不超时、不撑爆内存。
- 异步导出进度、完成、下载、过期清理链路可用。

---

## 6. 打印测试

必须验证：

```text
A4、A5、热敏纸、标签纸
条码、二维码（扫码枪可识别）
中文字体
分页、长表格、多页
批量打印、打印预览
```

---

## 7. 异常恢复测试

真实生产系统不能因为一个错误就把状态搞乱。

必须测试：

```text
事务回滚：扣库存成功但后续步骤失败 → 整体回滚，状态一致（architecture.md §4）
中断恢复：Excel 已生成但下载失败 → 用户可重新下载
任务失败：定时任务失败 → 重试 + 告警，不产生重复副作用
进程重启：服务重启后单据/库存/任务状态完整（一切状态落库）
```

---

## 8. 权限与安全测试

```text
无权限 API 直接调用 → 403 且不泄漏数据
越权访问他人仓库数据 → 数据权限过滤生效
登录连续失败 → 锁定生效
Token 过期/伪造 → 拒绝访问
文件上传恶意类型/超大文件 → 拒绝
审计数据修改接口 → 不存在（不可篡改）
```

---

## 9. 扫码枪专项测试

完整验收标准见 scanner.md §11，自动化/测试重点：

```text
HID 输入识别：快速连续输入+Enter 识别为扫码；人工逐字输入不误判
码制解析：Code128/Code39/EAN-13/EAN-8/UPC/QR/DataMatrix 均可识别
对象解析：SKU/库位/单据/任务/批次/SN/箱码/托盘码 映射正确
防重复：极短时间重复事件去重，不产生重复业务（scanner.md §6.6）
连续计数：同 SKU 连续扫码数量 1→2→…→N 正确累计
校验拦截：扫错商品/库位/单据、库存不足、SN 已出库 均立即阻止
风险分级：高风险操作（发货/库存调整）不被自动确认
手工兜底：扫码枪断开可手工输入，且手工输入同样通过业务校验
设备日志：扫码日志记录设备/操作员/原始条码/解析对象/结果
```

---

## 10. 设备与真实终端测试

完整清单见 devices.md §17。

**不能只使用浏览器开发者工具模拟手机尺寸**，必须覆盖三种真实形态：

```text
PC + USB HID 扫码枪
Android Pad + Bluetooth HID 扫码枪
工业 Android PDA + 内置扫描头（StockFlow Scan）
```

验证点：扫码、连续扫码、实体 Scan 键、声音、震动、网络断开/恢复、登录、切换用户（共享设备）、任务、库存、盘点、打印、异常。

设备专项：

```text
设备注册/激活二维码/绑定仓库/解绑/停用/远程注销
配置下发后 Scan 端自动同步
离线断网：最终库存操作不置成功，恢复后冲突明确提示
App 版本检查与升级链路（预留架构）
设备监控：在线/电量/最后扫码/最近错误/最后同步
```

---

## 11. 验收级测试（对应 requirements.md 场景）

9 个验收场景（采购入库、销售出库、调拨、盘点、Excel、打印、追溯、扫码枪现场验收、工业 PDA 现场验收）作为端到端验收用例，发布前全部人工/自动化走通。

---

## 12. 作业效率提升层一期专项测试（2026-10-06）

> 依据 [docs/plans/2026-10-06-efficiency-layer-phase1.md](plans/2026-10-06-efficiency-layer-phase1.md) §7 收编。
> 涉及新包 internal/search、internal/userpref 及 reports/sales/purchase/printing/datax/stockops 增量；
> 后端测试零外部依赖（fakedb/fakeRepo 项目约定，先例 internal/purchase/fakedb_test.go），集成链路仍按 §3 构建标签机制执行。

### 12.1 后端单元测试矩阵

| # | 用例 | 断言要点 |
|---|---|---|
| T1 | 搜索权限过滤 | 无 masterdata:sku:list 的用户查询→结果不含 sku 分组；有码则含且 items 形状齐（GET /api/search 端点只挂认证，组内按用户权限集逐 type 过滤；超管直通语义与 RequirePermission 同构） |
| T2 | 搜索数据权限 | warehouse_id 越用户 scope→该分组空结果；scope 交集生效（不 403，防范围探测） |
| T3 | 搜索短路 | q 长度 <2 零 SQL 执行（repo 调用计数=0）；limit 缺省 5、上限 20 |
| T4 | 视图按用户隔离 | 用户 A 建视图，B GET/PUT/DELETE→404（非本人防探测）；A 的列表不见 B 的行（全部查询强制 user_id=当前用户） |
| T5 | 视图默认唯一 | 设新默认后旧默认自动清除；并发设默认最终恰一个（部分唯一索引兜底） |
| T6 | 偏好读写 | key 白名单外→400 invalidParam；GET ?keys= 过滤生效；单值 >16KB→400；recent_visits 服务端裁剪至 20 条 |
| T7 | next 排序与候选池 | 四层排序（本人进行中 > priority > 超时 > created_at）逐层断言（超时阈值注入）；候选池含未领取 PENDING（照抄 /api/tasks 原 assignee 硬过滤会恒 has_next=false，必须 (本人进行中) OR (PENDING 未领取)）；checking 分支无超时层；receipt/exception 分支仅 created_at 排序；current_task_id 被排除；无候选 has_next=false；白名单外 task_type→400 |
| T8 | 批量结果计数 | PENDING→success；本人已领→skipped；他人已领/状态非法→failed(既有冲突码)；混合批次 total/success_count/failed_count/skipped_count 与 results 逐条对账；批量不整体回滚 |
| T9 | 并发双确认 | 同一任务两 goroutine 并发 claim（fake repo 原子 UPDATE 抢占）恰一 success 一 failed；对齐 TestConcurrentLockNoOversell 模式 |
| T10 | priority 守卫 | 完成/取消态设优先级→409；值域外→400；审计写入 |
| T11 | 打印批量结果 | 不可打印 SKU→failed(reason=PRINT_SKU_DISABLED)；100→96/3/1 计数形状断言；全成功不返回 409（旧 409+disabled_ids 整体拒绝语义已废止） |
| T12 | Excel 失败行往返 | retry-failed 仅收源任务 INVALID/FAILED 行建新导入任务；重跑不再产生重复单据（fake writer 计数=失败行数）；源任务行状态不被篡改 |
| T13 | 幂等重复提交 | 同 Idempotency-Key 二次提交返回首次结果且库存/流水/单据不变（行级键=头键前缀合成规则，api.md §7；锚定 internal/inventory/integration_test.go 既有幂等/并发用例保持绿） |
| T14 | 路由冻结端点集 | search/userpref/next/recent-ops/batch-claim×3/priority×3/retry-failed 全部进入各包 routes_test 冻结清单（漏注册即测试红） |
| T15 | 迁移双向 | 000020–000023 本地 up + down 往返验证（database.md 迁移硬要求；无 PG 环境时如实挂账，不入「已验证」） |

### 12.2 前端测试策略（零依赖最小设施 + 人工验收矩阵，2026-10-06 测试加固更新）

web 基线无测试框架（引入 vitest 已有裁决先例不做，**不新增测试框架、不加 npm 依赖**维持不变）；
2026-10-06 起新增**零依赖最小自动化设施**：Node 22 内置 `node:test` + `--experimental-strip-types`
（`cd web && npm test`，glob `src/**/*.test.ts`，无需安装任何包）。被测对象仅限纯逻辑抽取层——
`src/components/shortcut/shortcutMatcher.ts`（输入态冲突矩阵/combo 匹配/G 系 chord 分发规划）、
`src/components/table/viewPayload.ts`（保存视图序列化/恢复）、`src/components/batch/batchRetry.ts`
（批量失败项筛选）；组件/页面交互与视觉仍走下方人工验收矩阵。
一期门禁 = `npm test` 全绿 + `npm run typecheck`（tsc -b）+ `npm run lint` + `npm run build`。

**人工验收矩阵**（交付时逐项执行并记录结果）：

```text
8 个验收场景（效率层计划 §9）：Ctrl+K 输入 SKU001 三分组直达 / 保存视图登出重登还原 /
收货任务 001 完成进 002 / 批量打印 100→96/3/1 仅重试失败 3 张 / Excel 980/20 只重导 20 行 /
连点确认收货 10 次仅一次库存变化+一条流水 / SKU 详情关联业务一屏七项可达 / 进工作台即知该干什么
快捷键全表走查：§32 键位逐键验证（含 G 系 chord 800ms 序列窗口、? 帮助面板、Ctrl+R 刷新）
快捷键与输入框冲突：焦点在输入框/文本域/下拉/可编辑区时页面级快捷键全部禁用；
  扫码输入框（[data-sf-scan-input]）内击键不触发快捷键；Esc 不双抢 antd Modal/Drawer
权限口径：G 系跳转无权限时提示不跳转；全局搜索无权限分组不出现；保存视图仅本人可见可改
状态与主题：Light/Dark 两主题 × 1440/1024/768 三宽度；Loading/Empty/Error 状态真实接口形态
动效核验：reduced-motion 下新增动效（搜索结果/批量结果/任务切换/反馈）归零
```

> 注：矩阵中「快捷键与输入框冲突」「保存视图还原」「批量打印仅重试失败」的**纯逻辑判定面**
> 已由 `npm test` 自动化（见上文零依赖最小设施）；矩阵保留用于真实 DOM 交互/视觉/权限全量走查。
