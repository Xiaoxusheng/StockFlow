import { Card, Col, Flex, Row, Statistic, Typography } from 'antd'
import { useNavigate } from 'react-router'
import { SfError } from '@/components/common/SfError'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

export interface DashboardMetric {
  label: string
  value?: number
  /** 真实路由（config/menu.tsx 注册路径），缺省不可点击 */
  link?: string
  danger?: boolean
}

export interface DashboardMetricStripProps {
  metrics: DashboardMetric[]
  loading: boolean
  error: unknown
  onRetry: () => void
}

/** 第一层今日业务指标条（frontend.md §5，管理层 / 仓库人员两套由页面按视图组装） */
export function DashboardMetricStrip({ metrics, loading, error, onRetry }: DashboardMetricStripProps) {
  const navigate = useNavigate()
  return (
    <Card size="small" styles={{ body: { padding: '12px 8px' } }}>
      {error ? (
        <SfError error={error} onRetry={onRetry} />
      ) : (
        <Row gutter={[8, 12]}>
          {metrics.map((metric, index) => (
            <Col key={metric.label} xs={12} md={8} lg={6}>
              <Flex
                vertical
                align="center"
                gap={2}
                style={{
                  cursor: metric.link ? 'pointer' : 'default',
                  borderRight: index < metrics.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined,
                }}
                onClick={() => metric.link && navigate(metric.link)}
              >
                <Statistic
                  title={<Text type="secondary" style={{ fontSize: 13 }}>{metric.label}</Text>}
                  value={loading ? '-' : formatNumber(metric.value ?? 0)}
                  valueStyle={metric.danger ? { color: 'var(--sf-danger)' } : undefined}
                />
              </Flex>
            </Col>
          ))}
        </Row>
      )}
    </Card>
  )
}
