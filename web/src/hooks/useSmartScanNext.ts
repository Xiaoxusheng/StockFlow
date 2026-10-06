import { useCallback, useState } from 'react'
import { resolveErrorMessage } from '@/api/client'
import {
  scannerApi,
  type ScanDocKind,
  type ScanResolveItem,
  type ScanResolveResult,
} from '@/api/device'

/**
 * 智能下一步作业上下文（扫码作业优化 2026-10-06）：声明当前页面所处的作业域，
 * resolve 识别命中后据此决定「继续」的落点。'global' = 无特定上下文（PadActionBar
 * 全局扫码入口 / 任务页），按识别对象类型直接路由到对应作业页。
 */
export type SmartScanContext =
  | 'receive'
  | 'quality'
  | 'putaway'
  | 'count'
  | 'stockmove'
  | 'transfer'
  | 'inventory'
  | 'global'

/** 智能下一步指令（resolve 结果 × 上下文 → 页面据此执行「继续」步） */
export type SmartScanDirective =
  /** 单据/任务码命中 → 页面在自身列表中定位该任务并选中（如收货任务→收货确认） */
  | { kind: 'locate-task'; docKind: ScanDocKind; code: string; message: string }
  /** SKU / 批次 / 序列号命中 → 页面定位业务明细行或过滤库存 */
  | {
      kind: 'locate-item'
      objectType: 'sku' | 'batch' | 'serial'
      id: number | string
      code: string
      message: string
    }
  /** 库位码命中 → 页面定位该库位（过滤/校验） */
  | { kind: 'locate-bin'; id: number | string; code: string; message: string }
  /** 识别对象不属于当前上下文 → 跳转到对应作业页（智能下一步 = 带到正确作业面） */
  | { kind: 'navigate'; path: string; message: string }
  /** 多命中未定位（库位跨仓同码等，resolve id=0 且 items 非空） */
  | { kind: 'ambiguous'; message: string }
  /** 识别成功但对象不存在（UNKNOWN_BARCODE / *_NOT_FOUND 等业务错误码） */
  | { kind: 'unmatched'; message: string }
  /** resolve 请求本身失败（网络 / 无 scanner:resolve:list 权限等） */
  | { kind: 'resolve-failed'; message: string }

export interface SmartScanOutcome {
  directive: SmartScanDirective
  /** resolve 原始结果（页面需要 duplicate / items / name 时取用） */
  result: ScanResolveResult
}

/**
 * 单据前缀 → 作业页冻结路由表（scan 域前缀映射与 internal/devices/ports.go:89
 * docPrefixOwners 15 值一一对应；作业页 = 当前已交付的真实页面）。
 * 收货任务→收货确认（IN/PO/RC → /pad/receive）、质检（QC → /pad/quality）、
 * 上架→上架（PW → /pad/putaway）、拣货→拣货（PK/OUT/SO → PC /picking）、
 * 复核/打包/发货（CH/BP/SH → PC /checking /packing /shipment）、
 * 盘点（CK → /pad/count）、调拨（TR → /pad/transfer）、异常（EX → /pad/exception）。
 * RT（退货）无统一作业页 → 不入表，走 unmatched 语义如实提示。
 */
const DOC_KIND_ROUTES: Partial<Record<ScanDocKind, string>> = {
  IN: '/pad/receive',
  PO: '/pad/receive',
  RC: '/pad/receive',
  QC: '/pad/quality',
  PW: '/pad/putaway',
  SO: '/picking',
  OUT: '/picking',
  PK: '/picking',
  CH: '/checking',
  BP: '/packing',
  SH: '/shipment',
  TR: '/pad/transfer',
  CK: '/pad/count',
  EX: '/pad/exception',
}

/** 单据前缀 → 人读作业名（指令 message 用，与 DOC_KIND_ROUTES 同源） */
const DOC_KIND_LABEL: Record<ScanDocKind, string> = {
  IN: '入库单',
  PO: '采购单',
  QC: '质检单',
  RC: '收货单',
  PW: '上架任务',
  SO: '销售单',
  OUT: '出库单',
  PK: '拣货任务',
  CH: '复核单',
  BP: '箱码',
  SH: '发货单',
  TR: '调拨单',
  CK: '盘点任务',
  RT: '退货单',
  EX: '异常单',
}

const RESULT_TYPE_LABEL: Record<ScanResolveItem['type'], string> = {
  doc: '单据',
  sku: 'SKU',
  bin: '库位',
  serial: '序列号',
  batch: '批次',
}

