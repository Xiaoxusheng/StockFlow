import { useMemo, useState } from 'react'
import { Button, Card, Form, Input, Modal, Radio, Space, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined, SwapOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  TRANSFER_STATUS_TAG,
  TRANSFER_CREATE_PERMISSION,
  TRANSFER_UPDATE_PERMISSION,
  TRANSFER_SUBMIT_PERMISSION,
  TRANSFER_APPROVE_PERMISSION,
  TRANSFER_CANCEL_PERMISSION,
  transferApi,
  type TransferApprovePayload,
  type TransferOrder,
  type TransferQuery,
  type TransferStatus,
  type TransferType,
} from '@/api/transfer'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfToolbar } from '@/components/table/SfToolbar'
import { formatDateTime } from '@/utils/format'
import TransferFormModal from './TransferFormModal'
import TransferInTransitDrawer from './TransferInTransitDrawer'

/** 调拨类型文案（models.go:153-154：WAREHOUSE 跨仓 / BIN 库位间，库位间必须同仓） */
const TRANSFER_TYPE_LABEL: Record<TransferType, string> = {
  WAREHOUSE: '仓库 → 仓库',
  BIN: '库位 → 库位',
}

const TRANSFER_TYPE_OPTIONS = (Object.entries(TRANSFER_TYPE_LABEL) as Array<[TransferType, string]>).map(
  ([value, label]) => ({ label, value }),
)

/** 状态筛选选项与列内标签同源（api/transfer.ts TRANSFER_STATUS_TAG 七态唯一映射） */
const STATUS_OPTIONS = (
  Object.entries(TRANSFER_STATUS_TAG) as Array<[TransferStatus, (typeof TRANSFER_STATUS_TAG)[TransferStatus]]>
).map(([value, meta]) => ({ label: meta.label, value }))

/** 可取消状态（transfer.go:24-25：DRAFT/PENDING_APPROVAL/APPROVED 可取消，
 * TRANSFERRING/AWAITING_RECEIPT 取消被拒——只能反向调拨冲正，business-flow.md §13.3） */
const CANCELLABLE_STATUS: TransferStatus[] = ['DRAFT', 'PENDING_APPROVAL', 'APPROVED']

/**
 * 调拨单状态 → SfStatusTag：七态值域 internal/stockops/models.go:134-142（迁移 000009
 * CHECK 同源）。注册表（types/status.ts）以小写键收录同语义键；approved 注册表通用文案
 * 为「已审核」，调拨语境「待出库」（business-flow.md §10.1）按注册表声明的覆盖路径，
 * 经 TRANSFER_STATUS_TAG 的 label/semantic 生效——大写原始值不命中注册表
 * （resolveStatus 精确匹配），label/semantic 兜底接管；后端返回未知值时同样兜底
 * 中性灰 + 原始文案，不崩溃。
 */
