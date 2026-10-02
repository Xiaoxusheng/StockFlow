import { useEffect, useState } from 'react'
import type { CSSProperties } from 'react'
import { Card, Descriptions, Drawer, Flex, Select, Space, Spin, Tooltip, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import {
  BIN_TYPE_LABEL,
  ZONE_TYPE_LABEL,
  toStatusKey,
  warehouseApi,
  type WarehouseMapBin,
  type WarehouseMapShelf,
  type WarehouseMapZone,
} from '@/api/warehouse'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'
import { resolveStatus, type StatusSemantic } from '@/types/status'

/**
 * 库位地图（frontend.md §11、requirements.md §2.6）：
 * 仓库 → 库区 → 货架 → 库位网格；颜色语义全局唯一来自 types/status.ts，
 * 单元格颜色经语义 → --sf-* Token 映射，页面不写死色值。
 */

/** 语义色 → Design Token（与 SfStatusTag 同源注册表，仅换色值载体） */
const SEMANTIC_TOKEN: Record<StatusSemantic, string> = {
  success: 'var(--sf-success)',
  processing: 'var(--sf-primary)',
  pending: 'var(--sf-warning)',
  warning: 'var(--sf-warning)',
  danger: 'var(--sf-danger)',
  neutral: 'var(--sf-text-muted)',
  disabled: 'var(--sf-text-muted)',
}

/** 库位占用状态图例（frontend.md §11：空闲/部分占用/满载/锁定/冻结/异常） */
const MAP_LEGEND: string[] = ['idle', 'partially_occupied', 'full', 'locked', 'frozen', 'abnormal']

interface BinCell {
  layer: number
  columnNo: number
  bin?: WarehouseMapBin
}

function statusSemantic(status?: string): StatusSemantic {
  return resolveStatus(toStatusKey(status))?.semantic ?? 'neutral'
}

function occupiedCellStyle(semantic: StatusSemantic): CSSProperties {
  const color = SEMANTIC_TOKEN[semantic]
  return {
    height: 44,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    paddingInline: 4,
    borderRadius: 'var(--sf-radius-sm)',
    border: `1px solid color-mix(in srgb, ${color} 45%, transparent)`,
    background: `color-mix(in srgb, ${color} 14%, transparent)`,
    color,
    fontSize: 11,
    fontWeight: 500,
    overflow: 'hidden',
    whiteSpace: 'nowrap',
    textOverflow: 'ellipsis',
    cursor: 'pointer',
  }
}

function emptyCellStyle(): CSSProperties {
  return {
    height: 44,
    borderRadius: 'var(--sf-radius-sm)',
    border: '1px dashed var(--sf-border)',
  }
}

function buildCells(shelf: WarehouseMapShelf): BinCell[] {
  const byPosition = new Map<string, WarehouseMapBin>()
  for (const bin of shelf.bins) {
    byPosition.set(`${bin.layer}-${bin.columnNo}`, bin)
  }
  const rows = Math.max(shelf.layers ?? 1, ...shelf.bins.map((b) => b.layer ?? 1), 1)
  const cols = Math.max(shelf.columns ?? 1, ...shelf.bins.map((b) => b.columnNo ?? 1), 1)
  const cells: BinCell[] = []
  for (let layer = 1; layer <= rows; layer += 1) {
    for (let columnNo = 1; columnNo <= cols; columnNo += 1) {
      cells.push({ layer, columnNo, bin: byPosition.get(`${layer}-${columnNo}`) })
    }
  }
  return cells
}

type SelectBin = (bin: WarehouseMapBin, zone: WarehouseMapZone, shelf: WarehouseMapShelf) => void

function ShelfGrid({
  zone,
  shelf,
  onSelect,
}: {
  zone: WarehouseMapZone
  shelf: WarehouseMapShelf
  onSelect: SelectBin
}) {
  const cells = buildCells(shelf)
  const cols = Math.max(shelf.columns ?? 1, 1)
  const rows = Math.max(shelf.layers ?? 1, 1)
  return (
    <div
      style={{
        border: '1px solid var(--sf-border)',
        borderRadius: 'var(--sf-radius-md)',
        padding: 12,
        background: 'var(--sf-surface)',
      }}
    >
      <Space size={8} style={{ marginBottom: 8 }}>
        <Typography.Text strong>{shelf.code}</Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {rows}层 × {cols}列
        </Typography.Text>
      </Space>
      <div style={{ overflowX: 'auto' }}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `repeat(${cols}, minmax(52px, 1fr))`,
            gap: 4,
            minWidth: cols * 58,
          }}
        >
          {cells.map((cell) => {
            const bin = cell.bin
            if (!bin) {
              return <div key={`${cell.layer}-${cell.columnNo}`} style={emptyCellStyle()} />
            }
            const meta = resolveStatus(toStatusKey(bin.status))
            return (
              <Tooltip
                key={`${cell.layer}-${cell.columnNo}`}
                title={
                  <div>
                    <div>{bin.code}</div>
                    <div>
                      {meta?.label ?? bin.status ?? '未知状态'} ·{' '}
                      {bin.binType ? (BIN_TYPE_LABEL[bin.binType] ?? bin.binType) : '未设类型'}
                    </div>
                    <div>
                      容量 {formatNumber(bin.currentCapacity)} / {formatNumber(bin.maxCapacity)}
                    </div>
                  </div>
                }
              >
                <div style={occupiedCellStyle(statusSemantic(bin.status))} onClick={() => onSelect(bin, zone, shelf)}>
                  {bin.code}
                </div>
              </Tooltip>
            )
          })}
        </div>
      </div>
    </div>
  )
}