/** 按上下文把 resolve 结果映射为智能下一步指令（「判」步，纯函数便于单测推演） */
function toDirective(
  result: ScanResolveResult,
  context: SmartScanContext,
): SmartScanDirective {
  // 多命中未定位：如实携带 items 摘要（id=0 表示未定位，scanner.md §5.3）
  if (result.id === 0 && result.items && result.items.length > 0) {
    const summary = result.items
      .slice(0, 3)
      .map((item) => `${item.code}${item.name ? `（${item.name}）` : ''}`)
      .join('、')
    return {
      kind: 'ambiguous',
      message: `该编码命中 ${result.items.length} 个对象（${summary}${result.items.length > 3 ? ' 等' : ''}），请手工选择具体对象`,
    }
  }

  if (result.type === 'doc') {
    const label = DOC_KIND_LABEL[result.doc_kind ?? 'IN']
    if (context === 'receive' && (result.doc_kind === 'IN' || result.doc_kind === 'PO' || result.doc_kind === 'RC')) {
      return {
        kind: 'locate-task',
        docKind: result.doc_kind,
        code: result.code,
        message: `已识别${label} ${result.code}，正在定位收货任务`,
      }
    }
    const path = result.doc_kind ? DOC_KIND_ROUTES[result.doc_kind] : undefined
    if (path) {
      return {
        kind: 'navigate',
        path,
        message: `已识别${label} ${result.code}，正在进入对应作业页`,
      }
    }
    return {
      kind: 'unmatched',
      message: `识别为${label}，但该域作业页未立项，暂无智能下一步落点`,
    }
  }

  if (result.type === 'bin') {
    if (context === 'global') {
      return {
        kind: 'navigate',
        path: '/pad/inventory',
        message: `已识别库位 ${result.code}，正在进入库存页定位`,
      }
    }
    return {
      kind: 'locate-bin',
      id: result.id,
      code: result.code,
      message: `已识别库位 ${result.code}`,
    }
  }

  // sku / batch / serial：低风险定位类
  const label = RESULT_TYPE_LABEL[result.type]
  if (context === 'global') {
    return {
      kind: 'navigate',
      path: '/pad/inventory',
      message: `已识别${label} ${result.code}，正在进入库存页定位`,
    }
  }
  return {
    kind: 'locate-item',
    objectType: result.type as 'sku' | 'batch' | 'serial',
    id: result.id,
    code: result.code,
    message: `已识别${label} ${result.code}${result.name ? `（${result.name}）` : ''}`,
  }
}

export interface UseSmartScanNextOptions {
  /** 作业上下文（决定命中后的落点） */
  context: SmartScanContext
  /** 当前页面标识（scan_logs.page 审计列，传路由路径） */
  page: string
}

/**
 * 智能下一步（扫码作业优化 2026-10-06，scanner.md §1 标准链路的「识别→下一步」段）：
 * 统一调 POST /api/scanner/resolve（前端不做业务解析，scanner.md §5.3），按上下文
 * 把识别结果映射为 SmartScanDirective——页面拿到指令后执行「继续」步（定位任务/明细、
 * 过滤库存或路由到对应作业页）。只识别不执行业务：一切确认/扣减类提交仍由页面显式
 * 按钮承担（requirements.md §2.10；scanner.md §6.5 高风险必须明确确认）。
 *
 * resolve 失败（网络/权限）不伪造结果：返回 resolve-failed 携带真实错误信息，
 * 页面可据此走手输兜底（scanner.md §4.7 手工输入必须经过同样业务校验）。
 */
export function useSmartScanNext({ context, page }: UseSmartScanNextOptions) {
  const [isResolving, setIsResolving] = useState(false)

  const resolveNext = useCallback(
    async (code: string): Promise<SmartScanOutcome> => {
      setIsResolving(true)
      try {
        const result = await scannerApi.resolve({ code, page })
        return { directive: toDirective(result, context), result }
      } catch (error) {
        return {
          directive: {
            kind: 'resolve-failed',
            message: `扫码识别失败：${resolveErrorMessage(error)}`,
          },
          result: {
            type: 'doc',
            id: 0,
            code,
            name: '',
            duplicate: false,
          },
        }
      } finally {
        setIsResolving(false)
      }
    },
    [context, page],
  )

  return { resolveNext, isResolving }
}
