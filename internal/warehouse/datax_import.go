package warehouse

// warehouse 域 datax 接入（backend-m3-plan §2.2 冻结新增文件；§6.1 写入器）：
//   - 两个导入写入器：WAREHOUSE / LOCATION（写路径经本域既有 Service 通路
//     CreateWarehouse/CreateBin——§2.2 规则①禁止旁路 SQL；编码重复为校验错误行）；
//   - DataxWarehouseResolver：仓库/库位编码解析器（purchase/sales/inventory 各域
//     datax_import.go 的跨域依赖，router 桥接注入——plan §12.2 窄接口机制，只读本域表）。
//
// 数据权限（plan §13.6）：写入器 actor 携带仓库范围快照（datax.Actor.AllWarehouses/
// WarehouseIDs），经本域 Scope 语义校验——库位创建归属仓库必须在范围内，范围外按
// 不存在处理（fail-closed）。

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/response"
)

// warehouseActor datax.Actor → 本域 Actor（Scope 用快照重建——数据权限在创建时点冻结）。
func warehouseActor(a datax.Actor) Actor {
	return Actor{
		UserID: a.UserID, Username: a.Username,
		RequestID: a.RequestID, IP: a.IP, UserAgent: a.UserAgent,
		Method: a.Method, Path: a.Path,
		Scope: Scope{All: a.AllWarehouses, WarehouseIDs: a.WarehouseIDs},
	}
}

// rowErr 行错误快捷构造。
func rowErr(rowNo int, key, msg string) datax.RowError {
	return datax.RowError{Row: rowNo, Column: key, Message: msg}
}

// serviceErrMsg 领域错误 → 用户可读消息。
func serviceErrMsg(err error) string {
	if re, ok := err.(*response.Error); ok {
		return re.Message()
	}
	return err.Error()
}

// ---- 仓库导入（WAREHOUSE）----

// WarehouseImportWriter 仓库导入写入器（plan §6.1：编码重复、启用状态）。
type WarehouseImportWriter struct{ svc *Service }

// NewWarehouseImportWriter 构建仓库导入写入器（router 装配）。
func NewWarehouseImportWriter(svc *Service) datax.ImportWriter {
	return &WarehouseImportWriter{svc: svc}
}

// Template 模板规格。
func (w *WarehouseImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportWarehouse,
		Name:        "仓库导入",
		FileName:    "仓库导入模板.xlsx",
		Description: "批量导入仓库基础资料；编码已存在视为错误行。",
		Sheet:       "仓库导入",
		Columns: []datax.Column{
			{Key: "code", Title: "仓库编码", Required: true, UniqueInFile: true, Example: "WH-A", Note: "全局唯一，创建后不可修改", Width: 12},
			{Key: "name", Title: "仓库名称", Required: true, Example: "示例一号仓", Width: 18},
			{Key: "wh_type", Title: "仓库类型", Example: "NORMAL", Note: "缺省 NORMAL", Width: 12},
			{Key: "address", Title: "地址", Width: 26},
			{Key: "contact", Title: "联系人", Width: 12},
			{Key: "phone", Title: "电话", Width: 14},
			{Key: "area", Title: "面积(㎡)", Type: datax.CellNumber, Width: 12},
			{Key: "capacity", Title: "容量", Type: datax.CellNumber, Width: 12},
		},
		SampleRows: [][]string{
			{"WH-A", "示例一号仓", "NORMAL", "示例市示例区示例路 1 号", "李四", "13900000000", "1200", "8000"},
		},
		Notes: []string{
			"仓库编码必填且全文件内不得重复、不得与系统已有编码重复",
			"仓库类型缺省 NORMAL（值域开放，格式须为大写编码）",
			"面积/容量为数字列，须 >= 0",
			"仓库创建后为启用状态",
		},
	}
}

