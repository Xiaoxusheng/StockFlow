/**
 * SFQR 商品二维码协议 —— 前端全站唯一构造/拆解点（docs/qr-code.md §1「构造唯一点」冻结）。
 *
 * 协议（v1，管道式 4 段，docs/qr-code.md §2）：
 *
 *   SFQR|1|SKU|<sku_code>
 *
 * 职责边界（qr-code.md §1 三条统一口径，任何调用方不得偏离）：
 * - 本文件只做「协议字符串 ↔ 段」的纯字符串构造与格式拆解，不访问任何业务数据；
 * - 扫码业务识别唯一入口 = POST /api/scanner/resolve（后端解析唯一点
 *   internal/devices/sfqr.go，resolve 管线第 0 段）——前端禁止本地业务解析，
 *   parseSfqrPayload 仅用于预览/核对场景展示段含义，不代表对象身份裁决；
 * - 禁止第二处拼串：二维码中心预览、SKU 详情抽屉、SKU_LABEL 标签渲染一律经本文件构造。
 *
 * 正确性依据：docs/qr-code.md §4 黄金向量表（逐字冻结 14 条）——本实现逐字对照该表；
 * Go 侧权威自动化测试见 internal/devices/sfqr_test.go（同表表驱动）。
 */

/** 协议 magic + 分隔符前缀：读码侧据此锁型进入 SFQR 分支（字面，大小写敏感） */
export const SFQR_PREFIX = 'SFQR|'

/** v1 协议版本（冻结；解析侧对 ≠"1" 一律版本不支持，禁止降级猜测，qr-code.md §3.1） */
export const SFQR_VERSION = '1'

/** v1 唯一实现的类型段：SKU（BIN/BOX/PALLET 为协议预留，解析端显式返回不支持，qr-code.md §2.2） */
export const SFQR_TYPE_SKU = 'SKU'

/** 载荷段（SKU 编码）最大长度（qr-code.md §2.1 BNF：1*64<chr>，非空、≤64） */
export const SFQR_SKU_CODE_MAX_LENGTH = 64

/** 协议格式错误码（与后端识别类错误码逐字同源，qr-code.md §5；仅标识格式原因，非业务错误） */
export type SfqrPayloadErrorCode = 'SFQR_INVALID' | 'SFQR_VERSION_UNSUPPORTED' | 'SFQR_TYPE_UNSUPPORTED'

/** SKU 类型载荷拆解结果（version/type 字面量类型由常量推导，保证与协议冻结值同源） */
export interface SfqrSkuPayload {
  version: typeof SFQR_VERSION
  type: typeof SFQR_TYPE_SKU
  /** SKU 编码（协议身份段；该编码对应哪个 SKU 属业务语义，由 resolve 接口裁决） */
  skuCode: string
}

/** 协议拆解结果：ok=false 时 errorCode 与 docs/qr-code.md §5 错误码逐字对应 */
export type SfqrParseResult =
  | { ok: true; payload: SfqrSkuPayload }
  | { ok: false; errorCode: SfqrPayloadErrorCode }

/**
 * 构造 SKU 二维码载荷：`SFQR|1|SKU|<sku_code>`（qr-code.md §7.1 生成唯一点）。
 *
 * 非法输入显式 throw——交互场景失败必须可见（打印出一张扫不出的码比静默失败更危险）；
 * 渲染层兜底场景请用 {@link tryBuildSfqrSku}。
 *
 * @param skuCode SKU 编码（创建后不可变、活集唯一，qr-code.md §2.1）
 * @throws {TypeError} skuCode 非字符串
 * @throws {RangeError} skuCode 为空 / 含分隔符 "|" / 超过 64 字符
 */
