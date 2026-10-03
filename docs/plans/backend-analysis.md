# StockFlow 后端开工前项目分析（development-plan §2）

> 日期：2026-10-02 ｜ 范围：Go 后端里程碑 M1（development-plan 阶段 3–8）开工前分析 ｜ 依据：docs/README.md、development-plan §2/§3/§5、architecture、api、database、permission、inventory-rules、business-flow、testing、deployment、docs/tasks/current.md
>
> 本文对应 [development-plan.md §2](../development-plan.md) 要求的 14 项开工前分析（§2.1–§2.14），其中"将建成的项目结构与技术栈落位"（§A）、"M1 实体与迁移清单"（§B）、"推荐开发顺序"（§C）为本次重点。M1 的逐包实施方案见 [backend-m1-plan.md](backend-m1-plan.md)。

---

## 0. 分析方法（§2.1 之前的诚实声明）

本节所有"现状"结论均来自 2026-10-02 在工作区实际执行的检查，非推测：

```text
ls（工作区根）      → 仅 AGENTS.md、docs/、web/，无 server/、无 scan/（ls server 报 No such file）
ls docs            → 17 份规范文档（README/api/architecture/business-flow/changelog/database/deployment/
                     development-plan/devices/excel/frontend/inventory-rules/permission/plans/printing/
                     requirements/scanner/tasks/testing）
ls docs/plans      → 仅 2026-10-02-frontend-foundation.md
ls web             → 前端脚手架初始文件（package.json/vite.config.ts/tsconfig*/eslint.config.js/index.html），
                     无 src/（T2 进行中）
git log --oneline  → 仅 1 个提交：362c1bb "chore: 初始化仓库，补齐 AGENTS.md 与前端基础平台计划"
git status         → docs/ 与 web/ 均未提交（untracked）
```

结论：**这是一个绿地项目。后端没有"需要重构的模块"，只有"需要按规范一次建对的模块"**。因此 §2.9（需要重构的模块）在本项目当前时点为空集，分析重心转为"将建成什么、按什么顺序建成"。

---

## 1. 当前项目结构（§2.1）与当前技术栈（§2.2）

```text
D:\StockFlow
├── AGENTS.md            # 项目记忆：技术栈基线、硬性规则、目录约定
├── docs/                # 唯一真相来源：17 份规范（基线 v1.x）
│   ├── plans/           # 开发计划（本文件所在）
│   └── tasks/           # current.md 唯一任务状态文件
└── web/                 # StockFlow Web 前端（脚手架初始文件，src/ 尚未建立，T2 进行中）
```

技术栈现状：文档基线已定（AGENTS.md + architecture.md §11），**代码层面前后端均未落地**。后端基线（architecture.md §11.2）：

| 层 | 选型 | 落位（详见 §A.2） |
|---|---|---|
| 语言/框架 | Go + Gin | server/ 全部 |
| 数据库 | PostgreSQL 15+（pgx 驱动） | internal/database + db/migrations |
| 数据访问 | GORM 为主 + 库存关键路径原生 SQL | internal/database（连接/事务）+ internal/inventory（原子 UPDATE） |
| 缓存 | go-redis v9 | internal/cache + internal/auth（会话/权限缓存） |
| 日志 | zap | internal/logger |
| 配置 | viper | internal/config |
| 认证 | golang-jwt/v5 + x/crypto(bcrypt) | internal/auth |
| 迁移 | golang-migrate | db/migrations |
| API 文档 | swaggo/swag | 各域 handler 注释 → server/apidocs |
| 测试 | testify + testcontainers-go | 各包 _test.go |

M1 不引入：asynq、robfig/cron、excelize、boombuler/barcode、sonic、ants（用途分别属于阶段 14–19，见 M1 方案 §9）。

## 2. 已实现功能（§2.3）与缺少功能（§2.4）

**已实现（均属文档与前端平台，无任何后端功能）**：

1. 完整规范体系：需求/架构/领域/数据库/API/权限/库存规则/业务流程/测试/部署 17 份文档（基线 v1.x）。
2. 开发计划（22 阶段 + 每阶段九项 DoD + 里程碑 M1–M4）。
3. 前端基础平台进行中（docs/tasks/current.md：T1 ✅，T2–T11 ⏳）：Vite+React18+TS strict 脚手架初始文件、F1–F5 计划。

