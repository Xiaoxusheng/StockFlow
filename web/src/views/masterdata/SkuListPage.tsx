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
  Switch,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { SfConfirm } from '@/components/common/SfConfirm'
import {
  OPTIONS_PAGE_SIZE,
  masterdataApi,
  type SkuItem,
  type SkuQuery,
  type SkuSavePayload,
} from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

const ENABLED_FILTER_OPTIONS = [
  { label: '已启用', value: 'true' },
  { label: '已停用', value: 'false' },
]

/** 表单值：InputNumber 可清空为 null，提交前统一转 undefined（对应"未填写"）；
 * 字段名与后端 JSON tag（snake_case）一致 */
interface SkuFormValues {
  code: string
  product_id?: string
  cost_price?: number | null
  sale_price?: number | null
  safety_stock?: number | null
  max_stock?: number | null
  min_replenish_qty?: number | null
  is_batch_managed?: boolean
  is_expiry_managed?: boolean
  is_serial_managed?: boolean
  is_enabled?: boolean
}

function toFormValues(record: SkuItem): SkuFormValues {
  return {
    code: record.code,
    product_id: String(record.product_id),
    cost_price: record.cost_price ?? null,
    sale_price: record.sale_price ?? null,
    safety_stock: record.safety_stock ?? null,
    max_stock: record.max_stock ?? null,
    min_replenish_qty: record.min_replenish_qty ?? null,
    is_batch_managed: record.is_batch_managed,
    is_expiry_managed: record.is_expiry_managed,
    is_serial_managed: record.is_serial_managed,
    is_enabled: record.is_enabled,
  }
}

/** 提交契约（SKUCreateInput/SKUUpdateInput，service_sku.go:146-176）：
 * product_id 为 int64 必须 number；barcodes 省略=更新不修改既有条码 */
function toPayload(values: SkuFormValues): SkuSavePayload {
  return {
    code: values.code.trim(),
    product_id: Number(values.product_id),
    cost_price: values.cost_price ?? undefined,
    sale_price: values.sale_price ?? undefined,
    safety_stock: values.safety_stock ?? undefined,
    max_stock: values.max_stock ?? undefined,
    min_replenish_qty: values.min_replenish_qty ?? undefined,
    is_batch_managed: values.is_batch_managed ?? false,
    is_expiry_managed: values.is_expiry_managed ?? false,
    is_serial_managed: values.is_serial_managed ?? false,
    is_enabled: values.is_enabled ?? true,
  }
}

const QTY = { min: 0, precision: 0, style: { width: '100%' } } as const
const MONEY = { min: 0, precision: 2, style: { width: '100%' } } as const

