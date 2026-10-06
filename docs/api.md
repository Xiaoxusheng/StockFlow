# StockFlow API 设计规范

> 版本：v1.0 ｜ 后端接口统一规范 ｜ 关联文档：[architecture](architecture.md)、[permission](permission.md)

---

## 1. API 领域划分

API 按业务领域划分：

```text
—— 认证与权限 ——
/api/auth          登录/登出/刷新/会话
/api/users         用户
/api/roles         角色
/api/permissions   权限
/api/departments   部门（CRUD + 启停，/api/departments/{id}/status；已注册并被部门页消费）

—— 基础资料 ——
/api/products             商品
/api/skus                 SKU
/api/product-categories   商品分类（CRUD + 启停；无删除，停用即生命周期终点）
/api/units                计量单位（CRUD + 启停；无删除，停用即生命周期终点）

—— 仓库空间 ——
/api/warehouses    仓库
/api/warehouses/workload  仓库作业量（单量/行量双指标，reports 实现、warehouses 前缀
                          挂载；权限挂 inventory:inventory:list——仓库分析页既有
                          端点同码，2026-10-05 分析卡片轮）
/api/zones         库区
/api/shelves       货架
/api/bins          库位

—— 往来单位 ——
/api/suppliers     供应商
/api/customers     客户

—— 采购与入库 ——
/api/purchases     采购
/api/purchases/analytics/trend  采购订单金额趋势（reports 实现、purchases 前缀挂载；
                                权限挂 purchase:purchase:list，2026-10-05 分析卡片轮）
/api/purchases/supplier-rank    供应商采购排行（reports 实现、挂载与权限口径同上）
/api/purchases/status-composition 采购单状态构成（reports 实现、挂载与权限口径同上）
/api/inbounds      入库
/api/inbounds/status-composition  入库单状态构成（reports 实现、inbounds 前缀挂载；
                                  权限挂 reports:report:read——与所在入库分析页
                                  既有趋势端点同码，2026-10-05 分析卡片轮）
/api/inbounds/supplier-rank       供应商入库排行（reports 实现、挂载与权限口径同上）
/api/receipts      收货
/api/putaway       上架

—— 销售与出库 ——
/api/sales         销售
/api/sales/analytics/trend      销售订单金额趋势（reports 实现、sales 前缀挂载；
                                权限挂 sales:sales:list，2026-10-05 分析卡片轮；
                                订单金额口径，非流水估值）
/api/sales/product-rank         商品销售排行（reports 实现、挂载与权限口径同上）
/api/sales/status-composition   销售单状态构成（reports 实现、挂载与权限口径同上）
/api/outbounds     出库
/api/outbounds/completion-rate   出库订单完成率（reports 实现、outbounds 前缀挂载；
                                 权限挂 reports:report:read——出库分析页既有趋势
                                 端点同码，2026-10-05 分析卡片轮）
/api/outbounds/product-rank      商品出库排行（reports 实现、挂载与权限口径同上）
/api/allocations   库存分配
/api/picks         拣货
/api/checks        复核
/api/packing       打包
/api/shipments     发货

—— 库存 ——
/api/inventory           实时库存
/api/inventory/summary        库存汇总七计数（reports 实现、inventory 前缀挂载；
                              权限挂 inventory:inventory:list——消费方为库存页汇总条
                              与 Dashboard，2026-10-04 裁决，reports:report:read 易致
                              仅持报表列表权限的角色恒 403）
/api/inventory/alerts         库存预警（reports 实现、inventory 前缀挂载；权限口径同上）
/api/inventory/analytics      库存分析（reports 实现、inventory 前缀挂载；权限同上，
                              见 §9 2026-10-05 联调轮）
/api/inventory/sku-top        SKU 库存 TOP N（reports 实现、inventory 前缀挂载；权限
                              同上，2026-10-05 分析卡片轮）
/api/inventory/turnover-trend 库存周转趋势（reports 实现、inventory 前缀挂载；权限
                              同上，2026-10-05 分析卡片轮）
/api/reports/flow-trend       出入库流水趋势（免分页日粒度序列，报表域端点，
                              2026-10-05 分析卡片轮——销 report-flowstats-trend-cap）
/api/inventory-ledgers   库存流水
/api/batches             批次
/api/serials             序列号
/api/inventory/locks          锁定记录列表（M2，backend-m2-plan §8.3 条 5）
/api/inventory/adjustments    调整单列表/审批/执行（M2）
/api/inventory/moves          仓内移库（M2，POST 提交）
/api/inventory/trace          库存追溯（M2：流水+台账+单据链聚合）

—— 调拨/盘点/质量/异常 ——
/api/transfers     调拨
/api/counts        盘点
/api/quality       质检
/api/exceptions    异常中心

—— 退货 ——
/api/returns            销售退货（创建/审批/收货/质检/完成）
/api/purchase-returns   采购退货（创建/审批/出库/完成）

—— 报表 ——
/api/reports       报表

—— 数据中心 ——
/api/imports            导入（含 /api/imports/templates/{type} 模板下载、
                        /api/imports/{id}/error-file 错误明细）
/api/imports/{id}/retry-failed  导入失败行重试（以源任务 INVALID/FAILED 行创建新导入
                        任务，走正常校验+确认流程；2026-10-06 效率层一期，见 §9 同日节）
/api/exports            导出
/api/files              文件中心（上传/下载/预览/列表/删除；异常图片等附件挂接的数据源）
/api/data-tasks         数据任务详情/任务卡刷新（GET /api/data-tasks/{kind}/{id}，
                        kind=IMPORT/EXPORT）
/api/prints             打印（历史列表支持 template_id 筛选，见 §9 2026-10-05
                        二维码闭环段；打印域全量端点契约补账另行挂账）

—— 扫码 ——
/api/scanner       扫码解析 /api/scanner/resolve、扫码设备管理、扫码日志
                   （resolve 管线第 0 段支持 SFQR 商品二维码，见 §9 与 qr-code.md）

—— 设备（多终端） ——
/api/devices       设备注册/激活/绑定/配置下发/心跳/健康监控/App 版本（devices.md §6–7）

—— 平台 ——
/api/workbench/summary  工作台四块入口计数（reports 实现、inventory:inventory:list——
                        Dashboard/tasks 同信息面先例；2026-10-05 平台批）
/api/tasks              我的任务列表（上架/拣货/复核 UNION 分页明细化，reports 实现、
                        inventory:inventory:list 同上；assignee 恒=当前用户；
                        2026-10-05 平台批）
/api/tasks/next         自动下一条候选任务（reports 实现、inventory:inventory:list 同上；
                        候选池=(本人进行中)∪(PENDING 未领取)；2026-10-06 效率层一期，
                        契约见 §9 同日节）
/api/workbench/recent-operations 最近操作（本人 operation_logs 尾 N 条，reports 实现、
                        权限同上；2026-10-06 效率层一期）
/api/search         全局业务搜索（Ctrl/Cmd+K 命令面板数据源；internal/search 实现、
                        仅认证+逐 type 权限过滤；2026-10-06 效率层一期）
/api/user/views     保存视图 CRUD（internal/userpref 实现、认证即可无权限点；同上）
/api/user/preferences 用户偏好 GET/PUT（key 服务端白名单；同上）
/api/notifications 通知
/api/logs          日志
/api/system        系统配置/监控/定时任务
```

---

## 2. 统一标准

以下内容全系统统一，不得各模块自行其是：

```text
请求格式（JSON）
响应格式（统一信封）
错误格式（见 architecture.md §2）
分页格式
时间格式（YYYY-MM-DD HH:mm:ss）
ID 类型
认证方式（Bearer Token）
```

### 2.1 分页格式

