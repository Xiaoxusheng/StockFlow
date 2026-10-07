import { useEffect, useMemo } from 'react'
import { Alert, Button, Form, Input, InputNumber, Modal, Radio, Select, Space, message } from 'antd'
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TransferCreatePayload, TransferDetail, TransferLineInput, TransferLocInput, TransferOrder, TransferType } from '@/api/transfer'
import { transferApi } from '@/api/transfer'
import { resolveErrorMessage } from '@/api/client'
import {
  buildIdItemMap,
  fetchBatchOptions,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions, SKU_OPTIONS_KEY, WAREHOUSE_OPTIONS_KEY, BIN_OPTIONS_KEY, batchOptionsKey } from '@/api/options'
import type { BinItem } from '@/api/warehouse'

export interface TransferFormModalProps {
  open: boolean
  /** edit 模式的目标调拨单 id（后端仅 DRAFT 可改，transferApi.update） */
  mode: 'create' | 'edit'
  transferId?: TransferOrder['id']
  onClose: () => void
}

interface LineValues {
  sku_id?: string
  batch_id?: string
  from_bin_id?: string
  to_bin_id?: string
  qty?: number
}

interface FormValues {
  type: TransferType
  from_warehouse_id?: string
  to_warehouse_id?: string
  remark?: string
  lines: LineValues[]
}

/** 类型选项（models.go:153-154：WAREHOUSE 跨仓 / BIN 库位间且两端必须同仓） */
const TYPE_OPTIONS: Array<{ label: string; value: TransferType }> = [
  { label: '仓库 → 仓库（跨仓）', value: 'WAREHOUSE' },
  { label: '库位 → 库位（同仓）', value: 'BIN' },
]

/**
 * 登记调拨单 / 草稿编辑弹窗（POST /api/transfers、PUT /api/transfers/{id}，TransferInput，
 * internal/stockops/transfer.go:66-71）。后端完整校验（validateTransferInput）：
 * WAREHOUSE 两仓必须不同 / BIN 两端必须同仓、明细行两端仓库与单据一致、源/目标库位必填、
 * 目标行 zone_id/shelf_id 必填（五维）——前端经库位 options 反查库位的仓库/库区/货架四维
 * 组装 TransferLocInput，不做假定位；创建即 DRAFT，流转（提交/审核）由列表行动作承接。
 */
