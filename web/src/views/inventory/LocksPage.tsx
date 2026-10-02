import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type InventoryLockItem,
  type InventoryLockQuery,
  type InventoryLockStatus,
  type InventoryLockType,
} from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const LOCK_TYPE_OPTIONS: Array<{ label: string; value: InventoryLockType }> = [
  { label: '订单占用', value: 'ORDER_HOLD' },
  { label: '盘点锁定', value: 'COUNT_FREEZE' },
  { label: '质检冻结', value: 'QC_FREEZE' },
  { label: '人工冻结', value: 'MANUAL_FREEZE' },
  { label: '异常冻结', value: 'EXCEPTION_FREEZE' },
]

const LOCK_STATUS_OPTIONS: Array<{ label: string; value: InventoryLockStatus }> = [
  { label: '生效中', value: 'ACTIVE' },
  { label: '已释放', value: 'RELEASED' },
  { label: '已消耗', value: 'CONSUMED' },
]

/** 锁定类型文案（inventory-rules.md §4，值域对齐 db/migrations/000005 CHECK 约束） */
const LOCK_TYPE_LABEL: Record<InventoryLockType, string> = {
  ORDER_HOLD: '订单占用',
  COUNT_FREEZE: '盘点锁定',
  QC_FREEZE: '质检冻结',
  MANUAL_FREEZE: '人工冻结',
  EXCEPTION_FREEZE: '异常冻结',
}

/**
 * 锁定状态 → SfStatusTag：key 对齐 types/status.ts 注册表键名风格，
 * 注册表尚未收录时由 label/semantic 兜底（颜色仍由 SfStatusTag 统一映射）。
 */
const LOCK_STATUS_TAG: Record<InventoryLockStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  ACTIVE: { key: 'active', label: '生效中', semantic: 'processing' },
  RELEASED: { key: 'released', label: '已释放', semantic: 'neutral' },
  CONSUMED: { key: 'consumed', label: '已消耗', semantic: 'success' },
}

function LockStatusTag({ status }: { status: InventoryLockStatus }) {
  const meta = LOCK_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

const COLUMNS: ColumnsType<InventoryLockItem> = [
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130, fixed: 'left' },
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
  {
    title: '锁定类型',
    dataIndex: 'lockType',
    width: 100,
    render: (v: InventoryLockType) => LOCK_TYPE_LABEL[v] ?? v,
  },
  { title: '来源类型', dataIndex: 'sourceType', width: 110 },
  { title: '来源单号', dataIndex: 'sourceNo', width: 160 },
  {
    title: '锁定数量',
    dataIndex: 'qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: InventoryLockStatus) => <LockStatusTag status={v} />,
  },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  { title: '操作人', dataIndex: 'createdByName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '锁定时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '释放时间',
    dataIndex: 'releasedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 库存锁定（frontend.md §10.1；锁定规则见 inventory-rules.md §4：释放须由明确业务动作触发并生成流水） */
export default function LocksPage() {
  const [params, setParams] = useState<InventoryLockQuery>({})
  const list = usePagedList<InventoryLockItem, InventoryLockQuery>({
    queryKey: ['inventory', 'locks'],
    fetch: (q) => inventoryApi.locks(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as InventoryLockQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存锁定"
        subtitle="订单占用 / 盘点锁定 / 质检 / 人工 / 异常冻结明细"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU / 商品名称 / 来源单号' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
            { name: 'lockType', label: '锁定类型', control: 'select', options: LOCK_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: LOCK_STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<InventoryLockItem>
          storageKey="inventory-locks"
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
          emptyText="当前筛选条件下没有库存锁定记录"
          scrollX={1790}
        />
      </Card>
    </div>
  )
}