统一为：

```text
请求：page、pageSize（+ 各自筛选参数）
响应：page、pageSize、total、items
```

或按项目现有标准统一后全站一致。

### 2.2 响应信封

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "xxx"
}
```

错误时 `code` 为业务错误码，`message` 为用户可读信息（见 architecture.md §2）。

---

## 3. 接口文档（Swagger / OpenAPI）

提供 Swagger / OpenAPI 文档（后端通过 swaggo/swag 注释生成，工具基线见 architecture.md §11.2）。所有接口必须包含：

```text
描述
请求参数（含类型、必填、示例）
响应结构
错误码
鉴权要求
```

接口文档随代码同步更新，不允许文档与实现脱节。

> **生成机制（2026-10-04 T6 收口，清偿项 F8）**：各域 handler 的 doc 注释携带最小集注解
> （`@Summary/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Router`，统一信封引用
> `internal/response.Envelope` 文档形态定义），经 `make swag`（go run 固定版本 CLI）汇总
> 生成 `apidocs/`（docs.go + swagger.json/yaml）。路由清单以 gin 实际注册为准（临时装配
> 引擎 `r.Routes()` 交叉核对，不编造不存在的路由）；api.md 本文件仍是接口契约的人工真
> 相来源，swagger 输出为机器可读的派生物。

---

## 4. 数据校验

后端必须进行完整校验，**禁止只靠前端 required**。

所有关键参数必须由后端验证：

```text
格式（类型、长度、正则）
范围（数量 > 0、金额 >= 0、日期先后关系）
业务关系（SKU 是否存在、仓库/库位是否存在、供应商是否存在）
状态（单据当前状态是否允许该操作——状态机守卫）
权限（当前用户是否有权操作该数据——含数据权限）
库存（可用库存是否充足）
```

校验失败返回明确错误码与 `details`（如具体哪一行、哪个字段错误）。

---

## 5. 文件上传安全

上传类型：图片、Excel、PDF、附件。

必须校验：

```text
扩展名（白名单）
MIME 类型（白名单，与扩展名交叉校验）
文件大小（上限）
文件名（重命名，禁止使用用户原始文件名作为存储路径）
存储路径（服务端生成，禁止拼接用户输入，防路径穿越）
```

文件元信息统一写入文件中心（见 excel.md §7）。

---

## 6. 鉴权与权限接入

1. 除 `/health`、`/ready`、登录接口外，所有 API 必须认证。
2. API 级权限校验：接口声明所需权限点，中间件统一校验（不只靠前端隐藏按钮，见 permission.md §2）。
3. 数据权限在 Service 层过滤（仓库/部门/本人，见 permission.md §4）。
4. 敏感操作（导出、删除、审批）额外记录操作日志。

---

## 7. 幂等与并发接口约定

- 收货、入库、出库、库存调整、调拨、盘点、发货等确认类接口必须支持幂等（architecture.md §3）。
- 任务领取类接口必须原子抢占，失败返回明确冲突信息（architecture.md §5）。
- 扫码场景的幂等：`/api/scanner/resolve` 只做对象识别；业务执行必须走各领域业务 API，扫码重复事件去重规则见 scanner.md §6.6。
- **幂等键构成规则**（M2 冻结，backend-m2-plan §7；实现落 `inventory_ledgers.idempotency_key` 唯一索引兜底 + `Idempotency-Key` 请求头优先）：

```text
构成通式：{动作前缀}:{单据号}:{行号/任务号}:{定位维度…}[:子动作]
定位维度按动作涉及的操作对象取（bin/sku/batch 顺序固定），缺省维度省略；
HTTP 显式幂等键（Idempotency-Key 头）优先于服务端确定性构成。

putaway:{inbound_no}:{putaway_no}:{bin}:{sku}:{batch}        上架入库（收货单本身以
                                                             receipts.idempotency_key
                                                             唯一索引兜底，头优先）
putaway:{return_no}:{line}:{zone}:{shelf}:{bin}:{sku}:{batch}:{qty}   退货收货入库
lock:{so_no}:{line}:{seq} / relock:{so_no}:{line}:{seq}      销售分配预占 / 重新分配
release:{outbound_no}:{lock_id}                              预占释放（取消/关闭/重分配）
shortpick:{outbound_no}:{pick_no}:{lock_id}                  短拣差额释放
ship:{outbound_no}:{line}:{bin}:{sku}:{batch}                发货正式扣减
trout:{transfer_no}:{line}:…:{qty} / trin:{transfer_no}:…    调拨出库 / 入库（两段）
cfreeze:{count_no}:{inventory_row_id}                        盘点范围冻结
adjust:{count_no}:{diff_line}                                盘点差异调整
prt:{return_no}:{line}:{zone}:{shelf}:{bin}:{sku}:{batch}:lock / :deduct[:sn:{serial}]
                                                             采购退货预占/扣减（序列号逐件追加）
inspect:{qc_no}:{line_no}:{pass|defect}:{bin}:{sku}:{batch}  质检结果应用
efreeze:{exception_no}:{bin}:{sku}:{batch}                   异常冻结
release:{exception_no}:{lock_id}                             异常解冻
```

- 请求显式幂等键（`Idempotency-Key` 头）优先；未提供时按上表构成在服务端确定性生成——同一单据同一动作重试必然命中重放（返回既有结果，不再扣减），部分行重放视为矛盾请求整体回滚。
- **端点级幂等仲裁（2026-10-06 效率层一期集成收口新增，迁移 000024 `idempotency_keys` 表）**：高风险端点经 `internal/idempotency` 组级路由感知中间件挂载——首次请求占用执行权（INSERT ... ON CONFLICT 恰一方，数据库唯一索引 `uk_idempotency_keys(key,user_id,endpoint)` 为仲裁真相源）并真实执行，响应（含失败信封）落快照；同键同用户同端点重复提交回放首次响应（HTTP 状态与体一致，信封体 `request_id` 改写为当前请求）；并发同键余者 409 `IDEMPOTENCY_IN_PROGRESS`；同键换载荷（请求体 SHA-256 不符）409 `IDEMPOTENCY_REQUEST_MISMATCH`；键值域 `[A-Za-z0-9._:-]{1,64}`（400 `IDEMPOTENCY_KEY_INVALID`），panic 中断自动释放占用（同键重试重新执行）。挂载端点（key=`method + gin 路由模式`，组级白名单见 `internal/router/idempotency_mount.go`，漂移由启动核验 fail-fast）：POST /api/receipts、POST /api/putaway/:id/execute、POST /api/transfers/:id/approve|outbound|arrive|receive、PUT /api/picks/:id/confirm、PUT /api/checks/:id/confirm、POST /api/packing、POST /api/shipments、POST /api/counts/:id/complete、POST /api/putaway/batch-claim、POST /api/picks/batch-claim、POST /api/checks/batch-claim、POST /api/prints/tasks、POST /api/imports/:id/retry-failed。一期全表灰度（无键放行不缓存），前端提交点接 `useIdempotentMutation` 附键后即获防护，逐端点升级 Required（缺键 400 `IDEMPOTENCY_KEY_REQUIRED`）属后续批次。与本节行级幂等两层正交并存：挂载端点同时消费头键的（收货/打包/发货/stockops 流水路径），头键既进行级合成又进端点快照，语义兼容；重复提交优先命中端点快照回放（不再触达业务层），无键重复提交仍由行级唯一索引兜底。
- **行级幂等键 × 头键合成规则（2026-10-06 效率层一期冻结）**：stockops 产生库存流水的写路径（调拨 approve/execute、移库、盘点完成产生的调整）中，`Idempotency-Key` 头**不得直接**灌给每行原语——同请求 N 行同键会撞 `uk_inventory_ledgers_idempotency_key` 唯一索引、批量中断。合成规则：头键存在且非空 → 行级键 = `{头键}:{原行级通式}`（新命名空间：同头键重试=同行键，命中部分唯一索引即原语重放既有结果，不重复扣加库存/流水）；头键缺省 → 行级键与上表存量通式形态完全一致（零行为变化）。头键 ≤32 字符、超长 400 invalidParam（落笔前核验拼接结果 ≤ inventory_ledgers.idempotency_key 列宽 varchar(128)，超限下调头键上限）。适用边界：仅产生 ledger 流水的写路径引入头键透传；单据创建类端点（POST /transfers、POST /counts）一期不引入（无单据级幂等键存储列）。服务端实际消费头键的端点以各域契约为准（收货、打包/发货既有，stockops 流水路径随本规则新增）；对不消费头键的提交点（上架 execute/拣货确认/复核/批量领取/盘点登记），前端附头仅得按钮防双击，服务端防重按既有派生键通式或状态机原子抢占执行。

---

## 8. 健康检查接口

```text
/health  存活检查
/ready   就绪检查
```

`/ready` 检查依赖：服务、数据库、缓存、文件系统。供负载均衡与部署探针使用（见 deployment.md §3）。

---

## 9. 契约变更与补齐记录

### 2026-10-05 二维码闭环轮（SFQR 商品二维码——契约增量，协议唯一契约见 qr-code.md）

> 本轮为文档先行契约增量：打印任务链、模板、条码渲染等端点全部**复用既有端点**，零新增路由（swag 路由表不变）；仅以下行为与校验增量。

筛选与错误码增量（打印域）：

```text
GET /api/prints/history   新增 template_id 筛选（正整数，可选；与既有 keyword/
                          object_type/result 并列，历史列表只加参数不加端点）。
