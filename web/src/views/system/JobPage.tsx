import { useState } from 'react'
import { Button, Card, Drawer, Modal, Switch, Tooltip, Typography, message } from 'antd'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  systemApi,
  type SystemJobId,
  type SystemJobItem,
  type SystemJobQuery,
  type SystemJobRunLogItem,
  type SystemJobRunStatus,
} from '@/api/system'
import { resolveErrorMessage } from '@/api/client'
import type { PageQuery } from '@/types/api'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 任务启停权限码（三段冻结码，internal/auth/permissions.go:353）：
 * 端点 PUT /api/system/jobs/:id/status 由后端 system:job:status 校验（sysops/routes.go:104），
 * 前端据此禁用开关仅为体验优化，后端必须校验（permission.md §5）。
 */
const SYSTEM_JOB_STATUS_PERMISSION = 'system:job:status'

const ENABLED_FILTER_OPTIONS = [
  { label: '已启用', value: 'enabled' },
  { label: '已停用', value: 'disabled' },
]

/** 启停下拉字符串值 → 契约布尔筛选（undefined=不过滤） */
function toEnabledFilter(value: unknown): boolean | undefined {
  if (value === 'enabled') return true
  if (value === 'disabled') return false
  return undefined
}

/** 最近执行结果（DDL 000014 chk_scheduled_jobs_last_run_status 小写值域；
 * never=从未执行，键未注册故直指定文案与语义，值域外兜底中性灰） */
const RUN_STATUS_META: Record<SystemJobRunStatus, { label: string; semantic: 'success' | 'danger' | 'processing' | 'neutral' }> = {
  success: { label: '成功', semantic: 'success' },
  failed: { label: '失败', semantic: 'danger' },
  running: { label: '执行中', semantic: 'processing' },
  never: { label: '未执行', semantic: 'neutral' },
}

function RunStatusTag({ status }: { status?: SystemJobRunStatus }) {
  if (!status) return <Text type="secondary">{EMPTY_TEXT}</Text>
  const hit = RUN_STATUS_META[status]
  return hit ? (
    <SfStatusTag label={hit.label} semantic={hit.semantic} />
  ) : (
    <SfStatusTag label={status} semantic="neutral" />
  )
}

/** 触发方式列文案（DDL 000014 chk_scheduled_job_runs_trigger 值域注释：调度/手动/防重入跳过） */
const TRIGGER_LABEL: Record<string, string> = {
  SCHEDULED: '调度触发',
  MANUAL: '手动触发',
  SKIPPED: '防重入跳过',
}

/** 耗时列：毫秒右对齐（frontend.md §1.5 数字统一 utils/format） */
function renderDuration(value?: number): string {
  return value == null ? EMPTY_TEXT : `${formatNumber(value)} ms`
}

/** 执行日志抽屉（独立组件）：挂载期 = 抽屉打开期，任务切换自然重置分页，
 * fetch 闭包内 job 恒非空（sysops/routes.go:105 GET /api/system/jobs/:id/run-logs） */
function RunLogDrawer({ job, onClose }: { job: SystemJobItem | null; onClose: () => void }) {
  return (
    <Drawer
      title={job ? `执行日志——${job.name}（${job.code}）` : '执行日志'}
      open={job !== null}
      width={880}
      destroyOnHidden
      onClose={onClose}
    >
      {job && <RunLogTable job={job} />}
    </Drawer>
  )
}

function RunLogTable({ job }: { job: SystemJobItem }) {
  const runLogs = usePagedList<SystemJobRunLogItem, PageQuery>({
    queryKey: ['system', 'jobs', 'run-logs', job.id],
    fetch: (q) => systemApi.jobs.runLogs(job.id, q),
    params: {},
  })

  const columns: ColumnsType<SystemJobRunLogItem> = [
    {
      title: '开始时间',
      dataIndex: 'start_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '结束时间',
      dataIndex: 'end_at',
      width: 160,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '触发方式',
      dataIndex: 'trigger',
      width: 110,
      render: (v: string) => TRIGGER_LABEL[v] ?? v,
    },
    {
      title: '结果',
      dataIndex: 'success',
      width: 90,
      render: (v: boolean) =>
        v ? <SfStatusTag label="成功" semantic="success" /> : <SfStatusTag label="失败" semantic="danger" />,
    },
    {
      title: '耗时',
      dataIndex: 'duration_ms',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{renderDuration(v)}</span>,
    },
    {
      title: '说明',
      dataIndex: 'message',
      width: 240,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 240 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT,
    },
  ]

  return (
    <SfTable<SystemJobRunLogItem>
      storageKey="system-job-run-logs"
      rowKey="id"
      columns={columns}
      dataSource={runLogs.items}
      loading={runLogs.isFetching}
      error={runLogs.error}
      onRetry={runLogs.refetch}
      onRefresh={runLogs.refetch}
      pagination={runLogs.pagination}
      total={runLogs.total}
      onPageChange={runLogs.onPageChange}
      emptyText="该任务暂无执行记录"
      scrollX={860}
      showColumnSetting={false}
      showFullscreen={false}
    />
  )
}

