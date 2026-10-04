import { Card, Col, Row, Skeleton, Statistic } from 'antd'
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
                      title={entry.label}
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
