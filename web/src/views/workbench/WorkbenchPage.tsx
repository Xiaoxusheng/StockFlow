import { Card, Col, Row, Skeleton, Statistic, Tooltip, Typography } from 'antd'
import { InfoCircleOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  taskApi,
  type RecentOperationItem,
  type TaskItem,
  type WorkbenchSummary,
} from '@/api/task'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 工作台四块入口（frontend.md §15.1 PC：我的待办 / 我的审批 / 我的任务 / 我的异常）。
 * path 仅指向 config/menu.tsx 既有菜单路径，不发明新菜单；
 * 待办 / 审批暂无对应菜单路径，后端模块上线前仅展示计数、不跳转。
 * 四计数经 taskApi.summary → GET /api/workbench/summary 真数据（2026-10-05 平台批交付，
 * 与 web/src/api/task.ts WorkbenchSummary 逐字段回对，2026-10-05 实测 200）；
 * 口径 tooltip 按 api.md §9 收口披露节④ 如实披露 Σ 差异。
 *
 * 2026-10-06 效率层一期（§2.11）改版：进页面即回答「该干什么」——
 * ① 增量计数（超时 / 待我处理 / 今日完成，summary additive 字段）；
 * ② 「待我处理」Top5（GET /api/tasks?status=in_progress，后端 assignee=me 恒过滤）；
 * ③ 「最近操作」（GET /api/workbench/recent-operations，读本人 operation_logs 尾 10 条，
 *    写入链路零新增——仍仅 middleware.Audit）。
 */
interface WorkbenchEntry {
  key: keyof WorkbenchSummary
  label: string
  path?: string
  danger?: boolean
}

const ENTRIES: WorkbenchEntry[] = [
  { key: 'todo_count', label: '我的待办' },
  { key: 'approval_count', label: '我的审批' },
  { key: 'task_count', label: '我的任务', path: '/tasks' },
  { key: 'exception_count', label: '我的异常', path: '/exceptions', danger: true },
]

/** Σ 差异共享披露（api.md §9 收口披露节④ tooltip 义务）：四块互斥，
 * Σ(todo+approval+task+exception) ≠ Dashboard「待处理任务」计数——pendingTask
 * （dashboard.go:613）= 待收货 + 六作业块 + 异常，不含审批，即 Σ = 其 + 审批数。 */
const SIGMA_NOTE =
  '四块互斥拆分，Σ ≠ Dashboard「待处理任务」计数（后者 = 待收货+六作业块+异常，不含审批；Σ = 其 + 审批数）'

/** 四计数口径 tooltip（口径直引 internal/reports/workbench_summary.go:9-17 后端聚合口径） */
const SUMMARY_TOOLTIPS: Record<keyof WorkbenchSummary, string> = {
  todo_count: `我的待办：采购单待收货（APPROVED / PARTIAL_RECEIVED 单据型待办）。${SIGMA_NOTE}`,
  approval_count: `我的审批：五单据待审聚合（采购/销售/调拨/库存调整待审批 + 盘点待复核）。${SIGMA_NOTE}`,
  task_count: `我的任务：六作业块活动任务（上架/拣货/复核/打包/发货/盘点），不含审批。${SIGMA_NOTE}`,
  exception_count: `我的异常：未闭环异常（RESOLVED / CLOSED 之外，全量口径）。${SIGMA_NOTE}`,
  // additive 增量字段（效率层一期）不参与四块 Σ 口径，故不设 tooltip 文案
  timeout_count: '超时任务数（本人进行中且超时阈值已过：上架/拣货按 task.timeout.* 配置）。',
  mine_count: '指派给我且进行中的任务数（六作业块活动任务子集）。',
  today_completed_count: '今日已完成任务数（本人，按完成时间落在当日）。',
}

/** 待我处理任务列（GET /api/tasks?status=in_progress，后端 assignee=me 恒过滤） */
const TASK_COLUMNS: ColumnsType<TaskItem> = [
  { title: '任务号', dataIndex: 'task_no', width: 170, ellipsis: true },
  { title: '类型', dataIndex: 'task_type', width: 100 },
  { title: '来源单号', dataIndex: 'source_no', width: 160, ellipsis: true, render: (v?: string) => v ?? '-' },
  { title: '状态', dataIndex: 'status', width: 100, render: (v: string) => <SfStatusTag status={v} /> },
  {
    title: '进度',
    key: 'qty',
    width: 110,
    align: 'right',
    render: (_: unknown, r: TaskItem) => `${formatNumber(r.completed_qty)} / ${formatNumber(r.total_qty)}`,
  },
  { title: '创建时间', dataIndex: 'created_at', width: 150, render: (v: string) => formatDateTime(v) },
]