POST /api/prints/tasks    新增错误码 PRINT_SKU_DISABLED（409，打印对象中存在已停用
                          SKU；details.disabled_ids 逐条列出停用 SKU 编码）——创建
                          装配时整体拒绝，前端可「仅打印可用」降级重提。既有
                          PRINT_TOO_MANY_DATA_IDS（data_ids ≤500）口径不变。
```

行为变更说明：SKU_LABEL 装配开始拒绝停用 SKU——「打印中心手输 ID 打停用 SKU」路径从成功变为 409（依据：商品已停用为不可打印原因，qr-code.md §9）。

字段与形态增量（打印域）：

```text
打印任务行（GET /api/prints/tasks/{id} 的 rows[].* 与 /api/prints/history 行）
     新增 data_id（string，可选 omitempty）——任务创建时持久化的行业务身份快照
     （SKU 数字 ID 十进制文本），历史重打取数唯一依据；存量行无该值为空，
     凡缺 data_id 的行一律不可自动重打（fail-closed，qr-code.md §7.4）。
```

扫码解析（resolve）SFQR 分支：

```text
POST /api/scanner/resolve  管线第 0 段（优先级最高，先于单号前缀与条码匹配器）：
     HasPrefix("SFQR|") 即锁型，载荷解析失败显式返回新错误码且不落回——
     SFQR_INVALID（400，段数/载荷空/超 64 字符）、
     SFQR_VERSION_UNSUPPORTED（400，version ≠ 1）、
     SFQR_TYPE_UNSUPPORTED（400，BIN/BOX/PALLET 预留未实现及未知类型）；
     载荷合法但 SKU 不存在/软删/停用 → 既有 SKU_NOT_FOUND（404）。
     命中响应形态复用 {type:"sku",id,code,name}；scan_logs 照常落库（错误也落）。
     格式 BNF、黄金向量表逐字冻结于 qr-code.md §4。
```

入参校验增量（基础资料域）：

```text
POST/PUT /api/skus 的 barcodes 入参：新增校验——条码值含 "SFQR|" 前缀 → 400
     invalidParam（"条码保留 SFQR 协议前缀，禁止注册（qr-code.md 命名空间）"）。
     封死条码注册侧写入协议保留字造成一码两义的通路；存量数据不清洗。
```

### 2026-10-04 后端补齐轮（前端先行契约立项 + 一致性修复）

新增端点（均已注册并挂权限点，swag 注解同步）：

```text
POST /api/putaway/{id}/pause     上架任务暂停（IN_PROGRESS→PAUSED，仅领取人/超管；
                                 权限 purchase:putaway:execute——作业执行构成动作，
                                 不发明 plan §9.1 清单外动作词；状态值域经迁移
                                 000017 扩充 PAUSED，PAUSED 视为活动态参与入库单
                                 推进/关闭守卫）
POST /api/putaway/{id}/resume    上架任务恢复（PAUSED→IN_PROGRESS，权限同上）
GET  /api/quality/trace          质量追溯（检验→处置记录链，quality_items 行粒度；
                                 权限 purchase:quality:list；筛选 sku_code/batch_no/
                                 biz_no 等值 + 分页。serial_no 传值显式 400——
                                 质检链未记录序列号）
GET  /api/quality/nonconforming  不合格品记录（已完成质检且行不良>0；筛选 keyword/
                                 disposition 六值英文键。destination 传值显式 400——
                                 质检单未记录处置去向）
POST /api/exceptions/{id}/images 异常图片挂接（business-flow §11.2；两段式：先
                                 POST /api/files 上传取得文件 ID，再提交
                                 {file_ids:[...]}——服务端校验存在/未过期/image/*，
                                 追加 image_refs（去重、累计 ≤20）+ 处理记录 + 审计
                                 同事务；OPEN..PENDING_REVIEW 可挂接，RESOLVED/CLOSED
                                 拒绝；权限 returns:exception:execute）
```

行为变更：

```text
POST /api/purchases/{id}/cancel、POST /api/inbounds/{id}/cancel
     请求体新增可选 {reason}（空体兼容）——取消原因落取消审批记录 opinion 与审计
     快照，不再静默丢弃（对齐 /api/sales/{id}/cancel 的 CancelInput 先例）。
PUT  /api/products/{id}
     category_id / unit_id 三态语义：缺省（null）不修改 / 0 显式清空 / >0 换绑
     （对齐 /api/product-categories/{id} 的 parent_id 0 哨兵先例；前端 allowClear
     显式清空不再失效）。
DELETE /api/suppliers/{id}、DELETE /api/customers/{id}
     补"已产生业务记录不可删"引用校验（business-flow §1.4/§1.5）：供应商存在任何
     采购单、客户存在任何销售单（含已取消，软删单除外）即 409 拒绝，应使用停用。
GET  /api/users
     列表项补 role_ids（批量装配）——用户列表「角色」列不再恒空。
PUT  /api/users/{id}
     data_scope=SPECIFIED_WAREHOUSE 的"至少绑定一仓"守卫改为按变更后生效值判定
     （与创建口径对称）：未传 warehouse_ids 仅改 data_scope 而现有绑定集为空、或
     传入空集清空绑定，均 400 拒绝。
```

字段与形态统一：

```text
数据中心（/api/imports、/api/exports、/api/files、/api/data-tasks）响应时间字段
     统一为 api.md §2 的 YYYY-MM-DD HH:mm:ss 文本（JSONTime）：file_expired_at /
     started_at / finished_at / expires_at / created_at 此前 RFC3339 与格式化串
     混用，已全部收敛；空值序列化为 null。
/api/notifications 个人收件箱行 id 统一字符串形态（database.ID；此前裸 int64 为
     JSON number，违反全仓"业务 ID 字符串"约定）。
/api/inventory 导出模块（POST /api/exports，module=INVENTORY）筛选键补齐
     zone_id / shelf_id / batch_id（与实时库存页导出按钮 scopeParams 一致，消除
     "所见非所得"；此前按"未知键忽略"静默丢弃）。
DELETE /api/auth/sessions/{id} 的 {id} 为会话 SID（字符串形态），swagger 注解已
     由 int 修正为 string（仅文档修正，行为不变）。
```

### 2026-10-05 联调轮（真联调 Smoke 发现的断裂修复 + Dashboard/分析域端点补齐）

新增端点（前端先行契约兑现——web/src/api/dashboard.ts、web/src/api/inventory.ts
InventoryAnalytics 立项挂账项，internal/reports/dashboard.go 实现；swag 注解同步）：

```text
GET /api/reports/dashboard/today           Dashboard 第一层今日指标（camelCase 与前端
                                           DashboardTodayMetrics 契约逐字段回对）
GET /api/reports/dashboard/trend           第二层趋势（range=7d/30d/90d/custom，
                                           custom 必带 time_from/time_to；行形
                                           {date,inbound,outbound,stockQty}）
GET /api/reports/dashboard/tasks           第三层任务概览（type/label/count/link）
GET /api/reports/dashboard/alerts          第三层预警条目（id/type/level/sku_code/
                                           product_name/message/created_at，取前 8）
GET /api/reports/dashboard/warehouse-stock 第四层仓库分布（warehouse_code/名称/
                                           sku_count/total_qty/bin_utilization 0~100）
GET /api/inventory/analytics               库存分析（total_stock_value/total_qty/
                                           total_sku_count/turnover_rate/turnover_days/
                                           abc[]/trend[]；days 默认 30 上限 366；
                                           ABC 80/15/5 分档由后端计算——前端不得
                                           自行分档的既有契约）
```

以上六端点均为 reports 域实现、挂 `inventory:inventory:list`（与 /api/inventory/
summary|alerts 的"数据域读权限承载 reports 实现"裁决同口径——Dashboard 页无独立
权限码、全员可见）。日末库存趋势采用"现存量锚点回推"（日末 = Σtotal_qty − 之后
INBOUND/OUTBOUND/TRANSFER/ADJUST 净变化；LOCK/RELEASE/INSPECT_*/MOVE 不改变总量
不入净变化集），演示种子配平后锚点与流水重构恒等。

