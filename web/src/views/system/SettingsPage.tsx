import { useMemo } from 'react'
import {
  Button,
  Card,
  Col,
  Flex,
  Form,
  Input,
  InputNumber,
  Row,
  Select,
  Skeleton,
  Switch,
  Typography,
  message,
} from 'antd'
import { SaveOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  SYSTEM_CONFIG_VIEW_PERMISSION,
  systemApi,
  type SystemConfigItem,
  type SystemConfigSavePayload,
} from '@/api/system'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'

const { Text } = Typography

/** 表单值：boolean 走 Switch（valuePropName=checked），其余保持字符串 */
type ConfigFormValue = string | number | boolean

type ConfigFormValues = Record<string, ConfigFormValue>

/**
 * 分组标签映射：group 编码为前端提案值域（api/system.ts），未知分组兜底展示原始编码，
 * 后端冻结后新增分组不阻塞页面。
 */
const CONFIG_GROUP_LABEL: Record<string, string> = {
  basic: '基础参数',
  inventory: '库存参数',
  document: '单据参数',
  security: '安全参数',
}

function groupLabel(group: string): string {
  return CONFIG_GROUP_LABEL[group] ?? group
}

/** 字段说明：配置键 + 备注（只读项显式标注） */
function ConfigFieldExtra({ item }: { item: SystemConfigItem }) {
  return (
    <Text type="secondary" style={{ fontSize: 12 }}>
      {item.key}
      {item.remark ? ` · ${item.remark}` : ''}
      {item.readonly ? ' · 只读' : ''}
    </Text>
  )
}

/** 按控件类型渲染配置控件；无权限或只读项禁用（前端权限仅体验优化，后端必须校验） */
function ConfigControl({ item, disabled }: { item: SystemConfigItem; disabled: boolean }) {
  const isDisabled = disabled || item.readonly === true
  switch (item.type) {
    case 'boolean':
      return <Switch disabled={isDisabled} />
    case 'number':
      return <InputNumber style={{ width: '100%' }} disabled={isDisabled} />
    case 'enum':
      return (
        <Select
          options={(item.options ?? []).map((option) => ({ label: option, value: option }))}
          disabled={isDisabled}
        />
      )
    default:
      return <Input maxLength={255} disabled={isDisabled} placeholder={`请输入${item.name}`} />
  }
}

/**
 * 系统配置（/system/settings，menu.tsx:159 权限码 system:config:view）：
 * 按 group 分区渲染分组表单（SfDetailSection），保存仅提交变更项走契约端点
 * PUT /api/system/configs（api.md:76 /api/system 域；修改属敏感操作须后端审计，permission.md §6）。
 * 后端系统域未交付：读取呈统一错误态，禁止 mock / 预置假配置。
 */
export default function SettingsPage() {
  const [form] = Form.useForm<ConfigFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  // fail-closed：无权限时整个表单只读、隐藏保存入口（types/permission.ts canAccess）
  const canManage = canAccess(user, SYSTEM_CONFIG_VIEW_PERMISSION)

  const configs = useQuery({
    queryKey: ['system', 'configs'],
    queryFn: () => systemApi.configs.list(),
  })

  const items = useMemo(() => configs.data ?? [], [configs.data])

  /** 按分组聚合（保持接口返回顺序；每组内保持配置项顺序） */
  const groups = useMemo(() => {
    const map = new Map<string, SystemConfigItem[]>()
    for (const item of items) {
      const bucket = map.get(item.group) ?? []
      bucket.push(item)
      map.set(item.group, bucket)
    }
    return [...map.entries()].map(([group, groupItems]) => ({ group, items: groupItems }))
  }, [items])

  /** initialValues：boolean 转 Switch 的布尔值，其余保持后端字符串 */
  const initialValues = useMemo(() => {
    const values: ConfigFormValues = {}
    for (const item of items) {
      values[item.key] = item.type === 'boolean' ? item.value === 'true' : item.value
    }
    return values
  }, [items])

  const saveMutation = useMutation({
    mutationFn: (payload: SystemConfigSavePayload) => systemApi.configs.save(payload),
    onSuccess: () => {
      messageApi.success('保存成功')
      void queryClient.invalidateQueries({ queryKey: ['system', 'configs'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSave = () => {
    form
      .validateFields()
      .then((values) => {
        // 仅提交发生变更的项（契约：后端逐项审计）；只读项恒不提交
        const changed: SystemConfigSavePayload['items'] = []
        for (const item of items) {
          if (item.readonly) continue
          const raw = values[item.key]
          const value = item.type === 'boolean' ? String(raw === true) : String(raw ?? '')
          if (value !== item.value) changed.push({ key: item.key, value })
        }
        if (changed.length === 0) {
          messageApi.info('配置未修改，无需保存')
          return
        }
        saveMutation.mutate({ items: changed })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="系统配置"
        subtitle="基础 / 库存 / 单据 / 安全等全局参数（修改留痕）"
        extra={
          canManage ? (
            <Button
              type="primary"
              icon={<SaveOutlined />}
              loading={saveMutation.isPending}
              onClick={handleSave}
            >
              保存配置
            </Button>
          ) : undefined
        }
      />
      {configs.isPending ? (
        <Card size="small">
          <Skeleton active paragraph={{ rows: 10 }} />
        </Card>
      ) : configs.error ? (
        <SfError
          error={configs.error}
          onRetry={configs.refetch}
          description="系统配置接口 GET /api/system/configs 尚未交付（系统域后端未启动），接口就绪后自动展示真实配置"
        />
      ) : items.length === 0 ? (
        <Card size="small">
          <SfEmpty description="后端未返回任何配置项，请联系管理员检查初始化数据" />
        </Card>
      ) : (
        <Form<ConfigFormValues> form={form} layout="vertical" initialValues={initialValues}>
          <Flex vertical gap={16}>
            {groups.map(({ group, items: groupItems }) => (
              <SfDetailSection key={group} title={groupLabel(group)}>
                <Row gutter={[16, 0]}>
                  {groupItems.map((item) => (
                    <Col key={item.key} xs={24} md={12} xl={8}>
                      <Form.Item
                        name={item.key}
                        label={item.name}
                        valuePropName={item.type === 'boolean' ? 'checked' : 'value'}
                        extra={<ConfigFieldExtra item={item} />}
                      >
                        <ConfigControl item={item} disabled={!canManage} />
                      </Form.Item>
                    </Col>
                  ))}
                </Row>
              </SfDetailSection>
            ))}
          </Flex>
        </Form>
      )}
    </div>
  )
}
