# StockFlow 后端 M1 实施方案（development-plan 阶段 3–8）

> 日期：2026-10-02 ｜ 范围：Go 后端里程碑 M1——数据库设计、基础设施、认证权限、基础资料、仓库管理、库存核心 ｜
> 依据：development-plan §1/§3/§5、architecture、api、database、permission、inventory-rules、business-flow、testing、deployment ｜
> 开工前分析见 [backend-analysis.md](backend-analysis.md)。本方案冻结 M1 的**包所有权契约、路由注册契约、迁移 DDL 契约、库存变更入口契约**，多工程师并行实现时以本文为合同，跨包修改视为违约（§4）。

---

## 1. 目标与验收

**M1 = development-plan §5**："基础平台 + 库存核心可用，库存恒等式与并发测试通过"。按 §3 九项 DoD 映射到 M1：

| DoD 项 | M1 交付 | 验证方式 |
|---|---|---|
| 数据库 | 5 组迁移 + 索引 + 恒等式 CHECK | migrate up/down 双向验证（database.md §9） |
| 后端 | 4 个业务域 Service/Repository 完整链路（architecture.md §1） | 单元 + 集成测试（机制注记：实际落地为 `//go:build integration` + SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控外部实例，testcontainers 为备选未落地——testing.md §3） |
| API | 各域接口 + swaggo OpenAPI + 后端完整校验（api.md §3/§4） | swag init + 接口测试 |
| 前端 | M1 范围 = 库存示范页（实时库存/库存流水）对接真实 API；**阶段 5–7 页面（用户/角色/权限、基础资料、仓库管理、F10 库位地图页）由前端任务流 F6/F7/F10 补齐——这是已确认的里程碑偏差，见 §13.5，纳入 M1 验收遗留项** | 页面走真实接口、无假数据（requirements.md §10） |
| 权限 | 全部 M1 接口挂 RequirePermission + 数据权限过滤（permission.md §2/§4） | 越权测试（testing.md §8） |
| 校验/错误处理 | 后端完整校验 + 统一错误码 `AUTH_*`/`MASTERDATA_*`/`WAREHOUSE_*`/`INVENTORY_*`/`COMMON_*` | 错误码响应测试 |
| 日志 | 访问日志 + 操作日志（经 §4.4 共享审计 helper 写 operation_logs）+ 登录日志（login_logs），敏感脱敏 | 日志断言 |
| 测试 | 单元/集成/并发/幂等/恒等式（testing.md §2、§4 的 M1 子集） | `go test -race` 全绿 |

**全局技术约定**（全 M1 统一，各域不得自行其是）：

- Go module：`github.com/stockflow/server`（**module 根 = StockFlow 仓库根**——T0 调整：脚本门禁在仓库根执行 `go build ./...`，Go 1.27 workspace 模式下裸 `./...` 不匹配子目录 module，故由原 server/ 目录上移并入根；import 路径为 module 相对路径，全部不变；若实际托管组织不同，必须在产生任何 import 之前确定，见 §13 风险）。
- Go 1.22+；PostgreSQL 15+；Redis 7.x。
- 服务端口默认 **8080**（与前端代理 `plans/2026-10-02-frontend-foundation.md §5` 对齐，回写 deployment.md 固化，见 §12）。
- ID：int64，JSON 序列化为**字符串**（防 JS 2^53 精度丢失；回写 api.md §2，见 §12）。
- 时间：`YYYY-MM-DD HH:mm:ss`（api.md §2），由 `internal/database` 的 `JSONTime` 统一 MarshalJSON。
- 金额/数量：`numeric(18,4)`（精确十进制，禁 float；数量恒等式比较因此无浮点误差）。
- 分页：请求 `page/pageSize`，响应 `{page,pageSize,total,items}`（api.md §2.1）；信封 `{code,message,data,request_id,details}`，成功 `code=0` 且无 details，失败 code 为模块命名空间字符串错误码、details 携带结构化补充（api.md §2.2 + architecture.md §2.1/§2.1 details + api.md §4 校验失败必须带 details）。

## 2. 目录结构与包职责（一句话/包）

```text
（仓库根 = module 根，T0 由 server/ 上移；树内路径均为 module 相对路径）
├── cmd/server/         main.go        进程装配与生命周期（配置→日志→依赖→启动检查→迁移→路由→优雅关闭），禁止业务逻辑
├── db/migrations/                     golang-migrate SQL 迁移（§6 划分，up/down 成对）
├── db/grants/          app_grants.sql 审计表账号权限分层脚本（database.md §7.2，生产执行）
├── db/seed/            dev_seed.sql   开发演示数据，仅手工执行（database.md §8.2，禁止生产）
├── internal/config/                   viper 配置加载：SF_ 环境变量 + yaml；启动完整性校验失败快速退出（deployment.md §3）
├── internal/logger/                   zap 分类日志（access/error/business），request_id 贯穿，密码/Token 脱敏（architecture.md §6/§8）
├── internal/response/                 统一信封 OK/OKPage/Err（含 details）、分页结构、模块错误码注册表（api.md §2/§4、architecture.md §2）
├── internal/middleware/               RequestID、访问日志、Recovery、CORS 白名单、共享审计 helper（operation_logs/login_logs 模型 + Audit()，§4.4；不含认证——认证中间件归 auth 包）
├── internal/database/                 GORM(pgx) 初始化与连接池显式配置（architecture.md §11.4）、BaseModel 通用字段、JSONTime、事务助手 Tx()
├── internal/cache/                    go-redis v9 初始化、连接池、键规范 sf:{module}:{key}、GetJSON/SetJSON/Incr 助手
├── internal/health/                   GET /health 存活；GET /ready 就绪（DB ping + Redis ping；api.md §8 的文件系统检查随阶段 14 文件中心引入，偏差经 §12 回写消除）
├── internal/router/                   静态导入全部域包，完成中间件链与 /api 装配（唯一允许 import 所有域的包，§5.4）
├── internal/auth/                     认证权限域：登录/登出/刷新/会话/踢下线、JWT、RBAC、数据权限、登录保护、users/roles/permissions/departments API、空库初始化
├── internal/masterdata/               基础资料域：商品/SKU/分类/单位/条码/供应商/客户 CRUD 与业务校验
├── internal/warehouse/                仓库空间域：仓库/库区/货架/库位四级结构 CRUD 与级联校验
├── internal/inventory/                库存引擎：唯一变更入口（§8.2）、锁定、append-only 流水、幂等、并发原子 UPDATE、库存/流水查询
└── apidocs/                          swag init 生成物（不入手改）
```

分层纪律（architecture.md §1）：每个域包内部固定 `handler → service → repository` 三层；handler 只做参数/校验/响应；service 持事务边界与业务判断；repository 无业务判断；**禁止 handler 直接操作数据库**。

## 3. 依赖方向规则

```text
cmd/api ──→ router ──→ { health, auth, masterdata, warehouse, inventory }
                              │
        ┌─────────────────────┼  域包仅允许 import：
        ▼                     ▼
  scaffold 五件套：response / middleware / database / cache / logger
  auth 公共契约（仅 CurrentUser / AuthRequired / RequirePermission / WarehouseScope，§5.1）
```

