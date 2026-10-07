import { useState } from 'react'
import { Button, Flex, Form, Input, Modal, message } from 'antd'
import { CheckOutlined, CloseOutlined, EditOutlined, PlusOutlined, SendOutlined, StopOutlined } from '@ant-design/icons'
import { useMutation } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  PURCHASE_APPROVE_PERMISSION,
  PURCHASE_CANCEL_PERMISSION,
  PURCHASE_CLOSE_PERMISSION,
  PURCHASE_SUBMIT_PERMISSION,
  PURCHASE_UPDATE_PERMISSION,
  purchaseApi,
  type PurchaseOrder,
} from '@/api/purchase'
import { INBOUND_CREATE_PERMISSION } from '@/api/inbound'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'

/**
 * 文本原因弹窗（驳回意见 / 差额关闭原因共用）：TextArea + 必填校验。
 * 驳回必填见 ApprovePO（internal/purchase/service_purchase.go:310-312）；
 * 差额关闭原因必填见 ClosePO（service_purchase.go:423-425，存在未上架完成数量时强制）。
 */
function ReasonModal({
  open,
  title,
  okText,
  danger,
  requiredMessage,
  confirmLoading,
  onCancel,
  onOk,
}: {
  open: boolean
  title: string
  okText: string
  danger?: boolean
  requiredMessage: string
  confirmLoading: boolean
  onCancel: () => void
  onOk: (reason: string) => void
}) {
  const [form] = Form.useForm<{ reason: string }>()
  return (
    <Modal
      open={open}
      title={title}
      okText={okText}
      cancelText="取消"
      okButtonProps={{ danger, loading: confirmLoading }}
      onCancel={() => {
        form.resetFields()
        onCancel()
      }}
      onOk={() =>
        form
          .validateFields()
          .then(({ reason }) => onOk(reason.trim()))
          .catch(() => {
            // 校验失败：Form.Item 已内联提示
          })
      }
      destroyOnHidden
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="reason"
          label="意见 / 原因"
          rules={[{ required: true, message: requiredMessage }]}
        >
          <Input.TextArea rows={3} placeholder="请输入（将记入审批/审计记录）" maxLength={255} showCount />
        </Form.Item>
      </Form>
    </Modal>
  )
}

/**
 * 采购订单详情页操作区（frontend.md §7 Header「关键操作」，仅放真实可用操作）。
 * 按钮可见性 = canAccess 权限点 × 单据状态机（internal/purchase/models.go:72-80
 * purchaseTransitions）：
 * - 编辑（DRAFT，purchase:purchase:update）→ 跳 /purchases/:id/edit；
 * - 提交审核（DRAFT→PENDING_APPROVAL，purchase:purchase:submit）；
 * - 审核通过 / 驳回（PENDING_APPROVAL，purchase:purchase:approve，驳回意见必填）；
 * - 取消（DRAFT/PENDING_APPROVAL/APPROVED 且无收货，purchase:purchase:cancel）；
 * - 差额关闭（PARTIAL_RECEIVED/RECEIVED_ALL→COMPLETED，purchase:purchase:close）；
 * - 新建入库单（APPROVED/PARTIAL_RECEIVED/RECEIVED_ALL，purchase:inbound:create，
 *   2026-10-07 联动批次一 L5）：跳 /inbound/new 带 source_type/source_no/仓库预填
 *   （frontend.md §33 契约 2；后端 CreateInbound 强校验来源与仓库一致）。
 * 操作成功统一回调 onChanged（详情页 refetch + 列表缓存失效）；
 * 前端权限仅是体验优化，后端仍做状态与权限最终校验（permission.md §5）。
 */