/** 最近操作列（GET /api/workbench/recent-operations：本人 operation_logs 尾 N 条） */
const OP_COLUMNS: ColumnsType<RecentOperationItem> = [
  { title: '时间', dataIndex: 'time', width: 150, render: (v: string) => formatDateTime(v) },
  { title: '动作', dataIndex: 'action', width: 120, ellipsis: true },
  { title: '模块', dataIndex: 'module', width: 110, ellipsis: true },
  {
    title: '对象',
    key: 'object',
    width: 150,
    ellipsis: true,
    render: (_: unknown, r: RecentOperationItem) =>
      r.object_type ? `${r.object_type}${r.object_id ? ` #${r.object_id}` : ''}` : '-',
  },
  {
    title: '结果',
    dataIndex: 'success',
    width: 100,
    render: (ok: boolean, r: RecentOperationItem) =>
      ok ? (
        <SfStatusTag label="成功" semantic="success" />
      ) : (
        <SfStatusTag label={r.error_code ?? '失败'} semantic="danger" />
      ),
  },
]

/** 我的工作台（/workbench，menu.tsx 既有菜单；GET /api/workbench/summary 真实数据） */
export default function WorkbenchPage() {
  const navigate = useNavigate()
  const summary = useQuery({ queryKey: ['workbench', 'summary'], queryFn: taskApi.summary })
  /** 待我处理 Top5（后端 assignee=me 恒过滤，真数据；无进行中任务时呈现真实空态） */
  const myTasks = useQuery({
    queryKey: ['workbench', 'mine-tasks'],
    queryFn: () => taskApi.list({ status: 'in_progress', page: 1, pageSize: 5 }),
  })
  /** 最近操作（只读端点；失败时呈现统一错误态，不造假记录） */
  const recentOps = useQuery({
    queryKey: ['workbench', 'recent-operations'],
    queryFn: () => taskApi.recentOperations(10),
  })

  return (
    <div className="sf-page">
      <SfPageHeader title="我的工作台" subtitle="待办 / 审批 / 任务 / 异常 入口总览" />
      <Card size="small">
        {summary.isPending ? (
          <Row gutter={[16, 16]}>
            {ENTRIES.map((entry) => (
              <Col key={entry.key} xs={12} md={6}>
                <Skeleton.Node active style={{ width: '100%', height: 64 }} />
              </Col>
            ))}
          </Row>
        ) : summary.error ? (
          <SfError
            error={summary.error}
            onRetry={summary.refetch}
            description="工作台汇总接口（/api/workbench/summary）暂不可用"
          />
        ) : (
          <Row gutter={[16, 16]}>
            {ENTRIES.map((entry) => {
              const path = entry.path
              return (
                <Col key={entry.key} xs={12} md={6}>
                  <Card
                    size="small"
                    hoverable={path !== undefined}
                    style={{ cursor: path !== undefined ? 'pointer' : undefined }}
                    onClick={path ? () => navigate(path) : undefined}
                  >
                    <Statistic
                      title={
                        <Tooltip title={SUMMARY_TOOLTIPS[entry.key]}>
                          <span>
                            {entry.label}
                            <InfoCircleOutlined
                              aria-hidden
                              style={{ marginInlineStart: 4, color: 'var(--sf-text-muted)' }}
                            />
                          </span>
                        </Tooltip>
                      }
                      value={formatNumber(summary.data?.[entry.key])}
                      valueStyle={entry.danger ? { color: 'var(--sf-danger)' } : undefined}
                    />
                  </Card>
                </Col>
              )
            })}
          </Row>
        )}
      </Card>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={14}>
          <SfDetailSection
            title="我现在该做什么"
            extra={<Text type="secondary">超时 / 待我处理 / 今日完成</Text>}
          >
            <SfSummaryBar
              items={[
                { label: '超时任务', value: formatNumber(summary.data?.timeout_count) },
                { label: '待我处理', value: formatNumber(summary.data?.mine_count) },
                { label: '今日完成', value: formatNumber(summary.data?.today_completed_count) },
              ]}
            />
            <div style={{ marginTop: 12 }}>
              <SfTable<TaskItem>
                variant="nested"
                rowKey="id"
                columns={TASK_COLUMNS}
                dataSource={myTasks.data?.items ?? []}
                loading={myTasks.isFetching}
                error={myTasks.error}
                onRetry={myTasks.refetch}
                emptyText="当前没有进行中的任务；可到「我的任务」查看与领取"
                scrollX={790}
              />
            </div>
          </SfDetailSection>
        </Col>
        <Col xs={24} xl={10}>
          <SfDetailSection title="最近操作" extra={<Text type="secondary">本人最近 10 条</Text>}>
            <SfTable<RecentOperationItem>
              variant="nested"
              rowKey={(r) => `${r.time}-${r.action}-${r.object_id ?? ''}-${r.request_id ?? ''}`}
              columns={OP_COLUMNS}
              dataSource={recentOps.data?.items ?? []}
              loading={recentOps.isFetching}
              error={recentOps.error}
              onRetry={recentOps.refetch}
              emptyText="暂无操作记录（业务动作经审计中间件写入 operation_logs）"
              scrollX={630}
            />
          </SfDetailSection>
        </Col>
      </Row>
    </div>
  )
}
