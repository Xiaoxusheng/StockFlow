import { useMemo, useState } from 'react'
import { Alert, Card, Descriptions, Skeleton, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import {
  inventoryApi,
  type InventoryChangeType,
  type TraceLedgerItem,
  type TraceQuery,
  type TraceResult,
  type TraceStockRow,
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
  idKey,
} from '@/api/options'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { formatDateTime, formatQty } from '@/utils/format'

const { Text } = Typography

/** 变更类型文案（值域对齐 db/migrations/000005 inventory_ledgers CHECK 约束；未知值回退展示原始值） */
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

/** 库存状态列文案（inventory_ledgers status_from/status_to CHECK 值域；未知值回退展示原始值） */
const STATE_LABEL: Record<string, string> = {
  available: '可用',
  locked: '锁定',
  frozen: '冻结',
  pending_inspect: '待检',
  defective: '不良',
}

/** 追溯链上限选项（service_trace.go:87-90：1-500，缺省 100）；SfSearchForm 下拉值统一 string */
const TRACE_LIMIT_OPTIONS = [50, 100, 200, 500].map((value) => ({ label: `${value} 条`, value: String(value) }))

interface TraceFormValues {
  /** SKU 下拉值 = String(sku.id) */
  sku_id?: string
  serial_no?: string
  warehouse_id?: string
  /** 下拉值统一 string，提交时 Number() 还原（后端 limit 为正整数，handler.go:693-696） */
  limit?: string
}

/** 可选维度 ID 0 值显示占位符（0=非批次/不在库） */
function idOrDash(value: string): string {
  return value === '0' ? '-' : value
}

interface TraceNameMaps {
  skuCodes: Map<string, string>
  warehouseNames: Map<string, string>
  binCodes: Map<string, string>
  batchNos: Map<string, string>
}

/** 当前库存行表（TraceStockRow，六状态数量为字符串化 numeric(18,4)，formatQty 直读字符串） */
function StockRowsTable({ rows, maps }: { rows: TraceStockRow[]; maps: TraceNameMaps }) {
  const columns: ColumnsType<TraceStockRow> = [
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      key: 'warehouse_name',
      width: 110,
      render: (v: TraceStockRow['warehouse_id']) => maps.warehouseNames.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '库位',
      dataIndex: 'bin_id',
      key: 'bin_code',
      width: 110,
      render: (v: TraceStockRow['bin_id']) => maps.binCodes.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '批次',
      dataIndex: 'batch_id',
      key: 'batch_no',
      width: 120,
      render: (v: TraceStockRow['batch_id']) => idOrDash(maps.batchNos.get(idKey(v)) ?? idKey(v)),
    },
    { title: '总量', dataIndex: 'total_qty', width: 100, align: 'right', render: renderQtyText },
    { title: '可用', dataIndex: 'available_qty', width: 100, align: 'right', render: renderQtyText },
    { title: '锁定', dataIndex: 'locked_qty', width: 100, align: 'right', render: renderQtyText },
    { title: '冻结', dataIndex: 'frozen_qty', width: 100, align: 'right', render: renderQtyText },
    { title: '待检', dataIndex: 'pending_inspect_qty', width: 100, align: 'right', render: renderQtyText },
    { title: '不良', dataIndex: 'defective_qty', width: 100, align: 'right', render: renderQtyText },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]
  return (
    <Table<TraceStockRow>
      size="small"
      rowKey={(row) => `${row.warehouse_id}/${row.bin_id}/${row.batch_id}`}
      columns={columns}
      dataSource={rows}
      pagination={false}
      scroll={{ x: 1210 }}
      locale={{ emptyText: () => <SfEmpty description="该 SKU 当前无在库库存行" /> }}
    />
  )
}

function renderQtyText(value: string) {
  return <span className="sf-num">{formatQty(value)}</span>
}