- 禁止：域包之间互相 import；域包 import config（依赖值一律由构造函数注入）；scaffold 包 import 任何域包（router 除外）；cmd/api 写 SQL。
- 跨域数据校验（如 inventory 校验 SKU 存在，api.md §4"业务关系"）：**消费方定义窄接口 + router 装配注入**，见 §4.3。

## 4. 包的独占所有权划分（并行开发合同）

### 4.1 六个独占 scope

后续多工程师并行，每人认领一个 scope；**scope 外的任何文件修改、任何绕过契约的写法均视为违约**。scope A/B 存在先后关系（B 依赖 A 的 Makefile/migrate 集成），可一人顺序承担；C–F 四线在 T1 完成后完全并行。

| Scope | 负责 | 独占文件/目录 | 独占表（迁移评审人） | 交付 |
|---|---|---|---|---|
| **A 脚手架** | 平台工程师 | `cmd/`、`go.mod`、`Makefile`（除 B 的目标块，判据 1 例外）、`docker-compose.dev.yml`、`.golangci.yml`、`internal/config`、`internal/logger`、`internal/middleware`（含 §4.4 审计 helper）、`internal/response`、`internal/database`、`internal/cache`、`internal/health`、`internal/router` | 000002 内容确认（审计表模型归 middleware，§4.4） | 可运行空服务（/health /ready）、信封/错误码/details/RequestID/日志/配置、审计 helper、路由装配 |
| **B 迁移** | 数据工程师 | `db/**`、Makefile 的 migrate/swag/seed-dev 目标块 | 全部 5 个迁移文件（按 §6 DDL 契约实现；A 确认 000002，C/D/E/F 对各自域 DDL 有确认义务，分歧先改本计划再改 SQL） | 5 组 up/down、grants、dev_seed、迁移 Makefile 目标块 |
| **C auth** | 工程师 C | `internal/auth/**` | 000001 内容确认 | 登录/会话/RBAC/数据权限/初始化 + users/roles/permissions/departments API + 权限点种子清单（与 §5.4.1 冻结清单同源） |
| **D masterdata** | 工程师 D | `internal/masterdata/**` | 000003 内容确认 | 商品/SKU/分类/单位/条码/供应商/客户 + `SKUChecker` 实现 |
| **E warehouse** | 工程师 E | `internal/warehouse/**` | 000004 内容确认 | 仓库/库区/货架/库位 + 级联校验（删除检查） |
| **F inventory** | 工程师 F | `internal/inventory/**` | 000005 内容确认 | 库存引擎 + 查询 API + 恒等式/并发/幂等专项测试 |

### 4.2 违约判据（验收时逐条机械可查）

1. 修改他人 scope 文件（git diff 按目录归属判定；**唯一例外：Makefile 按目标块归属**——migrate/swag/seed-dev 目标块归 B，其余归 A）。
2. 任何 handler 绕过 `internal/response` 直接 `c.JSON`/`c.String`/`http.Error`。
3. `internal/inventory` 之外的任何代码对库存族六表（`inventory`/`inventory_ledgers`/`inventory_locks`/`inventory_adjustments`/`batches`/`serial_numbers`）出现写 SQL（CI 守卫见 §8.6），或引用库存族 GORM 模型——**库存族模型仅可定义/导出于 internal/inventory**（类型级防线：域外拿不到模型类型，GORM 链式写 `db.Model(...)` 无从构造，弥补字符串 grep 对链式写法的盲区）。
4. `internal/auth` 之外的任何代码出现对 `users/roles/permissions/user_*` 的写 SQL，或自行解析/签发 JWT。
5. 域包 import 另一个业务域包（§3 白名单外）。
6. C–F 自建迁移文件（迁移统一由 scope B 在预留编号内交付，§6.1）。
7. 在 cmd/api、repository 层之外出现事务边界；repository 层出现业务判断。
8. 日志/响应中出现密码、Token、密钥明文（architecture.md §6 日志红线）——含初始化管理员密码（§7.5）。
9. 跨域数据访问未走 §4.3 消费方窄接口注入——任何域包直接 SELECT/写其他业务域的表视为违约（§4.3 是唯一跨域机制，不存在"授权只读 SELECT"旁路）。

### 4.3 跨域数据访问的唯一机制：消费方窄接口 + Option 注入（router 装配）

**唯一机制，无旁路**（判据 9）：域包定义自己需要的最小消费接口，实现由被消费域提供（实现只读自己的表），在 router 装配时经可变参数 Option 注入；域包之间仍禁止 import。

```go
// —— internal/inventory/ports.go：消费方定义最小接口（禁止 import masterdata/warehouse）——
type SKUChecker interface{ ExistsActive(ctx context.Context, skuID int64) (bool, error) }
type BinChecker interface{ ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error) }

// —— 实现方：被消费域导出具体实现（读自己的表），结构化满足消费方接口 ——
// internal/masterdata：func NewSKUChecker(db *gorm.DB) *SKUCheckService
// internal/warehouse： func NewBinChecker(db *gorm.DB) *BinCheckService

// —— internal/router 装配（唯一注入点，§5.3）——
inventory.RegisterRoutes(protected, db, rdb,
    inventory.WithSKUChecker(masterdata.NewSKUChecker(db)),
    inventory.WithBinChecker(warehouse.NewBinChecker(db)))
auth.RegisterProtectedRoutes(protected, db, rdb,
    auth.WithWarehouseChecker(warehouse.NewChecker(db))) // 用户绑定仓库的存在性校验（api.md §4 业务关系）
```

规则：① M1 必需的 Checker（inventory 的两个、auth 的仓库校验）**未注入即启动 fail-fast**（deployment.md §3 精神），杜绝静默跳过业务校验；② warehouse 库位地图（§5.4）需要库存占用数据，由 warehouse 定义 `BinOccupancyReader` 消费接口、inventory 提供实现，同机制注入；③ 依赖箭头经 router 指向"被消费域导出的实现"，被消费域对消费方接口无感知，并行期无等待。

### 4.4 共享审计 helper（scope A 独占，各域调用）

operation_logs/login_logs 的 GORM 模型与写入 helper 归 internal/middleware（scope A 独占，§4.1）；auth 的登录日志也经同一 helper 写入（login_logs 模型同在 middleware 包，auth 仅调用）。形态：

```go
// internal/middleware/audit.go
type AuditEntry struct {
    Module, ObjectType, Action string
    ObjectID                   int64
    OperatorID                 int64  // 调用方经 auth.CurrentUser 填入
    OperatorName, RequestID, IP string
    Success                    bool
    ErrorCode                  string
    Before, After, Request     any    // 关键更新必填 before/after 快照（architecture.md §8.1）
}
func Audit(tx *gorm.DB, e AuditEntry) error // 在业务事务内写 operation_logs（architecture.md §4：写操作日志与业务同事务）
```

