# StockFlow 商品二维码规范（SFQR 协议）

> 版本：v1.0 ｜ 本文档是 SFQR（StockFlow QR）商品二维码协议的唯一契约，格式/版本/错误码/纸张布局以本文件为准 ｜ 关联文档：[printing.md](printing.md)、[scanner.md](scanner.md)、[frontend.md](frontend.md)、[api.md](api.md)

---

## 1. 定位与统一口径

SFQR 是 SKU 的**协议化二维码身份**：把「这是 StockFlow 的一个 SKU」编码为机器可自识别的字符串，扫码枪扫入后由后端统一解析直达对象。它与 SKU 既有的一维主条码并存，分工见 §2.5。

三条全站统一口径（任何实现不得偏离）：

```text
① QR 由 SKU 身份 + 协议动态生成，不持久化——不新增二维码表、不存二维码列；
   载荷仅由 SKU 编码按 §2 规则构造，可随时重建。
② 打印统一走 PrintTask——二维码标签与其它打印对象一样经模板 → 任务 → 预览 →
   执行链路（printing.md §1.2），禁止页面绕过打印中心直接出码打印。
③ 扫码统一走 BarcodeResolver——前端禁止任何本地业务解析，扫码识别唯一入口为
   POST /api/scanner/resolve（scanner.md §5.3、api.md §7）。
```

**前后端如何共享同一协议**：

```text
唯一真相     本文件（黄金向量表 §4 逐字冻结，两侧测试/实现逐字对照）
构造唯一点   前端 web/src/utils/qrPayload.ts（buildSfqrSku / tryBuildSfqrSku；另导出
             parseSfqrPayload / isSfqrPayload 两个纯格式辅助——预览/核对场景展示段含义
             与前缀探测，不是业务解析）——二维码中心预览、SKU 详情抽屉、SKU_LABEL
             标签渲染三处共用，禁止第二处拼串
解析唯一点   后端 internal/devices/sfqr.go parseSfqr——resolve 管线第 0 段专用，
             其余代码不得各自解析 SFQR
业务解析入口 POST /api/scanner/resolve（前端不做协议解析）
```

协议正确性由同一份黄金向量分别钉死：Go 侧 `internal/devices/sfqr_test.go` 表驱动断言全表（权威自动化测试）；前端依赖 tsc strict + 实现逐字对照本表评审 + 运行时载荷明文自证 + 扫码往返实测（docs/testing.md §6）。

---

## 2. 协议格式（v1）

### 2.1 格式

管道式 4 段文本，ASCII `|` 分隔：

```text
SFQR|1|SKU|<sku_code>
```

BNF：

```text
<sfqr-code> ::= <magic> "|" <version> "|" <type> "|" <payload>

<magic>     ::= "SFQR"                  ; 字面，大小写敏感
<version>   ::= 1*DIGIT                 ; v1 实现仅接受 "1"
<type>      ::= 1*ALPHA                 ; v1 实现仅接受 "SKU"（§3 预留类型显式拒绝）
<payload>   ::= <sku-code>              ; v1 载荷 = SKU 编码单身份段
<sku-code>  ::= 1*64<chr>               ; 非空、≤64 字符；不含 "|"，不校验字符集
<chr>       ::= 除 ASCII "|" 外任意字符  ; 查无即 SKU_NOT_FOUND，与条码匹配器口径一致
```

- 分隔符 `|` 与 SKU 编码字符集零交叠：SKU 编码正则 `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`（internal/masterdata/service.go:63）不含 `|`，无需转义。
- 总长受 resolve 入口既有 ≤255 校验天然覆盖（internal/devices/service_scan.go:85）。
- SKU 编码创建后不可变（UpdateSKU 不接受 code 修改）且活集唯一（uk_skus_code WHERE deleted_at IS NULL），满足作为唯一载荷身份段的全部前提。

### 2.2 段表

