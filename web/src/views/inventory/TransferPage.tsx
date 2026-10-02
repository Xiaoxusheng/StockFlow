import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type InventoryTransferItem,
  type InventoryTransferQuery,
  type InventoryTransferStatus,
  type InventoryTransferType,
} from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const TRANSFER_TYPE_OPTIONS: Array<{ label: string; value: InventoryTransferType }> = [
  { label: '跨仓调拨', value: 'warehouse' },
  { label: '库位调拨', value: 'bin' },
]

/** 调拨维度文案（business-flow.md §10.1：仓库→仓库 / 库位→库位；未知值回退展示原始值） */
const TRANSFER_TYPE_LABEL: Record<InventoryTransferType, string> = {
  warehouse: '跨仓调拨',
  bin: '库位调拨',
}

const TRANSFER_STATUS_OPTIONS: Array<{ label: string; value: InventoryTransferStatus }> = [
  { label: '草稿', value: 'draft' },
  { label: '待审核', value: 'pending_review' },
  { label: '待出库', value: 'pending_outbound' },
  { label: '调拨中', value: 'transferring' },
  { label: '待入库', value: 'pending_inbound' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

/**
 * 调拨单状态 → SfStatusTag：状态机对齐 business-flow.md §10.1；
 * 注册表已收录 draft/pending_review/completed/cancelled，其余 key 由 label/semantic 兜底。
 */
const TRANSFER_STATUS_TAG: Record<InventoryTransferStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  draft: { key: 'draft', label: '草稿', semantic: 'neutral' },
  pending_review: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  pending_outbound: { key: 'pending_outbound', label: '待出库', semantic: 'pending' },
  transferring: { key: 'transferring', label: '调拨中', semantic: 'processing' },
  pending_inbound: { key: 'pending_inbound', label: '待入库', semantic: 'pending' },
  completed: { key: 'completed', label: '已完成', semantic: 'success' },
  cancelled: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function TransferStatusTag({ status }: { status: InventoryTransferStatus }) {
  const meta = TRANSFER_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

const COLUMNS: ColumnsType<InventoryTransferItem> = [
  { title: '调拨单号', dataIndex: 'transferNo', width: 150, fixed: 'left' },
  {
    title: '类型',
    dataIndex: 'transferType',
    width: 100,
    render: (v: InventoryTransferType) => TRANSFER_TYPE_LABEL[v] ?? v,
  },
  { title: '源仓库', dataIndex: 'sourceWarehouseName', width: 100 },
  { title: '目标仓库', dataIndex: 'targetWarehouseName', width: 100 },
  { title: '源库位', dataIndex: 'sourceBinCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '目标库位', dataIndex: 'targetBinCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  {
    title: '数量',
    dataIndex: 'qty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: InventoryTransferStatus) => <TransferStatusTag status={v} />,
  },
  { title: '创建人', dataIndex: 'createdByName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '完成时间',
    dataIndex: 'completedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 库存转移（frontend.md §10.1 库存中心模块；菜单 /inventory/transfers，config/menu.tsx:61）。
 * GET /api/inventory/transfers 为前端先行骨架：M1 库存 HTTP 面只读且仅冻结
 * inventory:inventory:list / inventory:ledger:list 两个权限点（internal/auth/permissions.go:116-117），
 * 调拨单 M1 未建表（backend-m1-plan.md §13），后端就绪前页面呈统一错误态（SfTable error 兜底），
 * 属预期行为，禁止 mock（约束 6）。维度与状态机对齐 business-flow.md §10.1。
 */
export default function TransferPage() {
  const [params, setParams] = useState<InventoryTransferQuery>({})
  const list = usePagedList<InventoryTransferItem, InventoryTransferQuery>({
    queryKey: ['inventory', 'transfers'],
    fetch: (q) => inventoryApi.transfers(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as InventoryTransferQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存转移"
        subtitle="跨仓 / 库位间调拨：源仓减少、目标仓增加，两端均生成库存流水"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '调拨单号 / SKU' },
            { name: 'transferType', label: '类型', control: 'select', options: TRANSFER_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: TRANSFER_STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<InventoryTransferItem>
          storageKey="inventory-transfers"
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
          emptyText="当前筛选条件下没有调拨单"
          scrollX={1580}
        />
      </Card>
    </div>
  )
}
