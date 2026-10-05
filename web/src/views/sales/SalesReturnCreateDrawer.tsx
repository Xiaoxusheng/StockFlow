import { useEffect, useMemo, useState } from 'react'
import { Button, Descriptions, Drawer, Flex, Form, Input, InputNumber, Typography, message } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { SfTable } from '@/components/table/SfTable'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  SALES_RETURN_CREATE_PERMISSION,
  salesApi,
  type SalesOrderDetail,
  type SalesOrderItem,
  type SalesReturnCreatePayload,
  type SalesReturnLineInput,
} from '@/api/sales'
import { toStatusKey } from '@/api/masterdata'
import {
  buildCustomerMaps,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchCustomerOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SALES_ORDER_STATUS_TAG } from './salesStatusMeta'
import { EMPTY_TEXT, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 行编辑态：qty_return 为 numeric(18,4) 文本契约的数值输入，提交时转字符串 */
interface LineEditState {
  qty_return?: number
  reason?: string
  remark?: string
}

/** 明细行可退上限（仅前端预检提示：qty_shipped；「已发货 − 已退量」由后端强校验，
 * internal/returns/service_sales.go:188-210 sumReturned 同源累计） */
function shipQtyOf(item: SalesOrderItem): number {
  return item.qty_shipped
}

/**
 * 新建销售退货抽屉（POST /api/returns，returns:salesreturn:create）。
 * 交互：输入来源销售单号精确查询（salesApi.orders.list({so_no})，handler.go:114 精确匹配）
 * → 带出订单与已发货明细（qty_shipped>0 的行可退）→ 填退货数量 + 原因（必填）
 * → 提交 SalesReturnCreateInput（service_sales.go:44-50：so_no/customer_id/warehouse_id
 * 由来源单带出，退货仓必须与原单发货仓一致由后端强校验 service_sales.go:183-187）。
 * 来源单出参为裸 ID（sku_id/customer_id/warehouse_id 无联表名称），经基础资料 options
 * 本地映射补充，映射失败降级 #ID /「-」，不造假数据；单据状态走 SfStatusTag（§24）。
 * 不支持的行如实显示为不可退，不造假数据。
 */
export function SalesReturnCreateDrawer({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: () => void
}) {
  const [messageApi, contextHolder] = message.useMessage()
  const [form] = Form.useForm<{ so_no?: string; remark?: string }>()
  const [source, setSource] = useState<SalesOrderDetail | null>(null)
  const [lineEdits, setLineEdits] = useState<Record<number, LineEditState>>({})
  const [sourceError, setSourceError] = useState<Error | null>(null)
  const [sourceLoading, setSourceLoading] = useState(false)
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_RETURN_CREATE_PERMISSION)

  // 来源单裸 ID → SKU 编码/名称、客户/仓库名称映射（options 一次取全，失败降级不阻塞抽屉；
  // queryKey 与列表/详情页共享缓存，打开抽屉不重复请求）
  const customersQuery = useQuery({
    queryKey: ['options', 'customers'],
    queryFn: fetchCustomerOptions,
  })
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skusQuery = useQuery({
    queryKey: ['options', 'skus'],
    queryFn: fetchSkuOptions,
  })
  const customerNames = useMemo(
    () => buildCustomerMaps(customersQuery.data ?? []).name,
    [customersQuery.data],
  )
  const warehouseMaps = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []),
    [warehousesQuery.data],
  )
  const skuMaps = useMemo(() => buildSkuMaps(skusQuery.data ?? []), [skusQuery.data])

  useEffect(() => {
    if (open) {
      form.resetFields()
      setSource(null)
      setLineEdits({})
      setSourceError(null)
    }
  }, [form, open])

  const loadSource = async () => {
    const soNo = (form.getFieldValue('so_no') as string | undefined)?.trim()
    if (!soNo) {
      messageApi.warning('请先输入来源销售单号')
      return
    }
    setSourceError(null)
    setSourceLoading(true)
    try {
      const page = await salesApi.orders.list({ so_no: soNo, page: 1, pageSize: 1 })
      const order = page.items[0]
      if (!order) {
        setSource(null)
        messageApi.error(`未找到销售单号 ${soNo}`)
        return
      }
      const detail = await salesApi.orders.detail(order.id)
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
    () => (source?.items ?? []).filter((item) => item.qty_shipped > 0),
    [source],
  )

  const createMutation = useMutation({
    mutationFn: (payload: SalesReturnCreatePayload) => salesApi.returns.create(payload),
    onSuccess: (created) => {
      messageApi.success(`退货单 ${created.return_no} 已创建`)
      onCreated()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSubmit = () => {
    if (!source) {
      messageApi.warning('请先带出来源销售单明细')
      return
    }
    const lines: SalesReturnLineInput[] = returnableItems.flatMap((item) => {
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
      return item ? Number(line.qty_return) > shipQtyOf(item) : false
    })
    if (over) {
      messageApi.warning('退货数量不能超过该行已发货量（最终以「已发货 − 已退」为准，后端校验）')
      return
    }
    createMutation.mutate({
      so_no: source.order.so_no,
      customer_id: Number(source.order.customer_id),
      warehouse_id: Number(source.order.warehouse_id),
      remark: (form.getFieldValue('remark') as string | undefined)?.trim() || undefined,
      lines,
    })
  }

  const columns: ColumnsType<SalesOrderItem> = [
    { title: '行号', dataIndex: 'line_no', width: 60 },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      ellipsis: true,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? `#${String(v)}`,
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 160,
      ellipsis: true,
      render: (_: unknown, record: SalesOrderItem) =>
        skuMaps.name.get(idKey(record.sku_id)) ?? EMPTY_TEXT,
    },
    {
      title: '已发货',
      dataIndex: 'qty_shipped',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '退货数量',
      key: 'qty_return',
      width: 130,
      render: (_: unknown, record: SalesOrderItem) => (
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
      render: (_: unknown, record: SalesOrderItem) => (
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
      render: (_: unknown, record: SalesOrderItem) => (
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

  const sourceOrderStatusMeta = source ? SALES_ORDER_STATUS_TAG[source.order.status] : undefined
  const sourceWarehouseName = source
    ? warehouseMaps.name.get(idKey(source.order.warehouse_id))
    : undefined
  const sourceWarehouseCode = source
    ? warehouseMaps.code.get(idKey(source.order.warehouse_id))
    : undefined
  const sourceCustomerName = source
    ? customerNames.get(idKey(source.order.customer_id))
    : undefined

  return (
    <Drawer
      open={open}
      title="新建销售退货"
      width={860}
      onClose={onClose}
      destroyOnHidden
      footer={
        <Flex justify="end" gap={8}>
          <Button onClick={onClose}>取消</Button>
          {canCreate ? (
            <SfConfirm
              okText="创建退货单"
              confirming={createMutation.isPending}
              title="确认创建销售退货单？"
              description="创建后为草稿状态，提交审核前可继续编辑来源明细的退货数量与原因。"
              onConfirm={handleSubmit}
            >
              <Button type="primary" loading={createMutation.isPending}>
                创建退货单
              </Button>
            </SfConfirm>
          ) : (
            <Button type="primary" disabled title="需要 returns:salesreturn:create 权限">
              创建退货单
            </Button>
          )}
        </Flex>
      }
    >
      {contextHolder}
      <Flex vertical gap={16}>
        <Form form={form} layout="vertical">
          <Form.Item label="来源销售单号" required style={{ marginBottom: 8 }}>
            <Flex gap={8}>
              <Form.Item name="so_no" noStyle rules={[{ required: true, message: '请输入来源销售单号' }]}>
                <Input placeholder="如 SO-20261004-0001（精确匹配）" maxLength={64} onPressEnter={loadSource} />
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
          <SfError error={sourceError} description="来源销售单查询失败，可重试" />
        ) : source ? (
          <>
            <Descriptions
              bordered
              size="small"
              column={2}
              items={[
                { key: 'soNo', label: '销售单号', children: source.order.so_no },
                {
                  key: 'warehouse',
                  label: '发货仓库',
                  children: sourceWarehouseName
                    ? `${sourceWarehouseName}（${sourceWarehouseCode}）（退货仓必须一致）`
                    : `#${String(source.order.warehouse_id)}（退货仓必须一致）`,
                },
                {
                  key: 'customer',
                  label: '客户',
                  children: sourceCustomerName ?? `#${String(source.order.customer_id)}`,
                },
                {
                  key: 'status',
                  label: '单据状态',
                  children: (
                    <SfStatusTag
                      status={toStatusKey(source.order.status)}
                      label={sourceOrderStatusMeta?.label}
                      semantic={sourceOrderStatusMeta?.semantic}
                    />
                  ),
                },
              ]}
            />
            <SfTable<SalesOrderItem>
              variant="nested"
              rowKey="id"
              columns={columns}
              dataSource={returnableItems}
              scroll={{ x: 880 }}
              emptyText="该销售单没有已发货明细，无可退行"
            />
            <Text type="secondary">
              退货数量为 numeric(18,4)，提交后端将强校验「退量 ≤ 已发货量 − 已退量」（plan §3.1）；
              未发货的行不可退。
            </Text>
          </>
        ) : (
          <SfEmpty description="输入来源销售单号后带出可退明细" />
        )}
      </Flex>
    </Drawer>
  )
}
