# StockFlow 部署与运维规范

> 版本：v1.3 ｜ 关联文档：[database](database.md)、[architecture](architecture.md)、[permission](permission.md)
>
> v1.3（2026-10-04 阶段 20 CI 关卡）：§7 补 CI 承载说明——新增 `.github/workflows/ci.yml`（push/pull_request 触发），lint/test/guards 三并行 job 与 §7.1 第 1 项 `make ci` 同源；go test -race 关卡由 ubuntu-latest（自带 gcc）承载。既有章节编号保持不变。
>
> v1.2（2026-10-03 Docker Compose 单机部署，changelog 同日条目）：新增 §10 Docker Compose 部署（单机，目标目录 /home/stockflow）——后端多阶段 Dockerfile、postgres + redis + migrate + app 编排（可选 web profile）、一次性 migrate 容器与 §7 发布纪律对齐。
>
> v1.1（2026-10-03 安全修复轮）：新增 §1.1 环境变量清单、§2.1 grants 执行纪律、§7.1 发布检查单、§9 反向代理与网络安全基线；§3/§6 与实现对齐。既有章节编号保持不变（多文档/代码注释按节号引用）。

---

## 1. 环境规划

项目必须区分三个环境：

```text
开发环境（dev）
测试环境（test）
生产环境（prod）
```

配置不能写死，采用：环境变量 + 配置文件 + Secret。

实现落位：internal/config（viper）——优先级 **环境变量 > 配置文件 > 内置默认值**；环境变量名由配置键路径推导（`.` → `_` 加 `SF_` 前缀大写，如 `database.password → SF_DATABASE_PASSWORD`）。启动完整性校验失败快速报错退出，禁止带病启动（§3）。

配置必须区分：

```text
DB（数据库连接）
Redis（缓存）
文件存储（本地/OSS）
JWT（密钥、有效期）
日志（级别、路径）
服务端口
```

**生产环境密钥（数据库/Redis 密码、JWT 密钥、初始管理员密码）一律经环境变量/Secret 注入，禁止写入 config.yaml 提交仓库**（config.yaml 已被 .gitignore 排除，样例见 [config.example.yaml](../config.example.yaml)）。

### 1.1 环境变量清单

以代码实现为准（internal/config/config.go `defaults()`、internal/auth/config.go 环境变量常量、internal/database/seed.go 密码策略），2026-10-03 安全修复轮新增项已含。

**核心安全与部署项：**

