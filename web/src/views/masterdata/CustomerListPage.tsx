import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Row,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { SfConfirm } from '@/components/common/SfConfirm'
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
  shipping_address?: string
}

function toFormValues(record: CustomerItem): CustomerFormValues {
  return {
    code: record.code,
    name: record.name,
    contact: record.contact,
    phone: record.phone,
    email: record.email,
    address: record.address,
    shipping_address: record.shipping_address,
  }
}

/** 提交契约（CustomerCreateInput/CustomerUpdateInput，service_partner.go:108-126）：
 * 后端无 status 字段——创建恒 ENABLED、启停走 PUT /customers/:id/status，编码创建后不可改 */
function toPayload(values: CustomerFormValues): CustomerSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    contact: values.contact,
    phone: values.phone,
    email: values.email,
    address: values.address,
    shipping_address: values.shipping_address,
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

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: CustomerItem['id']; status: EnabledStatus }) =>
      masterdataApi.customers.setStatus(id, { status }),
    onSuccess: (data) => {
      messageApi.success(data.status === 'ENABLED' ? '已启用' : '已停用')
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
      dataIndex: 'shipping_address',
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
      dataIndex: 'updated_at',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 150,
      render: (_: unknown, record: CustomerItem) => {
        const disabling = record.status === 'ENABLED'
        return (
          <span style={{ whiteSpace: 'nowrap' }}>
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
            <SfConfirm
              title={disabling ? '确认停用该客户？' : '确认启用该客户？'}
              description={
                disabling
                  ? '停用后不可再被新销售业务引用，已有业务记录不受影响。'
                  : '启用后客户可重新参与销售业务。'
              }
              okText={disabling ? '停用' : '启用'}
              confirming={statusMutation.isPending}
              onConfirm={() =>
                statusMutation.mutate({ id: record.id, status: disabling ? 'DISABLED' : 'ENABLED' })
              }
            >
              <Button type="link" size="small" danger={disabling}>
                {disabling ? '停用' : '启用'}
              </Button>
            </SfConfirm>
            <SfConfirm
              title="确认删除该客户？"
              description="删除为软删除，后端当前不校验业务引用（引用校验随后续版本交付）：已产生销售业务的客户删除后将从列表与下拉消失，历史单据中将按 ID 显示，建议改用停用。"
              okText="删除"
              confirming={removeMutation.isPending}
              onConfirm={() => removeMutation.mutate(record.id)}
            >
              <Button type="link" size="small" danger>
                删除
              </Button>
            </SfConfirm>
          </span>
        )
      },
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
          scrollX={1510}
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
        <Form<CustomerFormValues> form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="code"
                label="客户编码"
                rules={[{ required: true, message: '请输入客户编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={64} disabled={editing !== null} />
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
            <Col span={24}>
              <Form.Item name="address" label="地址">
                <Input.TextArea rows={2} placeholder="请输入地址" maxLength={255} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="shipping_address" label="收货地址">
                <Input.TextArea rows={2} placeholder="请输入收货地址" maxLength={255} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
