import type { ReactNode } from 'react'
import { Button, Card, Col, Flex, Progress, Row, Skeleton, Statistic, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { systemApi, type SystemMonitorMetrics } from '@/api/system'
import { SfLineChart } from '@/components/charts'
import { SfDetailSection } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatNumber, formatPercent } from '@/utils/format'

const { Text } = Typography

/** Progress percent 值域保护（0~100；接口异常值不阻塞渲染） */
function clampPercent(value: number | undefined): number | undefined {
  if (value == null || !Number.isFinite(value)) return undefined
  return Math.min(100, Math.max(0, value))
}

/** 运行时长：秒 → 「x 小时 y 分」（仅展示换算，不做业务计算） */
function formatUptime(seconds: number | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return EMPTY_TEXT
  return `${formatNumber(Math.floor(seconds / 3600))} 小时 ${formatNumber(Math.floor((seconds % 3600) / 60))} 分`
}

/** Redis 状态标签：未启用不视为故障（monitor.go:253 healthy 置 true 不误报） */
function RedisTag({ metrics }: { metrics?: SystemMonitorMetrics }) {
  if (!metrics) return null
  if (!metrics.redis.enabled) return <SfStatusTag label="未启用" semantic="neutral" />
  return metrics.redis.healthy ? (
    <SfStatusTag label="正常" semantic="success" />
  ) : (
    <SfStatusTag label="不可达" semantic="danger" />
  )
}

/** Redis 探测结果文案（monitor.go:253-257 status 值域：ok/unreachable/disabled） */
const REDIS_STATUS_LABEL: Record<string, string> = {
  ok: '连通正常',
  unreachable: '连接失败',
  disabled: '未配置',
}

