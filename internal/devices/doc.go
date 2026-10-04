// Package devices 设备管理与统一扫码解析域（backend-m3-plan §2.1 scope D）：
// 设备注册/二维码激活/绑定/心跳/配置下发/App 版本查询/设备日志 + POST /api/scanner/resolve
// 统一解析与 scan_logs 扫码审计。
//
// 业务依据（docs/ 唯一真相来源）：
//   - devices.md §6 设备生命周期（注册 → 激活二维码 → 激活绑定仓库 → 心跳 → 共享设备）；
//   - devices.md §7 设备管理后台（列表/详情/停用/配置下发 §7.3/App 版本 §7.4/健康监控 §7.2）；
//   - devices.md §13.2 扫码审计：谁、在哪台设备、什么时候、扫了什么、执行了什么（scan_logs）；
//   - scanner.md §5 统一条码解析（解析与业务执行分离——resolve 只识别不执行业务）；
//   - scanner.md §6.6 扫码幂等：极短时间内完全相同事件按重复事件去重；正常连续扫码允许重复；
//   - backend-m3-plan §8 设备管理与统一扫码解析（端点/激活协议/匹配器管线顺序冻结）。
//
// 设备令牌（plan §8.2 冻结，与用户 JWT 并行的最小设备凭证）：
//   - JWT HS256，claims did/code/type/token_version，TTL 365 天，issuer=stockflow-devices；
//   - 无刷新端点：失效语义由 token_version 承担——停用/解绑/重新生成激活码即 +1 全量失效；
//     DeviceAuthRequired 每次请求查 DB（status=ENABLED AND activated AND token_version 匹配）；
//   - Scope 限制：设备令牌仅可访问只读 + 作业上报类端点（activate/heartbeat/self/config/
//     self/logs/app 版本查询/scanner resolve），管理端点一律用户 JWT + RequirePermission。
//
// 激活协议（plan §8.2）：二维码内容 = {"server_url","device_code","token"} JSON；
// token 安全随机 32 字节、服务端仅存 SHA-256 哈希、15 分钟有效、单次消费——激活为
// 单条守卫 UPDATE（PENDING + 哈希匹配 + 未过期），防重放裁决；旧 token 消费后即失效。
//
// resolve 管线（plan §8.3 顺序冻结）：
//  1. 单据号（docnum frozenRules 业务前缀 15 值分派 4 域 DocFinder；LED/ADJ/IMP/EXP/PT 不可扫）
//  2. SKU 条码（masterdata barcodes 唯一索引）
//  3. 库位码（warehouse bins，多仓命中返回列表）
//  4. 序列号（inventory serial_numbers 唯一命中）
//  5. 批次码（inventory batches，跨 SKU 命中返回列表）
//  6. 全未命中 → UNKNOWN_BARCODE；命中类型但对象不存在/停用 → *_NOT_FOUND。
//
// 只识别不执行（scanner.md §5.3 硬性约束）：resolve 无业务事务、不改任何库存/单据状态；
// 业务执行走 M2 既有各域业务 API。重复扫码去重属业务 API 幂等体系；本域按 ask 交付
// §6.6 重复事件去重窗口（DedupWindow，同码+同页面+同上下文极短窗口内标记 duplicate），
// 仅作响应注记不抑制识别——识别结果恒完整，业务幂等准绳仍在业务 API（plan §8.3 口径，
// 偏离记录见 DomainResult）。
//
// scan_logs 审计（plan §8.3）：resolve 命中或失败均写 scan_logs；写失败记 error 日志并
// 降级放行（resolve 为只读识别非业务事务）；设备端调用经 DeviceAuthRequired 携带设备上下文，
// PC Web HID 以用户 JWT 调用、device_id/device_code 落 NULL 以 user_id/username/ip 归因。
//
// 跨域（plan §12.2 消费方窄接口 + router 注入，域包之间禁止 import）：
// 本包定义 SKUBarcodeReader/BinCodeReader/DocFinder/SerialReader/BatchReader 五个解析
// 窄接口（ports.go），实现落各域 devices_resolve.go（masterdata/warehouse/inventory/
// purchase/sales/stockops/returns）；仓库存在性校验复用既有 auth.WarehouseChecker 载体
// （warehouse.NewChecker 实现）。除 devices_resolve.go 冻结清单外不改任何域包文件。
//
// 装配（plan §12.1）：RegisterRoutes(protected, db, rdb, opts...) 挂管理端与扫码日志
// 路由（用户 JWT）；设备端路由（activate/heartbeat/self/*、app 版本、resolve）挂
// WithDeviceAPI 注入的组——该组必须不含 auth.AuthRequired（设备令牌与用户 JWT 并行，
// 设备端点自带 DeviceAuthRequired，activate 以一次性激活码即凭证）。缺省未注入启动期
// panic fail-fast（plan §3.1 规则①）。本域无生成单号（设备编码为管理端命名，000013
// DDL 注），不经 docnum；无异步任务，不经 asynqx。
package devices
