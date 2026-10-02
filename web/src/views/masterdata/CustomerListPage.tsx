import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Popconfirm,
  Row,
  Select,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  masterdataApi,
  toStatusKey,
  type CustomerItem,
  type CustomerQuery,
  type CustomerSavePayload,
  type EnabledStatus,
} from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

interface CustomerFormValues {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  shippingAddress?: string
  status?: EnabledStatus
}

function toFormValues(record: CustomerItem): CustomerFormValues {
  return {
    code: record.code,
    name: record.name,
    contact: record.contact,
    phone: record.phone,
    email: record.email,
    address: record.address,
    shippingAddress: record.shippingAddress,
    status: record.status,
  }
}

function toPayload(values: CustomerFormValues): CustomerSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    contact: values.contact,
    phone: values.phone,
    email: values.email,
    address: values.address,
    shippingAddress: values.shippingAddress,
    status: values.status,
  }
}

/** 客户管理（/customers，backend-m1-plan §5.4：含 shipping_address 收货地址） */
export default function CustomerListPage() {
  const [params, setParams] = useState<CustomerQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<CustomerItem | null>(null)
  const [form] = Form.useForm<CustomerFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const list = usePagedList<CustomerItem, CustomerQuery>({
    queryKey: ['masterdata', 'customers'],
    fetch: (q) => masterdataApi.customers.list(q),
    params,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'customers'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: CustomerSavePayload) =>
      editing
        ? masterdataApi.customers.update(editing.id, payload)
        : masterdataApi.customers.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: CustomerItem['id']) => masterdataApi.customers.remove(id),
    onSuccess: invalidate,
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: CustomerItem) => {
    saveMutation.reset()
    setEditing(record)
    setModalOpen(true)
    form.setFieldsValue(toFormValues(record))
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => saveMutation.mutate(toPayload(values)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const columns: ColumnsType<CustomerItem> = [
    { title: '客户编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '客户名称',
      dataIndex: 'name',
      width: 180,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    { title: '联系人', dataIndex: 'contact', width: 110, render: (v?: string) => v ?? '-' },
    {
      title: '联系电话',
      dataIndex: 'phone',
      width: 140,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{v ?? '-'}</span>,
    },
    { title: '邮箱', dataIndex: 'email', width: 180, render: (v?: string) => v ?? '-' },
    {
      title: '地址',
      dataIndex: 'address',
      width: 180,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '收货地址',
      dataIndex: 'shippingAddress',
      width: 180,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: EnabledStatus) => <SfStatusTag status={toStatusKey(v)} />,
    },
    {
      title: '更新时间',
      dataIndex: 'updatedAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 110,
      render: (_: unknown, record: CustomerItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该客户？"
            description="已产生销售业务的客户后端将拒绝删除，建议改用停用。"
            okText="删除"
            okButtonProps={{ danger: true, loading: removeMutation.isPending }}
            onConfirm={() => removeMutation.mutate(record.id)}
          >
            <Button type="link" size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </span>
      ),
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="客户管理"
        subtitle="客户档案、联系信息与收货地址"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建客户
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '客户编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as CustomerQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<CustomerItem>
          storageKey="masterdata-customers"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="暂无客户，点击右上角「新建客户」创建"
          scrollX={1470}
        />
      </Card>

      <Modal
        title={editing ? '编辑客户' : '新建客户'}
        open={modalOpen}
        width={640}
        forceRender
        confirmLoading={saveMutation.isPending}
        okText={editing ? '保存' : '创建'}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        {saveMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(saveMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form<CustomerFormValues> form={form} layout="vertical" initialValues={{ status: 'ENABLED' }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="客户编码" rules={[{ required: true, message: '请输入客户编码' }]}>
                <Input placeholder="唯一编码" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="客户名称" rules={[{ required: true, message: '请输入客户名称' }]}>
                <Input placeholder="请输入客户名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="contact" label="联系人">
                <Input placeholder="请输入联系人" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="phone" label="联系电话">
                <Input placeholder="请输入联系电话" maxLength={32} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="email"
                label="邮箱"
                rules={[{ type: 'email', message: '邮箱格式不正确' }]}
              >
                <Input placeholder="请输入邮箱" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="status" label="状态">
                <Select options={STATUS_OPTIONS} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="address" label="地址">
                <Input.TextArea rows={2} placeholder="请输入地址" maxLength={255} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="shippingAddress" label="收货地址">
                <Input.TextArea rows={2} placeholder="请输入收货地址" maxLength={255} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
