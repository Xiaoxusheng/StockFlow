import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Col,
  Dropdown,
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
import { MinusCircleOutlined, MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import type { MenuProps } from 'antd'
import {
  masterdataApi,
  type SkuItem,
  type SkuQuery,
  type SkuSavePayload,
} from '@/api/masterdata'
import { fetchProductOptions } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfQrPreviewDrawer, type SfQrSkuInfo } from '@/components/print/SfQrPreviewDrawer'
import { SfQrPrintModal } from '@/components/print/SfQrPrintModal'
import { buildSfqrSku } from '@/utils/qrPayload'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text, Paragraph } = Typography

const ENABLED_FILTER_OPTIONS = [
  { label: '已启用', value: 'true' },
  { label: '已停用', value: 'false' },
]

/** 条码表单行（对应 BarcodeInput，service_sku.go:139-143；code_type 缺省 CODE128） */
interface SkuBarcodeFormRow {
  barcode?: string
  code_type?: string
  is_primary?: boolean
}

/** 表单值：InputNumber 可清空为 null，提交前统一转 undefined（对应"未填写"）；
 * 字段名与后端 JSON tag（snake_case）一致 */
interface SkuFormValues {
  code: string
  product_id?: string
  barcodes?: SkuBarcodeFormRow[]
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
    barcodes: (record.barcodes ?? []).map((b) => ({
      barcode: b.barcode,
      code_type: b.code_type,
      is_primary: b.is_primary,
    })),
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
 * product_id 为 int64 必须 number；barcodes 提供即全量替换（service_sku.go:428-437「nil 不修改；
 * 提供即全量替换」、空数组=清空）——编辑未改动条码时回传原值等价幂等 */
function toPayload(values: SkuFormValues): SkuSavePayload {
  return {
    code: values.code.trim(),
    product_id: Number(values.product_id),
    barcodes: (values.barcodes ?? []).map((row) => ({
      barcode: (row.barcode ?? '').trim(),
      // code_type 后端自动大写化并缺省 CODE128（service_sku.go buildBarcodes）；空串交后端缺省
      code_type: row.code_type?.trim().toUpperCase() || undefined,
      is_primary: row.is_primary ?? false,
    })),
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
  // 动效 #7/删除行（frontend.md §31）：行淡色反馈 / 删除行 fade→收缩——仅在 API 成败回调后触发
  const fb = useTableRowFeedback()

  const list = usePagedList<SkuItem, SkuQuery>({
    queryKey: ['masterdata', 'skus'],
    fetch: (q) => masterdataApi.skus.list(q),
    params,
  })

  // 表单依赖下拉：所属商品分页取全（fetchProductOptions，超一页不截断）；接口失败时降级为空数组，不阻塞其余字段填写
  const products = useQuery({
    queryKey: ['masterdata', 'products', 'options'],
    queryFn: fetchProductOptions,
  })
  const productOptions = (products.data ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))
  // 后端列表不装配 product_name（omitempty，service_sku.go:43-44 仅详情返回），
  // 列表展示用一次取全的商品数据源按 product_id 兜底映射（同一 API 的真实数据）
  const productNameById = useMemo(
    () => new Map((products.data ?? []).map((item) => [String(item.id), item.name])),
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
    onSuccess: (data, { id }) => {
      messageApi.success(data.is_enabled ? '已启用' : '已停用')
      invalidate()
      fb.trigger(id, 'success')
    },
    onError: (error, { id }) => {
      messageApi.error(resolveErrorMessage(error))
      fb.trigger(id, 'error')
    },
  })

