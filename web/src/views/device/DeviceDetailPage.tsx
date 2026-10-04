import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Alert, Button, Col, Descriptions, Flex, Form, Input, InputNumber, Modal, Row, Select, Switch, Typography, message } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import dayjs from 'dayjs'
import {
  ACTIVATION_STATUS_META,
  DEVICE_LIST_PATH,
  DEVICE_SCANLOG_LIST_PERMISSION,
  DEVICE_STATUS_PERMISSION,
  DEVICE_TYPE_LABEL,
  DEVICE_UPDATE_PERMISSION,
  DEVICE_LOG_LEVEL_META,
  deviceApi,
  type DeviceActivationStatus,
  type DeviceBindPayload,
  type DeviceConfigPayload,
  type DeviceDetail,
  type DeviceLogLevel,
  type DeviceLogItem,
  type DeviceLogQuery,
  type ScanLogItem,
  type ScanLogQuery,
} from '@/api/device'
import {
  buildUserNameMap,
  buildWarehouseMaps,
  fetchUserOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import { toStatusKey } from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfDeviceStatus } from '@/components/device/SfDeviceStatus'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

const { Text } = Typography

/**
 * 激活态展示派生：DeviceDetailView 不含派生位（仅激活码载荷携带 status 派生态），
 * 按后端 deriveActivationStatus（service_device.go:213-221）同口径前端派生，仅用于展示：
 * disabled > activated > expired（expires_at 已过且未激活）> pending。
 */
function deriveActivationStatus(detail: DeviceDetail): DeviceActivationStatus {
  if (detail.status === 'DISABLED') return 'disabled'
  if (detail.activation_status === 'ACTIVATED') return 'activated'
  if (detail.activation_expires_at && dayjs(detail.activation_expires_at).valueOf() <= Date.now()) {
    return 'expired'
  }
  return 'pending'
}

/** 设备配置键 → 展示名（devices.md §7.3 冻结下发项，与 service_device.go:38-48 白名单同源；未知键显示原始键名） */
const CONFIG_KEY_LABELS: Record<string, string> = {
  scan_mode: '扫码模式',
  sound: '声音',
  vibrate: '震动',
  auto_focus: '自动聚焦',
  continuous_scan: '连续扫码',
  scan_timeout_seconds: '扫码超时（秒）',
  default_warehouse_id: '默认仓库',
  task_refresh_seconds: '任务刷新间隔（秒）',
  auto_lock_minutes: '自动锁屏（分钟）',
}

function formatConfigValue(key: string, value: unknown, warehouseNames: Map<string, string>): string {
  if (typeof value === 'boolean') return value ? '开' : '关'
  if (key === 'default_warehouse_id' && typeof value === 'number' && value > 0) {
    return warehouseNames.get(idKey(value)) ?? `#${idKey(value)}`
  }
  if (value === null || value === undefined) return '-'
  return String(value)
}

/** 裸 ID 展示：DeviceView 不联表下发名称（device.ts:6-9 注），经 options 映射，
 * 0=未绑定，映射失败降级 #ID，不造假数据 */
function idRefText(value: number, names: Map<string, string>, emptyText: string): string {
  if (!value) return emptyText
  return names.get(idKey(value)) ?? `#${idKey(value)}`
}

/** 配置下发表单值（九键白名单，devices.md §7.3；仓库下拉存字符串 ID 提交转 number） */
interface DeviceConfigFormValues {
  scan_mode?: string
  sound?: boolean
  vibrate?: boolean
  auto_focus?: boolean
  continuous_scan?: boolean
  scan_timeout_seconds?: number | null
  default_warehouse_id?: string
  task_refresh_seconds?: number | null
  auto_lock_minutes?: number | null
}

/** 日志级别标签（级别不在 types/status.ts 注册表，经 SfStatusTag 显式指定，frontend.md §24） */
function renderLogLevel(level?: string): ReactNode {
  const meta = level ? DEVICE_LOG_LEVEL_META[level as DeviceLogLevel] : undefined
  return <SfStatusTag label={meta?.label ?? level ?? '-'} semantic={meta?.semantic ?? 'neutral'} />
}

