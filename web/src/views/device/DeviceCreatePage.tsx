import { useEffect, useState } from 'react'
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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { toDataURL } from 'qrcode'
import {
  ACTIVATION_STATUS_META,
  DEVICE_TYPE_LABEL,
  deviceApi,
  type DeviceActivation,
  type DeviceCreatePayload,
  type DeviceType,
} from '@/api/device'
import { warehouseApi } from '@/api/warehouse'
import { OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

/** 激活状态轮询间隔（ExportTaskPage 同款模式：终态后 refetchInterval 置 false 自动停止） */
const ACTIVATION_POLL_MS = 5000

/** 二维码渲染尺寸（px） */
const QR_SIZE = 208

/** 表单值：仓库下拉存字符串 ID，提交时转契约的 number 外键 */
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

/** 二维码内容：契约优先取后端组装的 qr_content；缺失时按 devices.md §6.1
 * 「设备注册输入=设备编码+服务器地址」以 server_url + device_code 兜底拼接 */
function buildQrContent(activation: DeviceActivation): string {
  if (activation.qr_content) return activation.qr_content
  return `${activation.server_url}?device=${encodeURIComponent(activation.device_code)}`
}

/** 激活二维码面板：qrcode 库真实渲染（devices.md §6.2 / frontend.md §14.3）+
 * 激活状态真实轮询与手动刷新（后端未交付时呈统一错误态，不做假激活） */
function ActivationPanel({ activation, onViewDetail, onCreateAnother }: {
  activation: DeviceActivation
  onViewDetail: () => void
  onCreateAnother: () => void
}) {
  const [qrDataUrl, setQrDataUrl] = useState<string>()
  const [qrError, setQrError] = useState<Error>()
  const qrContent = buildQrContent(activation)

  // 本地真实渲染：内容变化时用 qrcode 库生成 PNG data URL（不走任何图片占位/假图）
  useEffect(() => {
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
            {qrError ? (
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
              { key: 'warehouse', label: '绑定仓库', children: latest.warehouse_name ?? '未绑定仓库' },
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
            description="激活状态接口不可用：GET /api/devices/{id}/activation（后端设备域尚未交付）"
          />
        ) : (
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
        )}
      </Card>
      <Flex gap={8}>
        <Button type="primary" onClick={onViewDetail}>
          查看设备详情
        </Button>
        <Button onClick={onCreateAnother}>再建一台</Button>
      </Flex>
    </Flex>
  )
}

/**
 * 新建设备页（/devices/new，无菜单路由；frontend.md §14.3 / devices.md §6.1–6.2）：
 * 设备表单 → 提交成功返回激活二维码 payload → qrcode 渲染 + 激活状态轮询。
 * 后端 POST /api/devices 未交付时提交呈统一错误态。
 */
export default function DeviceCreatePage() {
  const navigate = useNavigate()
  const [form] = Form.useForm<DeviceFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const [created, setCreated] = useState<DeviceActivation | null>(null)

  // 绑定仓库下拉：一次取全；接口失败降级为空数组，不阻塞表单其余字段
  const warehouses = useQuery({
    queryKey: ['warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const warehouseOptions = (warehouses.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))

  const createMutation = useMutation({
    mutationFn: (payload: DeviceCreatePayload) => deviceApi.create(payload),
    onSuccess: (data) => {
      setCreated(data)
      messageApi.success('设备已登记，请使用 StockFlow Scan 扫描激活二维码')
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSubmit = (values: DeviceFormValues) => {
    createMutation.mutate({
      code: values.code.trim(),
      name: values.name.trim(),
      type: values.type,
      brand: values.brand,
      model: values.model,
      warehouse_id: values.warehouse_id != null ? Number(values.warehouse_id) : null,
      remark: values.remark,
    })
  }

  const handleCreateAnother = () => {
    createMutation.reset()
    setCreated(null)
    form.resetFields()
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
          onViewDetail={() => navigate(`/devices/${encodeURIComponent(String(created.device_id))}`)}
          onCreateAnother={handleCreateAnother}
        />
      ) : (
        <Card size="small" title="设备信息">
          {createMutation.isError && (
            <Alert
              type="error"
              showIcon
              message={resolveErrorMessage(createMutation.error)}
              description="创建接口不可用：POST /api/devices（后端设备域尚未交付，契约冻结后回对字段）"
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
                  extra="设备注册主键，如 SF-SCAN-001（devices.md §6.1）"
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
                <Form.Item name="warehouse_id" label="绑定仓库" extra="作为设备初始配置仓库（devices.md §6.2）">
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
