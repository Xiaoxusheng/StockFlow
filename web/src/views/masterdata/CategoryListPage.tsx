import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  OPTIONS_PAGE_SIZE,
  masterdataApi,
  toStatusKey,
  type CategoryItem,
  type CategoryQuery,
  type CategorySavePayload,
  type EnabledStatus,
} from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

/** 表单值：InputNumber 可清空为 null，提交前统一转 undefined（对应"未填写"） */
interface CategoryFormValues {
  code: string
  name: string
  parentId?: string
  sort?: number | null
  status?: EnabledStatus
}

function toFormValues(record: CategoryItem): CategoryFormValues {
  return {
    code: record.code,
    name: record.name,
    parentId: record.parentId != null ? String(record.parentId) : undefined,
    sort: record.sort ?? null,
    status: record.status,
  }
}

function toPayload(values: CategoryFormValues): CategorySavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    parentId: values.parentId ?? null,
    sort: values.sort ?? undefined,
    status: values.status,
  }
}

/** 商品分类（/categories，后端端点 /api/product-categories，backend-m1-plan §5.4） */
export default function CategoryListPage() {
  const [params, setParams] = useState<CategoryQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<CategoryItem | null>(null)
  const [form] = Form.useForm<CategoryFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const list = usePagedList<CategoryItem, CategoryQuery>({
    queryKey: ['masterdata', 'categories'],
    fetch: (q) => masterdataApi.categories.list(q),
    params,
  })

  // 上级分类下拉：一次取全，编辑时排除自身；接口失败时降级为空数组，不阻塞其余字段填写
  const categories = useQuery({
    queryKey: ['masterdata', 'categories', 'options'],
    queryFn: () => masterdataApi.categories.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const parentOptions = (categories.data?.items ?? [])
    .filter((item) => editing === null || String(item.id) !== String(editing.id))
    .map((item) => ({ label: `${item.name}（${item.code}）`, value: String(item.id) }))

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'categories'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: CategorySavePayload) =>
      editing
        ? masterdataApi.categories.update(editing.id, payload)
        : masterdataApi.categories.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: CategoryItem['id']) => masterdataApi.categories.remove(id),
    onSuccess: invalidate,
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: CategoryItem) => {
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

  const columns: ColumnsType<CategoryItem> = [
    { title: '分类编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '分类名称',
      dataIndex: 'name',
      width: 200,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    {
      title: '上级分类',
      dataIndex: 'parentName',
      width: 180,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '排序',
      dataIndex: 'sort',
      width: 90,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
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
      render: (_: unknown, record: CategoryItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该分类？"
            description="存在子分类或被商品引用时后端将拒绝删除。"
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
        title="商品分类"
        subtitle="分类编码 / 名称 / 层级（parent_id）维护"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建分类
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '分类编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as CategoryQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<CategoryItem>
          storageKey="masterdata-categories"
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
          emptyText="暂无商品分类，点击右上角「新建分类」创建"
          scrollX={970}
        />
      </Card>

      <Modal
        title={editing ? '编辑分类' : '新建分类'}
        open={modalOpen}
        width={520}
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
        <Form<CategoryFormValues> form={form} layout="vertical" initialValues={{ status: 'ENABLED', sort: 0 }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="分类编码" rules={[{ required: true, message: '请输入分类编码' }]}>
                <Input placeholder="唯一编码" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="分类名称" rules={[{ required: true, message: '请输入分类名称' }]}>
                <Input placeholder="请输入分类名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="parentId" label="上级分类">
                <Select options={parentOptions} placeholder="不选则为顶级分类" allowClear />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="sort" label="排序">
                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
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
