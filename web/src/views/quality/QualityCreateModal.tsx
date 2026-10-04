import { useEffect, useState } from 'react'
import { Button, Form, Input, InputNumber, Modal, Select, Space, message } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { PlusOutlined } from '@ant-design/icons'
import { resolveErrorMessage } from '@/api/client'
import {
  INSPECTION_METHOD_LABEL,
  qualityApi,
  type InspectionMethod,
  type QualityCreatePayload,
  type QualityInspectionItem,
} from '@/api/quality'
import { fetchSkuOptions } from '@/api/options'

/**
 * 手动创建质检单（POST /api/quality，internal/purchase/purchase.go:65，权限
 * purchase:quality:create——按钮入口由列表页 canAccess 把关，Modal 内依赖后端强校验）。
 *
 * 入参对齐 QCCreateInput（internal/purchase/service_quality.go:37-45）：
 * - source_no：入库单号——入库单须 AWAITING_QC / AWAITING_PUTAWAY（未收齐不能质检）；
 * - inspection_type：免检 / 抽检 / 全检（迁移 chk_quality_orders_inspection_type 三值）；
 * - lines：SKU + 批次（选填）+ 计划检验数量（正数）；同一 SKU 不得重复、数量不得超过
 *   该 SKU 未处理余量（收货 − 已处理），逐条约束由后端强校验，错误信息随信封回显；
 * - remark：备注（选填）。
 * 后端没有「可选入库单号联想」端点，来源单号以手输 + 后端存在性校验为准，不造假数据。
 */

/** 检验方式三值（值即文案，与迁移 chk 同源） */
const INSPECTION_TYPE_OPTIONS = (Object.keys(INSPECTION_METHOD_LABEL) as InspectionMethod[]).map(
  (value) => ({ value, label: INSPECTION_METHOD_LABEL[value] }),
)

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

  // SKU 选项（options 一次取全；仅创建弹窗打开时拉取）
  const skuOptionsQuery = useQuery({
    queryKey: ['options', 'sku'],
    queryFn: fetchSkuOptions,
    enabled: open,
  })
  const skuSelectOptions = (skuOptionsQuery.data ?? []).map((sku) => ({
    value: String(sku.id),
    label: sku.product_name ? `${sku.code} ${sku.product_name}` : sku.code,
  }))

  // 每次打开重建表单（不残留上次草稿）
  useEffect(() => {
    if (open) form.resetFields()
  }, [open, form])

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
        面向入库质检补录场景：录入来源入库单号与计划检验明细；入库单须已收齐进入待质检
        （AWAITING_QC / AWAITING_PUTAWAY），SKU 与数量约束由后端按收货量强校验。
      </p>
      <Form<QualityCreateFormValues> form={form} layout="vertical" requiredMark="optional">
        <Space.Compact block>
          <Form.Item
            name="source_no"
            label="来源入库单号"
            rules={[{ required: true, message: '请输入来源入库单号' }]}
            style={{ flex: 1, marginRight: 12 }}
          >
            <Input placeholder="入库单号（IN-日期-流水）" allowClear />
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
                        loading={skuOptionsQuery.isFetching}
                        placeholder="SKU 编码 / 商品名"
                        notFoundContent={skuOptionsQuery.isFetching ? '加载中…' : '无匹配 SKU'}
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
                      rules={[{ required: true, message: '请输入数量' }]}
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