**缺少功能（按 development-plan 22 阶段对照）**：

- 阶段 1–2 的产出已由 docs/ 覆盖；阶段 3–8（M1 目标）**全部缺失**：无数据库 Migration、无基础设施、无认证权限、无基础资料、无仓库管理、无库存核心。
- 阶段 9–22 全部缺失（业务闭环、Excel/打印/扫码/设备、报表、智能、日志监控备份、测试、性能、生产部署）。
- 后端代码 0 行：无 go.mod、无 cmd/、无任何 internal/ 包。

## 3. 数据库现状（§2.5）、后端现状（§2.6）、前端现状（§2.7）

- **数据库**：无实例、无 schema、无迁移。文档已规定：PostgreSQL 15+、全部变更走 golang-migrate（database.md §1/§9）、通用字段（§3）、多仓五维模型（§4）、软删除对象清单（§5.1）、审计数据不可破坏（§7）、初始化与演示数据分离（§8）。
- **后端**：不存在。分层规范（architecture.md §1：API→Service→Repository→Database）、错误信封（§2.1）、RequestID/幂等（§3）、事务边界（§4）、并发与锁（§5）仅有文档约定。
- **前端**：web/ 仅脚手架初始文件，src/ 未建立。按 docs/tasks/current.md：API 层与类型将按 api.md §2 对齐（信封 `{code,message,data,request_id}`、分页 `page/pageSize/total/items`）；开发代理 `/api → http://localhost:8080`（plans/2026-10-02-frontend-foundation.md §5，端口待后端落地时在 deployment.md 固化——列入 M1 文档回写，见 M1 方案 §12）；后端未就绪时页面显示统一错误态是预期行为，禁止假数据。

## 4. 可以复用的模块（§2.8）与需要重构的模块（§2.9）

- **可复用**：① 全部 docs/ 规范（本分析与其后的 M1 方案即从规范直接推导，不引入规范外决策）；② 前端 API 层契约（api.md §2）作为后端响应格式的验收标准；③ docs/tasks/current.md 的任务状态机制（M1 执行期沿用）。
- **需要重构**：无（绿地，无历史代码）。唯一约束是 **先改文档再改代码**（AGENTS.md 硬性规则 2）：M1 发现规范缺口时（如 api.md §1 未列基础资料辅助路由，见 M1 方案 §12），先回写规范再实现。

## 5. 数据模型问题（§2.10，前瞻设计决策）

无存量模型，以下是 M1 建模必须一次做对的四个关键决策（详细表设计见 M1 方案 §6）：

1. **多仓五维**：库存维度 `warehouse_id + zone_id + shelf_id + bin_id (+ batch_id)`（database.md §4），禁止 `sku_id + quantity`。序列号不作为库存行的数量维度：`serial_numbers` 一物一行记录位置与状态（database.md §2 将批次与序列号单列；SN 数量恒为 1，独立成表才能支撑 inventory-rules §8 的全生命周期追溯）。
2. **状态数量列 + 恒等式落 DB**：库存行持 `total/available/locked/frozen/pending_inspect/defective` 六列（database.md §3），并用 PostgreSQL CHECK 约束强制恒等式（inventory-rules §2），使"恒等式被破坏"在数据库层即不可能提交，而非仅靠应用代码自觉。
3. **软删除按清单执行**：products/skus/suppliers/customers/warehouses/bins/users 用 deleted_at（database.md §5.1）；zones/shelves 用 status 停用；流水/日志表无 updated_by/deleted_at（§3 但书）。
4. **在途库存不进 M1 schema**：inventory-rules §2 列出"在途"，但 database.md §3 的状态列清单不含在途；在途数量由 M2 调拨域引入（独立迁移），避免 M1 出现没有业务来源的死列。

## 6. 权限问题（§2.11，前瞻设计决策）

无存量权限代码。M1 必须一次落位（依据 permission.md §2–§4，实现见 M1 方案 §7）：

