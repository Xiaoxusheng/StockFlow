import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, Flex, Form, Input, InputNumber, Modal, Select, Space, message } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { PlusOutlined } from '@ant-design/icons'
import { resolveErrorMessage } from '@/api/client'
import { inboundApi, type InboundOrder, type InboundOrderStatus } from '@/api/inbound'
import {
  INSPECTION_METHOD_LABEL,
  qualityApi,
  type InspectionMethod,
  type QualityCreatePayload,
  type QualityInspectionItem,
} from '@/api/quality'
import { fetchSkuOptions, fetchWarehouseOptions, idKey, SKU_OPTIONS_KEY, WAREHOUSE_OPTIONS_KEY } from '@/api/options'
import { toStatusKey } from '@/api/masterdata'
import { SfOrderSelect, type SfOrderSelectOption } from '@/components/common/SfOrderSelect'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDate } from '@/utils/format'

/**
 * 手动创建质检单（POST /api/quality，internal/purchase/purchase.go:65，权限
 * purchase:quality:create——按钮入口由列表页 canAccess 把关，Modal 内依赖后端强校验）。
 *
 * 入参对齐 QCCreateInput（internal/purchase/service_quality.go:37-45）：
 * - source_no：入库单号——入库单须 AWAITING_QC / AWAITING_PUTAWAY（未收齐不能质检，
 *   service_quality.go:85-89）；单号经 SfOrderSelect 远程联想（初始=两个待质检状态各取
 *   最近一页，输入防抖按 inbound_no/source_no ILIKE 模糊搜索——repository.go:391-393），
 *   非待质检状态禁用并给原因，数据源为既有入库单列表端点，不造假数据；
 * - 检验明细的 SKU 下拉限定为来源入库单已收货明细（GET /api/inbounds/{id} 带出 items），
 *   可检余量=qty_received−qty_inspected（与后端 service_quality.go:107-116 同口径），
 *   余量≤0 的 SKU 不出现；数量前端先行校验 ≤ 余量，后端强校验兜底——否则全量 SKU
 *   下拉选到非本单 SKU，后端只会回「请求参数错误：SKU 不在该入库单明细中」；
 * - inspection_type：免检 / 抽检 / 全检（迁移 chk_quality_orders_inspection_type 三值）；
 * - lines：SKU + 批次（选填）+ 计划检验数量（正数）；同一 SKU 不得重复；
 * - remark：备注（选填）。
 */

/** 检验方式三值（值即文案，与迁移 chk 同源） */
const INSPECTION_TYPE_OPTIONS = (Object.keys(INSPECTION_METHOD_LABEL) as InspectionMethod[]).map(
  (value) => ({ value, label: INSPECTION_METHOD_LABEL[value] }),
)

/**
 * 可建质检单的入库单状态（后端契约 service_quality.go:85-89 仅放行两待处理态）。
 */
const QC_ELIGIBLE_INBOUND_STATUSES: InboundOrderStatus[] = ['AWAITING_QC', 'AWAITING_PUTAWAY']

/** 入库单七态 → SfStatusTag（models.go:27-33 迁移 CHECK 同源，与 InboundPage INBOUND_STATUS_TAG 同值） */
const INBOUND_STATUS_TAG: Record<InboundOrderStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  RECEIVING: { key: 'receiving', label: '收货中', semantic: 'processing' },
  AWAITING_QC: { key: 'awaiting_qc', label: '待质检', semantic: 'pending' },
  AWAITING_PUTAWAY: { key: 'awaiting_putaway', label: '待上架', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
  CLOSED: { key: 'closed', label: '已关闭', semantic: 'neutral' },
}

interface QualityCreateFormValues {
  source_no: string
  inspection_type: InspectionMethod
  lines: Array<{ sku_id?: number; batch_no?: string; qty_inspected?: number }>
  remark?: string
}

export interface QualityCreateModalProps {
  open: boolean
  onClose: () => void
  /** 创建成功（返回新建质检单，PENDING 态）；由列表页刷新并提示 */
  onCreated: (order: QualityInspectionItem) => void
}