**M1 必须写 operation_logs 的操作清单**（permission.md §6、architecture.md §8.1）：用户创建/更新/启停/重置密码/解锁/绑定角色、角色变更与权限绑定、部门变更、强制下线（踢会话）、masterdata 与 warehouse 的删除/停用、inventory Service 全部变更方法（§8.2）。登录成功/失败写 login_logs（§7.3）。

## 5. 路由注册契约（冻结签名，M2 亦不得变更）

### 5.1 internal/auth 导出（其他包只允许调用，不允许修改）

```go
package auth

// 公开路由：POST /login、POST /refresh（api.md §6.1 豁免名单之内）
func RegisterPublicRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client)

// 受保护路由：/auth/logout、/auth/me、/auth/password、/auth/sessions（管理员踢下线）、
//             /users、/roles、/permissions、/departments（各自挂 RequirePermission）
func RegisterProtectedRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client)

// 中间件契约（三业务域唯一的鉴权入口）
func AuthRequired() gin.HandlerFunc                       // 校验 Bearer JWT + Redis 会话有效（被踢/过期→401）
func RequirePermission(perm string) gin.HandlerFunc       // RBAC 权限点校验（§7.2），失败 403 PERMISSION_DENIED
func CurrentUser(c *gin.Context) (UserContext, bool)      // 取当前用户上下文
func WarehouseScope(c *gin.Context) (all bool, ids []int64) // 数据权限仓库范围快照（§7.4）

type UserContext struct {
    UserID       int64
    Username     string
    IsSuper      bool
    DataScope    string   // ALL / SPECIFIED_WAREHOUSE / DEPARTMENT / SELF / SELF_IN_CHARGE
    WarehouseIDs []int64  // scope=SPECIFIED_WAREHOUSE 时的仓库集
    DeptID       int64
    SessionID    string
}
```

### 5.2 三个业务域统一导出（签名冻结：三参形态 + 可选 Option）

```go
package masterdata // warehouse、inventory 同构
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option)
```

约定：`rg` 已套 `AuthRequired()`；域包内部对每个路由挂 `RequirePermission(...)`（权限点全量清单见 §5.4.1）；**Option 仅用于注入 §4.3 的跨域消费接口**（如 `inventory.WithSKUChecker`），不得携带业务配置。auth 域对应：`RegisterPublicRoutes(rg, db, rdb)`（公开路由无跨域依赖，保持三参）与 `RegisterProtectedRoutes(rg, db, rdb, opts ...Option)`。**不设第二套路由注册入口**（无 RegisterRoutesWith 之类变体），避免并行期出现两套接线方式；无 Option 调用与原始三参形态源级兼容。

### 5.3 internal/router 静态装配形态

```go
// internal/router/router.go —— 静态 import 全部域包，M2 新增域时只改本文件
import (
    "github.com/stockflow/server/internal/auth"
    "github.com/stockflow/server/internal/inventory"
    "github.com/stockflow/server/internal/masterdata"
    "github.com/stockflow/server/internal/warehouse"
    // ...
)

func New(cfg *config.Config, db *gorm.DB, rdb *redis.Client) *gin.Engine {
    r := gin.New()
    r.Use(middleware.Recovery(), middleware.RequestID(), middleware.AccessLog(cfg), middleware.CORS(cfg.CORS.AllowedOrigins))
    r.GET("/health", health.Liveness())
    r.GET("/ready", health.Readiness(db, rdb))

    api := r.Group("/api")
    auth.RegisterPublicRoutes(api.Group("/auth"), db, rdb)

    protected := api.Group("")
    protected.Use(auth.AuthRequired())
    auth.RegisterProtectedRoutes(protected, db, rdb, auth.WithWarehouseChecker(warehouse.NewChecker(db)))
    masterdata.RegisterRoutes(protected, db, rdb)
    warehouse.RegisterRoutes(protected, db, rdb, warehouse.WithBinOccupancy(inventory.NewBinOccupancy(db)))
    inventory.RegisterRoutes(protected, db, rdb,
        inventory.WithSKUChecker(masterdata.NewSKUChecker(db)),
        inventory.WithBinChecker(warehouse.NewBinChecker(db)))
    return r
}
```

### 5.4 M1 路由总表（路径严格对齐 api.md §1；swag 注释随 handler 交付）

| 域 | 路由 | 权限点 |
|---|---|---|
| auth（公开） | POST /api/auth/login、POST /api/auth/refresh | — |
| auth（受保护） | POST /api/auth/logout、GET /api/auth/me、PUT /api/auth/password、GET/DELETE /api/auth/sessions | auth:session:list、auth:session:kick |
| auth | /api/users CRUD + 启停/重置密码/解锁/绑定角色；/api/roles CRUD + 绑定权限；GET /api/permissions；/api/departments CRUD | 见 §5.4.1 |
| masterdata | /api/products、/api/skus、/api/suppliers、/api/customers | 见 §5.4.1 |
| masterdata | /api/product-categories、/api/units（api.md §1 缺项，§12 文档先行补录） | 见 §5.4.1 |
| warehouse | /api/warehouses、/api/zones、/api/shelves、/api/bins；GET /api/warehouses/{id}/map（**库位地图**：按仓库返回区/架/位网格与占用状态，development-plan 阶段 7 交付项；占用数据经 §4.3 注入的 `BinOccupancyReader` 由 inventory 聚合） | 见 §5.4.1（map 复用 warehouse:bin:list） |
| inventory | GET /api/inventory（列表/详情）、GET /api/inventory-ledgers | 见 §5.4.1 |

所有列表接口强制分页（api.md §2.1、architecture.md §7 禁无条件全量查询）；全部接口认证（api.md §6.1：仅 /health、/ready、login、refresh 豁免）。

#### 5.4.1 权限点全量冻结（T2–T5 并行的唯一权威清单，auth 种子与本表同源）

动作枚举：`list / read / create / update / delete / status` 六个基础动作（status=启用/停用）；软删除对象（database.md §5.1：products/skus/suppliers/customers/warehouses/bins）才有 delete，zones/shelves 与 departments/roles 走 status 停用（users 的软删除保留 schema 能力，M1 不提供删除接口，停用即离岗路径）。各资源在基础动作之外仅允许下表列出的特有动作，**禁止发明清单外动作词**：

| 资源 | 权限点（全量） |
|---|---|
| auth:user | list、read、create、update、status、**assign-role**、**reset-password**、**unlock** |
| auth:role | list、read、create、update、status、**assign-permission** |
| auth:permission | list |
| auth:dept | list、read、create、update、status |
| auth:session | list、**kick** |
| masterdata:product / masterdata:sku | list、read、create、update、delete、status |
| masterdata:category / masterdata:unit | list、read、create、update、status |
| masterdata:supplier / masterdata:customer | list、read、create、update、delete、status |
| warehouse:warehouse | list、read、create、update、delete、status |
| warehouse:zone / warehouse:shelf | list、read、create、update、status |
| warehouse:bin | list、read、create、update、delete、status（GET /api/warehouses/{id}/map 复用 list） |
| inventory:inventory / inventory:ledger | list（M1 库存 HTTP 面只读，§8.7） |
| inventory:batch / inventory:serial | list（批次台账 /api/batches、序列号 /api/serials；2026-10-02 复核补录：路由已挂点而清单/种子漏登记，随 §8.7 只读口径） |