修复的契约断裂（真联调 SmokeCheck 实测发现）：

```text
GET /api/notifications        空列表 items:null → items:[]（nil slice 序列化；
                              listInbox 预置空 slice。统一分页契约 api.md §2.1）
GET /api/reports/inventory-summary、/api/reports/inventory-turnover、
GET /api/reports/stagnant-stock、/api/reports/replenishment-suggestions、
    inbound/outbound-stats
     1) 行结构 SKUID 字段补 gorm:"column:sku_id"——GORM 对连续大写缩写的默认列名
        映射为 sk_uid，与 SQL 别名 sku_id 不匹配，sku_id 恒 0（warehouse_id 正常、
        sku_code 正常，仅 sku_id 断裂）；
     2) 空列表 items:null → items:[]（nil slice 同上，五个报表仓储统一预置）。
GET /api/reports/inbound-stats、/api/reports/outbound-stats、/api/inventory/alerts、
GET /api/reports/inventory-turnover、/api/reports/stagnant-stock
     时间字段统一 api.md §2 YYYY-MM-DD HH:mm:ss（JSONTime）：stat_date /
     last_moved_at 此前裸 time.Time 序列化为 RFC3339（2026-10-04 数据中心收敛轮
     的漏网，报表域 2026-10-05 收敛完毕）。
```

行为修复（独立复测员发现，联调轮修复）：

```text
GET /api/inventory、/api/inventory-ledgers、/api/serials
     batch_id 过滤参数缺省语义修正：未提供=不过滤（此前缺省被 parseBatchIDQuery
     包装为 &0，仓储拼 AND batch_id=0，缺省列表隐式隐藏全部批次库存行——
     inventory 全表 24 行仅可见 16 行，/api/inventory/summary 口径不一致）；
     显式 batch_id=0 仍为「只看非批次库存行」、>0 指定批次（不变）。
```
演示种子（db/seed/dev_seed.sql §8.5 趋势形态段，is_dev 门禁内、幂等、verify 全过）：

```text
期初流水 created_at 分散至近 7 天（每天 4 笔，期初-流水仍 1:1 成对）；追加 4 笔
"先入后出"净零对补影流水（id 9875-9878，business_no DEV-SEED-FLOW-*，Σ 净变化
为 0——现存量锚点不变、恒等式不变），趋势/出库统计/周转率获得非零真实值。
```

### 2026-10-05 分析卡片轮（聚合分析端点批——14 个新端点，全部 internal/reports 实现）

兑现前端先行契约（web/src 各分析页空态卡 + ReportFlowStats 趋势换端点，2026-10-05
契约蓝图立项）。落点与工程约束：全部只读参数化 SELECT（guard-readonly 红线内，不做
汇总表）；数据范围一律会话仓库快照（scopeOf），禁收前端范围参数；全部免分页直出
（TopN≤50、时序≤366 行、状态构成≤8 行——不走 ParsePage/OKPage；长窗口趋势免分页即
report-flowstats-trend-cap 销项方式）；GET /api/reports 目录 Catalog() 不动（数据域
前缀端点不入目录，同 /api/inventory/summary 先例）。

```text
—— 库存域（reports 实现、inventory 前缀挂载；权限挂 inventory:inventory:list，
   同 /api/inventory/analytics 先例）——
GET /api/inventory/sku-top         SKU 库存 TOP N（metric=qty|value 缺省 qty；
                                   limit 缺省 10、1–50；warehouse_id 可选；
                                   HAVING SUM(total_qty)>0；金额=批次成本价优先、
                                   非批次 SKU 成本价——与 inventory-summary 同源）
GET /api/inventory/turnover-trend  库存周转趋势（days 缺省 30、1–366；日粒度连续
                                   序列 generate_series 补零；日末=现存量锚点回推
                                   （同 dashboard/trend 口径），日初=日末−当日净变化；
                                   outbound_qty=当日 Σ|OUTBOUND 流水量|；
                                   turnover_rate=outbound_qty/avg_inventory（avg≤0→0））

—— 入库/出库/仓库（reports 实现、数据域前缀挂载；权限挂 reports:report:read——
   与所在分析页既有趋势端点同码，页面同权限面"菜单可见⟺数据可达"；
   warehouses/workload 例外挂 inventory:inventory:list——仓库分析页既有
   warehouse-stock 同码）——
GET /api/inbounds/status-composition   入库单状态构成（无参数全量现状分布；
                                       items 仅含 count>0 态，total=全量单据数）
GET /api/inbounds/supplier-rank        供应商入库排行（time_from/time_to 缺省近 30 天
                                       上限 366；limit 缺省 10、1–50；仅 source_type=
                                       'PURCHASE' 经 source_no=po_no 关联 PO→供应商，
                                       OTHER 来源不入榜；received_qty=Σ inbound_items
                                       .qty_received；窗口按 inbound_orders.created_at）
GET /api/outbounds/completion-rate     出库订单完成率（分母=status≠CANCELLED 出库单数，
                                       分子=SHIPPED_ALL+CLOSED（差额关闭视为完成出库
                                       流程）；in_progress=分母−分子；
                                       completion_rate=分子/分母×100，分母 0→0）
GET /api/outbounds/product-rank        商品出库排行（OUTBOUND 流水 GROUP BY sku：
                                       qty=Σ|qty_change|、amount=Σ|qty_change|×SKU
                                       sale_price——估值口径 valuation.basis=sale_price
                                       随响应披露，含采购退货出库等一切 OUTBOUND 扣减，
                                       与 outbound-stats 同口径可对账）
GET /api/warehouses/workload           仓库作业量（inventory_ledgers 按仓 GROUP BY：
                                       *_order_count=COUNT(DISTINCT business_no)、
                                       *_qty=Σ|qty_change|（INBOUND/OUTBOUND）；
                                       可见仓全集 LEFT JOIN，零作业仓返回零行；
                                       排序两 qty 之和 DESC）
GET /api/reports/flow-trend            出入库流水趋势（type=inbound|outbound 必填；
                                       time_from/time_to 同 requireRange；flowStats
                                       同一参数化 SQL 去 LIMIT/OFFSET——估值口径
                                       inbound→cost_price、outbound→sale_price 与
                                       既有出入库统计一致；stat_date JSONTime 同
                                       FlowStatRow 行形）

—— 采购/销售（reports 实现、数据域前缀挂载；权限挂域列表读权限——新分析页独立
   权限面，current.md 挂账口径；立项若裁决改挂 reports:report:read 可平移不改形状）——
GET /api/purchases/analytics/trend     采购订单金额趋势（status NOT IN
                                       ('DRAFT','CANCELLED')；amount=Σ total_amount
                                       订单金额口径，metric 字段显式披露≠inbound-stats
                                       流水估值）
GET /api/purchases/supplier-rank       供应商采购排行（状态过滤同上；排序 total_amount DESC）
GET /api/purchases/status-composition  采购单状态构成（无参数全量现状分布）
GET /api/sales/analytics/trend         销售订单金额趋势（status NOT IN
                                       ('DRAFT','REJECTED','CANCELLED')；订单金额
                                       口径——页面须标注"订单金额口径，非流水估值"）
GET /api/sales/product-rank            商品销售排行（items qty=Σ items.qty 下单量、
                                       amount=Σ items.amount 行金额（下单金额非估值）；
                                       sort=qty|amount 缺省 qty）
GET /api/sales/status-composition      销售单状态构成（无参数全量现状分布）
```