function TransferStatusTag({ status }: { status: TransferStatus }) {
  const meta = TRANSFER_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/** 仓库名映射失败降级为 ID（api/options.ts：不造假数据） */
function warehouseNameOf(names: Map<string, string>, id: TransferOrder['from_warehouse_id']): string {
  return names.get(idKey(id)) ?? String(id)
}

interface ApproveFormValues {
  action: 'approve' | 'reject'
  opinion?: string
}

interface CancelFormValues {
  reason?: string
}

/**
 * 调拨单列表（/transfers，frontend.md §4 仓库中心菜单；后端 M2 已交付：
 * internal/stockops/routes.go:46-56）。GET /api/transfers 为单据级行（TransferOrderView，
 * handler.go:27-42，无 SKU/数量明细列），行级明细走 GET /api/transfers/{id}，
 * 本页仅列表不做详情跳转。搜索参数 type/status/transfer_no（handler.go:294-325 实测，
 * transfer_no 为精确匹配）。
 *
 * 创建/审核链路接线（transfer.go 状态机）：[登记调拨单] POST /api/transfers（DRAFT，
 * stockops:transfer:create）；行动作 [编辑] PUT（仅 DRAFT，update）、[提交审核] POST
 * /{id}/submit（DRAFT→PENDING_APPROVAL，submit）、[审核] POST /{id}/approve（approve 通过
 * 即源仓逐行预占 / reject 驳回取消，approve）、[取消] POST /{id}/cancel（DRAFT/
 * PENDING_APPROVAL/APPROVED，cancel）；[在途汇总] 消费 GET /api/transfers/in-transit
 * （routes.go:48，后端聚合）。前置权限码经 canAccess fail-closed 过滤（permission.md §5）。
 * 与 /inventory/transfers（views/inventory/TransferPage.tsx，库存转移作业）语义区分：
 * 本页是调拨单据流——两维度（仓库→仓库 / 库位→库位）、七态状态机（草稿→待审核→待出库→
 * 调拨中→待入库→已完成，任一环节可已取消）、TR- 单号（business-flow.md §10.1、§13.1），
 * 源仓减少、目标仓增加，两端均生成库存流水。
 */
export default function TransferListPage() {
  const [params, setParams] = useState<TransferQuery>({})
  const [formOpen, setFormOpen] = useState(false)
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create')
  const [editId, setEditId] = useState<TransferOrder['id'] | undefined>(undefined)
  const [inTransitOpen, setInTransitOpen] = useState(false)
  const [approveTarget, setApproveTarget] = useState<TransferOrder | null>(null)
  const [cancelTarget, setCancelTarget] = useState<TransferOrder | null>(null)

  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限码经 canAccess fail-closed 过滤（模式同 PadTransferPage；permission.md §5）
  const user = useAuthStore((state) => state.user)
  const canCreate = canAccess(user, TRANSFER_CREATE_PERMISSION)
  const canUpdate = canAccess(user, TRANSFER_UPDATE_PERMISSION)
  const canSubmit = canAccess(user, TRANSFER_SUBMIT_PERMISSION)
  const canApprove = canAccess(user, TRANSFER_APPROVE_PERMISSION)
  const canCancel = canAccess(user, TRANSFER_CANCEL_PERMISSION)

  const [approveForm] = Form.useForm<ApproveFormValues>()
  const [cancelForm] = Form.useForm<CancelFormValues>()

  const list = usePagedList<TransferOrder, TransferQuery>({
    queryKey: ['transfer', 'orders'],
    fetch: (q) => transferApi.list(q),
    params,
  })

  // 仓库 id → 名称映射（后端视图不联表下发仓库名，api/options.ts 一次取全后本地映射）
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )

  const invalidateOrders = () => {
    void queryClient.invalidateQueries({ queryKey: ['transfer', 'orders'] })
  }

  // 提交审核：DRAFT→PENDING_APPROVAL（无库存动作，审核通过才预占）
  const submitMutation = useMutation({
    mutationFn: (id: TransferOrder['id']) => transferApi.submit(id),
    onSuccess: (detail) => {
      messageApi.success(`调拨单 ${detail.order.transfer_no} 已提交审核（草稿 → 待审核）`)
      invalidateOrders()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  // 审核：approve 通过（→APPROVED + 源仓逐行预占）/ reject 驳回（→CANCELLED）
  const approveMutation = useMutation({
    mutationFn: ({ id, payload }: { id: TransferOrder['id']; payload: TransferApprovePayload }) =>
      transferApi.approve(id, payload),
    onSuccess: (detail, { payload }) => {
      messageApi.success(
        payload.action === 'approve'
          ? `调拨单 ${detail.order.transfer_no} 审核通过（待审核 → 待出库，源仓已预占）`
          : `调拨单 ${detail.order.transfer_no} 已驳回取消（→ 已取消）`,
      )
      setApproveTarget(null)
      invalidateOrders()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  // 取消：DRAFT/PENDING_APPROVAL/APPROVED→CANCELLED（APPROVED 先释放全部预占锁）
  const cancelMutation = useMutation({
    mutationFn: ({ id, reason }: { id: TransferOrder['id']; reason?: string }) =>
      transferApi.cancel(id, { reason }),
    onSuccess: (detail) => {
      messageApi.success(`调拨单 ${detail.order.transfer_no} 已取消（预占已释放）`)
      setCancelTarget(null)
      invalidateOrders()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as TransferQuery)
    list.resetToFirstPage()
  }

  const openCreate = () => {
    setFormMode('create')
    setEditId(undefined)
    setFormOpen(true)
  }
  const openEdit = (order: TransferOrder) => {
    setFormMode('edit')
    setEditId(order.id)
    setFormOpen(true)
  }

  const columns: ColumnsType<TransferOrder> = [
    { title: '调拨单号', dataIndex: 'transfer_no', width: 170, fixed: 'left' },
    {
      title: '类型',
      dataIndex: 'type',
      width: 120,
      render: (v: TransferType) => TRANSFER_TYPE_LABEL[v] ?? v,
    },
    {
      title: '源仓库',
      dataIndex: 'from_warehouse_id',
      width: 140,
      ellipsis: true,
      render: (v: TransferOrder['from_warehouse_id']) => warehouseNameOf(warehouseNames, v),
    },
    {
      title: '目标仓库',
      dataIndex: 'to_warehouse_id',
      width: 140,
      ellipsis: true,
      render: (v: TransferOrder['to_warehouse_id']) => warehouseNameOf(warehouseNames, v),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: TransferStatus) => <TransferStatusTag status={v} />,
    },
    {
      title: '出库时间',
      dataIndex: 'outbound_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '入库时间',
      dataIndex: 'received_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 210,
      fixed: 'right',
      render: (_, record) => (
        <Space size={0} wrap>
          {record.status === 'DRAFT' && canUpdate && (
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
          )}
          {record.status === 'DRAFT' && canSubmit && (
            <Button
              type="link"
              size="small"
              loading={submitMutation.isPending && submitMutation.variables === record.id}
              onClick={() => submitMutation.mutate(record.id)}
            >
              提交审核
            </Button>
          )}
          {record.status === 'PENDING_APPROVAL' && canApprove && (
            <Button type="link" size="small" onClick={() => setApproveTarget(record)}>
              审核
            </Button>
          )}
          {CANCELLABLE_STATUS.includes(record.status) && canCancel && (
            <Button type="link" size="small" danger onClick={() => setCancelTarget(record)}>
              取消
            </Button>
          )}
        </Space>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="调拨"
        subtitle="调拨单：仓库→仓库 / 库位→库位，两端均生成库存流水（business-flow.md §10.1）"
      />
      <Card size="small">
        <SfToolbar
          title={
            <Space wrap>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                disabled={!canCreate}
                title={canCreate ? undefined : `缺少 ${TRANSFER_CREATE_PERMISSION} 权限`}
                onClick={openCreate}
              >
                登记调拨单
              </Button>
              <Button icon={<SwapOutlined />} onClick={() => setInTransitOpen(true)}>
                在途汇总
              </Button>
            </Space>
          }
        />
        <SfSearchForm
          fields={[
            { name: 'transfer_no', label: '调拨单号', control: 'input', placeholder: '调拨单号（精确匹配）' },
            { name: 'type', label: '调拨维度', control: 'select', options: TRANSFER_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<TransferOrder>
          storageKey="transfer-orders"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有调拨单"
          scrollX={1440}
        />
      </Card>

      <TransferFormModal
        open={formOpen}
        mode={formMode}
        transferId={editId}
        onClose={() => setFormOpen(false)}
      />
      <TransferInTransitDrawer open={inTransitOpen} onClose={() => setInTransitOpen(false)} />

      <Modal
        title={approveTarget ? `审核调拨单 · ${approveTarget.transfer_no}` : '审核调拨单'}
        open={approveTarget != null}
        onCancel={() => setApproveTarget(null)}
        footer={null}
        destroyOnHidden
      >
        {approveTarget && (
          <>
            <p style={{ marginTop: 0 }}>
              状态：<TransferStatusTag status={approveTarget.status} />
              ；审核通过即源仓逐行预占（待审核 → 待出库），驳回则取消该单。
            </p>
            <Form<ApproveFormValues>
              form={approveForm}
              layout="vertical"
              initialValues={{ action: 'approve' }}
              onFinish={(values) =>
                approveMutation.mutate({ id: approveTarget.id, payload: { action: values.action, opinion: values.opinion?.trim() || undefined } })
              }
            >
              <Form.Item name="action" label="审核结论" rules={[{ required: true }]}>
                <Radio.Group
                  options={[
                    { label: '通过（预占源仓库存）', value: 'approve' },
                    { label: '驳回（取消该单）', value: 'reject' },
                  ]}
                />
              </Form.Item>
              <Form.Item name="opinion" label="审批意见">
                <Input.TextArea rows={3} placeholder="可空" maxLength={200} showCount />
              </Form.Item>
              <Button type="primary" htmlType="submit" icon={<ThunderboltOutlined />} block loading={approveMutation.isPending}>
                提交审核结论
              </Button>
            </Form>
          </>
        )}
      </Modal>

      <Modal
        title={cancelTarget ? `取消调拨单 · ${cancelTarget.transfer_no}` : '取消调拨单'}
        open={cancelTarget != null}
        onCancel={() => setCancelTarget(null)}
        footer={null}
        destroyOnHidden
      >
        {cancelTarget && (
          <>
            <p style={{ marginTop: 0 }}>
              状态：<TransferStatusTag status={cancelTarget.status} />
              ；调拨中/待入库状态不可取消（business-flow.md §13.3，只能反向调拨冲正），审核通过后取消将释放源仓预占。
            </p>
            <Form<CancelFormValues>
              form={cancelForm}
              layout="vertical"
              onFinish={(values) => cancelMutation.mutate({ id: cancelTarget.id, reason: values.reason?.trim() || undefined })}
            >
              <Form.Item name="reason" label="取消原因">
                <Input.TextArea rows={3} placeholder="可空" maxLength={200} showCount />
              </Form.Item>
              <Button danger type="primary" htmlType="submit" block loading={cancelMutation.isPending}>
                确认取消
              </Button>
            </Form>
          </>
        )}
      </Modal>
      {messageContext}
    </div>
  )
}