/** 扫码结果标签（scan_logs.success） */
function renderScanSuccess(success?: boolean): ReactNode {
  return success ? (
    <SfStatusTag label="成功" semantic="success" />
  ) : (
    <SfStatusTag label="失败" semantic="danger" />
  )
}

/**
 * 设备日志分区表（GET /api/devices/{id}/logs，handler.go:194/:503-520，devices.md §7.1
 * 「查看设备日志」）：使用记录不传 level 取全量，异常记录传 level=ERROR 取错误子集
 * （chk_device_logs_level 值域 INFO/WARN/ERROR）。详情页内小表固定每页 5 行。
 */
function DeviceLogSection({ deviceId, level, emptyText }: { deviceId: string; level?: DeviceLogLevel; emptyText: string }) {
  const list = usePagedList<DeviceLogItem, DeviceLogQuery>({
    queryKey: ['devices', 'logs', deviceId, level ?? 'ALL'],
    fetch: (query) => deviceApi.logs(deviceId, query),
    params: level ? { level } : {},
    defaultPageSize: 5,
  })

  const columns: ColumnsType<DeviceLogItem> = [
    { title: '发生时间', dataIndex: 'occurred_at', width: 160, render: (value: string) => formatDateTime(value) },
    { title: '级别', dataIndex: 'level', width: 80, render: renderLogLevel },
    { title: '事件', dataIndex: 'event_type', width: 150, ellipsis: true, render: (value?: string) => value || '-' },
    { title: '消息', dataIndex: 'message', ellipsis: true, render: (value?: string) => value || '-' },
  ]

  return (
    <SfTable<DeviceLogItem>
      columns={columns}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText={emptyText}
      scrollX={760}
      showDensity={false}
      showColumnSetting={false}
      showFullscreen={false}
    />
  )
}

/**
 * 操作记录分区表（GET /api/scanner/logs?device_id=，handler.go:197/:529-581）：
 * 数据源为扫码审计 scan_logs——谁、在哪台设备、什么时候、扫了什么（devices.md §13.2）；
 * 需 devices:scanlog:list 权限（后端 RequirePermission），无权限时如实说明、不发无效请求。
 */
