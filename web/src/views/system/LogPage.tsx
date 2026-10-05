import { useState } from 'react'
import { Button, Card, Descriptions, Drawer, Skeleton, Tabs, Typography, theme } from 'antd'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  systemLogApi,
  type LoginLogItem,
  type LoginLogQuery,
  type OperationLogItem,
  type OperationLogQuery,
} from '@/api/system'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfError } from '@/components/common/SfError'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'

const { Text } = Typography

/**
 * 操作日志详情权限码（三段冻结码，internal/auth/permissions.go:348）：
 * 详情端点 GET /api/logs/operations/:id 由后端 system:log:read 校验（sysops/routes.go:95），
 * 前端据此隐藏「详情」入口仅为体验优化，后端必须校验（permission.md §5）。
 */
const SYSTEM_LOG_READ_PERMISSION = 'system:log:read'

const RESULT_FILTER_OPTIONS = [
  { label: '成功', value: 'success' },
  { label: '失败', value: 'fail' },
]

/** 结果下拉字符串值 → 契约布尔筛选（undefined=不过滤） */
function toSuccessFilter(value: unknown): boolean | undefined {
  if (value === 'success') return true
  if (value === 'fail') return false
  return undefined
}

/** 执行结果列：成功/失败一律走 SfStatusTag（frontend.md §24，键未注册故直指定文案与语义） */
function SuccessTag({ success }: { success: boolean }) {
  return success ? (
    <SfStatusTag label="成功" semantic="success" />
  ) : (
    <SfStatusTag label="失败" semantic="danger" />
  )
}

/** 操作对象列：object_type 为主（object_id=0 视为无对象，DDL 000002 默认值） */
function renderObject(record: OperationLogItem): string {
  if (!record.object_type) return EMPTY_TEXT
  const id = record.object_id != null && String(record.object_id) !== '0' ? ` #${record.object_id}` : ''
  return `${record.object_type}${id}`
}

/** 快照块：jsonb 原样展示（database.md §7 审计数据只读展示，不做前端解读） */
function SnapshotBlock({ title, value }: { title: string; value?: Record<string, unknown> }) {
  const { token } = theme.useToken()
  if (!value || Object.keys(value).length === 0) return null
  return (
    <div style={{ marginTop: 12 }}>
      <Text strong>{title}</Text>
      <pre
        style={{
          maxHeight: 200,
          overflow: 'auto',
          margin: '6px 0 0',
          padding: 8,
          fontSize: 12,
          lineHeight: 1.6,
          border: `1px solid ${token.colorBorderSecondary}`,
          borderRadius: token.borderRadiusLG,
          background: token.colorFillQuaternary,
        }}
      >
        {JSON.stringify(value, null, 2)}
      </pre>
    </div>
  )
}

/** 操作日志详情抽屉：按 id 请求详情端点（sysops/routes.go:95，system:log:read），
 * 三快照内容以详情端点为权威来源；加载呈 Skeleton、失败呈统一错误态，禁止复用列表行冒充详情 */