敏感动作（reset-password、unlock、kick、assign-permission、delete、status）按 permission.md §6 强制记录审计（§4.4 清单）。

## 6. 迁移划分与表清单（M1 子集，按 database.md §2）

### 6.1 迁移文件划分（5 组，up/down 成对；文件由 scope B 独占产出）

```text
db/migrations/
├── 000001_create_auth_tables.up.sql      users, roles, permissions, departments, user_roles, role_permissions, user_warehouses
├── 000002_create_audit_log_tables.up.sql operation_logs, login_logs
├── 000003_create_masterdata_tables.up.sql product_categories, units, products, skus, barcodes, suppliers, customers
├── 000004_create_warehouse_tables.up.sql  warehouses, zones, shelves, bins
├── 000005_create_inventory_tables.up.sql  batches, serial_numbers, inventory, inventory_locks, inventory_ledgers, inventory_adjustments
└── （每个文件配同名 .down.sql，DROP 顺序与依赖相反）
```

每个迁移文件内容 = 对应域的 DDL 契约；域 scope 负责人确认后由 B 提交。**任何 schema 变更先改本计划（或回写 database.md）再改迁移**。M2+ 域（单据/平台/设备）的迁移从 000006 起按域预留编号段，M1 不建。

### 6.2 表清单与关键设计（26 表）

通用字段按 database.md §3：业务表含 `id(bigserial), created_at, updated_at, created_by, updated_by, deleted_at`；纯日志表（operation_logs、login_logs）无 updated_by/deleted_at（§3 但书）；软删除仅限 §5.1 清单（products、skus、suppliers、customers、warehouses、bins、users）；zones/shelves 用 status 停用。时间一律 `timestamptz`。

**auth 域（000001）**

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| users | username, password_hash(bcrypt), real_name, phone, email, department_id→departments, data_scope(枚举 ALL/SPECIFIED_WAREHOUSE/DEPARTMENT/SELF/SELF_IN_CHARGE), status(ACTIVE/DISABLED), must_change_password(bool), locked_until(timestamptz), last_login_at, last_login_ip, deleted_at | username 唯一（含软删语义用部分唯一索引 `WHERE deleted_at IS NULL`）；idx(department_id) |
| roles | code, name, is_system(bool，内置角色禁删), status | code 唯一 |
| permissions | code, name, type(MENU/BUTTON/API), parent_id, sort, status | code 唯一 |
| departments | parent_id, code, name, status | code 唯一；parent_id 自引用 |
| user_roles | user_id, role_id | PK(user_id, role_id) |
| role_permissions | role_id, permission_id | PK(role_id, permission_id) |
| user_warehouses | user_id, warehouse_id | PK(user_id, warehouse_id)；FK→warehouses（迁移内不建跨文件 FK，见 6.4） |

**审计日志（000002）**——database.md §7 审计数据：应用层无 UPDATE/DELETE 接口，DB 账号分层（db/grants/app_grants.sql：业务账号仅 INSERT，无 UPDATE/DELETE）；两表的 GORM 模型与写入 helper 归 internal/middleware（§4.4，迁移内容确认人相应为 scope A）。

| 表 | 关键列 | 索引 |
|---|---|---|
| operation_logs | request_id, user_id, username, ip, user_agent, module, object_type, object_id, action, method, path, success, error_code, request_snapshot(jsonb), before_snapshot(jsonb), after_snapshot(jsonb), created_at | (user_id, created_at)、(module, created_at)、(object_type, object_id)、(created_at) |
| login_logs | user_id, username, ip, user_agent, success, fail_reason, created_at | (username, created_at)、(created_at) |

**masterdata 域（000003）**——字段按 business-flow §1.1/§1.2 全量落列。

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| product_categories | parent_id, code, name, sort, status | code 唯一 |
| units | code, name, status | code 唯一 |
| products | code, name, short_name, category_id, brand, model, spec, unit_id, weight, length/width/height, volume, image_urls(jsonb，占位，上传接口 M3), description, **remark**（备注，business-flow §1.1 与"描述"并列的字段）, status(ENABLED/DISABLED), deleted_at | code 唯一（部分索引）；idx(category_id)、idx(status) |
| skus | code, product_id, spec_attrs(jsonb), cost_price, sale_price, safety_stock, max_stock, min_replenish_qty, is_batch_managed, is_expiry_managed, is_serial_managed, is_enabled, deleted_at | code 唯一（部分索引）；idx(product_id)；三开关决定 M2 入库/出库业务分支（business-flow §1.2） |
| barcodes | sku_id, barcode, code_type, is_primary | barcode 唯一（业务键，查询热点）；idx(sku_id) |
| suppliers | code, name, contact, phone, email, address, status(ENABLED/DISABLED), remark, deleted_at | code 唯一（部分索引）。已产生业务记录不可删只停用（business-flow §1.4）——删除接口在 M2 有单据数据后加引用校验 |
| customers | code, name, contact, phone, email, address, shipping_address, status, deleted_at | code 唯一（部分索引） |

**warehouse 域（000004）**——四级结构 business-flow §1.6。

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| warehouses | code, name, address, contact, phone, area, capacity, type, status, manager_user_id, deleted_at | code 唯一（部分索引） |
| zones | warehouse_id, code, name, zone_type, capacity, status | unique(warehouse_id, code)；idx(warehouse_id) |
| shelves | warehouse_id, zone_id, code, layers, columns, capacity, status | unique(zone_id, code)；idx(warehouse_id) |
| bins | warehouse_id, zone_id, shelf_id, layer, column_no, code, bin_type, max_capacity, current_capacity, status, deleted_at | unique(warehouse_id, code)（库位编码仓内唯一）；idx(zone_id)、idx(shelf_id)、idx(status) |

**inventory 域（000005）**——详细机制见 §8。本域各表 `batch_id` 统一 `NOT NULL DEFAULT 0`（0=非批次 SKU，同上理由；serial_no 仅序列号商品可空）。

