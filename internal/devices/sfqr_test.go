package devices

// SFQR 协议黄金向量表驱动测试（协议正确性的唯一权威——docs/qr-code.md §4 冻结表
// 逐条断言；§5 错误码字面 + 前缀探测/边界补充）。解析唯一点 internal/devices/sfqr.go，
// 前端构造唯一点 web/src/utils/qrPayload.ts 逐字对照同一份向量。

import (
	"strings"
	"testing"

	"github.com/stockflow/server/internal/response"
)

// TestParseSfqrGoldenVectors 黄金向量表（qr-code.md §4 逐字冻结 14 条 + 解析取码）。
func TestParseSfqrGoldenVectors(t *testing.T) {
	cases := []struct {
		name        string
		code        string
		wantSkuCode string // 期望载荷（合法向量）；空=期望报错
		wantErr     string // 期望错误码字面；空=期望成功
	}{
		// —— 合法 3 条（解析取码）——
		{"向量1 典型编码", "SFQR|1|SKU|SKU-001", "SKU-001", ""},
		{"向量2 含下划线", "SFQR|1|SKU|A1_002", "A1_002", ""},
		{"向量3 纯字母数字", "SFQR|1|SKU|S0001", "S0001", ""},
		// —— 版本（明确拒绝不降级）——
		{"向量4 未来版本", "SFQR|2|SKU|X", "", "SFQR_VERSION_UNSUPPORTED"},
		{"向量5 版本0", "SFQR|0|SKU|A", "", "SFQR_VERSION_UNSUPPORTED"},
		// —— 类型（预留未实现/未知/大小写敏感）——
		{"向量6 预留BIN", "SFQR|1|BIN|A-01", "", "SFQR_TYPE_UNSUPPORTED"},
		{"向量7 预留BOX", "SFQR|1|BOX|B1", "", "SFQR_TYPE_UNSUPPORTED"},
		{"向量8 预留PALLET", "SFQR|1|PALLET|P1", "", "SFQR_TYPE_UNSUPPORTED"},
		{"向量9 未知类型", "SFQR|1|XYZ|A", "", "SFQR_TYPE_UNSUPPORTED"},
		{"向量10 类型大小写敏感", "SFQR|1|sku|A", "", "SFQR_TYPE_UNSUPPORTED"},
		// —— 格式（payload 空/段数/越界）——
		{"向量11 payload空", "SFQR|1|SKU|", "", "SFQR_INVALID"},
		{"向量12 五段", "SFQR|1|SKU|A|B", "", "SFQR_INVALID"},
		{"向量13 两段", "SFQR|1", "", "SFQR_INVALID"},
		{"向量14 payload超64", "SFQR|1|SKU|" + strings.Repeat("A", 65), "", "SFQR_INVALID"},
		// —— 边界补充（BNF 1*64 上限本身放行；magic 大小写敏感由 IsSfqrCode 承载）——
		{"边界 payload恰64合法", "SFQR|1|SKU|" + strings.Repeat("A", 64), strings.Repeat("A", 64), ""},
		{"边界 magic小写不锁型", "sfqr|1|SKU|A", "", ""},
		{"边界 裸magic不锁型", "SFQR", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "边界 magic小写不锁型" || tc.name == "边界 裸magic不锁型" {
				// 前缀探测层面断言：不进入 SFQR 分支（落回后续匹配器），parseSfqr 不被调用。
				if IsSfqrCode(tc.code) {
					t.Fatalf("%q 不应锁型进入 SFQR 分支", tc.code)
				}
				return
			}
			got, err := parseSfqr(tc.code)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望成功，得到 %v", err)
				}
				if got != tc.wantSkuCode {
					t.Fatalf("载荷取码不符: 期望 %q 得到 %q", tc.wantSkuCode, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望错误码 %s，得到成功载荷 %q", tc.wantErr, got)
			}
			if code := errorCodeOf(err); code != tc.wantErr {
				t.Fatalf("错误码不符: 期望 %s 得到 %s（err=%v）", tc.wantErr, code, err)
			}
		})
	}
}

// TestSfqrPrefixProbe 前缀探测语义（qr-code.md §2.2 段 0）：magic+分隔符 O(1) 锁型；
// 残缺 SFQR（如 "SFQR|1|SKU|A|B"）仍锁型进分支，由 parseSfqr 显式报 INVALID——
// 禁止静默落回条码匹配器。
func TestSfqrPrefixProbe(t *testing.T) {
	yes := []string{"SFQR|1|SKU|SKU-001", "SFQR|1|SKU|A|B", "SFQR|2|SKU|X", "SFQR|1"}
	for _, code := range yes {
		if !IsSfqrCode(code) {
			t.Fatalf("%q 应锁型进入 SFQR 分支", code)
		}
	}
	no := []string{"sfqr|1|SKU|A", "SFQR", "SFQR:", "6901234567890", "PO-20261002-000001", ""}
	for _, code := range no {
		if IsSfqrCode(code) {
			t.Fatalf("%q 不应锁型进入 SFQR 分支", code)
		}
	}
}

// TestSfqrErrorCodeLiteral 错误码字面冻结核验（qr-code.md §5：均 400，
// 对齐 TestResolveErrorCodeLiteral 风格）。
func TestSfqrErrorCodeLiteral(t *testing.T) {
	for _, c := range []struct {
		code response.Code
		want string
	}{
		{ErrSfqrInvalid, "SFQR_INVALID"},
		{ErrSfqrVersionUnsupported, "SFQR_VERSION_UNSUPPORTED"},
		{ErrSfqrTypeUnsupported, "SFQR_TYPE_UNSUPPORTED"},
	} {
		if c.code.Code != c.want {
			t.Fatalf("错误码字面偏离 qr-code.md §5: 期望 %s 得到 %s", c.want, c.code.Code)
		}
	}
}