实现与契约细节（同轮裁决）：① 软删一致性——purchase_orders/inbound_orders/inbound_items
聚合 SQL 显式 `deleted_at IS NULL`（000016 补列、域模型 gorm.DeletedAt 自动过滤，
原始 SQL 须同口径对齐列表页可见集）；sales 域单据表无 deleted_at（000016 注明显式
字段域不涉及）。② 趋势行 date 字段为字符串 YYYY-MM-DD（dashboardTrendRepo/
analyticsTrendRow 先例）；flow-trend 的 stat_date 沿用 database.JSONTime（与既有
FlowStatRow 行形逐字段一致，RFC3339 收敛口径不变）。③ 状态构成 items 排序 count
DESC（图表友好；契约未冻结顺序）。④ status-composition/supplier-rank/trend 等聚合
的金额/数量字段 snake_case、SKUID 类字段显式 gorm:"column:sku_id"（sk_uid 映射缺陷
教训）、items 预置空 slice（nil→null 教训）。⑤ routes_test 冻结端点集同步 +14；
swag 全量重生成（269→283 操作）。

### 2026-10-05 平台批（工作台汇总 + 我的任务明细化——2 个新端点 + 既有端点接线回对）

```text
GET /api/workbench/summary   工作台四块入口计数（无参数；data={todo_count,approval_count,
                             task_count,exception_count}，与 web/src/api/task.ts
                             WorkbenchSummary 逐字段回对）。计数复用 dashboardTodayRepo
                             同源聚合：todo_count=PO 待收货（APPROVED/PARTIAL_RECEIVED
                             单据型待办）、approval_count=五单据待审聚合（purchase/sales/
                             transfer/inventory_adjustments PENDING_APPROVAL + count_orders
                             PENDING_REVIEW）、task_count=六作业块活动任务（上架
                             PENDING/IN_PROGRESS/PAUSED + 拣货 ALLOCATED/PICKING +
                             复核 PICKED + 打包 CHECKED + 发货 PACKED + 盘点
                             COUNTING/PENDING_REVIEW）、exception_count=异常未闭环
                             （NOT IN ('RESOLVED','CLOSED')，无仓库列全量口径）。四块
                             互斥拆分，Σ≠Dashboard pendingTaskCount（差 approval 一项），
                             前端 tooltip 如实披露。权限 inventory:inventory:list
                             （Dashboard/tasks 计数版已在同码暴露同一信息面）。
GET /api/tasks               我的任务列表（assignee 恒=当前用户，未领取任务不入）。
                             query：page/pageSize（ParsePage 缺省 1/20 上限 100）、
                             task_type=putaway|picking|checking（可选；packing/moving/
                             counting 不映射传值 400）、status=pending|in_progress|
                             completed|cancelled|exception（统一五值过滤，可选）、
                             warehouse_code（可选，warehouses.code 等值）。
                             行形 {id, task_no, task_type, source_no, warehouse_name,
                             total_qty, completed_qty, status(统一五值), raw_status
                             (原表态), assignee_name, created_at, completed_at|null}。
                             UNION ALL 三任务表 ORDER BY created_at DESC 分页：
                             putaway→putaway_tasks（claimed_by=me JOIN users 取姓名；
                             PAUSED→in_progress；completed_qty=COMPLETED?qty:0）；
                             picking→pick_tasks（assignee_id=me；CLAIMED/PICKING→
                             in_progress、PICKED→completed；completed_qty=picked_qty
                             进度列）；checking→check_tasks（assignee_id=me；
                             DONE→completed、EXCEPTION→exception 第五统一值；
                             completed_qty=DONE?qty:0）。权限 inventory:inventory:list
                             （dashboard/tasks 计数版已在同码暴露同一信息面，明细化
                             不抬高敏感面；不新增 task:* 域权限码——避免 seed 扩容与
                             DOMAIN_BOUND_RESOURCES 限定 fail-closed 恒隐身复发）。
POST /api/exceptions/{id}/images   既有端点接线回对（零后端改动；d314103 已交付
                             ——file_ids 校验存在/未过期/image/* → 追加 image_refs
                             去重累计≤20 + 处理记录 + 审计同事务；OPEN..PENDING_REVIEW
                             可挂、RESOLVED/CLOSED 拒绝 409；权限 returns:exception:execute
                             既有；image_refs 存 download URL 形态 '/api/files/{id}/
                             download'，展示走 /api/files/{id}/preview）。
                             接线状态（2026-10-05 端到端复核实测后明确）：后端已在位
                             （上传→挂接→去重→CLOSED 拒绝→持久化 e2e 实测通过）；
                             API 层已接（web/src/api/exception.ts
                             exceptionApi.attachImages）；视图层（异常中心页『挂接
                             图片』动作：先 POST /api/files multipart 上传取文件 ID
                             再提交、RESOLVED/CLOSED 状态按钮禁用）归前端批次落地
                             ——后端批次硬性范围禁改 web/，非后端缺口；创建流程遵循
                             『创建后挂接』裁决（创建入参无 image_refs）。
```

datax BY_FILTER 说明：internal/inventory/datax_export.go applyFilters 已按白名单处理
warehouse_id/zone_id/shelf_id/sku_id/bin_id/batch_id 六键（2026-10-04 补齐），zone_id/
shelf_id 过滤为已修复项非缺口——零端点零改动，既有导出契约不变。

### 2026-10-05 收口披露（后端两批跨批次口径汇总——前端批次消费清单，构建收口位落盘）

> 后端两批（分析卡片轮/平台批）提出的共享口径钉桩于此：端点形状以上文「分析卡片轮」
> 「平台批」两节为准，本节只披露跨批次口径与前端义务，前端批次消费前必读。