export default function QualityCreateModal({ open, onClose, onCreated }: QualityCreateModalProps) {
  const [form] = Form.useForm<QualityCreateFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const [submitting, setSubmitting] = useState(false)
  /** 选中的来源入库单（SfOrderSelect raw 带出）：限定可检 SKU 与余量 */
  const [selectedInbound, setSelectedInbound] = useState<InboundOrder | null>(null)

  // SKU / 仓库选项（options 一次取全；仅创建弹窗打开时拉取）
  const skuOptionsQuery = useQuery({
    queryKey: SKU_OPTIONS_KEY,
    queryFn: fetchSkuOptions,
    enabled: open,
  })
  const warehouseOptionsQuery = useQuery({
    queryKey: WAREHOUSE_OPTIONS_KEY,
    queryFn: fetchWarehouseOptions,
    enabled: open,
  })
  const warehouseNames = useMemo(
    () => new Map((warehouseOptionsQuery.data ?? []).map((w) => [idKey(w.id), `${w.name}（${w.code}）`])),
    [warehouseOptionsQuery.data],
  )

  // 来源入库单明细（sku_id/qty_received/qty_inspected）→ 各 SKU 可检余量
  const sourceDetailQuery = useQuery({
    queryKey: ['quality', 'create-source', selectedInbound?.id],
    queryFn: () => inboundApi.detail(selectedInbound!.id),
    enabled: open && selectedInbound != null,
  })
  const remainingBySku = useMemo(() => {
    const map = new Map<number, number>()
    for (const item of sourceDetailQuery.data?.items ?? []) {
      map.set(
        Number(item.sku_id),
        Math.max(0, (item.qty_received ?? 0) - (item.qty_inspected ?? 0)),
      )
    }
    return map
  }, [sourceDetailQuery.data])

  // SKU 选项=来源入库单内可检余量 > 0 的 SKU（名称经全局 options 映射，缺资料降级 #ID）
  const skuSelectOptions = useMemo(() => {
    const labels = new Map(
      (skuOptionsQuery.data ?? []).map((sku) => [
        Number(sku.id),
        sku.product_name ? `${sku.code} ${sku.product_name}` : sku.code,
      ]),
    )
    return [...remainingBySku.entries()]
      .filter(([, remaining]) => remaining > 0)
      .map(([skuId]) => ({
        value: String(skuId),
        label: labels.get(skuId) ?? `SKU #${skuId}`,
      }))
  }, [remainingBySku, skuOptionsQuery.data])

  /** 单号 → 下拉选项（仓库名经 options 本地映射，缺资料降级 #ID，不造假数据） */
  const toInboundOption = useCallback(
    (order: InboundOrder): SfOrderSelectOption<InboundOrder> => {
      const statusMeta = INBOUND_STATUS_TAG[order.status]
      return {
        value: order.inbound_no,
        label: order.inbound_no,
        description: (
          <Flex gap={8} align="center" wrap="nowrap">
            <span>
              {`来源 ${order.source_no || '-'}`}
              {' · '}
              {warehouseNames.get(idKey(order.warehouse_id)) ?? `仓库 #${order.warehouse_id}`}
              {' · '}
              {formatDate(order.created_at)}
            </span>
            <SfStatusTag
              status={toStatusKey(order.status)}
              label={statusMeta?.label}
              semantic={statusMeta?.semantic}
            />
          </Flex>
        ),
        raw: order,
      }
    },
    [warehouseNames],
  )

  /** 单号下拉禁用规则：仅两待处理态可建质检单（后端 service_quality.go:85-89 同口径） */
  const inboundDisabledReason = (status: InboundOrderStatus): string | undefined =>
    QC_ELIGIBLE_INBOUND_STATUSES.includes(status)
      ? undefined
      : '入库单未收齐进入待质检，不能创建质检单'

  /**
   * SfOrderSelect 数据源：空关键词=两个待质检状态各取最近一页合并（入库列表按 id ASC
   * 分页——repository.go:195-203，合并后倒序）；有关键词=单请求 inbound_no/source_no
   * ILIKE 模糊搜索，命中的非待质检单据标禁用 + 原因。
   */
  const loadInboundOptions = useCallback(
    async (keyword: string): Promise<Array<SfOrderSelectOption<InboundOrder>>> => {
      const trimmed = keyword.trim()
      if (!trimmed) {
        const pages = await Promise.all(
          QC_ELIGIBLE_INBOUND_STATUSES.map((status) =>
            inboundApi.list({ status, page: 1, pageSize: 20 }),
          ),
        )
        return pages
          .flatMap((page) => page.items)
          .sort((a, b) => Number(b.id) - Number(a.id))
          .map(toInboundOption)
      }
      const page = await inboundApi.list({ keyword: trimmed, page: 1, pageSize: 50 })
      return page.items.map((order) => ({
        ...toInboundOption(order),
        disabledReason: inboundDisabledReason(order.status),
      }))
    },
    [toInboundOption],
  )

  // 每次打开重建表单（不残留上次草稿），并清空来源单选择状态
  useEffect(() => {
    if (open) {
      form.resetFields()
      setSelectedInbound(null)
    }
  }, [open, form])

  /** 来源单切换/清空：可检 SKU 集合随之变化，明细行重置为一行空行防残留他单 SKU */
  const handleSourceChange = (
    value: string | undefined,
    option: SfOrderSelectOption<InboundOrder> | undefined,
  ) => {
    const next = value ? (option?.raw ?? null) : null
    const changed = next?.inbound_no !== selectedInbound?.inbound_no
    setSelectedInbound(next)
    if (changed) form.setFieldValue('lines', [{}])
  }

  /** 实际提交（表单值已通过校验）：API 错误呈可读信封信息，成功回调列表页刷新 */
  const submit = async (values: QualityCreateFormValues) => {
    setSubmitting(true)
    try {
      const payload: QualityCreatePayload = {
        source_no: values.source_no.trim(),
        inspection_type: values.inspection_type,
        lines: values.lines.map((line) => ({
          sku_id: Number(line.sku_id),
          batch_no: line.batch_no?.trim() || undefined,
          qty_inspected: line.qty_inspected as number,
        })),
        remark: values.remark?.trim() || undefined,
      }
      const order = await qualityApi.create(payload)
      messageApi.success(`质检单已创建：${order.qc_no}（待质检）`)
      onCreated(order)
      onClose()
    } finally {
      setSubmitting(false)
    }
  }

  const handleSubmit = () => {
    void form
      .validateFields()
      .then((values) => {
        submit(values).catch((error: unknown) => messageApi.error(resolveErrorMessage(error)))
      })
      .catch(() => {
        // 表单校验错误已在字段级 message 展示，不重复提示
      })
  }

  return (
    <Modal
      title="手动创建质检单"
      open={open}
      onCancel={onClose}
      destroyOnHidden
      width={720}
      footer={[
        <Button key="cancel" onClick={onClose}>
          取消
        </Button>,
        <Button key="submit" type="primary" loading={submitting} onClick={handleSubmit}>
          创建质检单
        </Button>,
      ]}
    >
      {contextHolder}
      <p style={{ marginBottom: 16 }}>
        面向入库质检补录场景：下拉选择来源入库单后，SKU 下拉限定为该单已收货明细（标注可检余量），
        录入计划检验明细；入库单须已收齐进入待质检（AWAITING_QC / AWAITING_PUTAWAY），
        SKU 与数量约束由后端按收货量强校验。
      </p>
      <Form<QualityCreateFormValues>
        form={form}
        layout="vertical"
        requiredMark="optional"
        initialValues={{ lines: [{}] }}
      >
        <Space.Compact block>
          <Form.Item
            name="source_no"
            label="来源入库单号"
            rules={[{ required: true, message: '请选择来源入库单号' }]}
            style={{ flex: 1, marginRight: 12 }}
          >
            <SfOrderSelect<InboundOrder>
              placeholder="选择入库单（可输入单号搜索）"
              loadOptions={loadInboundOptions}
              emptyText="没有待质检 / 待上架的入库单"
              onChange={handleSourceChange}
            />
          </Form.Item>
          <Form.Item
            name="inspection_type"
            label="检验方式"
            rules={[{ required: true, message: '请选择检验方式' }]}
            style={{ width: 180 }}
          >
            <Select options={INSPECTION_TYPE_OPTIONS} placeholder="免检 / 抽检 / 全检" />
          </Form.Item>
        </Space.Compact>

        <Form.Item label="检验明细（SKU + 批次 + 计划检验数量）" required style={{ marginBottom: 8 }}>
          <Form.List
            name="lines"
            rules={[
              {
                validator: async (_, value) => {
                  if (!value || value.length === 0) throw new Error('质检单必须至少一行明细')
                },
              },
            ]}
          >
            {(fields, { add, remove }, { errors }) => (
              <>
                {fields.map((field) => (
                  <Space.Compact key={field.key} block style={{ marginBottom: 8 }}>
                    <Form.Item
                      name={[field.name, 'sku_id']}
                      rules={[{ required: true, message: '请选择 SKU' }]}
                      style={{ flex: 2, marginRight: 8, marginBottom: 0 }}
                    >
                      <Select
                        showSearch
                        optionFilterProp="label"
                        options={skuSelectOptions}
                        loading={skuOptionsQuery.isFetching || sourceDetailQuery.isFetching}
                        placeholder="SKU 编码 / 商品名"
                        notFoundContent={
                          skuOptionsQuery.isFetching || sourceDetailQuery.isFetching ? (
                            '加载中…'
                          ) : !selectedInbound ? (
                            '请先选择来源入库单号'
                          ) : sourceDetailQuery.isError ? (
                            '来源单明细加载失败，请重新选择'
                          ) : (
                            '该入库单没有可检 SKU 余量'
                          )
                        }
                        optionRender={(option) => (
                          <Flex justify="space-between" gap={12}>
                            <span>{option.label}</span>
                            <span style={{ fontSize: 12, color: 'var(--sf-text-secondary)' }}>
                              {`可检 ${remainingBySku.get(Number(option.value)) ?? 0}`}
                            </span>
                          </Flex>
                        )}
                      />
                    </Form.Item>
                    <Form.Item
                      name={[field.name, 'batch_no']}
                      style={{ flex: 1, marginRight: 8, marginBottom: 0 }}
                    >
                      <Input placeholder="批次号（选填）" allowClear />
                    </Form.Item>
                    <Form.Item
                      name={[field.name, 'qty_inspected']}
                      rules={[
                        { required: true, message: '请输入数量' },
                        {
                          validator: async (_, value: number | undefined) => {
                            const skuId = Number(
                              form.getFieldValue(['lines', field.name, 'sku_id']),
                            )
                            const remaining = remainingBySku.get(skuId)
                            if (value != null && remaining != null && value > remaining) {
                              throw new Error(`检验数量不得超过该 SKU 可检余量 ${remaining}`)
                            }
                          },
                        },
                      ]}
                      style={{ width: 150, marginRight: 8, marginBottom: 0 }}
                    >
                      <InputNumber min={1} precision={0} placeholder="检验数量" style={{ width: '100%' }} />
                    </Form.Item>
                    <Button danger type="text" onClick={() => remove(field.name)}>
                      删除
                    </Button>
                  </Space.Compact>
                ))}
                <Button
                  type="dashed"
                  block
                  icon={<PlusOutlined />}
                  onClick={() => add({ qty_inspected: undefined })}
                >
                  添加明细行
                </Button>
                <Form.ErrorList errors={errors} />
              </>
            )}
          </Form.List>
        </Form.Item>

        <Form.Item name="remark" label="备注">
          <Input.TextArea rows={2} placeholder="选填，随质检单落库" maxLength={500} />
        </Form.Item>
      </Form>
    </Modal>
  )
}
