import { useState } from 'react'
import { Breadcrumb, Card, Descriptions, Flex, Skeleton, Tabs, Typography } from 'antd'
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  RightOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type BatchItem,
  type BatchQuery,
  type InventoryChangeType,
  type InventoryLockItem,
  type InventoryLockQuery,
  type InventoryLockStatus,
  type InventoryLockType,
  type LedgerItem,
  type LedgerQuery,
  type SerialItem,
  type SerialQuery,
  type SerialStatus,
  type StockDistributionNode,
  type TraceItem,
  type TraceQuery,
} from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDate, formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

// ---------- 状态 / 文案映射（与列表页 LocksPage / SerialListPage / TracePage 保持一致；
// 映射无法提到公共层（本组文件范围受限），后端冻结后随注册表 types/status.ts 收敛） ----------

/** 序列号状态 → SfStatusTag（值域对齐 db/migrations/000005 serial_numbers CHECK 约束） */
const SERIAL_STATUS_TAG: Record<SerialStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  IN_STOCK: { key: 'in_stock', label: '在库', semantic: 'success' },
  LOCKED: { key: 'locked', label: '锁定', semantic: 'warning' },
  OUTBOUND: { key: 'outbound', label: '已出库', semantic: 'neutral' },
  RETURNED: { key: 'returned', label: '已退货', semantic: 'neutral' },
  FROZEN: { key: 'frozen', label: '冻结', semantic: 'danger' },
}

function SerialStatusTag({ status }: { status: SerialStatus }) {
  const meta = SERIAL_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 锁定类型文案（inventory-rules.md §4，值域对齐 db/migrations/000005 CHECK 约束） */
const LOCK_TYPE_LABEL: Record<InventoryLockType, string> = {
  ORDER_HOLD: '订单占用',
  COUNT_FREEZE: '盘点锁定',
  QC_FREEZE: '质检冻结',
  MANUAL_FREEZE: '人工冻结',
  EXCEPTION_FREEZE: '异常冻结',
}

/** 锁定状态 → SfStatusTag（值域对齐 db/migrations/000005 inventory_locks CHECK 约束） */
const LOCK_STATUS_TAG: Record<InventoryLockStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  ACTIVE: { key: 'active', label: '生效中', semantic: 'processing' },
  RELEASED: { key: 'released', label: '已释放', semantic: 'neutral' },
  CONSUMED: { key: 'consumed', label: '已消耗', semantic: 'success' },
}

