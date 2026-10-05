package printing

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
)

// printing 域 GORM 模型（迁移 000012_create_printing_tables.up.sql 列结构，
// backend-m3-plan §5 000012 冻结 DDL）。不做软删除：模板停用走 status=DISABLED，
// 任务失败走状态机（迁移头注释同口径）。

// ---- 值域（与迁移 CHECK 约束同源；printing.md §1.1 中 F12 首批 9 类，
// 与 web/src/api/printing.ts 先行枚举一致，plan §5 000012）----

// 模板/任务对象类型（chk_print_templates_object_type / chk_print_tasks_object_type）。
const (
	ObjectSKULabel      = "SKU_LABEL"      // SKU 标签
	ObjectBinLabel      = "BIN_LABEL"      // 库位标签
	ObjectCartonCode    = "CARTON_CODE"    // 箱码
	ObjectPalletCode    = "PALLET_CODE"    // 托盘码
	ObjectInboundOrder  = "INBOUND_ORDER"  // 入库单
	ObjectOutboundOrder = "OUTBOUND_ORDER" // 出库单
	ObjectPickOrder     = "PICK_ORDER"     // 拣货单
	ObjectCountOrder    = "COUNT_ORDER"    // 盘点单
	ObjectShipmentOrder = "SHIPMENT_ORDER" // 发货单
)

// objectTypes 九类对象类型（值域判定用；顺序即预设/文档顺序）。
var objectTypes = []string{
	ObjectSKULabel, ObjectBinLabel, ObjectCartonCode, ObjectPalletCode,
	ObjectInboundOrder, ObjectOutboundOrder, ObjectPickOrder, ObjectCountOrder, ObjectShipmentOrder,
}

// ObjectTypeValid 对象类型值域判定。
func ObjectTypeValid(t string) bool {
	for _, v := range objectTypes {
		if v == t {
			return true
		}
	}
	return false
}

// isLabelTypes 标签类对象（热敏/标签纸，一码一页；symbology 必填——plan §7.1）。
// 与前端 isLabelObjectType（web/src/api/printing.ts）同源。
var isLabelTypes = map[string]bool{
	ObjectSKULabel: true, ObjectBinLabel: true, ObjectCartonCode: true, ObjectPalletCode: true,
}

// IsLabelObjectType 标签类判定（plan §7.1：symbology 仅标签类必填）。
func IsLabelObjectType(t string) bool { return isLabelTypes[t] }

// 内置承接对象：托盘/箱码业务域未建（plan §15），data_ids 即码值原文，
// 由本包内置 reader 承接（plan §7.2）。
var builtinObjectTypes = map[string]bool{
	ObjectCartonCode: true, ObjectPalletCode: true,
}

// IsBuiltinObjectType 是否为内置承接对象（data_ids 为码值原文，不经域包 reader）。
func IsBuiltinObjectType(t string) bool { return builtinObjectTypes[t] }

// 纸张规格（chk_*_paper；printing.md §6 多纸张，与前端 PRINT_PAPERS 同源）。
const (
	PaperA4          = "A4"
	PaperA5          = "A5"
	PaperThermal4030 = "THERMAL_40_30"
	PaperThermal6040 = "THERMAL_60_40"
	PaperThermal1005 = "THERMAL_100_50"
)

var papers = map[string]bool{
	PaperA4: true, PaperA5: true, PaperThermal4030: true, PaperThermal6040: true, PaperThermal1005: true,
}

// PaperValid 纸张值域判定。
func PaperValid(p string) bool { return papers[p] }

// 模板条码码制（chk_print_templates_barcode_symbology；模板列只承载一维条码码制，
// 二维码经 qrcode_enabled 开关、DATAMATRIX 仅条码端点支持——printing.md §4.1 全量
// 码制在 barcode.go 值域）。
const (
	SymbologyCode128 = "CODE128"
	SymbologyCode39  = "CODE39"
	SymbologyEAN13   = "EAN13"
	SymbologyEAN8    = "EAN8"
	SymbologyUPC     = "UPC"
)

var templateSymbologies = map[string]bool{
	SymbologyCode128: true, SymbologyCode39: true, SymbologyEAN13: true, SymbologyEAN8: true, SymbologyUPC: true,
}

