import { useMemo, useState } from 'react'
import { Button, Card } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  PURCHASE_RETURN_CREATE_PERMISSION,
  purchaseApi,
  type PurchaseReturnOrder,
  type PurchaseReturnQuery,
  type PurchaseReturnStatus,
} from '@/api/purchase'
import { toStatusKey } from '@/api/masterdata'
import { buildWarehouseMaps, fetchWarehouseOptions } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { PurchaseReturnCreateDrawer } from './PurchaseReturnCreateDrawer'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime } from '@/utils/format'

/**
 * 采购退货单状态 → SfStatusTag（internal/returns/models.go:27-36 八态，经
 * api/purchase.ts PurchaseReturnStatus 回对）。types/status.ts 注册表已收录
 * draft/pending_approval/approved/receiving/shipped/completed/cancelled（文案/语义
 * 一致，SfStatusTag 以注册表优先）；IN_QC 为退货语境专有键未注册，经 SfStatusTag
 * 的 label/semantic 兜底；后端返回未知值时中性灰 + 原始文案，不崩溃。
 */
const RETURN_STATUS_TAG: Record<PurchaseReturnStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  RECEIVING: { label: '收货中', semantic: 'processing' },
  IN_QC: { label: '质检中', semantic: 'processing' },
  SHIPPED: { label: '已发货', semantic: 'processing' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

const STATUS_OPTIONS = Object.entries(RETURN_STATUS_TAG).map(([value, tag]) => ({
  label: tag.label,
  value,
}))

function renderStatus(status: PurchaseReturnStatus) {
  const tag = RETURN_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={tag?.label} semantic={tag?.semantic} />
}

/** 采购退货列表（/purchases/returns；GET /api/purchase-returns，退货域承载
 * internal/returns/handler.go:315-334——列表出参为 ReturnOrderView snake_case
 * （service_sales.go:124-136），type 后端固定 PURCHASE。warehouse_id 为裸 ID，
 * 经仓库 options 本地映射补充，映射失败降级为 ID，不造假数据）。
 * 创建入口为 PurchaseReturnCreateDrawer（POST /api/purchase-returns，
 * 仅持 returns:purchasereturn:create 权限可见）。 */
export default function PurchaseReturnListPage() {
  const [params, setParams] = useState<PurchaseReturnQuery>({})
  const [createOpen, setCreateOpen] = useState(false)
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, PURCHASE_RETURN_CREATE_PERMISSION)

  // 仓库 options 一次取全（api/options.ts 头注释：映射失败由调用方降级，不阻塞列表）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptionsQuery.data ?? []).name,
    [warehouseOptionsQuery.data],
  )

  const list = usePagedList<PurchaseReturnOrder, PurchaseReturnQuery>({
    queryKey: ['purchase', 'returns'],
    fetch: (q) => purchaseApi.returns.list(q),
    params,
  })

  const columns: ColumnsType<PurchaseReturnOrder> = [
    { title: '退货单号', dataIndex: 'return_no', width: 180, fixed: 'left' },
    { title: '来源单号', dataIndex: 'source_no', width: 180, render: (v: string) => v || '-' },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 130,
      ellipsis: true,
      render: (_: unknown, record: PurchaseReturnOrder) =>
        warehouseNames.get(String(record.warehouse_id)) ?? `仓库 #${record.warehouse_id}`,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: PurchaseReturnStatus) => renderStatus(v),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]

  // 搜索参数对齐 internal/returns/handler.go:315-334：status/source_no/warehouse_id
  // （type 由后端固定 PURCHASE，前端不传）
  const searchFields: SearchField[] = [
    { name: 'source_no', label: '来源单号', control: 'input', placeholder: '来源采购单号' },
    { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
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
    setParams(values as PurchaseReturnQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="采购退货"
        subtitle="退货申请 → 审核 → 退货出库 → 供应商"
        extra={
          canCreate ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreateOpen(true)}
            >
              新建采购退货
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm fields={searchFields} onSearch={handleSearch} />
        <SfTable<PurchaseReturnOrder>
          storageKey="purchase-returns"
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
          emptyText="当前筛选条件下没有采购退货单"
          scrollX={760}
        />
      </Card>
      <PurchaseReturnCreateDrawer
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={() => {
          setCreateOpen(false)
          queryClient.invalidateQueries({ queryKey: ['purchase', 'returns'] })
          list.refetch()
        }}
      />
    </div>
  )
}
