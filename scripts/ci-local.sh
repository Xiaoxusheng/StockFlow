#!/usr/bin/env bash
# scripts/ci-local.sh —— Makefile `ci` 目标的无 make 依赖实现（backend-m1-plan §10 / §8.6、
# backend-m2-plan §10.4、backend-m3-plan §13.7 门禁清单，2026-10-04 由本地环境搭建任务落位）。
#
# 步骤（与 Makefile ci 同序，另含 dev-environment 要求的 vet -tags integration）：
#   fmt-check → vet → vet(integration) → test → build
#   → guard-docnum → guard-inventory → guard-asynq → guard-readonly → guard-datax → guard-devices
#   → guard-status（只报告不失败——单行 grep 无法裁决跨行 WHERE 状态守卫，人工复核，Makefile 同口径）
#
# 用法：仓库根或任意目录执行 bash scripts/ci-local.sh；任一门禁步骤失败即非零退出。
# 依赖：go 1.2x（PATH 可用）、grep/awk/sed（Git Bash 自带）。集成测试默认不编译，
#       仅以 -tags integration 做 vet 编译检查（同 Makefile 注释口径）。

set -u
cd "$(dirname "$0")/.." || exit 1 # 仓库根（module 根 = 仓库根，见 AGENTS.md）

# ---------- 步骤定义 ----------

step_fmt_check() {
	files=$(gofmt -l cmd internal)
	if [ -n "$files" ]; then
		echo "gofmt 未格式化文件："
		echo "$files"
		return 1
	fi
	echo "fmt-check 通过（cmd internal 零未格式化文件）"
}

step_vet() {
	go vet ./...
}

step_vet_integration() {
	go vet -tags integration ./...
}

step_test() {
	go test ./...
}

step_build() {
	go build ./...
}

step_guard_status() {
	# 只报告不失败：业务 SQL 普遍跨行，单行 grep 无法裁决 WHERE 状态守卫，逐条人工核对。
	echo "== 状态守卫人工复核清单（判据 4：UPDATE+SET status 无 WHERE 同串，注释行除外）=="
	grep -rniE "UPDATE .* SET .*status" --include="*.go" cmd internal | grep -viE "WHERE" | grep -v ": *[0-9]*: *//" || echo "(零命中)"
	echo "guard-status 仅报告不失败（Makefile 同口径）"
	return 0
}

step_guard_docnum() {
	hits=$(grep -rn "newBusinessNo" --include="*.go" cmd internal | grep -v "internal/inventory" || true)
	if [ -n "$hits" ]; then
		echo "newBusinessNo 残留（判据 5：internal/inventory 外零命中）:"
		echo "$hits"
		return 1
	fi
	echo "newBusinessNo 残留检查通过（零命中）"
}

step_guard_inventory() {
	hits=$(grep -rnE "\b(INSERT INTO|UPDATE|DELETE FROM) (inventory|inventory_locks|inventory_ledgers|inventory_adjustments|batches|serial_numbers)\b" --include="*.go" cmd internal | grep -v "internal/inventory" | grep -v "_test" || true)
	if [ -n "$hits" ]; then
		echo "guard-inventory 违约（§8.6：库存族六表写入仅限 internal/inventory）:"
		echo "$hits"
		return 1
	fi
	echo "guard-inventory 通过（inventory 外非测试代码零命中；_test fixture 豁免）"
}

step_guard_asynq() {
	imports=$(grep -rln "hibiken/asynq" --include="*.go" cmd internal | grep -v "internal/asynqx" || true)
	hits=$(grep -rnE "asynq\.(NewClient|NewServer|NewInspector)" --include="*.go" cmd internal | grep -v "internal/asynqx" || true)
	if [ -n "$imports$hits" ]; then
		echo "guard-asynq 违约（判据 4：asynq 库仅 asynqx 封装）:"
		echo "$imports"
		echo "$hits"
		return 1
	fi
	echo "guard-asynq 通过（asynqx 外零命中）"
}

