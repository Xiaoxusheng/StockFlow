# StockFlow 后端 Makefile（backend-m1-plan §10 T0：`make ci` 为门禁）
# 说明：本机（Windows）未安装 make 时，可直接执行各目标对应的 go 命令，效果等价。

.PHONY: fmt fmt-check vet test build run tidy ci guard-status guard-docnum guard-inventory guard-asynq guard-readonly guard-datax guard-devices

# 格式化（gofmt 为 Go 工具链自带命令）
fmt:
	gofmt -s -w cmd internal

# 格式检查（CI 用：有未格式化文件即失败）
fmt-check:
	@files=$$(gofmt -l cmd internal); if [ -n "$$files" ]; then echo "gofmt 未格式化文件："; echo "$$files"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

build:
	go build ./...

run:
	go run ./cmd/server

tidy:
	go mod tidy

# 门禁：格式检查 → vet → 单元测试 → 编译 → 冻结契约守卫（backend-m2-plan §10.4：
# 状态守卫 grep（判据 4）+ newBusinessNo 残留检查（判据 5）；集成测试需 -tags
# integration，默认不编译）
# M3 追加守卫（backend-m3-plan §13.7）：guard-asynq（asynqx 外 asynq 直用零命中）、
# guard-readonly（reports 包零写 SQL）、guard-datax（写面 ⊆ 白名单四表）、
# guard-devices（devices 包业务表写 SQL 零命中）。
# 清偿轮追加（backend-m1-plan §8.6 第一道接线，F3）：guard-inventory（库存族六表
# 写入口唯一性——internal/inventory 外零命中）。
ci: fmt-check vet test build guard-status guard-docnum guard-inventory guard-asynq guard-readonly guard-datax guard-devices

# 状态守卫（backend-m2-plan §2.3 判据 4 / §10.4）：单行内 UPDATE...SET status 而无
# WHERE 同串的命中即"人工复核清单"输出——业务 SQL 字符串普遍跨行，WHERE status=
# <前置态> 守卫常在后续行，grep 单行无法裁决，故本目标只报告不失败（评审/CI 人工
# 逐条核对命中行所在完整语句是否带状态守卫；注释行排除）。
guard-status:
	@echo "== 状态守卫人工复核清单（判据 4：UPDATE+SET status 无 WHERE 同串，注释行除外）=="
	@grep -rniE "UPDATE .* SET .*status" --include="*.go" cmd internal | grep -viE "WHERE" | grep -v ": *[0-9]*: *//" || echo "(零命中)"

# 单号引擎守卫（backend-m2-plan §2.3 判据 5）：绕过 internal/docnum 的 M1 过渡实现
# newBusinessNo 在 internal/inventory 外必须零命中（命中即失败——MT0 已删除，残留
# 视为绕过统一编号引擎的回归）。
guard-docnum:
	@hits=$$(grep -rn "newBusinessNo" --include="*.go" cmd internal | grep -v "internal/inventory" || true); \
	if [ -n "$$hits" ]; then echo "newBusinessNo 残留（判据 5：internal/inventory 外零命中）:"; echo "$$hits"; exit 1; fi; \
	echo "newBusinessNo 残留检查通过（零命中）"