| 表 | 关键列 | 约束/索引 |
|---|---|---|
| batches | sku_id, batch_no, supplier_id, production_date, inbound_date, expiry_date, cost_price, remark | unique(sku_id, batch_no)；idx(expiry_date)（M2 FEFO/临期扫描热点） |
| serial_numbers | serial_no, sku_id, batch_id, warehouse_id, bin_id, status(IN_STOCK/LOCKED/OUTBOUND/RETURNED/FROZEN), last_source_type, last_source_no, last_event_at | serial_no 全局唯一（inventory-rules §8.1）；idx(sku_id, status)、idx(warehouse_id, bin_id) |
| inventory | warehouse_id, zone_id, shelf_id, bin_id, sku_id, **batch_id NOT NULL DEFAULT 0（0=非批次 SKU；不用可空列，避免 PG 唯一索引对 NULL 视为互异导致重复行；batches.id 从 1 起不冲突）**, total_qty, available_qty, locked_qty, frozen_qty, pending_inspect_qty, defective_qty + 通用字段 | **unique(warehouse_id, bin_id, sku_id, batch_id)**（五维定位 inventory-rules §3，保证"同仓同位同批同 SKU 恰一行"使行锁粒度精确；序列号经 serial_numbers 表）；**CHECK 恒等式与非负（§8.1）**；idx(sku_id)、idx(warehouse_id, sku_id) |
| inventory_locks | warehouse_id, bin_id, sku_id, batch_id, lock_type(ORDER_HOLD/COUNT_FREEZE/QC_FREEZE/MANUAL_FREEZE/EXCEPTION_FREEZE), source_type, source_no, qty, status(ACTIVE/RELEASED/CONSUMED), released_at, released_by, remark + 通用字段 | idx(sku_id, warehouse_id, status)、idx(source_type, source_no)（按来源单据幂等查重） |
| inventory_ledgers | ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id, serial_no, change_type, business_type, business_no, status_from, status_to, qty_before, qty_change, qty_after, idempotency_key, operator_id, operator_name, request_id, remark, created_at | ledger_no 唯一；**idempotency_key 部分唯一索引 `WHERE idempotency_key IS NOT NULL`（§8.5）**；idx(sku_id, created_at)、idx(warehouse_id, created_at)、idx(business_no)——**表上无 UPDATE/DELETE 通路（§8.4）** |
| inventory_adjustments | adjustment_no, warehouse_id, sku_id, bin_id, batch_id, adjust_type(盘盈/盘亏/损耗/报废/其他), qty, reason(必填), status(DRAFT/PENDING_APPROVAL/APPROVED/REJECTED/EXECUTED/CANCELLED), approved_by/at, executed_by/at | adjustment_no 唯一；idx(warehouse_id, status)。M1 只落 schema 与 Service 执行入口，审批流 UI/接口随 M2（business-flow §11.1） |

### 6.3 索引设计要点（database.md §6 落地）

1. 列表查询条件字段全部建索引：单号/编码类唯一索引，状态+时间、仓库+SKU 组合索引（§6.1）。
2. inventory 与 inventory_ledgers 是最高频读写表：库存行用**覆盖五维的唯一索引**保证"同位同批同 SKU 仅一行"，使行锁粒度精确；流水按 (sku_id, created_at)、(warehouse_id, created_at)、(business_no) 三条主查询路径建索引。
3. 流水表 append-only；按时间分区/归档策略**预留**（字段与查询均按 created_at 友好设计），实际分区在数据量出现后的性能阶段（阶段 21）启用——M1 不提前分区（禁过度设计）。
4. 禁无条件 `SELECT *`：列表接口强制分页 + 明确列序，repository 层不接受空过滤的全表查询（architecture.md §7）。

### 6.4 迁移工程规则

- 外键策略：M1 迁移内不建跨域外键（库表分文件交付且域并行），一致性由 Service 层校验 + 域内 FK 表达；该决策与理由回写 database.md（§12）。
- 每个迁移本地 `migrate up` + `migrate down 1` 双向验证后才能提交（database.md §9）；Makefile 目标 `migrate-up / migrate-down / migrate-new`。
- 生产首次部署前执行 `db/grants/app_grants.sql` 做账号分层（database.md §7.2）；开发环境使用属主账号，不影响验证。

## 7. 认证权限设计要点（permission.md §2–§4）

### 7.1 模型与落位

- RBAC：`用户 → 角色 → 权限点`（user_roles / role_permissions），权限点覆盖 MENU/BUTTON/API 三级（permission.md §2）；M1 权限点即 §5.4 清单（单点维护在 auth 包种子文件，按 `域:资源:动作` 命名）。
- 六层面落位：页面/按钮权限由 /api/auth/me 返回权限点集供前端（permission.md §5，前端仅体验优化）；API 权限 = RequirePermission 中间件；数据权限/仓库/部门 = §7.4；操作权限 = 敏感操作独立权限点（M1 内如 auth:user:reset-password、auth:session:kick）。
- 内置角色种子：permission.md §1 的 16 个角色全部种子化（is_system=true 禁删）；M1 仅对超级管理员（全部权限）、系统管理员、仓库管理员（仓库+库存查看）、财务/查看人员（只读）配置权限映射，其余角色随 M2 业务域补配——避免为不存在的页面造权限映射。

### 7.2 认证与会话（permission.md §3.1/§3.4）

- 双 Token：Access JWT（HS256，默认 2h，claims 含 uid/sid）+ Refresh Token（不透明随机串，Redis 会话键 `sf:session:{sid}`，滑动 TTL 默认 7d，存储用户与数据范围快照）；刷新时轮换 sid 并作废旧会话。
- **强制下线**：DELETE /api/auth/sessions/{id} 删除 Redis 会话键 → AuthRequired 校验会话不存在立即 401（SESSION_INVALID）——这是"JWT + Redis 会话登记"双轨的原因，纯无状态 JWT 无法满足 permission.md §3.4。
- 会话列表：Redis SCAN `sf:session:*` 汇总在线用户/设备/IP/最近活动（permission.md §3.4）；Redis 为易失数据，会话列表仅作运维视图，登录记录以 login_logs 落库为准（deployment.md §8 一切审计状态落库）。
- Token 刷新/登出均写 login_logs 或 operation_logs。

### 7.3 登录保护（permission.md §3.2/§3.3）

- 连续失败 N 次（默认 5，config 可配）→ `users.locked_until` 锁定 M 分钟；失败计数在 Redis `sf:loginfail:{username}`（TTL=锁定窗口），锁定状态落库（进程重启不丢锁）。**2026-10-03 安全修复轮（S4）变更**：计数扩展为 username+IP 双键、任一键达阈值即锁（未知用户名的 IP 维度同样计数，防跨用户名喷洒）；/api/auth 公开组另挂 IP 维度组级限流（`SF_AUTH_RATE_LIMIT_IP_PER_MINUTE`，平台层中间件，见 deployment.md §1.1）。
- 每次登录（成功/失败）写 login_logs：用户、IP、UA、结果、失败原因（permission.md §3.3）。
- **防枚举统一响应（2026-10-03 变更，安全审查 S8；变更原因：原设计锁定/停用响应与"账号不存在"可区分，构成用户名枚举预言机）**：锁定、停用与"账号不存在/密码错误"对客户端一律返回 AUTH_CREDENTIALS_INVALID，不回显 locked_until 等真实原因；真实失败原因仅写 login_logs——permission.md §3.2 的可操作提示（"账户已锁定"等）改由**管理端经 login_logs / 用户管理（含 unlock 接口）承担**，不在登录响应呈现。账户解锁入口不变（见下）。
- 初始密码强制修改：登录响应携带 `must_change_password=true`，除 PUT /api/auth/password 外的接口在 AuthRequired 中直接 403 PASSWORD_CHANGE_REQUIRED（白名单除外）；强密码策略：长度 ≥ 8 且含字母+数字（validator 实现在 auth 包）。空库首启的**默认管理员初始密码**另按部署口径执行更严的 ≥12 位三类字符策略（S3，见 deployment.md §1.1/§6）。
- **改密保护（2026-10-03 新增，S15）**：PUT /api/auth/password 的原密码校验失败接入独立计数与短期锁定（username+IP 维度），失败写 login_logs——防止持会话者在线爆破原密码。
- 密码哈希 bcrypt cost 12（x/crypto，architecture.md §6/§11.2）；日志/错误信息不回显账号是否存在（防枚举）。
- **账户解锁**：PUT /api/users/{id}/unlock（权限点 auth:user:unlock，§5.4.1）在锁定窗口内提前解锁——重置 locked_until 并清空 Redis 失败计数，写审计（permission.md §3.2"锁定与解锁" + §6 强制下线类敏感操作审计）；管理员无需等待自动到期。

