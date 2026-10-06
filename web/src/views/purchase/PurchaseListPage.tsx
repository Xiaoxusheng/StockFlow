import { useMemo, useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router'
import {
  PURCHASE_CREATE_PERMISSION,
  purchaseApi,
  type PurchaseOrder,
  type PurchaseQuery,
  type PurchaseStatus,
} from '@/api/purchase'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import {
  buildSupplierMaps,
  buildWarehouseMaps,
  fetchSupplierOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatMoney } from '@/utils/format'

const { Text } = Typography

/**
 * 采购订单状态 → SfStatusTag（internal/purchase/models.go:17-23 七态）。
 * types/status.ts 注册表已收录 draft/pending_approval/approved/completed/cancelled
 * （文案/语义一致，SfStatusTag 以注册表优先）；PARTIAL_RECEIVED/RECEIVED_ALL 为
 * 采购语境专有键未注册，经 SfStatusTag 的 label/semantic 兜底；后端返回未知值时
 * 映射缺失，SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
const PO_STATUS_TAG: Record<PurchaseStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  PARTIAL_RECEIVED: { label: '部分到货', semantic: 'processing' },
  RECEIVED_ALL: { label: '到货完成', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

const STATUS_OPTIONS = Object.entries(PO_STATUS_TAG).map(([value, tag]) => ({
  label: tag.label,
  value,
}))

function renderStatus(status: PurchaseStatus) {
  const tag = PO_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={tag?.label} semantic={tag?.semantic} />
}

/** 采购订单列表（/purchases；GET /api/purchases，出参为 PurchaseOrder 裸模型
 * snake_case，internal/purchase/models.go:120-135——供应商/仓库为裸 ID，
 * 经基础资料 options 本地映射补充，映射失败降级为 ID，不造假数据）。
 * 新建入口跳 /purchases/new（PurchaseOrderFormPage，路由见 sharedChanges），
 * 仅持 purchase:purchase:create 权限可见。 */
export default function PurchaseListPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, PURCHASE_CREATE_PERMISSION)

  // 供应商/仓库 options 一次取全（api/options.ts 头注释：映射失败由调用方降级，不阻塞列表）
  const supplierOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'suppliers'],
    queryFn: fetchSupplierOptions,
  })
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const supplierNames = useMemo(
    () => buildSupplierMaps(supplierOptionsQuery.data ?? []).name,
    [supplierOptionsQuery.data],
  )
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptionsQuery.data ?? []).name,
    [warehouseOptionsQuery.data],
  )

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<PurchaseOrder, PurchaseQuery>({
    queryKey: ['purchase', 'orders'],
    fetch: (q) => purchaseApi.list(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  const columns: ColumnsType<PurchaseOrder> = [
    { title: '采购单号', dataIndex: 'po_no', width: 180, fixed: 'left' },
    {
      title: '供应商',
      dataIndex: 'supplier_id',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: PurchaseOrder) => {
        const name = supplierNames.get(String(record.supplier_id)) ?? `供应商 #${record.supplier_id}`
        return (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 130,
      ellipsis: true,
      render: (_: unknown, record: PurchaseOrder) =>
        warehouseNames.get(String(record.warehouse_id)) ?? `仓库 #${record.warehouse_id}`,
    },
    {
      title: '金额',
      dataIndex: 'total_amount',
      width: 130,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: PurchaseStatus) => renderStatus(v),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: PurchaseOrder) => (
        <Button type="link" size="small" onClick={() => navigate(`/purchases/${record.id}`)}>
          详情
        </Button>
      ),
    },
  ]

  // 搜索参数对齐 internal/purchase/handler.go:87-115：keyword/status/supplier_id/warehouse_id
  // （keyword 后端按 po_no ILIKE 模糊，repository.go:292-294；下拉选项失败时呈无选项空态，不造假数据）
  const searchFields: SearchField[] = [
    { name: 'keyword', label: '关键词', control: 'input', placeholder: '采购单号' },
    { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
    {
      name: 'supplier_id',
      label: '供应商',
      control: 'select',
      options: (supplierOptionsQuery.data ?? []).map((s) => ({
        label: `${s.name}（${s.code}）`,
        value: String(s.id),
      })),
    },
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
        title="采购订单"
        subtitle="草稿 → 待审核 → 已审核 → 到货 → 完成"
        extra={
          canCreate ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/purchases/new')}
            >
              新建采购订单
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={searchFields}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <SfViewBar
              pageKey="purchase.order"
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
        <SfTable<PurchaseOrder>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="purchase-orders"
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
          emptyText="当前筛选条件下没有采购订单"
          emptyAction={
            canCreate ? (
              <Button type="primary" onClick={() => navigate('/purchases/new')}>
                新建采购订单
              </Button>
            ) : undefined
          }
          scrollX={970}
        />
      </Card>
    </div>
  )
}
