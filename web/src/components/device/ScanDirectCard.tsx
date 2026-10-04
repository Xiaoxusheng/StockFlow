import { useState } from 'react'
import { Alert, Button, Card, Descriptions, Flex, Input, List, Typography, message } from 'antd'
import { ScanOutlined } from '@ant-design/icons'
import { useMutation } from '@tanstack/react-query'
import { useLocation, useNavigate } from 'react-router'
import {
  SCANNER_RESOLVE_PERMISSION,
  scannerApi,
  type ScanDocKind,
  type ScanResolveInput,
  type ScanResolveItem,
  type ScanResolveResult,
  type ScanResolveType,
} from '@/api/device'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

const { Text } = Typography

/** 识别类型文案（resolvePipeline 管线冻结五类，service_scan.go:110-198） */
const RESOLVE_TYPE_LABEL: Record<ScanResolveType, string> = {
  doc: '业务单据',
  sku: 'SKU',
  bin: '库位',
  serial: '序列号',
  batch: '批次',
}

/**
 * 单据前缀 → PC 详情路由（ResolveResult.id 为业务行 ID，与各详情页 :id 消费口径一致）。
 * 仅收录 PC 端已存在行详情路由的前缀；QC/RC/PW/PK/CH/BP/SH/TR/RT/EX 无 PC 详情页——
 * 识别结果如实展示，不硬造跳转目标（printing.md §4.3 直达以真实路由为准）。
 */
const DOC_KIND_DETAIL_PATH: Partial<Record<ScanDocKind, (id: string) => string>> = {
  PO: (id) => `/purchases/${id}`,
  IN: (id) => `/inbound/${id}`,
  SO: (id) => `/sales/${id}`,
  OUT: (id) => `/outbound/${id}`,
  CK: (id) => `/counts/${id}`,
}

/** 单个识别对象的可直达路由：单据类且 PC 有详情页时返回路径，否则 undefined */
function resolveTarget(item: ScanResolveItem): string | undefined {
  if (item.type === 'doc' && item.doc_kind && item.id) {
    return DOC_KIND_DETAIL_PATH[item.doc_kind]?.(String(item.id))
  }
  return undefined
}

/**
 * PC 扫码直达入口（scanner.md §5.3 / printing.md §4.3 统一扫码路由协议）：
 * 输入框承接 USB/蓝牙 HID 扫码枪（模拟键盘输入 + Enter，scanner.md §2.3）与手工键入，
 * 经 POST /api/scanner/resolve 用户 JWT 链识别（scanner:resolve:list，handler.go:209），
 * 单据命中且有 PC 详情路由时自动直达，多命中（库位跨仓/批次跨 SKU）列出候选，
 * 其余类型展示识别结果并如实说明暂无直达路由。只识别不执行业务——scanner.md §5.3。
 */
export function ScanDirectCard() {
  const navigate = useNavigate()
  const location = useLocation()
  const [messageApi, contextHolder] = message.useMessage()
  const [code, setCode] = useState('')
  const [result, setResult] = useState<ScanResolveResult | null>(null)
  const permissions = useAuthStore((s) => s.permissions)

  /** dev 会话权限快照为 ['*'] 全通过（LoginPage:240-242）；无权限不渲染入口（后端仍校验） */
  const hasPerm = permissions.includes('*') || permissions.includes(SCANNER_RESOLVE_PERMISSION)

  const resolveMutation = useMutation({
    mutationFn: (input: ScanResolveInput) => scannerApi.resolve(input),
    onSuccess: (data) => {
      // 去重窗口注记（scanner.md §6.6：只标记不抑制识别）
      if (data.duplicate) {
        messageApi.info('该条码在去重窗口内重复扫入（仅注记，识别结果不受影响）')
      }
      setCode('')
      const target = resolveTarget(data)
      if (target) {
        // 单据单命中且有 PC 详情路由：按 printing.md §4.3 直达
        navigate(target)
        return
      }
      setResult(data)
    },
    onError: () => setResult(null),
  })

  if (!hasPerm) {
    return null
  }

  const handleSubmit = (value: string) => {
    const trimmed = value.trim()
    if (!trimmed) {
      messageApi.warning('请先扫入或输入条码 / 单号')
      return
    }
    // page 落 scan_logs.page 列：记录发起识别的页面路径（扫码审计归因）
    resolveMutation.mutate({ code: trimmed, page: location.pathname })
  }

  const renderItem = (item: ScanResolveItem) => {
    const target = resolveTarget(item)
    return (
      <List.Item
        actions={
          target
            ? [
                <Button key="goto" type="link" size="small" onClick={() => navigate(target)}>
                  前往
                </Button>,
              ]
            : undefined
        }
      >
        <span style={{ marginRight: 12 }}>
          {RESOLVE_TYPE_LABEL[item.type] ?? item.type}：{item.code}
          {item.name ? `（${item.name}）` : ''}
        </span>
      </List.Item>
    )
  }

  return (
    <Card
      size="small"
      title={
        <span>
          <ScanOutlined /> 扫码直达
        </span>
      }
      style={{ marginBottom: 16 }}
    >
      {contextHolder}
      <Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
        使用 USB/蓝牙 HID 扫码枪扫入或手工键入条码 / 单号，回车识别并直达对应对象（scanner.md §5.3 /
        printing.md §4.3）；单据类 PO / IN / SO / OUT / CK 自动打开详情，库位与批次多命中时列出候选。
      </Text>
      <Input.Search
        placeholder="扫入条码 / 单号，如 PO-20261004-0001"
        enterButton="识别"
        allowClear
        autoFocus
        value={code}
        onChange={(event) => setCode(event.target.value)}
        onSearch={handleSubmit}
        loading={resolveMutation.isPending}
        style={{ maxWidth: 480 }}
      />
      {resolveMutation.isError && (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 12 }}
          message={`「${code.trim() || '该条码'}」识别失败`}
          description={resolveErrorMessage(resolveMutation.error)}
        />
      )}
      {result && (
        <Card size="small" type="inner" title="识别结果" style={{ marginTop: 12 }}>
          {result.items && result.items.length > 0 ? (
            <>
              <Text type="secondary">该码命中多个对象，请选择要前往的对象（plan §8.3 歧义消解）：</Text>
              <List size="small" dataSource={result.items} renderItem={renderItem} />
            </>
          ) : (
            <>
              <Descriptions
                size="small"
                column={1}
                items={[
                  { key: 'type', label: '对象类型', children: RESOLVE_TYPE_LABEL[result.type] ?? result.type },
                  { key: 'code', label: '编码', children: result.code },
                  { key: 'name', label: '名称', children: result.name || '-' },
                  ...(result.status ? [{ key: 'status', label: '状态', children: result.status }] : []),
                ]}
              />
              <Flex vertical gap={4}>
                {resolveTarget(result) && (
                  <Button type="primary" size="small" style={{ alignSelf: 'flex-start' }} onClick={() => navigate(resolveTarget(result) as string)}>
                    前往
                  </Button>
                )}
                {!resolveTarget(result) && (
                  <Text type="secondary">
                    PC 端暂无该对象类型的详情路由（仅 PO / IN / SO / OUT / CK 单据有行详情页），已如实展示识别结果。
                  </Text>
                )}
              </Flex>
            </>
          )}
        </Card>
      )}
    </Card>
  )
}
