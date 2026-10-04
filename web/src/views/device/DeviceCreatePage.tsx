import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Flex,
  Form,
  Input,
  Row,
  Col,
  Select,
  message,
} from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { toDataURL } from 'qrcode'
import {
  ACTIVATION_STATUS_META,
  DEVICE_TYPE_LABEL,
  deviceApi,
  type DeviceActivation,
  type DeviceCreatePayload,
  type DeviceId,
  type DeviceType,
} from '@/api/device'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

/** 激活状态轮询间隔（ExportTaskPage 同款模式：终态后 refetchInterval 置 false 自动停止） */
const ACTIVATION_POLL_MS = 5000

/** 二维码渲染尺寸（px） */
const QR_SIZE = 208

/**
 * 刷新恢复记录键：创建成功的激活 payload（含二维码）仅存组件内存，页面刷新即丢；
 * 记录 device_id 供激活状态查询恢复现场，二维码（qr_content）查询不可还原，
 * 需经 PUT /{id}/activation 重新生成取回（device.ts:110-111 / service_device.go:185-186）。
 */
const PENDING_ACTIVATION_KEY = 'sf.device.pending-activation'

function readPendingActivationId(): string | null {
  try {
    return sessionStorage.getItem(PENDING_ACTIVATION_KEY)
  } catch {
    // 存储不可用时降级为会话内状态
    return null
  }
}

function writePendingActivationId(id: DeviceId): void {
  try {
    sessionStorage.setItem(PENDING_ACTIVATION_KEY, String(id))
  } catch {
    // 同上
  }
}

function clearPendingActivationId(): void {
  try {
    sessionStorage.removeItem(PENDING_ACTIVATION_KEY)
  } catch {
    // 同上
  }
}

/** 表单值：仓库下拉存字符串 ID，提交时转契约的 number 外键（0=暂不绑定，service_device.go:258） */
interface DeviceFormValues {
  code: string
  name: string
  type: DeviceType
  brand?: string
  model?: string
  warehouse_id?: string
  remark?: string
}

const DEVICE_TYPE_OPTIONS = (Object.keys(DEVICE_TYPE_LABEL) as DeviceType[]).map((value) => ({
  label: DEVICE_TYPE_LABEL[value],
  value,
}))

/**
 * 二维码内容：仅使用后端组装的 qr_content（冻结 JSON 三字段 {server_url,device_code,token}，
 * service_device.go:199-210 qrContent——Scan 端激活依赖其中的一次性 token，plan §8.2）。
 * 前端不自行拼装替代内容：缺 token 的「{server_url}?device={code}」URL 码扫之无法激活；
 * qr_content 缺失（异常场景）时唯一正确取回路径是「重新生成激活码」。
 */

/** 绑定仓库展示：DeviceActivationPayload 仅携带 warehouse_id 裸 ID（service_device.go:187-197），
 * 经仓库 options 本地映射；0=未绑定仓库，映射失败降级 #ID */
function warehouseText(warehouseId: number, names: Map<string, string>): string {
  if (!warehouseId) return '未绑定仓库'
  return names.get(idKey(warehouseId)) ?? `#${idKey(warehouseId)}`
}

/** 激活二维码面板：qrcode 库真实渲染（devices.md §6.2 / frontend.md §14.3）+
 * 激活状态真实轮询与手动刷新 + 重新生成激活码（PUT /{id}/activation，旧码作废） */