function OperationDetailDrawer({
  record,
  onClose,
}: {
  record: OperationLogItem | null
  onClose: () => void
}) {
  const detail = useQuery({
    queryKey: ['system', 'logs', 'operations', 'detail', record?.id ?? 0],
    queryFn: () => systemLogApi.operation(record?.id ?? 0),
    enabled: record !== null,
  })
  const data = detail.data

  return (
    <Drawer title="操作日志详情" open={record !== null} size={560} onClose={onClose}>
      {record !== null && detail.isPending && <Skeleton active paragraph={{ rows: 10 }} />}
      {record !== null && detail.error && (
        <SfError
          error={detail.error}
          onRetry={() => void detail.refetch()}
          description={`操作日志详情（ID ${record.id}）读取失败，可重试`}
        />
      )}
      {data && (
        <>
          <Descriptions column={2} size="small" bordered>
            <Descriptions.Item label="时间" span={2}>
              {formatDateTime(data.created_at)}
            </Descriptions.Item>
            <Descriptions.Item label="用户">{data.username || EMPTY_TEXT}</Descriptions.Item>
            <Descriptions.Item label="用户 ID">
              <span className="sf-num">{data.user_id}</span>
            </Descriptions.Item>
            <Descriptions.Item label="IP">{data.ip || EMPTY_TEXT}</Descriptions.Item>
            <Descriptions.Item label="日志 ID">
              <span className="sf-num">{data.id}</span>
            </Descriptions.Item>
            <Descriptions.Item label="模块">{data.module}</Descriptions.Item>
            <Descriptions.Item label="动作">{data.action}</Descriptions.Item>
            <Descriptions.Item label="对象" span={2}>
              {renderObject(data)}
            </Descriptions.Item>
            <Descriptions.Item label="请求" span={2}>
              {data.method ? `${data.method} ${data.path ?? ''}` : data.path || EMPTY_TEXT}
            </Descriptions.Item>
            <Descriptions.Item label="结果">
              <SuccessTag success={data.success} />
            </Descriptions.Item>
            <Descriptions.Item label="错误码">{data.error_code || EMPTY_TEXT}</Descriptions.Item>
            <Descriptions.Item label="Request ID" span={2}>
              <Text style={{ wordBreak: 'break-all' }}>{data.request_id || EMPTY_TEXT}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="User-Agent" span={2}>
              <Text style={{ wordBreak: 'break-all' }}>{data.user_agent || EMPTY_TEXT}</Text>
            </Descriptions.Item>
          </Descriptions>
          <SnapshotBlock title="请求快照" value={data.request_snapshot} />
          <SnapshotBlock title="变更前（before）" value={data.before_snapshot} />
          <SnapshotBlock title="变更后（after）" value={data.after_snapshot} />
        </>
      )}
    </Drawer>
  )
}

/** 操作日志 Tab：关键操作审计（db/migrations/000002 operation_logs 全字段） */
function OperationLogPane() {
  const [params, setParams] = useState<OperationLogQuery>({})
  const [detail, setDetail] = useState<OperationLogItem | null>(null)
  const user = useAuthStore((s) => s.user)
  // fail-closed：无 system:log:read 权限时隐藏详情入口（端点侧同码校验，sysops/routes.go:95）
  const canViewDetail = canAccess(user, SYSTEM_LOG_READ_PERMISSION)

  const list = usePagedList<OperationLogItem, OperationLogQuery>({
    queryKey: ['system', 'logs', 'operations'],
    fetch: (q) => systemLogApi.operations(q),
    params,
  })

  const columns: ColumnsType<OperationLogItem> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 160,
      fixed: 'left',
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    { title: '用户', dataIndex: 'username', width: 110, ellipsis: true },
    { title: '模块', dataIndex: 'module', width: 110, ellipsis: true },
    { title: '动作', dataIndex: 'action', width: 120, ellipsis: true },
    {
      title: '对象',
      key: 'object',
      width: 150,
      ellipsis: true,
      render: (_: unknown, record: OperationLogItem) => renderObject(record),
    },
    {
      title: '结果',
      dataIndex: 'success',
      width: 80,
      render: (v: boolean) => <SuccessTag success={v} />,
    },
    {
      title: '错误码',
      dataIndex: 'error_code',
      width: 130,
      ellipsis: true,
      render: (v?: string) => v || EMPTY_TEXT,
    },
    { title: 'IP', dataIndex: 'ip', width: 130, ellipsis: true },
    {
      title: '请求',
      key: 'request',
      width: 220,
      ellipsis: true,
      render: (_: unknown, record: OperationLogItem) =>
        record.method ? `${record.method} ${record.path ?? ''}` : record.path || EMPTY_TEXT,
    },
    ...(canViewDetail
      ? [
          {
            title: '操作',
            key: 'actions',
            fixed: 'right' as const,
            width: 70,
            render: (_: unknown, record: OperationLogItem) => (
              <Button
                type="link"
                size="small"
                style={{ paddingInline: 0 }}
                onClick={() => setDetail(record)}
              >
                详情
              </Button>
            ),
          },
        ]
      : []),
  ]

  return (
    <>
      <SfSearchForm
        fields={[
          { name: 'keyword', label: '关键词', control: 'input', placeholder: '用户名 / IP' },
          { name: 'module', label: '模块', control: 'input', placeholder: '模块编码' },
          { name: 'action', label: '动作', control: 'input', placeholder: '动作编码（如 update）' },
          { name: 'success', label: '结果', control: 'select', options: RESULT_FILTER_OPTIONS },
        ]}
        onSearch={(values) => {
          setParams({
            keyword: values.keyword as string | undefined,
            module: values.module as string | undefined,
            action: values.action as string | undefined,
            success: toSuccessFilter(values.success),
          })
          list.resetToFirstPage()
        }}
        onReset={() => list.resetToFirstPage()}
      />
      <SfTable<OperationLogItem>
        storageKey="system-log-operations"
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
        emptyText="暂无操作日志"
        scrollX={1260}
      />
      <OperationDetailDrawer record={detail} onClose={() => setDetail(null)} />
    </>
  )
}

