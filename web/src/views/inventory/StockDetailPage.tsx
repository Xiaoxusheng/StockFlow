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
  type InventoryId,
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

// ---------- 状态 / 文案映射（与列表页 LocksPage / SerialListPage / LedgerPage 保持一致；
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

/** 可选维度 ID 0 值显示占位符（0=未指定/非批次/不在库） */
function idOrDash(value: InventoryId): string {
  return String(value) === '0' ? '-' : String(value)
}

function DirectionText({ qtyChange }: { qtyChange: number }) {
  return qtyChange > 0 ? (
    <Text style={{ color: 'var(--sf-success)' }}>
      <ArrowDownOutlined /> 入库
    </Text>
  ) : qtyChange < 0 ? (
    <Text style={{ color: 'var(--sf-danger)' }}>
      <ArrowUpOutlined /> 出库
    </Text>
  ) : (
    <Text type="secondary">无变动</Text>
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
 * 层级下钻：整棵分布树来自 GET /api/inventory/{id}/distribution 一次返回，
 * 点击带 children 的节点进入下一层（仓库 → 库区 → 库位），面包屑回退。
 * 分布接口为前端先行契约（后端 M1 库存查询面未含），就绪前呈统一错误态。
 */
function DistributionTab({ stockId }: { stockId: string }) {
  const distribution = useQuery({
    queryKey: ['inventory', 'stock', 'detail', stockId, 'distribution'],
    queryFn: () => inventoryApi.stockDistribution(stockId),
    enabled: stockId !== '',
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
        description="库存分布接口 GET /api/inventory/{id}/distribution 尚未交付（后端 M1 库存查询面未含层级分布），接口就绪后自动展示真实数据"
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

// ---------- 页签二至六：复用既有列表端点，按详情行的 SKU/维度固定过滤（frontend.md §10.3） ----------

const BATCH_COLUMNS: ColumnsType<BatchItem> = [
  { title: '批次号', dataIndex: 'batch_no', width: 140, fixed: 'left' },
  { title: '供应商 ID', dataIndex: 'supplier_id', width: 140, render: (v: BatchItem['supplier_id']) => idOrDash(v) },
  { title: '生产日期', dataIndex: 'production_date', width: 110, render: (v?: string | null) => formatDate(v) },
  { title: '入库日期', dataIndex: 'inbound_date', width: 110, render: (v?: string | null) => formatDate(v) },
  { title: '效期', dataIndex: 'expiry_date', width: 110, render: (v?: string | null) => formatDate(v) },
  {
    title: '成本价',
    dataIndex: 'cost_price',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 180,
    ellipsis: true,
    render: (v: string) => (v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 批次库存页签：GET /api/batches?sku_id=（后端 T5 已交付；批次维度库存聚合未下发） */
function BatchTab({ skuId }: { skuId?: InventoryId }) {
  const ready = skuId !== undefined && String(skuId) !== '0'
  const list = usePagedList<BatchItem, BatchQuery>({
    queryKey: ['inventory', 'stock', 'detail', 'batches', String(skuId ?? '')],
    fetch: (q) => inventoryApi.batches({ ...q, sku_id: skuId }),
    params: {},
    enabled: ready,
  })
  if (!ready) return <SfEmpty description="该库存行未关联批次（非批次 SKU）" />
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
      emptyText="该 SKU 暂无批次台账"
      scrollX={1200}
    />
  )
}

const SERIAL_COLUMNS: ColumnsType<SerialItem> = [
  { title: '序列号', dataIndex: 'serial_no', width: 150, fixed: 'left' },
  { title: '批次 ID', dataIndex: 'batch_id', width: 110, render: (v: SerialItem['batch_id']) => idOrDash(v) },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 110, render: (v: SerialItem['warehouse_id']) => idOrDash(v) },
  { title: '库位 ID', dataIndex: 'bin_id', width: 110, render: (v: SerialItem['bin_id']) => idOrDash(v) },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: SerialStatus) => <SerialStatusTag status={v} />,
  },
  { title: '最近来源类型', dataIndex: 'last_source_type', width: 110, render: (v: string) => v || '-' },
  { title: '最近来源单号', dataIndex: 'last_source_no', width: 150, render: (v: string) => v || '-' },
  {
    title: '最近事件时间',
    dataIndex: 'last_event_at',
    width: 160,
    render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 序列号页签：GET /api/serials?sku_id=（后端 T5 已交付，handler.go:385-393 支持 sku_id 过滤） */
function SerialTab({ skuId }: { skuId?: InventoryId }) {
  const list = usePagedList<SerialItem, SerialQuery>({
    queryKey: ['inventory', 'stock', 'detail', 'serials', String(skuId ?? '')],
    fetch: (q) => inventoryApi.serials({ ...q, sku_id: skuId }),
    params: {},
    enabled: skuId !== undefined,
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
      scrollX={1210}
    />
  )
}

const LEDGER_COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'created_at',
    width: 160,
    fixed: 'left',
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  { title: '单据号', dataIndex: 'business_no', width: 150, render: (v: string) => v || '-' },
  { title: '业务类型', dataIndex: 'business_type', width: 100, render: (v: string) => v || '-' },
  {
    title: '变更类型',
    dataIndex: 'change_type',
    width: 100,
    render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
  },
  {
    title: '方向',
    key: 'direction',
    width: 90,
    render: (_: unknown, record: LedgerItem) => <DirectionText qtyChange={record.qty_change} />,
  },
  {
    title: '数量',
    dataIndex: 'qty_change',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动前',
    dataIndex: 'qty_before',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动后',
    dataIndex: 'qty_after',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '库位 ID', dataIndex: 'bin_id', width: 100, render: (v: LedgerItem['bin_id']) => idOrDash(v) },
  { title: '批次 ID', dataIndex: 'batch_id', width: 100, render: (v: LedgerItem['batch_id']) => idOrDash(v) },
  { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
]

/** 库存流水页签：GET /api/inventory-ledgers?sku_id=（handler.go:288-289 支持 sku_id 过滤） */
function LedgerTab({ skuId }: { skuId?: InventoryId }) {
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['inventory', 'stock', 'detail', 'ledger', String(skuId ?? '')],
    fetch: (q) => inventoryApi.ledger({ ...q, sku_id: skuId }),
    params: {},
    enabled: skuId !== undefined,
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
      scrollX={1320}
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

/** 库存锁定页签：GET /api/inventory/locks（前端先行契约，M1 不交付该端点，呈统一错误态；
 * skuCode 字段暂以详情行 SKU ID 填充，契约冻结后回对为 sku_id） */
function LockTab({ skuId }: { skuId?: InventoryId }) {
  const ready = skuId !== undefined
  const list = usePagedList<InventoryLockItem, InventoryLockQuery>({
    queryKey: ['inventory', 'stock', 'detail', 'locks', String(skuId ?? '')],
    fetch: (q) => inventoryApi.locks({ ...q, skuCode: skuId === undefined ? undefined : String(skuId) }),
    params: {},
    enabled: ready,
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

/** 追溯页签：GET /api/inventory/trace?skuCode=（前端先行契约，呈统一错误态；
 * skuCode 字段暂以详情行 SKU ID 填充，契约冻结后回对为 sku_id） */
function TraceTab({ skuId }: { skuId?: InventoryId }) {
  const ready = skuId !== undefined
  const list = usePagedList<TraceItem, TraceQuery>({
    queryKey: ['inventory', 'stock', 'detail', 'trace', String(skuId ?? '')],
    fetch: (q) => inventoryApi.trace({ ...q, skuCode: skuId === undefined ? undefined : String(skuId) }),
    params: {},
    enabled: ready,
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
 * 库存详情（frontend.md §10.3，无菜单动态段路由 /inventory/stock/:id）：
 * 路径段承载库存行 int64 id（GET /api/inventory/{id}，handler.go:260-275；router 沿用旧段名
 * `:skuCode` 注册且本组禁改 router，页面内一律以 stockId 语义消费）。
 * 页头 → 六状态指标条（StockState 恒等式口径）→ 基础信息（§7 SfDetailSection）
 * → 六页签（库存分布/批次库存/序列号/库存流水/库存锁定/追溯；
 * 盘点按既有裁决由盘点中心 /counts 承载，不做页签）。
 * 库存分布为前端先行契约（GET /api/inventory/{id}/distribution），就绪前对应区块呈统一错误态；
 * 页签数据全部来自真实列表端点，禁止 mock。
 */
export default function StockDetailPage() {
  const routeParams = useParams<{ skuCode: string }>()
  // 路由段名 :skuCode 为历史遗留（router 禁改），实际值为库存行 id
  const stockId = routeParams.skuCode ?? ''
  const navigate = useNavigate()

  const detail = useQuery({
    queryKey: ['inventory', 'stock', 'detail', stockId],
    queryFn: () => inventoryApi.stockDetail(stockId),
    enabled: stockId !== '',
  })
  const summary = detail.data
  const skuId = summary?.sku_id

  // §26.3：默认回到列表（history 有上一页时回退，保证浏览器返回栈一致）
  const handleBack = () => {
    const historyState = window.history.state as { idx?: number } | null
    if ((historyState?.idx ?? 0) > 0) navigate(-1)
    else navigate('/inventory/stock')
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存详情"
        subtitle={summary ? `库存行 ${stockId}` : `库存行 ${stockId || EMPTY_TEXT}`}
        onBack={handleBack}
      />

      {/* §10.3：六状态关键指标条（StockState，identity.go:18-25） */}
      <Card size="small" style={{ marginBottom: 16 }} styles={{ body: { padding: '12px 16px' } }}>
        {detail.isPending ? (
          <Skeleton active paragraph={{ rows: 1 }} />
        ) : detail.error ? (
          <SfError error={detail.error} onRetry={detail.refetch} />
        ) : (
          <SfSummaryBar
            items={[
              { label: '总库存', value: formatNumber(summary?.total_qty) },
              { label: '可用', value: formatNumber(summary?.available_qty) },
              { label: '锁定', value: formatNumber(summary?.locked_qty) },
              { label: '冻结', value: formatNumber(summary?.frozen_qty) },
              { label: '待检', value: formatNumber(summary?.pending_inspect_qty) },
              { label: '不良', value: formatNumber(summary?.defective_qty) },
            ]}
          />
        )}
      </Card>

      {/* §7：基础信息分区（详情接口就绪后渲染；维度列均为后端下发 ID 引用） */}
      {summary && (
        <div style={{ marginBottom: 16 }}>
          <SfDetailSection title="基础信息">
            <Descriptions
              size="small"
              column={{ xs: 1, sm: 2, md: 3 }}
              items={[
                { key: 'id', label: '库存行 ID', children: String(summary.id) },
                { key: 'skuId', label: 'SKU ID', children: String(summary.sku_id) },
                { key: 'warehouseId', label: '仓库 ID', children: String(summary.warehouse_id) },
                { key: 'zoneId', label: '库区 ID', children: idOrDash(summary.zone_id) },
                { key: 'shelfId', label: '货架 ID', children: idOrDash(summary.shelf_id) },
                { key: 'binId', label: '库位 ID', children: String(summary.bin_id) },
                { key: 'batchId', label: '批次 ID', children: idOrDash(summary.batch_id) },
                {
                  key: 'createdAt',
                  label: '创建时间',
                  children: summary.created_at ? formatDateTime(summary.created_at) : EMPTY_TEXT,
                },
                {
                  key: 'updatedAt',
                  label: '更新时间',
                  children: summary.updated_at ? formatDateTime(summary.updated_at) : EMPTY_TEXT,
                },
              ]}
            />
          </SfDetailSection>
        </div>
      )}

      {/* §10.3：六页签（批次/序列号/流水按详情行 sku_id 过滤；锁定/追溯/分布为前端先行契约） */}
      <Card size="small">
        <Tabs
          defaultActiveKey="distribution"
          items={[
            { key: 'distribution', label: '库存分布', children: <DistributionTab stockId={stockId} /> },
            { key: 'batches', label: '批次库存', children: <BatchTab skuId={skuId} /> },
            { key: 'serials', label: '序列号', children: <SerialTab skuId={skuId} /> },
            { key: 'ledger', label: '库存流水', children: <LedgerTab skuId={skuId} /> },
            { key: 'locks', label: '库存锁定', children: <LockTab skuId={skuId} /> },
            { key: 'trace', label: '追溯', children: <TraceTab skuId={skuId} /> },
          ]}
        />
      </Card>
    </div>
  )
}