### 7.4 数据权限（permission.md §4）

- 五种范围：`users.data_scope`（ALL / SPECIFIED_WAREHOUSE / DEPARTMENT / SELF / SELF_IN_CHARGE）+ `user_warehouses` 绑定仓库集 + departments 层级。
- 注入位置：登录/刷新时把范围快照写入 Redis 会话；Service/Repository 层经 `auth.WarehouseScope(c)` 取得 `(all, ids)` 注入过滤条件，**禁止**接受前端传入的仓库范围参数决定数据可见性（permission.md §4 实现位置约定）。
- M1 作用面：warehouse/inventory 全部查询按仓库范围过滤；masterdata 为组织级数据（商品/供应商/客户不分仓），不做行级过滤；departments/SELF 范围在 M1 只落模型与快照，行级过滤随 M2 单据域（M1 无"本人负责"类数据）。超级管理员恒为 all。

### 7.5 默认管理员初始化（database.md §8.1、deployment.md §6）

- `auth.BootstrapIfEmpty(db, rdb)`（cmd 启动时调用）：仅当 `users` 表为空时执行；幂等（重复启动不重复插入、不覆盖）。
- 种子内容：16 内置角色、M1 全量权限点（**MENU 类型权限点即 database.md §8.1"默认菜单"的落位**，菜单树与权限点同源于 §5.4.1 清单）、默认管理员账号。管理员初始密码经**环境变量 `SF_ADMIN_INITIAL_PASSWORD` 注入**：需要创建管理员而该变量缺失时启动即失败退出（deployment.md §3 禁止带病启动）；日志只记录"默认管理员已创建"事件本身，**任何情况下禁止把密码写入日志**（architecture.md §6 日志红线）。创建后 `must_change_password=true` 强制改密。
- 默认字典（database.md §8.1）随 dictionaries 表在后续里程碑引入（M1 无该表，理由见 §9），引入时回写 database.md 说明（§12）。
- 默认仓库示例由 `warehouse.EnsureDefaultWarehouse(db)` 同机制种子化（database.md §8.1 明确含"默认仓库示例"）。
- 演示数据完全分离：`db/seed/dev_seed.sql` 仅开发环境手工 psql 执行，任何启动路径不加载（database.md §8.2）。

## 8. 库存核心设计要点（inventory-rules §1/§2/§4/§5/§9）

### 8.1 状态字段与恒等式（inventory-rules §2）

- 六列：`total / available / locked / frozen / pending_inspect / defective`（database.md §3）；在途不进 M1 schema（backend-analysis §5.4，M2 调拨域引入迁移）。
- 恒等式（所有时刻、所有代码路径）：`total = available + locked + frozen + pending_inspect + defective`。**以 PostgreSQL CHECK 约束在数据库层强制**：

```sql
ALTER TABLE inventory ADD CONSTRAINT chk_inventory_identity CHECK (
  total_qty >= 0 AND available_qty >= 0 AND locked_qty >= 0 AND
  frozen_qty >= 0 AND pending_inspect_qty >= 0 AND defective_qty >= 0 AND
  total_qty = available_qty + locked_qty + frozen_qty + pending_inspect_qty + defective_qty
);
```

- 任何一次变更后恒等式由构造保证（每条 UPDATE 成对改两列、和不变），CHECK 是最后防线——被破坏的写库根本无法提交。测试仍须逐操作断言（testing.md §4）。

### 8.2 唯一变更入口（inventory-rules §1）

`internal/inventory.Service` 是全系统库存与流水的**唯一写入口**；M2 起所有业务域（入库/出库/调拨/盘点/调整）只允许经下列方法变更库存，禁止任何包直接写库存/流水表：

```go
// tx 传 nil 时方法自建事务；op 结构均携带可选 IdempotencyKey
func (s *Service) Putaway(ctx, tx, op)      // 上架增加：pending_inspect→available（或免检直达 available），total 增
func (s *Service) Deduct(ctx, tx, op)       // 出库扣减：核销锁定（locked、total 同减），business-flow §8.5 发货触发
func (s *Service) Lock(ctx, tx, op)         // 预占/冻结：available→locked（订单占用/盘点/质检/人工/异常五类，inventory-rules §4）
func (s *Service) ReleaseLock(ctx, tx, lockID, qty) // 释放锁定：locked→available，锁记录 RELEASED（须由明确业务动作触发并生成流水，§4.2）
func (s *Service) MoveBin(ctx, tx, op)      // 仓内移库：同 SKU 跨行转移
func (s *Service) InspectResult(ctx, tx, op) // 待检→合格(available)/不良(defective)
func (s *Service) Adjust(ctx, tx, op)       // 调整单执行（盘盈/亏/损耗/报废，reason 必填，business-flow §11.1）
```

锁定规则（inventory-rules §4）：每笔锁定记录来源单据/类型/数量/操作人/时间（inventory_locks）；分配与预占只允许使用可用库存。

### 8.3 事务边界与原子 UPDATE 形态（inventory-rules §9.3、architecture.md §4/§5）

每个变更方法 = 单事务：`锁定库存行（原子 UPDATE）→ 写 inventory_ledgers →（写/核销 inventory_locks）→ COMMIT`；任何步骤失败整体回滚（architecture.md §4.1）。M2 业务域把"业务单据状态更新 + 库存变更"放进同一外层事务：传入 tx 即可组合。

原子 UPDATE 统一形态（条件更新 + 影响行数判定，与 inventory-rules §9.3 示例等价语义，`total - locked >= n` 即 `available_qty >= n`）：

```sql
-- 预占（订单占用）
UPDATE inventory
SET locked_qty = locked_qty + $n, available_qty = available_qty - $n, updated_at = now(), updated_by = $uid
WHERE id = $id AND deleted_at IS NULL AND available_qty >= $n;
-- 发货核销（正式扣减发生在发货完成时，business-flow §7.2/§8.5）
UPDATE inventory
SET locked_qty = locked_qty - $n, total_qty = total_qty - $n, updated_at = now(), updated_by = $uid
WHERE id = $id AND deleted_at IS NULL AND locked_qty >= $n;
-- 待检转合格
UPDATE inventory
SET pending_inspect_qty = pending_inspect_qty - $n, available_qty = available_qty + $n, updated_at = now(), updated_by = $uid
WHERE id = $id AND deleted_at IS NULL AND pending_inspect_qty >= $n;
```