/** 登录日志 Tab：登录成功/失败均记录（permission.md §3.3） */
function LoginLogPane() {
  const [params, setParams] = useState<LoginLogQuery>({})

  const list = usePagedList<LoginLogItem, LoginLogQuery>({
    queryKey: ['system', 'logs', 'logins'],
    fetch: (q) => systemLogApi.logins(q),
    params,
  })

  const columns: ColumnsType<LoginLogItem> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 160,
      fixed: 'left',
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    { title: '用户', dataIndex: 'username', width: 130, ellipsis: true },
    {
      title: '结果',
      dataIndex: 'success',
      width: 90,
      render: (v: boolean) => <SuccessTag success={v} />,
    },
    {
      title: '失败原因',
      dataIndex: 'fail_reason',
      width: 200,
      ellipsis: true,
      render: (v?: string) => (v ? <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT),
    },
    { title: 'IP', dataIndex: 'ip', width: 140, ellipsis: true },
    {
      title: '客户端',
      dataIndex: 'user_agent',
      width: 280,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 280 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT,
    },
  ]

  return (
    <>
      <SfSearchForm
        fields={[
          { name: 'keyword', label: '关键词', control: 'input', placeholder: '用户名 / IP' },
          { name: 'success', label: '结果', control: 'select', options: RESULT_FILTER_OPTIONS },
        ]}
        onSearch={(values) => {
          setParams({
            keyword: values.keyword as string | undefined,
            success: toSuccessFilter(values.success),
          })
          list.resetToFirstPage()
        }}
        onReset={() => list.resetToFirstPage()}
      />
      <SfTable<LoginLogItem>
        storageKey="system-log-logins"
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
        emptyText="暂无登录日志"
        scrollX={1000}
      />
    </>
  )
}

/**
 * 日志（/system/logs，menu.tsx:157 权限码 system:log:view）：
 * 双 Tab 对应操作日志 + 登录日志两类（api.md:75 /api/logs 域，sysops/routes.go:94-96 已挂载）。
 * 审计数据只读（database.md §7：可查询、可追溯、不可篡改），页面不提供任何修改入口。
 */
export default function LogPage() {
  return (
    <div className="sf-page">
      <SfPageHeader title="日志" subtitle="操作日志 / 登录日志——审计数据只读" />
      <Card size="small">
        <Tabs
          defaultActiveKey="operations"
          items={[
            { key: 'operations', label: '操作日志', children: <OperationLogPane /> },
            { key: 'logins', label: '登录日志', children: <LoginLogPane /> },
          ]}
        />
      </Card>
    </div>
  )
}
