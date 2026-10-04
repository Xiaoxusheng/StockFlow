package printing

// 条码/二维码生成单测（ask 交付项：条码用生成的字节断言非空与格式；boombuler/barcode
// 渲染为纯函数，无网络/DB 依赖；尺寸参数与内容校验矩阵覆盖 plan §7.2"参数上限防滥用"）。

import (
	"bytes"
	"image"
	"image/png"
	"strconv"
	"strings"
	"testing"
)

// pngConfigBytes 断言字节为合法 PNG 并返回尺寸。
func pngConfigBytes(t *testing.T, b []byte) (w, h int) {
	t.Helper()
	if len(b) == 0 {
		t.Fatalf("PNG 字节非空断言失败")
	}
	// PNG 签名：\x89PNG\r\n\x1a\n。
	if !bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("字节缺少 PNG 签名")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("PNG 解码失败: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestRenderBarcodePNG_AllSymbologiesNonEmptyPNG(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		symbology string
		text      string
	}{
		{SymbologyCode128, "SF-BOX-2026-0001"},          // 默认推荐码制（printing.md §4.1）
		{SymbologyCode39, "ABC-1234/XYZ"},               // Code39 标准字符集（194 模块）
		{SymbologyCode39, "ok"},                         // 全 ASCII 模式小写（转义序列占 2 字符位）
		{SymbologyEAN13, "6901234567892"},               // 13 位全码（校验位合法）
		{SymbologyEAN13, "690123456789"},                // 12 位自动补校验位
		{SymbologyEAN8, "12345670"},                     // EAN-8 全码
		{SymbologyUPC, "036000291452"},                  // UPC-A 12 位（EAN-13 前导 0 同构）
		{SymbologyQR, "https://sf.example/bin/A-01-03"}, // 二维码
		{SymbologyDataMatrix, "PALLET-0001"},            // DataMatrix（printing.md §4.1 清单）
	}
	for _, tc := range cases {
		b, err := env.svc.RenderBarcodePNG(tc.text, tc.symbology, 0, 0)
		if err != nil {
			t.Fatalf("%s(%q) 生成失败: %v", tc.symbology, tc.text, err)
		}
		w, h := pngConfigBytes(t, b)
		if w <= 0 || h <= 0 {
			t.Fatalf("%s 尺寸非法: %dx%d", tc.symbology, w, h)
		}
	}
}

func TestRenderBarcodePNG_DefaultAndExplicitDimensions(t *testing.T) {
	env := newTestEnv(t)
	// 缺省尺寸（一维 320×120）。
	b, err := env.svc.RenderBarcodePNG("SF-1", SymbologyCode128, 0, 0)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if w, h := pngConfigBytes(t, b); w != DefaultBarcodeWidth || h != DefaultBarcodeHeight {
		t.Fatalf("缺省尺寸应为 %dx%d，得到 %dx%d", DefaultBarcodeWidth, DefaultBarcodeHeight, w, h)
	}
	// 显式尺寸。
	b, err = env.svc.RenderBarcodePNG("SF-1", SymbologyCode128, 500, 80)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if w, h := pngConfigBytes(t, b); w != 500 || h != 80 {
		t.Fatalf("显式尺寸不符: %dx%d", w, h)
	}
	// 二维码缺省正方形。
	b, err = env.svc.RenderBarcodePNG("SF-1", SymbologyQR, 0, 0)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if w, h := pngConfigBytes(t, b); w != DefaultQRSize || h != DefaultQRSize {
		t.Fatalf("二维码缺省应为 %d 正方形，得到 %dx%d", DefaultQRSize, w, h)
	}
}

func TestRenderBarcodePNG_ParamGuards(t *testing.T) {
	env := newTestEnv(t)
	// 内容为空。
	_, err := env.svc.RenderBarcodePNG("", SymbologyCode128, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_TEXT_INVALID")
	// 内容超长。
	_, err = env.svc.RenderBarcodePNG(strings.Repeat("x", MaxBarcodeTextLen+1), SymbologyCode128, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_TEXT_INVALID")
	// 码制值域外（模板五值 + QR + DATAMATRIX 之外）。
	_, err = env.svc.RenderBarcodePNG("x", "PDF417", 0, 0)
	asPrintErr(t, err, "PRINT_SYMBOLOGY_INVALID")
	// 尺寸超上限（防滥用）。
	_, err = env.svc.RenderBarcodePNG("x", SymbologyCode128, MaxBarcodeDimension+1, 100)
	asPrintErr(t, err, "PRINT_BARCODE_SIZE_INVALID")
	// 内容与码制不符：EAN13 非数字 / 位数错误。
	_, err = env.svc.RenderBarcodePNG("ABCDEF", SymbologyEAN13, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_CONTENT_INVALID")
	_, err = env.svc.RenderBarcodePNG("1234", SymbologyEAN13, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_CONTENT_INVALID")
	// EAN13 校验位错误。
	_, err = env.svc.RenderBarcodePNG("6901234567891", SymbologyEAN13, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_CONTENT_INVALID")
	// UPC 必须为 12 位数字。
	_, err = env.svc.RenderBarcodePNG("12345", SymbologyUPC, 0, 0)
	asPrintErr(t, err, "PRINT_BARCODE_CONTENT_INVALID")
	// 尺寸小于码制最小模块（200 字母数字 QR 为 49×49 模块，30×30 无法缩放）。
	_, err = env.svc.RenderBarcodePNG(strings.Repeat("A", 200), SymbologyQR, 30, 30)
	asPrintErr(t, err, "PRINT_BARCODE_SIZE_INVALID")
	// 非数字尺寸参数在 handler 层拦截（此处 Service 层容错为缺省）。
	if _, err := env.svc.RenderBarcodePNG("x", SymbologyCode128, -5, -5); err != nil {
		t.Fatalf("负尺寸应取缺省: %v", err)
	}
	// image 包引用守护（decode 断言经 image.RegisterFormat 生效）。
	_ = image.Point{}
}

func TestRenderBarcodePNG_CacheHit(t *testing.T) {
	env := newTestEnv(t)
	key := cacheKey(SymbologyCode128, "SF-CACHE-HIT", DefaultBarcodeWidth, DefaultBarcodeHeight)
	// 预置哨兵字节：命中缓存即返回哨兵（证明同键复用——render 预生成与端点共享）。
	sentinel := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	env.svc.cache.put(key, sentinel)
	b, err := env.svc.RenderBarcodePNG("SF-CACHE-HIT", SymbologyCode128, 0, 0)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(b, sentinel) {
		t.Fatalf("应命中缓存返回哨兵字节")
	}
}

func TestPNGCache_BoundedFIFO(t *testing.T) {
	c := newPNGCache()
	c.maxEnts = 4
	for i := 0; i < 6; i++ {
		c.put(cacheKey(SymbologyCode128, "k"+strconv.Itoa(i), 1, 1), []byte{byte(i)})
	}
	if len(c.entries) != 4 {
		t.Fatalf("缓存应有界: %d", len(c.entries))
	}
	if _, ok := c.get(cacheKey(SymbologyCode128, "k0", 1, 1)); ok {
		t.Fatalf("最早条目应被 FIFO 淘汰")
	}
	if _, ok := c.get(cacheKey(SymbologyCode128, "k5", 1, 1)); !ok {
		t.Fatalf("最新条目应保留")
	}
}
