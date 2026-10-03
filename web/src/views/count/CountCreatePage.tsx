import { Alert, Button, Form, Input, Select, Typography, message } from 'antd'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  COUNT_CREATE_PERMISSION,
  COUNT_SCOPE_TYPE_LABEL,
  COUNT_TYPE_LABEL,
  countApi,
  type CountCreatePayload,
  type CountScopeType,
  type CountType,
} from '@/api/count'
import { masterdataApi, OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { binApi, shelfApi, warehouseApi, zoneApi } from '@/api/warehouse'
import { userApi } from '@/api/user'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'

const { Text } = Typography

interface CountFormValues {
  warehouseCode: string
  scopeType: CountScopeType
  scopeValue?: string
  countType: CountType
  ownerId?: string
  remark?: string
}

const SCOPE_OPTIONS = (Object.entries(COUNT_SCOPE_TYPE_LABEL) as Array<[CountScopeType, string]>).map(
  ([value, label]) => ({ label, value }),
)

const TYPE_OPTIONS = (Object.entries(COUNT_TYPE_LABEL) as Array<[CountType, string]>).map(([value, label]) => ({
  label,
  value,
}))

/** 需要范围明细的范围类型（ALL 全盘无明细；WAREHOUSE 直接取所选仓库编码） */
const SCOPE_WITH_VALUE: CountScopeType[] = ['ZONE', 'SHELF', 'BIN', 'SKU']

const LIST_OPTIONS = { page: 1, pageSize: OPTIONS_PAGE_SIZE } as const

/**
 * 新建盘点单（/counts/new，POST /api/counts 前端先行契约）。
 * 范围/类型对齐 business-flow.md §10.2：全盘/按仓/按库区/按货架/按库位/按 SKU × 周期/动碰/抽盘；
 * 负责人取 /api/users（M1 冻结契约）。盘点创建后冻结范围，差异一律走库存调整单 + 审批链路。
 */
export default function CountCreatePage() {
  const navigate = useNavigate()
  const [form] = Form.useForm<CountFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, COUNT_CREATE_PERMISSION)

  const watchedWarehouseCode = Form.useWatch('warehouseCode', form)
  const watchedScopeType = Form.useWatch('scopeType', form)

  const warehouses = useQuery({
    queryKey: ['warehouse', 'warehouses', 'options'],
    queryFn: () => warehouseApi.list({ ...LIST_OPTIONS }),
  })
  const owners = useQuery({
    queryKey: ['auth', 'users', 'options'],
    queryFn: () => userApi.users({ ...LIST_OPTIONS, status: 'ACTIVE' }),
  })

  const selectedWarehouse = (warehouses.data?.items ?? []).find(
    (item) => item.code === watchedWarehouseCode,
  )
  // 库区 / 货架 / 库位下拉按所选仓库惰性加载（M1 冻结契约，支持仓库过滤）
  const zones = useQuery({
    queryKey: ['warehouse', 'zones', 'options', selectedWarehouse?.id],
    queryFn: () => zoneApi.list({ ...LIST_OPTIONS, warehouseId: selectedWarehouse?.id as string }),
    enabled: watchedScopeType === 'ZONE' && Boolean(selectedWarehouse),
  })
  const shelves = useQuery({
    queryKey: ['warehouse', 'shelves', 'options', selectedWarehouse?.id],
    queryFn: () => shelfApi.list({ ...LIST_OPTIONS, warehouseId: selectedWarehouse?.id as string }),
    enabled: watchedScopeType === 'SHELF' && Boolean(selectedWarehouse),
  })
  const bins = useQuery({
    queryKey: ['warehouse', 'bins', 'options', selectedWarehouse?.id],
    queryFn: () => binApi.list({ ...LIST_OPTIONS, warehouseId: selectedWarehouse?.id as string }),
    enabled: watchedScopeType === 'BIN' && Boolean(selectedWarehouse),
  })
  // SKU 下拉按需加载（scopeType=SKU 时），数据源 GET /api/skus 一次取全（模式对齐 SalesOrderCreatePage）
  const skus = useQuery({
    queryKey: ['masterdata', 'skus', 'options'],
    queryFn: () => masterdataApi.skus.list({ ...LIST_OPTIONS }),
    enabled: watchedScopeType === 'SKU',
  })

  const warehouseOptions = (warehouses.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }))
  const ownerOptions = (owners.data?.items ?? []).map((item) => ({
    label: item.real_name ? `${item.real_name}（${item.username}）` : item.username,
    value: String(item.id),
  }))
  const zoneOptions = (zones.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }))
  const shelfOptions = (shelves.data?.items ?? []).map((item) => ({ label: item.code, value: item.code }))
  const binOptions = (bins.data?.items ?? []).map((item) => ({ label: item.code, value: item.code }))
  const skuOptions = (skus.data?.items ?? []).map((item) => ({
    label: item.product_name ? `${item.code} ${item.product_name}` : item.code,
    value: item.code,
  }))

  const submitMutation = useMutation({
    mutationFn: (payload: CountCreatePayload) => countApi.create(payload),
    onSuccess: () => {
      messageApi.success('盘点单已创建，可返回盘点中心查看')
      navigate('/counts')
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        submitMutation.mutate({
          warehouseCode: values.warehouseCode.trim(),
          scopeType: values.scopeType,
          scopeValue:
            values.scopeType === 'WAREHOUSE'
              ? values.warehouseCode.trim()
              : values.scopeType === 'ALL'
                ? undefined
                : values.scopeValue?.trim(),
          countType: values.countType,
          ownerId: values.ownerId,
          remark: values.remark?.trim() || undefined,
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  if (!canCreate) {
    return (
      <div className="sf-page">
        <SfPageHeader title="新建盘点单" onBack={() => navigate('/counts')} />
        <SfError
          error={new Error('没有新建盘点单的权限')}
          description="需要盘点域「新建」权限点（前端先行码 count:create），请联系管理员开通；后端仍会做最终校验"
        />
      </div>
    )
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="新建盘点单"
        subtitle="创建盘点 → 冻结范围 → 实盘 → 差异审核（单号 CK- 前缀）"
        onBack={() => navigate('/counts')}
        extra={
          <>
            <Button onClick={() => navigate('/counts')}>取消</Button>
            <Button type="primary" loading={submitMutation.isPending} onClick={handleSubmit}>
              提交创建
            </Button>
          </>
        }
      />

      <Alert
        type="info"
        showIcon
        message="盘点创建后即冻结范围内库位 / 库存"
        description="实盘期间相关库存锁定；差异经复核 / 审核通过后由系统生成库存调整单并走审批链路，禁止直接修改系统库存（business-flow.md §10.2 硬性规则）"
        style={{ marginBottom: 16 }}
      />

      <Form<CountFormValues> form={form} layout="vertical" disabled={submitMutation.isPending}>
        <SfDetailSection title="基础信息">
          <Form.Item
            name="warehouseCode"
            label="仓库"
            rules={[{ required: true, message: '请选择仓库' }]}
            extra="盘点范围为「按仓」时，范围明细自动取所选仓库"
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={warehouseOptions}
              placeholder="请选择盘点仓库"
              loading={warehouses.isLoading}
              onChange={() => form.setFieldValue('scopeValue', undefined)}
            />
          </Form.Item>
          <Form.Item
            name="scopeType"
            label="盘点范围"
            rules={[{ required: true, message: '请选择盘点范围' }]}
          >
            <Select
              options={SCOPE_OPTIONS}
              placeholder="请选择盘点范围"
              onChange={() => form.setFieldValue('scopeValue', undefined)}
            />
          </Form.Item>
          {watchedScopeType === 'WAREHOUSE' && (
            <Form.Item label="范围明细">
              <Text type="secondary">范围 = 所选仓库{watchedWarehouseCode ? `（${watchedWarehouseCode}）` : ''}</Text>
            </Form.Item>
          )}
          {watchedScopeType === 'ALL' && (
            <Form.Item label="范围明细">
              <Text type="secondary">全盘盘点，无需范围明细</Text>
            </Form.Item>
          )}
          {watchedScopeType != null && SCOPE_WITH_VALUE.includes(watchedScopeType) && (
            <Form.Item
              name="scopeValue"
              label="范围明细"
              rules={[{ required: true, message: '请填写范围明细' }]}
              extra={
                watchedScopeType === 'SKU'
                  ? '下拉选择需要盘点的 SKU，提交值为 SKU 编码'
                  : '下拉按所选仓库过滤（M1 仓库空间契约）'
              }
            >
              {watchedScopeType === 'ZONE' ? (
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={zoneOptions}
                  placeholder="请选择库区"
                  loading={zones.isLoading}
                />
              ) : watchedScopeType === 'SHELF' ? (
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={shelfOptions}
                  placeholder="请选择货架"
                  loading={shelves.isLoading}
                />
              ) : watchedScopeType === 'BIN' ? (
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={binOptions}
                  placeholder="请选择库位"
                  loading={bins.isLoading}
                />
              ) : (
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={skuOptions}
                  placeholder="请选择 SKU"
                  loading={skus.isLoading}
                />
              )}
            </Form.Item>
          )}
          <Form.Item
            name="countType"
            label="盘点类型"
            rules={[{ required: true, message: '请选择盘点类型' }]}
          >
            <Select options={TYPE_OPTIONS} placeholder="请选择盘点类型（周期 / 动碰 / 抽盘）" />
          </Form.Item>
          <Form.Item
            name="ownerId"
            label="负责人"
            rules={[{ required: true, message: '请选择负责人' }]}
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={ownerOptions}
              placeholder="请选择盘点负责人"
              loading={owners.isLoading}
            />
          </Form.Item>
        </SfDetailSection>

        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="备注">
            <Form.Item name="remark" style={{ marginBottom: 0 }}>
              <Input.TextArea rows={3} placeholder="补充盘点说明（选填）" maxLength={500} showCount />
            </Form.Item>
          </SfDetailSection>
        </div>
      </Form>
    </div>
  )
}