| 环境变量 | 作用 | 默认值 | 是否必填 | 生成方式 / 取值说明 |
|---|---|---|---|---|
| `SF_AUTH_JWT_SECRET` | Access JWT 的 HS256 签名密钥（internal/auth/config.go，长度 ≥16 字节，过短拒绝启动） | 无——release 模式缺失即启动失败；debug/test 缺失时降级进程内随机密钥（重启全部 Token 失效，仅限本地冒烟） | **release 必填** | `openssl rand -base64 32`；经 Secret 注入，不入仓库；轮换会使全部会话/Token 失效 |
| `SF_ADMIN_INITIAL_PASSWORD` | 空库首次启动时默认管理员（admin）的初始密码（internal/database/seed.go） | 无——需要创建管理员而缺失或弱密码时启动失败；已初始化的库不消费该值 | **空库首启必填** | ≥12 位且至少含 大写字母/小写字母/数字/符号 中三类（2026-10-03 由"≥8 位字母+数字"收紧）；建议随机生成；绝不写入日志/配置文件；首登强制改密（§6） |
| `SF_SERVER_MODE` | Gin 运行模式 debug/release/test（透传 gin.SetMode） | `release`（2026-10-03 由 debug 收紧；本地开发请显式设 `SF_SERVER_MODE=debug`） | **生产必填 release** | release 下生产校验生效：JWT 密钥缺失 fail-fast、`sslmode=disable` 启动告警等 |
| `SF_DATABASE_PASSWORD` | PostgreSQL 业务账号密码 | 空 | **生产必填** | DBA 分发的最小权限业务账号密码（禁止用超级用户跑应用，§2.1）；Secret 注入 |
| `SF_REDIS_PASSWORD` | Redis 密码（会话、权限缓存、限流/锁定计数键的存储） | 空 | 生产必填（Redis 开启时） | 与 Redis `requirepass` 一致；Secret 注入 |
| `SF_DATABASE_SSLMODE` | PostgreSQL 连接 SSL 模式 | `disable` | 生产必填取值 | 生产建议 `verify-full`（证书 + 主机名校验）；release 模式下 `disable` 启动告警不阻断（同机 loopback 场景可按部署规范调整） |
| `SF_CORS_ALLOWED_ORIGINS` | CORS 白名单来源（逗号分隔，列表类配置特例） | `http://localhost:5173` | **生产必填** | 生产前端来源，形如 `https://wms.example.com,https://pad.example.com`；含非法来源启动失败 |
| `SF_SERVER_TRUSTED_PROXIES` | 受信任反向代理网段（逗号分隔 CIDR），gin `SetTrustedProxies` | 空 = 不信任任何代理，客户端 IP 一律取直连 RemoteAddr | 使用反代时必填 | 声明反代所在网段（同机反代含 `127.0.0.1/32`），如 `127.0.0.1/32,10.0.0.0/8`；必须与反代**覆写** X-Forwarded-For 配套（§9），否则审计 IP 失真或可被伪造 |
| `SF_SERVER_MAX_BODY_BYTES` | 请求体大小上限（字节，http.MaxBytesReader 全局中间件） | `1048576`（1MB） | 可选 | 超限返回 413；Excel 导入等大包上传如超限按需调大并评估滥用风险 |
| `SF_AUTH_RATE_LIMIT_IP_PER_MINUTE` | /api/auth 公开组 IP 维度滑动窗口限流（次/分钟，Redis） | `30` | 可选 | Redis 故障时 fail-open 并记 error 日志（可用性优先，与登录锁定的取舍一致） |

**认证既有可调项（默认即 backend-m1-plan §7.2/§7.3 冻结值，一般无需改动）：**

| 环境变量 | 作用 | 默认值 | 是否必填 | 格式 |
|---|---|---|---|---|
| `SF_AUTH_ACCESS_TTL` | Access JWT 有效期 | `2h` | 可选 | Go duration，正时长（如 `2h`） |
| `SF_AUTH_REFRESH_TTL` | Refresh Token / Redis 会话滑动 TTL | `168h`（7d） | 可选 | Go duration，正时长 |
| `SF_AUTH_MAX_LOGIN_FAILURES` | 连续登录失败锁定阈值（username+IP 双键计数，任一键达阈即锁） | `5` | 可选 | 整数 ≥1 |
| `SF_AUTH_LOCK_DURATION` | 登录锁定时长 | `15m` | 可选 | Go duration，正时长 |

**其余配置项**（默认值与 config.example.yaml 一致，按环境覆盖）：`SF_SERVER_PORT`（8080，与前端 Vite 代理对齐）、`SF_SERVER_READ_TIMEOUT`/`SF_SERVER_WRITE_TIMEOUT`（15s）、`SF_SERVER_SHUTDOWN_TIMEOUT`（10s）、`SF_LOG_LEVEL`（info）、`SF_LOG_FORMAT`（json）、`SF_DATABASE_HOST`（127.0.0.1）、`SF_DATABASE_PORT`（5432）、`SF_DATABASE_USER`（postgres）、`SF_DATABASE_NAME`（stockflow）、`SF_DATABASE_AUTO_MIGRATE`（false，**生产禁止 true**，§2.1）、连接池 `SF_DATABASE_MAX_OPEN_CONNS`（50）/`SF_DATABASE_MAX_IDLE_CONNS`（10）/`SF_DATABASE_CONN_MAX_LIFETIME`（1h）/`SF_DATABASE_CONN_MAX_IDLE_TIME`（10m）、`SF_REDIS_ENABLED`（true）/`SF_REDIS_ADDR`（127.0.0.1:6379）/`SF_REDIS_DB`（0）/`SF_REDIS_POOL_SIZE`（50）/`SF_REDIS_MIN_IDLE_CONNS`（5）。

