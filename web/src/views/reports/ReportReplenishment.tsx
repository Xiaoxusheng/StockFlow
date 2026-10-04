import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { reportsApi, type ReplenishmentQuery, type ReplenishmentSuggestion } from '@/api/reports'
import { usePagedList } from '@/hooks/usePagedList'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatQty } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<ReplenishmentSuggestion> = [
  {
    title: '仓库',
    dataIndex: 'warehouse_code',
    width: 150,
    fixed: 'left',
    render: (v: string, record: ReplenishmentSuggestion) => `${record.warehouse_name}（${v}）`,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '可用库存', dataIndex: 'current_available', width: 100, align: 'right', render: qty },
  {
    title: '日均销量',
    dataIndex: 'daily_avg_sales',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
  },
  { title: '安全库存', dataIndex: 'safety_stock', width: 100, align: 'right', render: qty },
  { title: '在途', dataIndex: 'incoming_qty', width: 90, align: 'right', render: qty },
  { title: '目标库存', dataIndex: 'target_stock', width: 100, align: 'right', render: qty },
  {
    title: '建议补货量',
    dataIndex: 'suggested_qty',
    width: 110,
    align: 'right',
    render: (v: number) => <Text strong className="sf-num">{formatQty(v)}</Text>,
  },
  {
    title: '计算依据',
    dataIndex: 'basis_text',
    ellipsis: true,
    render: (v: string) => (
      <Text type="secondary" style={{ maxWidth: 360, fontSize: 12 }} ellipsis={{ tooltip: v }}>
        {v}
      </Text>
    ),
  },
]

function qty(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

/**
 * 智能补货建议报表（GET /api/reports/replenishment-suggestions，reports:report:read）：
 * 建议量 = max(0, max(安全库存, 日均销量×(采购周期+缓冲)) − 可用 − 在途)
 * （ReplenishmentSuggestion，service.go:156-163；只读建议，不自动生成任何单据、
 * 不改任何库存——inventory-rules §11 红线）。basis_text 为后端组装的人读计算依据。
 * only_shortage=false 查看全部参与计算的行，缺省仅缺货行（handler.go:210-213）。
 */
export default function ReportReplenishment() {
  const [params, setParams] = useState<ReplenishmentQuery>({ only_shortage: true })

  const list = usePagedList<ReplenishmentSuggestion, ReplenishmentQuery>({
    queryKey: ['reports', 'replenishment-suggestions'],
    fetch: (q) => reportsApi.replenishmentSuggestions(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    // SfSearchForm 的 select 值为 string：'false' = 查看全部行，其余（含重置缺省）= 仅缺货行
    setParams({ only_shortage: values.only_shortage !== 'false' })
    list.resetToFirstPage()
  }

  return (
    <Card size="small" title="智能补货建议">
      <SfSearchForm
        loading={list.isFetching}
        fields={[
          {
            name: 'only_shortage',
            label: '建议范围',
            control: 'select',
            options: [
              { label: '仅缺货行（建议补货量 > 0）', value: 'true' },
              { label: '全部参与计算行', value: 'false' },
            ],
            placeholder: '仅缺货行',
          },
        ]}
        onSearch={handleSearch}
      />
      <SfTable<ReplenishmentSuggestion>
        storageKey="report-replenishment"
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
        emptyText="当前没有补货建议（可用库存与在途已满足安全库存与近期出库需求）"
        scrollX={1220}
      />
    </Card>
  )
}
