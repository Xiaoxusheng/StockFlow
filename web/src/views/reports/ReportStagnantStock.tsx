import { Card, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { reportsApi, type StagnantRow } from '@/api/reports'
import { usePagedList } from '@/hooks/usePagedList'
import { SfTable } from '@/components/table/SfTable'
import { formatQty } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<StagnantRow> = [
  {
    title: '仓库',
    dataIndex: 'warehouse_code',
    width: 150,
    fixed: 'left',
    render: (v: string, record: StagnantRow) => `${record.warehouse_name}（${v}）`,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '现存量', dataIndex: 'total_qty', width: 110, align: 'right', render: qty },
  {
    title: '末次移动',
    dataIndex: 'last_moved_at',
    width: 160,
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '未动天数',
    dataIndex: 'idle_days',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
  },
  {
    title: '积压档位',
    dataIndex: 'tier',
    width: 110,
    render: (v: string) => <Tag style={{ marginInlineEnd: 0 }}>≥ {v} 天</Tag>,
  },
]

function qty(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

/**
 * 库存积压报表（GET /api/reports/stagnant-stock，reports:report:read）：
 * 30/60/90 天未动库存清单（末次移动时间口径，与长期库存扫描同源；StagnantRow，
 * repository.go:218-233）。分档阈值清单可配置（system_configs，缺省 30/60/90），
 * tier 为该行命中的最大阈值；无筛选参数，仅分页（handler.go:184-196）。
 */
export default function ReportStagnantStock() {
  const list = usePagedList<StagnantRow, { page?: number; pageSize?: number }>({
    queryKey: ['reports', 'stagnant-stock'],
    fetch: (q) => reportsApi.stagnantStock(q),
    params: {},
  })

  return (
    <Card size="small" title="库存积压">
      <SfTable<StagnantRow>
        storageKey="report-stagnant-stock"
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
        emptyText="当前没有达到积压档位阈值的库存"
        scrollX={960}
      />
    </Card>
  )
}
