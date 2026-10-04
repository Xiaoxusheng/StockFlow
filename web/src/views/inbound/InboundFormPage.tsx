import { useEffect, useMemo } from 'react'
import { Button, Col, Descriptions, Flex, Form, Input, InputNumber, Row, Select, Typography, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import {
  buildIdItemMap,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import type { SkuItem } from '@/api/masterdata'
import {
  INBOUND_CREATE_PERMISSION,
  INBOUND_UPDATE_PERMISSION,
  inboundApi,
  type InboundCreatePayload,
  type InboundLineInput,
  type InboundSourceType,
} from '@/api/inbound'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/** 入库来源类型（InboundSourceType＝迁移 CHECK 值域 models.go:49-50；与列表页同源） */
const SOURCE_TYPE_OPTIONS: Array<{ label: string; value: InboundSourceType }> = [
  { label: '采购入库', value: 'PURCHASE' },
  { label: '其他入库', value: 'OTHER' },
]

/**
 * 表单值：明细行字段可空（Form.List 运行中允许未填完），提交前经校验规则与
 * flatMap 守卫收敛为 InboundLineInput，不使用非空断言。
 * 仓库/SKU 下拉 value 一律为 ID 数字（提交契约 InboundCreateInput 为 int64）。
 */
interface InboundLineFormValues {
  sku_id?: number
  qty?: number
  remark?: string
}

interface InboundFormValues {
  source_type?: InboundSourceType
  source_no?: string
  warehouse_id?: number
  lines: InboundLineFormValues[]
  remark?: string
}

/** 下拉选项 value：ID 出参为字符串，提交契约要求数字，此处统一转 number */
function toIdNumber(id: number | string): number {
  return Number(id)
}

/**
 * 新建 / 草稿编辑入库单（/inbound/new、/inbound/:id/edit——路由参数 :id 存在即编辑）。
 * 创建 POST /api/inbounds（InboundCreateInput，internal/purchase/service_inbound.go:32-37：
 * source_type 必填、source_no 可选来源单号、items 必填；同 SKU 后端合并单行）；
 * 编辑 PUT /api/inbounds/{id}（InboundUpdateInput，service_inbound.go:39-42：仅草稿，
 * 仅 remark + 明细整单替换——来源类型/来源单号/仓库创建后不可改，编辑态只读展示；
 * 后端 Remark 为非指针无条件覆盖，故编辑始终携带原 remark）。
 */
export default function InboundFormPage() {
  const { id } = useParams()
  const isEdit = Boolean(id)
  const navigate = useNavigate()
  const [form] = Form.useForm<InboundFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canSubmitForm = isEdit
    ? canAccess(user, INBOUND_UPDATE_PERMISSION)
    : canAccess(user, INBOUND_CREATE_PERMISSION)

  const warehouses = useQuery({
    queryKey: ['inbound', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skus = useQuery({
    queryKey: ['inbound', 'options', 'skus'],
    queryFn: fetchSkuOptions,
  })
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
  const watchedSourceType = Form.useWatch('source_type', form)

  // 编辑模式：拉取详情预填（仅草稿可编辑，后端 UpdateInbound 状态校验 service_inbound.go:211-214）
  const detailQuery = useQuery({
    queryKey: ['inbound', 'detail', id],
    queryFn: () => inboundApi.detail(id as string),
    enabled: isEdit,
  })
  const order = detailQuery.data?.order
  useEffect(() => {
    if (!order) return
    form.setFieldsValue({
      remark: order.remark || undefined,
      lines: (detailQuery.data?.items ?? []).map((item) => ({
        sku_id: Number(item.sku_id),
        qty: item.qty,
        remark: item.remark || undefined,
      })),
    })
  }, [form, order, detailQuery.data])

  const submitMutation = useMutation({
    mutationFn: (payload: InboundCreatePayload) => {
      if (isEdit) {
        // InboundUpdateInput 仅 remark + items 整单替换；source_type/source_no/warehouse
        // 不在编辑契约内（后端忽略），始终提交原 remark 防止被清空
        return inboundApi.update(id as string, { remark: payload.remark, items: payload.items })
      }
      return inboundApi.create(payload)
    },
    onSuccess: (saved) => {
      messageApi.success(isEdit ? '入库单已保存' : '入库单已创建')
      navigate(`/inbound/${String(saved.id)}`)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 计划数量合计预览（仅即时校验展示，业务口径以后端为准）
  const watchedLines = Form.useWatch('lines', form)
  const totalQty = useMemo(
    () => (watchedLines ?? []).reduce((acc, line) => (typeof line?.qty === 'number' ? acc + line.qty : acc), 0),
    [watchedLines],
  )

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        if (!isEdit && (values.source_type == null || values.warehouse_id == null)) return
        const items: InboundLineInput[] = (values.lines ?? []).flatMap((line) => {
          if (line?.sku_id == null || line.qty == null) return []
          return [
            {
              sku_id: line.sku_id,
              qty: line.qty,
              remark: line.remark?.trim() || undefined,
            },
          ]
        })
        submitMutation.mutate({
          source_type: values.source_type ?? 'OTHER',
          source_no: values.source_no?.trim() || undefined,
          warehouse_id: values.warehouse_id ?? 0,
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
          title={isEdit ? '编辑入库单' : '新建入库单'}
          onBack={() => navigate(isEdit ? `/inbound/${id}` : '/inbound')}
        />
        <SfError
          error={new Error(isEdit ? '没有编辑入库单的权限' : '没有新建入库单的权限')}
          description={`需要采购域入库「${isEdit ? '修改' : '新建'}」权限点（${
            isEdit ? INBOUND_UPDATE_PERMISSION : INBOUND_CREATE_PERMISSION
          }），请联系管理员开通；后端仍会做最终校验`}
        />
      </div>
    )
  }

  if (isEdit && detailQuery.status === 'pending') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑入库单" onBack={() => navigate('/inbound')} />
        <SfLoading rows={8} />
      </div>
    )
  }
  if (isEdit && detailQuery.status === 'error') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑入库单" onBack={() => navigate('/inbound')} />
        <SfError
          error={detailQuery.error}
          onRetry={detailQuery.refetch}
          description="入库单详情接口不可用：GET /api/inbounds/{id}"
        />
      </div>
    )
  }
  if (isEdit && order && order.status !== 'DRAFT') {
    return (
      <div className="sf-page">
        {contextHolder}
        <SfPageHeader title="编辑入库单" onBack={() => navigate(`/inbound/${id}`)} />
        <SfError
          error={new Error('仅草稿状态的入库单可以修改')}
          description="当前单据已进入收货流程，不可再编辑（后端仅允许 DRAFT 修改，service_inbound.go:211-214）"
        />
      </div>
    )
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title={isEdit ? '编辑入库单' : '新建入库单'}
        subtitle={order?.inbound_no ?? '草稿 → 收货 → 质检 → 上架 → 完成（business-flow.md §3.3）'}
        onBack={() => navigate(isEdit ? `/inbound/${id}` : '/inbound')}
        extra={
          <>
            <Button onClick={() => navigate(isEdit ? `/inbound/${id}` : '/inbound')}>取消</Button>
            <Button type="primary" loading={submitMutation.isPending} onClick={handleSubmit}>
              {isEdit ? '保存修改' : '提交创建'}
            </Button>
          </>
        }
      />

      <Form<InboundFormValues>
        form={form}
        layout="vertical"
        initialValues={{ lines: [{}] }}
        disabled={submitMutation.isPending}
      >
        <SfDetailSection title="基础信息">
          {isEdit ? (
            // 编辑契约不含来源类型/来源单号/仓库（InboundUpdateInput 仅 remark+items），只读展示
            <Descriptions
              bordered
              size="small"
              column={{ xs: 1, md: 2, xl: 3 }}
              items={[
                { key: 'inboundNo', label: '入库单号', children: order?.inbound_no ?? '-' },
                {
                  key: 'sourceType',
                  label: '入库类型',
                  children:
                    SOURCE_TYPE_OPTIONS.find((opt) => opt.value === order?.source_type)?.label ??
                    order?.source_type ??
                    '-',
                },
                { key: 'sourceNo', label: '来源单号', children: order?.source_no || '-' },
                { key: 'warehouseId', label: '仓库', children: `#${String(order?.warehouse_id ?? '')}` },
              ]}
            />
          ) : (
            <Row gutter={16}>
              <Col span={8}>
                <Form.Item
                  name="source_type"
                  label="入库类型"
                  rules={[{ required: true, message: '请选择入库类型' }]}
                >
                  <Select options={SOURCE_TYPE_OPTIONS} placeholder="请选择入库类型" />
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
                    placeholder="请选择收货仓库"
                    loading={warehouses.isLoading}
                  />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item
                  name="source_no"
                  label="来源单号"
                  rules={
                    watchedSourceType === 'PURCHASE'
                      ? [{ required: true, message: '采购入库必须填写来源采购单号' }]
                      : undefined
                  }
                >
                  <Input
                    placeholder={watchedSourceType === 'PURCHASE' ? '请输入采购单号' : '选填（其他入库可空）'}
                    maxLength={64}
                  />
                </Form.Item>
              </Col>
            </Row>
          )}
        </SfDetailSection>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="商品明细">
            <Flex vertical gap={8}>
              <Flex gap={12} wrap="nowrap">
                <Text type="secondary" style={{ width: 300 }}>SKU</Text>
                <Text type="secondary" style={{ width: 150 }}>计划数量</Text>
                <Text type="secondary" style={{ flex: 1 }}>行备注</Text>
                <Text type="secondary" style={{ width: 48 }}>操作</Text>
              </Flex>
              <Form.List
                name="lines"
                rules={[
                  {
                    validator: async (_, lines: InboundLineFormValues[] | undefined) => {
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
                      const sku = skuMap.get(idKey(watchedLines?.[field.name]?.sku_id ?? ''))
                      return (
                        <Flex key={field.key} gap={12} align="baseline" wrap="nowrap">
                          <Form.Item
                            name={[field.name, 'sku_id']}
                            rules={[{ required: true, message: '请选择 SKU' }]}
                            style={{ width: 300, marginBottom: 0 }}
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
                            style={{ width: 150, marginBottom: 0 }}
                          >
                            <InputNumber min={1} precision={0} style={{ width: '100%' }} placeholder="数量" />
                          </Form.Item>
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
                          {/* 同 SKU 多行由后端合并为单行（buildInboundItems），名称仅作录入辅助 */}
                          <Text type="secondary" style={{ width: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {sku?.product_name ?? ''}
                          </Text>
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
                  { label: '明细行数', value: formatNumber(watchedLines?.length ?? 0) },
                  { label: '计划数量合计', value: formatNumber(totalQty) },
                ]}
              />
              <Text type="secondary">数量为整数口径（后端 stock.Qty）；同 SKU 多行由后端合并为单行。</Text>
            </Flex>
          </SfDetailSection>
        </div>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="备注">
            <Form.Item name="remark" style={{ marginBottom: 0 }}>
              <Input.TextArea rows={3} placeholder="补充入库说明（选填）" maxLength={500} showCount />
            </Form.Item>
          </SfDetailSection>
        </div>
      </Form>
    </div>
  )
}