// TemplateSymbologyValid 模板码制值域判定。
func TemplateSymbologyValid(s string) bool { return templateSymbologies[s] }

// 模板/任务状态（chk_print_templates_status / chk_print_tasks_status）。
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"

	TaskStatusQueued     = "QUEUED"     // 排队（创建后入队 render 前）
	TaskStatusProcessing = "PROCESSING" // 生成中（render handler 执行中）
	TaskStatusSuccess    = "SUCCESS"    // 完成（条码预生成结束）
	TaskStatusFailed     = "FAILED"     // 失败（error_message 承载原因）
)

// 执行确认结果（chk_print_tasks_result；NULL=尚未确认——plan §13.2 回填一次）。
const (
	ResultSuccess = "SUCCESS"
	ResultFailed  = "FAILED"
)

// docPrefix 打印任务单号前缀（docnum frozenRules "PT"，plan §12.3；
// 有意避开 PRT——api §7 prt: 为采购退货预占幂等键动作前缀）。
const docPrefix = "PT"

// ---- jsonb 载体（列 print_templates.fields / print_tasks.template_snapshot /
// print_task_rows.values / print_task_rows.lines；形态对齐 internal/returns jsonb 约定）----

type jsonb json.RawMessage

func (j jsonb) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

func (j *jsonb) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(jsonb(nil), v...)
	case string:
		*j = jsonb(v)
	default:
		return fmt.Errorf("printing.jsonb.Scan 不支持类型 %T", src)
	}
	return nil
}

func (j jsonb) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *jsonb) UnmarshalJSON(b []byte) error {
	*j = append(jsonb(nil), b...)
	return nil
}

func marshalJSONB(v any) jsonb {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return jsonb(`{"error":"snapshot_marshal_failed"}`)
	}
	return jsonb(b)
}

// ---- 表模型 ----