  const removeMutation = useMutation({
    mutationFn: (id: SkuItem['id']) => masterdataApi.skus.remove(id),
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

  // ---- 二维码快捷入口（qr-code.md §7.2「SKU 列表快捷入口」；载荷构造唯一点 = utils/qrPayload.ts） ----
  const user = useAuthStore((s) => s.user)
  // 「打印二维码」fail-closed（约束 7）：无 printing:task:create 权限时菜单项不渲染
  const canCreatePrintTask = canAccess(user, 'printing:task:create')
  const [qrOpen, setQrOpen] = useState(false)
  const [qrSku, setQrSku] = useState<SfQrSkuInfo | null>(null)
  const [printOpen, setPrintOpen] = useState(false)
  const [printTarget, setPrintTarget] = useState<SfQrSkuInfo | null>(null)
  // 「更多」菜单内 停用/启用/删除 的二次确认：SfConfirm 为 Popconfirm 形态，无法锚定在点击后
  // 即关闭的 Dropdown 菜单项内——改用同语义声明式 Modal（danger ok + confirmLoading）
  const [rowConfirm, setRowConfirm] = useState<{ kind: 'toggle' | 'remove'; record: SkuItem } | null>(null)

  /** SKU 行 → 二维码抽屉/打印弹窗共用对象（商品名经 options map 兜底；主条码 is_primary 优先） */
  const toQrSku = (record: SkuItem): SfQrSkuInfo => ({
    id: record.id,
    code: record.code,
    productName: record.product_name ?? productNameById.get(String(record.product_id)),
    primaryBarcode: record.barcodes.find((b) => b.is_primary)?.barcode ?? record.barcodes[0]?.barcode,
    enabled: record.is_enabled,
  })

  /** 复制二维码内容 = SFQR 载荷明文（交互场景用 buildSfqrSku 显式构造，非法输入抛错可见） */
  const copyQrPayload = (record: SkuItem) => {
    let payload: string
    try {
      payload = buildSfqrSku(record.code)
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : '二维码内容构造失败')
      return
    }
    if (!navigator.clipboard) {
      messageApi.error('当前浏览器剪贴板不可用，请在二维码详情抽屉中手动复制')
      return
    }
    navigator.clipboard
      .writeText(payload)
      .then(() => messageApi.success('二维码内容已复制'))
      .catch(() => messageApi.error('复制失败，请到二维码详情抽屉手动复制'))
  }

  /** 行内「更多」菜单（UserListPage buildRowMenu 同构；打印项经权限 fail-closed 收敛） */
  const buildRowMenu = (record: SkuItem): MenuProps => {
    const disabling = record.is_enabled
    return {
      items: [
        ...(canCreatePrintTask ? [{ key: 'print', label: '打印二维码' }] : []),
        { key: 'copy', label: '复制二维码内容' },
        { type: 'divider' },
        { key: 'toggle', label: disabling ? '停用' : '启用', danger: disabling },
        { key: 'remove', label: '删除', danger: true },
      ],
      onClick: ({ key }) => {
        if (key === 'print') {
          setPrintTarget(toQrSku(record))
          setPrintOpen(true)
        }
        if (key === 'copy') copyQrPayload(record)
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
        { id: record.id, enabled: !record.is_enabled },
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
      width: 170,
      render: (_: unknown, record: SkuItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          {/* 二维码 = 详情快捷入口（qr-code.md §7.2：打开 SfQrPreviewDrawer，
              内含 QR 预览 + SKU 编码/商品名/主条码/状态 + 复制载荷 + 打印标签） */}
          <Button
            type="link"
            size="small"
            onClick={() => {
              setQrSku(toQrSku(record))
              setQrOpen(true)
            }}
          >
            二维码
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
          emptyAction={
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate}>
              新建 SKU
            </Button>
          }
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          removingRowKeys={fb.removingRowKeys}
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
            <Col span={24}>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                条码（主条码至多一个；编辑保存即按当前列表全量替换）
              </Text>
              <Form.List name="barcodes">
                {(fields, { add, remove }) => (
                  <>
                    {fields.map((field) => (
                      <Row key={field.key} gutter={8}>
                        <Col span={11}>
                          <Form.Item
                            name={[field.name, 'barcode']}
                            rules={[
                              { required: true, whitespace: true, message: '请输入条码' },
                              { max: 128, message: '条码不超过 128 字符' },
                            ]}
                          >
                            <Input placeholder="条码值（≤128 字符）" maxLength={128} />
                          </Form.Item>
                        </Col>
                        <Col span={7}>
                          <Form.Item
                            name={[field.name, 'code_type']}
                            rules={[{ max: 32, message: '类型不超过 32 字符' }]}
                          >
                            <Input placeholder="类型，缺省 CODE128" maxLength={32} />
                          </Form.Item>
                        </Col>
                        <Col span={4}>
                          <Form.Item name={[field.name, 'is_primary']} valuePropName="checked">
                            <Checkbox>主条码</Checkbox>
                          </Form.Item>
                        </Col>
                        <Col span={2}>
                          <Button
                            type="text"
                            danger
                            icon={<MinusCircleOutlined />}
                            onClick={() => remove(field.name)}
                          />
                        </Col>
                      </Row>
                    ))}
                    <Form.Item style={{ marginBottom: 0 }}>
                      {/* 上限对齐后端 maxBarcodesPerSKU=50（service_sku.go:26），超出由后端拒绝 */}
                      <Button
                        type="dashed"
                        icon={<PlusOutlined />}
                        block
                        disabled={fields.length >= 50}
                        onClick={() => add({ is_primary: fields.length === 0 })}
                      >
                        添加条码
                      </Button>
                    </Form.Item>
                  </>
                )}
              </Form.List>
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

      {/* 「更多」菜单 停用/启用/删除 的二次确认（文案与原行内 SfConfirm 逐字一致；
          危险动作 danger ok + confirmLoading 防重复，约束 6/8） */}
      <Modal
        title={
          rowConfirm?.kind === 'remove'
            ? '确认删除该 SKU？'
            : rowConfirm?.record.is_enabled
              ? '确认停用该 SKU？'
              : '确认启用该 SKU？'
        }
        open={rowConfirm !== null}
        width={440}
        confirmLoading={rowConfirm?.kind === 'remove' ? removeMutation.isPending : statusMutation.isPending}
        okText={
          rowConfirm?.kind === 'remove' ? '删除' : rowConfirm?.record.is_enabled ? '停用' : '启用'
        }
        okButtonProps={{
          danger: rowConfirm?.kind === 'remove' || rowConfirm?.record.is_enabled === true,
        }}
        onOk={handleRowConfirmOk}
        onCancel={() => setRowConfirm(null)}
      >
        <Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {rowConfirm?.kind === 'remove'
            ? '删除为软删除，后端当前不校验库存/单据引用（引用校验随后续版本交付）：已产生业务数据的 SKU 删除后将从列表与下拉消失，历史单据中将按 ID 显示，建议改用停用。'
            : rowConfirm?.record.is_enabled
              ? '停用后 SKU 不可被出库分配；启用后恢复正常参与业务。'
              : '启用后 SKU 恢复参与入库/出库业务。'}
        </Paragraph>
      </Modal>

      {/* 二维码详情抽屉（frontend.md §13.1 冻结链路；SKU 编码/商品名/主条码/状态 + 复制载荷 + 打印标签）。
          open 与数据分离：关闭动画期间保留内容，避免抽屉骤空 */}
      <SfQrPreviewDrawer open={qrOpen} sku={qrSku} onClose={() => setQrOpen(false)} />

      {/* 「更多 → 打印二维码」直达的单打配置弹窗（权限已在菜单项 fail-closed 收敛；
          data_ids 通道纪律见 SfQrPrintModal——恒传 String(sku.id)） */}
      <SfQrPrintModal
        open={printOpen}
        skus={printTarget ? [printTarget] : []}
        onClose={() => setPrintOpen(false)}
      />
    </div>
  )
}