| 段 | 名称 | 取值 | 规则 |
|---|---|---|---|
| 0 | magic | `SFQR` | 字面，大小写敏感；前缀 `SFQR|` 即锁型进 SFQR 分支 |
| 1 | version | `1` | 单字符；≠`"1"` → `SFQR_VERSION_UNSUPPORTED`，明确返回不落回 |
| 2 | type | `SKU` | v1 唯一实现值；`BIN`/`BOX`/`PALLET` 为协议预留但解析器显式返回 `SFQR_TYPE_UNSUPPORTED`（预留未实现）；其它未知值同码返回不支持；大小写敏感 |
| 3 | payload | `<sku_code>` | SKU 编码，非空、≤64 字符；不校验字符集 |

### 2.3 完整示例

```text
SFQR|1|SKU|SKU-001
```

### 2.4 为什么不用 JSON 载荷（定案记录）

- 现有 resolve 管线是纯字符串内容分发（internal/devices/service_scan.go:110-193），`strings.HasPrefix(code, "SFQR|")` 可 O(1) 自标识；JSON 载荷无前缀特征，只能对每个未命中码尝试解析，浪费且可能误判，解析失败还会静默落回 UNKNOWN_BARCODE——违反「未知版本必须明确不支持、禁止静默解析失败」。
- 段数固定、人眼可读，PDA/扫码枪回显即可核对身份；JSON 需先解析才能读。
- 更短载荷 = 更低 QR 模块密度 = 小尺寸热敏纸（40×30）更高扫码可靠度（`SFQR|1|SKU|SKU-001` 共 19 字符）。
- 决定性事实：print_task_rows 行上唯一稳定可得的身份是 SKU 编码（2026-10-05 前无业务 ID 列）；SKU 编码满足正则无 `|`、不可变、活集唯一三前提，v1 载荷定为 SKU 编码单身份段。SKU 数字 ID 不入码（避免跨环境标签 ID 撞库静默错绑），重打取数由迁移 000019 的 print_task_rows.data_id 列承载（§7.4）。

### 2.5 与一维主条码的双身份分工

同一张 SKU 标签上两种身份并存，都能扫码到达同一 SKU：

```text
一维条码（Code128 等）= 主条码（barcodes 表，物流扫码存量习惯）
QR 码                = SFQR 协议身份（载荷 = SKU 编码）
```

分工原则：一维条码面向既有物流/收货扫码习惯；SFQR 面向协议化直达（扫码 → resolve → 二维码中心）。两者语义由本文件统一，避免现场混淆。

### 2.6 边界注记：SKU 软删后编码可被复用

uk_skus_code 为部分唯一索引（WHERE deleted_at IS NULL）：SKU 软删后其编码可被新 SKU 复用，旧标签扫码将解析到新属主（与条码全局唯一不回用的语义不同）。建议现场废码即回收标签（库内回收流程属业务流程域，另行立项）。

---

## 3. 版本与命名空间策略

### 3.1 版本冻结与演进

v1 冻结。未来只允许两种演进，均不允许静默降级：

```text
a) v1 内新增预留 type（BIN/BOX/PALLET）——改解析点 + 本文件 + 两侧测试；
b) v2 新契约——另立文档；解析器对 version ≠ "1" 一律返回 SFQR_VERSION_UNSUPPORTED，
   绝不猜测或降级解析。
```

### 3.2 barcodes 命名空间保留

`SFQR|` 前缀是协议保留命名空间。为封死「条码注册侧写入协议保留字造成一码两义」的通路：

- SKU 条码新增/修改（internal/masterdata/service_sku.go buildBarcodes，Create/Update 两调用点经同一函数全覆盖）拒绝含 `SFQR|` 前缀的条码值，返回 400 入参校验错误（api.md §9）。
- 存量数据不清洗（可能性极低；扫码侧 SFQR 分支优先级高于条码匹配器，协议命名空间语义仍自洽）。

---

## 4. 黄金向量表（逐字冻结）

下表是协议正确性的唯一权威，两侧测试逐条对照、实现逐字遵守；任何一侧与表冲突以本表为准并视为实现缺陷。