> 新增/改名配置项必须三处同步：internal/config 结构体字段 + `defaults()` + config.example.yaml（config.go 包注释约定）；auth 域独立解析的 `SF_AUTH_*` 常量见 internal/auth/config.go（auth 不 import config）。

---

## 2. 数据库迁移

必须有 Migration 机制：

```text
禁止手工 CREATE TABLE
上线数据库变化必须能够：迁移、回滚/兼容
```

流程见 database.md §9。生产由发布流程显式执行迁移（`make migrate-up`，回滚预案 `make migrate-down`）；**生产环境 `SF_DATABASE_AUTO_MIGRATE` 必须为 false**（禁止随启动隐式迁移，§2.1）。

### 2.1 审计表账号权限分层（grants）执行纪律

库存流水（inventory_ledgers）、关键操作日志（operation_logs）、登录日志（login_logs）属审计数据（database.md §7：可查询、可追溯、不可随意篡改）。除应用层不提供 UPDATE/DELETE 接口外，数据库侧再做一层硬约束：**业务运行账号对审计表只有 SELECT + INSERT，无 UPDATE/DELETE/TRUNCATE**（双保险）。脚本：[db/grants/app_grants.sql](../db/grants/app_grants.sql)。

**执行步骤（生产首次部署前，由 DBA 以超级用户操作）：**

1. 创建最小权限业务运行账号（密码走 Secret，禁止入库文档/仓库）：
   ```sql
   CREATE USER stockflow_app WITH PASSWORD '<Secret 注入的密码>';
   ```
2. 先执行迁移（表就绪），再以超级用户执行 grants 脚本：
   ```bash
   psql -d stockflow -v app_user="stockflow_app" -f db/grants/app_grants.sql
   ```
   脚本自带 `\set ON_ERROR_STOP on`，任一语句失败即停。
3. 生效验证（以业务账号连接）：对 `inventory_ledgers` 尝试 `UPDATE`/`DELETE` 应被 permission denied 拒绝，`SELECT`/`INSERT` 正常。

**纪律：**

- **新审计表须人工重跑 grants**：`ALTER DEFAULT PRIVILEGES` 无法按"表是否审计表"区分，M2+ 新增审计类表（如审批记录）时，必须同步把表名追加进 app_grants.sql 的 REVOKE/GRANT 清单，并在该迁移于生产执行后**人工重跑本脚本**，列入发布检查单（§7.1 第 4 项）。
- 同理，`GRANT ... ON ALL TABLES` 仅覆盖执行时已存在的表——每次引入新表的迁移在生产执行后都应重跑本脚本（新审计表先补清单），否则业务账号对新表无 DML 权限。
- **生产禁 `auto_migrate=true`**：迁移由发布流程显式执行（先备份、含回滚预案，§7）；启动期隐式迁移绕过了备份与评审环节。
- 开发环境使用属主账号可跳过本脚本，不影响迁移验证（backend-m1-plan §6.4）。
- 指向勘误：app_grants.sql 尾注"见 deployment.md §4"为历史笔误（§4 实为备份与恢复章节），正确指向本节（§2.1）与 §7.1 发布检查单；db/grants 文件侧的尾注修正随该文件下一次变更同步。

---

## 3. 健康检查

提供两个端点用于生产部署：

```text
/health   存活检查（服务进程正常）
/ready    就绪检查（依赖正常）
```

`/ready` 检查项（M1 实现，internal/health）：数据库、Redis（`redis.enabled=false` 时计 DISABLED 不阻塞就绪）；文件系统检查随阶段 14 文件中心引入（api.md §8 偏差已挂账 backend-m1-plan §12）。就绪结果带 1 秒本地缓存，避免探针高频穿透真实依赖探测。

用途：负载均衡探针、容器编排探针、发布验证。

两探针免认证。**建议仅对负载均衡器/内网网段放行，不暴露公网**（nginx 放行示例见 §9）。

另需支持：启动检查（启动时验证配置完整性与依赖连通性，失败快速报错退出，禁止带病启动）——已实现于 internal/config.Validate 与各依赖初始化的 fail-fast。