- **影响行数为 0 即失败并回滚**：再次 SELECT 区分错误码（行不存在 → INVENTORY_RECORD_NOT_FOUND；可用不足 → INVENTORY_NOT_ENOUGH，details 带具体 SKU/库位/需求量与可用量，api.md §4）。
- 多行变更（如批量预占/移库）**按行 id 升序排序后逐行加锁**，固定加锁顺序防死锁；业务层+数据层双重校验（inventory-rules §9.2）。
- 库存行定位：先按五维唯一键 SELECT 行 id（同事务），再对行做条件 UPDATE；禁止"先 SELECT 数量再内存判断后无条件 UPDATE"的两段式写法。

### 8.4 append-only 流水（inventory-rules §5）

- 每次库存变化必须生成一条流水，且与库存变更**同事务**提交（inventory-rules §5.2、architecture.md §4）；流水字段按 §5 清单全量落列（§6.2 表设计）。
- append-only：应用层不提供任何 UPDATE/DELETE 接口；CI 守卫（§8.6）禁止域外写；生产 DB 账号对流水/日志表无 UPDATE/DELETE 权限（database.md §7.2，db/grants/app_grants.sql）。
- 流水号：`LED + YYYYMMDD + '-' + 独立 PG sequence`（禁止裸自增 ID 当业务单号，business-flow §13.1）；统一单据编号规则引擎随 M2 单据域引入，M1 流水号不与其冲突（独立 sequence）。
- change_type 枚举：INBOUND / OUTBOUND / TRANSFER_OUT / TRANSFER_IN / LOCK / RELEASE / MOVE / INSPECT_PASS / INSPECT_DEFECTIVE / ADJUST；business_type/business_no 记录来源单据，构成"单据+流水+操作日志"审计链（architecture.md §8.3）。
- **数量口径（冻结）**：`qty_before/qty_change/qty_after` 记录**受影响状态列**（status_from 所指列）的数量三态。total 不变的迁移（LOCK/RELEASE/MOVE/INSPECT_*）以 `status_from/status_to + qty` 表达列间转移（如 LOCK：status_from=available、status_to=locked、qty 三态为 available 的前/变/后）；total 变化的（INBOUND/OUTBOUND/ADJUST）total 同步 ±qty_change。§8.7 一致性测试断言按此口径编写。

### 8.5 幂等（architecture.md §3.2、api.md §7）

- `inventory_ledgers.idempotency_key` 部分唯一索引；变更方法收到重复 key 时**返回既有结果（不重复扣减）**——查询同 key 流水反解结果直接返回。
- M1 内幂等键在 Service 入口生效；HTTP 层 `Idempotency-Key` 头与"业务单据+行号"去重随 M2 收货/入库/出库接口接入（architecture.md §3.2 三种方式之二）。
- 单据级天然幂等（状态机重复提交返回成功或明确冲突）随 M2 状态机实现。

### 8.6 违约守卫（CI 可执行，两道防线）

第一道——字符串守卫，覆盖原生 SQL（**`db/**` 豁免**：迁移 DDL 与 dev_seed.sql 是合法交付物，database.md §8.2 演示数据本就含库存行；词边界 `\b` 防止 `inventory` 前缀与 `inventory_ledgers` 互相误配）：

```bash
# 库存族六表写入口唯一性守卫：除 internal/inventory 外必须零命中
rg -n "\b(INSERT INTO|UPDATE|DELETE FROM) (inventory|inventory_locks|inventory_ledgers|inventory_adjustments|batches|serial_numbers)\b" \
   --glob '!internal/inventory/**' --glob '!db/**'
```

第二道——覆盖 GORM 链式写法（`db.Model("inventory").Update(...)` 等字符串 grep 无法覆盖的形态）：库存族 GORM 模型**仅定义/导出于 internal/inventory**（判据 3），域外拿不到模型类型即无法构造链式写；评审清单另加静态搜索项"非 inventory 包不得出现库存族表名字符串（守卫豁免路径除外）"。

两条均纳入 Makefile `make ci`；命中即失败。

### 8.7 M1 库存 HTTP 面与测试策略

- HTTP 只读：GET /api/inventory（五维筛选 + 分页）、GET /api/inventory-ledgers（SKU/仓库/时间/单号筛选 + 分页）。锁定/释放/扣减等写操作在 M1 以 Service 层为边界（被 M2 单据域消费），不暴露 HTTP——阶段 8 范围即"库存模型、流水、锁定、并发控制"引擎；触发它们的业务 API 在阶段 9–13。
- 专项测试（testing.md §2/§4 M1 子集，testcontainers-go 起 PG+Redis——机制注记：实际落地为 SF_TEST_PG_*/SF_TEST_REDIS_ADDR 门控外部实例、未设置即 Skip，testcontainers 为备选未落地——testing.md §3）：
  1. 并发扣减/预占：N goroutine 并发对同一库存行预占/核销，断言无负库存、无超卖（inventory-rules §9.1 场景：总量 15、两路各 10，恰一路成功）。
  2. 恒等式：每个变更操作后断言六列恒等式（DB CHECK 之外测试仍显式验证）。
  3. 流水一一对应：每次变更恰一条流水；断言口径按 §8.4——状态迁移型（LOCK/RELEASE/MOVE/INSPECT_*）断言 status_from/to 与受影响列前后值，total 变化型（INBOUND/OUTBOUND/ADJUST）断言 total 前后差 = qty_change。
  4. 幂等：同 idempotency_key 重放，库存仅变化一次。
  5. 锁定生命周期：Lock→ReleaseLock / Deduct 核销，锁记录状态与流水配套正确。
  6. 权限：无权限 403、越权仓库数据被数据权限过滤（testing.md §8）。

## 9. M1 明确不做（与原因）

