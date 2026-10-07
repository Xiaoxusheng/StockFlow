import { useMemo } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import {
  inventoryApi,
  type InventoryLockItem,
  type InventoryLockQuery,
  type InventoryLockStatus,
  type InventoryLockType,
} from '@/api/inventory'
import {
  buildBinCodeMap,
  buildIdMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBatchOptions,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey, SKU_OPTIONS_KEY, WAREHOUSE_OPTIONS_KEY, BIN_OPTIONS_KEY, batchOptionsKey } from '@/api/options'
import { DateCell } from '@/components/table/cells'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatQty } from '@/utils/format'

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

/** 可选维度 ID 0 值显示占位符（0=非批次） */
function idOrDash(value: string): string {
  return value === '0' ? '-' : value
}

interface LockNameMaps {
  skuCodes: Map<string, string>
  skuNames: Map<string, string>
  warehouseNames: Map<string, string>
  binCodes: Map<string, string>
  batchNos: Map<string, string>
}

/** 组装列：LockView 仅下发裸 ID，经 maps 本地解析编码/名称（解析失败降级 ID，不造假数据） */
function buildColumns(maps: LockNameMaps): ColumnsType<InventoryLockItem> {
  return [
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      fixed: 'left',
      render: (v: InventoryLockItem['sku_id']) => maps.skuCodes.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 200,
      ellipsis: true,
      render: (_: unknown, record: InventoryLockItem) => {
        const name = maps.skuNames.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      key: 'warehouse_name',
      width: 100,
      render: (v: InventoryLockItem['warehouse_id']) => maps.warehouseNames.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '库位',
      dataIndex: 'bin_id',
      key: 'bin_code',
      width: 110,
      render: (v: InventoryLockItem['bin_id']) => maps.binCodes.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '批次',
      dataIndex: 'batch_id',
      key: 'batch_no',
      width: 110,
      render: (v: InventoryLockItem['batch_id']) => idOrDash(maps.batchNos.get(idKey(v)) ?? idKey(v)),
    },
    {
      title: '锁定类型',
      dataIndex: 'lock_type',
      width: 100,
      render: (v: InventoryLockType) => LOCK_TYPE_LABEL[v] ?? v,
    },
    { title: '来源类型', dataIndex: 'source_type', width: 110, render: (v: string) => v || '-' },
    { title: '来源单号', dataIndex: 'source_no', width: 160, render: (v: string) => v || '-' },
    {
      title: '锁定数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      // stock.Qty 裸数字出参（numeric(18,4)），按最多 4 位小数展示、不强制补零
      render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
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
    {
      title: '锁定时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <DateCell value={v} />,
    },
    {
      title: '释放时间',
      dataIndex: 'released_at',
      width: 160,
      render: (v: string | null) => <DateCell value={v} />,
    },
  ]
}

/** 库存锁定（frontend.md §10.1；GET /api/inventory/locks 后端已交付：internal/inventory/handler.go:479-516；
 * 出参 LockView snake_case 仅裸 ID，经 api/options.ts 一次取全基础资料后本地映射补充编码/名称，
 * 失败降级 ID；锁定规则见 inventory-rules.md §4：释放须由明确业务动作触发并生成流水） */
export default function LocksPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<InventoryLockItem, InventoryLockQuery>({
    queryKey: ['inventory', 'locks'],
    fetch: (q) => inventoryApi.locks(q),
    urlSync: true,
  })

  // LockView 无联表编码/名称（warehouse_id/sku_id/bin_id/batch_id 裸 ID），options 一次取全本地映射
  const warehouses = useQuery({ queryKey: WAREHOUSE_OPTIONS_KEY, queryFn: fetchWarehouseOptions })
  const skus = useQuery({ queryKey: SKU_OPTIONS_KEY, queryFn: fetchSkuOptions })
  const bins = useQuery({ queryKey: BIN_OPTIONS_KEY, queryFn: fetchBinOptions })
  // 批次映射按当前页行内 sku_id 集合按需拉取（/api/batches?sku_id= 过滤，handler.go:366-372），
  // 避免无过滤全量拉取超上限后 batch_id→batch_no 映射降级裸 ID
  const batchSkuIds = useMemo(
    () => [...new Set(list.items.map((row) => String(row.sku_id)))].sort(),
    [list.items],
  )
  const batches = useQuery({
    queryKey: batchOptionsKey(batchSkuIds),
    queryFn: () => fetchBatchOptions(batchSkuIds),
    enabled: batchSkuIds.length > 0,
  })

  const maps: LockNameMaps = useMemo(
    () => ({
      warehouseNames: buildWarehouseMaps(warehouses.data ?? []).name,
      skuCodes: buildSkuMaps(skus.data ?? []).code,
      skuNames: buildSkuMaps(skus.data ?? []).name,
      binCodes: buildBinCodeMap(bins.data ?? []),
      batchNos: buildIdMap(batches.data ?? [], (b) => b.id, (b) => b.batch_no),
    }),
    [warehouses.data, skus.data, bins.data, batches.data],
  )
  const columns = useMemo(() => buildColumns(maps), [maps])

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存锁定"
        subtitle="订单占用 / 盘点锁定 / 质检 / 人工 / 异常冻结明细"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'warehouse_id', label: '仓库 ID', control: 'input', placeholder: '仓库 ID（正整数）' },
            { name: 'lock_type', label: '锁定类型', control: 'select', options: LOCK_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: LOCK_STATUS_OPTIONS },
            { name: 'source_no', label: '来源单号', control: 'input', placeholder: '来源单号' },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<InventoryLockItem>
          storageKey="inventory-locks"
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
          emptyText="当前筛选条件下没有库存锁定记录"
          scrollX={1690}
        />
      </Card>
    </div>
  )
}
