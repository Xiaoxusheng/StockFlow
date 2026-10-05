import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Typography,
  message,
} from 'antd'
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  MinusOutlined,
  SwapOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  inventoryApi,
  type LedgerItem,
  type LedgerQuery,
} from '@/api/inventory'
import { masterdataApi, OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { binApi, type BinItem } from '@/api/warehouse'
import {
  MOVE_EXECUTE_PERMISSION,
  transferApi,
  type MoveBinPayload,
  type MoveKeyInput,
} from '@/api/transfer'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatQty } from '@/utils/format'

const { Text } = Typography

/** 下拉数据源一次取全（模式对齐 CountCreatePage / PadInventoryPage options 端点用法） */
const LIST_OPTIONS = { page: 1, pageSize: OPTIONS_PAGE_SIZE } as const

/** 方向由变更数量正负驱动（LedgerView 无独立 direction 字段，正=入、负=出；
 * 移库产生成对 MOVE 流水：源库位行负、目标库位行正） */
function DirectionText({ qtyChange }: { qtyChange: number }) {
  if (qtyChange > 0) {
    return (
      <Text style={{ color: 'var(--sf-success)' }}>
        <ArrowDownOutlined /> 入
      </Text>
    )
  }
  if (qtyChange < 0) {
    return (
      <Text style={{ color: 'var(--sf-danger)' }}>
        <ArrowUpOutlined /> 出
      </Text>
    )
  }
  return (
    <Text type="secondary">
      <MinusOutlined /> 无
    </Text>
  )
}

function renderQty(value: number) {
  // 移库数量为 numeric(18,4)，本页允许最多 4 位小数（:416 校验）——固定 0 位小数会四舍五入展示
  return <span className="sf-num">{formatQty(value)}</span>
}

/** 可选维度 ID 0 值显示占位符（0=非批次） */
function renderIdOrDash(value: LedgerItem['batch_id']): string {
  return String(value) === '0' ? '-' : String(value)
}

const COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  { title: '流水号', dataIndex: 'ledger_no', width: 150 },
  {
    // 移库流水 business_no = 提交时的作业依据号（moves.go:15 source_no 落账追溯）
    // 注意：本列前有「时间」「流水号」两个非固定列——antd 固定列必须位于列首/列尾，
    // 中间列 fixed 会在横向滚动时贴左覆盖前两列，故不设 fixed
    title: '作业依据号',
    dataIndex: 'business_no',
    width: 150,
    render: (v: string) => v || '-',
  },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 100 },
  { title: '库位 ID', dataIndex: 'bin_id', width: 100, render: renderIdOrDash },
  { title: '批次 ID', dataIndex: 'batch_id', width: 100, render: renderIdOrDash },
  {
    title: '方向',
    key: 'direction',
    width: 80,
    render: (_: unknown, record: LedgerItem) => <DirectionText qtyChange={record.qty_change} />,
  },
  { title: '数量', dataIndex: 'qty_change', width: 90, align: 'right', render: renderQty },
  { title: '变动前', dataIndex: 'qty_before', width: 90, align: 'right', render: renderQty },
  { title: '变动后', dataIndex: 'qty_after', width: 90, align: 'right', render: renderQty },
  { title: '操作人', dataIndex: 'operator_name', width: 100, render: (v: string) => v || '-' },
]

interface MoveFormValues {
  fromBinId?: string
  toBinId?: string
  skuId?: string
  batchId?: string
  qty?: number
  sourceNo?: string
  remark?: string
}

/** 移库端五维键（MoveKeyInput，stockops/moves.go:24-30）：关联 ID 提交 number，
 * 选项行 ID（database.ID 字符串）经 Number() 还原（warehouse.ts 同域约定） */
function toMoveKey(bin: BinItem, skuId: number, batchId: number): MoveKeyInput {
  return {
    warehouse_id: Number(bin.warehouse_id),
    zone_id: Number(bin.zone_id),
    shelf_id: Number(bin.shelf_id),
    bin_id: Number(bin.id),
    sku_id: skuId,
    batch_id: batchId,
  }
}

/**
 * 库存转移（frontend.md §10.1 库存中心模块；菜单 /inventory/transfers）。
 * 原页面读取的 GET /api/inventory/transfers 为前端先行骨架且后端未注册
 * （命中 GET /api/inventory/:id 参数错误，审计问题 #7）——列表区改读真实移库流水
 * GET /api/inventory-ledgers?change_type=MOVE（后端已注册且值域含 MOVE，
 * internal/inventory/handler.go:305）；作业入口 [发起移库] 提交
 * POST /api/inventory/moves（internal/stockops/routes.go:60 + moves.go:25-41）：
 * MoveBin 原语同仓同 SKU 同批次跨库位移动可用库存，源/目标两行各落一条 MOVE 流水。
 */