/** 追溯链主轴表（TraceLedger，append-only 流水，时间正序） */
function ChainTable({ chain, maps }: { chain: TraceLedgerItem[]; maps: TraceNameMaps }) {
  const columns: ColumnsType<TraceLedgerItem> = [
    {
      title: '事件时间',
      dataIndex: 'created_at',
      width: 160,
      fixed: 'left',
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '变更类型',
      dataIndex: 'change_type',
      width: 110,
      render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
    },
    { title: '业务类型', dataIndex: 'business_type', width: 110, render: (v: string) => v || '-' },
    { title: '单据号', dataIndex: 'business_no', width: 160, render: (v: string) => v || '-' },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      key: 'warehouse_name',
      width: 100,
      render: (v: TraceLedgerItem['warehouse_id']) => maps.warehouseNames.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '库位',
      dataIndex: 'bin_id',
      key: 'bin_code',
      width: 110,
      render: (v: TraceLedgerItem['bin_id']) => idOrDash(maps.binCodes.get(idKey(v)) ?? idKey(v)),
    },
    {
      title: '批次',
      dataIndex: 'batch_id',
      key: 'batch_no',
      width: 120,
      render: (v: TraceLedgerItem['batch_id']) => idOrDash(maps.batchNos.get(idKey(v)) ?? idKey(v)),
    },
    { title: '序列号', dataIndex: 'serial_no', width: 130, render: (v?: string) => v || '-' },
    {
      title: '状态流转',
      key: 'statusFlow',
      width: 140,
      render: (_: unknown, record: TraceLedgerItem) => {
        if (!record.status_from && !record.status_to) return '-'
        const from = STATE_LABEL[record.status_from] ?? record.status_from ?? '-'
        const to = STATE_LABEL[record.status_to] ?? record.status_to ?? '-'
        return (
          <Text style={{ maxWidth: 140, whiteSpace: 'nowrap' }} ellipsis={{ tooltip: `${from} → ${to}` }}>
            {from} → {to}
          </Text>
        )
      },
    },
    { title: '变更数量', dataIndex: 'qty_change', width: 100, align: 'right', render: renderQtyText },
    { title: '结余', dataIndex: 'qty_after', width: 100, align: 'right', render: renderQtyText },
    { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v?: string) => v || '-' },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 160,
      ellipsis: true,
      render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
    },
  ]
  return (
    <Table<TraceLedgerItem>
      size="small"
      rowKey="id"
      columns={columns}
      dataSource={chain}
      pagination={false}
      scroll={{ x: 1690 }}
      locale={{ emptyText: () => <SfEmpty description="该 SKU / 序列号暂无库存流水" /> }}
    />
  )
}

/**
 * 库存追溯（frontend.md §10.1 库存中心模块；菜单 /inventory/trace，config/menu.tsx）。
 * GET /api/inventory/trace 后端已交付（returns 域编排、inventory 前缀挂载：internal/returns/
 * handler.go:669-696 + service_trace.go）：查询主键 sku_id / serial_no（至少其一），响应为
 * 单个非分页 TraceResult（当前库存行 stock_rows + 序列号台账 serial + 追溯链 chain +
 * 单据富化 documents + 操作日志 operations）。旧「SKU/序列号/批次号/单据号四维分页查询」
 * 为前端先行契约残留，后端无对应读参，已按交付契约收敛。
 * 数据全部来自真实编排接口，禁止 mock（requirements.md §10）。
 */
