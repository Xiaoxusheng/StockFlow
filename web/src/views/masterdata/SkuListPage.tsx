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
  Switch,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
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

/** 表单值：InputNumber 可清空为 null，提交前统一转 undefined（对应"未填写"） */
interface SkuFormValues {
  code: string
  productId?: string
  costPrice?: number | null
  salePrice?: number | null
  safetyStock?: number | null
  maxStock?: number | null
  minReplenishQty?: number | null
  isBatchManaged?: boolean
  isExpiryManaged?: boolean
  isSerialManaged?: boolean
  isEnabled?: boolean
}

function toFormValues(record: SkuItem): SkuFormValues {
  return {
    code: record.code,
    productId: String(record.productId),
    costPrice: record.costPrice ?? null,
    salePrice: record.salePrice ?? null,
    safetyStock: record.safetyStock ?? null,
    maxStock: record.maxStock ?? null,
    minReplenishQty: record.minReplenishQty ?? null,
    isBatchManaged: record.isBatchManaged ?? false,
    isExpiryManaged: record.isExpiryManaged ?? false,
    isSerialManaged: record.isSerialManaged ?? false,
    isEnabled: record.isEnabled ?? true,
  }
}

function toPayload(values: SkuFormValues): SkuSavePayload {
  return {
    code: values.code.trim(),
    productId: values.productId ?? '',
    costPrice: values.costPrice ?? undefined,
    salePrice: values.salePrice ?? undefined,
    safetyStock: values.safetyStock ?? undefined,
    maxStock: values.maxStock ?? undefined,
    minReplenishQty: values.minReplenishQty ?? undefined,
    isBatchManaged: values.isBatchManaged ?? false,
    isExpiryManaged: values.isExpiryManaged ?? false,
    isSerialManaged: values.isSerialManaged ?? false,
    isEnabled: values.isEnabled ?? true,
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
      dataIndex: 'productName',
      width: 180,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '采购价',
      dataIndex: 'costPrice',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '销售价',
      dataIndex: 'salePrice',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '安全库存',
      dataIndex: 'safetyStock',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '最大库存',
      dataIndex: 'maxStock',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '最小补货量',
      dataIndex: 'minReplenishQty',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    { title: '批次管理', dataIndex: 'isBatchManaged', width: 90, render: (v?: boolean) => (v ? '是' : '否') },
    { title: '效期管理', dataIndex: 'isExpiryManaged', width: 90, render: (v?: boolean) => (v ? '是' : '否') },
    { title: '序列号管理', dataIndex: 'isSerialManaged', width: 100, render: (v?: boolean) => (v ? '是' : '否') },
    {
      title: '状态',
      dataIndex: 'isEnabled',
      width: 90,
      render: (v?: boolean) => <SfStatusTag status={v ? 'enabled' : 'disabled'} />,
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
      render: (_: unknown, record: SkuItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该 SKU？"
            description="已产生业务数据的 SKU 后端将拒绝删除，建议改用停用。"
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
        title="SKU 管理"
        subtitle="SKU 档案：价格 / 库存阈值 / 批次·效期·序列号开关"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建 SKU
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[{ name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU 编码 / 所属商品' }]}
          onSearch={(values) => {
            setParams(values as SkuQuery)
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
          scrollX={1490}
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
          initialValues={{ isEnabled: true, isBatchManaged: false, isExpiryManaged: false, isSerialManaged: false }}
        >
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="SKU 编码" rules={[{ required: true, message: '请输入 SKU 编码' }]}>
                <Input placeholder="唯一编码" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="productId" label="所属商品" rules={[{ required: true, message: '请选择所属商品' }]}>
                <Select
                  options={productOptions}
                  placeholder="请选择所属商品"
                  showSearch
                  optionFilterProp="label"
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="costPrice" label="采购价">
                <InputNumber {...MONEY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="salePrice" label="销售价">
                <InputNumber {...MONEY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="safetyStock" label="安全库存">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="maxStock" label="最大库存">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="minReplenishQty" label="最小补货量">
                <InputNumber {...QTY} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Row gutter={16}>
                <Col span={6}>
                  <Form.Item name="isBatchManaged" label="批次管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="isExpiryManaged" label="效期管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="isSerialManaged" label="序列号管理" valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="isEnabled" label="启用" valuePropName="checked">
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