1. RBAC 三层 `用户→角色→权限点`，权限点覆盖菜单/按钮/API 三级；**后端中间件强制校验**，前端权限仅是体验优化（AGENTS.md 硬性规则 7）。
2. 数据权限五种范围（全部仓库/指定仓库/指定部门/本人/本人负责）在 Service/Repository 层注入过滤，禁止前端传参决定范围；模型为 `users.data_scope` + `user_warehouses` + `departments`。
3. 会话必须支持**强制下线**（Token 立即失效）→ JWT 之外必须有 Redis 会话登记，纯无状态 JWT 不满足 permission.md §3.4。
4. 初始化数据（默认管理员/角色/权限点）仅空库首启执行、幂等、强制改密（database.md §8.1）；与演示数据（§8.2）物理分离。

## 7. 库存一致性问题（§2.12，前瞻设计决策）

无存量库存代码。M1 是全系统库存正确性的地基（依据 inventory-rules，实现见 M1 方案 §8）：

1. **唯一变更入口**：所有库存变化只允许经 internal/inventory 暴露的变更方法，变化与流水同事务（inventory-rules §1.3、architecture.md §4）；其他任何包出现库存/流水写 SQL 视为违约（CI grep 守卫，见 M1 方案 §8.6）。
2. **并发控制**：原子条件 UPDATE（`WHERE available >= n`）+ 行锁，影响行数为 0 即库存不足回滚（inventory-rules §9.3）；多行变更按行 id 升序加锁防死锁。
3. **append-only 流水**：inventory_ledgers 只增不改不删（inventory-rules §5），应用层无 UPDATE/DELETE 接口，并按 database.md §7.2 预留数据库账号权限分层。
4. **幂等**：流水表幂等键唯一约束，重复请求不重复扣减（architecture.md §3.2）。

## 8. 生产环境风险（§2.13）

| 风险 | 现状 | M1 应对 |
|---|---|---|
| 无迁移机制则 schema 漂移 | 文档已定 golang-migrate | M1 T1 即建迁移流程，up/down 双向验证（database.md §9） |
| 无鉴权地基则后续域并行开发各自造轮子 | 无代码 | M1 冻结路由/中间件契约（M1 方案 §5），权限点从第一个接口就强制 |
| 恒等式仅靠应用代码 | 无代码 | CHECK 约束 + 专项并发/恒等式测试（testing.md §4） |
| 初始化与演示数据混淆 → 生产被注入演示数据 | 无代码 | 初始化在启动逻辑（auth/warehouse 包），演示数据独立 dev_seed.sql |
| 端口/配置未固化 → 前端联调错位 | 前端代理假定 8080 | M1 固化 8080 并回写 deployment.md |
| 密钥/密码硬编码 | 无代码 | viper 环境变量 + 启动完整性校验，失败快速退出（deployment.md §3） |
| asynq/cron 等提前引入增加 M1 面积 | 无代码 | M1 明确不引入（M1 方案 §9），接口形态为 M2 留缝不落地 |

## A. 将建成的项目结构与技术栈落位（重点）

### A.1 目录结构（server/，M1 建成形态）

```text
server/
├── cmd/api/main.go            # 组装入口：配置→日志→DB/Redis→启动检查→迁移→路由→HTTP→优雅关闭
├── db/
│   ├── migrations/            # golang-migrate SQL（000001–000005，up/down 成对）
│   ├── grants/app_grants.sql  # 审计表账号权限分层（database.md §7.2，生产执行）
│   └── seed/dev_seed.sql      # 开发演示数据（手工执行，禁止生产注入）
├── internal/
│   ├── config/                # viper 配置（环境变量 SF_ 前缀 + yaml），启动完整性校验
│   ├── logger/                # zap 分类日志（access/error/business），request_id 贯穿，敏感脱敏
│   ├── response/              # 统一信封（含 details）/分页/业务错误码（api.md §2/§4 + architecture.md §2）
│   ├── middleware/            # RequestID、访问日志、Recovery、CORS 白名单、共享审计 helper（M1 方案 §4.4）
│   ├── database/              # GORM+pgx 初始化、连接池、通用模型字段、时间类型、事务助手
│   ├── cache/                 # go-redis v9 初始化、键规范 sf:{module}:*、JSON 助手
│   ├── health/                # /health 存活 + /ready 就绪（DB/Redis；文件系统检查随阶段 14 引入，api.md §8 偏差经 M1 方案 §12 回写消除）
│   ├── router/                # 静态导入全部域包完成 /api 装配（唯一知道所有域的包）
│   ├── auth/                  # 登录/JWT/会话/登录保护/用户/角色/权限点/部门/数据权限/初始化
│   ├── masterdata/            # 商品/SKU/分类/单位/条码/供应商/客户
│   ├── warehouse/             # 仓库/库区/货架/库位
│   └── inventory/             # 库存引擎：唯一变更入口/锁定/流水/幂等/查询（含原子 UPDATE）
├── apidocs/                   # swag 生成的 OpenAPI（M1 方案 §5.5）
├── go.mod                     # module github.com/stockflow/server
├── Makefile                   # run/build/test/vet/fmt/lint/migrate/swag/ci
├── docker-compose.dev.yml     # 本地 PG15 + Redis7
└── .golangci.yml
```

