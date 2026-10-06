import { useMemo, useState } from 'react'
import { Button, Card } from 'antd'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { purchaseApi, type Receipt, type ReceiptQuery } from '@/api/purchase'
import { buildWarehouseMaps, fetchWarehouseOptions } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'

/** 收货列表（/purchases/receipts；GET /api/receipts，出参为 Receipt 裸模型 snake_case，
 * internal/purchase/models.go:207-221——收货为事件型一次性生效（幂等键防重），
 * 无独立状态机，状态语义由入库单承载，故列表无状态列；后端 Receipt 亦无采购单号/
 * 供应商/应收已收数量字段，相应列不展示。warehouse_id 为裸 ID，经仓库 options
 * 本地映射补充，映射失败降级为 ID，不造假数据） */
export default function ReceiptListPage() {
  // 仓库 options 一次取全（api/options.ts 头注释：映射失败由调用方降级，不阻塞列表）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptionsQuery.data ?? []).name,
    [warehouseOptionsQuery.data],
  )

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<Receipt, ReceiptQuery>({
    queryKey: ['purchase', 'receipts'],
    fetch: (q) => purchaseApi.receipts.list(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  /** 空态 CTA（frontend.md §31 #8）：仅有生效筛选时提供「清空筛选」（applyFilters({}) 写空
   * URL 并回第 1 页——真实动作）；收货由 Pad 收货作业产生，PC 无创建入口，不放假按钮 */
  const hasFilters = Object.keys(list.params).length > 0

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
      render: (v: string | null) => <DateCell value={v} withTime={false} />,
    },
    { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '收货时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
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

  return (
    <div className="sf-page">
      <SfPageHeader
        title="收货"
        subtitle="到货 → 收货 → 质检 → 上架 → 入库完成"
        /* 导出当前视图：datax 十六导出模块（internal/datax/registry.go:21-38）无收货行源，
           不放假导出入口；待后端补 RECEIPT 行源后接 SfExportButton（followup） */
      />
      <Card size="small">
        <SfSearchForm
          fields={searchFields}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <SfViewBar
              pageKey="purchase.receipt"
              mode="url"
              paged={{ applyFilters: list.applyFilters, onPageChange: list.onPageChange }}
              appliedFilters={list.params as unknown as Record<string, unknown>}
              currentFilters={list.formValues}
              currentPageSize={list.pagination.pageSize}
              currentHiddenColumns={hiddenColumns}
              onHiddenColumnsChange={setHiddenColumns}
            />
          }
        />
        <SfTable<Receipt>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
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
          emptyAction={
            hasFilters ? (
              <Button type="link" size="small" onClick={() => list.applyFilters({})}>
                清空筛选
              </Button>
            ) : undefined
          }
          scrollX={1010}
        />
      </Card>
    </div>
  )
}