export default function TransferFormModal({ open, mode, transferId, onClose }: TransferFormModalProps) {
  const [form] = Form.useForm<FormValues>()
  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  const type = Form.useWatch('type', form)
  const fromWarehouseId = Form.useWatch('from_warehouse_id', form)
  const toWarehouseId = Form.useWatch('to_warehouse_id', form)

  useEffect(() => {
    if (!open) return
    form.resetFields()
    if (mode === 'edit' && transferId != null) {
      // 草稿编辑：预填单据头 + 明细行（from/to 库位由明细行 ID 回填）
      void transferApi.detail(transferId).then((detail) => {
        form.setFieldsValue({
          type: detail.order.type,
          from_warehouse_id: String(detail.order.from_warehouse_id),
          to_warehouse_id: String(detail.order.to_warehouse_id),
          remark: detail.order.remark || undefined,
          lines: detail.items.map((line) => ({
            sku_id: String(line.sku_id),
            batch_id: String(line.batch_id) === '0' ? undefined : String(line.batch_id),
            from_bin_id: String(line.from_bin_id),
            to_bin_id: String(line.to_bin_id),
            qty: line.qty,
          })),
        })
      })
    }
  }, [open, mode, transferId, form])

  const warehouseOptions = useQuery({
    queryKey: WAREHOUSE_OPTIONS_KEY,
    queryFn: fetchWarehouseOptions,
    enabled: open,
  })
  const skuOptions = useQuery({ queryKey: SKU_OPTIONS_KEY, queryFn: fetchSkuOptions, enabled: open })
  const binOptions = useQuery({ queryKey: BIN_OPTIONS_KEY, queryFn: fetchBinOptions, enabled: open })
  const batchOptions = useQuery({ queryKey: batchOptionsKey(), queryFn: () => fetchBatchOptions(), enabled: open })

  const binItemMap = useMemo(
    () => buildIdItemMap<BinItem>(binOptions.data ?? [], (b) => b.id),
    [binOptions.data],
  )
  const warehouseNameMap = useMemo(
    () => new Map((warehouseOptions.data ?? []).map((w) => [String(w.id), w.name])),
    [warehouseOptions.data],
  )
  const skuLabelMap = useMemo(
    () =>
      new Map(
        (skuOptions.data ?? []).map((s) => [String(s.id), `${s.code} ${s.product_name ?? ''}`.trim()]),
      ),
    [skuOptions.data],
  )

  /** 按单据端仓库过滤库位选项（明细行两端仓库必须与单据一致，后端硬校验） */
  const binOptionsOf = (warehouseId: string | undefined) =>
    warehouseId == null
      ? []
      : [...binItemMap.values()]
          .filter((bin) => String(bin.warehouse_id) === warehouseId)
          .map((bin) => ({ value: String(bin.id), label: bin.code }))

  // 仓库切换时清空明细行已选的库位：库位 options 按新仓库过滤，切换后旧值不在
  // 选项集合内，残留显示会让用户误以为还能提交一个「新仓库下不存在的库位」
  useEffect(() => {
    if (!open) return
    const lines = (form.getFieldValue('lines') ?? []) as Array<{
      from_bin_id?: string
      to_bin_id?: string
    }>
    if (lines.some((l) => l.from_bin_id || l.to_bin_id)) {
      form.setFieldsValue({
        lines: lines.map((l) => ({ ...l, from_bin_id: undefined, to_bin_id: undefined })),
      })
    }
  }, [fromWarehouseId, toWarehouseId, open, form])

  /** 库位 ID → TransferLocInput（仓库/库区/货架由 BinItem 层级锚点反查，满足目标行五维要求） */
  const locOf = (binId: string | undefined): TransferLocInput | undefined => {
    if (binId == null) return undefined
    const bin = binItemMap.get(binId)
    if (!bin) return undefined
    return {
      warehouse_id: Number(bin.warehouse_id),
      zone_id: Number(bin.zone_id),
      shelf_id: Number(bin.shelf_id),
      bin_id: Number(bin.id),
    }
  }

  const invalidateTransfers = () => {
    void queryClient.invalidateQueries({ queryKey: ['transfer', 'orders'] })
    void queryClient.invalidateQueries({ queryKey: ['pad', 'transfer'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: TransferCreatePayload) =>
      mode === 'create' ? transferApi.create(payload) : transferApi.update(transferId!, payload),
    onSuccess: (detail: TransferDetail) => {
      messageApi.success(
        mode === 'create'
          ? `调拨单已创建：${detail.order.transfer_no}（草稿，可提交审核）`
          : `调拨单 ${detail.order.transfer_no} 草稿已更新`,
      )
      invalidateTransfers()
      onClose()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleOk = () => {
    void form.validateFields().then((values) => {
      const lines: TransferLineInput[] = []
      for (const line of values.lines) {
        const from = locOf(line.from_bin_id)
        const to = locOf(line.to_bin_id)
        if (!from || !to) {
          messageApi.error('明细行库位定位失败：请重新选择源/目标库位')
          return
        }
        lines.push({
          sku_id: Number(line.sku_id),
          batch_id: line.batch_id != null && line.batch_id !== '' ? Number(line.batch_id) : 0,
          from,
          to,
          qty: line.qty as number,
        })
      }
      saveMutation.mutate({
        type: values.type,
        from_warehouse_id: Number(values.from_warehouse_id),
        to_warehouse_id: Number(values.to_warehouse_id),
        remark: values.remark?.trim() || undefined,
        lines,
      })
    })
  }

  return (
    <>
      {messageContext}
      <Modal
        title={mode === 'create' ? '登记调拨单' : '编辑调拨单草稿'}
        open={open}
        onCancel={onClose}
        onOk={handleOk}
        confirmLoading={saveMutation.isPending}
        okText={mode === 'create' ? '创建' : '保存'}
        destroyOnHidden
        width={880}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="创建后为草稿：在列表行「提交审核」进入审批；审核通过即源仓预占（待出库）"
        />
        <Form<FormValues>
          form={form}
          layout="vertical"
          initialValues={{ type: 'WAREHOUSE', lines: [{}] }}
        >
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr 1fr', gap: '0 16px' }}>
            <Form.Item name="type" label="调拨维度" rules={[{ required: true, message: '请选择调拨维度' }]}>
              <Radio.Group options={TYPE_OPTIONS} optionType="button" buttonStyle="solid" />
            </Form.Item>
            <Form.Item
              name="from_warehouse_id"
              label="源仓库"
              rules={[{ required: true, message: '请选择源仓库' }]}
            >
              <Select
                showSearch
                optionFilterProp="label"
                loading={warehouseOptions.isLoading}
                disabled={warehouseOptions.isError}
                placeholder={warehouseOptions.isError ? '仓库基础资料加载失败' : '选择源仓库'}
                options={(warehouseOptions.data ?? []).map((w) => ({ value: String(w.id), label: w.name }))}
              />
            </Form.Item>
            <Form.Item
              name="to_warehouse_id"
              label="目标仓库"
              rules={[
                { required: true, message: '请选择目标仓库' },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    const fromW = getFieldValue('from_warehouse_id')
                    if (type === 'WAREHOUSE' && value && fromW === value) {
                      return Promise.reject(new Error('跨仓调拨的源仓与目标仓必须不同（仓内移库属库位间调拨）'))
                    }
                    if (type === 'BIN' && value && fromW !== value) {
                      return Promise.reject(new Error('库位间调拨两端必须同仓'))
                    }
                    return Promise.resolve()
                  },
                }),
              ]}
            >
              <Select
                showSearch
                optionFilterProp="label"
                loading={warehouseOptions.isLoading}
                disabled={warehouseOptions.isError}
                placeholder={type === 'BIN' ? '库位间调拨须与源仓库相同' : '选择目标仓库'}
                options={(warehouseOptions.data ?? []).map((w) => ({
                  value: String(w.id),
                  label: warehouseNameMap.get(String(w.id)) ?? w.name,
                }))}
              />
            </Form.Item>
          </div>

          <Space style={{ marginBottom: 4 }}>
            <span style={{ fontWeight: 500 }}>调拨明细</span>
          </Space>
          <Form.List
            name="lines"
            rules={[
              {
                validator: async (_, lines: LineValues[]) => {
                  if (!lines || lines.length === 0) return Promise.reject(new Error('明细不能为空'))
                  return Promise.resolve()
                },
              },
            ]}
          >
            {(fields, { add, remove }, { errors }) => (
              <>
                {fields.map((field) => (
                  <Space key={field.key} align="baseline" wrap style={{ display: 'flex', marginBottom: 4 }}>
                    <Form.Item
                      name={[field.name, 'sku_id']}
                      rules={[{ required: true, message: '选择 SKU' }]}
                      style={{ minWidth: 200, marginBottom: 0 }}
                    >
                      <Select
                        showSearch
                        optionFilterProp="label"
                        loading={skuOptions.isLoading}
                        disabled={skuOptions.isError}
                        placeholder="SKU"
                        options={(skuOptions.data ?? []).map((s) => ({
                          value: String(s.id),
                          label: skuLabelMap.get(String(s.id)) ?? s.code,
                        }))}
                      />
                    </Form.Item>
                    <Form.Item name={[field.name, 'batch_id']} style={{ minWidth: 160, marginBottom: 0 }}>
                      <Select
                        allowClear
                        loading={batchOptions.isLoading}
                        placeholder="批次（0=非批次）"
                        options={(batchOptions.data ?? []).map((b) => ({
                          value: String(b.id),
                          label: b.batch_no,
                        }))}
                      />
                    </Form.Item>
                    <Form.Item
                      name={[field.name, 'from_bin_id']}
                      rules={[{ required: true, message: '选择源库位' }]}
                      style={{ minWidth: 160, marginBottom: 0 }}
                    >
                      <Select
                        showSearch
                        optionFilterProp="label"
                        loading={binOptions.isLoading}
                        placeholder={fromWarehouseId ? '源库位' : '先选源仓库'}
                        notFoundContent={
                          fromWarehouseId && !binOptions.isLoading ? '该仓库下暂无库位' : undefined
                        }
                        options={binOptionsOf(fromWarehouseId)}
                      />
                    </Form.Item>
                    <Form.Item
                      name={[field.name, 'to_bin_id']}
                      rules={[
                        { required: true, message: '选择目标库位' },
                        ({ getFieldValue }) => ({
                          validator(_, value) {
                            const fromBin = getFieldValue(['lines', field.name, 'from_bin_id'])
                            if (type === 'BIN' && value && fromBin === value) {
                              return Promise.reject(new Error('库位间调拨源库位与目标库位必须不同'))
                            }
                            return Promise.resolve()
                          },
                        }),
                      ]}
                      style={{ minWidth: 160, marginBottom: 0 }}
                    >
                      <Select
                        showSearch
                        optionFilterProp="label"
                        loading={binOptions.isLoading}
                        placeholder={toWarehouseId ? '目标库位' : '先选目标仓库'}
                        notFoundContent={
                          toWarehouseId && !binOptions.isLoading ? '该仓库下暂无库位' : undefined
                        }
                        options={binOptionsOf(toWarehouseId)}
                      />
                    </Form.Item>
                    <Form.Item
                      name={[field.name, 'qty']}
                      rules={[{ required: true, message: '调拨量必填且为正数' }]}
                      style={{ width: 140, marginBottom: 0 }}
                    >
                      <InputNumber min={0.0001} precision={4} style={{ width: '100%' }} placeholder="调拨量" />
                    </Form.Item>
                    {fields.length > 1 && (
                      <MinusCircleOutlined onClick={() => remove(field.name)} aria-label="删除明细行" />
                    )}
                  </Space>
                ))}
                <Form.ErrorList errors={errors} />
                <Button type="dashed" block icon={<PlusOutlined />} onClick={() => add()} style={{ marginTop: 8 }}>
                  添加明细行
                </Button>
              </>
            )}
          </Form.List>

          <Form.Item name="remark" label="备注" style={{ marginTop: 12 }}>
            <Input placeholder="可空" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}
