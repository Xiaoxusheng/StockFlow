package inventory

// inventory 域 datax 接入（backend-m3-plan §2.2 冻结新增文件；§6.1 INITIAL_INVENTORY 导入，
// §6.1 导出 16 模块中 INVENTORY / INVENTORY_LEDGER 两源）。
//
// 期初库存导入（excel §6.1 一致性约束 1：高危操作——仅 error_rows=0 可确认 + 前端二次
// 确认 + 审计，确认侧守卫在 datax Service.Confirm）：写路径 = 库存原语 EnsureBatch +
// Putaway（RequireInspect=false），Source{Type:"initial_import", No: import_no} → INBOUND
// 流水（§2.3 判据 10：唯一合法路径，禁止直写库存族六表）。幂等键按 api.md §7 通式：
// putaway:{import_no}:{row_no}:{bin}:{sku}:{batch}——断点续跑重放不重复入账。
//
// 跨域依赖（plan §12.2 窄接口机制）：SKU 编码/三开关解析、仓库内库位定位经消费方接口
// 定义、router 桥接注入（masterdata.DataxCodeResolver / warehouse.DataxWarehouseResolver
// 承载实现），本域不得直查他域表。导出两源只读本域表（§2.2 规则①）。

import (
	"context"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// —— 跨域编码解析接口（消费方定义，router 注入实现）——

// SKURef SKU 解析结果（含三开关——批次管理决定 BatchOp 是否执行）。
type SKURef struct {
	ID            int64
	Enabled       bool
	Found         bool
	BatchManaged  bool
	ExpiryManaged bool
}

// SKUCodes 基础资料 SKU 编码解析（masterdata.DataxCodeResolver 经 router 闭包桥接）。
type SKUCodes interface {
	ResolveSKUWithFlags(ctx context.Context, code string) (SKURef, error)
}

// WarehouseCodes 仓库编码解析（warehouse.DataxWarehouseResolver 经 router 闭包桥接）。
type WarehouseCodes interface {
	ResolveWarehouseCode(ctx context.Context, code string) (id int64, enabled, found bool, err error)
	ResolveBinCode(ctx context.Context, warehouseID int64, code string) (WarehouseBinRef, error)
}

// WarehouseBinRef 库位定位（warehouse.BinRef 经 router 闭包桥接——内建类型，不携带域类型）。
//
// MT6 集成最小改动（backend-m3-plan §2.2 规则③一次性最小改动清单，唯一执行人=集成工程师）：
// 原稿为未导出类型 warehouseBinRef，导致 WarehouseCodes 窄接口在包外不可实现
// （router 无法命名返回类型）——装配不可能，按导出重命名修复，零逻辑改动。
type WarehouseBinRef struct {
	WarehouseID int64
	ZoneID      int64
	ShelfID     int64
	BinID       int64
	Enabled     bool
	Found       bool
}

// InitialInventoryDeps 期初库存导入依赖（router 装配注入；nil 即 fail-closed 拒绝）。
type InitialInventoryDeps struct {
	SKUs       SKUCodes
	Warehouses WarehouseCodes
}

// InitialInventoryWriter 初始化库存导入写入器（plan §6.1：仓库/库位/SKU 存在、批次号+
// 效期格式、数量>0；高危）。
type InitialInventoryWriter struct {
	svc  *Service
	deps InitialInventoryDeps
}

// NewInitialInventoryWriter 构建期初库存导入写入器（router 装配；svc 必须已注入
// SKU/库位 Checker——与既有 RegisterRoutes 同款要求）。
func NewInitialInventoryWriter(svc *Service, deps InitialInventoryDeps) datax.ImportWriter {
	return &InitialInventoryWriter{svc: svc, deps: deps}
}

// Template 模板规格。
func (w *InitialInventoryWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportInitialInventory,
		Name:        "初始化库存导入",
		FileName:    "初始化库存导入模板.xlsx",
		Description: "期初库存批量导入（高危操作，确认需二次确认）；库存变动经入库原语落 INBOUND 流水。",
		Sheet:       "初始化库存导入",
		Columns: []datax.Column{
			{Key: "warehouse_code", Title: "仓库编码", Required: true, Example: "WH-A", Width: 12},
			{Key: "bin_code", Title: "库位编码", Required: true, Example: "BIN-A010101", Note: "须为该仓库下已启用库位", Width: 16},
			{Key: "sku_code", Title: "SKU编码", Required: true, Example: "SKU-0001", Note: "须为已启用 SKU", Width: 14},
			{Key: "batch_no", Title: "批次号", Example: "B20260101", Note: "批次管理 SKU 必填", Width: 14},
			{Key: "production_date", Title: "生产日期", Type: datax.CellDate, Example: "2026-01-01", Width: 14},
			{Key: "inbound_date", Title: "入库日期", Type: datax.CellDate, Example: "2026-01-02", Width: 14},
			{Key: "expiry_date", Title: "到期日期", Type: datax.CellDate, Example: "2027-01-01", Note: "效期管理 SKU 必填", Width: 14},
			{Key: "qty", Title: "数量", Required: true, Type: datax.CellNumber, Example: "100", Note: "> 0，最多 4 位小数", Width: 10},
			{Key: "remark", Title: "备注", Width: 20},
		},
		SampleRows: [][]string{
			{"WH-A", "BIN-A010101", "SKU-0001", "B20260101", "2026-01-01", "2026-01-02", "2027-01-01", "100", "示例行，导入前请删除"},
		},
		Notes: []string{
			"仓库/库位/SKU 编码必须存在且启用，库位须属于所填仓库",
			"批次管理 SKU 必须填写批次号；效期管理 SKU 必须填写到期日期",
			"数量必须 > 0",
			"高危操作：校验通过后仍需显式二次确认；导入产生业务类型=初始化导入的库存流水",
			"同一文件内同 SKU+批次+库位重复行会被拒绝",
		},
	}
}