// PrintTemplate 打印模板（迁移 000012 print_templates；printing.md §2 模板体系）。
// fields 为字段绑定键值 jsonb 对象 {绑定键: 预设文案}（键 = object_type 预设键子集，
// plan §7.1；文案取自本包 fields.go 预设注册表，不信前端传入文案）。
type PrintTemplate struct {
	ID               database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name             string            `gorm:"column:name;size:128" json:"name"`
	ObjectType       string            `gorm:"column:object_type;size:32" json:"object_type"`
	Paper            string            `gorm:"column:paper;size:32" json:"paper"`
	BarcodeSymbology *string           `gorm:"column:barcode_symbology;size:16" json:"barcode_symbology"`
	QRCodeEnabled    bool              `gorm:"column:qrcode_enabled" json:"qrcode_enabled"`
	Fields           jsonb             `gorm:"column:fields;type:jsonb" json:"fields"`
	HeaderText       string            `gorm:"column:header_text;size:255" json:"header_text"`
	Status           string            `gorm:"column:status;size:16" json:"status"`
	Remark           string            `gorm:"column:remark" json:"remark"`
	CreatedAt        database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy        int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy        int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式表名。
func (PrintTemplate) TableName() string { return "print_templates" }

// FieldMap 反序列化字段绑定键值（损坏 JSON 返回空 map，不静默丢数据——原样保留
// 由审计行承载，此处只影响读视图）。
func (t *PrintTemplate) FieldMap() map[string]string {
	out := map[string]string{}
	if len(t.Fields) == 0 {
		return out
	}
	_ = json.Unmarshal([]byte(t.Fields), &out)
	return out
}

// SnapshotFromTemplate 冻结模板快照（任务创建时调用——后续模板修改不影响已建任务，
// plan §7.2；快照即前端预览的渲染依据）。
func SnapshotFromTemplate(t *PrintTemplate) TemplateSnapshot {
	snap := TemplateSnapshot{
		Name:          t.Name,
		ObjectType:    t.ObjectType,
		Paper:         t.Paper,
		QRCodeEnabled: t.QRCodeEnabled,
		HeaderText:    t.HeaderText,
		Fields:        t.FieldMap(),
	}
	if t.BarcodeSymbology != nil {
		snap.BarcodeSymbology = *t.BarcodeSymbology
	}
	return snap
}

// TemplateSnapshot 模板快照（print_tasks.template_snapshot 存储形态；与前端
// PrintTemplateSnapshot 同构，snake_case 由前端对齐轮回对——plan §12.4）。
type TemplateSnapshot struct {
	Name             string            `json:"name"`
	ObjectType       string            `json:"object_type"`
	Paper            string            `json:"paper"`
	BarcodeSymbology string            `json:"barcode_symbology,omitempty"`
	QRCodeEnabled    bool              `json:"qrcode_enabled"`
	Fields           map[string]string `json:"fields,omitempty"`
	HeaderText       string            `json:"header_text,omitempty"`
}

// snapshotJSONB 序列化快照（序列化失败落占位 JSON——快照列 NOT NULL 语义不受影响）。
func snapshotJSONB(s TemplateSnapshot) jsonb { return marshalJSONB(s) }

// PrintTask 打印任务（迁移 000012 print_tasks；printing.md §1.2 任务模型）。
// status 为 render 队列态（QUEUED→PROCESSING→SUCCESS/FAILED，装配在任务创建时
// 同步完成，render 仅预生成条码 PNG）；result 为执行确认结果（回填一次）。
type PrintTask struct {
	ID               database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	PrintNo          string            `gorm:"column:print_no;size:64" json:"print_no"`
	ObjectType       string            `gorm:"column:object_type;size:32" json:"object_type"`
	TemplateID       int64             `gorm:"column:template_id" json:"template_id"`
	TemplateSnapshot jsonb             `gorm:"column:template_snapshot;type:jsonb" json:"template_snapshot"`
	Paper            string            `gorm:"column:paper;size:32" json:"paper"`
	Copies           int               `gorm:"column:copies" json:"copies"`
	TotalCount       int               `gorm:"column:total_count" json:"total_count"`
	Status           string            `gorm:"column:status;size:16" json:"status"`
	Result           *string           `gorm:"column:result;size:16" json:"result"`
	PrintedBy        int64             `gorm:"column:printed_by" json:"printed_by"`
	PrintedAt        database.JSONTime `gorm:"column:printed_at" json:"printed_at"`
	ErrorMessage     string            `gorm:"column:error_message" json:"error_message"`
	CreatedAt        database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy        int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy        int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式表名。
func (PrintTask) TableName() string { return "print_tasks" }

// Snapshot 反序列化模板快照（损坏 JSON 返回零值，error_message/审计行承载原值）。
func (t *PrintTask) Snapshot() TemplateSnapshot {
	var s TemplateSnapshot
	if len(t.TemplateSnapshot) == 0 {
		return s
	}
	_ = json.Unmarshal([]byte(t.TemplateSnapshot), &s)
	return s
}

// PrintTaskRow 打印任务行（迁移 000012 print_task_rows + 000019 data_id 列；渲染数据包——
// code=主码内容（SKU 条码/库位编码/箱码/托盘码/单据号），values=字段绑定取值快照，
// lines=单据明细行，data_id=行业务身份快照。重投递整体重写覆盖，装配为纯函数幂等
// （plan §7.2））。
type PrintTaskRow struct {
	ID        database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID    int64             `gorm:"column:task_id" json:"task_id"`
	Seq       int               `gorm:"column:seq" json:"seq"`
	Code      string            `gorm:"column:code;size:255" json:"code"`
	Values    jsonb             `gorm:"column:values;type:jsonb" json:"values"`
	Lines     jsonb             `gorm:"column:lines;type:jsonb" json:"lines"`
	DataID    string            `gorm:"column:data_id;size:64" json:"data_id"`
	CreatedAt database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式表名。
func (PrintTaskRow) TableName() string { return "print_task_rows" }

// RowValues 反序列化字段绑定取值（损坏 JSON 返回空 map）。
func (r *PrintTaskRow) RowValues() map[string]string {
	out := map[string]string{}
	if len(r.Values) == 0 {
		return out
	}
	_ = json.Unmarshal([]byte(r.Values), &out)
	return out
}

// RowLines 反序列化单据明细行（损坏 JSON 返回空数组；标签类行为空）。
func (r *PrintTaskRow) RowLines() []map[string]string {
	out := []map[string]string{}
	if len(r.Lines) == 0 {
		return out
	}
	_ = json.Unmarshal([]byte(r.Lines), &out)
	return out
}
