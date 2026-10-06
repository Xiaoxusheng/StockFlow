import { useEffect, useMemo, useState } from 'react'
import { Button, DatePicker, Flex, Form, Input, Select } from 'antd'
import { ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import type { ReactNode } from 'react'

const { useWatch } = Form

export interface SearchField {
  name: string
  label: string
  /**
   * 控件类型：input / select / dateRange。
   * - dateRange：渲染 RangePicker，提交时**展开为两个 query 参数**（见 rangeKeys），
   *   与后端 `created_from` / `created_to` 等成对时间参数对齐；
   * - 仅当目标列表端点**真实支持**时间参数时才使用（后端不消费的参数会造成假筛选）。
   */
  control: 'input' | 'select' | 'dateRange'
  options?: Array<{ label: ReactNode; value: string }>
  placeholder?: string
  allowClear?: boolean
  /**
   * dateRange 专用：展开输出的两个参数名，缺省 `${name}_from` / `${name}_to`。
   * 例：name='created' + rangeKeys=['created_from','created_to']。
   */
  rangeKeys?: readonly [string, string]
  /** dateRange 专用：是否带时分秒（缺省 true → 'YYYY-MM-DD HH:mm:ss'；false → 'YYYY-MM-DD'） */
  withTime?: boolean
}

export interface SfSearchFormProps {
  fields: SearchField[]
  onSearch: (values: Record<string, unknown>) => void
  /** 重置回调（通常同时清筛选并回到第一页） */
  onReset?: () => void
  loading?: boolean
  /** ≥4 个字段时默认折叠，展开按钮控制 */
  collapsible?: boolean
  /** 尾部动作区（如「发起移库」等业务主操作）：与查询/重置同行渲染，避免按钮单独换行 */
  extraActions?: ReactNode
  /**
   * 回填值：从 URL / 持久化恢复的筛选（通常是 usePagedList 的 params）。
   * 变化时同步到表单，使刷新后输入框仍回显当前生效条件；不传则维持原「非受控」行为。
   *
   * 类型用 `object` 而非 `Record<string, unknown>`：调用方传入的是 interface 形态的
   * Query 类型，TS 的 interface 没有隐式索引签名，声明成 Record 会报
   * "Index signature for type 'string' is missing"。
   */
  initialValues?: object
}

/**
 * 统一查询表单（frontend.md §6.3）。
 * 非受控实现：提交时把值交给页面状态，页面据此触发 usePagedList 请求。
 */
export function SfSearchForm({
  fields,
  onSearch,
  onReset,
  loading,
  collapsible = fields.length >= 5,
  extraActions,
  initialValues,
}: SfSearchFormProps) {
  const [form] = Form.useForm()
  const collapsed = useWatch('collapsed', form) ?? collapsible
  // 任务书 §50：已生效筛选条件数——只在用户提交后增长，如实反映当前过滤状态
  const [appliedCount, setAppliedCount] = useState(0)

  const hasInitialValues = initialValues !== undefined
  const initialValuesRecord = useMemo(
    () => (initialValues ?? {}) as Record<string, unknown>,
    [initialValues],
  )
  /**
   * 字段规格序列（字符串化以稳定 effect 依赖：fields 由调用方每次渲染新建数组）。
   * dateRange 字段编入其展开规则（`name:fromKey~toKey`），供回填把成对 query 参数
   * 合成 RangePicker 值、提交时再展开回两个参数。
   */
  const fieldNamesKey = useMemo(
    () =>
      fields
        .map((field) =>
          field.control === 'dateRange'
            ? `${field.name}:${(field.rangeKeys ?? [`${field.name}_from`, `${field.name}_to`]).join('~')}`
            : field.name,
        )
        .join(','),
    [fields],
  )

  /**
   * 回填：从 URL / 持久化恢复的筛选变化时同步到表单，使刷新后输入框仍回显当前条件。
   * 只写本表单声明的字段（不碰 collapsed 等内部字段），未命中的字段置 undefined 以清空。
   * 「已筛选 N 项」同样只数本表单字段——否则 URL 上出现无关参数（如别的页面残留的 query）
   * 会显示「已筛选 1 项」但表单全空，与实际过滤条件不符。
   */
  useEffect(() => {
    if (!hasInitialValues) return
    const next: Record<string, unknown> = {}
    let applied = 0
    for (const token of fieldNamesKey.split(',')) {
      if (!token) continue
      const [name, rangeSpec] = token.split(':')
      if (rangeSpec) {
        // dateRange 字段：由成对参数合成 RangePicker 值（缺一边则半边为空）
        const [fromKey, toKey] = rangeSpec.split('~')
        const fromRaw = initialValuesRecord[fromKey]
        const toRaw = initialValuesRecord[toKey]
        const hasRange = fromRaw !== undefined || toRaw !== undefined
        next[name] = hasRange
          ? [fromRaw ? dayjs(String(fromRaw)) : null, toRaw ? dayjs(String(toRaw)) : null]
          : undefined
        if (fromRaw || toRaw) applied += 1
        continue
      }
      const value = initialValuesRecord[name]
      next[name] = value
      if (value !== undefined && value !== null && value !== '') applied += 1
    }
    form.setFieldsValue(next)
    setAppliedCount(applied)
  }, [hasInitialValues, initialValuesRecord, fieldNamesKey, form])

  const visibleFields = collapsed ? fields.slice(0, 3) : fields

  const handleFinish = (values: Record<string, unknown>) => {
    const cleaned = cleanValues(values, fields)
    // 计数按「筛选项」而非 query 参数个数：dateRange 展开后是 2 个参数但只算 1 项
    setAppliedCount(countAppliedFields(cleaned, fieldNamesKey))
    onSearch(cleaned)
  }

  const handleReset = () => {
    form.resetFields()
    setAppliedCount(0)
    onReset?.()
    onSearch({})
  }

  return (
    <Form
      form={form}
      onFinish={handleFinish}
      initialValues={{ collapsed }}
      className="sf-search-form"
      style={{ marginBottom: 'var(--sf-space-3)' }}
    >
      {/* 弹性行：字段 flex 挤占 + 操作按钮同排尾部（f854d23 行为），仅去除外层重复下边距。
          字段宽度上限走 --sf-search-field-width（240px，原硬编码 260px 过宽，会把右侧
          操作按钮挤到第二行）；label 定宽右对齐（样式见 global.css .sf-search-form），
          两者共同保证「同一行内所有控件严格等宽」。 */}
      <Flex gap="var(--sf-space-3)" wrap="wrap" align="middle">
        {visibleFields.map((field) => (
          <Form.Item
            key={field.name}
            name={field.name}
            label={field.label}
            style={{
              marginBottom: 0,
              /* basis 220px：3 字段 + 查询/重置 在 1161px 视口（内容区 871px）恰好不折行；
                 宽屏时自由增长到 --sf-search-field-width 上限，窄屏收缩而不折行。
                 时间范围控件更宽（两个日期 + 分隔），单独放宽上限与下限。 */
              flex: field.control === 'dateRange' ? '1 1 300px' : '1 1 220px',
              maxWidth:
                field.control === 'dateRange'
                  ? 'calc(var(--sf-search-field-width) + 120px)'
                  : 'var(--sf-search-field-width)',
              minWidth: field.control === 'dateRange' ? 240 : 200,
            }}
          >
            {field.control === 'select' ? (
              <Select
                options={field.options}
                placeholder={field.placeholder ?? `请选择${field.label}`}
                allowClear={field.allowClear ?? true}
                style={{ width: '100%' }}
              />
            ) : field.control === 'dateRange' ? (
              <DatePicker.RangePicker
                showTime={field.withTime !== false}
                allowClear={field.allowClear ?? true}
                style={{ width: '100%' }}
                placeholder={[field.placeholder ?? '开始时间', '结束时间']}
              />
            ) : (
              <Input
                placeholder={field.placeholder ?? `请输入${field.label}`}
                allowClear={field.allowClear ?? true}
              />
            )}
          </Form.Item>
        ))}
        <Flex gap="var(--sf-space-2)" wrap="wrap" style={{ flex: '0 0 auto' }}>
          <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading}>
            查询
          </Button>
          <Button icon={<ReloadOutlined />} onClick={handleReset}>
            重置
          </Button>
          {collapsible && (
            <Button type="link" onClick={() => form.setFieldValue('collapsed', !collapsed)}>
              {collapsed ? '展开' : '收起'}
            </Button>
          )}
        </Flex>
        {/* extraActions 独立成 flex item（不再与「查询/重置」同组）：空间不足时只让这一组
            折到下一行，而不是把查询/重置一起拖下去。修前整组折行——复核页 536px 的
            视图+自动刷新组把查询/重置带到第二行，首行只剩筛选字段（2026-10-06 实测）。 */}
        {extraActions && (
          <Flex gap="var(--sf-space-2)" wrap="wrap" style={{ flex: '0 0 auto' }}>
            {extraActions}
          </Flex>
        )}
      </Flex>
      {appliedCount > 0 && (
        <Flex
          align="center"
          gap={4}
          className="sf-search-form__applied"
          style={{
            marginTop: 'var(--sf-space-2)',
            fontSize: 'var(--sf-font-size-caption)',
            color: 'var(--sf-text-muted)',
          }}
        >
          已筛选 {appliedCount} 项
          <Button
            type="link"
            size="small"
            style={{ paddingInline: 4, height: 22 }}
            onClick={handleReset}
          >
            清除全部
          </Button>
        </Flex>
      )}
    </Form>
  )
}