# 库存族六表写入口唯一性守卫（backend-m1-plan §8.6 第一道防线，清偿项 F3 接线）：
# 除 internal/inventory 外对六表（inventory/inventory_locks/inventory_ledgers/
# inventory_adjustments/batches/serial_numbers）出现写 SQL 即失败（词边界 \b 防前缀误配）。
# 计划原文为 rg 命令（搜索范围含全仓，db/、docs/ glob 豁免——迁移 DDL 与文档示例合法）：
#   rg -n "\b(INSERT INTO|UPDATE|DELETE FROM) (inventory|inventory_locks|inventory_ledgers|inventory_adjustments|batches|serial_numbers)\b" --glob '!internal/inventory/**' --glob '!db/**' --glob '!docs/**'
# 本机可执行形态说明：本机 rg 14.1.1 对 `!internal/inventory/**` 前缀排除 glob 在
# Windows 反斜杠路径下不匹配（2026-10-04 实测，rg --debug 证实 glob 正则为 / 分隔而
# 候选路径为 \ 分隔），故落地为 grep+grep -v 等价形态（与 guard-docnum 同模式）；
# 搜索范围限定 cmd internal，docs/ 与 db/ 天然豁免（等价于计划的两条 glob）。
# _test.go 豁免（2026-10-04 复核修正，对齐 guard-readonly/guard-datax 口径）：集成测试
# fixture（如 internal/auth/integration_datascope_test.go 的 inventory/inventory_adjustments
# INSERT）属合法测试数据，非生产写入路径；守卫约束生产代码，命中判定仅看非测试文件。
guard-inventory:
	@hits=$$(grep -rnE "\b(INSERT INTO|UPDATE|DELETE FROM) (inventory|inventory_locks|inventory_ledgers|inventory_adjustments|batches|serial_numbers)\b" --include="*.go" cmd internal | grep -v "internal/inventory" | grep -v "_test" || true); \
	if [ -n "$$hits" ]; then echo "guard-inventory 违约（§8.6：库存族六表写入仅限 internal/inventory）:"; echo "$$hits"; exit 1; fi; \
	echo "guard-inventory 通过（inventory 外非测试代码零命中；_test fixture 豁免）"

# ====== 数据库迁移与演示数据（scope B 目标块，backend-m1-plan §4.1/§6.4） ======
# golang-migrate CLI 经 go run 固定版本执行（首次需联网拉取模块；也可 go install 后改用本地二进制）。
MIGRATE_PKG    := github.com/golang-migrate/migrate/v4/cmd/migrate@v4.20.1
MIGRATIONS_DIR := db/migrations
# 连接串：默认本地开发库；用环境变量覆盖，如
#   PG_URL=postgres://user:pass@127.0.0.1:5432/stockflow?sslmode=disable make migrate-up
PG_URL ?= postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable

.PHONY: migrate-up migrate-down migrate-new seed-demo seed-demo-verify swag

# 升级到最新（database.md §9：交付前须 migrate-up + migrate-down 双向验证）
migrate-up:
	go run $(MIGRATE_PKG) -path $(MIGRATIONS_DIR) -database "$(PG_URL)" up

# 回滚最近一个版本
migrate-down:
	go run $(MIGRATE_PKG) -path $(MIGRATIONS_DIR) -database "$(PG_URL)" down 1

# 新建成对迁移文件：make migrate-new name=create_xxx
migrate-new:
	go run $(MIGRATE_PKG) create -ext sql -seq -dir $(MIGRATIONS_DIR) $(name)

# 演示数据（database.md §8.2）：仅开发环境（必须 SF_ENV=dev）；SQL 文件内另有 is_dev 门禁与
# "生产安全初始化已完成"前置守卫（bootstrap 内置角色缺失即拒绝），生产环境不可能经本目标注入演示数据。
seed-demo:
	@test "$(SF_ENV)" = "dev" || { echo "seed-demo 仅限开发环境：请以 SF_ENV=dev make seed-demo 执行"; exit 1; }
	psql "$(PG_URL)" -v is_dev=1 -f db/seed/dev_seed.sql

# 演示数据灌数后只读自检（可重复执行，任何环境安全）：实体覆盖/恒等式/期初库存-流水成对断言，
# 任一断言失败 psql 返回非 0。配套 db/seed/dev_seed_verify.sql。
seed-demo-verify:
	psql "$(PG_URL)" -f db/seed/dev_seed_verify.sql

# swag 注解汇总（清偿项 F8，T6 收口）：swag CLI 经 go run 固定版本执行（不落 bin），
# 解析各域 handler doc 注释中的最小集注解（@Summary/@Tags/@Param/@Success/@Failure/@Router）
# 生成 apidocs/docs.go + swagger.json/yaml。路由清单与 gin 实际注册一一对应（不编造路由）。
# 本机等价命令（无 make 时直接执行）：
#   go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/server/main.go -o apidocs --parseDependency --parseDepth 3
swag:
	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/server/main.go -o apidocs --parseDependency --parseDepth 3

