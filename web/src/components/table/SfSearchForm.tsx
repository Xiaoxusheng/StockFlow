import { Button, Col, Flex, Form, Row, Select, Input } from 'antd'
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

  const visibleFields = collapsed ? fields.slice(0, 3) : fields

  const handleFinish = (values: Record<string, unknown>) => {
    onSearch(cleanValues(values))
  }

  const handleReset = () => {
    form.resetFields()
    onReset?.()
    onSearch({})
  }

  return (
    <Form
      form={form}
      onFinish={handleFinish}
      initialValues={{ collapsed }}
      style={{ marginBottom: 12 }}
    >
      <Row gutter={[12, 12]} align="middle">
        {visibleFields.map((field) => (
          <Col key={field.name} xs={24} sm={12} md={8} lg={6} xxl={5}>
            <Form.Item
              name={field.name}
              label={field.label}
              style={{ marginBottom: 0 }}
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
          </Col>
        ))}
        <Col xs={24} sm={24} md={8} lg={6} xxl={5}>
          <Flex gap={8} wrap="wrap">
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
        </Col>
      </Row>
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