---

## 4. 备份与恢复

真正投入生产必须考虑数据库安全。

### 4.1 备份

```text
数据库备份
备份记录（备份时间、备份大小、备份状态）
支持后台执行备份（定时 + 手动触发）
```

### 4.2 恢复

必须提供**数据恢复方案**（恢复步骤文档 + 演练验证）。

**不能只在 README 中写"建议用户自己备份"**。

---

## 5. 系统监控

增加**系统监控**页面，展示：

```text
CPU、内存、磁盘
数据库连接、API 请求、错误率
运行时间、队列任务、定时任务
```

监控至少能够发现：

```text
服务挂了
数据库连接异常
磁盘快满
后台任务失败
```

### 5.1 告警

重要异常应能够告警：

```text
数据库连接失败
磁盘空间不足
后台任务失败
导入失败、导出失败
库存异常
服务异常
```

告警渠道接入通知中心（站内），可扩展邮件/Webhook。

---

## 6. 初始化与演示数据

遵循 database.md §8：

- 生产**首次启动（users 表为空）**执行安全初始化：16 内置角色（permission.md §1，is_system 禁删）、M1 全量权限点与默认菜单、默认管理员（admin，绑定 super_admin）、默认仓库示例（WH-DEFAULT）。幂等（ON CONFLICT DO NOTHING，重复启动不覆盖任何已有数据）+ 事务级 advisory lock 防多副本并发首启重复种子。
- 默认管理员初始密码经 `SF_ADMIN_INITIAL_PASSWORD` 注入：**空库首启必填**，策略 **≥12 位且至少含大小写字母/数字/符号中三类**（2026-10-03 收紧，原 ≥8 位字母+数字）；缺失或弱密码启动直接失败（§3 禁止带病启动）；密码与哈希绝不写入日志（architecture.md §6 日志红线）。
- **必做步骤（发布检查单 §7.1 第 6 项）**：首次启动后**立即以 admin 登录完成改密**（系统以 `must_change_password=true` 强制改密门禁兜底）；**公网放行前必须确认初始密码已改**——注入窗口期内 admin 持超级管理员权限，改密完成前不得将服务暴露公网。
- 演示数据与生产初始化逻辑完全分离：`db/seed/dev_seed.sql` + `make seed-demo`（强制 `SF_ENV=dev` 门禁 + SQL 内 is_dev 门禁双保险），生产环境不可注入。
- 禁止每次启动重置业务数据。

---

## 7. 发布流程

```text
代码合并 → CI（构建 + 测试）→ 测试环境部署验证
→ 生产部署（低峰期）：
   1. 数据库备份
   2. 执行 Migration（含回滚预案）
   3. 滚动更新服务
   4. /ready 验证
   5. 核心链路冒烟（登录、入库、出库、库存查询）
   6. 观察错误率与关键日志
```

CI 由 GitHub Actions 承载（`.github/workflows/ci.yml`，阶段 20）：push 与 pull_request 触发，三个并行 job 与检查单第 1 项 `make ci` 同源——**lint**（gofmt -l cmd internal 有输出即失败 + go vet ./... + go vet -tags integration ./...）、**test**（go test -count=1 ./... + go test -race -count=1 ./...，race 由 ubuntu-latest 自带 gcc 承载）、**guards**（Makefile 七守卫目标，命令单一来源在 Makefile，与 backend-m1-plan §8.6 清偿轮 F3 接线一致）；Go 版本读 go.mod，go mod download 经 setup-go 缓存。

### 7.1 发布检查单

生产发布逐项勾验（2026-10-03 新增）：

