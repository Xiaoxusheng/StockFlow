import { useEffect, useMemo } from 'react'
import { Button, Col, Flex, Form, Input, InputNumber, Row, Select, Typography, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import {
  fetchSkuOptions,
  fetchSupplierOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import {
  PURCHASE_CREATE_PERMISSION,
  PURCHASE_UPDATE_PERMISSION,
  purchaseApi,
  type PurchaseCreatePayload,
  type PurchaseLineInput,
} from '@/api/purchase'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 表单值：明细行字段可空（Form.List 运行中允许未填完），提交前经校验规则与
 * flatMap 守卫收敛为 PurchaseLineInput，不使用非空断言。
 * 供应商/仓库/SKU 下拉 value 一律为 ID 数字（提交契约 POCreateInput 为 int64，
 * masterdata.ts：提交必须用 number，传字符串会 400）。
 */
interface PurchaseLineFormValues {
  sku_id?: number
  qty?: number
  price?: number
  remark?: string
}

interface PurchaseOrderFormValues {
  supplier_id?: number
  warehouse_id?: number
  lines: PurchaseLineFormValues[]
  remark?: string
}

/** 下拉选项 value：ID 出参为字符串，提交契约要求数字，此处统一转 number */
function toIdNumber(id: number | string): number {
  return Number(id)
}

/**
 * 新建 / 草稿编辑采购订单（/purchases/new、/purchases/:id/edit——路由参数 :id 存在即编辑）。
 * 创建 POST /api/purchases（POCreateInput，internal/purchase/service_purchase.go:30-35）；
 * 编辑 PUT /api/purchases/{id}（POUpdateInput，仅草稿可改 service_purchase.go:171-174，
 * 字段可选=不修改、明细提供即整单替换，故编辑提交始终携带全部明细）。
 * 分区遵循 frontend.md §8：基础信息（供应商/仓库）→ 商品明细（SKU/数量/单价/金额）→ 备注。
 * 行金额与合计仅作即时校验展示，以后端计算为准（total_amount 后端按明细汇总）。
 */
export default function PurchaseOrderFormPage() {
  const { id } = useParams()
  const isEdit = Boolean(id)
  const navigate = useNavigate()
  const [form] = Form.useForm<PurchaseOrderFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canSubmitForm = isEdit
    ? canAccess(user, PURCHASE_UPDATE_PERMISSION)
    : canAccess(user, PURCHASE_CREATE_PERMISSION)

  // 表单依赖下拉：供应商 / 仓库 / SKU 一次取全（options 端点已冻结）；失败呈错误态可重试
  const suppliers = useQuery({
    queryKey: ['purchase', 'options', 'suppliers'],
    queryFn: fetchSupplierOptions,
  })
  const warehouses = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skus = useQuery({
    queryKey: ['purchase', 'options', 'skus'],
    queryFn: fetchSkuOptions,
  })

  const supplierOptions = (suppliers.data ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: toIdNumber(item.id),
  }))
  const warehouseOptions = (warehouses.data ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: toIdNumber(item.id),
  }))
  const skuOptions = (skus.data ?? []).map((item) => ({
    label: item.product_name ? `${item.code} ${item.product_name}` : item.code,
    value: toIdNumber(item.id),
  }))

  // 编辑模式：拉取详情预填（仅草稿可编辑，后端 UpdatePO 状态校验 service_purchase.go:171-174）
  const detailQuery = useQuery({
    queryKey: ['purchase', 'detail', id],
    queryFn: () => purchaseApi.detail(id as string),
    enabled: isEdit,
  })
  const order = detailQuery.data?.order
  useEffect(() => {
    if (!order) return
    form.setFieldsValue({
      supplier_id: Number(order.supplier_id),
      warehouse_id: Number(order.warehouse_id),
      remark: order.remark || undefined,
      lines: (detailQuery.data?.items ?? []).map((item) => ({
        sku_id: Number(item.sku_id),
        qty: item.qty_ordered,
        price: item.price,
        remark: item.remark || undefined,
      })),
    })
  }, [form, order, detailQuery.data])

  const submitMutation = useMutation({
    mutationFn: (payload: PurchaseCreatePayload) => {
      if (isEdit) {
        // POUpdateInput：supplier/warehouse/remark 可选=不修改，明细提供即整单替换；
        // 编辑表单全量提交（后端 Remark 为非指针无条件覆盖，缺省会清空原备注）
        return purchaseApi.update(id as string, payload)
      }
      return purchaseApi.create(payload)
    },
    onSuccess: (saved) => {
      messageApi.success(isEdit ? '采购订单已保存' : '采购订单已创建')
      navigate(`/purchases/${String(saved.id)}`)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 合计预览：行金额 = 数量 × 单价，仅作即时校验展示（后端以自身计算为准）
  const watchedLines = Form.useWatch('lines', form)
  const totals = useMemo(() => {
    let totalQty = 0
    let totalAmount = 0
    for (const line of watchedLines ?? []) {
      const { qty, price } = line ?? {}
      if (typeof qty === 'number') totalQty += qty
      if (typeof qty === 'number' && typeof price === 'number') totalAmount += qty * price
    }
    return { totalQty, totalAmount, rowCount: watchedLines?.length ?? 0 }
  }, [watchedLines])

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        if (values.supplier_id == null || values.warehouse_id == null) return
        const items: PurchaseLineInput[] = (values.lines ?? []).flatMap((line) => {
          if (line?.sku_id == null || line.qty == null) return []
          return [
            {
              sku_id: line.sku_id,
              qty: line.qty,
              price: line.price,
              remark: line.remark?.trim() || undefined,
            },
          ]
        })
        submitMutation.mutate({
          supplier_id: values.supplier_id,
          warehouse_id: values.warehouse_id,
          remark: values.remark?.trim() || undefined,
          items,
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  if (!canSubmitForm) {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader
          title={isEdit ? '编辑采购订单' : '新建采购订单'}
          onBack={() => navigate(isEdit ? `/purchases/${id}` : '/purchases')}
        />
        <SfError
          error={new Error(isEdit ? '没有编辑采购订单的权限' : '没有新建采购订单的权限')}
          description={`需要采购域「${isEdit ? '修改' : '新建'}」权限点（${
            isEdit ? PURCHASE_UPDATE_PERMISSION : PURCHASE_CREATE_PERMISSION
          }），请联系管理员开通；后端仍会做最终校验`}
        />
      </div>
    )
  }

  if (isEdit && detailQuery.status === 'pending') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑采购订单" onBack={() => navigate('/purchases')} />
        <SfLoading rows={8} />
      </div>
    )
  }
  if (isEdit && detailQuery.status === 'error') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑采购订单" onBack={() => navigate('/purchases')} />
        <SfError
          error={detailQuery.error}
          onRetry={detailQuery.refetch}
          description="采购订单详情接口不可用：GET /api/purchases/{id}"
        />
      </div>
    )
  }
  if (isEdit && order && order.status !== 'DRAFT') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑采购订单" onBack={() => navigate(`/purchases/${id}`)} />
        <SfError
          error={new Error('仅草稿状态的采购订单可以修改')}
          description="当前单据已进入审核/收货流程，不可再编辑（后端仅允许 DRAFT 修改，service_purchase.go:171-174）"
        />
      </div>
    )
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title={isEdit ? '编辑采购订单' : '新建采购订单'}
        subtitle={order?.po_no ?? '草稿 → 待审核 → 已审核 → 到货 → 完成（business-flow.md §2.2）'}
        onBack={() => navigate(isEdit ? `/purchases/${id}` : '/purchases')}
        extra={
          <>
            <Button onClick={() => navigate(isEdit ? `/purchases/${id}` : '/purchases')}>
              取消
            </Button>
            <Button type="primary" loading={submitMutation.isPending} onClick={handleSubmit}>
              {isEdit ? '保存修改' : '提交创建'}
            </Button>
          </>
        }
      />

      <Form<PurchaseOrderFormValues>
        form={form}
        layout="vertical"
        initialValues={{ lines: [{}] }}
        disabled={submitMutation.isPending}
      >
        <SfDetailSection title="基础信息">
          <Row gutter={16}>
            <Col span={8}>
              <Form.Item
                name="supplier_id"
                label="供应商"
                rules={[{ required: true, message: '请选择供应商' }]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={supplierOptions}
                  placeholder="请选择供应商"
                  loading={suppliers.isLoading}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="warehouse_id"
                label="收货仓库"
                rules={[{ required: true, message: '请选择收货仓库' }]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={warehouseOptions}
                  placeholder="请选择收货仓库"
                  loading={warehouses.isLoading}
                />
              </Form.Item>
            </Col>
          </Row>
        </SfDetailSection>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="商品明细">
            <Flex vertical gap={8}>
              <Flex gap={12} wrap="nowrap">
                <Text type="secondary" style={{ width: 280 }}>SKU</Text>
                <Text type="secondary" style={{ width: 130 }}>数量</Text>
                <Text type="secondary" style={{ width: 150 }}>单价</Text>
                <Text type="secondary" style={{ width: 130 }}>金额</Text>
                <Text type="secondary" style={{ flex: 1 }}>行备注</Text>
                <Text type="secondary" style={{ width: 48 }}>操作</Text>
              </Flex>
              <Form.List
                name="lines"
                rules={[
                  {
                    validator: async (_, lines: PurchaseLineFormValues[] | undefined) => {
                      if (!lines || lines.length === 0) {
                        return Promise.reject(new Error('请至少添加一行商品明细'))
                      }
                    },
                  },
                ]}
              >
                {(fields, { add, remove }, { errors }) => (
                  <Flex vertical gap={4}>
                    {fields.map((field) => {
                      const line = watchedLines?.[field.name]
                      const lineAmount =
                        typeof line?.qty === 'number' && typeof line?.price === 'number'
                          ? line.qty * line.price
                          : null
                      return (
                        <Flex key={field.key} gap={12} align="baseline" wrap="nowrap">
                          <Form.Item
                            name={[field.name, 'sku_id']}
                            rules={[{ required: true, message: '请选择 SKU' }]}
                            style={{ width: 280, marginBottom: 0 }}
                          >
                            <Select
                              showSearch
                              optionFilterProp="label"
                              options={skuOptions}
                              placeholder="选择 SKU"
                              loading={skus.isLoading}
                            />
                          </Form.Item>
                          <Form.Item
                            name={[field.name, 'qty']}
                            rules={[
                              { required: true, message: '请输入数量' },
                              { type: 'number', min: 1, message: '数量必须大于 0' },
                            ]}
                            style={{ width: 130, marginBottom: 0 }}
                          >
                            <InputNumber min={1} precision={0} style={{ width: '100%' }} placeholder="数量" />
                          </Form.Item>
                          <Form.Item
                            name={[field.name, 'price']}
                            rules={[{ type: 'number', min: 0, message: '单价不能为负数' }]}
                            style={{ width: 150, marginBottom: 0 }}
                          >
                            <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="单价（选填）" />
                          </Form.Item>
                          <Text className="sf-num" style={{ width: 130 }}>
                            {lineAmount == null ? '-' : formatMoney(lineAmount)}
                          </Text>
                          <div style={{ flex: 1 }}>
                            <Form.Item name={[field.name, 'remark']} style={{ marginBottom: 0 }}>
                              <Input placeholder="行备注（选填）" maxLength={255} />
                            </Form.Item>
                          </div>
                          <Button
                            type="text"
                            danger
                            icon={<DeleteOutlined />}
                            disabled={fields.length <= 1}
                            onClick={() => remove(field.name)}
                          />
                        </Flex>
                      )
                    })}
                    <Form.ErrorList errors={errors} />
                    <Button
                      type="dashed"
                      block
                      icon={<PlusOutlined />}
                      onClick={() => add({})}
                    >
                      新增明细行
                    </Button>
                  </Flex>
                )}
              </Form.List>
              <SfSummaryBar
                items={[
                  { label: '明细行数', value: formatNumber(totals.rowCount) },
                  { label: '合计数量', value: formatNumber(totals.totalQty) },
                  { label: '合计金额', value: formatMoney(totals.totalAmount) },
                ]}
              />
              <Text type="secondary">
                数量与单价为整数口径（后端 stock.Qty）；行金额与合计为录入即时校验预览，
                正式金额以后端计算为准（total_amount 由后端按明细汇总）。
              </Text>
            </Flex>
          </SfDetailSection>
        </div>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="备注">
            <Form.Item name="remark" style={{ marginBottom: 0 }}>
              <Input.TextArea rows={3} placeholder="补充采购说明（选填）" maxLength={500} showCount />
            </Form.Item>
          </SfDetailSection>
        </div>
      </Form>
    </div>
  )
}
