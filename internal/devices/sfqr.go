package devices

// SFQR（StockFlow QR）协议 v1 唯一 Go 解析点（docs/qr-code.md §2 格式/§4 黄金向量
// 逐字冻结）。仅 resolve 管线第 0 段（service_scan.go）调用；其余代码不得各自解析
// SFQR。构造唯一点在前端 web/src/utils/qrPayload.ts——两侧正确性由同一份黄金向量
// 分别钉死（Go 侧 internal/devices/sfqr_test.go 为协议正确性的唯一权威测试）。

import (
	"strings"

	"github.com/stockflow/server/internal/response"
)

// 协议常量（qr-code.md §2.2 段表冻结：SFQR|1|SKU|<sku_code>，ASCII '|' 分隔 4 段）。
const (
	// SfqrMagic magic 段字面（大小写敏感）。
	SfqrMagic = "SFQR"
	// SfqrVersion v1 实现唯一接受的版本字面（≠"1" 一律 SFQR_VERSION_UNSUPPORTED，
	// 绝不猜测或降级解析——qr-code.md §3.1）。
	SfqrVersion = "1"
	// SfqrTypeSKU v1 唯一实现的 type 值。
	SfqrTypeSKU = "SKU"
	// sfqrSegments 管道式段数（magic|version|type|payload）。
	sfqrSegments = 4
	// maxSfqrPayloadLen 载荷（SKU 编码）长度上限（qr-code.md BNF 1*64）。
	maxSfqrPayloadLen = 64
	// sfqrPrefix 锁型前缀：命中即进入 SFQR 分支（O(1) 自标识，§2.2 段 0）。
	sfqrPrefix = SfqrMagic + "|"
)

// sfqrReservedTypes 协议预留类型（v1 显式未实现——BIN/BOX/PALLET 与未知 type 同码
// 返回 SFQR_TYPE_UNSUPPORTED；未来在解析点同址扩展，qr-code.md §2.2/§3.1）。
var sfqrReservedTypes = map[string]bool{"BIN": true, "BOX": true, "PALLET": true}

// IsSfqrCode 前缀探测：码值是否进入 SFQR 分支（大小写敏感——"sfqr|..." 不是协议码，
// 落回后续匹配器）。
func IsSfqrCode(code string) bool {
	return strings.HasPrefix(code, sfqrPrefix)
}

// parseSfqr 解析 SFQR v1 码值 → 载荷 SKU 编码。任何格式/版本/类型非法都显式返回
// 对应 SFQR_* 业务错误码（400，qr-code.md §5），禁止把残缺 SFQR 当普通条码静默
// 落回后续匹配器（约束 2：未知版本/类型必须明确不支持）。成功返回 sku_code
// （非空 ≤64、不校验字符集——查无即 SKU_NOT_FOUND，与条码匹配器口径一致）。
func parseSfqr(code string) (string, error) {
	seg := strings.Split(code, "|")
	if len(seg) != sfqrSegments || seg[0] != SfqrMagic {
		// 段数非法（2 段/5 段等）——magic 已由 IsSfqrCode 前缀保证，此处防御复核。
		return "", response.NewError(ErrSfqrInvalid, ginH{"code": code})
	}
	if seg[1] != SfqrVersion {
		return "", response.NewError(ErrSfqrVersionUnsupported, ginH{"code": code, "version": seg[1]})
	}
	if typ := seg[2]; typ != SfqrTypeSKU {
		reason := "未知类型"
		if sfqrReservedTypes[typ] {
			reason = "类型已预留未实现"
		}
		return "", response.NewError(ErrSfqrTypeUnsupported, ginH{"code": code, "type": typ, "reason": reason})
	}
	payload := seg[3]
	if payload == "" || len(payload) > maxSfqrPayloadLen {
		return "", response.NewError(ErrSfqrInvalid, ginH{"code": code, "reason": "payload 须为 1-64 字符"})
	}
	return payload, nil
}
