import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type InventoryChangeType,
  type TraceItem,
  type TraceQuery,
} from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 变更类型文案（值域对齐 db/migrations/000005 inventory_ledgers CHECK 约束；未知值回退展示原始值） */
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

/** 库存状态列文案（inventory_ledgers status_from/status_to CHECK 值域；未知值回退展示原始值） */
const STATE_LABEL: Record<string, string> = {
  available: '可用',
  locked: '锁定',
  frozen: '冻结',
  pending_inspect: '待检',
  defective: '不良',
}

const COLUMNS: ColumnsType<TraceItem> = [
  {
    title: '事件时间',
    dataIndex: 'occurredAt',
    width: 160,
    fixed: 'left',
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '变更类型',
    dataIndex: 'changeType',
    width: 110,
    render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
  },
  { title: '业务类型', dataIndex: 'bizType', width: 110 },
  { title: '单据号', dataIndex: 'bizNo', width: 160 },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  { title: '序列号', dataIndex: 'serialNo', width: 130, render: (v?: string) => v ?? '-' },
  {
    title: '状态流转',
    key: 'statusFlow',
    width: 140,
    render: (_: unknown, record: TraceItem) => {
      if (!record.statusFrom && !record.statusTo) return '-'
      const from = STATE_LABEL[record.statusFrom ?? ''] ?? record.statusFrom ?? '-'
      const to = STATE_LABEL[record.statusTo ?? ''] ?? record.statusTo ?? '-'
      return (
        <Text style={{ maxWidth: 140, whiteSpace: 'nowrap' }} ellipsis={{ tooltip: `${from} → ${to}` }}>
          {from} → {to}
        </Text>
      )
    },
  },
  {
    title: '变更数量',
    dataIndex: 'qtyChange',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '结余',
    dataIndex: 'qtyAfter',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '操作人', dataIndex: 'operatorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
]

/**
 * 库存追溯（frontend.md §10.1 库存中心模块；菜单 /inventory/trace，config/menu.tsx:64）。
 * GET /api/inventory/trace 为前端先行骨架：M1 库存 HTTP 面只读且仅冻结
 * inventory:inventory:list / inventory:ledger:list 两个权限点（internal/auth/permissions.go:116-117），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 追溯数据源为 inventory_ledgers（append-only，db/migrations/000005），支持 SKU / 序列号 /
 * 批次号 / 单据号 四维查询（backend-m1-plan.md §8.4 状态三态口径）。
 */
export default function TracePage() {
  const [params, setParams] = useState<TraceQuery>({})
  const list = usePagedList<TraceItem, TraceQuery>({
    queryKey: ['inventory', 'trace'],
    fetch: (q) => inventoryApi.trace(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as TraceQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存追溯"
        subtitle="按 SKU / 序列号 / 批次号 / 单据号追溯库存全链路流水"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'skuCode', label: 'SKU', control: 'input', placeholder: 'SKU 编码' },
            { name: 'serialNo', label: '序列号 SN', control: 'input', placeholder: '序列号' },
            { name: 'batchNo', label: '批次号', control: 'input', placeholder: '批次号' },
            { name: 'bizNo', label: '单据号', control: 'input', placeholder: '业务单据号' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<TraceItem>
          storageKey="inventory-trace"
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
          emptyText="请输入 SKU / 序列号 / 批次号 / 单据号进行追溯"
          scrollX={1900}
        />
      </Card>
    </div>
  )
}
