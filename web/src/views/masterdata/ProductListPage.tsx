import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Typography,
  message,
} from 'antd'
import { MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import type { MenuProps } from 'antd'
import { CodeCell, DateCell } from '@/components/table/cells'
import {
  masterdataApi,
  toStatusKey,
  type EnabledStatus,
  type ProductItem,
  type ProductQuery,
  type ProductSavePayload,
} from '@/api/masterdata'
import { fetchCategoryOptions, fetchUnitOptions } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

const { Text, Paragraph } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

/** 表单值：InputNumber 可清空为 null，提交前统一转 undefined（对应"未填写"）；
 * 字段名与后端 JSON tag（snake_case）一致，减少映射出错面 */
interface ProductFormValues {
  code: string
  name: string
  short_name?: string
  category_id?: string
  brand?: string
  model?: string
  spec?: string
  unit_id?: string
  weight?: number | null
  length?: number | null
  width?: number | null
  height?: number | null
  volume?: number | null
  description?: string
  remark?: string
}

function toFormValues(record: ProductItem): ProductFormValues {
  return {
    code: record.code,
    name: record.name,
    short_name: record.short_name,
    category_id: record.category_id != null ? String(record.category_id) : undefined,
    brand: record.brand,
    model: record.model,
    spec: record.spec,
    unit_id: record.unit_id != null ? String(record.unit_id) : undefined,
    weight: record.weight ?? null,
    length: record.length ?? null,
    width: record.width ?? null,
    height: record.height ?? null,
    volume: record.volume ?? null,
    description: record.description,
    remark: record.remark,
  }
}

/** 提交契约（ProductCreateInput/UpdateInput，service_product.go:100-136）：
 * 外键 *int64 必须 number；null/省略=创建不设置 / 更新不修改（指针三态） */
function toPayload(values: ProductFormValues): ProductSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    short_name: values.short_name,
    category_id: values.category_id != null ? Number(values.category_id) : null,
    brand: values.brand,
    model: values.model,
    spec: values.spec,
    unit_id: values.unit_id != null ? Number(values.unit_id) : null,
    weight: values.weight ?? undefined,
    length: values.length ?? undefined,
    width: values.width ?? undefined,
    height: values.height ?? undefined,
    volume: values.volume ?? undefined,
    description: values.description,
    remark: values.remark,
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
  // 动效 #7/删除行（frontend.md §31）：行淡色反馈 / 删除行 fade→收缩——仅在 API 成败回调后触发
  const fb = useTableRowFeedback()

  const list = usePagedList<ProductItem, ProductQuery>({
    queryKey: ['masterdata', 'products'],
    fetch: (q) => masterdataApi.products.list(q),
    params,
  })

  // 表单依赖下拉：分类 / 单位分页取全（fetchCategoryOptions/fetchUnitOptions，超一页不截断）；
  // 接口失败时降级为空数组，不阻塞其余字段填写
  const categories = useQuery({
    queryKey: ['masterdata', 'categories', 'options'],
    queryFn: fetchCategoryOptions,
  })
  const units = useQuery({
    queryKey: ['masterdata', 'units', 'options'],
    queryFn: fetchUnitOptions,
  })

  const categoryOptions = (categories.data ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))
  const unitOptions = (units.data ?? []).map((item) => ({
    label: item.name,
    value: String(item.id),
  }))
  const categoryFilterOptions = (categories.data ?? []).map((item) => ({
    label: item.name,
    value: String(item.id),
  }))

  // 后端列表不装配 category_name/unit_name（omitempty，service_product.go:29/34 仅详情返回），
  // 列表展示用一次取全的下拉数据源按 id 兜底映射（同一 API 的真实数据，非前端造数）
  const categoryNameById = useMemo(
    () => new Map((categories.data ?? []).map((item) => [String(item.id), item.name])),
    [categories.data],
  )
  const unitNameById = useMemo(
    () => new Map((units.data ?? []).map((item) => [String(item.id), item.name])),
    [units.data],
  )

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

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: ProductItem['id']; status: EnabledStatus }) =>
      masterdataApi.products.setStatus(id, { status }),
    onSuccess: (data, { id }) => {
      const cascaded = data.cascade_disabled_skus ?? 0
      if (data.status === 'DISABLED' && cascaded > 0) {
        messageApi.success(`已停用，并级联停用 ${cascaded} 个启用中的 SKU`)
      } else {
        messageApi.success(data.status === 'ENABLED' ? '已启用' : '已停用')
      }
      invalidate()
      fb.trigger(id, 'success')
    },
    onError: (error, { id }) => {
      messageApi.error(resolveErrorMessage(error))
      fb.trigger(id, 'error')
    },
  })

  const removeMutation = useMutation({
    mutationFn: (id: ProductItem['id']) => masterdataApi.products.remove(id),
    onSuccess: (_data, id) => {
      // 删除行动效：fade→收缩→过滤 DOM（refetch 成功自愈 / 失败 2.5s 兜底恢复显示）
      fb.triggerRemove(id)
      invalidate()
    },
    onError: (error, id) => {
      messageApi.error(resolveErrorMessage(error))
      fb.trigger(id, 'error')
    },
  })

  // 「更多」菜单内 停用/启用/删除 的二次确认：SfConfirm 为 Popconfirm 形态，无法锚定在点击后
  // 即关闭的 Dropdown 菜单项内——改用同语义声明式 Modal（danger ok + confirmLoading）
  const [rowConfirm, setRowConfirm] = useState<{ kind: 'toggle' | 'remove'; record: ProductItem } | null>(null)

  /** 行内「更多」菜单（SkuListPage buildRowMenu 同构）：停用/启用/删除收进更多，操作列只留 编辑 + 更多 */
  const buildRowMenu = (record: ProductItem): MenuProps => {
    const disabling = record.status === 'ENABLED'
    return {
      items: [
        { key: 'toggle', label: disabling ? '停用' : '启用', danger: disabling },
        { type: 'divider' },
        { key: 'remove', label: '删除', danger: true },
      ],
      onClick: ({ key }) => {
        if (key === 'toggle') setRowConfirm({ kind: 'toggle', record })
        if (key === 'remove') setRowConfirm({ kind: 'remove', record })
      },
    }
  }

  const handleRowConfirmOk = () => {
    if (!rowConfirm) return
    const { kind, record } = rowConfirm
    if (kind === 'toggle') {
      statusMutation.mutate(
        { id: record.id, status: record.status === 'ENABLED' ? 'DISABLED' : 'ENABLED' },
        { onSuccess: () => setRowConfirm(null) },
      )
    } else {
      removeMutation.mutate(record.id, { onSuccess: () => setRowConfirm(null) })
    }
  }

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
    {
      title: '商品编码',
      dataIndex: 'code',
      width: 130,
      fixed: 'left',
      render: (v: string) => <CodeCell value={v} label="商品编码" />,
    },
    {
      title: '商品名称',
      dataIndex: 'name',
      width: 180,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    { title: '简称', dataIndex: 'short_name', width: 100, render: (v?: string) => v ?? '-' },
    {
      title: '分类',
      key: 'category_name',
      width: 110,
      render: (_: unknown, record: ProductItem) =>
        record.category_name ??
        (record.category_id != null ? categoryNameById.get(String(record.category_id)) : undefined) ??
        '-',
    },
    { title: '品牌', dataIndex: 'brand', width: 100, render: (v?: string) => v ?? '-' },
    { title: '型号', dataIndex: 'model', width: 100, render: (v?: string) => v ?? '-' },
    {
      title: '规格',
      dataIndex: 'spec',
      width: 100,
      ellipsis: true,
      render: (v?: string) => v ?? '-',
    },
    {
      title: '单位',
      key: 'unit_name',
      width: 80,
      render: (_: unknown, record: ProductItem) =>
        record.unit_name ??
        (record.unit_id != null ? unitNameById.get(String(record.unit_id)) : undefined) ??
        '-',
    },
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
      dataIndex: 'updated_at',
      width: 160,
      render: (v?: string) => <DateCell value={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 150,
      render: (_: unknown, record: ProductItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Dropdown menu={buildRowMenu(record)} trigger={['click']}>
            <Button type="link" size="small" aria-label="更多操作">
              更多<MoreOutlined style={{ marginLeft: 2 }} />
            </Button>
          </Dropdown>
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
            { name: 'category_id', label: '分类', control: 'select', options: categoryFilterOptions },
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
          emptyAction={
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate}>
              新建商品
            </Button>
          }
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          removingRowKeys={fb.removingRowKeys}
          scrollX={1690}
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
        <Form<ProductFormValues> form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="code" label="商品编码" rules={[{ required: true, message: '请输入商品编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={64} disabled={editing !== null} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="商品名称" rules={[{ required: true, message: '请输入商品名称' }]}>
                <Input placeholder="请输入商品名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="short_name" label="简称">
                <Input placeholder="请输入简称" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="category_id" label="商品分类">
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
              <Form.Item name="unit_id" label="计量单位">
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
          </Row>
        </Form>
      </Modal>

      {/* 「更多」菜单 停用/启用/删除 的二次确认（文案与原行内 SfConfirm 逐字一致；
          危险动作 danger ok + confirmLoading 防重复） */}
      <Modal
        title={
          rowConfirm?.kind === 'remove'
            ? '确认删除该商品？'
            : rowConfirm?.record.status === 'ENABLED'
              ? '确认停用该商品？'
              : '确认启用该商品？'
        }
        open={rowConfirm !== null}
        width={440}
        confirmLoading={rowConfirm?.kind === 'remove' ? removeMutation.isPending : statusMutation.isPending}
        okText={
          rowConfirm?.kind === 'remove' ? '删除' : rowConfirm?.record.status === 'ENABLED' ? '停用' : '启用'
        }
        okButtonProps={{
          danger: rowConfirm?.kind === 'remove' || rowConfirm?.record.status === 'ENABLED',
        }}
        onOk={handleRowConfirmOk}
        onCancel={() => setRowConfirm(null)}
      >
        <Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {rowConfirm?.kind === 'remove'
            ? '已产生业务数据的商品后端将拒绝删除，建议改用停用。'
            : rowConfirm?.record.status === 'ENABLED'
              ? '停用将级联停用其启用中的 SKU；启用不自动反启 SKU。'
              : '启用后商品可重新挂 SKU 与参与业务。'}
        </Paragraph>
      </Modal>
    </div>
  )
}
