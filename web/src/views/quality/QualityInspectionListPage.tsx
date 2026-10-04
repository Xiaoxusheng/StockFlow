import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, Card, message, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  INSPECTION_METHOD_LABEL,
  QUALITY_CREATE_PERMISSION,
  QUALITY_RESULT_TAG_META,
  qualityApi,
  type InspectionMethod,
  type QualityInspectionItem,
  type QualityInspectionQuery,
  type QualityOrderStatus,
  type QualityResult,
  type QualitySourceType,
} from '@/api/quality'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfToolbar } from '@/components/table/SfToolbar'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'
import QualityCreateModal from './QualityCreateModal'

const { Text } = Typography

/** 来源类型文案（internal/purchase/models.go:52-53：INBOUND 入库质检 / RETURN 退货质检） */
const SOURCE_TYPE_LABEL: Record<QualitySourceType, string> = {
  INBOUND: '入库质检',
  RETURN: '退货质检',
}

const SOURCE_TYPE_OPTIONS = (
  Object.entries(SOURCE_TYPE_LABEL) as Array<[QualitySourceType, string]>
).map(([value, label]) => ({ label, value }))

/**
 * 质检单状态 → SfStatusTag：三态值域 internal/purchase/models.go:36-38（PENDING→INSPECTING
 * →COMPLETED，COMPLETED 即处理结果落定并触发库存映射）。大写原始值不命中注册表
 * （types/status.ts resolveStatus 精确匹配小写键），经 label/semantic 显式指定，语义与
 * 入库单质检段（status.ts 待质检/质检中/已质检）同源；未知值由 SfStatusTag 兜底
 * 中性灰 + 原始文案，不崩溃。
 */
const QC_STATUS_TAG_META: Record<QualityOrderStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待质检', semantic: 'pending' },
  INSPECTING: { label: '质检中', semantic: 'processing' },
  COMPLETED: { label: '已质检', semantic: 'success' },
}

const STATUS_OPTIONS = (
  Object.entries(QC_STATUS_TAG_META) as Array<
    [QualityOrderStatus, (typeof QC_STATUS_TAG_META)[QualityOrderStatus]]
  >
).map(([value, meta]) => ({ label: meta.label, value }))

function StatusTag({ value }: { value: QualityOrderStatus }) {
  const meta = QC_STATUS_TAG_META[value]
  // 显式 label + semantic、不传 status：SfStatusTag 文案优先级 meta?.label ?? label ?? status，
  // status 一旦被注册表收录，label 会被注册表文案覆盖（盘点「待盘」被「待处理」覆盖同款教训）
  return <SfStatusTag label={meta?.label ?? value} semantic={meta?.semantic ?? 'neutral'} />
}

/**
 * 检验结果九值 → SfStatusTag：中文值域（business-flow.md §4.3，迁移 chk_quality_orders_result
 * 同源）不与 types/status.ts 小写注册表相交，经 QUALITY_RESULT_TAG_META 的 label/semantic
 * 显式指定；后端返回未知值时兜底中性灰 + 原始文案，不崩溃。
 */
function ResultTag({ value }: { value: string }) {
  const meta = QUALITY_RESULT_TAG_META[value as QualityResult]
  // 中文九值域不命中注册表，仍走显式 label + semantic（不传 status，同 StatusTag 口径）
  return <SfStatusTag label={meta?.label ?? value} semantic={meta?.semantic ?? 'neutral'} />
}

