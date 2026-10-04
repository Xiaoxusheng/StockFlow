import { useMemo, useState } from 'react'
import { Card } from 'antd'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { purchaseApi, type Receipt, type ReceiptQuery } from '@/api/purchase'
import { buildWarehouseMaps, fetchWarehouseOptions } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDate, formatDateTime } from '@/utils/format'

/** 收货列表（/purchases/receipts；GET /api/receipts，出参为 Receipt 裸模型 snake_case，
 * internal/purchase/models.go:207-221——收货为事件型一次性生效（幂等键防重），
 * 无独立状态机，状态语义由入库单承载，故列表无状态列；后端 Receipt 亦无采购单号/
 * 供应商/应收已收数量字段，相应列不展示。warehouse_id 为裸 ID，经仓库 options
 * 本地映射补充，映射失败降级为 ID，不造假数据） */
export default function ReceiptListPage() {
  const [params, setParams] = useState<ReceiptQuery>({})

  // 仓库 options 一次取全（api/options.ts 头注释：映射失败由调用方降级，不阻塞列表）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptionsQuery.data ?? []).name,
    [warehouseOptionsQuery.data],
  )

  const list = usePagedList<Receipt, ReceiptQuery>({
    queryKey: ['purchase', 'receipts'],
    fetch: (q) => purchaseApi.receipts.list(q),
    params,
  })

  const columns: ColumnsType<Receipt> = [
    { title: '收货单号', dataIndex: 'receipt_no', width: 180, fixed: 'left' },
    { title: '入库单号', dataIndex: 'inbound_no', width: 180, render: (v: string) => v || '-' },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 130,
      ellipsis: true,
      render: (_: unknown, record: Receipt) =>
        warehouseNames.get(String(record.warehouse_id)) ?? `仓库 #${record.warehouse_id}`,
    },
    { title: '批次号', dataIndex: 'batch_no', width: 140, render: (v: string) => v || '-' },
    {
      title: '效期',
      dataIndex: 'expiry_date',
      width: 110,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDate(v)}</span>,
    },
    { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '收货时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]

  // 搜索参数对齐 internal/purchase/handler.go:320-340：receipt_no/inbound_no/warehouse_id
  // （单号后端为精确匹配，repository.go:494-499）
  const searchFields: SearchField[] = [
    { name: 'receipt_no', label: '收货单号', control: 'input', placeholder: '收货单号（精确）' },
    { name: 'inbound_no', label: '入库单号', control: 'input', placeholder: '入库单号（精确）' },
    {
      name: 'warehouse_id',
      label: '仓库',
      control: 'select',
      options: (warehouseOptionsQuery.data ?? []).map((w) => ({
        label: `${w.name}（${w.code}）`,
        value: String(w.id),
      })),
    },
  ]

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ReceiptQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="收货"
        subtitle="到货 → 收货 → 质检 → 上架 → 入库完成"
      />
      <Card size="small">
        <SfSearchForm fields={searchFields} onSearch={handleSearch} />
        <SfTable<Receipt>
          storageKey="purchase-receipts"
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
          emptyText="当前筛选条件下没有收货记录"
          scrollX={1010}
        />
      </Card>
    </div>
  )
}
