import { useState } from 'react'
import { Button, Card, Modal, Popconfirm, Tooltip, Typography, message } from 'antd'
import { CloudUploadOutlined, DownloadOutlined, FileTextOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  systemApi,
  SYSTEM_BACKUP_CREATE_PERMISSION,
  SYSTEM_BACKUP_READ_PERMISSION,
  type SystemBackupItem,
  type SystemBackupQuery,
  type SystemBackupStatus,
  type SystemBackupTrigger,
  type SystemPgDumpTemplate,
} from '@/api/system'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime, formatFileSize } from '@/utils/format'

const { Text } = Typography

/** 备份状态语义（000014 chk_backup_records_status 值域；REQUESTED=已登记待部署侧执行器拾取） */
const BACKUP_STATUS_META: Record<SystemBackupStatus, { label: string; semantic: 'pending' | 'processing' | 'success' | 'danger' }> = {
  REQUESTED: { label: '待执行', semantic: 'pending' },
  RUNNING: { label: '执行中', semantic: 'processing' },
  SUCCESS: { label: '成功', semantic: 'success' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 触发方式（backups.go:40：AUTO=部署侧执行器调度 / MANUAL=管理端登记） */
const TRIGGER_META: Record<SystemBackupTrigger, { label: string; semantic: 'processing' | 'neutral' }> = {
  AUTO: { label: '自动调度', semantic: 'neutral' },
  MANUAL: { label: '手动登记', semantic: 'processing' },
}

const STATUS_FILTER_OPTIONS: Array<{ label: string; value: SystemBackupStatus }> = [
  { label: '待执行', value: 'REQUESTED' },
  { label: '执行中', value: 'RUNNING' },
  { label: '成功', value: 'SUCCESS' },
  { label: '失败', value: 'FAILED' },
]

/** 触发浏览器下载已拿到的 Blob（后端 Content-Disposition 由 axios blob 模式不可见，文件名取记录行） */
function saveBlob(blob: Blob, fileName: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

/** pg_dump 模板弹窗（GET /api/system/backups/pg-dump-template；密码不进模板——backups.go:174-181） */
function PgDumpTemplateModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const templateQuery = useQuery({
    queryKey: ['system', 'backup', 'pg-dump-template'],
    queryFn: () => systemApi.backups.pgDumpTemplate(),
    enabled: open,
  })
  const data: SystemPgDumpTemplate | undefined = templateQuery.data

  return (
    <Modal title="pg_dump 备份命令模板" open={open} footer={null} onCancel={onClose} width={720}>
      {templateQuery.isPending ? (
        <Text type="secondary">加载中…</Text>
      ) : templateQuery.error ? (
        <Text type="danger">{resolveErrorMessage(templateQuery.error)}</Text>
      ) : data ? (
        <>
          <Typography.Paragraph copyable={{ text: data.command }}>
            <Text code style={{ fontSize: 12, wordBreak: 'break-all' }}>
              {data.command}
            </Text>
          </Typography.Paragraph>
          <Typography.Paragraph copyable={{ text: data.crontab_line }}>
            <Text code style={{ fontSize: 12 }}>
              {data.crontab_line}
            </Text>
          </Typography.Paragraph>
          <ul style={{ margin: '12px 0 0', paddingLeft: 20 }}>
            {data.notes.map((note) => (
              <li key={note}>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {note}
                </Text>
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </Modal>
  )
}

/**
 * 数据备份（/system/backups，端点 GET/POST /api/system/backups、/:id/download、
 * /pg-dump-template——internal/sysops/routes.go:111-114 全量接线）：
 * 裁决②混合模式——应用侧只登记（REQUESTED）/ 列表 / 下载 / 模板查看，
 * pg_dump 由部署侧执行器拾取 REQUESTED 执行并回写（deployment.md §4）。
 */
export default function BackupPage() {
  const [templateOpen, setTemplateOpen] = useState(false)
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  // fail-closed：权限点集为空一律隐藏登记/下载入口（前端权限仅体验优化，后端必须校验）
  const canCreate = canAccess(user, SYSTEM_BACKUP_CREATE_PERMISSION)
  const canRead = canAccess(user, SYSTEM_BACKUP_READ_PERMISSION)

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<SystemBackupItem, SystemBackupQuery>({
    queryKey: ['system', 'backups'],
    fetch: (q) => systemApi.backups.list(q),
    urlSync: true,
  })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['system', 'backups'] })

  const registerMutation = useMutation({
    mutationFn: () => systemApi.backups.register(),
    onSuccess: (result) => {
      message.success(result.message || '备份已登记，等待部署侧执行器拾取')
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const downloadMutation = useMutation({
    mutationFn: (record: SystemBackupItem) => systemApi.backups.download(record.id),
    onSuccess: (blob, record) => {
      saveBlob(blob, record.file_name || `backup-${record.id}.dump`)
      message.success('备份文件已开始下载')
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const columns: ColumnsType<SystemBackupItem> = [
    { title: 'ID', dataIndex: 'id', width: 80 },
    {
      title: '文件名',
      dataIndex: 'file_name',
      width: 240,
      ellipsis: true,
      render: (v?: string) => (v ? <Text style={{ maxWidth: 240 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT),
    },
    {
      title: '大小',
      dataIndex: 'size_bytes',
      width: 110,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{v ? formatFileSize(v) : EMPTY_TEXT}</span>,
    },
    {
      title: '触发方式',
      dataIndex: 'trigger',
      width: 100,
      render: (v: SystemBackupTrigger) => {
        const meta = TRIGGER_META[v]
        return meta ? <SfStatusTag label={meta.label} semantic={meta.semantic} /> : v
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: SystemBackupStatus) => {
        const meta = BACKUP_STATUS_META[v]
        return meta ? <SfStatusTag label={meta.label} semantic={meta.semantic} /> : <SfStatusTag label={v} semantic="neutral" />
      },
    },
    {
      title: '说明',
      dataIndex: 'message',
      width: 200,
      ellipsis: true,
      render: (v?: string) => (v ? <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text> : EMPTY_TEXT),
    },
    {
      title: '开始时间',
      dataIndex: 'started_at',
      width: 150,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '结束时间',
      dataIndex: 'finished_at',
      width: 150,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '登记时间',
      dataIndex: 'created_at',
      width: 150,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      fixed: 'right',
      render: (_, record) => {
        // 仅 SUCCESS 记录可下载（backups.go backupFilePath：非 SUCCESS / 空 file_path 拒绝）
        const downloadable = canRead && record.status === 'SUCCESS'
        return (
          <Tooltip title={record.status !== 'SUCCESS' ? '仅成功的备份可下载' : undefined}>
            <Button
              type="link"
              size="small"
              icon={<DownloadOutlined />}
              disabled={!downloadable}
              loading={downloadMutation.isPending && downloadMutation.variables?.id === record.id}
              onClick={() => downloadMutation.mutate(record)}
            >
              下载
            </Button>
          </Tooltip>
        )
      },
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="数据备份"
        subtitle="备份登记 / 记录查询 / 下载（pg_dump 由部署侧执行器拾取执行）"
        extra={
          <>
            <Button icon={<FileTextOutlined />} onClick={() => setTemplateOpen(true)}>
              pg_dump 模板
            </Button>
            <Popconfirm
              title="确认登记备份任务？"
              description="将生成 REQUESTED 记录，由部署侧执行器拾取执行（同一时间仅允许一个在途登记）"
              onConfirm={() => registerMutation.mutate()}
              disabled={!canCreate}
            >
              <Button
                type="primary"
                icon={<CloudUploadOutlined />}
                loading={registerMutation.isPending}
                disabled={!canCreate}
              >
                登记备份
              </Button>
            </Popconfirm>
          </>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[{ name: 'status', label: '状态', control: 'select', options: STATUS_FILTER_OPTIONS }]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          onReset={() => list.resetToFirstPage()}
        />
        <SfTable<SystemBackupItem>
          storageKey="system-backups"
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
          emptyText="暂无备份记录"
          scrollX={1420}
        />
      </Card>

      <PgDumpTemplateModal open={templateOpen} onClose={() => setTemplateOpen(false)} />
    </div>
  )
}
