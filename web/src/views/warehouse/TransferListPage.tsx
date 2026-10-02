import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  TRANSFER_STATUS_TAG,
  transferApi,
  type TransferItem,
  type TransferQuery,
  type TransferStatus,
  type TransferType,
} from '@/api/transfer'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const TRANSFER_TYPE_OPTIONS: Array<{ label: string; value: TransferType }> = [
  { label: '仓库 → 仓库', value: 'warehouse' },
  { label: '库位 → 库位', value: 'bin' },
]

const TRANSFER_TYPE_LABEL: Record<TransferType, string> = {
  warehouse: '仓库 → 仓库',
  bin: '库位 → 库位',
}

const STATUS_OPTIONS: Array<{ label: string; value: TransferStatus }> = [
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
 * draft/pending_review/completed/cancelled 命中 types/status.ts 注册表，
 * 其余 key 由 TRANSFER_STATUS_TAG 的 label/semantic 兜底；
 * 后端返回未知值时兜底中性灰 + 原始文案，不崩溃。
 */
function TransferStatusTag({ status }: { status: TransferStatus }) {
  const meta = TRANSFER_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

const COLUMNS: ColumnsType<TransferItem> = [
  { title: '调拨单号', dataIndex: 'transferNo', width: 150, fixed: 'left' },
  {
    title: '类型',
    dataIndex: 'transferType',
    width: 120,
    render: (v: TransferType) => TRANSFER_TYPE_LABEL[v] ?? v,
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
  { title: '批次号', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
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
    render: (v: TransferStatus) => <TransferStatusTag status={v} />,
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
 * 调拨单列表（frontend.md §8.1 仓库中心模块；菜单 /transfers，config/menu.tsx:78）。
 * GET /api/transfers 为前端先行契约：M1 后端未交付调拨单域（backend-m1-plan.md §13），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 与 /inventory/transfers（views/inventory/TransferPage.tsx，库存转移作业）语义区分：
 * 本页是调拨单据流——两维度（仓库→仓库 / 库位→库位）、7 态状态机
 * （草稿→待审核→待出库→调拨中→待入库→已完成，任一环节可已取消）、TR- 单号
 * （business-flow.md §10.1、§13.1），源仓减少、目标仓增加，两端均生成库存流水。
 */
export default function TransferListPage() {
  const [params, setParams] = useState<TransferQuery>({})
  const list = usePagedList<TransferItem, TransferQuery>({
    queryKey: ['transfer', 'orders'],
    fetch: (q) => transferApi.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as TransferQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="调拨"
        subtitle="调拨单：仓库→仓库 / 库位→库位，两端均生成库存流水（business-flow.md §10.1）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '调拨单号 / SKU' },
            { name: 'transferType', label: '调拨维度', control: 'select', options: TRANSFER_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<TransferItem>
          storageKey="transfer-orders"
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
          scrollX={1740}
        />
      </Card>
    </div>
  )
}
