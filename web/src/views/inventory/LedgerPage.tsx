import { useState, type ReactNode } from 'react'
import { Card, Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined, MinusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type InventoryChangeType,
  type LedgerItem,
  type LedgerQuery,
} from '@/api/inventory'
import { DateCell } from '@/components/table/cells'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/** 流水类型选项（inventory_ledgers CHECK 值域，与 StockDetailPage CHANGE_TYPE_LABEL 同源；后端 handler.go:300-308 校验） */
const CHANGE_TYPE_OPTIONS: Array<{ label: string; value: InventoryChangeType }> = [
  { label: '入库', value: 'INBOUND' },
  { label: '出库', value: 'OUTBOUND' },
  { label: '调拨出库', value: 'TRANSFER_OUT' },
  { label: '调拨入库', value: 'TRANSFER_IN' },
  { label: '锁定', value: 'LOCK' },
  { label: '释放', value: 'RELEASE' },
  { label: '移库', value: 'MOVE' },
  { label: '质检合格', value: 'INSPECT_PASS' },
  { label: '质检不合格', value: 'INSPECT_DEFECTIVE' },
  { label: '调整', value: 'ADJUST' },
]

/** 变更类型文案（值域同上） */
const CHANGE_TYPE_LABEL: Record<InventoryChangeType, string> = {
  INBOUND: '入库',
  OUTBOUND: '出库',
  TRANSFER_OUT: '调拨出库',
  TRANSFER_IN: '调拨入库',
  LOCK: '锁定',
  RELEASE: '释放',
  MOVE: '移库',
  INSPECT_PASS: '质检合格',
  INSPECT_DEFECTIVE: '质检不合格',
  ADJUST: '调整',
}

/** 库存状态文案（inventory_ledgers status_from/status_to CHECK 值域） */
const STATE_LABEL: Record<string, string> = {
  available: '可用',
  locked: '锁定',
  frozen: '冻结',
  pending_inspect: '待检',
  defective: '不良',
}

/** 方向由变更数量正负驱动（LedgerView 无独立 direction 字段，正=入、负=出） */
function DirectionText({ qtyChange }: { qtyChange: number }) {
  if (qtyChange > 0) {
    return (
      <Text style={{ color: 'var(--sf-success)' }}>
        <ArrowDownOutlined /> 入
      </Text>
    )
  }
  if (qtyChange < 0) {
    return (
      <Text style={{ color: 'var(--sf-danger)' }}>
        <ArrowUpOutlined /> 出
      </Text>
    )
  }
  return (
    <Text type="secondary">
      <MinusOutlined /> 无
    </Text>
  )
}

function renderQty(value: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

function renderState(from?: string, to?: string): ReactNode {
  if (!from && !to) return '-'
  const fromLabel = STATE_LABEL[from ?? ''] ?? from ?? '-'
  const toLabel = STATE_LABEL[to ?? ''] ?? to ?? '-'
  return (
    <Text style={{ maxWidth: 120, whiteSpace: 'nowrap' }} ellipsis={{ tooltip: `${fromLabel} → ${toLabel}` }}>
      {fromLabel} → {toLabel}
    </Text>
  )
}

const COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <DateCell value={v} />,
  },
  { title: '流水号', dataIndex: 'ledger_no', width: 150 },
  { title: '单据编号', dataIndex: 'business_no', width: 150, fixed: 'left', render: (v: string) => v || '-' },
  { title: '业务类型', dataIndex: 'business_type', width: 100, render: (v: string) => v || '-' },
  {
    title: '变更类型',
    dataIndex: 'change_type',
    width: 100,
    render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
  },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 100 },
  { title: '库位 ID', dataIndex: 'bin_id', width: 100, render: renderIdOrDash },
  { title: '批次 ID', dataIndex: 'batch_id', width: 100, render: renderIdOrDash },
  { title: '序列号', dataIndex: 'serial_no', width: 130, render: (v: string) => v || '-' },
  {
    title: '方向',
    key: 'direction',
    width: 80,
    render: (_: unknown, record: LedgerItem) => <DirectionText qtyChange={record.qty_change} />,
  },
  { title: '数量', dataIndex: 'qty_change', width: 90, align: 'right', render: renderQty },
  { title: '变动前', dataIndex: 'qty_before', width: 90, align: 'right', render: renderQty },
  { title: '变动后', dataIndex: 'qty_after', width: 90, align: 'right', render: renderQty },
  {
    title: '状态流转',
    key: 'state_flow',
    width: 130,
    render: (_: unknown, record: LedgerItem) => renderState(record.status_from, record.status_to),
  },
  { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
]

/** 可选维度 ID 0 值显示占位符（0=非批次/不在库） */
function renderIdOrDash(value: LedgerItem['batch_id']): string {
  return String(value) === '0' ? '-' : String(value)
}

/**
 * 库存流水（GET /api/inventory-ledgers，后端 T5 已交付）：所有库存变化完整轨迹
 * （inventory-rules.md §5 字段清单，append-only 禁止前端拼装数据）。
 */
export default function LedgerPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['inventory', 'ledger'],
    fetch: (q) => inventoryApi.ledger(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  return (
    <div className="sf-page">
      <SfPageHeader title="库存流水" subtitle="库存变化完整轨迹，与库存保持一致" />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'business_no', label: '单据编号', control: 'input', placeholder: '来源单据编号' },
            { name: 'change_type', label: '变更类型', control: 'select', options: CHANGE_TYPE_OPTIONS },
            { name: 'serial_no', label: '序列号', control: 'input', placeholder: '序列号' },
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<LedgerItem>
          /* 保存视图（§2.2）：urlSync 页经 usePagedList 公开 API 写 URL，应用即还原筛选+分页 */
          actions={
            <SfViewBar
              pageKey="inventory.ledger"
              mode="url"
              paged={{ applyFilters: list.applyFilters, onPageChange: list.onPageChange }}
              appliedFilters={list.params as unknown as Record<string, unknown>}
              currentFilters={list.formValues}
              currentPageSize={list.pagination.pageSize}
              currentHiddenColumns={hiddenColumns}
              onHiddenColumnsChange={setHiddenColumns}
            />
          }
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="inventory-ledger"
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
          emptyText="当前筛选条件下没有库存流水"
          scrollX={1960}
        />
      </Card>
    </div>
  )
}
