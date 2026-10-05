import { useState } from 'react'
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
}

/**
 * 统一查询表单（frontend.md §6.3）。
 * 非受控实现：提交时把值交给页面状态，页面据此触发 usePagedList 请求。
 */
export function SfSearchForm({ fields, onSearch, onReset, loading, collapsible = fields.length >= 5 }: SfSearchFormProps) {
  const [form] = Form.useForm()
  const collapsed = useWatch('collapsed', form) ?? collapsible
  // 任务书 §50：已生效筛选条件数——只在用户提交后增长，如实反映当前过滤状态
  const [appliedCount, setAppliedCount] = useState(0)

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
            style={{ marginBottom: 0, flex: '1 1 240px', maxWidth: 360, minWidth: 200 }}
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
