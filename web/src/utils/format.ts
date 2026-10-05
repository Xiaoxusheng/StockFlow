import dayjs from 'dayjs'

/** 统一空值占位（frontend.md §1.5：格式化统一封装，禁止页面自己拼） */
export const EMPTY_TEXT = '-'

type DateLike = string | number | Date | null | undefined

/** 时间：YYYY-MM-DD HH:mm（任务书 §36：全站统一分钟精度，禁止各页面自选格式） */
export function formatDateTime(value: DateLike): string {
  return formatByPattern(value, 'YYYY-MM-DD HH:mm')
}

/** 日期：YYYY-MM-DD */
export function formatDate(value: DateLike): string {
  return formatByPattern(value, 'YYYY-MM-DD')
}

function formatByPattern(value: DateLike, pattern: string): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT
  const d = dayjs(value)
  return d.isValid() ? d.format(pattern) : EMPTY_TEXT
}

/** 数量：千分位 */
export function formatNumber(value: number | string | null | undefined, digits = 0): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT
  const n = Number(value)
  if (!Number.isFinite(n)) return EMPTY_TEXT
  return new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(n)
}

/**
 * 库存数量：千分位 + 最多 4 位小数、不强制补零（1.5 → "1.5"、5 → "5"）。
 * 后端数量为 numeric(18,4)（stock.ParseQty 最多 4 位小数），固定 digits=0 会把
 * 合法小数数量四舍五入展示；JSON 出参为字符串化 Qty 亦可直接传入（stock/qty.go:72-74）。
 */
export function formatQty(value: number | string | null | undefined): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT
  const n = Number(value)
  if (!Number.isFinite(n)) return EMPTY_TEXT
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: 4,
  }).format(n)
}

/** 金额：¥ 1,234.56 */
export function formatMoney(value: number | string | null | undefined, digits = 2): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT
  const n = Number(value)
  if (!Number.isFinite(n)) return EMPTY_TEXT
  return `¥ ${new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(n)}`
}

/** 百分比：98.5%（入参为 0~100 数值） */
export function formatPercent(value: number | string | null | undefined, digits = 1): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT
  const n = Number(value)
  if (!Number.isFinite(n)) return EMPTY_TEXT
  return `${n.toFixed(digits)}%`
}

/** 文件大小：1.2 MB */
export function formatFileSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return EMPTY_TEXT
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes
  let i = -1
  do {
    v /= 1024
    i += 1
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(1)} ${units[i]}`
}
