# StockFlow 本地开发环境（Windows 便携版）运维手册

> 2026-10-04 搭建并全程实测。零安装：不动注册表、不开系统服务、无需管理员权限，
> 全部落在 `D:/StockFlow/.local-env/`（已加入 .gitignore）。适配 Git Bash（MINGW64）。

## 1. 组成与来源

| 组件 | 版本 | 来源（本次实测可下载） | 本地落位 |
|---|---|---|---|
| PostgreSQL | 16.10 | `https://get.enterprisedb.com/postgresql/postgresql-16.10-1-windows-x64-binaries.zip`（EDB 官方 binaries，322,530,154 字节） | `.local-env/pgsql/`（zip 内 `pgsql/` 原样解压） |
| Redis | 5.0.14.1 | `https://github.com/tporadowski/redis/releases/download/v5.0.14.1/Redis-x64-5.0.14.1.zip` | `.local-env/redis/` |
| 后端可执行 | go1.27.0 构建 | `go build -o .local-env/server.exe ./cmd/server` | `.local-env/server.exe` |

数据与日志（均可删、可重建）：

```text
.local-env/pgdata/     PG 数据目录（initdb 产物）
.local-env/pglogs/postgresql.log
.local-env/redis/redis.conf / redis.log / data/
.local-env/logs/server-boot.log   后端启动日志
.local-env/downloads/             原始 zip（保留可离线重建）
```

## 2. 启动 / 停止（最短序列）

```bash
cd /d/StockFlow

# ① 起 PG（5432）
.local-env/pgsql/bin/pg_ctl.exe -D .local-env/pgdata -l .local-env/pglogs/postgresql.log -o "-p 5432" start
# 停：.local-env/pgsql/bin/pg_ctl.exe -D .local-env/pgdata stop

# ② 起 Redis（6379）
.local-env/redis/redis-server.exe .local-env/redis/redis.conf &

# ③ 起后端（8080）
SF_SERVER_MODE=debug \
SF_AUTH_JWT_SECRET=stockflow-dev-jwt-secret-0123456789abcdef \
SF_ADMIN_INITIAL_PASSWORD='Dev#StockFlow2026' \
SF_DATABASE_PASSWORD=postgres \
./.local-env/server.exe > .local-env/logs/server-boot.log 2>&1 &
# 就绪探测：curl http://127.0.0.1:8080/ready  → {"code":0,...,"database":"UP","redis":"UP","storage":"UP"}

# ④ 起前端（5173，/api 代理到 8080）
cd web && npm install && npm run dev
```

 Redis 无优雅停止需求时：`taskkill //F //IM redis-server.exe`（Git Bash 双斜杠）；
 后端同理按 `netstat -ano | grep :8080` 查 PID 后 `taskkill //F //PID <pid>`。

## 3. 数据库初始化（全新机器按序执行一次）

```bash
cd /d/StockFlow

# 3.1 建库簇（UTF8 + trust；--locale=C 规避中文 Windows locale 与 UTF8 冲突）
.local-env/pgsql/bin/initdb.exe -U postgres -A trust -E UTF8 --locale=C -D .local-env/pgdata

# 3.2 起库、建业务库（第 2 节命令）
.local-env/pgsql/bin/createdb.exe -h 127.0.0.1 -p 5432 -U postgres stockflow

# 3.3 迁移到 000016（注意必须 -tags postgres，见 §6.1）
go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.20.1 \
  -path db/migrations \
  -database "postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable" up
# 查版本：同命令去 up 换 version，应输出 16

# 3.4 首启自举（空库必需；写 admin + 16 内置角色 + 292 权限点）
#     就是第 2 节 ③ 起后端；等 /ready 200 后 psql 验证：
.local-env/pgsql/bin/psql.exe -At -d "postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable" \
  -c "SELECT count(*) FROM roles WHERE is_system=TRUE"   # 应为 16

# 3.5 演示数据（仅开发库；前置：3.3 迁移 + 3.4 自举已完成）
.local-env/pgsql/bin/psql.exe -d "postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable" \
  -v is_dev=1 -f db/seed/dev_seed.sql
.local-env/pgsql/bin/psql.exe -d "postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable" \
  -f db/seed/dev_seed_verify.sql    # 断言全过则末尾打印演示集摘要，退出码 0
```

## 4. 管理员与测试账号（仅本地开发库）

| 账号 | 密码 | 说明 |
|---|---|---|
| `admin` | `Dev#StockFlow2026` | 首启自举创建，绑 super_admin；登录实测 200（`POST /api/auth/login`）。**仅本地 trust 库，严禁用于任何部署环境** |
| `dev_manager` / `dev_receiver` / `dev_shipper` / `dev_stocktaker` / `dev_viewer` | 见 `db/seed/dev_seed.sql` | 演示测试账号（仓库经理/收货员/发货员/盘点员/财务查看），绑不同仓库可验 SPECIFIED_WAREHOUSE 数据权限 |

