import { Alert, Button, Form, Input, Select, Typography, message } from 'antd'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  COUNT_CREATE_PERMISSION,
  COUNT_SCOPE_MODE_LABEL,
  countApi,
  type CountCreatePayload,
  type CountScope,
  type CountScopeMode,
} from '@/api/count'
import { masterdataApi, OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { binApi, shelfApi, warehouseApi, zoneApi } from '@/api/warehouse'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'

const { Text } = Typography

/** 表单值：下拉选项值统一为 String(id)，提交时转 number（后端 CountInput int64，字符串 400） */
interface CountFormValues {
  warehouse_id?: string
  mode?: CountScopeMode
  zone_ids?: string[]
  shelf_ids?: string[]
  bin_ids?: string[]
  sku_ids?: string[]
  remark?: string
}

const SCOPE_OPTIONS = (Object.entries(COUNT_SCOPE_MODE_LABEL) as Array<[CountScopeMode, string]>).map(
  ([value, label]) => ({ label, value }),
)

/** 各范围模式对应的明细字段名（ALL 全盘无明细；按仓 = 单据 warehouse_id 本身，models.go:162-168） */
const MODE_FIELD: Partial<Record<CountScopeMode, keyof CountFormValues>> = {
  ZONE: 'zone_ids',
  SHELF: 'shelf_ids',
  BIN: 'bin_ids',
  SKU: 'sku_ids',
}

const SCOPE_FIELD_LABEL: Partial<Record<CountScopeMode, string>> = {
  ZONE: '库区',
  SHELF: '货架',
  BIN: '库位',
  SKU: 'SKU',
}

const LIST_OPTIONS = { page: 1, pageSize: OPTIONS_PAGE_SIZE } as const

/**
 * 新建盘点单（/counts/new，POST /api/counts；入参 CountInput，
 * internal/stockops/count.go:39-43：{warehouse_id, scope:{mode, zone_ids|shelf_ids|bin_ids|sku_ids}, remark}）。
 * 范围模式五值（models.go:163：ALL/ZONE/SHELF/BIN/SKU；按仓 = 单据 warehouse_id 本身，无独立模式），
 * 范围明细为对应维度的多选 ID（后端校验 mode 与维度匹配，models.go:180-207）。
 * 后端 CountInput 无盘点类型 / 负责人字段，表单不再提供（后端缺口，见 frontend.md §10.5）。
 * 盘点创建后冻结范围，差异一律走库存调整单 + 审批链路。
 */
export default function CountCreatePage() {
  const navigate = useNavigate()
  const [form] = Form.useForm<CountFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, COUNT_CREATE_PERMISSION)

  const watchedWarehouseId = Form.useWatch('warehouse_id', form)
  const watchedMode = Form.useWatch('mode', form)

  const warehouses = useQuery({
    queryKey: ['count', 'create', 'warehouses'],
    queryFn: () => warehouseApi.list({ ...LIST_OPTIONS }),
  })

  // 库区 / 货架 / 库位下拉按所选仓库惰性加载（列表筛选参数为 camelCase，api/warehouse.ts 头注）
  const zones = useQuery({
    queryKey: ['count', 'create', 'zones', watchedWarehouseId],
    queryFn: () => zoneApi.list({ ...LIST_OPTIONS, warehouseId: watchedWarehouseId }),
    enabled: watchedMode === 'ZONE' && Boolean(watchedWarehouseId),
  })
  const shelves = useQuery({
    queryKey: ['count', 'create', 'shelves', watchedWarehouseId],
    queryFn: () => shelfApi.list({ ...LIST_OPTIONS, warehouseId: watchedWarehouseId }),
    enabled: watchedMode === 'SHELF' && Boolean(watchedWarehouseId),
  })
  const bins = useQuery({
    queryKey: ['count', 'create', 'bins', watchedWarehouseId],
    queryFn: () => binApi.list({ ...LIST_OPTIONS, warehouseId: watchedWarehouseId }),
    enabled: watchedMode === 'BIN' && Boolean(watchedWarehouseId),
  })
  // SKU 下拉按需加载（mode=SKU 时），数据源 GET /api/skus 一次取全
  const skus = useQuery({
    queryKey: ['count', 'create', 'skus'],
    queryFn: () => masterdataApi.skus.list({ ...LIST_OPTIONS }),
    enabled: watchedMode === 'SKU',
  })

  const warehouseOptions = (warehouses.data?.items ?? []).map((w) => ({
    label: `${w.name}（${w.code}）`,
    value: String(w.id),
  }))
  const zoneOptions = (zones.data?.items ?? []).map((z) => ({
    label: z.code ? `${z.code} ${z.name}` : z.name,
    value: String(z.id),
  }))
  const shelfOptions = (shelves.data?.items ?? []).map((s) => ({ label: s.code, value: String(s.id) }))
  const binOptions = (bins.data?.items ?? []).map((b) => ({ label: b.code, value: String(b.id) }))
  const skuOptions = (skus.data?.items ?? []).map((s) => ({
    label: s.product_name ? `${s.code} ${s.product_name}` : s.code,
    value: String(s.id),
  }))

  const dimensionOptions =
    watchedMode === 'ZONE'
      ? zoneOptions
      : watchedMode === 'SHELF'
        ? shelfOptions
        : watchedMode === 'BIN'
          ? binOptions
          : skuOptions
  const dimensionLoading =
    watchedMode === 'ZONE'
      ? zones.isLoading
      : watchedMode === 'SHELF'
        ? shelves.isLoading
        : watchedMode === 'BIN'
          ? bins.isLoading
          : skus.isLoading

  /** 切换仓库 / 模式时清空全部范围明细字段，避免残留不属于新范围的 ID */
  const clearScopeFields = () => {
    form.setFieldsValue({ zone_ids: undefined, shelf_ids: undefined, bin_ids: undefined, sku_ids: undefined })
  }

  const submitMutation = useMutation({
    mutationFn: (payload: CountCreatePayload) => countApi.create(payload),
    onSuccess: (created) => {
      messageApi.success('盘点单已创建，范围内库存将在开始盘点时冻结')
      navigate(`/counts/${String(created.order.id)}`)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        if (!values.warehouse_id || !values.mode) return
        const scope: CountScope = { mode: values.mode }
        if (values.mode === 'ZONE') scope.zone_ids = (values.zone_ids ?? []).map(Number)
        else if (values.mode === 'SHELF') scope.shelf_ids = (values.shelf_ids ?? []).map(Number)
        else if (values.mode === 'BIN') scope.bin_ids = (values.bin_ids ?? []).map(Number)
        else if (values.mode === 'SKU') scope.sku_ids = (values.sku_ids ?? []).map(Number)
        submitMutation.mutate({
          warehouse_id: Number(values.warehouse_id),
          scope,
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
          description={`需要盘点域「新建」权限点（${COUNT_CREATE_PERMISSION}），请联系管理员开通；后端仍会做最终校验`}
        />
      </div>
    )
  }

  const modeField = watchedMode ? MODE_FIELD[watchedMode] : undefined
  /** 范围明细维度文案（watchedMode 可能为 undefined，索引前先窄化） */
  const scopeFieldLabel = watchedMode ? SCOPE_FIELD_LABEL[watchedMode] : undefined

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="新建盘点单"
        subtitle="创建 → 开始盘点（冻结范围）→ 实盘 → 差异审核（单号 CK- 前缀）"
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
        message="盘点范围内库存将在「开始盘点」时冻结"
        description="实盘期间相关库存锁定；差异经审核通过后由系统生成库存调整单并走审批链路，禁止直接修改系统库存（business-flow.md §10.2 硬性规则）"
        style={{ marginBottom: 16 }}
      />

      <Form<CountFormValues> form={form} layout="vertical" disabled={submitMutation.isPending}>
        <SfDetailSection title="盘点范围">
          <Form.Item
            name="warehouse_id"
            label="仓库"
            rules={[{ required: true, message: '请选择仓库' }]}
            extra="盘点单挂唯一仓库；「按仓」盘点即范围 = 该仓库全部库存，无需另选明细"
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={warehouseOptions}
              placeholder="请选择盘点仓库"
              loading={warehouses.isLoading}
              onChange={() => {
                clearScopeFields()
              }}
            />
          </Form.Item>
          <Form.Item
            name="mode"
            label="范围模式"
            rules={[{ required: true, message: '请选择范围模式' }]}
            extra="范围声明以 {mode, 维度 ID 列表} 随单据快照落库，提交后不可修改"
          >
            <Select
              options={SCOPE_OPTIONS}
              placeholder="请选择范围模式"
              onChange={() => {
                clearScopeFields()
              }}
            />
          </Form.Item>
          {watchedMode === 'ALL' && (
            <Form.Item label="范围明细">
              <Text type="secondary">全盘盘点，覆盖所选仓库全部库存行，无需选择明细</Text>
            </Form.Item>
          )}
          {modeField && watchedMode !== 'ALL' && (
            <Form.Item
              name={modeField}
              label={`范围明细（${scopeFieldLabel}，多选）`}
              rules={[{ required: true, message: `请选择需要盘点的${scopeFieldLabel ?? '明细'}` }]}
              extra={
                watchedMode === 'SKU'
                  ? '按 SKU 盘点该 SKU 在所选仓库的全部库存行'
                  : '下拉按所选仓库过滤（仓库空间契约，api/warehouse.ts）'
              }
            >
              <Select
                mode="multiple"
                showSearch
                optionFilterProp="label"
                options={dimensionOptions}
                placeholder={`请选择需要盘点的${scopeFieldLabel ?? '明细'}`}
                loading={dimensionLoading}
              />
            </Form.Item>
          )}
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