function ZoneCard({ zone, onSelect }: { zone: WarehouseMapZone; onSelect: SelectBin }) {
  return (
    <Card
      size="small"
      style={{ marginBottom: 16 }}
      title={
        <Space size={8}>
          <Typography.Text strong>{zone.name}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {zone.code}
          </Typography.Text>
          {zone.zoneType && (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {ZONE_TYPE_LABEL[zone.zoneType] ?? zone.zoneType}
            </Typography.Text>
          )}
        </Space>
      }
      extra={<SfStatusTag status={toStatusKey(zone.status)} />}
    >
      {zone.shelves.length === 0 ? (
        <SfEmpty description="该库区暂无货架" />
      ) : (
        <Flex gap={12} wrap="wrap">
          {zone.shelves.map((shelf) => (
            <ShelfGrid key={shelf.id} zone={zone} shelf={shelf} onSelect={onSelect} />
          ))}
        </Flex>
      )}
    </Card>
  )
}

function CenteredLoading() {
  return (
    <div style={{ padding: '48px 0', textAlign: 'center' }}>
      <Spin />
    </div>
  )
}

/** 库位地图（M1 契约：GET /api/warehouses/{id}/map 区/架/位网格与占用状态） */
export default function WarehouseMapPage() {
  const [selectedWarehouseId, setSelectedWarehouseId] = useState<string | undefined>(undefined)
  const [selected, setSelected] = useState<{
    bin: WarehouseMapBin
    zone: WarehouseMapZone
    shelf: WarehouseMapShelf
  } | null>(null)

  const warehousesQuery = useQuery({
    queryKey: ['warehouse', 'warehouses', 'map-options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: 200 }),
  })
  const warehouseOptions = (warehousesQuery.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))

  useEffect(() => {
    if (selectedWarehouseId === undefined && warehouseOptions.length > 0) {
      setSelectedWarehouseId(warehouseOptions[0].value)
    }
  }, [selectedWarehouseId, warehouseOptions])

  const mapQuery = useQuery({
    queryKey: ['warehouse', 'map', selectedWarehouseId],
    queryFn: () => warehouseApi.map(selectedWarehouseId as string),
    enabled: selectedWarehouseId !== undefined,
  })

  const renderBody = () => {
    if (warehousesQuery.error) {
      return <SfError error={warehousesQuery.error} onRetry={warehousesQuery.refetch} />
    }
    if (warehousesQuery.isPending) {
      return <CenteredLoading />
    }
    if (warehouseOptions.length === 0) {
      return <SfEmpty description="请先在「仓库管理」创建仓库" />
    }
    if (mapQuery.error) {
      return <SfError error={mapQuery.error} onRetry={mapQuery.refetch} />
    }
    if (mapQuery.isPending) {
      return <CenteredLoading />
    }
    const zones = mapQuery.data?.zones ?? []
    if (zones.length === 0) {
      return <SfEmpty description="该仓库暂无库区数据" />
    }
    return (
      <>
        <Card size="small" style={{ marginBottom: 16 }}>
          <Flex align="center" gap={12} wrap="wrap">
            <Typography.Text type="secondary">库位状态：</Typography.Text>
            {MAP_LEGEND.map((key) => (
              <SfStatusTag key={key} status={key} />
            ))}
          </Flex>
        </Card>
        {zones.map((zone) => (
          <ZoneCard
            key={zone.id}
            zone={zone}
            onSelect={(bin, zoneOf, shelfOf) => setSelected({ bin, zone: zoneOf, shelf: shelfOf })}
          />
        ))}
      </>
    )
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库位地图"
        subtitle="仓库 → 库区 → 货架 → 库位占用网格"
        extra={
          <Select
            style={{ width: 240 }}
            placeholder="选择仓库"
            showSearch
            optionFilterProp="label"
            loading={warehousesQuery.isFetching}
            options={warehouseOptions}
            value={selectedWarehouseId}
            onChange={(value: string) => setSelectedWarehouseId(value)}
          />
        }
      />
      {renderBody()}

      <Drawer title="库位详情" width={380} open={selected !== null} onClose={() => setSelected(null)}>
        {selected && (
          <>
            <Descriptions
              column={1}
              size="small"
              items={[
                { key: 'code', label: '库位编码', children: selected.bin.code },
                {
                  key: 'type',
                  label: '库位类型',
                  children: selected.bin.binType
                    ? (BIN_TYPE_LABEL[selected.bin.binType] ?? selected.bin.binType)
                    : '-',
                },
                { key: 'status', label: '状态', children: <SfStatusTag status={toStatusKey(selected.bin.status)} /> },
                {
                  key: 'position',
                  label: '层 / 列',
                  children: (
                    <span className="sf-num">
                      {formatNumber(selected.bin.layer)} / {formatNumber(selected.bin.columnNo)}
                    </span>
                  ),
                },
                { key: 'shelf', label: '所属货架', children: selected.shelf.code },
                { key: 'zone', label: '所属库区', children: `${selected.zone.name}（${selected.zone.code}）` },
                {
                  key: 'capacity',
                  label: '容量（当前 / 最大）',
                  children: (
                    <span className="sf-num">
                      {formatNumber(selected.bin.currentCapacity)} / {formatNumber(selected.bin.maxCapacity)}
                    </span>
                  ),
                },
              ]}
            />
            <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0, fontSize: 12 }}>
              SKU / 批次 / 效期等库存明细请前往「实时库存」按库位查询。
            </Typography.Paragraph>
          </>
        )}
      </Drawer>
    </div>
  )
}