// Validate 业务层校验（编码与库内重复）。
func (w *WarehouseImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		if code := text(r, "code"); code != "" {
			exist, err := w.svc.repo.FindWarehouseByCode(ctx, code)
			if err != nil {
				return nil, err
			}
			if exist != nil {
				errs = append(errs, rowErr(r.RowNo, "code", "仓库编码已存在（如需修改请使用仓库编辑）"))
			}
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateWarehouse 通路写入。
func (w *WarehouseImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		in := WarehouseCreateInput{
			Code:    text(r, "code"),
			Name:    text(r, "name"),
			Address: text(r, "address"),
			Contact: text(r, "contact"),
			Phone:   text(r, "phone"),
			Type:    text(r, "wh_type"),
		}
		if v := text(r, "area"); v != "" {
			f := cellFloat(r, "area")
			in.Area = &f
		}
		if v := text(r, "capacity"); v != "" {
			f := cellFloat(r, "capacity")
			in.Capacity = &f
		}
		if _, err := w.svc.CreateWarehouse(ctx, warehouseActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// ---- 库位导入（LOCATION）----

// BinImportWriter 库位导入写入器（plan §6.1：仓库/库区/货架存在（Checker 复用）、
// 库位编码仓内唯一）。
type BinImportWriter struct{ svc *Service }

// NewBinImportWriter 构建库位导入写入器（router 装配）。
func NewBinImportWriter(svc *Service) datax.ImportWriter { return &BinImportWriter{svc: svc} }

// Template 模板规格。
func (w *BinImportWriter) Template() datax.TemplateSpec {
	return datax.TemplateSpec{
		ImportType:  datax.ImportLocation,
		Name:        "库位导入",
		FileName:    "库位导入模板.xlsx",
		Description: "批量导入库位；须按 仓库→库区→货架 既有层级定位（含软删占用编码判重）。",
		Sheet:       "库位导入",
		Columns: []datax.Column{
			{Key: "warehouse_code", Title: "仓库编码", Required: true, Example: "WH-A", Width: 12},
			{Key: "zone_code", Title: "库区编码", Required: true, Example: "Z-A01", Note: "须为该仓库下已启用库区", Width: 12},
			{Key: "shelf_code", Title: "货架编码", Required: true, Example: "SH-A0101", Note: "须为该库区下已启用货架", Width: 14},
			{Key: "code", Title: "库位编码", Required: true, UniqueInFile: true, Example: "BIN-A010101", Note: "同仓内唯一（含历史软删占用编码）", Width: 16},
			{Key: "bin_type", Title: "库位类型", Example: "PICK", Note: "缺省 PICK", Width: 10},
			{Key: "layer", Title: "层", Type: datax.CellNumber, Example: "1", Width: 8},
			{Key: "column_no", Title: "列", Type: datax.CellNumber, Example: "1", Width: 8},
			{Key: "max_capacity", Title: "最大容量", Type: datax.CellNumber, Width: 12},
		},
		SampleRows: [][]string{
			{"WH-A", "Z-A01", "SH-A0101", "BIN-A010101", "PICK", "1", "1", "120"},
		},
		Notes: []string{
			"仓库/库区/货架编码必须存在且启用，且层级归属一致",
			"库位编码同仓库内唯一（历史删除的编码占用不可复用）",
			"层/列为 >= 1 的整数；最大容量 >= 0",
		},
	}
}

// Validate 业务层校验（层级存在 + 启用 + 编码仓内唯一；复用本域 Checker 语义）。
func (w *BinImportWriter) Validate(ctx context.Context, rows []datax.ImportRow) ([]datax.RowError, error) {
	var errs []datax.RowError
	for _, r := range rows {
		loc, lerr := w.resolveLocation(ctx, r)
		if lerr != nil {
			return nil, lerr
		}
		if loc.err != nil {
			errs = append(errs, *loc.err)
			continue
		}
		if code := text(r, "code"); code != "" {
			exist, err := w.svc.repo.FindBinByCode(ctx, loc.warehouseID, code)
			if err != nil {
				return nil, err
			}
			if exist != nil {
				errs = append(errs, rowErr(r.RowNo, "code", "库位编码在该仓库内已存在（含历史占用编码）"))
			}
		}
	}
	return errs, nil
}

// Commit 逐行经既有 CreateBin 通路写入。
func (w *BinImportWriter) Commit(ctx context.Context, actor datax.Actor, ref datax.TaskRef, rows []datax.ImportRow) (datax.CommitResult, error) {
	res := datax.CommitResult{}
	for _, r := range rows {
		loc, lerr := w.resolveLocation(ctx, r)
		if lerr != nil {
			return res, lerr
		}
		if loc.err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, *loc.err)
			continue
		}
		in := BinCreateInput{
			ShelfID:     loc.shelfID,
			ZoneID:      loc.zoneID,
			WarehouseID: loc.warehouseID,
			Code:        text(r, "code"),
			BinType:     text(r, "bin_type"),
		}
		if v := text(r, "layer"); v != "" {
			n := int(cellFloat(r, "layer"))
			in.Layer = &n
		}
		if v := text(r, "column_no"); v != "" {
			n := int(cellFloat(r, "column_no"))
			in.ColumnNo = &n
		}
		if v := text(r, "max_capacity"); v != "" {
			f := cellFloat(r, "max_capacity")
			in.MaxCapacity = &f
		}
		if _, err := w.svc.CreateBin(ctx, warehouseActor(actor), in); err != nil {
			res.FailedRows++
			res.Errors = append(res.Errors, rowErr(r.RowNo, "code", serviceErrMsg(err)))
			continue
		}
		res.SuccessRows++
	}
	return res, nil
}

// binLocation 层级定位结果。
type binLocation struct {
	warehouseID, zoneID, shelfID int64
	err                          *datax.RowError
}

// resolveLocation 仓库→库区→货架逐级解析（本域表只读；层级/启用任一不满足即行错误）。
func (w *BinImportWriter) resolveLocation(ctx context.Context, r datax.ImportRow) (binLocation, error) {
	wh, err := w.svc.repo.FindWarehouseByCode(ctx, text(r, "warehouse_code"))
	if err != nil {
		return binLocation{}, err
	}
	if wh == nil {
		return binLocation{err: rowErrP(r.RowNo, "warehouse_code", "仓库编码不存在")}, nil
	}
	zone, err := w.svc.repo.FindZoneByCode(ctx, wh.ID.Int64(), text(r, "zone_code"))
	if err != nil {
		return binLocation{}, err
	}
	if zone == nil {
		return binLocation{err: rowErrP(r.RowNo, "zone_code", "库区编码在该仓库下不存在")}, nil
	}
	if zone.Status != StatusEnabled {
		return binLocation{err: rowErrP(r.RowNo, "zone_code", "库区已停用")}, nil
	}
	shelf, err := w.svc.repo.FindShelfByCode(ctx, zone.ID.Int64(), text(r, "shelf_code"))
	if err != nil {
		return binLocation{}, err
	}
	if shelf == nil {
		return binLocation{err: rowErrP(r.RowNo, "shelf_code", "货架编码在该库区下不存在")}, nil
	}
	if shelf.Status != StatusEnabled {
		return binLocation{err: rowErrP(r.RowNo, "shelf_code", "货架已停用")}, nil
	}
	return binLocation{
		warehouseID: wh.ID.Int64(),
		zoneID:      zone.ID.Int64(),
		shelfID:     shelf.ID.Int64(),
	}, nil
}

func rowErrP(rowNo int, key, msg string) *datax.RowError {
	e := rowErr(rowNo, key, msg)
	return &e
}

// text 读文本列。
func text(r datax.ImportRow, key string) string { return r.Cells[key].S }

// cellFloat 读数值列。
func cellFloat(r datax.ImportRow, key string) float64 { return r.Cells[key].N }

// ---- 跨域编码解析器（router 桥接 purchase/sales/inventory 各域 datax_import.go）----

// BinRef 库位解析结果（内建类型；zone/shelf 随 bin 冗余返回——库存原语 RowKey 需要完整定位）。
type BinRef struct {
	WarehouseID int64
	ZoneID      int64
	ShelfID     int64
	BinID       int64
	Enabled     bool
	Found       bool
}

// DataxWarehouseResolver 仓库/库位编码解析器（只读；只读本域表——plan §2.2 规则①）。
type DataxWarehouseResolver struct {
	repo Repository
}

// NewDataxWarehouseResolver 构建解析器（router 装配注入各域 datax_import.go）。
func NewDataxWarehouseResolver(db *gorm.DB) *DataxWarehouseResolver {
	return &DataxWarehouseResolver{repo: newGormRepository(db)}
}

// ResolveWarehouseCode 仓库编码 → ID（含启用态）。
func (r *DataxWarehouseResolver) ResolveWarehouseCode(ctx context.Context, code string) (warehouseRef, error) {
	w, err := r.repo.FindWarehouseByCode(ctx, code)
	if err != nil {
		return warehouseRef{}, err
	}
	if w == nil {
		return warehouseRef{}, nil
	}
	return warehouseRef{ID: w.ID.Int64(), Enabled: w.Status == StatusEnabled, Found: true}, nil
}

// warehouseRef 仓库解析结果（导出给跨域文件的极简值类型）。
type warehouseRef struct {
	ID      int64
	Enabled bool
	Found   bool
}

// ResolveBinCode 仓库内库位编码 → 完整五维定位（库位存在但仓库/库区/货架停用仍返回
// 定位与 Enabled=false——由消费方按业务语义拒绝）。
func (r *DataxWarehouseResolver) ResolveBinCode(ctx context.Context, warehouseID int64, code string) (BinRef, error) {
	b, err := r.repo.FindBinByCode(ctx, warehouseID, code)
	if err != nil {
		return BinRef{}, err
	}
	if b == nil {
		return BinRef{}, nil
	}
	return BinRef{
		WarehouseID: b.WarehouseID.Int64(),
		ZoneID:      b.ZoneID.Int64(),
		ShelfID:     b.ShelfID.Int64(),
		BinID:       b.ID.Int64(),
		Enabled:     b.Status == StatusEnabled,
		Found:       true,
	}, nil
}

// NewWarehouseImportService 构建仓库/库位导入写入器的承载 Service（router 装配）。
//
// MT6 集成最小改动（backend-m3-plan §2.2 规则③一次性最小改动清单，唯一执行人=集成工程师）：
// 本域 repo 构造器（newGormRepository）未导出，路由装配无法构造 *Service，WAREHOUSE /
// LOCATION 两类导入写入器不可接线——按本导出构造器修复（纯新增，零既有改动）。
// CreateWarehouse/CreateBin 为本域内聚校验 + 落库，无跨域依赖注入项。
func NewWarehouseImportService(db *gorm.DB) *Service {
	return NewService(newGormRepository(db))
}
