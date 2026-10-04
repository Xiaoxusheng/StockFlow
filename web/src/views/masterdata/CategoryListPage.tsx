import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { SfConfirm } from '@/components/common/SfConfirm'
import {
  masterdataApi,
  toStatusKey,
  type CategoryItem,
  type CategoryQuery,
  type CategorySavePayload,
  type EnabledStatus,
} from '@/api/masterdata'
import { fetchCategoryOptions } from '@/api/options'
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

/** 表单值；字段名与后端 JSON tag（snake_case）一致 */
interface CategoryFormValues {
  code: string
  name: string
  parent_id?: string
  sort?: number | null
}

/** 提交契约（CategoryCreateInput/CategoryUpdateInput，service_category.go:72-77/134-138）：
 * parent_id 为 *int64 必须 number。创建：null=顶级；更新：0=提升为顶级 / >0=换上级
 * （编辑表单清空选择即提交 0=置顶级，未变更时原值回传无副作用） */
function toPayload(values: CategoryFormValues, isEdit: boolean): CategorySavePayload {
  return {
    parent_id: isEdit
      ? values.parent_id != null
        ? Number(values.parent_id)
        : 0
      : values.parent_id != null
        ? Number(values.parent_id)
        : null,
    code: values.code.trim(),
    name: values.name.trim(),
    sort: values.sort ?? undefined,
  }
}

/** 商品分类（/categories，后端端点 /api/product-categories，backend-m1-plan §5.4；
 * 无删除接口——停用即生命周期终点，masterdata.go:22/66） */
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

  // 上级分类下拉：分页取全（fetchCategoryOptions，超一页不截断），编辑时排除自身；
  // 接口失败时降级为空数组，不阻塞其余字段填写
  const categories = useQuery({
    queryKey: ['masterdata', 'categories', 'options'],
    queryFn: fetchCategoryOptions,
  })
  const parentOptions = (categories.data ?? [])
    .filter((item) => editing === null || String(item.id) !== String(editing.id))
    .map((item) => ({ label: `${item.name}（${item.code}）`, value: String(item.id) }))
  // 后端 CategoryView 无 parent_name 装配字段（service_category.go:24-33），
  // 列表「上级分类」用一次取全的数据源按 parent_id 兜底映射（同一 API 的真实数据）
  const parentNameById = useMemo(
    () => new Map((categories.data ?? []).map((item) => [String(item.id), item.name])),
    [categories.data],
  )

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'categories'] })
  }

  const saveMutation = useMutation({
    mutationFn: ({ payload, isEdit }: { payload: CategorySavePayload; isEdit: boolean }) =>
      isEdit && editing
        ? masterdataApi.categories.update(editing.id, payload)
        : masterdataApi.categories.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: CategoryItem['id']; status: EnabledStatus }) =>
      masterdataApi.categories.setStatus(id, { status }),
    onSuccess: (data) => {
      messageApi.success(data.status === 'ENABLED' ? '已启用' : '已停用')
      invalidate()
    },
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
    form.setFieldsValue({
      code: record.code,
      name: record.name,
      parent_id: record.parent_id != null ? String(record.parent_id) : undefined,
      sort: record.sort ?? null,
    })
  }

  const handleSubmit = () => {
    const isEdit = editing !== null
    form
      .validateFields()
      .then((values) => saveMutation.mutate({ payload: toPayload(values, isEdit), isEdit }))
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
      key: 'parent_id',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: CategoryItem) => {
        if (record.parent_id == null) return '-'
        const name = parentNameById.get(String(record.parent_id))
        return name ? (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          String(record.parent_id)
        )
      },
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
      dataIndex: 'updated_at',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 120,
      render: (_: unknown, record: CategoryItem) => {
        const disabling = record.status === 'ENABLED'
        return (
          <span style={{ whiteSpace: 'nowrap' }}>
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
            <SfConfirm
              title={disabling ? '确认停用该分类？' : '确认启用该分类？'}
              description={
                disabling
                  ? '存在启用中的子分类或商品引用时后端将拒绝停用；建议先处理引用。'
                  : '启用后分类可重新被商品引用。'
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
          </span>
        )
      },
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="商品分类"
        subtitle="分类编码 / 名称 / 层级（parent_id）维护；分类无删除，停用即终点"
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
          scrollX={940}
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
        <Form<CategoryFormValues> form={form} layout="vertical" initialValues={{ sort: 0 }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="code"
                label="分类编码"
                rules={[{ required: true, message: '请输入分类编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={64} disabled={editing !== null} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="分类名称" rules={[{ required: true, message: '请输入分类名称' }]}>
                <Input placeholder="请输入分类名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="parent_id" label="上级分类" extra={editing ? '清空并保存则提升为顶级' : undefined}>
                <Select options={parentOptions} placeholder="不选则为顶级分类" allowClear />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="sort" label="排序">
                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