```text
① 权限挂载口径（/purchases/*、/sales/* 六端点）：
   挂域列表码 purchase:purchase:list / sales:sales:list（internal/reports/routes.go:103-108；
   current.md 挂账口径——新分析页独立权限面）。立项若裁决改挂 reports:report:read，
   可平移不改契约形状（routes.go:101-102 注释同文）——前端契约形状不受裁决结果影响。
② 出库估值口径（GET /api/outbounds/product-rank）：
   OUTBOUND 流水 GROUP BY sku（workbench.go:237 WHERE l.change_type='OUTBOUND'）；
   inventory Deduct 恒写 OUTBOUND（internal/inventory/service.go:611 Deduct → :657
   buildLedger，全仓唯一非测试 OUTBOUND 流水写入点），故采购退货出库等一切出库扣减
   全部入榜，与 outbound-stats 同口径可对账；amount=Σ|qty_change|×SKU sale_price，
   valuation.basis=sale_price 随响应披露。前端商品出库排行卡片副标题须如实披露估值
   口径（含采购退货等全部出库扣减、按售价估值），不得仅标"出库"造成口径误读。
③ 销售金额口径（GET /api/sales/analytics/trend）：
   订单金额口径（Σ total_amount，status NOT IN ('DRAFT','REJECTED','CANCELLED')），
   ≠ inbound/outbound-stats 的流水估值——前端页面须标注「订单金额口径，非流水估值」。
④ workbench 四计数口径（GET /api/workbench/summary，共享裁决）：
   四块互斥，Σ(todo+approval+task+exception) ≠ Dashboard pendingTaskCount——
   pendingTask（dashboard.go:613）= receive + 六作业块 + exception，不含 approval，
   即 Σ = pendingTaskCount + approval_count；task_count 为六作业块（putaway/pick/
   check/pack/ship/count）不含 approval。前端 WorkbenchPage/PadHomePage tooltip 须
   如实披露该差异（收口位 2026-10-05 19:57 真库复算：四计数 3/3/2/0，Σ=8，
   pendingTaskCount=5，dashboard/tasks 九块逐块对账一致）。接线面（WorkbenchPage.tsx:31
   queryKey ['workbench','summary']、PadHomePage.tsx:33 ['pad','home','workbench-summary']，
   均经 taskApi.summary）：端点路径与 WorkbenchSummary 字段零变化，queryKey 不变即通。
⑤ /api/tasks 统一五值 status + raw_status：
   status 统一五值 pending|in_progress|completed|cancelled|exception，exception 为
   第五值——picking（pick_tasks 原态 EXCEPTION，迁移 000008_create_sales_tables.up.sql:190）
   与 checking（check_tasks 原态 EXCEPTION，同文件 :229）的原态映射（workbench.go:763/774）；
   raw_status 随行下发原表态（workbench.go:730）。前端 web/src/api/task.ts 契约修订
   待前端批次同步：TaskType 收窄为 putaway|picking|checking 三值（packing/moving/
   counting 传值 400——无独立任务表不映射）、TaskStatus 扩 exception 第五值、
   TaskItem 补 raw_status；现前端契约（六值/四值/无 raw_status）与后端形状的差异面
   即为修订面。
⑥ 前端接线清单（后端两批报告 frontendWiring 10 项）：
   清单原文未随批落盘 docs/（收口位 grep docs/ 零命中，仅后端报告持有）——前端批次
   开工前经 orchestrator 取原清单为准；收口位从 api.md §1/§9 可证实的接线面至少含：
   workbench 四计数两页（/workbench、/pad/home）、/api/tasks 我的任务页、采购三分析
   端点、销售三分析端点、outbounds/product-rank 副标题披露（见②）、sales trend 口径
   标注（见③）。
```

⑦ 日界时区口径（2026-10-05 独立复核发现②，dev 验证发现冻结前入册披露——待裁决项）：
   全部日粒度聚合端点（/api/reports/flow-trend、/api/inventory/turnover-trend 及既有
   inbound/outbound-stats、inventory/analytics trend、purchases/sales trend 等）的
   `created_at::date` 分桶依赖 **DB 会话时区**；本项目 DSN 统一 `TimeZone=UTC`
   （internal/config/config.go:116），故日界为 UTC 00:00——Asia/Shanghai（+08）
   00:00–07:59 的流水/订单计入 **前一日** 桶。dev 真库实测：种子 2026-10-05
   00:30/01:10+08 的 OUTBOUND 流水在 flow-trend 中落 2026-10-04 桶（psql 同 DSN
   会话 SHOW timezone=UTC 复现；Asia/Shanghai 会话则单桶 10-05 合计）。该行为为
   全局既有口径（各端点同 SQL 面，非本批引入、批内"与既有端点同口径"声明成立），
   与 valuation.basis 同级的显式披露义务在此补齐：**前端日期标注/区间选择须按 UTC
   日界理解**。若裁决改为 Asia/Shanghai 日界，须改 DSN TimeZone 并全量回归日粒度
   端点与演示种子锚点回推恒等式（另行立项，本批不动全局配置）。
⑧ 前端接线移交（2026-10-05 独立复核发现③，交收口）：
   采购/销售分析两页组件（PurchaseAnalyticsPage.tsx / SalesAnalyticsPage.tsx，含
   analyticsApi 六函数真实调用与三态）已由前端批次落盘，但路由注册（router/index.tsx）、
   菜单项（config/menu.tsx，码 purchase:view / sales:view）与 lazy import 三件未接线，
   页面不可达——后端 6 端点已交付且实测 200（routes.go:103-108）。属 web/ 范围，
   后端批次硬性禁改，移交收口位/前端批次完成"四件套"接线。

### 2026-10-06 库存层级分布端点补齐（GET /api/inventory/{id}/distribution——前端先行契约 backend 交付）

```text
GET /api/inventory/{id}/distribution   库存层级分布树（frontend.md §10.4；库存详情页
                             『库存分布』页签前端先行契约的后端补齐，前端
                             StockDistributionNode 形状零变更）。path id=库存行 id
                             （GET /api/inventory/{id} 的 :id）；语义=按该行 SKU 聚合
                             其在数据权限范围内所有仓库的 仓库→库区→库位 三层分布
                             （详情页头部为行维度五指标，本页签回答"该 SKU 的库存在
                             哪里"）。响应 data=StockDistributionNode[]：warehouse 层
                             {warehouse_code,warehouse_name,total_qty,available_qty,
                             children}；zone 层 +zone_code；bin 层 +bin_code——编码
                             有无即层级标记，中间层回填 warehouse_code（前端 nodeKey
                             三段拼接依赖）；同库位多批次行在库位叶聚合（分布维度不含
                             批次，批次口径由批次页签承载）；零量行（total_qty=0）不进
                             树；排序按 仓库/库区/库位编码。权限
                             inventory:inventory:list（§5.4.1 该资源冻结仅 list 动作，
                             与详情同码）；数据权限 Scope 仓库集强制收敛（拒绝前端传入
                             范围参数）；入口行不存在或行仓库越权 → 404
                             COMMON_NOT_FOUND（fail-closed 与详情同口径），SKU 无正数
                             库存行 → data=[]（前端空态"该 SKU 当前在所有仓库均无库存"）。
                             实现：internal/inventory{handler.go getStockDistribution /
                             query.go GetStockDistribution+buildStockDistributionTree(纯
                             函数,单测 distribution_test.go) / repository.go
                             stockDistribution(单查询 LEFT JOIN warehouses/zones/bins 只
                             读取编码)}；routes_test 冻结端点集同步；swag 已重生成。
```