function LockStatusTag({ status }: { status: InventoryLockStatus }) {
  const meta = LOCK_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 变更类型文案（值域对齐 db/migrations/000005 inventory_ledgers CHECK 约束） */
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

/** 库存状态列文案（inventory_ledgers status_from/status_to CHECK 值域） */
const STATE_LABEL: Record<string, string> = {
  available: '可用',
  locked: '锁定',
  frozen: '冻结',
  pending_inspect: '待检',
  defective: '不良',
}

function DirectionText({ direction }: { direction: LedgerItem['direction'] }) {
  return direction === 'in' ? (
    <Text style={{ color: 'var(--sf-success)' }}>
      <ArrowDownOutlined /> 入库
    </Text>
  ) : (
    <Text style={{ color: 'var(--sf-danger)' }}>
      <ArrowUpOutlined /> 出库
    </Text>
  )
}

// ---------- 页签一：库存分布（frontend.md §10.4 层级视图，支持点击下钻） ----------

type DistributionLevel = 'warehouse' | 'zone' | 'bin'

const LEVEL_LABEL: Record<DistributionLevel, string> = {
  warehouse: '仓库',
  zone: '库区',
  bin: '库位',
}

function nodeLevel(node: StockDistributionNode): DistributionLevel {
  if (node.binCode) return 'bin'
  if (node.zoneCode) return 'zone'
  return 'warehouse'
}

function nodeTitle(node: StockDistributionNode): string {
  return node.binCode ?? node.zoneCode ?? node.warehouseName ?? node.warehouseCode
}

function nodeKey(node: StockDistributionNode): string {
  return `${node.warehouseCode}/${node.zoneCode ?? ''}/${node.binCode ?? ''}`
}

/**
 * 层级下钻：整棵分布树来自 GET /api/inventory/{skuCode}/distribution 一次返回，
 * 点击带 children 的节点进入下一层（仓库 → 库区 → 库位），面包屑回退。
 */
function DistributionTab({ skuCode }: { skuCode: string }) {
  const distribution = useQuery({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'distribution'],
    queryFn: () => inventoryApi.stockDistribution(skuCode),
  })
  const [path, setPath] = useState<StockDistributionNode[]>([])

  if (distribution.isPending) {
    return <Skeleton active paragraph={{ rows: 5 }} />
  }
  if (distribution.error) {
    return (
      <SfError
        error={distribution.error}
        onRetry={distribution.refetch}
        description="库存分布接口 GET /api/inventory/{skuCode}/distribution 尚未交付（后端 M1 库存查询面未含层级分布），接口就绪后自动展示真实数据"
      />
    )
  }

  const roots = distribution.data ?? []
  if (roots.length === 0) {
    return <SfEmpty description="该 SKU 当前在所有仓库均无库存" />
  }

  const currentLevel = path.length === 0 ? roots : (path[path.length - 1].children ?? [])

  return (
    <Flex vertical gap={8}>
      {path.length > 0 && (
        <Breadcrumb
          items={[
            { title: <Typography.Link onClick={() => setPath([])}>仓库</Typography.Link> },
            ...path.map((node, index) => ({
              title:
                index === path.length - 1 ? (
                  nodeTitle(node)
                ) : (
                  <Typography.Link onClick={() => setPath(path.slice(0, index + 1))}>
                    {nodeTitle(node)}
                  </Typography.Link>
                ),
            })),
          ]}
        />
      )}
      <Flex vertical>
        {currentLevel.map((node) => {
          const firstChild = node.children?.[0]
          const childCount = node.children?.length ?? 0
          const drillable = childCount > 0
          return (
            <Flex
              key={nodeKey(node)}
              align="center"
              justify="space-between"
              gap={12}
              style={{
                padding: '10px 4px',
                borderBottom: '1px solid var(--sf-border-subtle)',
                cursor: drillable ? 'pointer' : 'default',
              }}
              onClick={() => {
                if (drillable) setPath((prev) => [...prev, node])
              }}
            >
              <Flex align="center" gap={8}>
                <Text strong>{nodeTitle(node)}</Text>
                <Text type="secondary">{LEVEL_LABEL[nodeLevel(node)]}</Text>
                {drillable && firstChild && (
                  <Text type="secondary">
                    {childCount} 个{LEVEL_LABEL[nodeLevel(firstChild)]}
                  </Text>
                )}
              </Flex>
              <Flex align="center" gap={12}>
                <Flex vertical align="flex-end">
                  <Text className="sf-num">{formatNumber(node.totalQty)}</Text>
                  {node.availableQty !== undefined && (
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      可用 {formatNumber(node.availableQty)}
                    </Text>
                  )}
                </Flex>
                {drillable && (
                  <Text type="secondary">
                    <RightOutlined />
                  </Text>
                )}
              </Flex>
            </Flex>
          )
        })}
        {currentLevel.length === 0 && <SfEmpty description="该层级下没有库存分布" />}
      </Flex>
    </Flex>
  )
}

// ---------- 页签二至六：复用既有列表端点，按 skuCode 固定过滤（frontend.md §10.3） ----------

