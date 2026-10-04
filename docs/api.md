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
/api/prints             打印

—— 扫码 ——
/api/scanner       扫码解析 /api/scanner/resolve、扫码设备管理、扫码日志

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