1. **门禁全绿**：仓库根 `make ci`（gofmt / go vet / go build / go test）。
2. **数据库备份**已完成且恢复方案可用（§4）。
3. **迁移**由发布流程显式执行：`make migrate-up`（回滚预案 `make migrate-down`）；生产 `SF_DATABASE_AUTO_MIGRATE=false`（§2.1）。
4. **grants**：本次迁移若新建审计类表，db/grants/app_grants.sql 已同步补表名并在迁移执行后重跑（§2.1）。
5. **环境变量**按 §1.1 清单逐项核对：`SF_AUTH_JWT_SECRET`（`openssl rand -base64 32`）、`SF_SERVER_MODE=release`、`SF_DATABASE_PASSWORD`、`SF_REDIS_PASSWORD`、`SF_DATABASE_SSLMODE=verify-full`、`SF_CORS_ALLOWED_ORIGINS`（生产域名）、`SF_SERVER_TRUSTED_PROXIES`（反代网段）、`SF_SERVER_MAX_BODY_BYTES`、`SF_AUTH_RATE_LIMIT_IP_PER_MINUTE`；空库首启另需 `SF_ADMIN_INITIAL_PASSWORD`。
6. **首启场景**（§6）：`SF_ADMIN_INITIAL_PASSWORD` 已注入 → 服务启动 → 立即登录改密 → **公网放行前确认已改密**。
7. **反代与网络**（§9）：nginx 已覆写 X-Forwarded-For；`SF_SERVER_TRUSTED_PROXIES` 与实际反代网段一致；8080 仅内网可达；HTTPS 已启用（TLS 由反代终结）。
8. **探针**：`/health` `/ready` 仅对 LB/内网放行（§9）；`/ready` 返回 UP。
9. **核心链路冒烟**：登录、入库、出库、库存查询。
10. **观察**错误率与关键日志（含登录限流/锁定的 error 级日志）。

---

## 8. 运行保障

- 日志持久化并分类（architecture.md §8），按策略清理。
- 导出/打印等临时文件按有效期自动清理（excel.md §5）。
- 定时任务失败重试与告警（architecture.md §9）。
- 服务重启后数据不丢失：一切业务状态落库，禁止仅存内存。

---

## 9. 反向代理与网络安全基线

2026-10-03 新增（安全修复轮 S5/S12/S13 的部署侧配套，公网部署必须满足）：

- **HTTPS 由反代终结**：TLS 证书配置在 nginx（或同层网关），后端在内网以明文 HTTP 供反代回源；禁止用户直连后端端口。
- **8080 只绑内网**：服务监听 `:8080`（cmd/server/main.go 以 `:<port>` 绑定全部网卡，进程无绑定地址参数）——必须以防火墙/安全组将 8080 限制为**仅反代所在网段可达**，禁止公网直连。
- **X-Forwarded-For 覆写（非追加）**：反代必须 `proxy_set_header X-Forwarded-For $remote_addr;`。`$proxy_add_x_forwarded_for`（追加）会保留客户端可伪造的伪造链——污染 login_logs/operation_logs 审计 IP 并绕过 IP 维度限流。
- **`SF_SERVER_TRUSTED_PROXIES` 声明反代网段**：gin 按该清单自 XFF 右侧剥离可信代理取真实客户端 IP；默认空 = 不信任任何代理（客户端 IP 取直连 RemoteAddr）。声明错误的后果：漏声明 → 审计 IP 全为反代地址；声明过宽 → 客户端可伪造 XFF。多级代理逐级声明。
- **`/health` `/ready` 仅对 LB/内网放行**（两探针免认证，防探测与滥用，§3）。
- **公网暴露面第一道闸**：请求体上限 `SF_SERVER_MAX_BODY_BYTES`（默认 1MB，超限 413）+ /api/auth 公开组 IP 限流 `SF_AUTH_RATE_LIMIT_IP_PER_MINUTE`（默认 30/min）；反代层可再叠加连接数/速率限制。

nginx 最小示例（`SF_SERVER_TRUSTED_PROXIES=127.0.0.1/32` 同机回源；跨机反代追加其网段如 `,10.0.0.0/8`）：