### 2026-10-06 作业效率提升层一期（契约先行——依据定稿计划 docs/plans/2026-10-06-efficiency-layer-phase1.md 冻结，实现波次按本节验收）

> 文档先行契约增量（AGENTS.md 硬性规则 2）：登记计划冻结的端点/契约形状，后端实现波次
> 交付时不得擅改本节形状，口径差异以新日期小节披露。数据库迁移 000020–000023 与新表见
> database.md §2 效率层注记；需求口径见 requirements.md §2.10。

全局业务搜索（internal/search 新包，只读零写语句）：

```text
GET /api/search    全局业务搜索（Ctrl/Cmd+K 命令面板数据源）。端点级只挂认证（auth
                   保护组），组内按用户权限集逐 type 过滤——无对应 list 码的 type 不查询
                   不出组（需 auth 包导出 HasPermission helper，语义与 RequirePermission
                   同构：uc.IsSuper || 权限快照含 code，遗漏则超管看不到任何分组）。
                   query：q（上限 64 字符；空查询/长度<2 直接返回 {groups:[]} 不做任何
                   SQL；超长 400 invalidParam）、types（可选，逗号分隔）、warehouse_id
                   （可选，收窄过滤器——与数据权限 scopeOf 求交，越界仓库按该 type 无
                   结果处理不 403，防范围探测）、limit（每组条数缺省 5、上限 20）。
                   响应 data={groups:[{type,title,count,items:[{id,type,title,code,
                   status,summary,updated_at}]}]}；跳转 path 由前端映射（后端不编码前端
                   路由）。type→权限映射：sku/product/barcode→masterdata:sku:list、
                   batch→inventory:batch:list、serial→inventory:serial:list、bin→
                   warehouse:bin:list、warehouse→warehouse:warehouse:list、customer→
                   masterdata:customer:list、supplier→masterdata:supplier:list、doc→
                   采购/入库/销售/出库/调拨/盘点/异常各单据号（各自域 list 码）、
                   logistics→shipments 既有码（物流单号列名以 DDL 为准，实现者核验）。
```

保存视图（internal/userpref 包，迁移 000020 user_saved_views，权限=认证即可、无权限
点——notifications 个人收件箱先例口径）：

```text
GET    /api/user/views          视图列表（?page_key= 必填，正则 ^[a-z0-9._-]{1,64}$）
POST   /api/user/views          新建（name ≤64 字符且 (user_id,page_key,name) 唯一，重复
                                409；filters_json/sort_json/columns_json 合法 JSON 且各
                                ≤8KB；page_size 1–100）
PUT    /api/user/views/{id}     修改（改名/改内容/is_default；先按 (id,user_id) 查归属，
                                非本人一律 404 防探测）
DELETE /api/user/views/{id}     删除（非本人 404）
```

严格按用户隔离：全部查询强制 user_id=当前用户。is_default 每 (user_id,page_key) 至多
一个（部分唯一索引 uk_user_saved_views_default），「设为默认」服务层单事务内清除旧默认，
「恢复默认」= PUT is_default=false。字段披露：filters_json=筛选条件（SfSearchForm
cleanValues 产物）、sort_json 恒 '{}'（排序通道预留，一期不采集）、columns_json=隐藏
列键数组（列序拖拽不做）；视图应用以 URL/页面参数展开还原，view=<id> 仅作当前视图名
标记（用户改动任一筛选即清除标记）。

用户偏好（internal/userpref 包，迁移 000021 user_preferences，权限同上）：

```text
GET /api/user/preferences        读偏好（?keys= 逗号分隔可选，缺省全部）
PUT /api/user/preferences/{key}  写偏好。key 服务端白名单（冻结）：default_warehouse_id、
                                 last_warehouse_id、last_business_type、last_printer_id、
                                 last_print_template_id、recent_visits、recent_filters；
                                 白名单外 400 invalidParam。value 为 jsonb 单值 ≤16KB
                                 （超限 400）；recent_visits 由服务端裁剪至 20 条
                                 {path,title,visited_at}。不挂 Audit 中间件（高频写会
                                 刷屏 operation_logs——审计只记业务动作，口径披露）。
```

自动下一条（internal/reports workbench 扩展，纯 SELECT，权限 inventory:inventory:list
与 /api/tasks 同码）：

```text
GET /api/tasks/next   自动下一条候选任务。query：task_type 必填，白名单五值
                      putaway|picking|checking|receipt|exception（前三者复用 /api/tasks
                      三分支字段映射与五值映射；receipt→inbound_orders 状态 RECEIVING
                      候选池、exception→exceptions 状态 OPEN/处理中候选池；其余 400
                      invalidParam 沿 validTaskType 裁决注释扩展）；warehouse_id 可选
                      （与 scopeOf 求交收窄）；current_task_id 可选（结果恒排除）。
                      候选池=(本人已领取且进行中) OR (PENDING 未领取)——putaway：
                      claimed_by=me AND status IN ('IN_PROGRESS','PAUSED')，或
                      claimed_by=0 AND status='PENDING'；picking：assignee_id=me AND
                      status IN ('CLAIMED','PICKING')，或 assignee_id=0 AND
                      status='PENDING'；checking：(assignee_id=me OR assignee_id=0) AND
                      status='PENDING'（check_tasks 无 CLAIMED 态）。照抄 /api/tasks 原
                      WHERE（硬过滤 assignee=me）会致未领取 PENDING 永不可达，禁止。
                      排序为真实 SQL ORDER BY（严禁前端推算）：本人已领取(进行中) DESC
                      > priority DESC > 超时 > created_at ASC。口径差异披露：① 超时层
                      仅 putaway/picking（不新增 task.timeout.check_hours，避免连带超时
                      通知扫描与收件人规则扩展——checking 分支排序为 mine > priority >
                      created_at，无超时层）；② receipt/exception 分支无 priority 列
                      （000023 不扩此二表，不引入无处取数的排序层），排序仅
                      created_at ASC（先来先办），exception 分支另加 assignee=me 处理中
                      > OPEN 前置层；③ exceptions 表无仓库列，warehouse_id 过滤对
                      exception 分支不生效（验收不得当 bug）；④ receipt 无领取语义。
                      响应 {has_next: boolean, task: myTaskItemDTO|null}（task 行形复用
                      GET /api/tasks 明细行；receipt/exception 分支 task_no=新单号）。
```

任务优先级写入（internal/purchase / internal/sales——上条 priority 排序层的数据来源，
不写即假能力；状态守卫：完成/取消态拒绝 409；middleware.Audit 审计）：

```text
PUT /api/putaway/{id}/priority  上架任务优先级（值域 0–9；新权限码 purchase:putaway:assign）
PUT /api/picks/{id}/priority    拣货任务优先级（新码 sales:pick:assign）
PUT /api/checks/{id}/priority   复核任务优先级（新码 sales:check:assign）
```

权限码增量仅此三枚（动词 assign 在 M2 冻结枚举内；permissions.go/seed/seed_test/swag
四处同源同步，不新增域码、不触发 DOMAIN_BOUND_RESOURCES fail-closed 面）。

最近操作（internal/reports 扩展，只读零新表，权限 inventory:inventory:list）：

```text
GET /api/workbench/recent-operations  本人最近操作（query limit 可选缺省 10；SELECT
                                      operation_logs WHERE user_id=当前 ORDER BY id
                                      DESC LIMIT n；行形 {time,action,module,
                                      object_type,object_id,success,error_code,
                                      request_id}）。写入链路零新增——仍仅 middleware
                                      .Audit 既有业务写入；顶部「最近访问」属导航态，
                                      走用户偏好 recent_visits（前节），不入审计（避免
                                      污染 append-only 业务审计，口径披露）。
```