const BATCH_COLUMNS: ColumnsType<BatchItem> = [
  { title: '批次号', dataIndex: 'batchNo', width: 140, fixed: 'left' },
  { title: '供应商', dataIndex: 'supplierName', width: 140, render: (v?: string) => v ?? '-' },
  { title: '生产日期', dataIndex: 'productionDate', width: 110, render: (v?: string) => formatDate(v) },
  { title: '入库日期', dataIndex: 'inboundDate', width: 110, render: (v?: string) => formatDate(v) },
  { title: '效期', dataIndex: 'expiryDate', width: 110, render: (v?: string) => formatDate(v) },
  {
    title: '成本价',
    dataIndex: 'costPrice',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '总库存',
    dataIndex: 'totalQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '可用',
    dataIndex: 'availableQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  {
    title: '更新时间',
    dataIndex: 'updatedAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 批次库存页签：GET /api/inventory/batches?skuCode=（M1 不交付该端点，呈统一错误态） */
function BatchTab({ skuCode }: { skuCode: string }) {
  const list = usePagedList<BatchItem, BatchQuery>({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'batches'],
    fetch: (q) => inventoryApi.batches({ ...q, skuCode }),
    params: {},
  })
  return (
    <SfTable<BatchItem>
      storageKey="inventory-stock-detail-batches"
      rowKey="id"
      columns={BATCH_COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该 SKU 暂无批次库存"
      scrollX={1210}
    />
  )
}

const SERIAL_COLUMNS: ColumnsType<SerialItem> = [
  { title: '序列号', dataIndex: 'serialNo', width: 150, fixed: 'left' },
  { title: '批次', dataIndex: 'batchNo', width: 120, render: (v?: string) => v ?? '-' },
  { title: '仓库', dataIndex: 'warehouseName', width: 110, render: (v?: string) => v ?? '-' },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: SerialStatus) => <SerialStatusTag status={v} />,
  },
  { title: '最近来源类型', dataIndex: 'lastSourceType', width: 110, render: (v?: string) => v ?? '-' },
  { title: '最近来源单号', dataIndex: 'lastSourceNo', width: 150, render: (v?: string) => v ?? '-' },
  {
    title: '最近事件时间',
    dataIndex: 'lastEventAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 序列号页签：GET /api/inventory/serials?skuCode=（M1 不交付该端点，呈统一错误态） */
function SerialTab({ skuCode }: { skuCode: string }) {
  const list = usePagedList<SerialItem, SerialQuery>({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'serials'],
    fetch: (q) => inventoryApi.serials({ ...q, skuCode }),
    params: {},
  })
  return (
    <SfTable<SerialItem>
      storageKey="inventory-stock-detail-serials"
      rowKey="id"
      columns={SERIAL_COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该 SKU 暂无序列号"
      scrollX={1160}
    />
  )
}

const LEDGER_COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'createdAt',
    width: 160,
    fixed: 'left',
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  { title: '单据号', dataIndex: 'bizNo', width: 150 },
  { title: '业务类型', dataIndex: 'bizType', width: 100 },
  {
    title: '方向',
    dataIndex: 'direction',
    width: 90,
    render: (v: LedgerItem['direction']) => <DirectionText direction={v} />,
  },
  {
    title: '数量',
    dataIndex: 'qty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动前',
    dataIndex: 'beforeQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动后',
    dataIndex: 'afterQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  { title: '操作人', dataIndex: 'operatorName', width: 100, render: (v: string) => v || '-' },
]

/** 库存流水页签：GET /api/inventory-ledgers?skuCode=（仅 inventory:ledger:list 在 M1 冻结面内） */
function LedgerTab({ skuCode }: { skuCode: string }) {
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'ledger'],
    fetch: (q) => inventoryApi.ledger({ ...q, skuCode }),
    params: {},
  })
  return (
    <SfTable<LedgerItem>
      storageKey="inventory-stock-detail-ledger"
      rowKey="id"
      columns={LEDGER_COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该 SKU 暂无库存流水"
      scrollX={1190}
    />
  )
}