```nginx
server {
    listen 443 ssl;
    server_name wms.example.com;
    # ssl_certificate / ssl_certificate_key 此处略（HTTPS 由反代终结）

    location / {
        proxy_set_header Host            $host;
        proxy_set_header X-Real-IP       $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;   # 覆写；禁止 $proxy_add_x_forwarded_for（追加=可伪造）
        proxy_pass http://127.0.0.1:8080;
    }

    # 探针仅对 LB/内网网段放行（网段按实际 LB 替换）
    location ~ ^/(health|ready)$ {
        allow 10.0.0.0/8;
        deny   all;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

## 10. Docker Compose 部署（单机，目标目录 /home/stockflow）

仓库根自带完整部署物：`Dockerfile`（后端多阶段构建）、`docker-compose.yml`（postgres + redis + migrate + app，可选 `web` profile）、`.env.example`、`web/Dockerfile` + `web/nginx.conf`（前端静态托管 + /api 反代）。迁移由一次性 `migrate` 容器（migrate/migrate v4.20.1，与 Makefile 固定版本一致）显式执行，应用默认 `SF_DATABASE_AUTO_MIGRATE=false`，符合 §7 发布纪律。

文件中心存储根（`storage.root` 默认 `./data/files`，容器内即 `/app/data/files`）：镜像内 `/app/data` 已按运行用户 `app` 属主预建，compose 以命名卷 `filesdata:/app/data` 持久化（含 sysops 备份物理文件）。如改用宿主机 bind mount，需先 `chown` 为镜像内 `app` 用户（alpine `adduser -S app`，UID 100/GID 101）再启动。

### 10.1 前置条件

- 服务器：Linux x86_64，Docker Engine 26+ 与 Compose v2（`docker compose version` 可用；未装可执行 `curl -fsSL https://get.docker.com | bash`）。
- 国内网络建议先配置镜像加速（`/etc/docker/daemon.json` 的 `registry-mirrors`），避免拉取 golang/postgres/redis 镜像缓慢。
- 上传整个仓库到 `/home/stockflow`（git clone 或 scp；`.dockerignore` 已排除无关内容，`web/node_modules` 不要上传）。

### 10.2 首次部署步骤

```bash
cd /home/stockflow
cp .env.example .env && vim .env   # 必填 4 项：POSTGRES_PASSWORD、REDIS_PASSWORD、
                                   # SF_AUTH_JWT_SECRET（openssl rand -base64 32）、
                                   # SF_ADMIN_INITIAL_PASSWORD（≥12 位三类字符）
docker compose build               # 构建后端镜像（首次需联网拉依赖）
docker compose up -d               # postgres/redis → migrate → app 依次就绪
docker compose ps                  # app 应为 healthy；migrate 为 Exit 0
curl -s http://127.0.0.1:8080/health
curl -s http://127.0.0.1:8080/ready
docker compose logs app | grep "启动初始化检查完成"   # 确认管理员已创建
```

随后**立即登录 admin 并修改初始密码**（§7.1 纪律），确认已改密后再放开公网访问。8080 默认只绑 `127.0.0.1`，对外服务经 §9 反向代理；同机启用前端容器时改为 `docker compose --profile web up -d`，并在 `.env` 设 `SF_SERVER_TRUSTED_PROXIES=172.16.0.0/12`，站点在 80 端口（`WEB_BIND`）。

### 10.3 日常运维

```bash
# 升级：上传新代码后
docker compose build && docker compose up -d    # migrate 容器自动补跑新迁移
# 备份（§4）：
docker compose exec postgres pg_dump -U stockflow stockflow | gzip > backup_$(date +%F).sql.gz
# 演示数据（database.md §8.2：仅开发库，严禁对生产库执行）：
docker compose exec -T postgres psql -U stockflow -d stockflow -v is_dev=1 -f - < db/seed/dev_seed.sql
docker compose exec -T postgres psql -U stockflow -d stockflow -f - < db/seed/dev_seed_verify.sql
```

### 10.4 常见问题

- **app 起不来**：`docker compose logs migrate` 看迁移是否失败（dirty 状态需人工修复后 `migrate force`）；`docker compose logs app` 看配置校验错误（必填环境变量缺失会快速失败，符合 §3）。
- **拉镜像慢**：配置 registry-mirrors，或先在本机构建导出：`docker save stockflow-server | gzip` 上传后 `docker load`。
- **改了 .env 不生效**：`docker compose up -d` 会重建受影响容器；`SF_ADMIN_INITIAL_PASSWORD` 仅空库首次启动生效，之后改密走登录接口。

