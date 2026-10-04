import { useMemo, useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import {
  TRANSFER_STATUS_TAG,
  transferApi,
  type TransferOrder,
  type TransferQuery,
  type TransferStatus,
  type TransferType,
} from '@/api/transfer'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

/** 调拨类型文案（models.go:153-154：WAREHOUSE 跨仓 / BIN 库位间，库位间必须同仓） */
const TRANSFER_TYPE_LABEL: Record<TransferType, string> = {
  WAREHOUSE: '仓库 → 仓库',
  BIN: '库位 → 库位',
}

const TRANSFER_TYPE_OPTIONS = (Object.entries(TRANSFER_TYPE_LABEL) as Array<[TransferType, string]>).map(
  ([value, label]) => ({ label, value }),
)

/** 状态筛选选项与列内标签同源（api/transfer.ts TRANSFER_STATUS_TAG 七态唯一映射） */
const STATUS_OPTIONS = (
  Object.entries(TRANSFER_STATUS_TAG) as Array<[TransferStatus, (typeof TRANSFER_STATUS_TAG)[TransferStatus]]>
).map(([value, meta]) => ({ label: meta.label, value }))

/**
 * 调拨单状态 → SfStatusTag：七态值域 internal/stockops/models.go:134-142（迁移 000009
 * CHECK 同源）。注册表（types/status.ts）以小写键收录同语义键；approved 注册表通用文案
 * 为「已审核」，调拨语境「待出库」（business-flow.md §10.1）按注册表声明的覆盖路径，
 * 经 TRANSFER_STATUS_TAG 的 label/semantic 生效——大写原始值不命中注册表
 * （resolveStatus 精确匹配），label/semantic 兜底接管；后端返回未知值时同样兜底
 * 中性灰 + 原始文案，不崩溃。
 */
function TransferStatusTag({ status }: { status: TransferStatus }) {
  const meta = TRANSFER_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/** 仓库名映射失败降级为 ID（api/options.ts：不造假数据） */
function warehouseNameOf(names: Map<string, string>, id: TransferOrder['from_warehouse_id']): string {
  return names.get(idKey(id)) ?? String(id)
}

/**
 * 调拨单列表（/transfers，frontend.md §4 仓库中心菜单；后端 M2 已交付：
 * internal/stockops/routes.go:46-56）。GET /api/transfers 为单据级行（TransferOrderView，
 * handler.go:27-42，无 SKU/数量明细列），行级明细走 GET /api/transfers/{id}，
 * 本页仅列表不做详情跳转。搜索参数 type/status/transfer_no（handler.go:294-325 实测，
 * transfer_no 为精确匹配）。与 /inventory/transfers（views/inventory/TransferPage.tsx，
 * 库存转移作业）语义区分：本页是调拨单据流——两维度（仓库→仓库 / 库位→库位）、
 * 七态状态机（草稿→待审核→待出库→调拨中→待入库→已完成，任一环节可已取消）、
 * TR- 单号（business-flow.md §10.1、§13.1），源仓减少、目标仓增加，两端均生成库存流水。
 */
export default function TransferListPage() {
  const [params, setParams] = useState<TransferQuery>({})
  const list = usePagedList<TransferOrder, TransferQuery>({
    queryKey: ['transfer', 'orders'],
    fetch: (q) => transferApi.list(q),
    params,
  })

  // 仓库 id → 名称映射（后端视图不联表下发仓库名，api/options.ts 一次取全后本地映射）
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as TransferQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<TransferOrder> = [
    { title: '调拨单号', dataIndex: 'transfer_no', width: 170, fixed: 'left' },
    {
      title: '类型',
      dataIndex: 'type',
      width: 120,
      render: (v: TransferType) => TRANSFER_TYPE_LABEL[v] ?? v,
    },
    {
      title: '源仓库',
      dataIndex: 'from_warehouse_id',
      width: 140,
      ellipsis: true,
      render: (v: TransferOrder['from_warehouse_id']) => warehouseNameOf(warehouseNames, v),
    },
    {
      title: '目标仓库',
      dataIndex: 'to_warehouse_id',
      width: 140,
      ellipsis: true,
      render: (v: TransferOrder['to_warehouse_id']) => warehouseNameOf(warehouseNames, v),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: TransferStatus) => <TransferStatusTag status={v} />,
    },
    {
      title: '出库时间',
      dataIndex: 'outbound_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '入库时间',
      dataIndex: 'received_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="调拨"
        subtitle="调拨单：仓库→仓库 / 库位→库位，两端均生成库存流水（business-flow.md §10.1）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'transfer_no', label: '调拨单号', control: 'input', placeholder: '调拨单号（精确匹配）' },
            { name: 'type', label: '调拨维度', control: 'select', options: TRANSFER_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<TransferOrder>
          storageKey="transfer-orders"
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
          emptyText="当前筛选条件下没有调拨单"
          scrollX={1150}
        />
      </Card>
    </div>
  )
}
