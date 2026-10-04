import { lazy, Suspense, useState } from 'react'
import { Card, Col, Flex, Row, Segmented, Skeleton, Statistic, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { inventoryApi } from '@/api/inventory'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { formatMoney, formatNumber, formatPercent } from '@/utils/format'

const Line = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Line })))
const Column = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Column })))

const { Text } = Typography

const RANGE_OPTIONS = [
  { label: '近7天', value: '7' },
  { label: '近30天', value: '30' },
  { label: '近90天', value: '90' },
]

const DEFAULT_RANGE = '30'

/**
 * 库存分析（frontend.md §10.1 口径：库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势）。
 * GET /api/inventory/analytics 为前端先行契约：分析域后端 M2+ 交付（backend-m1-plan.md §13），
 * 就绪前整页呈统一错误态，禁止 mock / 前端伪造分析数据。
 */
export default function AnalyticsPage() {
  const [range, setRange] = useState(DEFAULT_RANGE)
  const analytics = useQuery({
    queryKey: ['inventory', 'analytics', range],
    queryFn: () => inventoryApi.analytics({ days: Number(range) }),
  })

  const data = analytics.data

  return (
    <div className="sf-page">
      <SfPageHeader title="库存分析" subtitle="库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势" />

      <Flex vertical gap={16}>
        {/* 指标条：库存金额 / 库存总量 / SKU 数 / 周转率 / 周转天数 */}
        <Card size="small" styles={{ body: { padding: '12px 16px' } }}>
          {analytics.isPending ? (
            <Flex gap={32}>
              {['库存金额', '库存总量', 'SKU 数', '周转率', '周转天数'].map((label) => (
                <Skeleton.Node active key={label} style={{ width: 72, height: 40 }} />
              ))}
            </Flex>
          ) : analytics.error ? (
            <SfError
              error={analytics.error}
              onRetry={analytics.refetch}
              description="库存分析接口 GET /api/inventory/analytics 尚未交付（分析域后端 M2+ 交付），接口就绪后自动展示真实数据"
            />
          ) : (
            <Flex gap={0} wrap="wrap">
              {[
                { label: '库存金额', value: formatMoney(data?.total_stock_value) },
                { label: '库存总量', value: formatNumber(data?.total_qty) },
                { label: 'SKU 数', value: formatNumber(data?.total_sku_count) },
                { label: '周转率', value: `${formatNumber(data?.turnover_rate, 2)} 次` },
                { label: '周转天数', value: `${formatNumber(data?.turnover_days, 1)} 天` },
              ].map((item, index, arr) => (
                <Statistic
                  key={item.label}
                  title={item.label}
                  value={item.value}
                  valueStyle={{ fontSize: 18 }}
                  style={{
                    padding: '0 24px',
                    borderRight: index < arr.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined,
                  }}
                />
              ))}
            </Flex>
          )}
        </Card>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={10}>
            <Card size="small" title="ABC 分析（按库存金额）">
              {analytics.isPending ? (
                <Skeleton active paragraph={{ rows: 5 }} />
              ) : analytics.error ? (
                <SfError error={analytics.error} onRetry={analytics.refetch} />
              ) : (data?.abc?.length ?? 0) === 0 ? (
                <SfEmpty description="暂无 ABC 分析数据" />
              ) : (
                <Suspense fallback={<Skeleton active paragraph={{ rows: 5 }} />}>
                  <Flex vertical gap={12}>
                    <Column
                      data={(data?.abc ?? []).map((item) => ({
                        grade: `${item.grade} 类`,
                        金额占比: item.value_percent,
                      }))}
                      xField="grade"
                      yField="金额占比"
                      colorField="grade"
                      height={220}
                      style={{ maxWidth: '100%' }}
                    />
                    <Flex vertical>
                      {(data?.abc ?? []).map((item) => (
                        <Flex
                          key={item.grade}
                          justify="space-between"
                          gap={12}
                          style={{
                            padding: '6px 4px',
                            borderBottom: '1px solid var(--sf-border-subtle)',
                          }}
                        >
                          <Text strong>{item.grade} 类</Text>
                          <Flex gap={16}>
                            <Text type="secondary">SKU {formatNumber(item.sku_count)}</Text>
                            <Text type="secondary">金额 {formatMoney(item.value_amount)}</Text>
                            <Text className="sf-num">占比 {formatPercent(item.value_percent)}</Text>
                          </Flex>
                        </Flex>
                      ))}
                    </Flex>
                  </Flex>
                </Suspense>
              )}
            </Card>
          </Col>
          <Col xs={24} lg={14}>
            <Card
              size="small"
              title="库存趋势"
              extra={
                <Segmented
                  size="small"
                  options={RANGE_OPTIONS}
                  value={range}
                  onChange={(v) => setRange(v as string)}
                />
              }
            >
              {analytics.isPending ? (
                <Skeleton active paragraph={{ rows: 5 }} />
              ) : analytics.error ? (
                <SfError error={analytics.error} onRetry={analytics.refetch} />
              ) : (data?.trend?.length ?? 0) === 0 ? (
                <SfEmpty description="所选时间范围内暂无库存趋势数据" />
              ) : (
                <Suspense fallback={<Skeleton active paragraph={{ rows: 5 }} />}>
                    <Line
                      data={(data?.trend ?? []).flatMap((point) => [
                        { date: point.date, type: '库存数量', 值: point.total_qty },
                        { date: point.date, type: '库存金额', 值: point.stock_value },
                      ])}
                    xField="date"
                    yField="值"
                    colorField="type"
                    shapeField="smooth"
                    height={260}
                    style={{ maxWidth: '100%' }}
                  />
                </Suspense>
              )}
            </Card>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