批量操作统一结果契约（冻结——新增批量端点与打印语义演进共用；前端统一 BatchResultDrawer
消费，禁各页面自写结果弹窗）：

```text
{total, success_count, failed_count, skipped_count,
 results:[{id, status: 'success'|'failed'|'skipped', reason}]}

语义冻结：skipped=幂等命中/已处目标态；failed.reason 用既有错误码字符串；批量不整体
回滚。新增端点：POST /api/putaway/batch-claim（权限 PermPutawayClaim 既有）、
POST /api/picks/batch-claim、POST /api/checks/batch-claim（权限既有，零新码）——逐条
调用既有 claim 服务函数：PENDING→success；已被本人领取→skipped；被他人领取/状态非法
→failed(reason=既有冲突码)；逐条审计。
语义演进：POST /api/prints/tasks 响应改为批量结果形态（200 + BatchResult；全部参数
合法但部分对象不可打印时逐条 failed，reason=PRINT_SKU_DISABLED 等；整请求参数错误仍
400）——2026-10-05 二维码闭环轮「409 + details.disabled_ids 整体拒绝」语义废止，旧节
不改写、以本节为准。
```

Excel 失败行修复（internal/datax 扩展，权限 PermImportCreate 既有）：

```text
GET  /api/imports/{id}/error-file    错误 Excel 下载（既有端点零改动——「原数据+错误
                                     原因列」既有口径）
POST /api/imports/{id}/retry-failed  失败行重试：读源任务 status IN ('INVALID','FAILED')
                                     的行 → 以同一 ImportWriter 管线创建新导入任务（走
                                     正常校验+确认流程，复用导入向导六步语义）；仅重导
                                     上次失败行，上次成功行不入集、不重复成功数据。
                                     前置核验：FAILED 行须保证无部分落库（分批事务边界），
                                     不满足则仅限 INVALID 行并在实现交付时披露。
```

工作台汇总增量（additive，既有消费方零破坏）：

```text
GET /api/workbench/summary   增量字段 timeout_count / mine_count / today_completed_count
                             （键名对齐 workbench_summary.go 既有命名；四计数既有形状
                             不变）。既有列表端点按需补 source_no/inbound_no 等过滤
                             query 参数（上下文导航核验后最小追加，禁新建聚合端点）。
```

### 2026-10-06 效率层一期后端交付披露（B1–B4 实现波次落地差异汇总——同日契约先行节的验收补充，旧节零改动）

```text
搜索（B2，DDL 复核修正）：
- skus 表无 name 列（名称在 products.name）——sku type 实际匹配 skus.code +
  products.name；000022 实建 17 个 trgm 索引（无 idx_skus_name_trgm）。
- logistics（shipments）匹配 shipment_no / tracking_no / outbound_no 三列（000008 DDL）。
- batches / exceptions 无仓库列：warehouse_id 过滤对两 type 不生效（不出组）；
  transfer 单号匹配 from_warehouse_id / to_warehouse_id 双仓 OR。
- count 字段=该 type 匹配总数（COUNT(*) OVER() 同查询取数）；空结果组不返回；
  limit<1 或非法值 400 invalidParam。

打印批量结果演进（B4，契约先行节形状的逐条 reason 补充）：
- 请求内重复 data_id → skipped(reason="DUPLICATE_DATA_ID")；对象缺失 →
  failed(reason="PRINT_DATA_NOT_FOUND")；停用 SKU → failed("PRINT_SKU_DISABLED")。
- 全部对象不可打印（0 可打印）→ 200 + 空结果集，不创建打印任务。

retry-failed（B4，8.9 前置核验结论）：
- 五域 Writer 落库均为逐行/逐单事务（期初库存逐行 database.Tx、PO/SO 整单回滚），
  FAILED 行无部分落库——重导集含 INVALID + FAILED（回退方案未触发）。
- 新任务为 PARSED 态、走正常校验+确认流程；响应复用 ImportUploadResult 形态
  （含新任务 id）；源任务行状态零篡改。

幂等头键（B4，§7 合成规则的长度核验）：
- 最长行级通式（trout，单号≤21+行号+三维≤12 位）≈73 字符，+头键 32+1=106 ≤
  varchar(128)——头键上限维持 32。
- moves（移库）存量无行级键（§7 通式表未列 move 行，api.md 此前声称已有系口径
  偏差）：头键缺省时保持无键（零行为变化）；带头键时按通式新增
  move:{source_no}:{bin}:{sku}:{batch}。

用户态（B1，行为口径补充）：
- recent_visits 服务端裁剪=最新在前保留 20 条。
- PUT /api/user/preferences/{key} 请求体为原始 JSON 值（非 {value:...} 包装信封）。

工作台汇总（B3）：timeout_count / mine_count / today_completed_count 已交付，
既有四计数形状不变。

全局搜索（B2 补录）：items 形状为八字段——在契约先行节七字段基础上增
`navigation:{kind,id}` 跳转标识（kind 权威值=type 本值 sku/product/barcode/batch/
serial/bin/warehouse/customer/supplier；doc 组=具体单据 kind purchase_order/
inbound_order/sales_order/outbound_order/transfer_order/count_order/exception；
logistics=shipment；id=业务 ID 字符串形态）。前端 searchTargets 可直接消费
navigation.kind（单号前缀映射保留为回退）。

### 2026-10-06 效率层一期集成收口披露（router 装配 / 幂等挂载 / 批量结果收敛——同日集成波次，旧节零改动）

```text
幂等中间件挂载（§7 端点级仲裁的实现落位）：
- 组级路由感知挂载（internal/idempotency.RouteGuard，注册表 internal/router/
  idempotency_mount.go）：挂 protected 组 AuthRequired 之后、各域 RegisterRoutes
  之前；仅白名单端点（写方法 + FullPath 命中）进入仲裁，表外端点与 GET 零干预。
  与逐路由行内挂载语义等价——装配改动集中在 router 层，白名单漂移由启动核验
  fail-fast（verifyRelaxedRoutes 同款）。挂载端点清单见 §7 新增条目（16 条）。
- 实现批 ask 清单 12 项差异披露：①「入库确认 POST /api/inbounds/:id/confirm」
  与②「库存调整写端点」在域内均不存在（purchase 入库确认入口即收货确认；调整单
  由盘点完成生成，inventory 域仅 GET 列表）——不造路由，如实不挂；调拨 execute
  按 outbound/arrive/receive 三段全挂。
- 灰度形态：全表 Required=false（无 Idempotency-Key 头放行不缓存）。retry-failed
  实现批建议 RequiredMiddleware，但已交付前端 dataApi.imports.retryFailed
  （web/src/api/data.ts:336）未附键，Required 即刻 400 打断重导入口——本期同表
  灰度，升级随前端提交点接 useIdempotentMutation 批次（followup）。

批量结果收敛（§2.7 统一结构 internal/batchresult）：
- POST /api/prints/tasks 响应类型收敛至 batchresult.Result（私有 TaskBatchResult/
  TaskBatchItem 删除）——JSON 契约零变化（id 恒字符串、reason omitempty、skipped
  可携 DUPLICATE_DATA_ID reason），前端零感知。
- batch-claim ×3（sales/purchase）暂不收敛：results[].id 现为 JSON 数字、统一结构
  为字符串（api.md §2 冻结口径），收敛即改契约且重试流以 results[].id 回填请求
  ids（数字→字符串会使服务端整数绑定 400）——须先改 api.md 再同步前端消费点
  （web/src/api/task.ts 等），挂账后续批次。
- reports routes_test 冻结清单补录 GET /api/workbench/recent-operations 与
  GET /api/tasks/next（B3 交付端点，防回退）。
```