/**
 * 已在效果中的筛选项数（用于「已筛选 N 项」提示）。
 * 按**字段**计数而非 query 参数：dateRange 展开为 from/to 两个参数但只应算 1 项，
 * 否则「已筛选 2 项」而表单里只填了一个时间范围，与实际过滤条件不符。
 */
function countAppliedFields(cleaned: Record<string, unknown>, fieldNamesKey: string): number {
  let count = 0
  for (const token of fieldNamesKey.split(',')) {
    if (!token) continue
    const [name, rangeSpec] = token.split(':')
    if (rangeSpec) {
      const [fromKey, toKey] = rangeSpec.split('~')
      if (cleaned[fromKey] !== undefined || cleaned[toKey] !== undefined) count += 1
      continue
    }
    if (cleaned[name] !== undefined) count += 1
  }
  return count
}

/**
 * 去掉 undefined / 空字符串，避免空筛选污染 QueryKey；
 * dateRange 控件的 `[Dayjs, Dayjs]` 值展开为成对 query 参数（from/to），
 * 格式缺省 `YYYY-MM-DD HH:mm:ss`（对齐后端 parseTimeParam），withTime=false 时为 `YYYY-MM-DD`。
 */
function cleanValues(
  values: Record<string, unknown>,
  fields: SearchField[],
): Record<string, unknown> {
  const rangeByName = new Map<string, SearchField>()
  for (const field of fields) {
    if (field.control === 'dateRange') rangeByName.set(field.name, field)
  }
  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(values)) {
    if (key === 'collapsed') continue
    if (value === undefined || value === null || value === '') continue
    const rangeField = rangeByName.get(key)
    if (rangeField && Array.isArray(value)) {
      const [from, to] = value as Array<Dayjs | null | undefined>
      const [fromKey, toKey] = rangeField.rangeKeys ?? [`${key}_from`, `${key}_to`]
      const fmt = rangeField.withTime === false ? 'YYYY-MM-DD' : 'YYYY-MM-DD HH:mm:ss'
      if (from) result[fromKey] = from.format(fmt)
      if (to) result[toKey] = to.format(fmt)
      continue
    }
    result[key] = value
  }
  return result
}