func (w *InitialInventoryWriter) depsReady() error {
	if w.deps.SKUs == nil || w.deps.Warehouses == nil {
		return response.NewError(response.CodeInternalError, map[string]any{
			"reason": "期初库存导入跨域解析器未注入（router 装配 NewInitialInventoryWriter deps）",
		})
	}
	return nil
}

// Validate 业务层校验（存在性/启用/三开关/数量；幂等键唯一性属于提交期幂等，不入校验面）。
func (w *InitialInventoryWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	if err := w.depsReady(); err != nil {
		return nil, err
	}
	var errs []datax.RowError
	for _, r := range rows {
		if e := w.checkRow(ctx, r); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs, nil
}

// checkRow 单行业务校验。
func (w *InitialInventoryWriter) checkRow(ctx context.Context, r datax.ImportRow) *datax.RowError {
	whID, _, whFound, err := w.deps.Warehouses.ResolveWarehouseCode(ctx, cellRaw(r, "warehouse_code"))
	if err != nil {
		return rowErrP(r.RowNo, "warehouse_code", "仓库校验失败: "+err.Error())
	}
	if !whFound {
		return rowErrP(r.RowNo, "warehouse_code", "仓库编码不存在")
	}
	bin, err := w.deps.Warehouses.ResolveBinCode(ctx, whID, cellRaw(r, "bin_code"))
	if err != nil {
		return rowErrP(r.RowNo, "bin_code", "库位校验失败: "+err.Error())
	}
	if !bin.Found {
		return rowErrP(r.RowNo, "bin_code", "库位编码在该仓库下不存在")
	}
	if !bin.Enabled {
		return rowErrP(r.RowNo, "bin_code", "库位已停用")
	}
	sku, err := w.deps.SKUs.ResolveSKUWithFlags(ctx, cellRaw(r, "sku_code"))
	if err != nil {
		return rowErrP(r.RowNo, "sku_code", "SKU 校验失败: "+err.Error())
	}
	if !sku.Found {
		return rowErrP(r.RowNo, "sku_code", "SKU 编码不存在")
	}
	if !sku.Enabled {
		return rowErrP(r.RowNo, "sku_code", "SKU 已停用")
	}
	// 三开关约束（inventory-rules §6：效期是批次属性）。
	if cellRaw(r, "batch_no") == "" && sku.BatchManaged {
		return rowErrP(r.RowNo, "batch_no", "批次管理 SKU 必须填写批次号")
	}
	if cellRaw(r, "expiry_date") != "" && !sku.ExpiryManaged {
		return rowErrP(r.RowNo, "expiry_date", "该 SKU 未启用效期管理，不能填写到期日期")
	}
	if cellRaw(r, "expiry_date") != "" && cellRaw(r, "batch_no") == "" && sku.BatchManaged {
		return rowErrP(r.RowNo, "batch_no", "填写效期必须先填写批次号")
	}
	if q := cellRaw(r, "qty"); q != "" {
		qty, err := stock.ParseQty(q)
		if err != nil || !qty.IsPositive() {
			return rowErrP(r.RowNo, "qty", "数量必须大于 0")
		}
	}
	return nil
}

// Commit 逐行经库存原语落库（EnsureBatch + Putaway 免检直达；每行独立事务——
// 行级失败不影响其他行/批次，excel §6.2）。
func (w *InitialInventoryWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	if err := w.depsReady(); err != nil {
		return res, err
	}
	for _, r := range rows {
		if err := w.commitRow(ctx, actor, ref, r); err != nil {
			if respErr, ok := err.(*response.Error); ok {
				// 行级业务失败（存在性/开关/幂等重放矛盾等）：失败行 + 原因。
				res.FailedRows++
				res.Errors = append(res.Errors, datax.RowError{Row: r.RowNo, Column: "sku_code", Message: respErr.Message()})
				continue
			}
			// 基础设施错误：交由队列断点续跑。
			return res, err
		}
		res.SuccessRows++
	}
	return res, nil
}

// commitRow 单行落库：解析 → 建批次（按需）→ 免检上架（幂等键）。
func (w *InitialInventoryWriter) commitRow(ctx context.Context, actor datax.Actor, ref datax.TaskRef, r datax.ImportRow) error {
	whID, _, whFound, err := w.deps.Warehouses.ResolveWarehouseCode(ctx, cellRaw(r, "warehouse_code"))
	if err != nil {
		return err
	}
	if !whFound {
		return response.NewError(ErrBinNotFound, map[string]any{"reason": "仓库不存在"})
	}
	// 数据权限（plan §13.6：域内写入器按 Actor 仓库范围快照构建 Scope，permission.md §4）：
	// 范围受限用户的行目标仓库不在快照集 → 行级失败（fail-closed；ALL/超管全量放行）。
	// Validate 阶段无 Actor 入参（ImportWriter 冻结签名），写路径在 Commit 强制执行。
	if !actor.AllWarehouses && !warehouseInScope(actor.WarehouseIDs, whID) {
		return response.NewError(ErrWarehouseScopeDenied, map[string]any{
			"warehouse_id": whID, "reason": "仓库不在数据权限范围内",
		})
	}
	bin, err := w.deps.Warehouses.ResolveBinCode(ctx, whID, cellRaw(r, "bin_code"))
	if err != nil {
		return err
	}
	if !bin.Found || !bin.Enabled {
		return response.NewError(ErrBinNotFound, map[string]any{"reason": "库位不存在或已停用"})
	}
	sku, err := w.deps.SKUs.ResolveSKUWithFlags(ctx, cellRaw(r, "sku_code"))
	if err != nil {
		return err
	}
	if !sku.Found || !sku.Enabled {
		return response.NewError(ErrSKUNotFound, map[string]any{"reason": "SKU 不存在或已停用"})
	}
	qty, err := stock.ParseQty(cellRaw(r, "qty"))
	if err != nil || !qty.IsPositive() {
		return response.NewError(ErrQtyInvalid, map[string]any{"reason": "数量必须大于 0"})
	}

	batchID := int64(0)
	if sku.BatchManaged {
		op := stock.BatchOp{
			SKUID:          sku.ID,
			BatchNo:        cellRaw(r, "batch_no"),
			ProductionDate: dateCellJSON(r, "production_date"),
			InboundDate:    dateCellJSON(r, "inbound_date"),
			ExpiryDate:     dateCellJSON(r, "expiry_date"),
			Actor:          stockActor(actor),
			Remark:         "期初库存导入 " + ref.ImportNo,
		}
		err = database.Tx(ctx, w.svc.db, func(tx *gorm.DB) error {
			id, _, berr := w.svc.EnsureBatch(ctx, tx, op)
			if berr != nil {
				return berr
			}
			batchID = id
			return nil
		})
		if err != nil {
			return err
		}
	}

	// 幂等键（api.md §7 通式）：putaway:{单据号}:{行号}:{bin}:{sku}:{batch}——
	// 断点续跑重放命中既有流水即幂等返回，不重复入账。
	key := strings.Join([]string{
		"putaway", ref.ImportNo, strconv.Itoa(r.RowNo),
		strconv.FormatInt(bin.BinID, 10), strconv.FormatInt(sku.ID, 10), strconv.FormatInt(batchID, 10),
	}, ":")
	op := stock.PutawayOp{
		Key: stock.RowKey{
			WarehouseID: bin.WarehouseID,
			ZoneID:      bin.ZoneID,
			ShelfID:     bin.ShelfID,
			BinID:       bin.BinID,
			SKUID:       sku.ID,
			BatchID:     batchID,
		},
		Qty:            qty,
		RequireInspect: false, // 期初导入免检直达可用（excel §6.1：产生 INBOUND 流水）
		Source:         stock.Source{Type: "initial_import", No: ref.ImportNo},
		Actor:          stockActor(actor),
		IdempotencyKey: key,
		Remark:         "期初库存导入第 " + strconv.Itoa(r.RowNo) + " 行",
	}
	// 审计由库存原语内建（plan §4.4：inventory Service 变更方法同事务写 operation_logs，
	// module=inventory）；任务级审计在 datax confirm/commit 侧完成（module=datax）——
	// 两级审计各司其职，本文件不重复写。
	return database.Tx(ctx, w.svc.db, func(tx *gorm.DB) error {
		_, err := w.svc.Putaway(ctx, tx, op)
		return err
	})
}

// stockActor datax.Actor → 库存原语 Actor。
func stockActor(a datax.Actor) stock.Actor {
	return stock.Actor{
		ID: a.UserID, Name: a.Username, RequestID: a.RequestID,
		IP: a.IP, UserAgent: a.UserAgent, Method: a.Method, Path: a.Path,
	}
}

// warehouseInScope 仓库是否在数据权限快照集内（仅范围受限用户调用——All 路径已放行）。
func warehouseInScope(ids []int64, warehouseID int64) bool {
	for _, id := range ids {
		if id == warehouseID {
			return true
		}
	}
	return false
}

// dateCellJSON DATE 单元格 → database.JSONTime（空值零值 → 批次列 NULL）。
func dateCellJSON(r datax.ImportRow, key string) database.JSONTime {
	d := r.Cells[key].D
	if d == "" {
		return database.JSONTime{}
	}
	t, err := time.Parse("2006-01-02 15:04:05", d)
	if err != nil {
		t, _ = time.Parse("2006-01-02", d)
	}
	return database.JSONTime{Time: t}
}

// cellRaw 读原始文本列。
func cellRaw(r datax.ImportRow, key string) string { return strings.TrimSpace(r.Raw[key]) }

// rowErrP 行错误指针快捷构造。
func rowErrP(rowNo int, key, msg string) *datax.RowError {
	e := datax.RowError{Row: rowNo, Column: key, Message: msg}
	return &e
}