环境变量约定（`internal/config/config.go:183-196`：`SF_` + 键路径大写；auth 域独立解析
`SF_AUTH_JWT_SECRET`，见 `internal/auth/config.go:39`）：

- `SF_SERVER_MODE=debug`（默认 release，debug 免 JWT 密钥强校验但仍显式提供）
- `SF_AUTH_JWT_SECRET` ≥16 字节的固定开发串
- `SF_ADMIN_INITIAL_PASSWORD`：空库首启必需，≥12 位且大小写/数字/符号四类占三类
  （校验在 `internal/database/seed.go` validateAdminInitialPassword）
- `SF_DATABASE_PASSWORD=postgres`、`SF_DATABASE_PORT=5432`、`SF_REDIS_ADDR=127.0.0.1:6379`
- 不写 `config.yaml`（main.go 对默认路径缺失自动降级"默认值+环境变量"），密钥不落盘

## 5. CI 门禁（无 make）

```bash
bash scripts/ci-local.sh   # fmt-check → vet → vet -tags integration → test → build
                           # → guard-docnum/inventory/asynq/readonly/datax/devices
                           # → guard-status（只报告不失败）；任一失败即非零退出
```

## 6. 常见问题（本次实测踩过的坑）

1. **`go run github.com/golang-migrate/migrate/...` 报 `unknown driver postgres (forgotten import?)`**
   v4.20.1 的 CLI 驱动注册全靠构建标签（`internal/cli/build_postgres.go` 首行 `//go:build postgres`）。
   解决：加 `-tags postgres`（本手册所有 migrate 命令已带）。
2. **psql 选项被忽略（`warning: extra command-line argument ... ignored`）**
   Windows 版 psql 的 getopt 在第一个位置参数（连接串）处停止解析——把连接串写在其后
   全部失效且**退出码仍为 0（假成功）**。解决：全选项形式，连接串放 `-d` 并置于
   `-v/-f/-c` 之后或之前皆可，唯独不要裸放第一个参数：
   `psql -At -d "postgres://..." -v is_dev=1 -f db/seed/dev_seed.sql`（注意与 Makefile
   `seed-demo` 目标的 `psql "$(PG_URL)" -v ...` 写法不同，后者在本机 psql 下会假成功）。
3. **EDB 下载尾部停滞**：curl 直连在最后 ~15MB 处可能挂死。解决：`curl -L -C - -o <file> <url>`
   断点续传（HTTP 206 秒补完），下载后 `unzip -t` 校验。
4. **initdb 报 encoding UTF8 与 locale 冲突**：中文 Windows 默认 locale 不兼容 UTF8。
   解决：加 `--locale=C`（数据为 UTF8，排序规则 C，开发库无碍）。
5. **端口被占**：5432 → pg_ctl 加 `-o "-p 5433"` 并同步 `SF_DATABASE_PORT=5433` 与
   迁移/种子连接串；6379 → `.local-env/redis/redis.conf` 改 `port 6380` 并同步
   `SF_REDIS_ADDR=127.0.0.1:6380`。本次 5432/6379 均空闲未启用备选端口。
6. **空库自举失败 `printing:task:list 父级 printing:task 未就绪`**：
   已修复（`internal/database/seed.go` buildPermissionSeeds 只给有菜单叶子的资源动作点
   挂父级；`printing:task`/`scanner:resolve`/`system:backup` 按冻结设计无独立菜单叶子）。
   修复前老库不炸（users 非空跳过种子），仅全新库必炸——旧开发库重装环境前请先拉最新代码。
7. **`/ready` 非 200**：按 `data` 里的 database/redis/storage 三项定位；storage UP 要求
   `./data/files` 可写（后端工作目录必须是仓库根）。
8. **server.exe 启动即退**：查 `.local-env/logs/server-boot.log`。空库缺
   `SF_ADMIN_INITIAL_PASSWORD`、密码弱于三类字符，或 PG/Redis 未起均会 fail-fast（deployment.md §3）。

## 7. 从零重建整个环境（一条龙速查）

```bash
cd /d/StockFlow
curl -L -C - -o .local-env/downloads/postgresql-16.10-1-windows-x64-binaries.zip \
  https://get.enterprisedb.com/postgresql/postgresql-16.10-1-windows-x64-binaries.zip
curl -L -C - -o .local-env/downloads/Redis-x64-5.0.14.1.zip \
  https://github.com/tporadowski/redis/releases/download/v5.0.14.1/Redis-x64-5.0.14.1.zip
unzip -q .local-env/downloads/postgresql-16.10-1-windows-x64-binaries.zip -d .local-env
mkdir -p .local-env/redis && unzip -q .local-env/downloads/Redis-x64-5.0.14.1.zip -d .local-env/redis
# 之后按 §3 → §2 顺序执行（3.1 initdb 起）
```