| # | 输入（逐字） | 期望结果 | 说明 |
|---|---|---|---|
| 1 | `SFQR\|1\|SKU\|SKU-001` | 合法，sku_code=`SKU-001` | 典型编码 |
| 2 | `SFQR\|1\|SKU\|A1_002` | 合法，sku_code=`A1_002` | 含下划线 |
| 3 | `SFQR\|1\|SKU\|S0001` | 合法，sku_code=`S0001` | 纯字母数字 |
| 4 | `SFQR\|2\|SKU\|X` | `SFQR_VERSION_UNSUPPORTED` | 未来版本，明确拒绝不降级 |
| 5 | `SFQR\|0\|SKU\|A` | `SFQR_VERSION_UNSUPPORTED` | 0 不是合法版本 |
| 6 | `SFQR\|1\|BIN\|A-01` | `SFQR_TYPE_UNSUPPORTED` | 预留类型未实现 |
| 7 | `SFQR\|1\|BOX\|B1` | `SFQR_TYPE_UNSUPPORTED` | 预留类型未实现 |
| 8 | `SFQR\|1\|PALLET\|P1` | `SFQR_TYPE_UNSUPPORTED` | 预留类型未实现 |
| 9 | `SFQR\|1\|XYZ\|A` | `SFQR_TYPE_UNSUPPORTED` | 未知类型 |
| 10 | `SFQR\|1\|sku\|A` | `SFQR_TYPE_UNSUPPORTED` | type 大小写敏感 |
| 11 | `SFQR\|1\|SKU\|` | `SFQR_INVALID` | payload 空 |
| 12 | `SFQR\|1\|SKU\|A\|B` | `SFQR_INVALID` | 5 段，段数非法 |
| 13 | `SFQR\|1` | `SFQR_INVALID` | 2 段，段数非法 |
| 14 | `SFQR\|1\|SKU\|` + 65 个字符 | `SFQR_INVALID` | payload 超 64 字符上限 |

> **表外输入边界注记（2026-10-05 实施回写）**：黄金向量 14 条之内两侧实现逐条一致；表外输入——版本段非数字字符（如 `SFQR|x|SKU|A`）——两侧允许实现差异：Go 侧 parseSfqr 按段表语义「≠`"1"` → `SFQR_VERSION_UNSUPPORTED`」（internal/devices/sfqr.go:51），TS 侧预览辅助 parseSfqrPayload 先校 BNF 字符类（1*DIGIT 不过 → `SFQR_INVALID`，web/src/utils/qrPayload.ts:115）。此类输入不属于黄金向量契约，业务裁决一律以后端 resolve 为准（前端 parseSfqrPayload 仅预览/核对展示，非业务解析入口）。

---

## 5. 错误码

新增三个识别类错误码（与 scanner.md §6.1 同表；注册于 internal/devices/errors.go，均 400）：

| 错误码 | HTTP | 中文说明 | 触发 |
|---|---|---|---|
| `SFQR_INVALID` | 400 | SFQR 载荷格式非法 | 段数错误 / payload 空 / 超 64 字符 |
| `SFQR_VERSION_UNSUPPORTED` | 400 | SFQR 协议版本不支持 | version ≠ `1`（明确拒绝，不降级） |
| `SFQR_TYPE_UNSUPPORTED` | 400 | SFQR 类型已预留未实现 | `BIN`/`BOX`/`PALLET` 及其它未知 type |

SFQR 分支内**不产生** `UNKNOWN_BARCODE`：前缀命中即锁型，格式/版本/类型错误显式返回对应 SFQR_* 错误码，禁止把残缺 SFQR 当普通条码静默落回后续匹配器。载荷合法但 SKU 不存在/软删/停用 → 既有 `SKU_NOT_FOUND`（404，与 SKU 条码分支 internal/devices/service_scan.go:141-147 同口径）。

---

## 6. 解析语义与优先级

resolve 管线（internal/devices/service_scan.go resolvePipeline，顺序冻结）新增**第 0 段**：

