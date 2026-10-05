import { useMemo, useState } from 'react'
import { Drawer } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import type { TransferInTransitQuery, TransferInTransitRow } from '@/api/transfer'
import { transferApi } from '@/api/transfer'
import { buildSkuMaps, buildIdMap, fetchBatchOptions, fetchSkuOptions, fetchWarehouseOptions } from '@/api/options'
import { SfError } from '@/components/common/SfError'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfLoading } from '@/components/common/SfLoading'
import { usePagedList } from '@/hooks/usePagedList'
import { formatNumber } from '@/utils/format'

export interface TransferInTransitDrawerProps {
  open: boolean
  onClose: () => void
}

/**
 * 在途汇总抽屉（GET /api/transfers/in-transit，internal/stockops/routes.go:48 /
 * handler.go:367-385，stockops:transfer:list 权限）：按 SKU+批次聚合跨调拨单在途量，
 * out_transit=自源仓已出未收、in_transit=向目标仓在途——数量由后端计算下发，
 * 前端禁止算库存（in_transit_qty 同口径）。
 */
export default function TransferInTransitDrawer({ open, onClose }: TransferInTransitDrawerProps) {
  const [params, setParams] = useState<TransferInTransitQuery>({})

  const list = usePagedList<TransferInTransitRow, TransferInTransitQuery>({
    queryKey: ['transfer', 'in-transit'],
    fetch: (q) => transferApi.inTransitList(q),
    params,
    defaultPageSize: 10,
  })

  // 编码映射（行出参仅 sku_id/batch_id 裸 ID，基础资料 options 本地映射，失败降级为 #id）
  const skuOptions = useQuery({ queryKey: ['transfer-intransit', 'sku-options'], queryFn: fetchSkuOptions, enabled: open })
  const batchOptions = useQuery({ queryKey: ['transfer-intransit', 'batch-options'], queryFn: () => fetchBatchOptions(), enabled: open })
  const warehouseOptions = useQuery({
    queryKey: ['transfer-intransit', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
    enabled: open,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const batchNoMap = useMemo(
    () => buildIdMap(batchOptions.data ?? [], (b) => b.id, (b) => b.batch_no),
    [batchOptions.data],
  )
  const warehouseNameMap = useMemo(
    () => buildIdMap(warehouseOptions.data ?? [], (w) => w.id, (w) => w.name),
    [warehouseOptions.data],
  )

  const columns: ColumnsType<TransferInTransitRow> = [
    {
      title: 'SKU',
      dataIndex: 'sku_id',
      width: 200,
      render: (v: number) => skuMaps.code.get(String(v)) ?? `#${String(v)}`,
    },
    {
      title: '批次',
      dataIndex: 'batch_id',
      width: 140,
      render: (v: number) => (String(v) === '0' ? '非批次' : (batchNoMap.get(String(v)) ?? `#${String(v)}`)),
    },
    {
      title: '已出未收（源仓视角）',
      dataIndex: 'out_transit',
      width: 170,
      align: 'right',
      render: (v: number) => formatNumber(v),
    },
    {
      title: '在途（目标仓视角）',
      dataIndex: 'in_transit',
      width: 160,
      align: 'right',
      render: (v: number) => formatNumber(v),
    },
  ]

  return (
    <Drawer title="调拨在途汇总（按 SKU + 批次聚合）" open={open} onClose={onClose} width={720} destroyOnHidden>
      {skuOptions.error || batchOptions.error ? (
        <SfError
          error={(skuOptions.error ?? batchOptions.error)!}
          description="基础资料 options 加载失败，SKU/批次将降级为 ID 展示"
          onRetry={() => {
            void skuOptions.refetch()
            void batchOptions.refetch()
          }}
        />
      ) : null}
      <SfSearchForm
        fields={[
          {
            name: 'warehouse_id',
            label: '仓库',
            control: 'select',
            options: (warehouseOptions.data ?? []).map((w) => ({ value: String(w.id), label: warehouseNameMap.get(String(w.id)) ?? w.name })),
          },
          {
            name: 'sku_id',
            label: 'SKU',
            control: 'select',
            options: (skuOptions.data ?? []).map((s) => ({ value: String(s.id), label: s.code })),
          },
        ]}
        onSearch={(values) => {
          setParams(values as TransferInTransitQuery)
          list.resetToFirstPage()
        }}
      />
      {list.isPending ? (
        <SfLoading rows={8} />
      ) : list.error ? (
        <SfError
          error={list.error}
          description="在途汇总（GET /api/transfers/in-transit）加载失败"
          onRetry={() => void list.refetch()}
        />
      ) : (
        <SfTable<TransferInTransitRow>
          variant="nested"
          rowKey={(row) => `${String(row.sku_id)}-${String(row.batch_id)}`}
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          pagination={{
            current: list.pagination.current,
            pageSize: list.pagination.pageSize,
          }}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有在途数据"
          scroll={{ x: 670 }}
        />
      )}
    </Drawer>
  )
}