| 不做 | 原因 |
|---|---|
| 阶段 9–13 全部业务闭环：采购/入库/收货/质检/上架/销售/出库/拣货/复核/打包/发货/退货/调拨/盘点/异常/追溯 | 依赖 M1 库存引擎与基础数据；单据域契约（状态机、编号规则引擎、审批流）应在 M2 立项时单独冻结，M1 交付其唯一依赖（§8.2 入口）即完成铺垫，避免半成品状态机 |
| Excel 导入导出、异步任务 asynq、协程池 ants、sonic | 属阶段 14 平台能力；M1 无导入导出需求，提前引入 asynq 徒增运维面（architecture.md §11.2 均为按需选型） |
| 打印中心、条码生成（boombuler/barcode） | 阶段 15；打印模板依赖文件中心与前端打印层 |
| 扫码解析 /api/scanner、设备/箱码/托盘表、PDA | 阶段 16（devices.md D1–D20）；依赖业务场景接口就绪 |
| 报表、智能能力（补货/预测/库位推荐/积压） | 阶段 17–18；依赖业务流水积累，且 inventory-rules §11 要求建议可追溯计算依据 |
| 通知中心、审批流引擎、system_configs/dictionaries 表 | 通知与审批的业务事件源在 M2 单据域；字典需求随编号规则/打印模板出现时引入，M1 用 product_categories/units 实体表覆盖下拉需求 |
| 定时任务框架（robfig/cron）、库存/效期预警扫描 | 阶段 19/18；M1 无预警数据源 |
| 文件上传/图片接口（仅 products.image_urls 字段占位） | 文件中心（excel.md §7）随阶段 14；上传校验（api.md §5）与其一起实现，避免两套上传逻辑 |
| 备份恢复/系统监控/告警（deployment.md §4–5） | 阶段 19、22；M1 提供启动检查与 /health /ready 即满足容器化前验证 |
| /api/batches、/api/serials 查询接口（api.md §1 已列，M1 不交付） | 批次/序列号业务数据由入库（收货/质检/上架）环节采集产生（inventory-rules §6/§8），M1 无入库流程则接口永远为空；M1 仅建表支撑库存维度与唯一约束，只读查询接口随 M2 入库域交付 |
| Docker 生产编排/CI 流水线全量 | 阶段 22；M1 仅交付 docker-compose.dev.yml 与 Makefile ci 目标 |

## 10. 任务拆分与并行安排（每 Task 一个 commit，Conventional Commits 中文实践）

| Task | Scope | 内容 | 依赖 | 验证 |
|---|---|---|---|---|
| T0 | A | go.mod、cmd/api、config/logger/response/middleware/database/cache/health/router 骨架、docker-compose.dev.yml、Makefile、.golangci.yml | — | `make ci`；/health /ready 通；信封/RequestID 断言测试 |
| T1 | B | 5 组迁移 + grants + dev_seed + migrate 目标；**schema 冻结点** | T0 | migrate up/down 双向通过；CHECK/索引与 §6 一致评审 |
| T2 | C | auth 全量（§7）+ 权限点种子 + BootstrapIfEmpty | T0（迁移文件由 T1 交付，测试可在 T1 后） | 登录/刷新/踢下线/锁定/越权 401/403 测试 |
| T3 | D | masterdata 全量 + SKUChecker 实现 | T1 | CRUD/校验/分页/权限测试；swag 生成 |
| T4 | E | warehouse 全量 + BinChecker + EnsureDefaultWarehouse | T1 | 四级结构/级联停用/权限测试 |
| T5 | F | inventory 引擎（§8）+ 查询 API + 专项测试 | T1（Service 层可与 T2–T4 并行，契约已冻结） | `go test -race ./internal/inventory/...`；§8.7 六项专项 |
| T6 | A | router 全量装配、跨域接口注入、swag 汇总、Makefile ci 纳入守卫命令（§8.6） | T2–T5 | 全接口冒烟；OpenAPI 产出 |
| T7 | 全体 | 前端库存示范页对接、文档回写（§12）、changelog、M1 验收清单（§1 表）全绿 | T6 | development-plan §5 M1 验收 |

并行图：`T0 → T1 → { T2 ∥ T3 ∥ T4 ∥ T5 } → T6 → T7`。T1 完成即 schema 冻结，是并行起点；C–F 只写各自域表，**跨域数据访问一律经 §4.3 消费方窄接口注入（无"只读 SELECT 他人表"旁路，判据 9）**，审计统一经 §4.4 共享 helper。

## 11. M1 验收命令清单（完成时必须实际逐条运行）

```bash
cd server
gofmt -l .                          # 零输出
go vet ./...
go test ./...                       # 全绿
go test -race ./internal/inventory/... ./internal/auth/...   # 并发路径
go build ./...
make migrate-up && make migrate-down && make migrate-up      # 双向验证
rg -n "\b(INSERT INTO|UPDATE|DELETE FROM) (inventory|inventory_locks|inventory_ledgers|inventory_adjustments|batches|serial_numbers)\b" . --glob '!internal/inventory/**' --glob '!db/**'   # 零命中
make swag                           # OpenAPI 生成无错误
# 手工冒烟：登录 → 改密 → 建商品/SKU → 建仓/区/架/位 → 库存查询/流水查询 → 无权限访问 403
```

## 12. 文档回写清单（文档先行，随 T6/T7 提交）

1. `docs/api.md §1`：补录 `/api/product-categories`、`/api/units`、`/api/departments`（基础资料辅助资源与组织实体——部门是 permission.md §4 数据权限的必需实体，api.md §1 认证与权限域现缺该路由）；§2 补 ID 序列化为字符串的约定；§8 补注"M1 的 /ready 检查 DB+Redis，文件系统检查随阶段 14 文件中心引入"。
2. `docs/deployment.md`：固化后端默认端口 8080、本地开发依赖（PG15/Redis7、docker-compose.dev.yml）与 `SF_ADMIN_INITIAL_PASSWORD` 初始化注入方式；/ready 同上补注。
3. `docs/database.md`：补记"M1 迁移内不建跨域外键，一致性由 Service 层校验 + 域内 FK"的决策与理由；补记流水号独立 sequence 过渡方案；补记"默认字典随 dictionaries 表后续里程碑引入"。
4. `docs/inventory-rules.md §3`：补记"序列号维度经 serial_numbers 表定位（一物一行，inventory 行不内嵌 serial_id）、batch_id=0 表示非批次（避免 PG 唯一索引对 NULL 视为互异）"的决策与理由（文档先行，AGENTS.md 硬性规则 2）。
5. `docs/changelog.md`：记录 M1 交付（模块级交付必记，AGENTS.md 硬性规则 2）。
6. `docs/tasks/current.md`：更新后端 M1 任务状态。

## 13. 风险与开放问题

1. **module 名**：`github.com/stockflow/server` 为本方案假定；若实际托管组织不同，必须在 T0 之前确定（此后改名成本随 import 数量上升）。
2. **api.md §1 缺项**（/api/product-categories、/api/units、/api/departments）与 **ID 字符串化**：按 §12 先改文档再实现；若用户对路径命名或 ID 形态另有要求，须在 T0 前拍板。
3. **数据权限快照时效**：数据范围/角色变更后，已签发 access token 内的快照最长 2h 内不刷新；靠权限缓存版本失效 + 管理员强制下线兜底。完全实时生效需引入 token 黑名单/版本机制，M1 不做（记录为已知限制）。
4. **前端对接范围**：M1 的"前端 DoD"仅覆盖库存示范页；基础资料/仓库页面属前端任务流，若需与 T3/T4 同步联调，由 Orchestrator 排期，不阻塞后端验收。
5. **里程碑验收偏差（已确认，验收清单遗留项）**：development-plan §3 九项 DoD"缺一不可"，但 M1 验收的"前端"项仅覆盖阶段 8 库存示范页对接真实 API；阶段 5–7 对应页面（用户/角色/权限、基础资料、仓库管理，含 F10 库位地图页）由前端任务流 F6/F7/F10 补齐。按 development-plan §5 验收 M1 时，以"后端九项全绿 + 前端遗留项显式挂账"为验收口径，不构成后端验收阻塞。
