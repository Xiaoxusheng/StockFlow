import { useState } from 'react'
import {
  Button,
  Descriptions,
  Flex,
  Modal,
  Select,
  Table,
  message,
} from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  DEVICE_LIST_PATH,
  DEVICE_TYPE_LABEL,
  deviceApi,
  type DeviceBindPayload,
  type DeviceExceptionRecord,
  type DeviceUsageRecord,
} from '@/api/device'
import { userApi } from '@/api/user'
import { OPTIONS_PAGE_SIZE, toStatusKey } from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfDeviceStatus } from '@/components/device/SfDeviceStatus'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

const USAGE_COLUMNS: ColumnsType<DeviceUsageRecord> = [
  { title: '时间', dataIndex: 'used_at', width: 170, render: (v?: string) => formatDateTime(v) },
  { title: '操作人', dataIndex: 'user_name', width: 120, render: (v?: string) => v ?? '-' },
  { title: '动作', dataIndex: 'action', width: 160, render: (v?: string) => v ?? '-' },
  { title: '备注', dataIndex: 'remark', render: (v?: string) => v ?? '-' },
]

const EXCEPTION_COLUMNS: ColumnsType<DeviceExceptionRecord> = [
  {
    title: '时间',
    dataIndex: 'occurred_at',
    width: 170,
    render: (v?: string) => formatDateTime(v),
  },
  { title: '类型', dataIndex: 'type', width: 140, render: (v?: string) => v ?? '-' },
  { title: '描述', dataIndex: 'message', render: (v?: string) => v ?? '-' },
  {
    title: '状态',
    dataIndex: 'resolved',
    width: 100,
    render: (v?: boolean) => (
      <SfStatusTag status={v ? 'resolved' : undefined} label={v ? undefined : '未解决'} semantic={v ? undefined : 'warning'} />
    ),
  },
]

/** 设备详情（/devices/:id，无菜单动态段；frontend.md §14.2 七分区结构）。
 * 后端设备域未交付时呈统一错误态；绑定/解绑/停用为真实操作，失败时以错误信息反馈 */
