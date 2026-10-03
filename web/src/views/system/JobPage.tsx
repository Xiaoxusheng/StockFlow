import { useState } from 'react'
import { Button, Card, Drawer, Switch, Typography, message } from 'antd'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  SYSTEM_JOB_VIEW_PERMISSION,
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

/** 最近执行结果：值域为前端提案（api/system.ts SystemJobRunStatus），键未注册故直指定文案与语义 */
function RunStatusTag({ status }: { status?: SystemJobRunStatus }) {
  if (!status) return <Text type="secondary">{EMPTY_TEXT}</Text>
  const meta: Record<SystemJobRunStatus, { label: string; semantic: 'success' | 'danger' | 'processing' }> = {
    success: { label: '成功', semantic: 'success' },
    failed: { label: '失败', semantic: 'danger' },
    running: { label: '执行中', semantic: 'processing' },
  }
  const hit = meta[status]
  return <SfStatusTag label={hit.label} semantic={hit.semantic} />
}

/** 耗时列：毫秒右对齐（frontend.md §1.5 数字统一 utils/format） */
function renderDuration(value?: number): string {
  return value == null ? EMPTY_TEXT : `${formatNumber(value)} ms`
}

/**
 * 定时任务（/system/jobs，menu.tsx:158 权限码 system:job:view）：
 * 任务列表（执行时间 cron / 启停开关 / 执行日志查看），任务清单依据
 * architecture.md §9.1、管理要求 §9.2（每次执行记录开始/结束/结果/耗时）。
 * 启停与执行日志走契约端点（api/system.ts，api.md:76 /api/system 域），
 * 后端系统域未交付：查询呈统一错误态、启停呈错误提示，禁止 mock。
 */
export default function JobPage() {
  const [params, setParams] = useState<SystemJobQuery>({})
  /** 执行日志抽屉当前任务（null=关闭，抽屉内分页查询随之暂停） */
  const [logJob, setLogJob] = useState<SystemJobItem | null>(null)
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  // fail-closed：无权限/权限点集为空一律不可操作（types/permission.ts canAccess）
  const canManage = canAccess(user, SYSTEM_JOB_VIEW_PERMISSION)

  const list = usePagedList<SystemJobItem, SystemJobQuery>({
    queryKey: ['system', 'jobs'],
    fetch: (q) => systemApi.jobs.list(q),
    params,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['system', 'jobs'] })
  }

  const statusMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: SystemJobId; enabled: boolean }) =>
      systemApi.jobs.setStatus(id, { enabled }),
    onSuccess: (_data, variables) => {
      messageApi.success(variables.enabled ? '已启用' : '已停用')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const runLogs = usePagedList<SystemJobRunLogItem, PageQuery>({
    queryKey: ['system', 'jobs', 'run-logs', String(logJob?.id ?? '')],
    fetch: (q) => systemApi.jobs.runLogs(logJob?.id ?? '', q),
    params: {},
    enabled: logJob !== null,
  })

  const runLogColumns: ColumnsType<SystemJobRunLogItem> = [
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

  const columns: ColumnsType<SystemJobItem> = [
    { title: '任务编码', dataIndex: 'code', width: 170, fixed: 'left', ellipsis: true },
    { title: '任务名称', dataIndex: 'name', width: 160, ellipsis: true },
    {
      title: '执行时间',
      key: 'cron',
      width: 220,
      ellipsis: true,
      render: (_: unknown, record: SystemJobItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Text code style={{ fontSize: 12 }}>
            {record.cron || EMPTY_TEXT}
          </Text>
          {record.cron_desc && (
            <Text type="secondary" style={{ marginLeft: 8 }}>
              {record.cron_desc}
            </Text>
          )}
        </span>
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
          <Switch
            size="small"
            checked={record.enabled}
            checkedChildren="启"
            unCheckedChildren="停"
            disabled={!canManage}
            loading={statusMutation.isPending && statusMutation.variables?.id === record.id}
            onChange={(enabled) => statusMutation.mutate({ id: record.id, enabled })}
          />
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
          scrollX={1400}
        />
      </Card>

      <Drawer
        title={logJob ? `执行日志——${logJob.name}（${logJob.code}）` : '执行日志'}
        open={logJob !== null}
        width={880}
        destroyOnHidden
        onClose={() => setLogJob(null)}
      >
        {logJob && (
          <SfTable<SystemJobRunLogItem>
            storageKey="system-job-run-logs"
            rowKey="id"
            columns={runLogColumns}
            dataSource={runLogs.items}
            loading={runLogs.isFetching}
            error={runLogs.error}
            onRetry={runLogs.refetch}
            onRefresh={runLogs.refetch}
            pagination={runLogs.pagination}
            total={runLogs.total}
            onPageChange={runLogs.onPageChange}
            emptyText="该任务暂无执行记录"
            scrollX={820}
            showColumnSetting={false}
            showFullscreen={false}
          />
        )}
      </Drawer>
    </div>
  )
}
