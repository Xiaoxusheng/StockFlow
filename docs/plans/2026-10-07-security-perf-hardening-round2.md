# 计划：安全与性能加固第二轮（建议项落地 + 三项设计题按推荐方案实施）

> 依据：2026-10-07《性能与安全渗透检查报告》38 条确认发现中未修复的 1 条 Medium + 17 条建议。
> 用户已确认按推荐方案继续（"继续做"）。前两轮修复（契约 17 处、安全性能 20 处）与并行会话的
> 验证码/登录页改造均在工作区未提交，本任务只做精准追加，禁止覆盖既有改动。

## 目标

把渗透检查报告中「建议」类 17 条里的 15 条落地，并按已推荐方案落地 3 项设计题：

1. **LED 取号全局串行化**（决策 1，方案 A 号段预取）：docnum 支持规则级号段预取，LED 规则启用；
   LED 流水号允许跳号（业务唯一性不变），文档同步。
2. **admin 定向锁定 DoS**（决策 2，方案 A）：admin 豁免用户名维度锁定（IP 限流保留），
   锁定事件写审计；JWT secret 强度校验；loginGuard Fail 原子化。
3. **幂等占用行滞留**（决策 3，方案 A 惰性回收）：PROCESSING 超 TTL（1h）原子抢占接管。

## 做什么（按并行分区，文件所有权互斥）

| 分区 | 内容 | 文件所有权 |
| --- | --- | --- |
| A 认证与配置 | JWT secret≥32 字节+字符类校验；loginGuard Fail 用 Lua 原子 INCR+EXPIRE；admin 豁免名单（默认 admin，可配）+ 锁定事件审计；安全响应头中间件（nosniff/X-Frame-Options/Referrer-Policy）；/api/devices/activate 挂 IP 限流（独立配置项，默认 30/min）；server.public_base_url 固定激活二维码 Host | internal/auth/**、internal/config/**、cmd/server/**、internal/middleware/secure_headers.go（新增）、internal/router/router.go、internal/devices/handler.go、config.example.yaml |
| B datax | file_name ILIKE keyword 通配符转义；上传文件名按 255 字节截断（UTF-8 安全） | internal/datax/** |
| C reports | 库存预警 keyword ILIKE 通配符转义 | internal/reports/** |
| D 幂等与队列 | idempotency PROCESSING 超 TTL（1h 常量）原子抢占接管；asynq 入队显式 Timeout（默认 2h） | internal/idempotency/**、internal/asynqx/**（不动 internal/config） |
| E 库存核心 | LED 号段预取（docnum）；ReleaseLock 与 Deduct 统一锁序（inventory→locks）；23505 同事务重试改 SAVEPOINT；序列号读侧 FOR UPDATE + updateSerial 状态守卫 | internal/inventory/**、internal/docnum/**、docs/inventory-rules.md |
| F 批量化与索引 | 调拨/盘点/销售明细逐行 INSERT 改多值 INSERT...RETURNING；000026 迁移：operation_logs(username,ip) trgm GIN 索引 | internal/stockops/repo_gorm.go、internal/sales/repository.go、db/migrations/000026_*、docs/database.md |
| G 前端 | fetchBatchOptions queryKey 规范化（集合排序 canonical 化 + 公共 key 工厂） | web/src/api/options.ts 及其消费页 |

## 不做什么

- 不做黑盒渗透、不搭压测；不引入新第三方依赖；不做大版本依赖升级。
- 不动 LED 以外取号规则的语义；不重构 docnum 对外 API（调用方零改动为前提）。
- 不改 CSP/HSTS（属前端页面策略与反代层，文档说明即可）。
- 不提交 git（等待用户确认，与既有未提交改动一并处理）。
- 不动工作区中并行会话的验证码/登录页改动，遇到同文件冲突只做函数级精准修改。

## 测试与验收

- 各分区：包级 `go test`（涉及并发加 `-race`）；前端 tsc/eslint/node --test。
- 汇总门禁：`go build ./... && go vet ./... && go test ./...` + `tsc -b` + eslint + node --test + vite build。
- 验收：门禁全绿；18 项均有对应实现或明确记录的遗留说明；docs（inventory-rules/database/config 示例）同步。

## 风险

- E 区涉及库存核心路径（锁序、SAVEPOINT、号段），靠全量测试 + -race 兜底；若方案与代码结构冲突，
  agent 停止并上报，不硬改。
- 多值 INSERT 改写涉及 RETURNING 顺序假设，依赖 PG「按序返回」语义（项目基线 PG15）。
- admin 豁免扩大 admin 在线爆破面（IP 限流兜底），已在决策清单中说明。