export default function DeviceDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [bindOpen, setBindOpen] = useState(false)
  const [bindUserId, setBindUserId] = useState<string>()

  const query = useQuery({
    queryKey: ['devices', 'detail', id],
    queryFn: () => deviceApi.get(id as string),
    enabled: Boolean(id),
  })

  // 绑定用户下拉：用户域为 M1 已交付契约（api/user.ts），失败时降级为空
  const users = useQuery({
    queryKey: ['users', 'options'],
    queryFn: () => userApi.users({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
    enabled: bindOpen,
  })
  const userOptions = (users.data?.items ?? []).map((user) => ({
    label: user.real_name ? `${user.real_name}（${user.username}）` : user.username,
    value: user.id,
  }))

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['devices'] })
  }

  const bindMutation = useMutation({
    mutationFn: (payload: DeviceBindPayload) => deviceApi.bind(id as string, payload),
    onSuccess: () => {
      messageApi.success('已绑定')
      setBindOpen(false)
      setBindUserId(undefined)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const unbindMutation = useMutation({
    mutationFn: () => deviceApi.unbind(id as string),
    onSuccess: () => {
      messageApi.success('已解绑')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const disableMutation = useMutation({
    mutationFn: () => deviceApi.disable(id as string),
    onSuccess: () => {
      messageApi.success('已停用')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleBindOk = () => {
    if (!bindUserId) {
      messageApi.warning('请选择要绑定的用户')
      return
    }
    bindMutation.mutate({ user_id: Number(bindUserId) })
  }

  if (!id) {
    return (
      <div className="sf-page">
        <SfError error={new Error('URL 缺少设备编号')} />
      </div>
    )
  }
  if (query.status === 'pending') {
    return (
      <div className="sf-page">
        <SfLoading rows={8} />
      </div>
    )
  }
  if (query.status === 'error') {
    return (
      <div className="sf-page">
        <SfError
          error={query.error}
          onRetry={query.refetch}
          description="设备详情接口不可用：GET /api/devices/{id}（后端设备域尚未交付，契约冻结后回对字段）"
        />
      </div>
    )
  }

  const detail = query.data
  const typeLabel = DEVICE_TYPE_LABEL[detail.type] ?? detail.type
  const backPath = DEVICE_LIST_PATH[detail.type]
  // 操作记录 → SfTimeline 节点（复用既有时间线组件，frontend.md §14.2 操作记录区）
  const operationSteps: SfTimelineStep[] = (detail.operation_logs ?? []).map((log) => ({
    key: log.id,
    title: log.action ?? '未命名操作',
    time: log.time,
    operator: log.operator,
    remark: log.detail,
  }))

  return (
    <div className="sf-page">
      {contextHolder}
      <SfDetailHeader
        code={detail.code}
        status={toStatusKey(detail.status)}
        summary={
          <SfSummaryBar
            items={[
              { label: '设备类型', value: typeLabel },
              { label: '设备名称', value: detail.name },
              { label: '在线状态', value: <SfDeviceStatus online={detail.online} /> },
              { label: '仓库', value: detail.warehouse_name ?? '-' },
              { label: '绑定用户', value: detail.bound_user_name ?? '-' },
              { label: 'App 版本', value: detail.app_version ?? '-' },
            ]}
          />
        }
        actions={
          <>
            <Button onClick={() => setBindOpen(true)}>绑定用户</Button>
            {detail.bound_user_id != null && (
              <SfConfirm
                title="确认解绑当前绑定用户？"
                description={`解绑后「${detail.bound_user_name ?? '该用户'}」与设备解除关联。`}
                okText="解绑"
                confirming={unbindMutation.isPending}
                onConfirm={() => unbindMutation.mutate()}
              >
                <Button>解绑</Button>
              </SfConfirm>
            )}
            {detail.status === 'ENABLED' && (
              <SfConfirm
                title="确认停用该设备？"
                description="停用后设备将无法继续作业；恢复需走启用流程。"
                okText="停用"
                confirming={disableMutation.isPending}
                onConfirm={() => disableMutation.mutate()}
              >
                <Button danger>停用</Button>
              </SfConfirm>
            )}
            <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
              刷新
            </Button>
          </>
        }
        onBack={() => (backPath ? navigate(backPath) : navigate(-1))}
      />
      <Flex vertical gap={16}>
        <SfDetailSection title="基础信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'code', label: '设备编号', children: detail.code },
              { key: 'name', label: '设备名称', children: detail.name },
              { key: 'type', label: '设备类型', children: typeLabel },
              { key: 'brand', label: '品牌', children: detail.brand ?? '-' },
              { key: 'model', label: '型号', children: detail.model ?? '-' },
              { key: 'warehouse', label: '仓库', children: detail.warehouse_name ?? '-' },
              { key: 'boundUser', label: '绑定用户', children: detail.bound_user_name ?? '-' },
              { key: 'remark', label: '备注', children: detail.remark ?? '-' },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(detail.created_at) },
              { key: 'updatedAt', label: '更新时间', children: formatDateTime(detail.updated_at) },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="在线状态">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'online', label: '在线状态', children: <SfDeviceStatus online={detail.online} /> },
              { key: 'lastOnline', label: '最后在线', children: formatDateTime(detail.last_online_at) },
              { key: 'lastSync', label: '最后同步', children: formatDateTime(detail.last_sync_at) },
              { key: 'ip', label: 'IP 地址', children: detail.ip ?? '-' },
              {
                key: 'battery',
                label: '电量',
                children:
                  detail.battery_level == null ? '-' : <span className="sf-num">{formatPercent(detail.battery_level, 0)}</span>,
              },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="软件版本">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'appVersion', label: 'App 版本', children: detail.app_version ?? '-' },
              { key: 'os', label: '操作系统', children: detail.os ?? '-' },
              {
                key: 'activatedAt',
                label: '激活时间',
                children: formatDateTime(detail.activation?.activated_at),
              },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="扫码信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'lastScan', label: '最后扫码', children: formatDateTime(detail.last_scan_at) },
              {
                key: 'scanToday',
                label: '今日扫码',
                children: <span className="sf-num">{formatNumber(detail.scan_today)}</span>,
              },
              {
                key: 'scanTotal',
                label: '累计扫码',
                children: <span className="sf-num">{formatNumber(detail.scan_total)}</span>,
              },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="使用记录">
          <Table<DeviceUsageRecord>
            size="small"
            rowKey="id"
            columns={USAGE_COLUMNS}
            dataSource={detail.usage_records ?? []}
            pagination={false}
            locale={{ emptyText: () => <SfEmpty description="该设备暂无使用记录" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="异常记录">
          <Table<DeviceExceptionRecord>
            size="small"
            rowKey="id"
            columns={EXCEPTION_COLUMNS}
            dataSource={detail.exception_records ?? []}
            pagination={false}
            scroll={{ x: 640 }}
            locale={{ emptyText: () => <SfEmpty description="该设备暂无异常记录" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="操作记录">
          <SfTimeline steps={operationSteps} emptyText="该设备暂无操作记录" />
        </SfDetailSection>
      </Flex>

      <Modal
        title="绑定用户"
        open={bindOpen}
        confirmLoading={bindMutation.isPending}
        okText="绑定"
        onOk={handleBindOk}
        onCancel={() => setBindOpen(false)}
      >
        <Select
          style={{ width: '100%' }}
          placeholder="请选择要绑定的用户"
          options={userOptions}
          value={bindUserId}
          onChange={setBindUserId}
          loading={users.isFetching}
          showSearch
          optionFilterProp="label"
        />
      </Modal>
    </div>
  )
}