step_guard_readonly() {
	hits=$(grep -rnE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET|\.Create\(|\.Save\(|\.Updates?\(|\.UpdateColumn|\.Delete\(" internal/reports --include="*.go" | grep -v _test || true)
	if [ -n "$hits" ]; then
		echo "guard-readonly 违约（reports 包零写 SQL 红线）:"
		echo "$hits"
		return 1
	fi
	echo "guard-readonly 通过（reports 包零写 SQL）"
}

step_guard_datax() {
	rawtables=$(grep -rniE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET" internal/datax --include="*.go" | grep -v _test | grep -oiE "(INSERT INTO|DELETE FROM|UPDATE) [a-z_]+" | awk '{print tolower($NF)}' | sort -u | grep -vE "^(import_tasks|import_task_rows|export_tasks|files)$" || true)
	models=$(grep -rnE "Model\(&[A-Za-z]+(\.[A-Za-z]+)?\)" internal/datax --include="*.go" | grep -v _test | grep -oE "Model\(&[A-Za-z]+(\.[A-Za-z]+)?" | sed 's/Model(&//' | sed 's/^storage\./storage_/' | sort -u | grep -vE "^(ImportTask|ImportTaskRow|ExportTask|storage_File)$" || true)
	if [ -n "$rawtables$models" ]; then
		echo "guard-datax 违约（写面超出白名单 import_tasks/import_task_rows/export_tasks/files）:"
		echo "$rawtables"
		echo "$models"
		return 1
	fi
	echo "guard-datax 通过（写面 ⊆ 白名单四表）"
}

step_guard_devices() {
	tables=$(grep -rniE "INSERT INTO|DELETE FROM|UPDATE [a-z_]+ SET" internal/devices --include="*.go" | grep -v _test | grep -oiE "(INSERT INTO|DELETE FROM|UPDATE) [a-z_]+" | awk '{print tolower($NF)}' | sort -u | grep -vE "^(devices|device_configs|scan_logs|device_logs|app_versions)$" || true)
	models=$(grep -rnE "Model\(&[A-Za-z]+" internal/devices --include="*.go" | grep -v _test | grep -oE "Model\(&[A-Za-z]+" | sed 's/Model(&//' | sort -u | grep -vE "^(Device|DeviceConfig|ScanLog|DeviceLog|AppVersion)$" || true)
	if [ -n "$tables$models" ]; then
		echo "guard-devices 违约（自有表外写入）:"
		echo "$tables"
		echo "$models"
		return 1
	fi
	echo "guard-devices 通过（写面 ⊆ 自有五表）"
}

# ---------- 执行器：打印分隔标题，任一门禁失败即非零退出 ----------

run_step() {
	title="$1"
	shift
	echo ""
	echo "============================================================"
	echo "== CI 步骤: $title"
	echo "============================================================"
	if ! "$@"; then
		echo ""
		echo "== CI 失败于步骤: $title"
		exit 1
	fi
}

run_step "fmt-check（gofmt -l cmd internal）" step_fmt_check
run_step "vet（go vet ./...）" step_vet
run_step "vet -tags integration（集成测试编译检查）" step_vet_integration
run_step "test（go test ./...）" step_test
run_step "build（go build ./...）" step_build
run_step "guard-docnum（newBusinessNo 残留）" step_guard_docnum
run_step "guard-inventory（库存族六表写入口唯一性）" step_guard_inventory
run_step "guard-asynq（asynq 仅 asynqx 封装）" step_guard_asynq
run_step "guard-readonly（reports 零写 SQL）" step_guard_readonly
run_step "guard-datax（写面 ⊆ 白名单四表）" step_guard_datax
run_step "guard-devices（devices 写面 ⊆ 自有五表）" step_guard_devices
run_step "guard-status（状态守卫报告，不失败）" step_guard_status

echo ""
echo "============================================================"
echo "== CI 全部通过（scripts/ci-local.sh 12/12 步）"
echo "============================================================"
