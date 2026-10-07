import { useState } from 'react'
import { Button, Flex, Form, Input, Modal, message } from 'antd'
import { CheckOutlined, CloseOutlined, SendOutlined } from '@ant-design/icons'
import { useMutation } from '@tanstack/react-query'
import {
  SALES_RETURN_APPROVE_PERMISSION,
  SALES_RETURN_SUBMIT_PERMISSION,
  salesApi,
  type ReturnApprovePayload,
  type SalesId,
} from '@/api/sales'
import {
  PURCHASE_RETURN_APPROVE_PERMISSION,
  PURCHASE_RETURN_SUBMIT_PERMISSION,
  purchaseApi,
} from '@/api/purchase'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'

/**
 * 退货单列表行内审核操作（销售/采购退货共用，列表无详情页阶段的审核入口）。
 * 按钮可见性 = canAccess 权限点 × 退货状态机（internal/returns/service.go:91-109
 * returnTransitions）：
 * - 提交审核（DRAFT→PENDING_APPROVAL，returns:*return:submit）；
 * - 审核通过 / 驳回（PENDING_APPROVAL，returns:*return:approve；驳回退回 DRAFT，
 *   意见落审批记录——退货域后端 opinion 无必填校验，与采购订单驳回必填不同，按契约可选）。
 * 操作成功回调 onChanged（调用方失效列表缓存重取）；前端权限仅是体验优化，
 * 后端仍做状态与权限最终校验（permission.md §5）。
 */
export function SfReturnAuditActions({
  type,
  orderId,
  status,
  onChanged,
}: {
  /** 退货域类型：sales 走 /api/returns，purchase 走 /api/purchase-returns */
  type: 'sales' | 'purchase'
  orderId: SalesId
  /** 退货单状态（大写枚举，ReturnOrderView.status） */
  status: string
  onChanged: () => void
}) {
  const user = useAuthStore((s) => s.user)
  const [messageApi, contextHolder] = message.useMessage()
  const [rejectOpen, setRejectOpen] = useState(false)
  const [form] = Form.useForm<{ opinion: string }>()

  const submitMutation = useMutation({
    mutationFn: () =>
      type === 'sales' ? salesApi.returns.submit(orderId) : purchaseApi.returns.submit(orderId),
    onSuccess: () => {
      messageApi.success('退货单已提交审核')
      onChanged()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  const approveMutation = useMutation({
    mutationFn: (payload: ReturnApprovePayload) =>
      type === 'sales'
        ? salesApi.returns.approve(orderId, payload)
        : purchaseApi.returns.approve(orderId, payload),
    onSuccess: (_res, variables) => {
      messageApi.success(variables.approved ? '审核通过' : '已驳回回草稿')
      onChanged()
      setRejectOpen(false)
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const canSubmit =
    canAccess(user, type === 'sales' ? SALES_RETURN_SUBMIT_PERMISSION : PURCHASE_RETURN_SUBMIT_PERMISSION) &&
    status === 'DRAFT'
  const canApprove =
    canAccess(user, type === 'sales' ? SALES_RETURN_APPROVE_PERMISSION : PURCHASE_RETURN_APPROVE_PERMISSION) &&
    status === 'PENDING_APPROVAL'

  if (!canSubmit && !canApprove) return <>-</>

  return (
    <Flex gap={4}>
      {contextHolder}
      {canSubmit && (
        <SfConfirm
          okText="提交审核"
          confirming={submitMutation.isPending}
          title="确认提交审核？"
          description="提交后单据进入待审核，审核通过前可驳回回草稿。"
          onConfirm={() => submitMutation.mutate()}
        >
          <Button type="link" size="small" icon={<SendOutlined />}>
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
            description={
              type === 'sales'
                ? '通过后单据进入已审核，可安排收货与质检。'
                : '通过后单据进入已审核，可安排退货出库。'
            }
            onConfirm={() => approveMutation.mutate({ approved: true })}
          >
            <Button type="link" size="small" icon={<CheckOutlined />}>
              通过
            </Button>
          </SfConfirm>
          <Button type="link" size="small" danger icon={<CloseOutlined />} onClick={() => setRejectOpen(true)}>
            驳回
          </Button>
        </>
      )}

      <Modal
        open={rejectOpen}
        title="驳回退货单"
        okText="确认驳回"
        cancelText="取消"
        okButtonProps={{ danger: true, loading: approveMutation.isPending }}
        onCancel={() => {
          form.resetFields()
          setRejectOpen(false)
        }}
        onOk={() =>
          form
            .validateFields()
            .then(({ opinion }) => approveMutation.mutate({ approved: false, opinion: opinion?.trim() || undefined }))
            .catch(() => {
              // 校验失败：Form.Item 已内联提示
            })
        }
        destroyOnHidden
      >
        <Form form={form} layout="vertical" initialValues={{ opinion: '' }}>
          <Form.Item name="opinion" label="审批意见（可选）">
            <Input.TextArea rows={3} placeholder="驳回原因建议填写，将记入审批/审计记录" maxLength={255} showCount />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  )
}