/** 定时任务状态标签：最近执行存在失败即告警（monitor.go:267-271 failed_last_run 汇总） */
function JobsTag({ metrics }: { metrics?: SystemMonitorMetrics }) {
  if (!metrics) return null
  if (metrics.jobs.total === 0) return <SfStatusTag label="无任务" semantic="neutral" />
  return metrics.jobs.failed_last_run > 0 ? (
    <SfStatusTag label="有失败" semantic="danger" />
  ) : (
    <SfStatusTag label="正常" semantic="success" />
  )
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
 * 数据库连接 / Redis 连通 / API 请求与错误率 / 定时任务 / 队列积压五类指标卡
 * （monitor.go:186-234 monitorResponse 契约；CPU/内存/磁盘系统资源指标按 Orchestrator
 * 裁决①豁免至阶段 21，口径注记由响应 remarks 下发，页面如实展示不补位）
 * + API 请求/错误 24h 趋势（api_trend 5 分钟聚桶，数据源为 router 挂载的
 * sysops.MetricsMiddleware 进程内采样，internal/router/router.go:81）。
 * 全部来自契约端点 GET /api/system/monitor（sysops/routes.go:108 已挂载），禁止 mock / 前端算指标。
 * 30s 自动刷新：监控须能及时发现服务/数据库异常（deployment.md §5）。
 */
export default function MonitorPage() {
  const metrics = useQuery({
    queryKey: ['system', 'monitor'],
    queryFn: () => systemApi.monitor.metrics(),
    refetchInterval: 30_000,
  })

  const data = metrics.data
  const queues = data?.queued_tasks ?? []
  const remarks = data?.remarks ?? []

  // 趋势宽表（SfLineChart 多序列入参）：请求量/错误数按 5 分钟桶对齐，
  // 图表主题/色板/三态由 SfChart 内核统一注入（业务页零图表色值字面量）
  const trendData = (data?.api_trend ?? []).map((point) => ({
    time: point.time,
    请求量: point.requests,
    错误数: point.errors,
  }))

  return (
    <div className="sf-page">
      <SfPageHeader
        title="系统监控"
        subtitle="数据库 / Redis / API / 定时任务 / 队列积压（自动刷新：30 秒）"
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
          description="监控数据读取失败，可点击重试；若持续失败请检查后端服务与数据库连通性"
        />
      ) : (
        <Flex vertical gap={16}>
          {/* 五类指标卡：数据库连接 / Redis 连通 / API 请求与错误率 / 定时任务 / 队列积压 */}
          <Row gutter={[16, 16]}>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="数据库连接"
                main={`${formatNumber(data?.database.in_use)} / ${formatNumber(data?.database.max_open_connections)}`}
                percent={
                  data && data.database.max_open_connections > 0
                    ? (data.database.in_use / data.database.max_open_connections) * 100
                    : undefined
                }
                sub={`使用中 / 最大连接 · 空闲 ${formatNumber(data?.database.idle)} · 等待 ${formatNumber(data?.database.wait_count)}`}
                extra={
                  data?.database.healthy ? (
                    <SfStatusTag label="正常" semantic="success" />
                  ) : (
                    <SfStatusTag label="异常" semantic="danger" />
                  )
                }
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="Redis 连通"
                main={data?.redis.enabled ? '已启用' : '未启用'}
                sub={`探测结果：${REDIS_STATUS_LABEL[data?.redis.status ?? ''] ?? data?.redis.status ?? EMPTY_TEXT}`}
                extra={<RedisTag metrics={data} />}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="API 请求 / 错误率"
                main={formatPercent(data?.api.error_rate_percent)}
                percent={data?.api.error_rate_percent}
                sub={`近 1 小时：请求 ${formatNumber(data?.api.request_count_1h)} · 5xx 错误 ${formatNumber(data?.api.error_count_1h)}`}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <MetricCard
                title="定时任务"
                main={`${formatNumber(data?.jobs.enabled)} / ${formatNumber(data?.jobs.total)}`}
                sub={`已启用 / 注册任务 · 最近执行失败 ${formatNumber(data?.jobs.failed_last_run)}`}
                extra={<JobsTag metrics={data} />}
              />
            </Col>
            <Col xs={24} sm={12} lg={8}>
              <Card size="small" title="队列积压">
                {queues.length === 0 ? (
                  <SfEmpty description="未注入队列统计（asynq Inspector 缺省省略，monitor.go:196）" />
                ) : (
                  <Flex vertical gap={8}>
                    {queues.map((queue) => (
                      <Flex key={queue.queue} justify="space-between" gap={8} wrap="wrap">
                        <Text strong>{queue.queue}</Text>
                        <Text type="secondary" className="sf-num" style={{ fontSize: 12 }}>
                          待处理 {formatNumber(queue.pending)} · 执行中 {formatNumber(queue.active)} · 计划{' '}
                          {formatNumber(queue.scheduled)} · 重试 {formatNumber(queue.retry)}
                        </Text>
                      </Flex>
                    ))}
                  </Flex>
                )}
              </Card>
            </Col>
          </Row>

          {/* API 请求/错误趋势（§32 折线）：图表数据全部来自契约端点 api_trend（近 24 小时，
              5 分钟聚桶）；SfLineChart 内聚三态（Loading/Empty/Error），空桶如实空态不画 0 */}
          <Card size="small" title="API 请求 / 错误趋势（近 24 小时 · 5 分钟聚桶）">
            <SfLineChart
              data={trendData}
              xField="time"
              series={[
                { key: '请求量', name: '请求量', color: 'primary' },
                { key: '错误数', name: '错误数', color: 'danger' },
              ]}
              height={280}
              loading={metrics.isPending}
              error={metrics.error}
              onRetry={() => void metrics.refetch()}
              emptyText="暂无趋势采样数据（进程内采样随请求积累，服务重启后清零）"
            />
          </Card>

          {/* 运行信息：进程运行时长 + 版本三元组（构建提交缺省不展示，不造假版本号） */}
          <SfDetailSection title="运行信息">
            <Flex gap={32} wrap="wrap">
              <Statistic
                title="运行时长"
                value={formatUptime(data?.uptime_seconds)}
                valueStyle={{ fontSize: 18 }}
              />
              <Statistic
                title="应用版本"
                value={data?.version || EMPTY_TEXT}
                valueStyle={{ fontSize: 18 }}
              />
              <Statistic
                title="Go 版本"
                value={data?.go_version || EMPTY_TEXT}
                valueStyle={{ fontSize: 18 }}
              />
              {data?.commit ? (
                <Flex vertical gap={2}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    构建提交
                  </Text>
                  <Text className="sf-num" style={{ wordBreak: 'break-all', maxWidth: 220 }}>
                    {data.commit}
                  </Text>
                </Flex>
              ) : null}
            </Flex>
          </SfDetailSection>

          {/* 指标口径注记：后端 remarks 下发（进程内采样局限、系统资源指标豁免），如实展示 */}
          {remarks.length > 0 && (
            <SfDetailSection title="指标口径注记">
              <Flex vertical gap={4}>
                {remarks.map((remark) => (
                  <Text key={remark} type="secondary" style={{ fontSize: 12 }}>
                    · {remark}
                  </Text>
                ))}
              </Flex>
            </SfDetailSection>
          )}
        </Flex>
      )}
    </div>
  )
}