export function buildSfqrSku(skuCode: string): string {
  if (typeof skuCode !== 'string') {
    throw new TypeError('SFQR 载荷构造失败：SKU 编码必须为字符串')
  }
  if (skuCode.length === 0) {
    throw new RangeError('SFQR 载荷构造失败：SKU 编码不能为空')
  }
  if (skuCode.includes('|')) {
    throw new RangeError('SFQR 载荷构造失败：SKU 编码不能包含协议分隔符 "|"')
  }
  if (skuCode.length > SFQR_SKU_CODE_MAX_LENGTH) {
    throw new RangeError(`SFQR 载荷构造失败：SKU 编码超过 ${SFQR_SKU_CODE_MAX_LENGTH} 字符上限`)
  }
  return `${SFQR_PREFIX}${SFQR_VERSION}|${SFQR_TYPE_SKU}|${skuCode}`
}

/**
 * 渲染层降级构造：非法输入返回 null 而非抛错（qr-code.md §7.1）。
 * 供列表预览、标签渲染等「个别脏数据不得中断整页渲染」的场景；
 * 交互动作（复制、打印等）请用 {@link buildSfqrSku} 让失败显式暴露。
 */
export function tryBuildSfqrSku(skuCode: string | null | undefined): string | null {
  if (typeof skuCode !== 'string' || skuCode.length === 0) return null
  try {
    return buildSfqrSku(skuCode)
  } catch {
    return null
  }
}

/**
 * 前缀探测：字符串是否进入 SFQR 语义（对应解析端 `strings.HasPrefix(code, "SFQR|")` 锁型，
 * qr-code.md §6 第 0 段）。仅判断前缀，不校验其余段——段级合法性用 {@link parseSfqrPayload}。
 */
export function isSfqrPayload(code: string | null | undefined): boolean {
  return typeof code === 'string' && code.startsWith(SFQR_PREFIX)
}

/**
 * 协议格式拆解（黄金向量表逐字对照，docs/qr-code.md §4）：
 *
 * - 段数 ≠ 4 → SFQR_INVALID（向量 #12 5 段 / #13 2 段）；
 * - magic ≠ "SFQR"（大小写敏感）→ SFQR_INVALID（双保险，正常入口已由 isSfqrPayload 锁型）；
 * - 版本段非 1*DIGIT → SFQR_INVALID；数字但 ≠ "1" → SFQR_VERSION_UNSUPPORTED（向量 #4/#5）；
 * - 类型段非 1*ALPHA → SFQR_INVALID；非 "SKU"（含预留 BIN/BOX/PALLET、大小写敏感）→
 *   SFQR_TYPE_UNSUPPORTED（向量 #6–#10）；
 * - 载荷段为空（向量 #11）或超 64 字符（向量 #14）→ SFQR_INVALID；
 *   含 "|" 由 4 段切分天然排除。
 *
 * 注意：这是纯格式拆解，用于预览/核对场景展示段含义；不是业务解析——
 * 「该载荷对应哪个 SKU」唯一入口 = POST /api/scanner/resolve（frontend.md §13，约束 4）。
 */
export function parseSfqrPayload(code: string): SfqrParseResult {
  if (typeof code !== 'string') return { ok: false, errorCode: 'SFQR_INVALID' }
  const segments = code.split('|')
  if (segments.length !== 4) return { ok: false, errorCode: 'SFQR_INVALID' }
  const [magic, version, type, skuCode] = segments
  if (magic !== 'SFQR') return { ok: false, errorCode: 'SFQR_INVALID' }
  if (!/^\d+$/.test(version)) return { ok: false, errorCode: 'SFQR_INVALID' }
  if (version !== SFQR_VERSION) return { ok: false, errorCode: 'SFQR_VERSION_UNSUPPORTED' }
  if (!/^[A-Za-z]+$/.test(type)) return { ok: false, errorCode: 'SFQR_INVALID' }
  if (type !== SFQR_TYPE_SKU) return { ok: false, errorCode: 'SFQR_TYPE_UNSUPPORTED' }
  if (skuCode.length === 0 || skuCode.length > SFQR_SKU_CODE_MAX_LENGTH) {
    return { ok: false, errorCode: 'SFQR_INVALID' }
  }
  return { ok: true, payload: { version: SFQR_VERSION, type: SFQR_TYPE_SKU, skuCode } }
}
