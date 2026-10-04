import { useEffect, useMemo } from 'react'
import { Alert, Form, Input, InputNumber, Modal, Select, Switch, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ExceptionCreatePayload, ExceptionType } from '@/api/exception'
import { EXCEPTION_TYPES, exceptionApi } from '@/api/exception'
import { resolveErrorMessage } from '@/api/client'
import {
  buildIdItemMap,
  buildIdMap,
  fetchBatchOptions,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import type { BinItem } from '@/api/warehouse'

/** 类型选项（九类中文值域即文案：internal/returns/models.go:48-51） */
const TYPE_OPTIONS: Array<{ label: string; value: ExceptionType }> = EXCEPTION_TYPES.map((value) => ({
  label: value,
  value,
}))

export interface ExceptionCreateModalProps {
  open: boolean
  onClose: () => void
}

interface FormValues {
  type: ExceptionType
  source_type?: string
  source_no?: string
  detail: string
  sku_id?: number | string
  bin_id?: number | string
  serial_no?: string
  owner_name?: string
  remark?: string
  freeze_enabled?: boolean
  freeze_warehouse_id?: number | string
  freeze_batch_id?: number | string
  freeze_qty?: number
}

/**
 * 登记异常弹窗（POST /api/exceptions，ExceptionCreateInput，internal/returns/service_exception.go:33-50）：
 * 创建即 OPEN（待处理）；sku_id/bin_id 可空定位（0=未定位）；冻结为可选项——后端 fail-closed
 * 要求 sku_id+bin_id+freeze_warehouse_id 定位到库存行且冻结量为正数（inventory-rules.md §4.1）。
 * 责任人仅采集姓名（owner_name，owner_id 留空=0）：异常中心无强归属语义，business-flow.md §11.2 责任人可空。
 */
export default function ExceptionCreateModal({ open, onClose }: ExceptionCreateModalProps) {
  const [form] = Form.useForm<FormValues>()
  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  const freezeEnabled = Form.useWatch('freeze_enabled', form)

  useEffect(() => {
    if (open) form.resetFields()
  }, [open, form])

  // 基础资料 options（失败呈禁用 + 提示，不造假数据）
  const skuOptions = useQuery({ queryKey: ['exception-form', 'sku-options'], queryFn: fetchSkuOptions, enabled: open })
  const binOptions = useQuery({ queryKey: ['exception-form', 'bin-options'], queryFn: fetchBinOptions, enabled: open })
  const warehouseOptions = useQuery({
    queryKey: ['exception-form', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
    enabled: open,
  })
  const batchOptions = useQuery({
    queryKey: ['exception-form', 'batch-options'],
    queryFn: () => fetchBatchOptions(),
    enabled: open && freezeEnabled === true,
  })
  const skuNameMap = useMemo(
    () => buildIdMap(skuOptions.data ?? [], (s) => s.id, (s) => `${s.code} ${s.product_name ?? ''}`.trim()),
    [skuOptions.data],
  )
  const binCodeMap = useMemo(
    () => buildIdItemMap<BinItem>(binOptions.data ?? [], (b) => b.id),
    [binOptions.data],
  )

  const createMutation = useMutation({
    mutationFn: (payload: ExceptionCreatePayload) => exceptionApi.create(payload),
    onSuccess: (data) => {
      messageApi.success(`异常单已登记：${data.exception_no}（待处理）`)
      void queryClient.invalidateQueries({ queryKey: ['exception', 'center'] })
      onClose()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleOk = () => {
    void form.validateFields().then((values) => {
      const skuId = values.sku_id != null && values.sku_id !== '' ? Number(values.sku_id) : undefined
      const binId = values.bin_id != null && values.bin_id !== '' ? Number(values.bin_id) : undefined
      const payload: ExceptionCreatePayload = {
        type: values.type,
        source_type: values.source_type?.trim() || undefined,
        source_no: values.source_no?.trim() || undefined,
        sku_id: skuId,
        bin_id: binId,
        serial_no: values.serial_no?.trim() || undefined,
        owner_name: values.owner_name?.trim() || undefined,
        detail: values.detail.trim(),
        remark: values.remark?.trim() || undefined,
      }
      if (values.freeze_enabled) {
        payload.freeze_enabled = true
        payload.freeze_warehouse_id = Number(values.freeze_warehouse_id)
        payload.freeze_batch_id = values.freeze_batch_id != null && values.freeze_batch_id !== '' ? Number(values.freeze_batch_id) : 0
        payload.freeze_qty = String(values.freeze_qty)
      }
      createMutation.mutate(payload)
    })
  }

  return (
    <>
      {messageContext}
      <Modal
        title="登记异常"
        open={open}
        onCancel={onClose}
        onOk={handleOk}
        confirmLoading={createMutation.isPending}
        okText="登记"
        destroyOnHidden
        width={640}
      >
        <Form<FormValues> form={form} layout="vertical" initialValues={{ freeze_enabled: false }}>
          <Form.Item name="type" label="异常类型" rules={[{ required: true, message: '请选择异常类型' }]}>
            <Select options={TYPE_OPTIONS} placeholder="九类异常（business-flow.md §11.2）" />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
            <Form.Item name="source_type" label="来源类型">
              <Input placeholder="收货单 / 质检单 / 拣货任务等产生环节" maxLength={32} />
            </Form.Item>
            <Form.Item name="source_no" label="来源单号">
              <Input placeholder="如 IN-20261004-000001" />
            </Form.Item>
          </div>
          <Form.Item
            name="detail"
            label="问题描述"
            rules={[{ required: true, message: '请填写问题描述' }]}
          >
            <Input.TextArea rows={3} placeholder="异常现象、影响与定位信息" showCount maxLength={500} />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
            <Form.Item name="sku_id" label="SKU（可空定位）">
              <Select
                showSearch
                allowClear
                optionFilterProp="label"
                loading={skuOptions.isLoading}
                disabled={skuOptions.isError}
                placeholder={skuOptions.isError ? 'SKU 基础资料加载失败' : '选择 SKU（不选=未定位）'}
                options={skuOptions.isError ? [] : [...skuNameMap.entries()].map(([value, label]) => ({ value, label }))}
              />
            </Form.Item>
            <Form.Item name="bin_id" label="库位（可空定位）">
              <Select
                showSearch
                allowClear
                optionFilterProp="label"
                loading={binOptions.isLoading}
                disabled={binOptions.isError}
                placeholder={binOptions.isError ? '库位基础资料加载失败' : '选择库位（不选=未定位）'}
                options={
                  binOptions.isError
                    ? []
                    : [...binCodeMap.values()].map((bin) => ({ value: String(bin.id), label: bin.code }))
                }
              />
            </Form.Item>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '0 16px' }}>
            <Form.Item name="serial_no" label="序列号">
              <Input placeholder="空=非序列号问题" />
            </Form.Item>
            <Form.Item name="owner_name" label="责任人">
              <Input placeholder="可空（business-flow.md §11.2）" />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input placeholder="可空" />
            </Form.Item>
          </div>

          <Form.Item name="freeze_enabled" label="异常冻结" valuePropName="checked">
            <Switch checkedChildren="冻结库存行" unCheckedChildren="不冻结" />
          </Form.Item>
          {freezeEnabled && (
            <>
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 12 }}
                message="异常冻结需要 SKU + 库位 + 仓库定位到具体库存行（inventory-rules.md §4.1），定位不足后端将拒绝创建"
              />
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '0 16px' }}>
                <Form.Item
                  name="freeze_warehouse_id"
                  label="冻结仓库"
                  rules={[{ required: true, message: '冻结需指定仓库' }]}
                >
                  <Select
                    showSearch
                    optionFilterProp="label"
                    loading={warehouseOptions.isLoading}
                    disabled={warehouseOptions.isError}
                    placeholder="选择仓库"
                    options={
                      warehouseOptions.isError
                        ? []
                        : (warehouseOptions.data ?? []).map((w) => ({ value: String(w.id), label: w.name }))
                    }
                  />
                </Form.Item>
                <Form.Item
                  name="freeze_qty"
                  label="冻结量"
                  rules={[{ required: true, message: '冻结量必填且为正数' }]}
                >
                  <InputNumber min={0.0001} style={{ width: '100%' }} placeholder="numeric(18,4)" />
                </Form.Item>
                <Form.Item name="freeze_batch_id" label="冻结批次">
                  <Select
                    allowClear
                    loading={batchOptions.isLoading}
                    placeholder="0=非批次 SKU"
                    options={
                      batchOptions.isError
                        ? []
                        : (batchOptions.data ?? []).map((b) => ({ value: String(b.id), label: b.batch_no }))
                    }
                  />
                </Form.Item>
              </div>
            </>
          )}
        </Form>
      </Modal>
    </>
  )
}
