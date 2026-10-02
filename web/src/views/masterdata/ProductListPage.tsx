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
  type EnabledStatus,
  type ProductItem,
  type ProductQuery,
  type ProductSavePayload,
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
interface ProductFormValues {
  code: string
  name: string
  shortName?: string
  categoryId?: string
  brand?: string
  model?: string
  spec?: string
  unitId?: string
  weight?: number | null
  length?: number | null
  width?: number | null
  height?: number | null
  volume?: number | null
  description?: string
  remark?: string
  status?: EnabledStatus
}

function toFormValues(record: ProductItem): ProductFormValues {
  return {
    code: record.code,
    name: record.name,
    shortName: record.shortName,
    categoryId: record.categoryId != null ? String(record.categoryId) : undefined,
    brand: record.brand,
    model: record.model,
    spec: record.spec,
    unitId: record.unitId != null ? String(record.unitId) : undefined,
    weight: record.weight ?? null,
    length: record.length ?? null,
    width: record.width ?? null,
    height: record.height ?? null,
    volume: record.volume ?? null,
    description: record.description,
    remark: record.remark,
    status: record.status,
  }
}

function toPayload(values: ProductFormValues): ProductSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    shortName: values.shortName,
    categoryId: values.categoryId,
    brand: values.brand,
    model: values.model,
    spec: values.spec,
    unitId: values.unitId,
    weight: values.weight ?? undefined,
    length: values.length ?? undefined,
    width: values.width ?? undefined,
    height: values.height ?? undefined,
    volume: values.volume ?? undefined,
    description: values.description,
    remark: values.remark,
    status: values.status,
  }
}

const NUM_2 = { min: 0, precision: 2, style: { width: '100%' } } as const

/** 商品管理（/products，backend-m1-plan §5.4 masterdata 契约域） */
export default function ProductListPage() {
  const [params, setParams] = useState<ProductQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ProductItem | null>(null)
  const [form] = Form.useForm<ProductFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const list = usePagedList<ProductItem, ProductQuery>({
    queryKey: ['masterdata', 'products'],
    fetch: (q) => masterdataApi.products.list(q),
    params,
  })

  // 表单依赖下拉：分类 / 单位一次取全；接口失败时降级为空数组，不阻塞其余字段填写
  const categories = useQuery({
    queryKey: ['masterdata', 'categories', 'options'],
    queryFn: () => masterdataApi.categories.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const units = useQuery({
    queryKey: ['masterdata', 'units', 'options'],
    queryFn: () => masterdataApi.units.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })

  const categoryOptions = (categories.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))
  const unitOptions = (units.data?.items ?? []).map((item) => ({
    label: item.name,
    value: String(item.id),
  }))
  const categoryFilterOptions = (categories.data?.items ?? []).map((item) => ({
    label: item.name,
    value: String(item.id),
  }))

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'products'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: ProductSavePayload) =>
      editing ? masterdataApi.products.update(editing.id, payload) : masterdataApi.products.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: ProductItem['id']) => masterdataApi.products.remove(id),
    onSuccess: invalidate,
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: ProductItem) => {
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

  const columns: ColumnsType<ProductItem> = [
    { title: '商品编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '商品名称',
      dataIndex: 'name',
      width: 180,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    { title: '简称', dataIndex: 'shortName', width: 100, render: (v?: string) => v ?? '-' },
    { title: '分类', dataIndex: 'categoryName', width: 110, render: (v?: string) => v ?? '-' },
    { title: '品牌', dataIndex: 'brand', width: 100, render: (v?: string) => v ?? '-' },
    { title: '型号', dataIndex: 'model', width: 100, render: (v?: string) => v ?? '-' },
    { title: '规格', dataIndex: 'spec', width: 100, render: (v?: string) => v ?? '-' },
    { title: '单位', dataIndex: 'unitName', width: 80, render: (v?: string) => v ?? '-' },
    {
      title: '重量',
      dataIndex: 'weight',
      width: 90,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v, 2)}</span>,
    },
    {
      title: '长×宽×高',
      key: 'dims',
      width: 140,
      align: 'right',
      render: (_: unknown, record: ProductItem) => {
        const dims = [record.length, record.width, record.height]
        return dims.every((d) => d === undefined || d === null) ? (
          '-'
        ) : (
          <span className="sf-num">{dims.map((d) => formatNumber(d, 2)).join(' × ')}</span>
        )
      },
    },
    {
      title: '体积',
      dataIndex: 'volume',
      width: 90,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v, 2)}</span>,
    },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 140,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 140 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
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
      render: (_: unknown, record: ProductItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该商品？"
            description="已产生业务数据的商品后端将拒绝删除，建议改用停用。"
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
        title="商品管理"
        subtitle="商品档案：分类 / 品牌 / 规格 / 计量单位"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建商品
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '商品编码 / 名称' },
            { name: 'categoryId', label: '分类', control: 'select', options: categoryFilterOptions },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as ProductQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<ProductItem>
          storageKey="masterdata-products"
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
          emptyText="暂无商品，点击右上角「新建商品」创建"
          scrollX={1650}
        />
      </Card>

      <Modal
        title={editing ? '编辑商品' : '新建商品'}
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
        <Form<ProductFormValues> form={form} layout="vertical" initialValues={{ status: 'ENABLED' }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="商品编码" rules={[{ required: true, message: '请输入商品编码' }]}>
                <Input placeholder="唯一编码" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="商品名称" rules={[{ required: true, message: '请输入商品名称' }]}>
                <Input placeholder="请输入商品名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="shortName" label="简称">
                <Input placeholder="请输入简称" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="categoryId" label="商品分类">
                <Select options={categoryOptions} placeholder="请选择商品分类" allowClear />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="brand" label="品牌">
                <Input placeholder="请输入品牌" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="model" label="型号">
                <Input placeholder="请输入型号" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="spec" label="规格">
                <Input placeholder="请输入规格" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="unitId" label="计量单位">
                <Select options={unitOptions} placeholder="请选择计量单位" allowClear />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="weight" label="重量">
                <InputNumber {...NUM_2} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="volume" label="体积">
                <InputNumber {...NUM_2} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="length" label="长">
                <InputNumber {...NUM_2} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="width" label="宽">
                <InputNumber {...NUM_2} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="height" label="高">
                <InputNumber {...NUM_2} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="description" label="描述">
                <Input.TextArea rows={2} placeholder="请输入描述" maxLength={500} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="remark" label="备注">
                <Input.TextArea rows={2} placeholder="请输入备注" maxLength={500} />
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
