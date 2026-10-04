import { useState } from 'react'
import { Button, Flex, Form, Input, Modal, message } from 'antd'
import { EditOutlined, StopOutlined } from '@ant-design/icons'
import { useMutation } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  INBOUND_CANCEL_PERMISSION,
  INBOUND_CLOSE_PERMISSION,
  INBOUND_UPDATE_PERMISSION,
  inboundApi,
  type InboundOrder,
} from '@/api/inbound'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'

/**
 * 差额关闭原因弹窗：原因必填（InboundCloseInput binding:"required"，
 * internal/purchase/service_inbound.go:46-49）。
 */
function CloseReasonModal({
  open,
  confirmLoading,
  onCancel,
  onOk,
}: {
  open: boolean
  confirmLoading: boolean
  onCancel: () => void
  onOk: (reason: string) => void
}) {
  const [form] = Form.useForm<{ reason: string }>()
  return (
    <Modal
      open={open}
      title="差额关闭入库单"
      okText="确认关闭"
      cancelText="取消"
      okButtonProps={{ loading: confirmLoading }}
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
          label="关闭原因"
          rules={[{ required: true, message: '差额关闭必须填写原因（plan §6.2）' }]}
        >
          <Input.TextArea rows={3} placeholder="请输入差额关闭原因（将记入审计记录）" maxLength={255} showCount />
        </Form.Item>
      </Form>
    </Modal>
  )
}

/**
 * 入库单详情页操作区（frontend.md §7 Header「关键操作」，仅放真实可用操作）。
 * 按钮可见性 = canAccess 权限点 × 单据状态机（internal/purchase/models.go:83-91
 * inboundTransitions）：
 * - 编辑（DRAFT，purchase:inbound:update）→ 跳 /inbound/:id/edit；
 * - 取消（仅 DRAFT 且无收货，purchase:inbound:cancel）；
 * - 差额关闭（RECEIVING→CLOSED，purchase:inbound:close，原因必填）。
 * 入库无独立提交/审核环节（DRAFT→RECEIVING 由收货确认触发，handleReceiptConfirm）。
 * 前端权限仅是体验优化，后端仍做状态与权限最终校验（permission.md §5）。
 */
export function InboundOrderActions({ order, onChanged }: { order: InboundOrder; onChanged: () => void }) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const [messageApi, contextHolder] = message.useMessage()
  const [closeOpen, setCloseOpen] = useState(false)

  const cancelMutation = useMutation({
    mutationFn: () => inboundApi.cancel(order.id),
    onSuccess: () => {
      messageApi.success('入库单已取消')
      onChanged()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  const closeMutation = useMutation({
    mutationFn: (payload: { reason: string }) => inboundApi.close(order.id, payload),
    onSuccess: () => {
      messageApi.success('入库单已差额关闭')
      onChanged()
      setCloseOpen(false)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const canEdit = canAccess(user, INBOUND_UPDATE_PERMISSION) && order.status === 'DRAFT'
  const canCancel = canAccess(user, INBOUND_CANCEL_PERMISSION) && order.status === 'DRAFT'
  const canClose = canAccess(user, INBOUND_CLOSE_PERMISSION) && order.status === 'RECEIVING'

  if (!canEdit && !canCancel && !canClose) {
    return <>{contextHolder}</>
  }

  return (
    <Flex gap={8} wrap="wrap">
      {contextHolder}
      {canEdit && (
        <Button icon={<EditOutlined />} onClick={() => navigate(`/inbound/${String(order.id)}/edit`)}>
          编辑
        </Button>
      )}
      {canCancel && (
        <SfConfirm
          okText="取消入库单"
          confirming={cancelMutation.isPending}
          title="确认取消该入库单？"
          description="仅草稿状态且未收货的入库单可取消；取消后不可恢复。"
          onConfirm={() => cancelMutation.mutate()}
        >
          <Button danger icon={<StopOutlined />}>
            取消
          </Button>
        </SfConfirm>
      )}
      {canClose && <Button onClick={() => setCloseOpen(true)}>差额关闭</Button>}

      <CloseReasonModal
        open={closeOpen}
        confirmLoading={closeMutation.isPending}
        onCancel={() => setCloseOpen(false)}
        onOk={(reason) => closeMutation.mutate({ reason })}
      />
    </Flex>
  )
}