```text
第 0 段  SFQR：HasPrefix("SFQR|") 即锁型 → parseSfqr
         → 格式/版本/类型错误：显式返回 SFQR_*，不落回后续匹配器
         → 载荷合法：按 sku_code 查 SKU（窄接口 SfqrSkuReader.FindBySkuCode）
           未命中/软删/停用 → SKU_NOT_FOUND
           命中 → {type:"sku", id, code, name}（复用既有响应形态）
第 1 段  单据号前缀（既有）
第 2 段  SKU 条码（既有）
第 3 段  库位码（既有）
第 4 段  序列号（既有）
第 5 段  批次码（既有）
第 6 段  全未命中 → UNKNOWN_BARCODE（既有）
```

- **优先级最高**：同一字符串即使被注册为 SKU 条码，也走 SFQR 分支（协议命名空间保留的运行时保证，§3.2）。
- **失败不落回**：SFQR_* 与 SKU_NOT_FOUND 均直接终止管线，禁止静默降级。
- **审计照常**：scan_logs 落库含原始码与 SFQR 错误码（错误也落，errorCodeOf 已通用）；去重窗口 duplicate 注记不受影响。
- 窄接口 SfqrSkuReader 定义于消费方 internal/devices/ports.go、实现落 masterdata/devices_resolve.go（skus.code 精确命中 JOIN products，双 deleted_at IS NULL 对齐既有 FindByBarcode 口径）、router 装配 `devices.WithSfqrSkus(masterdata.NewSKUBarcodeReader(db))`（同一实现双接口）、handler 启动期 nil panic fail-fast——沿 plan §12.2「新增匹配 = 接口 + 实现 + Option + 装配」四点模式。

---

## 7. 生成 / 预览 / 打印 / 扫码闭环

### 7.1 生成（动态，不持久化）

QR 内容 = `buildSfqrSku(sku.code)` 纯函数产出（前端构造唯一点 web/src/utils/qrPayload.ts：非法输入 `buildSfqrSku` 显式 throw（交互场景显式失败）、`tryBuildSfqrSku` 供渲染层降级返回 null）。不新增二维码存储，无任何二维码表/列。

### 7.2 预览

- **二维码中心**（/qr-codes，基础资料组菜单「商品二维码」，权限复用 `sku:view`）：SKU 列表 + 48px 预览 + 载荷明文展示 + 详情抽屉（SfQrPreviewDrawer）+ 批量打印（页面规格见 frontend.md §13.1）。
- **SKU 列表快捷入口**：操作列「二维码」打开同一详情抽屉。
- 载荷明文随预览展示，供人工核对协议正确性（§1 自证层）。

### 7.3 打印（统一 PrintTask）

- 模板 `object_type=SKU_LABEL` 且 `qrcode_enabled=true` 时，标签 QR 内容 = SFQR 载荷（取 values.sku_code 构造）；旧任务快照无 sku_code 值时维持主条码原文渲染（扫码走条码匹配器，行为不变）。
- 打印统一走 PrintTask（创建 → 模板快照冻结 → 预览 → 执行 → 确认），零新增打印端点。
- **纸张布局规范**：

| 纸张 | QR 尺寸 | 布局 |
|---|---|---|
| 热敏 40×30 | 20mm | 极简布局：仅 QR + SKU 编码 + 商品名（超长省略号截断），不渲染一维条码与其它字段 |
| 热敏 60×40 | 28mm | 标准布局（QR + 一维主条码 + 字段区） |
| 热敏 100×50 | 34mm | 标准布局 |
| A4 / A5 | 22mm（网格单元内） | 网格分片布局，见下 |

- **纠错规则**：QR 尺寸 <24mm 纠错级别升 Q，否则 M。
- **Quiet Zone**：margin=4（qrcode 库参数），全站 QrCodeView 统一生效——黑码白底、SVG 矢量渲染不变。
- **A4/A5 网格容量算法**（与前端 web/src/components/print/labelSheet.ts 同文）：

```text
最小标签单元 labelCellMm = 50 × 30
cols = floor((纸宽  − 12) / 50)      # 12mm 页边距
rows = floor((纸高  − 12) / 30)
A4（210×297）→ 3 × 9 = 27 张/页
A5（148×210）→ 2 × 6 = 12 张/页
热敏纸一律 1 张/页（一码一页）
```

