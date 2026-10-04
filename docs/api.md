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

—— 基础资料 ——
/api/products      商品
/api/skus          SKU

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
/api/imports       导入
/api/exports       导出
/api/prints        打印

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
