import { useEffect, useMemo, useState } from 'react'
import { Button, Flex, Form, Select, Input } from 'antd'
import { ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import type { ReactNode } from 'react'

const { useWatch } = Form

export interface SearchField {
  name: string
  label: string
  control: 'input' | 'select'
  options?: Array<{ label: ReactNode; value: string }>
  placeholder?: string
  allowClear?: boolean
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
  /** 字段名序列（字符串化以稳定 effect 依赖：fields 由调用方每次渲染新建数组） */
  const fieldNamesKey = useMemo(() => fields.map((field) => field.name).join(','), [fields])

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
    for (const name of fieldNamesKey.split(',')) {
      if (!name) continue
      const value = initialValuesRecord[name]
      next[name] = value
      if (value !== undefined && value !== null && value !== '') applied += 1
    }
    form.setFieldsValue(next)
    setAppliedCount(applied)
  }, [hasInitialValues, initialValuesRecord, fieldNamesKey, form])

  const visibleFields = collapsed ? fields.slice(0, 3) : fields

  const handleFinish = (values: Record<string, unknown>) => {
    const cleaned = cleanValues(values)
    setAppliedCount(Object.keys(cleaned).length)
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
      style={{ marginBottom: 'var(--sf-space-3)' }}
    >
      {/* 弹性行：字段 flex 挤占 + 操作按钮同排尾部（f854d23 行为），仅去除外层重复下边距 */}
      <Flex gap="var(--sf-space-3)" wrap="wrap" align="middle">
        {visibleFields.map((field) => (
          <Form.Item
            key={field.name}
            name={field.name}
            label={field.label}
            style={{ marginBottom: 0, flex: '1 1 190px', maxWidth: 260, minWidth: 160 }}
          >
            {field.control === 'select' ? (
              <Select
                options={field.options}
                placeholder={field.placeholder ?? `请选择${field.label}`}
                allowClear={field.allowClear ?? true}
                style={{ width: '100%' }}
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
          {extraActions}
          {collapsible && (
            <Button type="link" onClick={() => form.setFieldValue('collapsed', !collapsed)}>
              {collapsed ? '展开' : '收起'}
            </Button>
          )}
        </Flex>
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

/** 去掉 undefined / 空字符串，避免空筛选污染 QueryKey */
function cleanValues(values: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null || value === '') continue
    if (key === 'collapsed') continue
    result[key] = value
  }
  return result
}