export default function TracePage() {
  const [params, setParams] = useState<TraceFormValues>({})

  // TraceResult 仅下发裸 ID（warehouse_id/bin_id/batch_id/sku_id），options 一次取全本地映射，
  // 失败降级 ID 展示（api/options.ts 约定，不造假数据）
  const warehouses = useQuery({ queryKey: ['options', 'warehouses'], queryFn: fetchWarehouseOptions })
  const skus = useQuery({ queryKey: ['options', 'skus'], queryFn: fetchSkuOptions })
  const bins = useQuery({ queryKey: ['options', 'bins'], queryFn: fetchBinOptions })
  const batches = useQuery({ queryKey: ['options', 'batches'], queryFn: fetchBatchOptions })

  const maps: TraceNameMaps = useMemo(
    () => ({
      warehouseNames: buildWarehouseMaps(warehouses.data ?? []).name,
      skuCodes: buildSkuMaps(skus.data ?? []).code,
      binCodes: buildBinCodeMap(bins.data ?? []),
      batchNos: buildIdMap(batches.data ?? [], (b) => b.id, (b) => b.batch_no),
    }),
    [warehouses.data, skus.data, bins.data, batches.data],
  )

  // 序列号台账与追溯链依赖 sku 定位，至少提供 SKU 或序列号（service_trace.go:92-94）
  const traceEnabled = Boolean(params.sku_id || params.serial_no?.trim())
  const trace = useQuery({
    queryKey: ['inventory', 'trace', params],
    queryFn: () =>
      inventoryApi.trace({
        sku_id: params.sku_id,
        serial_no: params.serial_no?.trim() || undefined,
        warehouse_id: params.warehouse_id || undefined,
        limit: params.limit ? Number(params.limit) : undefined,
      } satisfies TraceQuery),
    enabled: traceEnabled,
  })

  const result: TraceResult | undefined = trace.data
  const skuCode = result ? (maps.skuCodes.get(idKey(result.sku_id)) ?? idKey(result.sku_id)) : undefined

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存追溯"
        subtitle="按 SKU / 序列号追溯库存全链路（当前状态 → 批次 → 流水 → 单据 → 操作日志）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            {
              name: 'sku_id',
              label: 'SKU',
              control: 'select',
              placeholder: '请选择 SKU',
              options: (skus.data ?? []).map((sku) => ({
                label: sku.product_name ? `${sku.code} ${sku.product_name}` : sku.code,
                value: String(sku.id),
              })),
            },
            { name: 'serial_no', label: '序列号 SN', control: 'input', placeholder: '序列号（与 SKU 至少其一）' },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              placeholder: '全部仓库（数据权限内）',
              options: (warehouses.data ?? []).map((w) => ({
                label: `${w.code} ${w.name}`,
                value: String(w.id),
              })),
            },
            { name: 'limit', label: '链上限', control: 'select', options: TRACE_LIMIT_OPTIONS, placeholder: '默认 100 条' },
          ]}
          onSearch={(values) => setParams(values as TraceFormValues)}
        />
        {!traceEnabled ? (
          <SfEmpty description="请选择 SKU 或输入序列号开始追溯" />
        ) : trace.isPending ? (
          <Skeleton active paragraph={{ rows: 6 }} />
        ) : trace.error ? (
          <SfError
            error={trace.error}
            onRetry={trace.refetch}
            description="库存追溯接口不可用：GET /api/inventory/trace（sku_id 与 serial_no 至少其一）"
          />
        ) : result ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {result.chain_truncated && (
              <Alert
                type="info"
                showIcon
                message={`追溯链已达上限，仅展示最近 ${result.chain.length} 条（时间正序）；可提高「链上限」或缩小仓库范围`}
              />
            )}
            {result.serial && (
              <Descriptions
                title="序列号当前台账"
                bordered
                size="small"
                column={{ xs: 1, md: 2, xl: 3 }}
                items={[
                  { key: 'serialNo', label: '序列号', children: result.serial.serial_no },
                  {
                    key: 'warehouse',
                    label: '所在仓库',
                    children:
                      maps.warehouseNames.get(idKey(result.serial.warehouse_id)) ??
                      idOrDash(idKey(result.serial.warehouse_id)),
                  },
                  {
                    key: 'bin',
                    label: '所在库位',
                    children: maps.binCodes.get(idKey(result.serial.bin_id)) ?? idOrDash(idKey(result.serial.bin_id)),
                  },
                  {
                    key: 'batch',
                    label: '批次',
                    children: idOrDash(maps.batchNos.get(idKey(result.serial.batch_id)) ?? idKey(result.serial.batch_id)),
                  },
                  { key: 'status', label: '状态', children: result.serial.status },
                  {
                    key: 'lastSource',
                    label: '最近来源',
                    children: result.serial.last_source_no
                      ? `${result.serial.last_source_type} · ${result.serial.last_source_no}`
                      : '-',
                  },
                  {
                    key: 'lastEventAt',
                    label: '最近事件时间',
                    children: formatDateTime(result.serial.last_event_at),
                  },
                ]}
              />
            )}
            <div>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                当前库存行{skuCode ? `（${skuCode}）` : ''}
              </Text>
              <StockRowsTable rows={result.stock_rows} maps={maps} />
            </div>
            <div>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                追溯链（库存流水）
              </Text>
              <ChainTable chain={result.chain} maps={maps} />
            </div>
            <div>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                关联来源单据
              </Text>
              <Table<NonNullable<TraceResult['documents']>[number]>
                size="small"
                rowKey={(doc) => `${doc.type}/${doc.no}`}
                pagination={false}
                locale={{ emptyText: () => <SfEmpty description="追溯链未命中可富化的来源单据" /> }}
                columns={[
                  { title: '单据类型', dataIndex: 'type', width: 160 },
                  { title: '单据号', dataIndex: 'no', width: 180 },
                  {
                    title: '命中',
                    dataIndex: 'found',
                    width: 90,
                    render: (v: boolean) => (v ? '是' : '否'),
                  },
                  {
                    title: '仓库',
                    dataIndex: 'warehouse_id',
                    width: 110,
                    render: (v: number) => (v > 0 ? (maps.warehouseNames.get(idKey(v)) ?? idKey(v)) : '-'),
                  },
                  {
                    title: '明细行数',
                    dataIndex: 'lines',
                    width: 90,
                    align: 'right',
                    render: (v?: unknown) => (Array.isArray(v) ? v.length : '-'),
                  },
                ]}
                dataSource={result.documents}
              />
            </div>
            <div>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                关联操作日志（经流水 request_id 关联）
              </Text>
              <Table<NonNullable<TraceResult['operations']>[number]>
                size="small"
                rowKey="id"
                pagination={false}
                locale={{ emptyText: () => <SfEmpty description="追溯链未关联到操作日志" /> }}
                columns={[
                  {
                    title: '时间',
                    dataIndex: 'created_at',
                    width: 160,
                    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
                  },
                  { title: '模块', dataIndex: 'module', width: 110 },
                  { title: '对象类型', dataIndex: 'object_type', width: 130 },
                  { title: '动作', dataIndex: 'action', width: 110 },
                  { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
                  {
                    title: '结果',
                    dataIndex: 'success',
                    width: 80,
                    render: (v: boolean) => (v ? '成功' : '失败'),
                  },
                  { title: '请求 ID', dataIndex: 'request_id', width: 220, render: (v: string) => v || '-' },
                ]}
                dataSource={result.operations}
              />
            </div>
          </div>
        ) : null}
      </Card>
    </div>
  )
}
