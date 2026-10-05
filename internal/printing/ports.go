package printing

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/stockflow/server/internal/asynqx"
)

// 跨域窄接口与装配端口（backend-m3-plan §12.2 唯一跨域机制：消费方在本包定义最小
// 接口，实现落各域包 printing_content.go（§2.2 冻结清单——命名以消费方包为前缀），
// router 装配经 Option 注入；本包禁止 import 任何业务域包，域包 → 平台包为合法
// import 方向（plan §2.3 判据 2））。
//
// ContentReader ×9（plan §12.2 冻结签名 Assemble(ctx, ids, fields)）：
//
//	object_type      实现方
//	SKU_LABEL        masterdata/printing_content.go  NewSKUContentReader
//	BIN_LABEL        warehouse/printing_content.go   NewBinContentReader
//	INBOUND_ORDER    purchase/printing_content.go    NewInboundContentReader
//	OUTBOUND_ORDER   sales/printing_content.go       NewOutboundContentReader
//	PICK_ORDER       sales/printing_content.go       NewPickContentReader
//	SHIPMENT_ORDER   sales/printing_content.go       NewShipmentContentReader
//	COUNT_ORDER      stockops/printing_content.go    NewCountContentReader
//	CARTON_CODE      本包内置 builtinReader（data_ids 即码值原文，plan §7.2）
//	PALLET_CODE      本包内置 builtinReader（同上）

// ContentRow 打印内容行（装配结果；签名一律内建类型/本包值类型，plan §12.2 规则：
// 不携带域类型）。
type ContentRow struct {
	// ID 行业务 ID（十进制文本；内置码值承接场景即码值原文——plan §7.2）。
	ID string `json:"id"`
	// Code 主码内容：SKU 条码 / 库位编码 / 箱码 / 托盘码 / 单据号（printing.md §4.2）。
	Code string `json:"code"`
	// Values 字段绑定取值（键 = 预设键子集；未装配的键不出现——渲染层留空展示）。
	Values map[string]string `json:"values,omitempty"`
	// Lines 单据明细行（仅单据类；行键对齐 lineFieldOrder，与前端
	// PRINT_LINE_FIELD_LABELS 同源）。
	Lines []map[string]string `json:"lines,omitempty"`
}

// ContentReader 打印数据装配窄接口（plan §12.2 冻结：模板字段绑定填充）。
//
// 实现约定（plan §7.2）：逐对象校验存在 + 按模板 fields 绑定装配 values/lines +
// 主码内容；任一对象不存在 → 返回 printing.NewDataNotFoundError(缺失标识)（整体
// 拒绝——打印数据必须完整，禁止部分行静默缺失）；基础设施错误返回 error。
type ContentReader interface {
	Assemble(ctx context.Context, ids []string, fields []string) ([]ContentRow, error)
}

// builtinReader 内置承接 reader（CARTON_CODE/PALLET_CODE：托盘/箱码业务域未建，
// data_ids 即码值原文，不装配业务字段——plan §7.2；真实托盘域立项后替换）。
type builtinReader struct{}

func (builtinReader) Assemble(_ context.Context, ids []string, _ []string) ([]ContentRow, error) {
	rows := make([]ContentRow, 0, len(ids))
	var missing []string
	for _, id := range ids {
		if id == "" {
			missing = append(missing, id)
			continue
		}
		rows = append(rows, ContentRow{ID: id, Code: id})
	}
	if len(missing) > 0 {
		return nil, NewDataNotFoundError(missing)
	}
	return rows, nil
}

// readerFor 对象类型 → reader（builtin 优先；其余取装配注册表）。
func (s *Service) readerFor(objectType string) (ContentReader, bool) {
	if IsBuiltinObjectType(objectType) {
		return builtinReader{}, true
	}
	r, ok := s.opt.readers[objectType]
	return r, ok
}

// ParseIDs 对象标识解析：十进制文本 → int64（域包 reader 共用；非数字/越界标识
// 归入缺失列表，由调用方统一报 PRINT_DATA_NOT_FOUND——标识形态对用户输入宽容，
// 不产生 500）。
//
// 通道纪律（docs/qr-code.md §8）：本通道恒为 SKU 数字 ID 的十进制文本（String(sku.id)），
// SKU 编码/SFQR 协议载荷绝不混入——SKU 编码正则允许纯数字，编码 "42" 混入会被装配成
// skus.id=42 的另一 SKU 标签（静默错绑打错货）。
func ParseIDs(ids []string) (parsed []int64, missing []string) {
	for _, id := range ids {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			missing = append(missing, id)
			continue
		}
		parsed = append(parsed, n)
	}
	return parsed, missing
}

// FieldFilter 绑定键集合（装配实现按需取值的判定依据；fields 为模板绑定的预设键序数组）。
func FieldFilter(fields []string) map[string]bool {
	set := make(map[string]bool, len(fields))
	for _, f := range fields {
		set[f] = true
	}
	return set
}

// FormatID 业务 ID → 十进制文本（ContentRow.ID / 缺失标识统一形态）。
func FormatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

// FailMissing 存在缺失标识时返回整体拒绝错误（装配契约：任一对象缺失整体报
// PRINT_DATA_NOT_FOUND——plan §7.2，禁止部分行静默缺失）；nil 表示无缺失。
func FailMissing(missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	return NewDataNotFoundError(missing)
}

// ---- Option 装配（plan §3.1：仅用于跨域消费接口注入，不得携带业务配置）----

type options struct {
	readers map[string]ContentReader
	queue   asynqx.Queue
	log     *zap.Logger
}

// Option RegisterRoutes / NewService 的可选注入项。
type Option func(*options)

// WithLogger 注入域内日志（router 装配：logger 基座实例；未注入时降级 nop——单测场景）。
func WithLogger(l *zap.Logger) Option { return func(o *options) { o.log = l } }

// WithContentReader 注入对象类型的装配 reader（router 装配：各域 printing_content.go
// 导出构造器；同一对象类型重复注入以后者为准视为装配错误——RegisterRoutes 启动期
// 不允许，测试替身注入用）。
func WithContentReader(objectType string, r ContentReader) Option {
	return func(o *options) {
		if o.readers == nil {
			o.readers = map[string]ContentReader{}
		}
		o.readers[objectType] = r
	}
}

// WithQueue 注入异步队列（router 装配：asynqx.Runtime.Queue——plan §4.1 双实现，
// redis.enabled=false 时为 inline 同步降级）。
func WithQueue(q asynqx.Queue) Option { return func(o *options) { o.queue = q } }