/** SKU 管理（/skus，backend-m1-plan §5.4 masterdata 契约域；三开关决定 M2 入库/出库业务分支） */
export default function SkuListPage() {
  const [params, setParams] = useState<SkuQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<SkuItem | null>(null)
  const [form] = Form.useForm<SkuFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const list = usePagedList<SkuItem, SkuQuery>({
    queryKey: ['masterdata', 'skus'],
    fetch: (q) => masterdataApi.skus.list(q),
    params,
  })

  // 表单依赖下拉：所属商品一次取全；接口失败时降级为空数组，不阻塞其余字段填写
  const products = useQuery({
    queryKey: ['masterdata', 'products', 'options'],
    queryFn: () => masterdataApi.products.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const productOptions = (products.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))
  // 后端列表不装配 product_name（omitempty，service_sku.go:43-44 仅详情返回），
  // 列表展示用一次取全的商品数据源按 product_id 兜底映射（同一 API 的真实数据）
  const productNameById = useMemo(
    () => new Map((products.data?.items ?? []).map((item) => [String(item.id), item.name])),
    [products.data],
  )

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'skus'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: SkuSavePayload) =>
      editing ? masterdataApi.skus.update(editing.id, payload) : masterdataApi.skus.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: SkuItem['id']; enabled: boolean }) =>
      masterdataApi.skus.setStatus(id, { enabled }),
    onSuccess: (data) => {
      messageApi.success(data.is_enabled ? '已启用' : '已停用')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: SkuItem['id']) => masterdataApi.skus.remove(id),
    onSuccess: invalidate,
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: SkuItem) => {
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

  const columns: ColumnsType<SkuItem> = [
    { title: 'SKU 编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '所属商品',
      key: 'product_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: SkuItem) => {
        const name =
          record.product_name ?? productNameById.get(String(record.product_id)) ?? '-'
        return name === '-' ? (
          '-'
        ) : (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '条码',
      key: 'barcodes',
      width: 200,
      ellipsis: true,
      render: (_: unknown, record: SkuItem) => {
        const codes = record.barcodes ?? []
        if (codes.length === 0) return '-'
        const text = codes.map((b) => (b.is_primary ? `${b.barcode}（主）` : b.barcode)).join('、')
        return (
          <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: text }}>
            {text}
          </Text>
        )
      },
    },
    {
      title: '采购价',
      dataIndex: 'cost_price',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '销售价',
      dataIndex: 'sale_price',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '安全库存',
      dataIndex: 'safety_stock',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '最大库存',
      dataIndex: 'max_stock',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '最小补货量',
      dataIndex: 'min_replenish_qty',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '批次管理',
      dataIndex: 'is_batch_managed',
      width: 90,
      render: (v?: boolean) => (v ? '是' : '否'),
    },
    {
      title: '效期管理',
      dataIndex: 'is_expiry_managed',
      width: 90,
      render: (v?: boolean) => (v ? '是' : '否'),
    },
    {
      title: '序列号管理',
      dataIndex: 'is_serial_managed',
      width: 100,
      render: (v?: boolean) => (v ? '是' : '否'),
    },
    {
      title: '状态',
      dataIndex: 'is_enabled',
      width: 90,
      render: (v: boolean) => <SfStatusTag status={v ? 'enabled' : 'disabled'} />,
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
      render: (_: unknown, record: SkuItem) => {
        const disabling = record.is_enabled
        return (
          <span style={{ whiteSpace: 'nowrap' }}>
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
            <SfConfirm
              title={disabling ? '确认停用该 SKU？' : '确认启用该 SKU？'}
              description={
                disabling
                  ? '停用后 SKU 不可被出库分配；启用后恢复正常参与业务。'
                  : '启用后 SKU 恢复参与入库/出库业务。'
              }
              okText={disabling ? '停用' : '启用'}
              confirming={statusMutation.isPending}
              onConfirm={() => statusMutation.mutate({ id: record.id, enabled: !disabling })}
            >
              <Button type="link" size="small" danger={disabling}>
                {disabling ? '停用' : '启用'}
              </Button>
            </SfConfirm>
            <SfConfirm
              title="确认删除该 SKU？"
              description="已产生业务数据的 SKU 后端将拒绝删除，建议改用停用。"
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
        title="SKU 管理"
        subtitle="SKU 档案：条码 / 价格 / 库存阈值 / 批次·效期·序列号开关"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建 SKU
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU 编码 / 所属商品' },
            { name: 'product_id', label: '所属商品', control: 'select', options: productOptions },
            { name: 'enabled', label: '启停', control: 'select', options: ENABLED_FILTER_OPTIONS },
          ]}
          onSearch={(values) => {
            // SfSearchForm 下拉值为字符串，enabled 转布尔（后端 strconv.ParseBool）
            const raw = values as Record<string, unknown>
            const enabledRaw = raw['enabled']
            setParams({
              keyword: typeof raw['keyword'] === 'string' ? raw['keyword'] : undefined,
              product_id: typeof raw['product_id'] === 'string' ? raw['product_id'] : undefined,
              enabled: enabledRaw === 'true' ? true : enabledRaw === 'false' ? false : undefined,
            })
            list.resetToFirstPage()
          }}
        />
        <SfTable<SkuItem>
          storageKey="masterdata-skus"
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
          emptyText="暂无 SKU，点击右上角「新建 SKU」创建"
          scrollX={1800}
        />
      </Card>

      <Modal
        title={editing ? '编辑 SKU' : '新建 SKU'}
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
        <Form<SkuFormValues>
          form={form}
          layout="vertical"
          initialValues={{ is_enabled: true, is_batch_managed: false, is_expiry_managed: false, is_serial_managed: false }}
        >
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="code"
                label="SKU 编码"
                rules={[{ required: true, message: '请输入 SKU 编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={64} disabled={editing !== null} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="product_id" label="所属商品" rules={[{ required: true, message: '请选择所属商品' }]}>
                <Select
                  options={productOptions}
                  placeholder="请选择所属商品"
                  showSearch
                  optionFilterProp="label"
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="cost_price" label="采购价">
                <InputNumber {...MONEY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="sale_price" label="销售价">
                <InputNumber {...MONEY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="safety_stock" label="安全库存">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="max_stock" label="最大库存">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="min_replenish_qty" label="最小补货量">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Row gutter={16}>
                <Col span={6}>
                  <Form.Item name="is_batch_managed" label="批次管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="is_expiry_managed" label="效期管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="is_serial_managed" label="序列号管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="is_enabled" label="启用" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
              </Row>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