export function PurchaseOrderActions({ order, onChanged }: { order: PurchaseOrder; onChanged: () => void }) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const [messageApi, contextHolder] = message.useMessage()
  const [rejectOpen, setRejectOpen] = useState(false)
  const [closeOpen, setCloseOpen] = useState(false)

  const submitMutation = useMutation({
    mutationFn: () => purchaseApi.submit(order.id),
    onSuccess: () => {
      messageApi.success('采购订单已提交审核')
      onChanged()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  const approveMutation = useMutation({
    mutationFn: (payload: { approved: boolean; opinion?: string }) =>
      purchaseApi.approve(order.id, payload),
    onSuccess: (_res, variables) => {
      messageApi.success(variables.approved ? '审核通过' : '已驳回回草稿')
      onChanged()
      setRejectOpen(false)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  const cancelMutation = useMutation({
    mutationFn: () => purchaseApi.cancel(order.id),
    onSuccess: () => {
      messageApi.success('采购订单已取消')
      onChanged()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  const closeMutation = useMutation({
    mutationFn: (payload: { reason: string }) => purchaseApi.close(order.id, payload),
    onSuccess: () => {
      messageApi.success('采购订单已差额关闭')
      onChanged()
      setCloseOpen(false)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const canEdit = canAccess(user, PURCHASE_UPDATE_PERMISSION) && order.status === 'DRAFT'
  const canSubmit = canAccess(user, PURCHASE_SUBMIT_PERMISSION) && order.status === 'DRAFT'
  const canApprove = canAccess(user, PURCHASE_APPROVE_PERMISSION) && order.status === 'PENDING_APPROVAL'
  const canCancel =
    canAccess(user, PURCHASE_CANCEL_PERMISSION) &&
    ['DRAFT', 'PENDING_APPROVAL', 'APPROVED'].includes(order.status)
  const canClose =
    canAccess(user, PURCHASE_CLOSE_PERMISSION) &&
    ['PARTIAL_RECEIVED', 'RECEIVED_ALL'].includes(order.status)
  // L5：入库单由人工从已审核 PO 派生（后端无 create-from 端点，POST /api/inbounds
  // 校验 source_no 须 APPROVED/PARTIAL_RECEIVED/RECEIVED_ALL 且仓库一致）
  const canCreateInbound =
    canAccess(user, INBOUND_CREATE_PERMISSION) &&
    ['APPROVED', 'PARTIAL_RECEIVED', 'RECEIVED_ALL'].includes(order.status)

  if (!canEdit && !canSubmit && !canApprove && !canCancel && !canClose && !canCreateInbound) {
    return <>{contextHolder}</>
  }

  return (
    <Flex gap={8} wrap="wrap">
      {contextHolder}
      {canEdit && (
        <Button icon={<EditOutlined />} onClick={() => navigate(`/purchases/${String(order.id)}/edit`)}>
          编辑
        </Button>
      )}
      {canSubmit && (
        <SfConfirm
          okText="提交审核"
          confirming={submitMutation.isPending}
          title="确认提交审核？"
          description="提交后单据进入待审核状态，审核通过前可驳回回草稿。"
          onConfirm={() => submitMutation.mutate()}
        >
          <Button type="primary" icon={<SendOutlined />}>
            提交审核
          </Button>
        </SfConfirm>
      )}
      {canApprove && (
        <>
          <SfConfirm
            okText="审核通过"
            confirming={approveMutation.isPending}
            title="确认审核通过？"
            description="通过后单据进入已审核状态，可安排收货。"
            onConfirm={() => approveMutation.mutate({ approved: true })}
          >
            <Button type="primary" icon={<CheckOutlined />}>
              审核通过
            </Button>
          </SfConfirm>
          <Button danger icon={<CloseOutlined />} onClick={() => setRejectOpen(true)}>
            驳回
          </Button>
        </>
      )}
      {canCancel && (
        <SfConfirm
          okText="取消订单"
          confirming={cancelMutation.isPending}
          title="确认取消该采购订单？"
          description="仅未产生任何收货的订单可取消；取消后不可恢复。"
          onConfirm={() => cancelMutation.mutate()}
        >
          <Button danger icon={<StopOutlined />}>
            取消
          </Button>
        </SfConfirm>
      )}
      {canClose && <Button onClick={() => setCloseOpen(true)}>差额关闭</Button>}
      {canCreateInbound && (
        <Button
          icon={<PlusOutlined />}
          onClick={() =>
            navigate(
              `/inbound/new?source_type=PURCHASE&source_no=${encodeURIComponent(order.po_no)}&warehouse_id=${order.warehouse_id}`,
            )
          }
        >
          新建入库单
        </Button>
      )}

      <ReasonModal
        open={rejectOpen}
        title="驳回采购订单"
        okText="确认驳回"
        danger
        requiredMessage="驳回必须填写审批意见（business-flow.md §12.2）"
        confirmLoading={approveMutation.isPending}
        onCancel={() => setRejectOpen(false)}
        onOk={(reason) => approveMutation.mutate({ approved: false, opinion: reason })}
      />
      <ReasonModal
        open={closeOpen}
        title="差额关闭采购订单"
        okText="确认关闭"
        requiredMessage="存在未上架完成数量时关闭原因必填"
        confirmLoading={closeMutation.isPending}
        onCancel={() => setCloseOpen(false)}
        onOk={(reason) => closeMutation.mutate({ reason })}
      />
    </Flex>
  )
}