# asynq 直用守卫（backend-m3-plan §2.3 判据 4）：asynq 库仅 internal/asynqx 可 import，
# asynq API（NewClient/NewServer/NewInspector）在 asynqx 外零命中——绕过 Queue 基座即失败。
guard-asynq:
	@imports=$$(grep -rln "hibiken/asynq" --include="*.go" cmd internal | grep -v "internal/asynqx" || true); 	hits=$$(grep -rnE "asynq\.(NewClient|NewServer|NewInspector)" --include="*.go" cmd internal | grep -v "internal/asynqx" || true); 	if [ -n "$$imports$$hits" ]; then echo "guard-asynq 违约（判据 4：asynq 库仅 asynqx 封装）:"; echo "$$imports"; echo "$$hits"; exit 1; fi; 	echo "guard-asynq 通过（asynqx 外零命中）"

# reports 只读守卫（backend-m3-plan §2.3 判据 3 / inventory-rules §11 只读红线）：
# reports 包 INSERT/DELETE/GORM 写方法零命中（命中即失败）。
guard-readonly:
	@hits=$$(grep -rnE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET|\.Create\(|\.Save\(|\.Updates?\(|\.UpdateColumn|\.Delete\(" internal/reports --include="*.go" | grep -v _test || true); 	if [ -n "$$hits" ]; then echo "guard-readonly 违约（reports 包零写 SQL 红线）:"; echo "$$hits"; exit 1; fi; 	echo "guard-readonly 通过（reports 包零写 SQL）"

# datax 写面白名单守卫（backend-m3-plan §2.3 判据 3）：写 SQL/gorm 写目标 ⊆
# import_tasks / import_task_rows / export_tasks / files（白名单外表写入即失败）。
guard-datax:
	@rawtables=$$(grep -rniE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET" internal/datax --include="*.go" | grep -v _test | grep -oiE "(INSERT INTO|DELETE FROM|UPDATE) [a-z_]+" | awk '{print tolower($$NF)}' | sort -u | grep -vE "^(import_tasks|import_task_rows|export_tasks|files)$$" || true); 	models=$$(grep -rnE "Model\(&[A-Za-z]+(\.[A-Za-z]+)?\)" internal/datax --include="*.go" | grep -v _test | grep -oE "Model\(&[A-Za-z]+(\.[A-Za-z]+)?" | sed 's/Model(&//' | sed 's/^storage\./storage_/' | sort -u | grep -vE "^(ImportTask|ImportTaskRow|ExportTask|storage_File)$$" || true); 	if [ -n "$$rawtables$$models" ]; then echo "guard-datax 违约（写面超出白名单 import_tasks/import_task_rows/export_tasks/files）:"; echo "$$rawtables"; echo "$$models"; exit 1; fi; 	echo "guard-datax 通过（写面 ⊆ 白名单四表）"

# devices 写面守卫（backend-m3-plan §2.3 判据 3）：devices 包对业务表出现写 SQL 零命中
# （自有表 devices/device_configs/scan_logs/device_logs/app_versions 之外命中即失败）。
guard-devices:
	@tables=$$(grep -rniE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET" internal/devices --include="*.go" | grep -v _test | grep -oiE "(INSERT INTO|DELETE FROM|UPDATE) [a-z_]+" | awk '{print tolower($$NF)}' | sort -u | grep -vE "^(devices|device_configs|scan_logs|device_logs|app_versions)$$" || true); 	models=$$(grep -rnE "Model\(&[A-Za-z]+" internal/devices --include="*.go" | grep -v _test | grep -oE "Model\(&[A-Za-z]+" | sed 's/Model(&//' | sort -u | grep -vE "^(Device|DeviceConfig|ScanLog|DeviceLog|AppVersion)$$" || true); 	if [ -n "$$tables$$models" ]; then echo "guard-devices 违约（自有表外写入）:"; echo "$$tables"; echo "$$models"; exit 1; fi; 	echo "guard-devices 通过（写面 ⊆ 自有五表）"