function renderQty(value: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/** 仓库名映射失败降级为 ID（api/options.ts：不造假数据） */
function warehouseNameOf(names: Map<string, string>, id: QualityInspectionItem['warehouse_id']): string {
  return names.get(idKey(id)) ?? String(id)
}

/**
 * 质检单列表（菜单 /quality/inspections，config/menu.tsx:105；GET /api/quality 后端 M2 已交付：
 * internal/purchase/handler.go:381-401 → repo.ListQCs repository.go:541-557）。
 * 出参为 QualityOrder GORM 裸模型（internal/purchase/models.go:273-292，snake_case）：
 * 单头无 sku/批次维度（质检明细行级才有，models.go:296-310，经 GET /api/quality/{id} 承载），
 * 故本页不放 SKU/批次列；检验/合格/不合格数量为单头汇总（qty_inspected/qty_qualified/
 * qty_defective，§4.2 质检记录字段）。搜索参数 keyword/status/source_type/source_no/
 * warehouse_id（handleQCList 实测：keyword 对 qc_no/source_no 模糊，source_no 精确）。
 * 工具栏提供「手动建单」（POST /api/quality 已注册，purchase.go:65；入库质检补录场景，
 * 按钮经 canAccess(purchase:quality:create) 把关）。
 * NonconformingListPage/QualityTracePage 端点未立项，保持各自现状（错误态），不在本页范围。
 */
export default function QualityInspectionListPage() {
  const [params, setParams] = useState<QualityInspectionQuery>({})
  const [createOpen, setCreateOpen] = useState(false)
  const user = useAuthStore((s) => s.user)
  const queryClient = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const canCreate = canAccess(user, QUALITY_CREATE_PERMISSION)
  const list = usePagedList<QualityInspectionItem, QualityInspectionQuery>({
    queryKey: ['quality', 'orders'],
    fetch: (q) => qualityApi.inspections(q),
    params,
  })

  const handleCreated = (order: QualityInspectionItem) => {
    void queryClient.invalidateQueries({ queryKey: ['quality', 'orders'] })
    messageApi.success(`已创建质检单 ${order.qc_no}，列表已刷新`)
  }

  // 仓库 id → 名称映射（单头只下发 warehouse_id，api/options.ts 一次取全后本地映射）
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )
  const warehouseOptions = useMemo(
    () =>
      (warehousesQuery.data ?? []).map((w) => ({ label: `${w.code} ${w.name}`, value: String(w.id) })),
    [warehousesQuery.data],
  )

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as QualityInspectionQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<QualityInspectionItem> = [
    { title: '质检单号', dataIndex: 'qc_no', width: 160, fixed: 'left' },
    {
      title: '来源类型',
      dataIndex: 'source_type',
      width: 100,
      render: (v: QualitySourceType) => SOURCE_TYPE_LABEL[v] ?? v,
    },
    { title: '来源单号', dataIndex: 'source_no', width: 160 },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 130,
      ellipsis: true,
      render: (v: QualityInspectionItem['warehouse_id']) => warehouseNameOf(warehouseNames, v),
    },
    {
      title: '检验方式',
      dataIndex: 'inspection_type',
      width: 90,
      render: (v: InspectionMethod) => INSPECTION_METHOD_LABEL[v] ?? v,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: QualityOrderStatus) => <StatusTag value={v} />,
    },
    { title: '检验数量', dataIndex: 'qty_inspected', width: 100, align: 'right', render: renderQty },
    { title: '合格数量', dataIndex: 'qty_qualified', width: 100, align: 'right', render: renderQty },
    { title: '不合格数量', dataIndex: 'qty_defective', width: 110, align: 'right', render: renderQty },
    {
      title: '检验结果',
      dataIndex: 'result',
      width: 110,
      render: (v: string) => <ResultTag value={v} />,
    },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 160,
      ellipsis: true,
      render: (v?: string) =>
        v ? (
          <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>
            {v}
          </Text>
        ) : (
          '-'
        ),
    },
    { title: '检验人', dataIndex: 'inspector_name', width: 100, render: (v?: string) => v || '-' },
    {
      title: '检验时间',
      dataIndex: 'inspected_at',
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
      {contextHolder}
      <SfPageHeader
        title="质检"
        subtitle="质检单：免检 / 抽检 / 全检，处理结果九值（business-flow.md §4）"
      />
      <Card size="small">
        {canCreate && (
          <SfToolbar>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              手动建单
            </Button>
          </SfToolbar>
        )}
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '质检单号 / 来源单号' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'source_type', label: '来源类型', control: 'select', options: SOURCE_TYPE_OPTIONS },
            { name: 'source_no', label: '来源单号', control: 'input', placeholder: '来源单号（精确匹配）' },
            { name: 'warehouse_id', label: '仓库', control: 'select', options: warehouseOptions },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<QualityInspectionItem>
          storageKey="quality-orders"
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
          emptyText="当前筛选条件下没有质检单"
          scrollX={1740}
        />
      </Card>
      <QualityCreateModal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />
    </div>
  )
}