/**
 * 定时任务（/system/jobs，menu.tsx:158 权限码 system:job:view）：
 * 任务列表（执行时间 cron / 启停开关 / 执行日志查看），任务清单依据
 * architecture.md §9.1、管理要求 §9.2（每次执行记录触发方式/开始/结束/结果/耗时）。
 * 启停与执行日志走契约端点（sysops/routes.go:103-105 已挂载：list / status 热更新 / run-logs）。
 */
export default function JobPage() {
  const [params, setParams] = useState<SystemJobQuery>({})
  /** 执行日志抽屉当前任务（null=关闭） */
  const [logJob, setLogJob] = useState<SystemJobItem | null>(null)
  const [messageApi, contextHolder] = message.useMessage()
  const [modalApi, modalContextHolder] = Modal.useModal()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  // fail-closed：无 system:job:status 权限/权限点集为空一律不可启停（types/permission.ts canAccess）
  const canToggle = canAccess(user, SYSTEM_JOB_STATUS_PERMISSION)

  const list = usePagedList<SystemJobItem, SystemJobQuery>({
    queryKey: ['system', 'jobs'],
    fetch: (q) => systemApi.jobs.list(q),
    params,
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: SystemJobId; enabled: boolean }) =>
      systemApi.jobs.setStatus(id, { enabled }),
    onSuccess: (_data, variables) => {
      messageApi.success(variables.enabled ? '已启用' : '已停用')
      void queryClient.invalidateQueries({ queryKey: ['system', 'jobs'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  /** 启停入口：未装配调度（scheduled=false，jobsapi.go:26-28 空实现位任务）时
   * 热更新不生效，启停仅落库、服务重启后按 DB 行生效——二次确认向用户言明差异 */
  const handleToggle = (record: SystemJobItem, enabled: boolean) => {
    if (record.scheduled) {
      statusMutation.mutate({ id: record.id, enabled })
      return
    }
    modalApi.confirm({
      title: `「${record.name}」未在本进程装配调度`,
      content: '该任务当前由空实现占位，本次启停仅更新数据库配置，服务重启后才会生效。确认继续？',
      okText: '继续启停',
      cancelText: '取消',
      onOk: () => statusMutation.mutate({ id: record.id, enabled }),
    })
  }

  const columns: ColumnsType<SystemJobItem> = [
    { title: '任务编码', dataIndex: 'code', width: 170, fixed: 'left', ellipsis: true },
    { title: '任务名称', dataIndex: 'name', width: 160, ellipsis: true },
    {
      title: '执行时间',
      dataIndex: 'cron',
      width: 160,
      ellipsis: true,
      render: (v: string) => (
        <Text code style={{ fontSize: 12 }}>
          {v || EMPTY_TEXT}
        </Text>
      ),
    },
    {
      title: '最近执行',
      dataIndex: 'last_run_at',
      width: 160,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '最近结果',
      dataIndex: 'last_run_status',
      width: 100,
      render: (v?: SystemJobRunStatus) => <RunStatusTag status={v} />,
    },
    {
      title: '耗时',
      dataIndex: 'last_run_duration_ms',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{renderDuration(v)}</span>,
    },
    {
      title: '下次执行',
      dataIndex: 'next_run_at',
      width: 160,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 180,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT,
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 90,
      render: (v: boolean) => <SfStatusTag status={v ? 'enabled' : 'disabled'} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 170,
      render: (_: unknown, record: SystemJobItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Tooltip
            title={
              record.scheduled
                ? undefined
                : '未装配调度：启停仅落库，服务重启后生效（jobsapi.go scheduled=false）'
            }
          >
            <Switch
              size="small"
              checked={record.enabled}
              checkedChildren="启"
              unCheckedChildren="停"
              disabled={!canToggle}
              loading={statusMutation.isPending && statusMutation.variables?.id === record.id}
              onChange={(enabled) => handleToggle(record, enabled)}
            />
          </Tooltip>
          <Button type="link" size="small" onClick={() => setLogJob(record)}>
            执行日志
          </Button>
        </span>
      ),
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      {modalContextHolder}
      <SfPageHeader
        title="定时任务"
        subtitle="预警扫描 / 统计汇总 / 日志清理等后台任务（architecture.md §9）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '任务编码 / 名称' },
            { name: 'enabled', label: '状态', control: 'select', options: ENABLED_FILTER_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams({
              keyword: values.keyword as string | undefined,
              enabled: toEnabledFilter(values.enabled),
            })
            list.resetToFirstPage()
          }}
          onReset={() => list.resetToFirstPage()}
        />
        <SfTable<SystemJobItem>
          storageKey="system-jobs"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="暂无定时任务"
          scrollX={1460}
        />
      </Card>

      <RunLogDrawer job={logJob} onClose={() => setLogJob(null)} />
    </div>
  )
}
