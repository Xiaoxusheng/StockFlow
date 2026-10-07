import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, Descriptions, Drawer, Flex, Form, Input, InputNumber, Typography, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { SfTable } from '@/components/table/SfTable'
import { SfOrderSelect, type SfOrderSelectOption } from '@/components/common/SfOrderSelect'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  SALES_RETURN_CREATE_PERMISSION,
  salesApi,
  type SalesOrder,
  type SalesOrderDetail,
  type SalesOrderItem,
  type SalesOrderStatus,
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
  idKey, SKU_OPTIONS_KEY, WAREHOUSE_OPTIONS_KEY } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SALES_ORDER_STATUS_TAG } from './salesStatusMeta'
import { EMPTY_TEXT, formatDate, formatNumber } from '@/utils/format'

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
 * 可退销售单状态（后端契约：FindReturnable 仅校验「存在且 QtyShipped>0」，
 * m2_bridges.go:259-286；已发货/已完结订单禁止取消——service.go:599-604，故
 * CANCELLED 必无发货；DRAFT/PENDING_APPROVAL/REJECTED 未到发货环节）。
 */
const RETURNABLE_SO_STATUSES: SalesOrderStatus[] = ['PARTIAL_SHIPPED', 'SHIPPED_ALL', 'COMPLETED']

/**
 * 新建销售退货抽屉（POST /api/returns，returns:salesreturn:create）。
 * 交互：来源销售单号下拉选择（SfOrderSelect 远程联想：初始=三个可退状态各取最近一页，
 * 输入防抖按 so_no ILIKE 模糊搜索——repository.go:348-350；不可退状态禁用并给原因）
 * → 选中即带出订单与已发货明细（qty_shipped>0 的行可退）→ 填退货数量 + 原因（必填）
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
    queryKey: WAREHOUSE_OPTIONS_KEY,
    queryFn: fetchWarehouseOptions,
  })
  const skusQuery = useQuery({
    queryKey: SKU_OPTIONS_KEY,
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

  /** 单号 → 下拉选项（客户/仓库经 options 本地映射，缺资料降级 #ID，不造假数据） */
  const toSoOption = useCallback(
    (order: SalesOrder): SfOrderSelectOption<SalesOrder> => {
      const customer = customerNames.get(idKey(order.customer_id))
      const warehouseName = warehouseMaps.name.get(idKey(order.warehouse_id))
      const warehouseCode = warehouseMaps.code.get(idKey(order.warehouse_id))
      const statusMeta = SALES_ORDER_STATUS_TAG[order.status]
      return {
        value: order.so_no,
        label: order.so_no,
        description: (
          <Flex gap={8} align="center" wrap="nowrap">
            <span>
              {customer ?? `客户 #${order.customer_id}`}
              {' · '}
              {warehouseName ? `${warehouseName}（${warehouseCode}）` : `#${String(order.warehouse_id)}`}
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
    [customerNames, warehouseMaps],
  )

  /**
   * 单号下拉禁用规则：白名单判定（与 RETURNABLE_SO_STATUSES 同源，防新增状态漏拦，
   * 如 APPROVED=已审核未发货同样不可退）；可退性最终由后端按发货量强校验。
   */
  const soDisabledReason = (status: SalesOrderStatus): string | undefined => {
    if (RETURNABLE_SO_STATUSES.includes(status)) return undefined
    return status === 'CANCELLED' ? '该销售单已取消，不可退货' : '该销售单未发货，不可退货'
  }

  /**
   * SfOrderSelect 数据源：空关键词=三个可退状态各取最近一页合并（销售列表按 id DESC
   * 分页——repository.go:362，合并后仍倒序）；有关键词=单请求 so_no ILIKE 模糊搜索，
   * 命中的不可退单据标禁用 + 原因（比隐藏更有信息量）。
   */
  const loadSoOptions = useCallback(
    async (keyword: string): Promise<Array<SfOrderSelectOption<SalesOrder>>> => {
      const trimmed = keyword.trim()
      if (!trimmed) {
        const pages = await Promise.all(
          RETURNABLE_SO_STATUSES.map((status) =>
            salesApi.orders.list({ status, page: 1, pageSize: 20 }),
          ),
        )
        return pages
          .flatMap((page) => page.items)
          .sort((a, b) => Number(b.id) - Number(a.id))
          .map(toSoOption)
      }
      const page = await salesApi.orders.list({ so_no: trimmed, page: 1, pageSize: 50 })
      return page.items.map((order) => ({
        ...toSoOption(order),
        disabledReason: soDisabledReason(order.status),
      }))
    },
    [toSoOption],
  )

  const loadSource = async (soNo: string) => {
    setSourceError(null)
    setSourceLoading(true)
    try {
      // so_no 为 ILIKE 模糊（repository.go:348-350）；client 端按 so_no 精确归一
      const page = await salesApi.orders.list({ so_no: soNo, page: 1, pageSize: 20 })
      const order = page.items.find((item) => item.so_no === soNo)
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
          <Form.Item
            name="so_no"
            label="来源销售单号"
            required
            rules={[{ required: true, message: '请选择来源销售单号' }]}
            style={{ marginBottom: 8 }}
          >
            <SfOrderSelect<SalesOrder>
              placeholder="选择销售单（可输入单号搜索）"
              loadOptions={loadSoOptions}
              emptyText="没有可退货的销售单"
              onChange={(soNo, option) => {
                if (!soNo) {
                  setSource(null)
                  setLineEdits({})
                  return
                }
                void loadSource(option?.raw?.so_no ?? soNo)
              }}
            />
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
            {/* 动效 #6（frontend.md §31）：重新带出明细时已加载表格压暗 0.8、新数据到达淡入，
                不再整块闪换；首次加载仍由「带出明细」按钮 loading 表达 */}
            <SfTable<SalesOrderItem>
              variant="nested"
              rowKey="id"
              columns={columns}
              dataSource={returnableItems}
              loading={sourceLoading}
              scroll={{ x: 880 }}
              emptyText="该销售单没有已发货明细，无可退行"
            />
            <Text type="secondary">
              退货数量为 numeric(18,4)，提交后端将强校验「退量 ≤ 已发货量 − 已退量」（plan §3.1）；
              未发货的行不可退。
            </Text>
          </>
        ) : (
          <SfEmpty description="选择来源销售单后带出可退明细" />
        )}
      </Flex>
    </Drawer>
  )
}