- 预览页按分片渲染（每片一张纸、CSS grid 固定行高、break-inside:avoid，分页不截断标签）；页码/翻页按分片计数；头部明示「总张数 = total_count × copies」。
- 最小单元为默认值：实际标签纸若为预制模切（如 A4 3×8 排列），按纸样调整 labelCellMm 常量，实纸验证口径见 docs/testing.md §6。

### 7.4 重打（评审修订：fail-closed）

- **重打 = 新任务，不碰历史**：重打以既有任务行为取数依据创建新 PrintTask，历史任务与历史行永不修改。
- 取数依据 = 任务行快照 data_id（print_task_rows.data_id，迁移 000019 增列，创建任务时持久化行业务身份；旧行 NULL → 前端读作空）。
- **凡无 data_id 的任务行一律不可自动重打**（含 000019 之前的全部旧任务，即便其模板绑定过 sku_code 字段）——data_ids 通道契约恒为 SKU 数字 ID 十进制文本（§8），以 SKU 编码兜底会把纯数字编码装配成同数字 ID 的另一 SKU 标签（静默错绑打错货），故 fail-closed。
- 前端重打前经 tasks.detail 校验：任一行 data_id 为空即中止并如实提示「该任务创建于身份快照能力之前，无法自动重打，请到商品二维码中心按 SKU 重选打印」，不发创建请求；全部行有 data_id 方可创建，且 all-or-nothing（禁止部分行静默重打）。
- 存量行不做回填造假（旧行身份不可恢复，设计如此）。

### 7.5 扫码直达

扫码枪扫入 SFQR → POST /api/scanner/resolve 命中 `{type:"sku",...}` → PC 端 ScanDirectCard 直达 `/qr-codes?code={code}` 打开对应 SKU 的二维码详情抽屉（闭环收口）。前端零自研解析。

---

## 8. data_ids 通道纪律

打印 data_ids 契约**恒为 SKU 数字 ID 的十进制文本**（`String(sku.id)`）：internal/printing/ports.go ParseIDs 十进制解析 → internal/masterdata/printing_content.go `WHERE s.id IN ?` 按 skus.id 装配。

- SFQR 载荷与 SKU 编码**绝不进入** data_ids 通道：SKU 编码正则允许纯数字（§2.1），把编码 `"42"` 混入通道会被装配成 skus.id=42 的另一 SKU 标签（静默错绑打错货）。
- 编码/协议载荷亦不得混入 ParseIDs 注释声明的该通道（契约注释同步）。
- 二维码中心/SKU 抽屉发起打印恒传 `String(sku.id)`；历史重打只认行快照 data_id（§7.4）。

---

## 9. 不可打印校验

批量打印前（创建任务装配时）校验可打印性：

- 命中停用 SKU → 拒绝创建，返回 `PRINT_SKU_DISABLED`（409，printing 域错误码；details 携 `disabled_ids` 逐条列出）。前端弹窗逐条展示「{code}：商品已停用」并提供「仅打印可用」降级按钮（剔除停用后重新提交）。
- 可打印对象 >500 → 既有 `PRINT_TOO_MANY_DATA_IDS`（单任务 data_ids ≤500）照常拒绝，前端预先禁用。
- 语义依据：「商品已停用」为不可打印原因（任务约束）；若业务复核后允许停用 SKU 补打标签，撤销装配侧拒绝分支即可，协议与页面不受影响（现有「打印中心手输 ID 打停用 SKU」路径将从成功变为 409——行为变化点，已在 changelog 注明）。

---

## 10. 权限（零新增）

全部复用既有冻结权限点，不新增 qr:* 权限码：

```text
页面/菜单    sku:view                 基础资料组「商品二维码」菜单与页面
打印按钮     printing:task:create     标签打印/批量打印/重打
模板下拉     printing:template:list   打印配置弹窗模板选择
扫码识别     scanner:resolve:list     /api/scanner/resolve（既有口径不变）
```

前端 canAccess fail-closed，后端 RequirePermission 照旧（permission.md §5）。
