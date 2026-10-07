package printing

import "context"

// 打印内容数据权限（f20 修复：跨仓装配/读取封堵）。
//
// 背景（printing/handler.go 数据权限注记的已知边界）：data_ids 由请求体直传，
// 各域 ContentReader.Assemble 原契约仅校验"存在性"，持 printing:task:create 的
// 仓库范围受限用户可传他仓单据 ID 装配全量行数据，再经 GET /api/prints/tasks/:id
// 读取渲染数据包。
//
// 修复方式：任务创建入口（handler.createTask）把 auth.WarehouseScope 快照注入
// 请求 context（WithWarehouseScope）；单据类 reader（INBOUND_ORDER/OUTBOUND_ORDER/
// PICK_ORDER/SHIPMENT_ORDER/COUNT_ORDER）从 context 取快照，越仓对象按"不存在"
// 处理（PRINT_DATA_NOT_FOUND failed，与装配契约逐对象失败语义及各域详情接口
// fail-closed 口径一致）。快照未注入（直接调用/单测）不过滤——行为与修复前一致；
// 生产 HTTP 入口恒注入。
//
// 任务详情读取（GET /api/prints/tasks/:id）在 handler 层按"创建人或全量范围"
// 收口（与 datax 导入任务产物 fileVisible ③ 同规则），渲染数据包不再可被他人枚举。
// Assemble 冻结签名（plan §12.2）不变——范围经 context 携带，接口形状零改动。

// warehouseScopeKey context 键（包内私有类型，杜绝跨包键冲突）。
type warehouseScopeKey struct{}

// WarehouseScope 数据权限仓库范围快照（auth.WarehouseScope 口径：ALL/超管 →
// All=true；SPECIFIED_WAREHOUSE → 绑定仓库集；其余范围空集 = 不可见任何仓库，
// fail-closed）。
type WarehouseScope struct {
	All          bool
	WarehouseIDs []int64
}

// Visible 仓库是否在范围内（nil 接收者 = 未注入 = 不限制，由调用方先判空）。
func (sc *WarehouseScope) Visible(warehouseID int64) bool {
	if sc == nil || sc.All {
		return true
	}
	for _, id := range sc.WarehouseIDs {
		if id == warehouseID {
			return true
		}
	}
	return false
}

// WithWarehouseScope 快照注入请求 context（handler 创建打印任务时调用）。
func WithWarehouseScope(ctx context.Context, sc *WarehouseScope) context.Context {
	return context.WithValue(ctx, warehouseScopeKey{}, sc)
}

// WarehouseScopeFrom 取请求 context 中的快照（nil = 未注入——直接调用/单测，不过滤）。
func WarehouseScopeFrom(ctx context.Context) *WarehouseScope {
	sc, _ := ctx.Value(warehouseScopeKey{}).(*WarehouseScope)
	return sc
}

// RestrictedScope 当前请求是否携带受限范围（非 nil 且非 ALL）。
func RestrictedScope(ctx context.Context) (*WarehouseScope, bool) {
	sc := WarehouseScopeFrom(ctx)
	return sc, sc != nil && !sc.All
}

// EmptyScopeIDs 范围受限但绑定仓库集为空（DEPARTMENT/SELF 等不落仓库行级的范围）
// → fail-closed：全部对象按缺失处理（auth.ApplyWarehouseScope "1 = 0" 同口径）。
func EmptyScopeIDs(sc *WarehouseScope) bool {
	return sc != nil && !sc.All && len(sc.WarehouseIDs) == 0
}

// InScope 过滤 SQL 追加片段与参数（col 为实体仓库列名）：
// 未注入/ALL → 无追加；受限 → " AND col IN ?" + 仓库集。
// 调用方 SQL 末尾必须是 "WHERE x.id IN ?"（parsed 参数在 args 首位）。
func InScope(ctx context.Context, col string, args []any) (string, []any) {
	sc := WarehouseScopeFrom(ctx)
	if sc == nil || sc.All {
		return "", args
	}
	return " AND " + col + " IN ?", append(args, sc.WarehouseIDs)
}
