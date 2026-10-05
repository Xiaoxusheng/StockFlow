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
/api/zones         库区
/api/shelves       货架
/api/bins          库位

—— 往来单位 ——
/api/suppliers     供应商
/api/customers     客户

—— 采购与入库 ——
/api/purchases     采购
/api/inbounds      入库
/api/receipts      收货
/api/putaway       上架

—— 销售与出库 ——
/api/sales         销售
/api/outbounds     出库
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
