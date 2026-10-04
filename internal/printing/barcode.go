package printing

// 条码/二维码 PNG 生成（printing.md §4.1 全量码制、§4.2 可生成的码；plan §7.2
// GET /api/prints/barcode——boombuler/barcode，供 Scan 端/调试/外部系统引用，
// 参数上限防滥用；条码 PNG 不落 files 表，预生成仅进进程内缓存，plan §4.2）。
//
// 码制与实现（github.com/boombuler/barcode v1.1.0，architecture §11.2 清单内选型）：
//   CODE128     code128.Encode（默认推荐——printing.md §4.1）
//   CODE39      code39.Encode（无校验位；全 ASCII 模式——小写内容可编码）
//   EAN13/EAN8  ean.Encode（12/13 位、7/8 位数字；校验位缺失自动补算、错误校验位拒绝）
//   UPC         UPC-A = EAN-13 前导 0 同构（12 位数字 → "0"+code 编码，业界标准映射）
//   QR          qr.Encode（纠错级别 M、自动模式——中英文/数字自适应）
//   DATAMATRIX  datamatrix.Encode
//
// UPC 映射与 DATAMATRIX 覆盖为本端点对模板五值域（CHECK 约束）的有意扩展：模板列
// 仅承载一维条码码制（000012 chk_print_templates_barcode_symbology），端点按
// printing.md §4.1 全量码制提供。

import (
	"bytes"
	"fmt"
	"image/png"
	"strconv"
	"sync"

	"github.com/boombuler/barcode"
	bccode128 "github.com/boombuler/barcode/code128"
	bccode39 "github.com/boombuler/barcode/code39"
	bcdatamatrix "github.com/boombuler/barcode/datamatrix"
	bcean "github.com/boombuler/barcode/ean"
	bcqr "github.com/boombuler/barcode/qr"

	"github.com/stockflow/server/internal/response"
)

// 端点码制值域（模板五值 + QR + DATAMATRIX；SymbologyQR 供 render handler
// 预生成与端点共用）。
const (
	SymbologyQR         = "QR"
	SymbologyDataMatrix = "DATAMATRIX"
)

var endpointSymbologies = map[string]bool{
	SymbologyCode128: true, SymbologyCode39: true, SymbologyEAN13: true, SymbologyEAN8: true,
	SymbologyUPC: true, SymbologyQR: true, SymbologyDataMatrix: true,
}

// 端点尺寸参数（plan §7.2"参数上限防滥用"；缺省值即 render handler 预生成值，
// 前端以缺省参数引用即命中缓存）。
const (
	// DefaultBarcodeWidth/Height 一维条码缺省像素（300dpi 下约 25mm 高，扫码枪可识别）。
	DefaultBarcodeWidth  = 320
	DefaultBarcodeHeight = 120
	// DefaultQRSize 二维码缺省边长（正方形）。
	DefaultQRSize = 240
	// MaxBarcodeDimension 单边像素上限（防滥用：2000×2000 PNG 上限约数 MB）。
	MaxBarcodeDimension = 2000
	// MaxBarcodeTextLen 内容长度上限（Code128 理论上限远超此值；防滥用边界）。
	MaxBarcodeTextLen = 512
)

// RenderBarcodePNG 生成条码/二维码 PNG（进程内缓存优先——同参数重复请求与 render
// 预生成共享缓存）。symbology ∈ 端点值域；width/height ≤0 取缺省（按码制），
// 超上限 4xx；内容与码制不符 4xx PRINT_BARCODE_CONTENT_INVALID（如 EAN13 非数字）。
func (s *Service) RenderBarcodePNG(text, symbology string, width, height int) ([]byte, error) {
	if text == "" || len(text) > MaxBarcodeTextLen {
		return nil, response.NewError(ErrBarcodeTextInvalid, map[string]any{
			"max_len": MaxBarcodeTextLen, "actual": len(text),
		})
	}
	if !endpointSymbologies[symbology] {
		return nil, response.NewError(ErrSymbologyInvalid, map[string]any{
			"barcode_symbology": symbology,
			"allowed":           []string{SymbologyCode128, SymbologyCode39, SymbologyEAN13, SymbologyEAN8, SymbologyUPC, SymbologyQR, SymbologyDataMatrix},
		})
	}
	width, height, err := normalizeDimensions(symbology, width, height)
	if err != nil {
		return nil, err
	}

	key := cacheKey(symbology, text, width, height)
	if b, ok := s.cache.get(key); ok {
		return b, nil
	}
	b, err := renderPNGBytes(text, symbology, width, height)
	if err != nil {
		return nil, err
	}
	s.cache.put(key, b)
	return b, nil
}

