import { useEffect, useMemo, useState } from 'react'
import { Button, Descriptions, Drawer, Flex, Form, Input, InputNumber, Typography, message } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { SfTable } from '@/components/table/SfTable'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  PURCHASE_RETURN_CREATE_PERMISSION,
  purchaseApi,
  type PurchaseOrder,
  type PurchaseOrderDetail,
  type PurchaseOrderItem,
  type PurchaseReturnCreatePayload,
  type PurchaseReturnLineInput,
  type PurchaseStatus,
} from '@/api/purchase'
import { toStatusKey } from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import {
  buildIdItemMap,
  fetchSkuOptions,
  fetchSupplierOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 采购订单状态 → SfStatusTag（internal/purchase/models.go:17-23 七态）。
 * types/status.ts 注册表已收录 draft/pending_approval/approved/completed/cancelled；
 * PARTIAL_RECEIVED/RECEIVED_ALL 为采购语境专有键未注册，经 SfStatusTag 的
 * label/semantic 兜底——与 PurchaseListPage/PurchaseOrderDetailPage 同一本地映射惯例，
 * 禁止在抽屉里显示英文裸枚举（AGENTS.md 规则 5：业务状态一律经 SfStatusTag）。
 */
const PO_STATUS_TAG: Record<PurchaseStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  PARTIAL_RECEIVED: { label: '部分到货', semantic: 'processing' },
  RECEIVED_ALL: { label: '到货完成', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 行编辑态：qty_return 为 numeric(18,4) 文本契约的数值输入，提交时转字符串 */
interface LineEditState {
  qty_return?: number
  reason?: string
  remark?: string
}

/**
 * 新建采购退货抽屉（POST /api/purchase-returns，returns:purchasereturn:create）。
 * 交互：输入来源采购单号查询（purchaseApi.list({keyword})，keyword 按 po_no ILIKE 模糊
 * repository.go:292-294，命中后前端按 po_no 精确过滤）→ 带出采购单与已收货明细
 * （qty_received>0 的行可退）→ 填退货数量 + 原因（必填）→ 提交 PurchaseReturnCreateInput
 * （service_purchase.go:44-50：po_no/supplier_id/warehouse_id 由来源单带出，退货仓必须
 * 与原单仓库一致、退量 ≤ 已收货量 − 已退量均由后端强校验）。
 * 整单一次出库（frozen DDL 无逐行已出量列），部分退货在创建期以退量 < 已收量表达
 * （service_purchase.go:20-23）。不支持的行如实显示为不可退，不造假数据。
 */
export function PurchaseReturnCreateDrawer({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: () => void
}) {
  const [messageApi, contextHolder] = message.useMessage()
  const [form] = Form.useForm<{ po_no?: string; remark?: string }>()
  const [source, setSource] = useState<PurchaseOrderDetail | null>(null)
  const [lineEdits, setLineEdits] = useState<Record<number, LineEditState>>({})
  const [sourceError, setSourceError] = useState<Error | null>(null)
  const [sourceLoading, setSourceLoading] = useState(false)
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, PURCHASE_RETURN_CREATE_PERMISSION)

  // 供应商/仓库/SKU options 一次取全（api/options.ts 头注释：映射失败由调用方降级，
  // 不阻塞抽屉；queryKey 与同域列表/详情页共享，react-query 去重命中缓存）
  const supplierOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'suppliers'],
    queryFn: fetchSupplierOptions,
  })
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skuOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'skus'],
    queryFn: fetchSkuOptions,
  })
  const supplierItems = useMemo(
    () => buildIdItemMap(supplierOptionsQuery.data ?? [], (s) => s.id),
    [supplierOptionsQuery.data],
  )
  const warehouseItems = useMemo(
    () => buildIdItemMap(warehouseOptionsQuery.data ?? [], (w) => w.id),
    [warehouseOptionsQuery.data],
  )
  const skuItems = useMemo(
    () => buildIdItemMap(skuOptionsQuery.data ?? [], (s) => s.id),
    [skuOptionsQuery.data],
  )

  useEffect(() => {
    if (open) {
      form.resetFields()
      setSource(null)
      setLineEdits({})
      setSourceError(null)
    }
  }, [form, open])

  const loadSource = async () => {
    const poNo = (form.getFieldValue('po_no') as string | undefined)?.trim()
    if (!poNo) {
      messageApi.warning('请先输入来源采购单号')
      return
    }
    setSourceError(null)
    setSourceLoading(true)
    try {
      // keyword 为 po_no ILIKE 模糊（handler.go:87-115）；client 端按 po_no 精确归一
      const page = await purchaseApi.list({ keyword: poNo, page: 1, pageSize: 20 })
      const order: PurchaseOrder | undefined = page.items.find((item) => item.po_no === poNo)
      if (!order) {
        setSource(null)
        messageApi.error(`未找到采购单号 ${poNo}`)
        return
      }
      const detail = await purchaseApi.detail(order.id)
      setSource(detail)
      setLineEdits({})
    } catch (error) {
      setSource(null)
      setSourceError(error instanceof Error ? error : new Error(String(error)))
    } finally {
      setSourceLoading(false)
    }
  }

  const returnableItems = useMemo(
    () => (source?.items ?? []).filter((item) => item.qty_received > 0),
    [source],
  )

  const createMutation = useMutation({
    mutationFn: (payload: PurchaseReturnCreatePayload) => purchaseApi.returns.create(payload),
    onSuccess: (created) => {
      messageApi.success(`退货单 ${created.return_no} 已创建`)
      onCreated()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSubmit = () => {
    if (!source) {
      messageApi.warning('请先带出来源采购单明细')
      return
    }
    const lines: PurchaseReturnLineInput[] = returnableItems.flatMap((item) => {
      const edit = lineEdits[item.line_no]
      if (!edit?.qty_return || edit.qty_return <= 0) return []
      return [
        {
          line_no: item.line_no,
          sku_id: Number(item.sku_id),
          qty_return: String(edit.qty_return),
          reason: (edit.reason ?? '').trim(),
          remark: edit.remark?.trim() || undefined,
        },
      ]
    })
    if (lines.length === 0) {
      messageApi.warning('请至少为一行填写退货数量')
      return
    }
    if (lines.some((line) => !line.reason)) {
      messageApi.warning('退货原因必填（business-flow.md §9.1）')
      return
    }
    const over = lines.find((line) => {
      const item = returnableItems.find((x) => x.line_no === line.line_no)
      return item ? Number(line.qty_return) > item.qty_received : false
    })
    if (over) {
      messageApi.warning('退货数量不能超过该行已收货量（最终以「已收货 − 已退」为准，后端校验）')
      return
    }
    createMutation.mutate({
      po_no: source.order.po_no,
      supplier_id: Number(source.order.supplier_id),
      warehouse_id: Number(source.order.warehouse_id),
      remark: (form.getFieldValue('remark') as string | undefined)?.trim() || undefined,
      lines,
    })
  }

  const columns: ColumnsType<PurchaseOrderItem> = [
    {
      title: '行号',
      dataIndex: 'line_no',
      width: 60,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: 'SKU',
      dataIndex: 'sku_id',
      width: 140,
      ellipsis: true,
      render: (_: unknown, record: PurchaseOrderItem) =>
        skuItems.get(String(record.sku_id))?.code ?? `SKU #${record.sku_id}`,
    },
    {
      title: '已收货',
      dataIndex: 'qty_received',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '退货数量',
      key: 'qty_return',
      width: 130,
      render: (_: unknown, record: PurchaseOrderItem) => (
        <InputNumber
          min={0}
          precision={4}
          value={lineEdits[record.line_no]?.qty_return}
          onChange={(v) =>
            setLineEdits((prev) => ({ ...prev, [record.line_no]: { ...prev[record.line_no], qty_return: v ?? undefined } }))
          }
          placeholder="0"
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: '退货原因（必填）',
      key: 'reason',
      width: 170,
      render: (_: unknown, record: PurchaseOrderItem) => (
        <Input
          value={lineEdits[record.line_no]?.reason}
          onChange={(e) =>
            setLineEdits((prev) => ({ ...prev, [record.line_no]: { ...prev[record.line_no], reason: e.target.value } }))
          }
          placeholder="如：质量问题"
          maxLength={64}
        />
      ),
    },
    {
      title: '行备注',
      key: 'remark',
      render: (_: unknown, record: PurchaseOrderItem) => (
        <Input
          value={lineEdits[record.line_no]?.remark}
          onChange={(e) =>
            setLineEdits((prev) => ({ ...prev, [record.line_no]: { ...prev[record.line_no], remark: e.target.value } }))
          }
          placeholder="选填"
          maxLength={255}
        />
      ),
    },
  ]

  // 来源单摘要展示值：供应商/仓库经 options 本地映射（同域列表/详情惯例），
  // 映射失败降级为裸 ID，不造假数据；状态经 SfStatusTag 出中文标签
  const supplierItem = source ? supplierItems.get(String(source.order.supplier_id)) : undefined
  const warehouseItem = source ? warehouseItems.get(String(source.order.warehouse_id)) : undefined
  const supplierText = supplierItem
    ? `${supplierItem.name}（${supplierItem.code}）`
    : source
      ? `供应商 #${source.order.supplier_id}`
      : '-'
  const warehouseText = warehouseItem
    ? `${warehouseItem.name}（${warehouseItem.code}）`
    : source
      ? `仓库 #${source.order.warehouse_id}`
      : '-'
  const statusTag = source ? PO_STATUS_TAG[source.order.status] : undefined

  return (
    <Drawer
      open={open}
      title="新建采购退货"
      size={860}
      onClose={onClose}
      destroyOnHidden
      footer={
        <Flex justify="end" gap={8}>
          <Button onClick={onClose}>取消</Button>
          {canCreate ? (
            <SfConfirm
              okText="创建退货单"
              confirming={createMutation.isPending}
              title="确认创建采购退货单？"
              description="创建后为草稿状态；整单一次出库（部分退货在创建期以退量 < 已收量表达），提交审核前可继续编辑。"
              onConfirm={handleSubmit}
            >
              <Button type="primary" loading={createMutation.isPending}>
                创建退货单
              </Button>
            </SfConfirm>
          ) : (
            <Button type="primary" disabled title="需要 returns:purchasereturn:create 权限">
              创建退货单
            </Button>
          )}
        </Flex>
      }
    >
      {contextHolder}
      <Flex vertical gap={16}>
        <Form form={form} layout="vertical">
          <Form.Item label="来源采购单号" required style={{ marginBottom: 8 }}>
            <Flex gap={8}>
              <Form.Item name="po_no" noStyle rules={[{ required: true, message: '请输入来源采购单号' }]}>
                <Input placeholder="如 PO-20261004-0001" maxLength={64} onPressEnter={loadSource} />
              </Form.Item>
              <Button icon={<ReloadOutlined />} loading={sourceLoading} onClick={loadSource}>
                带出明细
              </Button>
            </Flex>
          </Form.Item>
          <Form.Item name="remark" label="整单备注" style={{ marginBottom: 0 }}>
            <Input.TextArea rows={2} placeholder="选填" maxLength={255} />
          </Form.Item>
        </Form>

        {sourceError ? (
          <SfError error={sourceError} description="来源采购单查询失败，可重试" />
        ) : source ? (
          <>
            <Descriptions
              bordered
              size="small"
              column={2}
              items={[
                { key: 'poNo', label: '采购单号', children: source.order.po_no },
                {
                  key: 'warehouse',
                  label: '收货仓库',
                  children: `${warehouseText}（退货仓必须一致）`,
                },
                { key: 'supplier', label: '供应商', children: supplierText },
                {
                  key: 'status',
                  label: '单据状态',
                  children: (
                    <SfStatusTag
                      status={toStatusKey(source.order.status)}
                      label={statusTag?.label}
                      semantic={statusTag?.semantic}
                    />
                  ),
                },
              ]}
            />
            <SfTable<PurchaseOrderItem>
              variant="nested"
              rowKey="id"
              columns={columns}
              dataSource={returnableItems}
              scroll={{ x: 760 }}
              emptyText="该采购单没有已收货明细，无可退行"
            />
            <Text type="secondary">
              退货数量为 numeric(18,4)，提交后端将强校验「退量 ≤ 已收货量 − 已退量」（plan §3.1）；
              未收货的行不可退。
            </Text>
          </>
        ) : (
          <SfEmpty description="输入来源采购单号后带出可退明细" />
        )}
      </Flex>
    </Drawer>
  )
}
