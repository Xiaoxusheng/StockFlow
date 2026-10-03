import { lazy, Suspense } from 'react'
import { Col, Flex, Progress, Row, Skeleton, Typography } from 'antd'
import type { TrendPoint, DashboardWarehouseStock } from '@/api/dashboard'
import { formatPercent } from '@/utils/format'

const Line = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Line })))
const Bar = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Bar })))

const { Text } = Typography

/** 第二层入库 / 出库 / 库存趋势（数据来自 GET /api/reports/dashboard/trend，禁止前端拼装） */
export function TrendCharts({ points }: { points: TrendPoint[] }) {
  const bizData = points.flatMap((p) => [
    { date: p.date, type: '入库', qty: p.inbound },
    { date: p.date, type: '出库', qty: p.outbound },
  ])
  const stockData = points.map((p) => ({ date: p.date, qty: p.stockQty }))

  return (
    <Suspense fallback={<TrendChartSkeleton />}>
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={12}>
          <Text type="secondary">入库 / 出库趋势</Text>
          <Line
            data={bizData}
            xField="date"
            yField="qty"
            colorField="type"
            shapeField="smooth"
            height={220}
            style={{ maxWidth: '100%' }}
          />
        </Col>
        <Col xs={24} lg={12}>
          <Text type="secondary">库存趋势</Text>
          <Line
            data={stockData}
            xField="date"
            yField="qty"
            shapeField="smooth"
            height={220}
            style={{ maxWidth: '100%' }}
          />
        </Col>
      </Row>
    </Suspense>
  )
}

export function TrendChartSkeleton() {
  return <Skeleton active paragraph={{ rows: 5 }} />
}

/** 第四层仓库库存分布（数据来自 GET /api/reports/dashboard/warehouse-stock） */
export function WarehouseBar({ items }: { items: DashboardWarehouseStock[] }) {
  if (items.length === 0) {
    return <Text type="secondary">暂无仓库数据</Text>
  }
  return (
    <Suspense fallback={<Skeleton active paragraph={{ rows: 4 }} />}>
      <Bar
        data={items.map((w) => ({ warehouse: w.warehouseName, 库存量: w.totalQty }))}
        xField="warehouse"
        yField="库存量"
        height={240}
        style={{ maxWidth: '100%' }}
      />
    </Suspense>
  )
}

/** 第四层库位利用率（数据来自 warehouse-stock.binUtilization） */
export function BinUtilizationList({ items }: { items: DashboardWarehouseStock[] }) {
  if (items.length === 0) {
    return <Text type="secondary">暂无仓库数据</Text>
  }
  return (
    <Flex vertical gap={12}>
      {items.map((w) => (
        <Flex key={w.warehouseCode} align="center" gap={12}>
          <Text style={{ width: 80 }} ellipsis>
            {w.warehouseName}
          </Text>
          <Progress
            percent={w.binUtilization}
            size="small"
            style={{ flex: 1, marginBottom: 0 }}
            format={(p) => formatPercent(p ?? 0, 0)}
          />
        </Flex>
      ))}
    </Flex>
  )
}
