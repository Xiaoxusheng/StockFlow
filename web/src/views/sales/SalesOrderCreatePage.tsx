import { useMemo } from 'react'
import { Flex, Button, Form, Input, InputNumber, Row, Col, Select, Typography, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { OPTIONS_PAGE_SIZE, masterdataApi } from '@/api/masterdata'
import {
  SALES_ORDER_CREATE_PERMISSION,
  salesApi,
  type SalesOrderCreatePayload,
  type SalesOrderLineInput,
} from '@/api/sales'
import { warehouseApi } from '@/api/warehouse'
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
 */
interface SalesLineFormValues {
  skuCode?: string
  qty?: number
  unitPrice?: number
  remark?: string
}

interface SalesOrderFormValues {
  customerCode: string
  warehouseCode: string
  shippingAddress?: string
  deliveryType?: string
  lines: SalesLineFormValues[]
  remark?: string
}

/** 配送方式值域（business-flow.md §6.1 仅定义字段；枚举为前端先行提案，后端冻结时回对） */
const DELIVERY_TYPE_OPTIONS = [
  { label: '快递', value: 'EXPRESS' },
  { label: '物流专线', value: 'LOGISTICS' },
  { label: '客户自提', value: 'SELF_PICK' },
  { label: '其他', value: 'OTHER' },
]

/**
 * 新建销售订单（/sales/new，POST /api/sales-orders 前端先行契约）。
 * 分区遵循 frontend.md §8：基础信息（客户/仓库/收货地址/配送方式）→ 商品明细（SKU/数量/单价/金额）→ 备注。
 * 行金额与合计仅作即时校验展示，以后端计算为准。
 */
export default function SalesOrderCreatePage() {
  const navigate = useNavigate()
  const [form] = Form.useForm<SalesOrderFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_ORDER_CREATE_PERMISSION)

  // 表单依赖下拉：客户 / 仓库 / SKU 一次取全；接口失败时降级为空数组，不阻塞其余字段填写
  const customers = useQuery({
    queryKey: ['masterdata', 'customers', 'options'],
    queryFn: () => masterdataApi.customers.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const warehouses = useQuery({
    queryKey: ['warehouse', 'warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const skus = useQuery({
    queryKey: ['masterdata', 'skus', 'options'],
    queryFn: () => masterdataApi.skus.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })

  const customerOptions = (customers.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }))
  const warehouseOptions = (warehouses.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }))
  const skuOptions = (skus.data?.items ?? []).map((item) => ({
    label: item.product_name ? `${item.code} ${item.product_name}` : item.code,
    value: item.code,
  }))
  const skuMap = useMemo(
    () => new Map((skus.data?.items ?? []).map((item) => [item.code, item])),
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
      const { qty, unitPrice } = line ?? {}
      if (typeof qty === 'number') totalQty += qty
      if (typeof qty === 'number' && typeof unitPrice === 'number') totalAmount += qty * unitPrice
    }
    return { totalQty, totalAmount, rowCount: watchedLines?.length ?? 0 }
  }, [watchedLines])

  const handleSkuChange = (index: number, skuCode: string) => {
    const sku = skuMap.get(skuCode)
    if (sku?.sale_price != null) {
      form.setFieldValue(['lines', index, 'unitPrice'], sku.sale_price)
    }
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        const lines: SalesOrderLineInput[] = (values.lines ?? []).flatMap((line) => {
          if (!line?.skuCode || line.qty == null || line.unitPrice == null) return []
          return [
            {
              skuCode: line.skuCode,
              qty: line.qty,
              unitPrice: line.unitPrice,
              remark: line.remark?.trim() || undefined,
            },
          ]
        })
        submitMutation.mutate({
          customerCode: values.customerCode.trim(),
          warehouseCode: values.warehouseCode.trim(),
          shippingAddress: values.shippingAddress?.trim() || undefined,
          deliveryType: values.deliveryType,
          lines,
          remark: values.remark?.trim() || undefined,
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
          description="需要销售域「新建」权限点（前端先行码 sales:create），请联系管理员开通；后端仍会做最终校验"
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
                name="customerCode"
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
                name="warehouseCode"
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
              <Form.Item name="deliveryType" label="配送方式">
                <Select options={DELIVERY_TYPE_OPTIONS} placeholder="请选择配送方式" allowClear />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="shippingAddress" label="收货地址">
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
                        typeof line?.qty === 'number' && typeof line?.unitPrice === 'number'
                          ? line.qty * line.unitPrice
                          : null
                      return (
                        <Flex key={field.key} gap={12} align="baseline" wrap="nowrap">
                          <Form.Item
                            name={[field.name, 'skuCode']}
                            rules={[{ required: true, message: '请选择 SKU' }]}
                            style={{ width: 280, marginBottom: 0 }}
                          >
                            <Select
                              showSearch
                              optionFilterProp="label"
                              options={skuOptions}
                              placeholder="选择 SKU"
                              loading={skus.isLoading}
                              onChange={(value: string) => handleSkuChange(field.name, value)}
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
                            name={[field.name, 'unitPrice']}
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
