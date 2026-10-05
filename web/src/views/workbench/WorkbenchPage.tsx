import { Card, Col, Row, Skeleton, Statistic, Tooltip } from 'antd'
import { InfoCircleOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { taskApi, type WorkbenchSummary } from '@/api/task'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { formatNumber } from '@/utils/format'

/**
 * 工作台四块入口（frontend.md §15.1 PC：我的待办 / 我的审批 / 我的任务 / 我的异常）。
 * path 仅指向 config/menu.tsx 既有菜单路径，不发明新菜单；
 * 待办 / 审批暂无对应菜单路径，后端模块上线前仅展示计数、不跳转。
 * 四计数经 taskApi.summary → GET /api/workbench/summary 真数据（2026-10-05 平台批交付，
 * 与 web/src/api/task.ts WorkbenchSummary 逐字段回对，2026-10-05 实测 200）；
 * 口径 tooltip 按 api.md §9 收口披露节④ 如实披露 Σ 差异。
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
}

/** 我的工作台（/workbench，menu.tsx 既有菜单；GET /api/workbench/summary 前端先行契约） */
export default function WorkbenchPage() {
  const navigate = useNavigate()
  const summary = useQuery({ queryKey: ['workbench', 'summary'], queryFn: taskApi.summary })

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
    </div>
  )
}