// normalizeDimensions 尺寸归一：≤0 取码制缺省；上限校验。
func normalizeDimensions(symbology string, width, height int) (int, int, error) {
	if width <= 0 {
		width = DefaultBarcodeWidth
	}
	if height <= 0 {
		height = DefaultBarcodeHeight
	}
	if symbology == SymbologyQR || symbology == SymbologyDataMatrix {
		// 二维码正方形：width/height 任一提供即取其大者（另一边补齐）。
		if width != DefaultBarcodeWidth || height != DefaultBarcodeHeight {
			size := width
			if height > size {
				size = height
			}
			width, height = size, size
		} else {
			width, height = DefaultQRSize, DefaultQRSize
		}
	}
	if width > MaxBarcodeDimension || height > MaxBarcodeDimension {
		return 0, 0, response.NewError(ErrBarcodeSizeInvalid, map[string]any{
			"max": MaxBarcodeDimension, "width": width, "height": height,
		})
	}
	return width, height, nil
}

// renderPNGBytes 编码 + 缩放 + PNG 编码（boombuler：Encode → barcode.Scale → png.Encode）。
func renderPNGBytes(text, symbology string, width, height int) ([]byte, error) {
	bc, err := encodeBarcode(text, symbology)
	if err != nil {
		return nil, err
	}
	scaled, err := barcode.Scale(bc, width, height)
	if err != nil {
		// 目标尺寸小于码制最小模块尺寸（如密集 QR 请求 10×10）。
		return nil, response.NewError(ErrBarcodeSizeInvalid, map[string]any{
			"width": width, "height": height, "min_bounds": fmt.Sprintf("%dx%d", bc.Bounds().Dx(), bc.Bounds().Dy()),
		})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return nil, fmt.Errorf("printing: PNG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// encodeBarcode 按码制分派编码器（内容校验错误归一为 4xx PRINT_BARCODE_CONTENT_INVALID）。
func encodeBarcode(text, symbology string) (barcode.Barcode, error) {
	invalid := func() (barcode.Barcode, error) {
		return nil, response.NewError(ErrBarcodeContentInvalid, map[string]any{
			"barcode_symbology": symbology, "text_len": len(text),
		})
	}
	switch symbology {
	case SymbologyCode128:
		bc, err := bccode128.Encode(text)
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyCode39:
		bc, err := bccode39.Encode(text, false, true) // 无校验位；全 ASCII 模式
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyEAN13:
		// ean.Encode 自动识别 8/13 位——显式码制须校验位数（EAN13 = 12 位补校验位或 13 位全码）。
		if !eanDigits(text, 12) && !eanDigits(text, 13) {
			return invalid()
		}
		bc, err := bcean.Encode(text)
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyEAN8:
		if !eanDigits(text, 7) && !eanDigits(text, 8) {
			return invalid()
		}
		bc, err := bcean.Encode(text)
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyUPC:
		// UPC-A 共 12 位（11 数据 + 校验位）；与 EAN-13 前导 0 同构——业界标准映射。
		if len(text) != 12 || !allDigits(text) {
			return invalid()
		}
		bc, err := bcean.Encode("0" + text)
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyQR:
		bc, err := bcqr.Encode(text, bcqr.M, bcqr.Auto)
		if err != nil {
			return invalid()
		}
		return bc, nil
	case SymbologyDataMatrix:
		bc, err := bcdatamatrix.Encode(text)
		if err != nil {
			return invalid()
		}
		return bc, nil
	}
	return invalid()
}

// eanDigits 数字位数校验（全数字且指定长度）。
func eanDigits(text string, n int) bool {
	return len(text) == n && allDigits(text)
}

func allDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// ---- 进程内 PNG 缓存（plan §4.2"预生成与进程内缓存"；有界 FIFO，防无界内存增长
// ——go-dev-standard 规则 15/无界内存红线；容量按 500 行任务 × 每行 2 图（主码+二维码）
// = 1000 图设计，峰值约数 MB）----

const pngCacheMaxEntries = 2048

type pngCache struct {
	mu      sync.Mutex
	maxEnts int
	entries map[string][]byte
	order   []string // FIFO 淘汰序
}

func newPNGCache() *pngCache {
	return &pngCache{
		maxEnts: pngCacheMaxEntries,
		entries: make(map[string][]byte, 64),
	}
}

func cacheKey(symbology, text string, width, height int) string {
	return symbology + "|" + text + "|" + strconv.Itoa(width) + "x" + strconv.Itoa(height)
}

func (c *pngCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.entries[key]
	return b, ok
}

func (c *pngCache) put(key string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.entries[key]; !dup {
		c.order = append(c.order, key)
	}
	c.entries[key] = b
	for len(c.entries) > c.maxEnts && len(c.order) > 0 {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, evict)
	}
}