export default function TransferPage() {
  const [params, setParams] = useState<LedgerQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<MoveFormValues>()
  const [messageApi, messageContext] = message.useMessage()
  // 按钮级权限码经 canAccess fail-closed 过滤（模式同 SfExportButton；permission.md §5：
  // 前端权限仅是体验优化，后端 RequirePermission(PermMoveExecute) 仍强校验）
  const user = useAuthStore((state) => state.user)
  const canMove = canAccess(user, MOVE_EXECUTE_PERMISSION)

  // 移库流水列表：固定 change_type=MOVE，其余筛选透传（后端 handler.go:295-323 校验）
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['inventory', 'move-ledger'],
    fetch: (q) => inventoryApi.ledger({ ...q, change_type: 'MOVE' }),
    params,
  })

  // 发起移库表单选项：库位（GET /api/bins，行含 warehouse_id/zone_id/shelf_id 可解析
  // MoveKeyInput 定位键）与 SKU（GET /api/skus）；弹窗打开才拉取，失败在 Select 内呈现
  const bins = useQuery({
    queryKey: ['warehouse', 'bins', 'options', 'inventory-move'],
    queryFn: () => binApi.list({ ...LIST_OPTIONS }),
    enabled: modalOpen,
  })
  const skus = useQuery({
    queryKey: ['masterdata', 'skus', 'options', 'inventory-move'],
    queryFn: () => masterdataApi.skus.list({ ...LIST_OPTIONS }),
    enabled: modalOpen,
  })

  const watchedFromBinId = Form.useWatch('fromBinId', form)
  const watchedSkuId = Form.useWatch('skuId', form)

  const binItems = bins.data?.items ?? []
  const skuItems = skus.data?.items ?? []
  const fromBin = binItems.find((bin) => String(bin.id) === watchedFromBinId)
  const selectedSku = skuItems.find((sku) => String(sku.id) === watchedSkuId)
  const batchManaged = selectedSku?.is_batch_managed === true

  // 目标库位下拉按 MoveBin 原语约束收敛（inventory/service.go:706-713：同仓 + 目标≠源）：
  // 仅列与源库位同仓的库位并排除源库位；跨仓属调拨域（/transfers），后端仍做最终校验
  const toBinOptions = binItems
    .filter(
      (bin) =>
        fromBin != null &&
        String(bin.id) !== String(fromBin.id) &&
        String(bin.warehouse_id) === String(fromBin.warehouse_id),
    )
    .map((bin) => ({ label: `${bin.code}（仓 ${String(bin.warehouse_id)}）`, value: String(bin.id) }))
  const binOptions = binItems.map((bin) => ({
    label: `${bin.code}（仓 ${String(bin.warehouse_id)}）`,
    value: String(bin.id),
  }))
  const skuOptions = skuItems.map((item) => ({
    label: item.product_name ? `${item.code} ${item.product_name}` : item.code,
    value: String(item.id),
  }))

  // 批次选项：SKU 启用批次管理时按所选 SKU 拉 /api/batches；未启用按非批次（batch_id=0）提交
  const batches = useQuery({
    queryKey: ['inventory', 'batches', 'options', String(watchedSkuId ?? '')],
    queryFn: () => inventoryApi.batches({ sku_id: watchedSkuId!, ...LIST_OPTIONS }),
    enabled: modalOpen && batchManaged && Boolean(watchedSkuId),
  })
  const batchOptions = (batches.data?.items ?? []).map((item) => ({
    label: item.batch_no,
    value: String(item.id),
  }))

  const moveMutation = useMutation({
    mutationFn: (payload: MoveBinPayload) => transferApi.moveBin(payload),
    onSuccess: (result) => {
      messageApi.success(
        result.replay
          ? `移库已提交（幂等重放，库存未重复变更；流水 ${result.ledger.ledger_no}）`
          : `移库成功，已生成移库流水 ${result.ledger.ledger_no}`,
      )
      setModalOpen(false)
      void list.refetch()
    },
  })

  const openCreate = () => {
    moveMutation.reset()
    setModalOpen(true)
    form.resetFields()
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        const source = binItems.find((bin) => String(bin.id) === values.fromBinId)
        const target = binItems.find((bin) => String(bin.id) === values.toBinId)
        if (!source || !target || values.skuId == null || values.qty == null) {
          messageApi.error('表单数据不完整，请重新选择源/目标库位与 SKU')
          return
        }
        const skuId = Number(values.skuId)
        const batchId = values.batchId ? Number(values.batchId) : 0
        moveMutation.mutate({
          from: toMoveKey(source, skuId, batchId),
          to: toMoveKey(target, skuId, batchId),
          // numeric(18,4) 文本（moves.go:38 qty 注释；stock.ParseQty 十进制格式校验）
          qty: String(values.qty),
          source_no: values.sourceNo!.trim(),
          remark: values.remark?.trim() || undefined,
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as LedgerQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      {messageContext}
      <SfPageHeader
        title="库存转移"
        subtitle="仓内移库：同仓库位间移动可用库存，源/目标库位各生成一条移库流水"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'business_no', label: '作业依据号', control: 'input', placeholder: '移库作业依据号' },
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'bin_id', label: '库位 ID', control: 'input', placeholder: '库位 ID（正整数）' },
            { name: 'batch_id', label: '批次 ID', control: 'input', placeholder: '批次 ID（0=非批次）' },
          ]}
          onSearch={handleSearch}
          extraActions={
            canMove ? (
              <Button type="primary" icon={<SwapOutlined />} onClick={openCreate}>
                发起移库
              </Button>
            ) : undefined
          }
        />
        <SfTable<LedgerItem>
          storageKey="inventory-move-ledger"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有移库流水"
          scrollX={1320}
        />
      </Card>

      <Modal
        title="发起移库"
        open={modalOpen}
        width={560}
        forceRender
        confirmLoading={moveMutation.isPending}
        okText="提交移库"
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        {moveMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(moveMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Alert
          type="info"
          showIcon
          message="同仓同 SKU 同批次跨库位移动可用库存"
          description="目标库位须与源库位同仓；作业依据号必填并落入库存流水（inventory-rules §5 来源追溯，POST /api/inventory/moves）"
          style={{ marginBottom: 16 }}
        />
        <Form<MoveFormValues> form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="fromBinId"
                label="源库位"
                rules={[{ required: true, message: '请选择源库位' }]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={binOptions}
                  placeholder="请选择源库位"
                  loading={bins.isLoading}
                  onChange={() => form.setFieldValue('toBinId', undefined)}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="toBinId"
                label="目标库位"
                rules={[{ required: true, message: '请选择目标库位' }]}
                extra="仅列出与源库位同仓的库位（跨仓请走调拨单）"
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={toBinOptions}
                  placeholder={watchedFromBinId ? '请选择目标库位' : '请先选择源库位'}
                  loading={bins.isLoading}
                  disabled={!watchedFromBinId}
                />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="skuId" label="SKU" rules={[{ required: true, message: '请选择 SKU' }]}>
                <Select
                  showSearch
                  optionFilterProp="label"
                  options={skuOptions}
                  placeholder="请选择 SKU"
                  loading={skus.isLoading}
                  onChange={() => form.setFieldValue('batchId', undefined)}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              {batchManaged ? (
                <Form.Item
                  name="batchId"
                  label="批次"
                  rules={[{ required: true, message: '请选择批次' }]}
                  extra="该 SKU 启用批次管理，按批次维度移动库存"
                >
                  <Select
                    showSearch
                    optionFilterProp="label"
                    options={batchOptions}
                    placeholder="请选择批次"
                    loading={batches.isLoading}
                  />
                </Form.Item>
              ) : (
                <Form.Item label="批次" extra="该 SKU 未启用批次管理，按非批次（batch_id=0）提交">
                  <Select disabled options={[{ label: '非批次', value: '0' }]} value="0" />
                </Form.Item>
              )}
            </Col>
          </Row>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="qty"
                label="移库数量"
                rules={[
                  { required: true, message: '请输入移库数量' },
                  {
                    validator: (_rule, value: number | null) => {
                      if (value == null) return Promise.resolve()
                      // 对齐后端 stock.ParseQty：十进制数字、numeric(18,4) 最多 4 位小数
                      if (!/^\d+(\.\d{1,4})?$/.test(String(value))) {
                        return Promise.reject(new Error('数量须为十进制数字，最多 4 位小数'))
                      }
                      if (!(Number(value) > 0)) {
                        return Promise.reject(new Error('数量必须大于 0'))
                      }
                      return Promise.resolve()
                    },
                  },
                ]}
              >
                <InputNumber min={0} style={{ width: '100%' }} placeholder="大于 0，最多 4 位小数" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="sourceNo"
                label="作业依据号"
                // whitespace: true 拦截纯空白输入（antd required 默认放行 " "），
                // 前端内联报错先于后端 ErrMoveSourceRequired（moves.go:59-62）
                rules={[{ required: true, whitespace: true, message: '请输入作业依据号' }]}
                extra="作业工单 / 异常单 / 审批记录号等，后端强校验必填"
              >
                <Input placeholder="如 GZ-20261004-001" maxLength={64} />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} placeholder="补充移库说明（选填）" maxLength={255} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
