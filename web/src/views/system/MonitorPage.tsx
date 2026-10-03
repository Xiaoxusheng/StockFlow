import { lazy, Suspense } from 'react'
import type { ReactNode } from 'react'
import { Button, Card, Col, Flex, Progress, Row, Skeleton, Statistic, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { systemApi } from '@/api/system'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatFileSize, formatNumber, formatPercent } from '@/utils/format'

const { Text } = Typography

/** 图表按需加载（同 AnalyticsPage 模式，避免 plots 进入首包） */
const Line = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Line })))

/** Progress percent 值域保护（0~100；接口异常值不阻塞渲染） */
function clampPercent(value: number | undefined): number {
  if (value == null || !Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

interface MetricCardProps {
  title: string
  /** 主数值（已格式化文案） */
  main: string
  /** 进度条百分比（0~100，可选：无比率语义的指标不展示） */
  percent?: number
  /** 次要说明（如用量、请求数） */
  sub?: string
  /** 标题右侧状态标签（如数据库健康） */
  extra?: ReactNode
}

/** 监控指标卡（frontend.md §5 指标条模式：主数值 + 比率 + 次要说明） */
function MetricCard({ title, main, percent, sub, extra }: MetricCardProps) {
  return (
    <Card size="small" title={title} extra={extra}>
      <Flex vertical gap={8}>
        <Statistic value={main} valueStyle={{ fontSize: 22 }} />
        {percent !== undefined && <Progress percent={clampPercent(percent)} size="small" />}
        {sub && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            {sub}
          </Text>
        )}
      </Flex>
    </Card>
  )
}

/**
 * 系统监控（/system/monitor，menu.tsx:160 权限码 system:monitor:view）：
 * CPU / 内存 / 磁盘 / 数据库连接 / API 错误率五类指标卡（requirements.md:184、deployment.md §5）
 * + API 请求/错误趋势图表，全部来自契约端点 GET /api/system/monitor（api.md:76 /api/system 域），
 * 后端系统域未交付：整页呈统一错误态，禁止 mock / 前端算指标。
 * 30s 自动刷新：监控须能及时发现服务/数据库异常（deployment.md §5）。
 */
export default function MonitorPage() {
  const metrics = useQuery({
    queryKey: ['system', 'monitor'],
    queryFn: () => systemApi.monitor.metrics(),
    refetchInterval: 30_000,
  })

  const data = metrics.data

  const trendData = (data?.apiTrend ?? []).flatMap((point) => [
    { time: point.time, 类型: '请求量', 数量: point.requests },
    { time: point.time, 类型: '错误数', 数量: point.errors },
  ])

  return (
    <div className="sf-page">
      <SfPageHeader
        title="系统监控"
        subtitle="CPU / 内存 / 磁盘 / 数据库连接 / API 错误率（自动刷新：30 秒）"
        extra={
          <Button
            icon={<ReloadOutlined />}
            loading={metrics.isFetching}
            onClick={() => void metrics.refetch()}
          >
            刷新
          </Button>
        }
      />

      {metrics.isPending ? (
        <Card size="small">
          <Skeleton active paragraph={{ rows: 8 }} />
        </Card>
      ) : metrics.error ? (
        <SfError
          error={metrics.error}
          onRetry={metrics.refetch}
          description="监控接口 GET /api/system/monitor 尚未交付（系统域后端未启动），接口就绪后自动展示真实指标"
        />
      ) : (
        <Flex vertical gap={16}>
          {/* 五类指标卡：CPU / 内存 / 磁盘 / 数据库连接 / API 错误率 */}
          <Row gutter={[16, 16]}>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="CPU"
                main={formatPercent(data?.cpu.usagePercent)}
                percent={data?.cpu.usagePercent}
                sub={data?.cpu.cores != null ? `逻辑核数：${formatNumber(data.cpu.cores)}` : undefined}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="内存"
                main={formatPercent(data?.memory.usagePercent)}
                percent={data?.memory.usagePercent}
                sub={`已用 ${formatFileSize(data?.memory.usedBytes)} / 总量 ${formatFileSize(data?.memory.totalBytes)}`}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="磁盘"
                main={formatPercent(data?.disk.usagePercent)}
                percent={data?.disk.usagePercent}
                sub={`已用 ${formatFileSize(data?.disk.usedBytes)} / 总量 ${formatFileSize(data?.disk.totalBytes)}`}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="数据库连接"
                main={`${formatNumber(data?.database.openConnections)} / ${formatNumber(data?.database.maxConnections)}`}
                percent={
                  data && data.database.maxConnections > 0
                    ? (data.database.openConnections / data.database.maxConnections) * 100
                    : 0
                }
                sub={data?.database.healthy ? '连接正常' : '连接异常——请立即检查数据库（deployment.md §5）'}
                extra={
                  <SfStatusTag status={data?.database.healthy ? 'online' : 'offline'} />
                }
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="API 错误率"
                main={formatPercent(data?.api.errorRatePercent)}
                percent={data?.api.errorRatePercent}
                sub={`近 1 小时：请求 ${formatNumber(data?.api.requestCount1h)} / 错误 ${formatNumber(data?.api.errorCount1h)}`}
              />
            </Col>
          </Row>

          {/* API 请求/错误趋势：图表数据全部来自契约端点 apiTrend（近 24 小时） */}
          <Card size="small" title="API 请求 / 错误趋势（近 24 小时）">
            {trendData.length === 0 ? (
              <SfEmpty description="暂无趋势采样数据" />
            ) : (
              <Suspense fallback={<Skeleton active paragraph={{ rows: 5 }} />}>
                <Line
                  data={trendData}
                  xField="time"
                  yField="数量"
                  colorField="类型"
                  shapeField="smooth"
                  height={280}
                  style={{ maxWidth: '100%' }}
                />
              </Suspense>
            )}
          </Card>

          {(data?.uptimeSeconds != null || data?.queuedTasks != null) && (
            <Card size="small" title="运行状况">
              <Flex gap={32} wrap="wrap">
                {data?.uptimeSeconds != null && (
                  <Statistic
                    title="运行时长"
                    value={`${formatNumber(Math.floor(data.uptimeSeconds / 3600))} 小时 ${formatNumber(Math.floor((data.uptimeSeconds % 3600) / 60))} 分`}
                    valueStyle={{ fontSize: 18 }}
                  />
                )}
                {data?.queuedTasks != null && (
                  <Statistic
                    title="队列任务"
                    value={formatNumber(data.queuedTasks)}
                    valueStyle={{ fontSize: 18 }}
                  />
                )}
              </Flex>
            </Card>
          )}
        </Flex>
      )}
    </div>
  )
}
