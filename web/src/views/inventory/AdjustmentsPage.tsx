import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type InventoryAdjustmentItem,
  type InventoryAdjustmentQuery,
  type InventoryAdjustmentStatus,
  type InventoryAdjustmentType,
} from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

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

const COLUMNS: ColumnsType<InventoryAdjustmentItem> = [
  { title: '调整单号', dataIndex: 'adjustmentNo', width: 150, fixed: 'left' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  { title: '调整类型', dataIndex: 'adjustType', width: 90 },
  {
    title: '调整数量',
    dataIndex: 'qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
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
  { title: '申请人', dataIndex: 'createdByName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '申请时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '审核时间',
    dataIndex: 'approvedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '执行时间',
    dataIndex: 'executedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 库存调整（business-flow.md §11.1：申请必填原因 → 审核 → 执行 → 生成库存流水） */
export default function AdjustmentsPage() {
  const [params, setParams] = useState<InventoryAdjustmentQuery>({})
  const list = usePagedList<InventoryAdjustmentItem, InventoryAdjustmentQuery>({
    queryKey: ['inventory', 'adjustments'],
    fetch: (q) => inventoryApi.adjustments(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as InventoryAdjustmentQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存调整"
        subtitle="盘盈 / 盘亏 / 损耗 / 报废 / 其他调整单（申请 → 审核 → 执行）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '调整单号 / SKU / 商品名称' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
            { name: 'adjustType', label: '调整类型', control: 'select', options: ADJUST_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: ADJUST_STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<InventoryAdjustmentItem>
          storageKey="inventory-adjustments"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有库存调整单"
          scrollX={1880}
        />
      </Card>
    </div>
  )
}