function ActivationPanel({ activation, warehouseNames, regenerating, onRegenerate, onViewDetail, onCreateAnother }: {
  activation: DeviceActivation
  warehouseNames: Map<string, string>
  regenerating: boolean
  onRegenerate: () => void
  onViewDetail: () => void
  onCreateAnother: () => void
}) {
  const [qrDataUrl, setQrDataUrl] = useState<string>()
  const [qrError, setQrError] = useState<Error>()
  const qrContent = activation.qr_content

  // 本地真实渲染：内容变化时用 qrcode 库生成 PNG data URL（不走任何图片占位/假图）
  useEffect(() => {
    if (!qrContent) return
    let cancelled = false
    setQrDataUrl(undefined)
    setQrError(undefined)
    toDataURL(qrContent, { width: QR_SIZE, margin: 2 })
      .then((url) => {
        if (!cancelled) setQrDataUrl(url)
      })
      .catch((error: unknown) => {
        if (!cancelled) setQrError(error instanceof Error ? error : new Error(String(error)))
      })
    return () => {
      cancelled = true
    }
  }, [qrContent])

  // 激活状态查询：待激活时每 5s 轮询，进入终态自动停止（TanStack Query refetchInterval）
  const activationQuery = useQuery({
    queryKey: ['devices', 'activation', String(activation.device_id)],
    queryFn: () => deviceApi.activation(activation.device_id),
    refetchInterval: (query) => (query.state.data?.status === 'pending' ? ACTIVATION_POLL_MS : false),
  })
  const latest = activationQuery.data ?? activation
  const statusMeta = ACTIVATION_STATUS_META[latest.status] ?? ACTIVATION_STATUS_META.pending

  return (
    <Flex vertical gap={16}>
      <Card size="small" title="激活二维码">
        <Flex gap={24} wrap="wrap" align="flex-start">
          <Flex vertical align="center" gap={8}>
            {!qrContent ? (
              <SfEmpty
                description="二维码内容缺失（qr_content 仅在创建/重新生成时返回）；请点击下方「重新生成激活码」取回新二维码"
              />
            ) : qrError ? (
              <SfError error={qrError} description="二维码生成失败" />
            ) : qrDataUrl ? (
              <img src={qrDataUrl} width={QR_SIZE} height={QR_SIZE} alt="设备激活二维码" />
            ) : (
              <div style={{ width: QR_SIZE, height: QR_SIZE }} aria-busy="true" />
            )}
          </Flex>
          <Descriptions
            size="small"
            column={1}
            style={{ minWidth: 320, flex: 1 }}
            items={[
              { key: 'code', label: '设备编号', children: latest.device_code },
              { key: 'server', label: '服务器地址', children: latest.server_url },
              { key: 'warehouse', label: '绑定仓库', children: warehouseText(latest.warehouse_id, warehouseNames) },
              { key: 'expires', label: '有效期至', children: formatDateTime(latest.expires_at) },
              { key: 'activated', label: '激活时间', children: formatDateTime(latest.activated_at) },
            ]}
          />
        </Flex>
        <Alert
          style={{ marginTop: 16 }}
          type="info"
          showIcon
          message="请使用 StockFlow Scan 扫描该二维码完成激活"
          description="Scan 首次启动扫码后自动获取服务器地址、设备编号与仓库配置，无需人工输入（devices.md §6.2）。激活码请妥善保管，仅用于设备绑定。"
        />
      </Card>
      <Card size="small" title="激活状态">
        {activationQuery.isError ? (
          <SfError
            error={activationQuery.error}
            onRetry={activationQuery.refetch}
            description="激活状态查询失败：GET /api/devices/{id}/activation"
          />
        ) : (
          <Flex vertical gap={12}>
            <Flex gap={12} align="center" wrap="wrap">
              <SfStatusTag label={statusMeta.label} semantic={statusMeta.semantic} />
              <Button
                size="small"
                icon={<ReloadOutlined />}
                loading={activationQuery.isFetching}
                onClick={() => void activationQuery.refetch()}
              >
                刷新激活状态
              </Button>
              {activationQuery.data && (
                <span>激活状态每 {ACTIVATION_POLL_MS / 1000} 秒自动刷新，直至进入终态</span>
              )}
            </Flex>
            {latest.status === 'activated' && (
              <Alert
                type="success"
                showIcon
                message="设备已激活"
                description="该设备已完成激活并获取设备令牌，可关闭本页；后续可在设备详情查看在线与扫码情况。"
              />
            )}
          </Flex>
        )}
      </Card>
      <Flex gap={8} wrap="wrap">
        <Button type="primary" onClick={onViewDetail}>
          查看设备详情
        </Button>
        <Button onClick={onCreateAnother}>再建一台</Button>
        <SfConfirm
          title="重新生成激活二维码？"
          description="原激活码与设备端令牌将全部作废，设备回到待激活状态。"
          okText="重新生成"
          confirming={regenerating}
          onConfirm={onRegenerate}
        >
          <Button icon={<ReloadOutlined />}>重新生成激活码</Button>
        </SfConfirm>
      </Flex>
    </Flex>
  )
}

/**
 * 刷新恢复面板：创建成功的激活 payload 仅存内存，页面刷新即丢；凭 sessionStorage
 * 记录的 device_id 经 GET /{id}/activation 取回激活状态；二维码查询不可还原——
 * 待激活/过期态提供「重新生成激活码」经 PUT /{id}/activation 取回新二维码
 * （service_device.go:185-186：token 仅存哈希，查询不出参）。
 */
