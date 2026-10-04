import { useMemo } from 'react'
import { Flex, Button, Form, Input, InputNumber, Row, Col, Select, Typography, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  fetchCustomerOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  buildIdItemMap,
  idKey,
} from '@/api/options'
import type { SkuItem } from '@/api/masterdata'
import {
  SALES_ORDER_CREATE_PERMISSION,
  salesApi,
  type SalesOrderCreatePayload,
  type SalesOrderLineInput,
} from '@/api/sales'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'
import { formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 表单值：明细行字段可空（Form.List 运行中允许未填完），提交前经校验规则与
 * flatMap 守卫收敛为 SalesOrderLineInput，不使用非空断言。
 * 客户/仓库/SKU 下拉 value 一律为 ID 数字（提交契约 CreateOrderInput 为 int64，
 * masterdata.ts：提交必须用 number，传字符串会 400）。
 */
interface SalesLineFormValues {
  sku_id?: number
  qty?: number
  price?: number
  remark?: string
}

interface SalesOrderFormValues {
  customer_id?: number
  warehouse_id?: number
  shipping_address?: string
  delivery_method?: string
  lines: SalesLineFormValues[]
  remark?: string
}

/** 配送方式值域（business-flow.md §6.1 仅定义字段为自由文本；枚举为前端提案，仅作录入约束） */
const DELIVERY_METHOD_OPTIONS = [
  { label: '快递', value: 'EXPRESS' },
  { label: '物流专线', value: 'LOGISTICS' },
  { label: '客户自提', value: 'SELF_PICK' },
  { label: '其他', value: 'OTHER' },
]

/** 下拉选项 value：ID 出参为字符串，提交契约要求数字，此处统一转 number */
function toIdNumber(id: number | string): number {
  return Number(id)
}

/**
 * 新建销售订单（/sales/new，POST /api/sales）。
 * 提交载荷 CreateOrderInput（internal/sales/service.go:114-121）：
 * {customer_id, warehouse_id, shipping_address, delivery_method, remark, items:[{sku_id, qty, price}]}。
 * 分区遵循 frontend.md §8：基础信息（客户/仓库/收货地址/配送方式）→ 商品明细（SKU/数量/单价/金额）→ 备注。
 * 行金额与合计仅作即时校验展示，以后端计算为准。
 */
export default function SalesOrderCreatePage() {
  const navigate = useNavigate()
  const [form] = Form.useForm<SalesOrderFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_ORDER_CREATE_PERMISSION)

  // 表单依赖下拉：客户 / 仓库 / SKU 一次取全（options 端点已冻结）；失败呈错误态可重试
  const customers = useQuery({
    queryKey: ['options', 'customers'],
    queryFn: fetchCustomerOptions,
  })
  const warehouses = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skus = useQuery({
    queryKey: ['options', 'skus'],
    queryFn: fetchSkuOptions,
  })

  const customerOptions = (customers.data ?? []).map((item) => ({
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
  const skuMap = useMemo(
    () => buildIdItemMap<SkuItem>(skus.data ?? [], (item) => item.id),
    [skus.data],
  )

  const submitMutation = useMutation({
    mutationFn: (payload: SalesOrderCreatePayload) => salesApi.orders.create(payload),
    onSuccess: () => {
      messageApi.success('销售订单已创建，可返回销售订单列表查看')
      navigate('/sales')
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

  const handleSkuChange = (index: number, skuId: number) => {
    const sku = skuMap.get(idKey(skuId))
    if (sku?.sale_price != null) {
      form.setFieldValue(['lines', index, 'price'], sku.sale_price)
    }
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        if (values.customer_id == null || values.warehouse_id == null) return
        const items: SalesOrderLineInput[] = (values.lines ?? []).flatMap((line, index) => {
          if (line?.sku_id == null || line.qty == null || line.price == null) return []
          return [
            {
              line_no: index + 1,
              sku_id: line.sku_id,
              qty: line.qty,
              price: line.price,
              remark: line.remark?.trim() || undefined,
            },
          ]
        })
        submitMutation.mutate({
          customer_id: values.customer_id,
          warehouse_id: values.warehouse_id,
          shipping_address: values.shipping_address?.trim() || undefined,
          delivery_method: values.delivery_method || undefined,
          remark: values.remark?.trim() || undefined,
          items,
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  if (!canCreate) {
    return (
      <div className="sf-page">
        <SfPageHeader title="新建销售订单" onBack={() => navigate('/sales')} />
        <SfError
          error={new Error('没有新建销售订单的权限')}
          description="需要销售域「新建」权限点（sales:sales:create），请联系管理员开通；后端仍会做最终校验"
        />
      </div>
    )
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="新建销售订单"
        subtitle="订单 → 审核 → 库存预占 → 出库（business-flow.md §6）"
        onBack={() => navigate('/sales')}
        extra={
          <>
            <Button onClick={() => navigate('/sales')}>取消</Button>
            <Button
              type="primary"
              loading={submitMutation.isPending}
              onClick={handleSubmit}
            >
              提交创建
            </Button>
          </>
        }
      />

      <Form<SalesOrderFormValues>
        form={form}
        layout="vertical"
        initialValues={{ lines: [{}] }}
        disabled={submitMutation.isPending}
      >
        <SfDetailSection title="基础信息">
          <Row gutter={16}>
            <Col span={8}>
              <Form.Item
                name="customer_id"
                label="客户"
                rules={[{ required: true, message: '请选择客户' }]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={customerOptions}
                  placeholder="请选择客户"
                  loading={customers.isLoading}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="warehouse_id"
                label="仓库"
                rules={[{ required: true, message: '请选择仓库' }]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={warehouseOptions}
                  placeholder="请选择出货仓库"
                  loading={warehouses.isLoading}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="delivery_method" label="配送方式">
                <Select options={DELIVERY_METHOD_OPTIONS} placeholder="请选择配送方式" allowClear />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="shipping_address" label="收货地址">
                <Input placeholder="请输入收货地址" maxLength={255} />
              </Form.Item>
            </Col>
          </Row>
        </SfDetailSection>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="商品明细">
            <Flex vertical gap={8}>
              <Flex gap={12} wrap="nowrap">
                <Text type="secondary" style={{ width: 280 }}>SKU</Text>
                <Text type="secondary" style={{ width: 120 }}>数量</Text>
                <Text type="secondary" style={{ width: 150 }}>单价</Text>
                <Text type="secondary" style={{ width: 130 }}>金额</Text>
                <Text type="secondary" style={{ flex: 1 }}>行备注</Text>
                <Text type="secondary" style={{ width: 48 }}>操作</Text>
              </Flex>
              <Form.List
                name="lines"
                rules={[
                  {
                    validator: async (_, lines: SalesLineFormValues[] | undefined) => {
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
                              onChange={(value: number) => handleSkuChange(field.name, value)}
                            />
                          </Form.Item>
                          <Form.Item
                            name={[field.name, 'qty']}
                            rules={[
                              { required: true, message: '请输入数量' },
                              { type: 'number', min: 1, message: '数量必须大于 0' },
                            ]}
                            style={{ width: 120, marginBottom: 0 }}
                          >
                            <InputNumber min={1} precision={0} style={{ width: '100%' }} placeholder="数量" />
                          </Form.Item>
                          <Form.Item
                            name={[field.name, 'price']}
                            rules={[
                              { required: true, message: '请输入单价' },
                              { type: 'number', min: 0, message: '单价不能为负数' },
                            ]}
                            style={{ width: 150, marginBottom: 0 }}
                          >
                            <InputNumber min={0} precision={2} style={{ width: '100%' }} placeholder="单价" />
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
                行金额与合计为录入即时校验预览，正式金额以后端计算为准（total_amount 由后端按明细汇总）。
              </Text>
            </Flex>
          </SfDetailSection>
        </div>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="备注">
            <Form.Item name="remark" style={{ marginBottom: 0 }}>
              <Input.TextArea rows={3} placeholder="补充订单说明（选填）" maxLength={500} showCount />
            </Form.Item>
          </SfDetailSection>
        </div>
      </Form>
    </div>
  )
}