### A.2 技术栈落位表（依赖只允许按"行内列出"方向引用）

| 包 | 直接依赖（Go 库） | 可 import 的内部包 |
|---|---|---|
| cmd/api | gin、viper、zap、gorm、go-redis、golang-migrate | 全部（仅做装配） |
| config / logger | viper / zap | 无 |
| response / middleware / database / cache / health | gin、zap、gorm、pgx、go-redis | config、logger |
| router | gin | health、auth、masterdata、warehouse、inventory |
| auth | gin、gorm、go-redis、golang-jwt/v5、x/crypto | scaffold 五件套* |
| masterdata / warehouse / inventory | gin、gorm（inventory 另用原生 SQL）、go-redis | scaffold 五件套 + **auth 公共契约** |

\* scaffold 五件套 = response / middleware / database / cache / logger。域包之间禁止互相 import；跨域数据校验用"消费方定义接口 + router 装配注入"（M1 方案 §4.3）。

### B. M1 实体与迁移清单（重点，详细列设计见 M1 方案 §6）

按 database.md §2 取 M1 子集，共 **26 表 / 5 个迁移文件**：

| 迁移 | 域 | 表 |
|---|---|---|
| 000001 | 权限与组织 | users, roles, permissions, departments, user_roles, role_permissions, user_warehouses |
| 000002 | 审计日志 | operation_logs, login_logs |
| 000003 | 商品与往来 | product_categories, units, products, skus, barcodes, suppliers, customers |
| 000004 | 仓库空间 | warehouses, zones, shelves, bins |
| 000005 | 库存核心 | batches, serial_numbers, inventory（含恒等式 CHECK）, inventory_locks, inventory_ledgers, inventory_adjustments |

M1 明确不建的表（阶段 9–19 实体）：采购/入库/销售/出库/调拨/盘点/质量/异常单据族、notifications、attachments、print/export/import 任务族、system_configs、dictionaries、scheduled_jobs、设备与容器族（devices/scan_logs/boxes/pallets/device_*）。

## C. 推荐开发顺序（§2.14，重点）

```text
T0 脚手架可运行（阶段 4 基础设施）→ T1 迁移与 schema 冻结（阶段 3）
→ 并行三线（契约已冻结，无相互等待）：
   T2 auth（阶段 5）   T3 masterdata（阶段 6）   T4 warehouse（阶段 7）   T5 inventory 引擎（阶段 8，Service 层先行）
→ T6 router 装配 + Swagger + 前端示范页联调
→ T7 M1 验收：恒等式/并发/幂等/权限专项测试（testing.md §2、§4 子集）+ 文档回写 + changelog
```

排序理由：阶段 3（schema）先于一切业务包（表是并行开发的契约）；阶段 4（基础设施）先于阶段 5–8（否则各域自造错误处理/日志）；inventory 引擎的 Service 层只依赖契约不依赖 auth 实现细节（路由契约在 M1 方案 §5 冻结），故可与 T2–T4 并行；单据域（M2）依赖 inventory 唯一变更入口，必须在 M1 把入口契约钉死。

---

## 结论

项目处于"规范完备、代码为零"的理想起点：没有迁移负担，M1 的全部风险集中在**把规范一次性落对**——迁移流程、统一信封、鉴权地基、库存唯一变更入口。上述四点的契约（包所有权、路由注册、表清单、原子 UPDATE 形态）已在 [backend-m1-plan.md](backend-m1-plan.md) 中冻结为可并行实施的合同。