function RecoveredActivationPanel({ activation, warehouseNames, loading, error, onRetry, regenerating, onRegenerate, onViewDetail, onDiscard }: {
  activation?: DeviceActivation
  warehouseNames: Map<string, string>
  loading: boolean
  error: unknown
  onRetry: () => void
  regenerating: boolean
  onRegenerate: () => void
  onViewDetail: () => void
  onDiscard: () => void
}) {
  if (loading) {
    return (
      <Card size="small" title="激活状态恢复">
        <SfLoading rows={3} />
      </Card>
    )
  }
  if (error || !activation) {
    return (
      <Card size="small" title="激活状态恢复">
        <Flex vertical gap={12}>
          <SfError
            error={error ?? new Error('激活状态查询失败')}
            onRetry={onRetry}
            description="激活状态查询失败：GET /api/devices/{id}/activation"
          />
          <Button onClick={onDiscard}>返回登记表单</Button>
        </Flex>
      </Card>
    )
  }
  const statusMeta = ACTIVATION_STATUS_META[activation.status] ?? ACTIVATION_STATUS_META.pending
  return (
    <Card size="small" title="激活状态恢复">
      <Flex vertical gap={16}>
        {activation.status === 'activated' ? (
          <Alert
            type="success"
            showIcon
            message="设备已激活"
            description={`设备 ${activation.device_code} 已完成激活，本次登记流程结束；页面刷新丢失的激活二维码无需补发。`}
          />
        ) : (
          <Alert
            type="warning"
            showIcon
            message="激活二维码已随页面刷新丢失"
            description="二维码内容仅在创建或重新生成时返回（激活令牌仅存哈希，查询不可还原）。如需重新展示二维码，请重新生成激活码，原激活码与设备令牌将作废。"
          />
        )}
        <Descriptions
          bordered
          size="small"
          column={{ xs: 1, md: 2, xl: 3 }}
          items={[
            { key: 'code', label: '设备编号', children: activation.device_code },
            {
              key: 'status',
              label: '激活状态',
              children: <SfStatusTag label={statusMeta.label} semantic={statusMeta.semantic} />,
            },
            { key: 'warehouse', label: '绑定仓库', children: warehouseText(activation.warehouse_id, warehouseNames) },
            { key: 'expires', label: '有效期至', children: formatDateTime(activation.expires_at) },
            { key: 'activated', label: '激活时间', children: formatDateTime(activation.activated_at) },
          ]}
        />
        <Flex gap={8} wrap="wrap">
          {activation.status !== 'activated' && (
            <SfConfirm
              title="重新生成激活二维码？"
              description="原激活码与设备端令牌将全部作废，设备回到待激活状态。"
              okText="重新生成"
              confirming={regenerating}
              onConfirm={onRegenerate}
            >
              <Button type="primary">重新生成激活码</Button>
            </SfConfirm>
          )}
          <Button onClick={onViewDetail}>查看设备详情</Button>
          <Button onClick={onDiscard}>返回登记表单</Button>
        </Flex>
      </Flex>
    </Card>
  )
}

/**
 * 新建设备页（/devices/new，无菜单路由；frontend.md §14.3 / devices.md §6.1–6.2）：
 * 设备表单 → 提交成功返回激活二维码 payload（POST /api/devices，service_device.go:264-327）
 * → qrcode 渲染 + 激活状态轮询；页面刷新丢码时经重新生成接口取回。
 */
