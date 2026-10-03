# StockFlow 后端 Makefile（backend-m1-plan §10 T0：`make ci` 为门禁）
# 说明：本机（Windows）未安装 make 时，可直接执行各目标对应的 go 命令，效果等价。

.PHONY: fmt fmt-check vet test build run tidy ci

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

# 门禁：格式检查 → vet → 单元测试 → 编译（集成测试需 -tags integration，默认不编译）
ci: fmt-check vet test build

# ====== 数据库迁移与演示数据（scope B 目标块，backend-m1-plan §4.1/§6.4） ======
# golang-migrate CLI 经 go run 固定版本执行（首次需联网拉取模块；也可 go install 后改用本地二进制）。
MIGRATE_PKG    := github.com/golang-migrate/migrate/v4/cmd/migrate@v4.20.1
MIGRATIONS_DIR := db/migrations
# 连接串：默认本地开发库；用环境变量覆盖，如
#   PG_URL=postgres://user:pass@127.0.0.1:5432/stockflow?sslmode=disable make migrate-up
PG_URL ?= postgres://postgres:postgres@127.0.0.1:5432/stockflow?sslmode=disable

.PHONY: migrate-up migrate-down migrate-new seed-demo seed-demo-verify

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