function ScanLogSection({ deviceId, canView }: { deviceId: string; canView: boolean }) {
  const list = usePagedList<ScanLogItem, ScanLogQuery>({
    queryKey: ['devices', 'scan-logs', deviceId],
    fetch: (query) => deviceApi.scanLogs({ ...query, device_id: deviceId }),
    params: {},
    defaultPageSize: 5,
    enabled: canView,
  })

  const columns: ColumnsType<ScanLogItem> = [
    { title: '时间', dataIndex: 'created_at', width: 160, render: (value: string) => formatDateTime(value) },
    { title: '操作人', dataIndex: 'username', width: 110, ellipsis: true, render: (value?: string) => value || '-' },
    { title: '识别类型', dataIndex: 'resolve_type', width: 90, render: (value?: string) => value || '-' },
    { title: '识别对象', dataIndex: 'resolve_code', width: 160, ellipsis: true, render: (value?: string) => value || '-' },
    { title: '扫码内容', dataIndex: 'raw_code', width: 180, ellipsis: true, render: (value?: string) => value || '-' },
    { title: '结果', dataIndex: 'success', width: 80, render: (value: boolean) => renderScanSuccess(value) },
  ]

  if (!canView) {
    return <SfEmpty description="查看扫码操作记录需要 devices:scanlog:list 权限（当前账号未持有，后端将拒绝该查询）" />
  }

  return (
    <SfTable<ScanLogItem>
      columns={columns}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      onRefresh={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      emptyText="该设备暂无扫码记录；扫码解析（/api/scanner/resolve）落审计后在此展示（devices.md §13.2）"
      scrollX={940}
      showDensity={false}
      showColumnSetting={false}
      showFullscreen={false}
    />
  )
}

/**
 * 设备详情（/devices/:id，无菜单动态段；frontend.md §14.2 分区结构）。
 * 出参 DeviceDetailView = Device 裸模型 + online/config/config_version/scan_total/scan_today
 * （internal/devices/service_device.go:172-180）。绑定/解绑/停用对应 POST /:id/bind|unbind|disable
 * （handler.go:190-192；bind/unbind 需 devices:device:update、disable 需 devices:device:status，
 * 前端按权限快照显隐仅为体验优化，后端 RequirePermission 为准）。操作成功后失效 devices 缓存刷新。
 */
export default function DeviceDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [bindOpen, setBindOpen] = useState(false)
  const [bindUserId, setBindUserId] = useState<string>()
  const [configOpen, setConfigOpen] = useState(false)
  const [configForm] = Form.useForm<DeviceConfigFormValues>()
  const permissions = useAuthStore((s) => s.permissions)

  /** dev 会话权限快照为 ['*'] 全通过（LoginPage:240-242） */
  const hasPerm = (code: string) => permissions.includes('*') || permissions.includes(code)

  const query = useQuery({
    queryKey: ['devices', 'detail', id],
    queryFn: () => deviceApi.get(id as string),
    enabled: Boolean(id),
  })

  // 仓库/用户 options（GET /api/warehouses、/api/users）：warehouse_id/bound_user_id/activated_by
  // 为裸 ID，本地映射展示，拉取失败降级 #ID（api/options.ts 约定）；用户 options 同时供绑定弹窗
  const warehouses = useQuery({
    queryKey: ['devices', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const users = useQuery({
    queryKey: ['devices', 'options', 'users'],
    queryFn: fetchUserOptions,
  })
  const warehouseNames = useMemo(() => buildWarehouseMaps(warehouses.data ?? []).name, [warehouses.data])
  // 配置下发弹窗「默认仓库」下拉（device_configs.default_warehouse_id int 键；DeviceCreatePage 同款映射）
  const warehouseOptions = useMemo(
    () =>
      (warehouses.data ?? []).map((item) => ({
        label: `${item.name}（${item.code}）`,
        value: String(item.id),
      })),
    [warehouses.data],
  )
  const userNames = useMemo(() => buildUserNameMap(users.data ?? []), [users.data])
  const userOptions = useMemo(
    () =>
      (users.data ?? []).map((user) => ({
        label: user.real_name ? `${user.real_name}（${user.username}）` : user.username,
        value: user.id,
      })),
    [users.data],
  )

  // 三 mutation 成功统一失效 devices 缓存：详情查询（['devices','detail',id]）随之 refetch
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

  // 配置下发（PUT /api/devices/{id}/config，devices.md §7.3 白名单校验）：后端
  // UpsertDeviceConfig 为整体覆盖（repository.go:292 SET config = EXCLUDED.config），
  // 表单按现有配置全量预填、提交全量值——清空的键即从配置中移除，与界面所见一致
  const configMutation = useMutation({
    mutationFn: (payload: DeviceConfigPayload) => deviceApi.config(id as string, payload),
    onSuccess: (result) => {
      setConfigOpen(false)
      messageApi.success(`配置已下发（版本 v${result.version}），设备端将自动同步（devices.md §7.3）`)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openConfigModal = (detail: DeviceDetail) => {
    configMutation.reset()
    // detail.config 为 jsonb 原样出参，键值类型由后端白名单约束，收窄为表单值类型
    const currentConfig = (detail.config ?? {}) as Partial<DeviceConfigPayload>
    configForm.setFieldsValue({
      scan_mode: currentConfig.scan_mode,
      sound: currentConfig.sound ?? false,
      vibrate: currentConfig.vibrate ?? false,
      auto_focus: currentConfig.auto_focus ?? false,
      continuous_scan: currentConfig.continuous_scan ?? false,
      scan_timeout_seconds: currentConfig.scan_timeout_seconds,
      default_warehouse_id: currentConfig.default_warehouse_id
        ? String(currentConfig.default_warehouse_id)
        : undefined,
      task_refresh_seconds: currentConfig.task_refresh_seconds,
      auto_lock_minutes: currentConfig.auto_lock_minutes,
    })
    setConfigOpen(true)
  }

  const handleConfigSubmit = () => {
    configForm
      .validateFields()
      .then((values) => {
        const payload: DeviceConfigPayload = {}
        const scanMode = values.scan_mode?.trim()
        if (scanMode) payload.scan_mode = scanMode
        if (values.sound !== undefined) payload.sound = values.sound
        if (values.vibrate !== undefined) payload.vibrate = values.vibrate
        if (values.auto_focus !== undefined) payload.auto_focus = values.auto_focus
        if (values.continuous_scan !== undefined) payload.continuous_scan = values.continuous_scan
        if (values.scan_timeout_seconds != null) payload.scan_timeout_seconds = values.scan_timeout_seconds
        if (values.default_warehouse_id) payload.default_warehouse_id = Number(values.default_warehouse_id)
        if (values.task_refresh_seconds != null) payload.task_refresh_seconds = values.task_refresh_seconds
        if (values.auto_lock_minutes != null) payload.auto_lock_minutes = values.auto_lock_minutes
        if (Object.keys(payload).length === 0) {
          messageApi.warning('请至少填写一项配置')
          return
        }
        configMutation.mutate(payload)
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

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
          description="设备详情加载失败：GET /api/devices/{id}，可点击重试"
        />
      </div>
    )
  }

  const detail = query.data
  const typeLabel = DEVICE_TYPE_LABEL[detail.type] ?? detail.type
  const backPath = DEVICE_LIST_PATH[detail.type]
  const activationMeta = ACTIVATION_STATUS_META[deriveActivationStatus(detail)]
  const enabled = detail.status === 'ENABLED'
  const warehouseName = idRefText(detail.warehouse_id, warehouseNames, '未绑定仓库')
  const boundUserName = idRefText(detail.bound_user_id, userNames, '未绑定')

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
              { label: '仓库', value: warehouseName },
              { label: '绑定用户', value: boundUserName },
              { label: 'App 版本', value: detail.app_version || '-' },
            ]}
          />
        }
        actions={
          <>
            {enabled && hasPerm(DEVICE_UPDATE_PERMISSION) && (
              <Button
                onClick={() => {
                  setBindUserId(undefined)
                  setBindOpen(true)
                }}
              >
                绑定用户
              </Button>
            )}
            {enabled && hasPerm(DEVICE_UPDATE_PERMISSION) && detail.bound_user_id > 0 && (
              <SfConfirm
                title="确认解绑当前绑定用户？"
                description={`解绑后「${boundUserName}」与设备解除关联，设备端现有登录令牌将全部失效（token_version+1）。`}
                okText="解绑"
                confirming={unbindMutation.isPending}
                onConfirm={() => unbindMutation.mutate()}
              >
                <Button>解绑</Button>
              </SfConfirm>
            )}
            {enabled && hasPerm(DEVICE_STATUS_PERMISSION) && (
              <SfConfirm
                title="确认停用该设备？"
                description="停用后设备端令牌立即失效（token_version+1），设备将无法继续作业。"
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
              { key: 'brand', label: '品牌', children: detail.brand || '-' },
              { key: 'model', label: '型号', children: detail.model || '-' },
              { key: 'warehouse', label: '仓库', children: warehouseName },
              { key: 'boundUser', label: '绑定用户', children: boundUserName },
              {
                key: 'activation',
                label: '激活状态',
                children: <SfStatusTag label={activationMeta.label} semantic={activationMeta.semantic} />,
              },
              { key: 'expires', label: '有效期至', children: formatDateTime(detail.activation_expires_at) },
              { key: 'activatedAt', label: '激活时间', children: formatDateTime(detail.activated_at) },
              {
                key: 'activatedBy',
                label: '激活操作者',
                children: detail.activated_by > 0 ? idRefText(detail.activated_by, userNames, '-') : '-',
              },
              { key: 'remark', label: '备注', children: detail.remark || '-' },
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
              { key: 'ip', label: 'IP 地址', children: detail.ip || '-' },
              {
                key: 'battery',
                label: '电量',
                children:
                  detail.battery_level == null ? (
                    '-'
                  ) : (
                    <span className="sf-num">{formatPercent(detail.battery_level, 0)}</span>
                  ),
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
              { key: 'appVersion', label: 'App 版本', children: detail.app_version || '-' },
              { key: 'os', label: '操作系统', children: detail.os || '-' },
              {
                key: 'tokenVersion',
                label: '令牌版本',
                children: <span className="sf-num">{formatNumber(detail.token_version)}</span>,
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
              { key: 'lastScan', label: '最后扫码', children: formatDateTime(detail.last_scan_at) },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection
          title="设备配置"
          extra={
            <Flex gap={8} align="center">
              {detail.config ? <Text type="secondary">配置版本 v{detail.config_version}</Text> : null}
              {enabled && hasPerm(DEVICE_UPDATE_PERMISSION) && (
                <Button size="small" onClick={() => openConfigModal(detail)}>
                  下发配置
                </Button>
              )}
            </Flex>
          }
        >
          {detail.config && Object.keys(detail.config).length > 0 ? (
            <Descriptions
              bordered
              size="small"
              column={{ xs: 1, md: 2, xl: 3 }}
              items={Object.entries(detail.config).map(([key, value]) => ({
                key,
                label: CONFIG_KEY_LABELS[key] ?? key,
                children: formatConfigValue(key, value, warehouseNames),
              }))}
            />
          ) : (
            <SfEmpty description="该设备尚未下发配置（devices.md §7.3：下发后 Scan 端自动同步）" />
          )}
        </SfDetailSection>
        {/* frontend.md §14.2 使用/异常/操作记录三区：使用记录=设备运行日志全量、异常记录=ERROR
            子集（GET /api/devices/{id}/logs，handler.go:194），操作记录=扫码审计
            （GET /api/scanner/logs?device_id，handler.go:197）——数据源已封装（api/device.ts），
            空态为真实无数据；设备操作审计（绑定/解绑/停用）后端无管理端查询端点，未纳入 */}
        <SfDetailSection title="使用记录">
          <DeviceLogSection
            deviceId={String(detail.id)}
            emptyText="该设备暂无运行日志；设备端批量上报（device_logs）后在此展示"
          />
        </SfDetailSection>
        <SfDetailSection title="异常记录">
          <DeviceLogSection
            deviceId={String(detail.id)}
            level="ERROR"
            emptyText="暂无异常记录；设备运行日志 ERROR 级别条目将在此展示"
          />
        </SfDetailSection>
        <SfDetailSection title="操作记录">
          <ScanLogSection deviceId={String(detail.id)} canView={hasPerm(DEVICE_SCANLOG_LIST_PERMISSION)} />
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

      <Modal
        title="下发设备配置"
        open={configOpen}
        width={560}
        forceRender
        confirmLoading={configMutation.isPending}
        okText="下发"
        onOk={handleConfigSubmit}
        onCancel={() => setConfigOpen(false)}
      >
        {configMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(configMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="下发展为整体覆盖：表单已按当前配置预填，清空某项并下发即从配置中移除该项"
        />
        {/* 键集 = devices.md §7.3 冻结九键（service_device.go:38-48 白名单同源）；
            后端值校验：bool 开关 / 非负整数 / scan_mode 1-64 字符字符串 */}
        <Form<DeviceConfigFormValues> form={configForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="scan_mode" label="扫码模式" extra="1–64 字符，留空=移除该项">
                <Input placeholder="如：continuous" maxLength={64} allowClear />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="default_warehouse_id" label="默认仓库" extra="留空=移除该项">
                <Select
                  options={warehouseOptions}
                  placeholder="请选择默认仓库"
                  allowClear
                  loading={warehouses.isFetching}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="sound" label="声音" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="vibrate" label="震动" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="auto_focus" label="自动聚焦" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="continuous_scan" label="连续扫码" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="scan_timeout_seconds" label="扫码超时（秒）" extra="0 或留空=移除该项">
                <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="非负整数" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="task_refresh_seconds" label="任务刷新间隔（秒）" extra="0 或留空=移除该项">
                <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="非负整数" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="auto_lock_minutes" label="自动锁屏（分钟）" extra="0=关闭自动锁屏，留空=移除该项">
                <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="非负整数" />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