export default function DeviceCreatePage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<DeviceFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const [created, setCreated] = useState<DeviceActivation | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(readPendingActivationId)

  // 绑定仓库下拉：一次取全；接口失败降级为空数组，不阻塞表单其余字段
  const warehouses = useQuery({
    queryKey: ['devices', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseOptions = (warehouses.data ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))
  const warehouseNames = useMemo(() => buildWarehouseMaps(warehouses.data ?? []).name, [warehouses.data])

  const createMutation = useMutation({
    mutationFn: (payload: DeviceCreatePayload) => deviceApi.create(payload),
    onSuccess: (data) => {
      setCreated(data)
      writePendingActivationId(data.device_id)
      messageApi.success('设备已登记，请使用 StockFlow Scan 扫描激活二维码')
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 重新生成激活码（PUT /api/devices/{id}/activation，handler.go:346-358）：
  // 刷新丢失二维码 / 换码场景下二维码的唯一取回路径
  const regenMutation = useMutation({
    mutationFn: (deviceId: DeviceId) => deviceApi.regenActivation(deviceId),
    onSuccess: (data) => {
      setCreated(data)
      writePendingActivationId(data.device_id)
      // 同步轮询缓存：重新生成后回到待激活态，避免面板短暂展示重新生成前的旧状态
      queryClient.setQueryData(['devices', 'activation', String(data.device_id)], data)
      messageApi.success('已重新生成激活二维码，原激活码与设备令牌已作废')
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 刷新恢复：created 内存态丢失但记录了 device_id 时，经激活状态查询恢复现场
  const recoveredQuery = useQuery({
    queryKey: ['devices', 'activation', 'recover', pendingId],
    queryFn: () => deviceApi.activation(pendingId as string),
    enabled: !created && Boolean(pendingId),
  })

  const handleSubmit = (values: DeviceFormValues) => {
    createMutation.mutate({
      code: values.code.trim(),
      name: values.name.trim(),
      type: values.type,
      brand: values.brand,
      model: values.model,
      warehouse_id: values.warehouse_id ? Number(values.warehouse_id) : 0,
      remark: values.remark,
    })
  }

  const handleCreateAnother = () => {
    clearPendingActivationId()
    setPendingId(null)
    createMutation.reset()
    setCreated(null)
    form.resetFields()
  }

  const handleViewDetail = (deviceId: DeviceId) => {
    clearPendingActivationId()
    setPendingId(null)
    navigate(`/devices/${encodeURIComponent(String(deviceId))}`)
  }

  const handleDiscardPending = () => {
    clearPendingActivationId()
    setPendingId(null)
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="新建设备"
        subtitle="登记设备并生成激活二维码（devices.md §6.1–6.2）"
        onBack={() => navigate(-1)}
      />
      {created ? (
        <ActivationPanel
          activation={created}
          warehouseNames={warehouseNames}
          regenerating={regenMutation.isPending}
          onRegenerate={() => regenMutation.mutate(created.device_id)}
          onViewDetail={() => handleViewDetail(created.device_id)}
          onCreateAnother={handleCreateAnother}
        />
      ) : pendingId ? (
        <RecoveredActivationPanel
          activation={recoveredQuery.data}
          warehouseNames={warehouseNames}
          loading={recoveredQuery.isPending}
          error={recoveredQuery.error}
          onRetry={() => void recoveredQuery.refetch()}
          regenerating={regenMutation.isPending}
          onRegenerate={() => regenMutation.mutate(pendingId)}
          onViewDetail={() => handleViewDetail(pendingId)}
          onDiscard={handleDiscardPending}
        />
      ) : (
        <Card size="small" title="设备信息">
          {createMutation.isError && (
            <Alert
              type="error"
              showIcon
              message={resolveErrorMessage(createMutation.error)}
              description="创建失败：请检查设备编号是否重复、仓库是否有效后重试（POST /api/devices）。"
              style={{ marginBottom: 16 }}
            />
          )}
          <Form<DeviceFormValues>
            form={form}
            layout="vertical"
            onFinish={handleSubmit}
            style={{ maxWidth: 720 }}
          >
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item
                  name="code"
                  label="设备编号"
                  rules={[{ required: true, message: '请输入设备编号' }]}
                  extra="设备注册主键，2–64 位字母数字开头，如 SF-SCAN-001（devices.md §6.1）"
                >
                  <Input placeholder="唯一设备编号" maxLength={64} />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item
                  name="name"
                  label="设备名称"
                  rules={[{ required: true, message: '请输入设备名称' }]}
                >
                  <Input placeholder="请输入设备名称" maxLength={128} />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item
                  name="type"
                  label="设备类型"
                  rules={[{ required: true, message: '请选择设备类型' }]}
                >
                  <Select options={DEVICE_TYPE_OPTIONS} placeholder="请选择设备类型" />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item name="warehouse_id" label="绑定仓库" extra="作为设备初始配置仓库，可暂不绑定（devices.md §6.3）">
                  <Select options={warehouseOptions} placeholder="请选择绑定仓库" allowClear />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item name="brand" label="品牌">
                  <Input placeholder="请输入品牌" maxLength={64} />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item name="model" label="型号">
                  <Input placeholder="请输入型号" maxLength={64} />
                </Form.Item>
              </Col>
              <Col span={24}>
                <Form.Item name="remark" label="备注">
                  <Input.TextArea rows={2} placeholder="请输入备注" maxLength={500} />
                </Form.Item>
              </Col>
            </Row>
            <Flex gap={8}>
              <Button type="primary" htmlType="submit" loading={createMutation.isPending}>
                创建并生成激活二维码
              </Button>
              <Button onClick={() => navigate(-1)}>取消</Button>
            </Flex>
          </Form>
        </Card>
      )}
    </div>
  )
}
