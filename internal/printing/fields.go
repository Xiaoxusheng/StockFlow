package printing

import (
	"sort"

	"github.com/stockflow/server/internal/response"
)

// 模板字段绑定预设注册表（plan §7.1：预设清单与 web/src/api/printing.ts
// PRINT_TEMPLATE_FIELD_PRESETS 冻结同源——新增/修改键必须先改前端预设再改此处，
// 两表逐键一致；fields 绑定校验 = object_type 预设键子集）。
//
// 键语义（渲染层由前端 PrintContentRenderer 按 values 键取值）：
//   - 标签类（SKU/BIN/CARTON/PALLET）：values 为单值字段；
//   - 单据类：values 为表头字段，lines 为明细行（行键见 lineFieldOrder——与前端
//     PRINT_LINE_FIELD_LABELS 同源）。
//
// 单一真相来源：绑定的展示文案由本注册表派生（保存时后端按键回填文案，不信前端
// 传入的 label），前端表单仅提供键集合。

// FieldPreset 一个可绑定字段（键 + 展示文案）。
type FieldPreset struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// fieldPresets 各对象类型可绑定字段预设（键序即渲染顺序；与
// web/src/api/printing.ts PRINT_TEMPLATE_FIELD_PRESETS 逐键同源冻结）。
var fieldPresets = map[string][]FieldPreset{
	ObjectSKULabel: {
		{Key: "sku_code", Label: "SKU编码"},
		{Key: "product_name", Label: "商品名称"},
		{Key: "spec", Label: "规格"},
		{Key: "unit", Label: "单位"},
		{Key: "qty", Label: "数量"},
	},
	ObjectBinLabel: {
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "zone_name", Label: "库区"},
		{Key: "shelf_code", Label: "货架"},
		{Key: "bin_code", Label: "库位编码"},
	},
	ObjectCartonCode: {
		{Key: "carton_code", Label: "箱码"},
		{Key: "order_no", Label: "关联单号"},
		{Key: "box_seq", Label: "箱序"},
		{Key: "total_qty", Label: "箱内数量"},
	},
	ObjectPalletCode: {
		{Key: "pallet_code", Label: "托盘码"},
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "total_qty", Label: "托盘总量"},
		{Key: "customer_name", Label: "客户"},
	},
	ObjectInboundOrder: {
		{Key: "order_no", Label: "单据号"},
		{Key: "supplier_name", Label: "供应商"},
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "inbound_at", Label: "入库时间"},
		{Key: "operator", Label: "操作人"},
	},
	ObjectOutboundOrder: {
		{Key: "order_no", Label: "单据号"},
		{Key: "customer_name", Label: "客户"},
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "outbound_at", Label: "出库时间"},
		{Key: "operator", Label: "操作人"},
	},
	ObjectPickOrder: {
		{Key: "order_no", Label: "单据号"},
		{Key: "picker_name", Label: "拣货人"},
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "pick_at", Label: "拣货时间"},
	},
	ObjectCountOrder: {
		{Key: "order_no", Label: "单据号"},
		{Key: "warehouse_name", Label: "仓库"},
		{Key: "counter_name", Label: "盘点人"},
		{Key: "count_at", Label: "盘点时间"},
	},
	ObjectShipmentOrder: {
		{Key: "order_no", Label: "单据号"},
		{Key: "customer_name", Label: "客户"},
		{Key: "carrier_name", Label: "承运商"},
		{Key: "shipment_at", Label: "发货时间"},
		{Key: "operator", Label: "发货人"},
	},
}

// lineFieldOrder 单据明细行列键（与前端 PRINT_LINE_FIELD_LABELS 同源；预设键优先，
// 其余键按装配原始顺序殿后——渲染层排序口径一致）。
var lineFieldOrder = []string{
	"line_no", "bin_code", "sku_code", "product_name", "spec", "unit", "batch_no", "qty", "actual_qty",
}

// FieldPresetsFor 返回对象类型的字段预设（未注册对象类型返回 false——值域在
// ObjectTypeValid 已先行拦截，此处兜底）。
func FieldPresetsFor(objectType string) ([]FieldPreset, bool) {
	p, ok := fieldPresets[objectType]
	return p, ok
}

// validateFields 校验并规范化字段绑定（plan §7.1：object_type 预设键子集）：
// 去重保序；未知键报 PRINT_FIELDS_INVALID（details 携带非法键）；空绑定合法
// （DDL fields DEFAULT '{}'——模板可只渲染主码内容）。返回 键→预设文案 map
// （存储形态 print_templates.fields jsonb 对象）。
func validateFields(objectType string, keys []string) (map[string]string, error) {
	presets, ok := fieldPresets[objectType]
	if !ok {
		return nil, response.NewError(ErrObjectTypeInvalid, map[string]any{"object_type": objectType})
	}
	allowed := make(map[string]string, len(presets))
	for _, p := range presets {
		allowed[p.Key] = p.Label
	}
	seen := make(map[string]bool, len(keys))
	out := make(map[string]string, len(keys))
	var unknown []string
	for _, k := range keys {
		if k == "" {
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		label, ok := allowed[k]
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		out[k] = label
	}
	if len(unknown) > 0 {
		return nil, response.NewError(ErrFieldsInvalid, map[string]any{
			"object_type": objectType, "unknown_keys": unknown,
		})
	}
	return out, nil
}

// orderedFieldList 字段绑定按预设键序输出（读视图形态 [{key,label}]；
// 未在预设序中的存量键殿后——防御手改数据）。
func orderedFieldList(objectType string, fields map[string]string) []FieldPreset {
	presets, ok := fieldPresets[objectType]
	out := make([]FieldPreset, 0, len(fields))
	if ok {
		for _, p := range presets {
			if label, bound := fields[p.Key]; bound {
				out = append(out, FieldPreset{Key: p.Key, Label: label})
			}
		}
	}
	if len(out) == len(fields) {
		return out
	}
	bound := make(map[string]bool, len(out))
	for _, f := range out {
		bound[f.Key] = true
	}
	// 存量非预设键（防御）：按字母序殿后，不丢弃。
	var rest []string
	for k := range fields {
		if !bound[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, FieldPreset{Key: k, Label: fields[k]})
	}
	return out
}
