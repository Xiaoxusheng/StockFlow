import { useCallback, useEffect, useRef, useState } from 'react'
import { Select, Spin } from 'antd'
import type { ReactNode } from 'react'

/**
 * 来源单号远程选择器（frontend.md §23）：系统内已有单据下拉联想，替代手输单号。
 *
 * 交互契约（api.md §2 分页列表复用）：
 * - 展开下拉 = 以当前关键词重新拉取（保证新开单据可见，不做本地快照）；关闭取消在途防抖；
 * - 输入防抖 300ms 远程搜索（filterOption=false，过滤完全由服务端承担）；
 * - 竞态守卫：序号计数，慢响应丢弃（loadOptions 由调用方合并多状态查询，见各调用方）；
 * - 选项三态：可选 / disabledReason 禁用（原因随行展示，如「未收货不可退」）/ 拉取失败如实空列表；
 * - value 即单号字符串：URL 预填（frontend.md §33）等未命中选项时直接显示原值，不造选项。
 *
 * 实现注记（@rc-component/select）：选项对象直接携带 description/disabledReason/raw
 * 透传字段——optionRender 收到的是展平项（自定义字段在 option.data 上），onChange 收到
 * 的是 injectPropsWithOption 展开后的同一对象；label 仅为收起态显示文本。
 * 状态过滤口径（哪些状态可选）由调用方按后端校验契约决定，本组件不掺业务规则。
 */

export type SfOrderSelectOption<T = unknown> = {
  /** 单号（Select value：po_no / so_no / inbound_no） */
  value: string
  /** 主文本（收起态显示；一般为单号本身） */
  label: string
  /** 下拉行辅助说明（供应商/仓库/状态标签等，小字一行） */
  description?: ReactNode
  /** 有值即禁用该选项，文案作为不可选原因随行展示 */
  disabledReason?: string
  /** onChange 透传的原始业务行（选中后带明细/联动仓库用） */
  raw?: T
}

export interface SfOrderSelectProps<T> {
  value?: string
  /** antd Form 受控契约：value/onChange 由 Form.Item 注入；清除时 value=undefined */
  onChange?: (value: string | undefined, option: SfOrderSelectOption<T> | undefined) => void
  /** 远程取选项：keyword 为空串=初始最近单据视图；实现内自行合并多状态查询 */
  loadOptions: (keyword: string) => Promise<Array<SfOrderSelectOption<T>>>
  placeholder?: string
  disabled?: boolean
  allowClear?: boolean
  /** 空列表/加载失败兜底文案 */
  emptyText?: string
  style?: React.CSSProperties
}

/** 搜索防抖（ms）：与服务端 ILIKE 前缀扫描成本匹配，低于人工连续输入节奏 */
const SEARCH_DEBOUNCE_MS = 300

export function SfOrderSelect<T>({
  value,
  onChange,
  loadOptions,
  placeholder,
  disabled,
  allowClear = true,
  emptyText = '无匹配单据',
  style,
}: SfOrderSelectProps<T>) {
  const [options, setOptions] = useState<Array<SfOrderSelectOption<T>>>([])
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  const seqRef = useRef(0)
  const timerRef = useRef<number | undefined>(undefined)
  const keywordRef = useRef('')

  const runSearch = useCallback(
    (keyword: string) => {
      const seq = ++seqRef.current
      setLoading(true)
      setFailed(false)
      loadOptions(keyword)
        .then((rows) => {
          if (seqRef.current !== seq) return // 过期响应丢弃（竞态守卫）
          setOptions(rows)
        })
        .catch(() => {
          if (seqRef.current !== seq) return
          setOptions([])
          setFailed(true)
        })
        .finally(() => {
          if (seqRef.current === seq) setLoading(false)
        })
    },
    [loadOptions],
  )

  useEffect(() => () => window.clearTimeout(timerRef.current), [])

  /** 展开=拉取（含首次）；关闭=取消在途防抖（选中后 autoClear 的 onSearch 不再空打） */
  const handleOpenChange = (open: boolean) => {
    if (open) {
      runSearch(keywordRef.current)
    } else {
      window.clearTimeout(timerRef.current)
    }
  }

  const handleSearch = (keyword: string) => {
    keywordRef.current = keyword
    window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => runSearch(keyword), SEARCH_DEBOUNCE_MS)
  }

  return (
    <Select<string, SfOrderSelectOption<T>>
      value={value}
      onChange={(v, option) => {
        const opt = Array.isArray(option) ? option[0] : option
        onChange?.(v, opt as SfOrderSelectOption<T> | undefined)
      }}
      showSearch
      filterOption={false}
      onSearch={handleSearch}
      onOpenChange={handleOpenChange}
      options={options.map((o) => ({ ...o, disabled: Boolean(o.disabledReason) }))}
      optionRender={(option) => {
        const o = (option as unknown as { data?: SfOrderSelectOption<T> }).data
        return (
          <div style={{ lineHeight: 1.5 }}>
            <div>{o?.label ?? option.label}</div>
            {o?.description ? (
              <div style={{ fontSize: 12, color: 'var(--sf-text-secondary)' }}>{o.description}</div>
            ) : null}
            {o?.disabledReason ? (
              <div style={{ fontSize: 12, color: 'var(--sf-danger)' }}>{o.disabledReason}</div>
            ) : null}
          </div>
        )
      }}
      notFoundContent={
        loading ? (
          <Spin size="small" />
        ) : (
          <span style={{ color: 'var(--sf-text-secondary)', fontSize: 13 }}>
            {failed ? '选项加载失败，请关闭重试' : emptyText}
          </span>
        )
      }
      loading={loading}
      allowClear={allowClear}
      disabled={disabled}
      placeholder={placeholder}
      style={style}
    />
  )
}
