import { useState } from 'react'
import { Card, Flex, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { Dayjs } from 'dayjs'
import { DateCell } from '@/components/table/cells'
import { reportsApi, type ReportRangeQuery, type TurnoverRow } from '@/api/reports'
import { usePagedList } from '@/hooks/usePagedList'
import { SfTable } from '@/components/table/SfTable'
import { formatQty } from '@/utils/format'
import { rangeToParams, ReportRangePicker } from './ReportRangePicker'

const { Text } = Typography

const COLUMNS: ColumnsType<TurnoverRow> = [
  {
    title: '仓库',
    dataIndex: 'warehouse_code',
    width: 150,
    fixed: 'left',
    render: (v: string, record: TurnoverRow) => `${record.warehouse_name}（${v}）`,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '出库量', dataIndex: 'outbound_qty', width: 100, align: 'right', render: qty },
  { title: '期初库存', dataIndex: 'start_qty', width: 100, align: 'right', render: qty },
  { title: '期末库存', dataIndex: 'end_qty', width: 100, align: 'right', render: qty },
  { title: '平均库存', dataIndex: 'avg_inventory', width: 100, align: 'right', render: qty },
  { title: '周转率', dataIndex: 'turnover_rate', width: 100, align: 'right', render: qty },
  {
    title: '周转天数',
    dataIndex: 'turnover_days',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
  },
  {
    title: '末次移动',
    dataIndex: 'last_moved_at',
    width: 160,
    render: (v?: string | null) =>
      v ? <DateCell value={v} /> : '-',
  },
]

function qty(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

/**
 * 库存周转报表（GET /api/reports/inventory-turnover，reports:report:read）：
 * 按仓库/SKU 的出库量与平均库存（期初期末均值口径）计算的周转率与周转天数
 * （TurnoverRow，repository.go:148-163；比率/天数为后端 Service 层纯函数计算，
 * 平均库存 ≤ 0 或无出库时记 0——后端不造假分母，calc.go:145-155）。
 * 时间范围 time_from/time_to（YYYY-MM-DD，缺省近 30 天，上限 366 天）。
 */
export default function ReportInventoryTurnover() {
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null)

  const list = usePagedList<TurnoverRow, ReportRangeQuery>({
    queryKey: ['reports', 'inventory-turnover'],
    fetch: (q) => reportsApi.inventoryTurnover(q),
    params: rangeToParams(range),
  })

  const handleRangeChange = (value: [Dayjs, Dayjs] | null) => {
    setRange(value)
    list.resetToFirstPage()
  }

  return (
    <Card
      size="small"
      title="库存周转"
      extra={<ReportRangePicker value={range} onChange={handleRangeChange} />}
    >
      <Flex vertical gap={8}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          口径：周转率 = 出库量 / 平均库存，平均库存 =（期初 + 期末）/ 2（期初由流水净变化重构）；
          时间范围缺省近 30 天，上限 366 天
        </Text>
        <SfTable<TurnoverRow>
          storageKey="report-inventory-turnover"
          // 行键用 code 对而非 id：后端 SKUID 扫描列名不匹配（GORM naming），sku_id 现网恒 0
          rowKey={(record) => `${record.warehouse_code}-${record.sku_code}`}
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="所选时间范围内没有库存周转数据"
          scrollX={1220}
        />
      </Flex>
    </Card>
  )
}
