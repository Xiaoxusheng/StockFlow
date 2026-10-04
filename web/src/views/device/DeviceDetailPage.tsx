import { useMemo, useState } from 'react'
import { Button, Descriptions, Flex, Modal, Select, Typography, message } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import dayjs from 'dayjs'
import {
  ACTIVATION_STATUS_META,
  DEVICE_LIST_PATH,
  DEVICE_STATUS_PERMISSION,
  DEVICE_TYPE_LABEL,
  DEVICE_UPDATE_PERMISSION,
  deviceApi,
  type DeviceActivationStatus,
  type DeviceBindPayload,
  type DeviceDetail,
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
import { useAuthStore } from '@/stores/auth'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfDeviceStatus } from '@/components/device/SfDeviceStatus'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
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
            detail.config ? (
              <Text type="secondary">配置版本 v{detail.config_version}</Text>
            ) : undefined
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
        {/* frontend.md §14.2 规划的使用/异常/操作记录三区：设备日志 GET /api/devices/{id}/logs、
            扫码日志 /api/scanner/logs 与设备操作审计均未纳入本页数据契约（api/device.ts 未封装），
            呈真实空态说明，不造假数据 */}
        <SfDetailSection title="使用记录">
          <SfEmpty description="暂无使用记录（设备日志数据源未纳入管理端页面契约）" />
        </SfDetailSection>
        <SfDetailSection title="异常记录">
          <SfEmpty description="暂无异常记录（设备异常数据源未纳入管理端页面契约）" />
        </SfDetailSection>
        <SfDetailSection title="操作记录">
          <SfEmpty description="暂无操作记录（设备操作审计数据源未纳入管理端页面契约）" />
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
