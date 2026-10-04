import { useMemo, useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import {
  inventoryApi,
  type InventoryAdjustmentItem,
  type InventoryAdjustmentQuery,
  type InventoryAdjustmentStatus,
  type InventoryAdjustmentType,
} from '@/api/inventory'
import {
  buildBinCodeMap,
  buildIdMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBatchOptions,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatQty } from '@/utils/format'

const { Text } = Typography

const ADJUST_TYPE_OPTIONS: Array<{ label: string; value: InventoryAdjustmentType }> = [
  { label: '盘盈', value: '盘盈' },
  { label: '盘亏', value: '盘亏' },
  { label: '损耗', value: '损耗' },
  { label: '报废', value: '报废' },
  { label: '其他', value: '其他' },
]

const ADJUST_STATUS_OPTIONS: Array<{ label: string; value: InventoryAdjustmentStatus }> = [
  { label: '草稿', value: 'DRAFT' },
  { label: '待审核', value: 'PENDING_APPROVAL' },
  { label: '已审核', value: 'APPROVED' },
  { label: '已驳回', value: 'REJECTED' },
  { label: '已执行', value: 'EXECUTED' },
  { label: '已取消', value: 'CANCELLED' },
]

/**
 * 调整单状态 → SfStatusTag（状态机：db/migrations/000005 注释 + business-flow.md §13.2：
 * DRAFT→PENDING_APPROVAL→APPROVED→EXECUTED，驳回 REJECTED，作废 CANCELLED）。
 * 除 EXECUTED 外均已收录 types/status.ts；未收录键由 label/semantic 兜底，颜色仍由 SfStatusTag 统一映射。
 */
const ADJUST_STATUS_TAG: Record<InventoryAdjustmentStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  APPROVED: { key: 'approved', label: '已审核', semantic: 'success' },
  REJECTED: { key: 'rejected', label: '已驳回', semantic: 'danger' },
  EXECUTED: { key: 'executed', label: '已执行', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function AdjustStatusTag({ status }: { status: InventoryAdjustmentStatus }) {
  const meta = ADJUST_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 可选维度 ID 0 值显示占位符（0=非批次） */
function idOrDash(value: string): string {
  return value === '0' ? '-' : value
}

interface AdjustmentNameMaps {
  skuCodes: Map<string, string>
  skuNames: Map<string, string>
  warehouseNames: Map<string, string>
  binCodes: Map<string, string>
  batchNos: Map<string, string>
}

/** 组装列：AdjustmentView 仅下发裸 ID，经 maps 本地解析编码/名称（解析失败降级 ID，不造假数据） */
function buildColumns(maps: AdjustmentNameMaps): ColumnsType<InventoryAdjustmentItem> {
  return [
    { title: '调整单号', dataIndex: 'adjustment_no', width: 150, fixed: 'left' },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: InventoryAdjustmentItem['sku_id']) => maps.skuCodes.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 200,
      ellipsis: true,
      render: (_: unknown, record: InventoryAdjustmentItem) => {
        const name = maps.skuNames.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      key: 'warehouse_name',
      width: 100,
      render: (v: InventoryAdjustmentItem['warehouse_id']) => maps.warehouseNames.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '库位',
      dataIndex: 'bin_id',
      key: 'bin_code',
      width: 110,
      render: (v: InventoryAdjustmentItem['bin_id']) => maps.binCodes.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '批次',
      dataIndex: 'batch_id',
      key: 'batch_no',
      width: 110,
      render: (v: InventoryAdjustmentItem['batch_id']) => idOrDash(maps.batchNos.get(idKey(v)) ?? idKey(v)),
    },
    { title: '调整类型', dataIndex: 'adjust_type', width: 90 },
    {
      title: '调整数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      // stock.Qty 裸数字出参（numeric(18,4)），按最多 4 位小数展示、不强制补零
      render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
    },
    {
      title: '调整原因',
      dataIndex: 'reason',
      width: 220,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 220 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: InventoryAdjustmentStatus) => <AdjustStatusTag status={v} />,
    },
    {
      title: '申请时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '执行时间',
      dataIndex: 'executed_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]
}

/** 库存调整（business-flow.md §11.1：申请必填原因 → 审核 → 执行 → 生成库存流水；
 * GET /api/inventory/adjustments 后端已交付：internal/inventory/handler.go:524-556，
 * 出参 AdjustmentView snake_case 仅裸 ID，经 api/options.ts 本地映射补充编码/名称，
 * 失败降级 ID；后端无 approved_at，执行信息仅 executed_at/executed_by） */
export default function AdjustmentsPage() {
  const [params, setParams] = useState<InventoryAdjustmentQuery>({})
  const list = usePagedList<InventoryAdjustmentItem, InventoryAdjustmentQuery>({
    queryKey: ['inventory', 'adjustments'],
    fetch: (q) => inventoryApi.adjustments(q),
    params,
  })

  // AdjustmentView 无联表编码/名称（warehouse_id/sku_id/bin_id/batch_id 裸 ID），options 一次取全本地映射
  const warehouses = useQuery({ queryKey: ['options', 'warehouses'], queryFn: fetchWarehouseOptions })
  const skus = useQuery({ queryKey: ['options', 'skus'], queryFn: fetchSkuOptions })
  const bins = useQuery({ queryKey: ['options', 'bins'], queryFn: fetchBinOptions })
  const batches = useQuery({ queryKey: ['options', 'batches'], queryFn: fetchBatchOptions })

  const maps: AdjustmentNameMaps = useMemo(
    () => ({
      warehouseNames: buildWarehouseMaps(warehouses.data ?? []).name,
      skuCodes: buildSkuMaps(skus.data ?? []).code,
      skuNames: buildSkuMaps(skus.data ?? []).name,
      binCodes: buildBinCodeMap(bins.data ?? []),
      batchNos: buildIdMap(batches.data ?? [], (b) => b.id, (b) => b.batch_no),
    }),
    [warehouses.data, skus.data, bins.data, batches.data],
  )
  const columns = useMemo(() => buildColumns(maps), [maps])

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存调整"
        subtitle="盘盈 / 盘亏 / 损耗 / 报废 / 其他调整单（申请 → 审核 → 执行）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'warehouse_id', label: '仓库 ID', control: 'input', placeholder: '仓库 ID（正整数）' },
            { name: 'adjust_type', label: '调整类型', control: 'select', options: ADJUST_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: ADJUST_STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as InventoryAdjustmentQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<InventoryAdjustmentItem>
          storageKey="inventory-adjustments"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有库存调整单"
          scrollX={1750}
        />
      </Card>
    </div>
  )
}
