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
  type EnabledStatus,
  type UnitItem,
  type UnitQuery,
  type UnitSavePayload,
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

interface UnitFormValues {
  code: string
  name: string
  status?: EnabledStatus
}

function toFormValues(record: UnitItem): UnitFormValues {
  return {
    code: record.code,
    name: record.name,
    status: record.status,
  }
}

function toPayload(values: UnitFormValues): UnitSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    status: values.status,
  }
}

/** 计量单位（/units，backend-m1-plan §5.4：code / name / status） */
export default function UnitListPage() {
  const [params, setParams] = useState<UnitQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<UnitItem | null>(null)
  const [form] = Form.useForm<UnitFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const list = usePagedList<UnitItem, UnitQuery>({
    queryKey: ['masterdata', 'units'],
    fetch: (q) => masterdataApi.units.list(q),
    params,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'units'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: UnitSavePayload) =>
      editing ? masterdataApi.units.update(editing.id, payload) : masterdataApi.units.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: UnitItem['id']) => masterdataApi.units.remove(id),
    onSuccess: invalidate,
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: UnitItem) => {
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

  const columns: ColumnsType<UnitItem> = [
    { title: '单位编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '单位名称',
      dataIndex: 'name',
      width: 200,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
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
      render: (_: unknown, record: UnitItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该单位？"
            description="被商品引用时后端将拒绝删除，建议改用停用。"
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
        title="计量单位"
        subtitle="商品计量单位（个 / 箱 / 千克等）维护"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建单位
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '单位编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as UnitQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<UnitItem>
          storageKey="masterdata-units"
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
          emptyText="暂无计量单位，点击右上角「新建单位」创建"
          scrollX={810}
        />
      </Card>

      <Modal
        title={editing ? '编辑单位' : '新建单位'}
        open={modalOpen}
        width={480}
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
        <Form<UnitFormValues> form={form} layout="vertical" initialValues={{ status: 'ENABLED' }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="单位编码" rules={[{ required: true, message: '请输入单位编码' }]}>
                <Input placeholder="唯一编码" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="单位名称" rules={[{ required: true, message: '请输入单位名称' }]}>
                <Input placeholder="如：个 / 箱 / 千克" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="status" label="状态">
                <Select options={STATUS_OPTIONS} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
