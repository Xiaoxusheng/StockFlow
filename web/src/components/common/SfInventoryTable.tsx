import type { ReactNode } from 'react'
import type { ColumnsType } from 'antd/es/table'
import type { StockItem } from '@/api/inventory'
import { SfTable, type SfTableProps } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 数量列渲染（后端 Qty 为裸数字，identity.go:71） */
function renderQty(value: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/** 可选维度 ID（zone/shelf/batch）0 值显示占位符（handler.go 注释：0=未指定/非批次） */
function renderIdOrZero(value: StockItem['zone_id']): string {
  return String(value) === '0' ? '-' : String(value)
}

/**
 * 库存列定义工厂（InventoryView 仅含五维定位 ID + 六状态数量，handler.go:30-41；
 * 文本联表待后端聚合字段下发）。返回新数组，调用方可在其上追加/裁剪列。
 */
export function buildStockColumns(): ColumnsType<StockItem> {
  return [
    { title: 'SKU ID', dataIndex: 'sku_id', width: 120, fixed: 'left' },
    { title: '仓库 ID', dataIndex: 'warehouse_id', width: 110 },
    { title: '库区 ID', dataIndex: 'zone_id', width: 100, render: renderIdOrZero },
    { title: '货架 ID', dataIndex: 'shelf_id', width: 100, render: renderIdOrZero },
    { title: '库位 ID', dataIndex: 'bin_id', width: 110 },
    { title: '批次 ID', dataIndex: 'batch_id', width: 110, render: renderIdOrZero },
    { title: '总库存', dataIndex: 'total_qty', width: 90, align: 'right', render: renderQty },
    { title: '可用', dataIndex: 'available_qty', width: 90, align: 'right', render: renderQty },
    { title: '锁定', dataIndex: 'locked_qty', width: 90, align: 'right', render: renderQty },
    { title: '冻结', dataIndex: 'frozen_qty', width: 90, align: 'right', render: renderQty },
    { title: '待检', dataIndex: 'pending_inspect_qty', width: 90, align: 'right', render: renderQty },
    { title: '不良', dataIndex: 'defective_qty', width: 90, align: 'right', render: renderQty },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]
}

const STOCK_COLUMNS = buildStockColumns()

export type SfInventoryTableProps = Omit<SfTableProps<StockItem>, 'columns'>

/**
 * 实时库存标准表（frontend.md §10.2）：内置库存列工厂，
 * 页面不再自拼库存列；其余能力（分页/工具栏/列显示/密度/错误态）复用 SfTable。
 */
export function SfInventoryTable(props: SfInventoryTableProps) {
  return <SfTable<StockItem> columns={STOCK_COLUMNS} {...props} />
}