const LOCK_COLUMNS: ColumnsType<InventoryLockItem> = [
  {
    title: '锁定类型',
    dataIndex: 'lockType',
    width: 100,
    render: (v: InventoryLockType) => LOCK_TYPE_LABEL[v] ?? v,
  },
  { title: '来源类型', dataIndex: 'sourceType', width: 110 },
  { title: '来源单号', dataIndex: 'sourceNo', width: 160 },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
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

/** 库存锁定页签：GET /api/inventory/locks?skuCode=（M1 不交付该端点，呈统一错误态） */
function LockTab({ skuCode }: { skuCode: string }) {
  const list = usePagedList<InventoryLockItem, InventoryLockQuery>({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'locks'],
    fetch: (q) => inventoryApi.locks({ ...q, skuCode }),
    params: {},
  })
  return (
    <SfTable<InventoryLockItem>
      storageKey="inventory-stock-detail-locks"
      rowKey="id"
      columns={LOCK_COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该 SKU 暂无库存锁定记录"
      scrollX={1460}
    />
  )
}

const TRACE_COLUMNS: ColumnsType<TraceItem> = [
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
  { title: '业务类型', dataIndex: 'bizType', width: 100 },
  { title: '单据号', dataIndex: 'bizNo', width: 160 },
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
  { title: '操作人', dataIndex: 'operatorName', width: 100, render: (v: string) => v || '-' },
]

/** 追溯页签：GET /api/inventory/trace?skuCode=（前端先行骨架，呈统一错误态） */
function TraceTab({ skuCode }: { skuCode: string }) {
  const list = usePagedList<TraceItem, TraceQuery>({
    queryKey: ['inventory', 'stock', 'detail', skuCode, 'trace'],
    fetch: (q) => inventoryApi.trace({ ...q, skuCode }),
    params: {},
  })
  return (
    <SfTable<TraceItem>
      storageKey="inventory-stock-detail-trace"
      rowKey="id"
      columns={TRACE_COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该 SKU 暂无追溯记录"
      scrollX={1420}
    />
  )
}

/**
 * 库存详情（frontend.md §10.3，无菜单动态段路由 /inventory/stock/:skuCode）：
 * 页头（SKU + 商品名 + 返回）→ 关键指标条（总库存/可用/锁定/冻结/待检，§7 SfSummaryBar）
 * → 基础信息（§7 SfDetailSection）→ 六页签（库存分布/批次库存/序列号/库存流水/库存锁定/追溯；
 * 盘点按既有裁决由盘点中心 /counts 承载，不做页签）。
 * SKU 汇总与层级分布为前端先行契约（GET /api/inventory/{skuCode}、/{skuCode}/distribution），
 * 后端 M1 库存查询面未含，就绪前对应区块呈统一错误态；页签数据全部来自真实列表端点，禁止 mock。
 */
export default function StockDetailPage() {
  const params = useParams<{ skuCode: string }>()
  const skuCode = params.skuCode ?? ''
  const navigate = useNavigate()

  const detail = useQuery({
    queryKey: ['inventory', 'stock', 'detail', skuCode],
    queryFn: () => inventoryApi.stockDetail(skuCode),
    enabled: skuCode !== '',
  })
  const summary = detail.data

  // §26.3：默认回到列表（history 有上一页时回退，保证浏览器返回栈一致）
  const handleBack = () => {
    const historyState = window.history.state as { idx?: number } | null
    if ((historyState?.idx ?? 0) > 0) navigate(-1)
    else navigate('/inventory/stock')
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title={skuCode || '库存详情'}
        subtitle={summary?.productName}
        onBack={handleBack}
      />

      {/* §10.3：SKU 汇总关键指标条 */}
      <Card size="small" style={{ marginBottom: 16 }} styles={{ body: { padding: '12px 16px' } }}>
        {detail.isPending ? (
          <Skeleton active paragraph={{ rows: 1 }} />
        ) : detail.error ? (
          <SfError
            error={detail.error}
            onRetry={detail.refetch}
            description={`库存汇总接口 GET /api/inventory/${skuCode || '{skuCode}'} 尚未交付（后端 M1 库存查询面未含 SKU 汇总），接口就绪后自动展示真实数据`}
          />
        ) : (
          <SfSummaryBar
            items={[
              { label: '总库存', value: formatNumber(summary?.totalQty) },
              { label: '可用', value: formatNumber(summary?.availableQty) },
              { label: '锁定', value: formatNumber(summary?.lockedQty) },
              { label: '冻结', value: formatNumber(summary?.frozenQty) },
              { label: '待检', value: formatNumber(summary?.pendingInspectQty) },
            ]}
          />
        )}
      </Card>

      {/* §7：基础信息分区（汇总接口就绪后渲染） */}
      {summary && (
        <div style={{ marginBottom: 16 }}>
          <SfDetailSection title="基础信息">
            <Descriptions
              size="small"
              column={{ xs: 1, sm: 2, md: 3 }}
              items={[
                { key: 'skuCode', label: 'SKU 编码', children: summary.skuCode },
                { key: 'productName', label: '商品名称', children: summary.productName },
                { key: 'barcode', label: '商品条码', children: summary.barcode ?? EMPTY_TEXT },
                { key: 'unitName', label: '计量单位', children: summary.unitName ?? EMPTY_TEXT },
                {
                  key: 'warehouseCount',
                  label: '有货仓库数',
                  children:
                    summary.warehouseCount !== undefined
                      ? formatNumber(summary.warehouseCount)
                      : EMPTY_TEXT,
                },
              ]}
            />
          </SfDetailSection>
        </div>
      )}

      {/* §10.3：六页签 */}
      <Card size="small">
        <Tabs
          defaultActiveKey="distribution"
          items={[
            { key: 'distribution', label: '库存分布', children: <DistributionTab skuCode={skuCode} /> },
            { key: 'batches', label: '批次库存', children: <BatchTab skuCode={skuCode} /> },
            { key: 'serials', label: '序列号', children: <SerialTab skuCode={skuCode} /> },
            { key: 'ledger', label: '库存流水', children: <LedgerTab skuCode={skuCode} /> },
            { key: 'locks', label: '库存锁定', children: <LockTab skuCode={skuCode} /> },
            { key: 'trace', label: '追溯', children: <TraceTab skuCode={skuCode} /> },
          ]}
        />
      </Card>
    </div>
  )
}
