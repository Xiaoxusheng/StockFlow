import { useEffect, useMemo, useState } from 'react'
import { Button, Descriptions, Drawer, Empty, Input, Select, Space, Tag, Timeline, Tooltip, Typography, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ExceptionId, ExceptionItem, ExceptionStatus } from '@/api/exception'
import {
  EXCEPTION_ASSIGN_PERMISSION,
  EXCEPTION_CLOSE_PERMISSION,
  EXCEPTION_EXECUTE_PERMISSION,
  EXCEPTION_STATUS_TAG,
  exceptionApi,
} from '@/api/exception'
import { resolveErrorMessage } from '@/api/client'
import { buildBinCodeMap, buildSkuMaps, buildUserNameMap, fetchBinOptions, fetchSkuOptions, fetchUserOptions } from '@/api/options'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { canAccess } from '@/types/permission'
import { useAuthStore } from '@/stores/auth'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'

const { Text } = Typography
const NOTE_MAX = 200

export interface ExceptionDetailDrawerProps {
  open: boolean
  exceptionId: ExceptionId | null
  onClose: () => void
}

type LifecycleAction = 'assign' | 'start' | 'review' | 'resolve' | 'close'

/** 处理动作语义（business-flow.md §11.2 生命周期；前置态与权限码一一对应） */
const ACTION_META: Record<LifecycleAction, { label: string; from: ExceptionStatus; permission: string }> = {
  assign: { label: '分派', from: 'OPEN', permission: EXCEPTION_ASSIGN_PERMISSION },
  start: { label: '开始处理', from: 'ASSIGNED', permission: EXCEPTION_EXECUTE_PERMISSION },
  review: { label: '提交复核', from: 'PROCESSING', permission: EXCEPTION_EXECUTE_PERMISSION },
  resolve: { label: '解决', from: 'PENDING_REVIEW', permission: EXCEPTION_EXECUTE_PERMISSION },
  close: { label: '关闭', from: 'RESOLVED', permission: EXCEPTION_CLOSE_PERMISSION },
}

/** 异常状态 → SfStatusTag（六态大写枚举经 EXCEPTION_STATUS_TAG 指定 label/semantic，见 api/exception.ts） */
function ExceptionStatusTag({ status }: { status: ExceptionStatus }) {
  const meta = EXCEPTION_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/**
 * 异常单详情抽屉（GET /api/exceptions/{id}，ExceptionView 全量，internal/returns/handler.go:563-574）：
 * 主档字段 + 追加式处理记录（handle_records jsonb，business-flow.md §11.2）+ 冻结联动展示
 * （freeze_lock_id，解决/关闭时后端同事务释放，inventory-rules.md §4.2）；
 * 生命周期动作真实接线（POST assign/start/review/resolve/close），前置权限码经 canAccess
 * fail-closed 控制、状态机前置态不满足时 disabled + Tooltip 说明原因（死按钮门禁）。
 * image_refs 后端有列无写入路径（创建恒空数组，service_exception.go:140）——只读展示，
 * 上传能力属后端侧条目，不提供假入口。
 */
export default function ExceptionDetailDrawer({ open, exceptionId, onClose }: ExceptionDetailDrawerProps) {
  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.user)
  const canAssign = canAccess(user, EXCEPTION_ASSIGN_PERMISSION)
  const canExecute = canAccess(user, EXCEPTION_EXECUTE_PERMISSION)
  const canClose = canAccess(user, EXCEPTION_CLOSE_PERMISSION)

  const [assigneeId, setAssigneeId] = useState<string | undefined>(undefined)
  const [note, setNote] = useState('')

  useEffect(() => {
    if (open) {
      setAssigneeId(undefined)
      setNote('')
    }
  }, [open, exceptionId])

  const detailQuery = useQuery({
    queryKey: ['exception', 'center', 'detail', String(exceptionId ?? '')],
    queryFn: () => {
      if (exceptionId == null) throw new Error('未选择异常单')
      return exceptionApi.detail(exceptionId)
    },
    enabled: open && exceptionId != null,
  })

  // 编码映射（列表/详情出参仅 ID，基础资料 options 本地映射，失败降级为 #id）
  const skuOptions = useQuery({ queryKey: ['exception-detail', 'sku-options'], queryFn: fetchSkuOptions, enabled: open })
  const binOptions = useQuery({ queryKey: ['exception-detail', 'bin-options'], queryFn: fetchBinOptions, enabled: open })
  const userOptions = useQuery({ queryKey: ['exception-detail', 'user-options'], queryFn: fetchUserOptions, enabled: open })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const binCodeMap = useMemo(() => buildBinCodeMap(binOptions.data ?? []), [binOptions.data])
  const userNameMap = useMemo(() => buildUserNameMap(userOptions.data ?? []), [userOptions.data])

  const skuCodeOf = (id: number) => (String(id) === '0' ? EMPTY_TEXT : (skuMaps.code.get(String(id)) ?? `#${String(id)}`))
  const binCodeOf = (id: number) => (String(id) === '0' ? EMPTY_TEXT : (binCodeMap.get(String(id)) ?? `#${String(id)}`))
  const userLabelOf = (id: string) => userNameMap.get(id) ?? `#${id}`

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['exception', 'center'] })
  }

  // 分派：OPEN→ASSIGNED（PC 侧分派给任意用户；Pad 侧认领=分派给自己）
  const assignMutation = useMutation({
    mutationFn: () => {
      if (!assigneeId) throw new Error('请选择处理人')
      return exceptionApi.assign(exceptionId!, {
        assignee_id: Number(assigneeId),
        assignee_name: userLabelOf(assigneeId),
      })
    },
    onSuccess: (view) => {
      messageApi.success(`已分派给 ${view.assignee_name}（待处理 → 已分派）`)
      setNote('')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 迁移动作：start/review/resolve/close（note 可选；resolve/close 由后端释放异常冻结）
  const lifecycleMutation = useMutation({
    mutationFn: (action: Exclude<LifecycleAction, 'assign'>) =>
      exceptionApi[action](exceptionId!, { note: note.trim() || undefined }),
    onSuccess: (view, action) => {
      const fromTo: Record<Exclude<LifecycleAction, 'assign'>, string> = {
        start: '已分派 → 处理中',
        review: '处理中 → 待复核',
        resolve: '待复核 → 已解决（异常冻结已按需释放）',
        close: '已解决 → 已关闭',
      }
      messageApi.success(`异常单 ${view.exception_no}：${fromTo[action]}`)
      setNote('')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const detail: ExceptionItem | undefined = detailQuery.data
  const currentStatus: ExceptionStatus | undefined = detail?.status

  /** 动作可用性：权限 fail-closed + 状态机前置态，给出具体原因（死按钮门禁） */
  const actionReason = (action: LifecycleAction): string | undefined => {
    if (detail == null) return '详情未加载'
    const meta = ACTION_META[action]
    const hasPermission =
      action === 'assign' ? canAssign : action === 'close' ? canClose : canExecute
    if (!hasPermission) return `缺少 ${meta.permission} 权限`
    if (currentStatus !== meta.from) {
      return `仅「${EXCEPTION_STATUS_TAG[meta.from].label}」状态可${meta.label}（当前 ${
        EXCEPTION_STATUS_TAG[detail.status]?.label ?? detail.status
      }）`
    }
    if (action === 'assign' && !assigneeId) return '请先选择处理人'
    return undefined
  }

  return (
    <Drawer
      title={detail ? `异常单详情 · ${detail.exception_no}` : '异常单详情'}
      open={open}
      onClose={onClose}
      width={760}
      destroyOnHidden
    >
      {messageContext}
      {detailQuery.isPending ? (
        <SfLoading rows={8} />
      ) : detailQuery.error ? (
        <SfError
          error={detailQuery.error}
          description="异常单详情（GET /api/exceptions/{id}）加载失败"
          onRetry={() => void detailQuery.refetch()}
        />
      ) : !detail ? (
        <Empty description="未选择异常单" />
      ) : (
        <div style={{ display: 'grid', gap: 20 }}>
          <Descriptions
            size="small"
            column={2}
            title={
              <Space>
                <ExceptionStatusTag status={detail.status} />
                <Text type="secondary">{detail.exception_no}</Text>
              </Space>
            }
            items={[
              { key: 'type', label: '异常类型', children: detail.type },
              { key: 'source', label: '来源', children: `${detail.source_type || EMPTY_TEXT} ${detail.source_no || EMPTY_TEXT}`.trim() || EMPTY_TEXT },
              { key: 'sku', label: 'SKU', children: skuCodeOf(detail.sku_id) },
              { key: 'bin', label: '库位', children: binCodeOf(detail.bin_id) },
              { key: 'serial', label: '序列号', children: detail.serial_no || EMPTY_TEXT },
              { key: 'owner', label: '责任人', children: detail.owner_name || EMPTY_TEXT },
              { key: 'assignee', label: '处理人', children: detail.assignee_name || EMPTY_TEXT },
              { key: 'created', label: '登记时间', children: formatDateTime(detail.created_at) },
              { key: 'assigned', label: '分派时间', children: formatDateTime(detail.assigned_at) },
              { key: 'resolved', label: '解决时间', children: formatDateTime(detail.resolved_at) },
              { key: 'closed', label: '关闭时间', children: formatDateTime(detail.closed_at) },
              {
                key: 'freeze',
                label: '异常冻结',
                children:
                  String(detail.freeze_lock_id) === '0'
                    ? '未冻结'
                    : `锁 #${String(detail.freeze_lock_id)}（解决/关闭时后端释放）`,
              },
              { key: 'detail', label: '描述', children: detail.detail || EMPTY_TEXT, span: 2 },
              { key: 'remark', label: '备注', children: detail.remark || EMPTY_TEXT, span: 2 },
              {
                key: 'images',
                label: '图片',
                children: detail.image_refs.length > 0 ? (
                  <Space wrap>{detail.image_refs.map((ref, i) => <Tag key={`${ref}-${i}`}>{ref}</Tag>)}</Space>
                ) : (
                  <Text type="secondary">无（后端异常域暂无图片上传/挂接端点，image_refs 有列无写入路径）</Text>
                ),
                span: 2,
              },
            ]}
          />

          <section aria-label="处理记录">
            <Text strong>处理记录（追加式台账，{detail.handle_records.length} 条）</Text>
            {detail.handle_records.length === 0 ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无处理记录" style={{ margin: '12px 0' }} />
            ) : (
              <Timeline
                style={{ marginTop: 12 }}
                items={detail.handle_records.map((record, index) => ({
                  key: `${String(record.at)}-${record.action}-${index}`,
                  children: (
                    <div>
                      <Space wrap size={8}>
                        <Tag>{record.action}</Tag>
                        <Text>{record.by_name || `#${String(record.by_id)}`}</Text>
                        <Text type="secondary">{formatDateTime(record.at)}</Text>
                        {record.qty ? <Text type="secondary">数量 {record.qty}</Text> : null}
                      </Space>
                      {record.note ? (
                        <div>
                          <Text type="secondary" style={{ whiteSpace: 'pre-wrap' }}>{record.note}</Text>
                        </div>
                      ) : null}
                    </div>
                  ),
                }))}
              />
            )}
          </section>

          <section aria-label="生命周期操作">
            <Text strong>生命周期操作</Text>
            <Input.TextArea
              rows={2}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              maxLength={NOTE_MAX}
              showCount
              placeholder="处理备注（可选，随动作写入追加式处理记录）"
              style={{ margin: '8px 0' }}
            />
            <Space wrap>
              {currentStatus === 'OPEN' ? (
                <Select
                  showSearch
                  optionFilterProp="label"
                  placeholder="选择处理人"
                  style={{ minWidth: 180 }}
                  loading={userOptions.isLoading}
                  disabled={userOptions.isError}
                  value={assigneeId}
                  onChange={setAssigneeId}
                  options={
                    userOptions.isError
                      ? []
                      : (userOptions.data ?? []).map((u) => ({
                          value: String(u.id),
                          label: u.real_name || u.username,
                        }))
                  }
                />
              ) : null}
              {(Object.keys(ACTION_META) as LifecycleAction[]).map((action) => {
                const reason = actionReason(action)
                const meta = ACTION_META[action]
                const loading =
                  (action === 'assign' && assignMutation.isPending) ||
                  (action !== 'assign' && lifecycleMutation.isPending && lifecycleMutation.variables === action)
                return (
                  <Tooltip key={action} title={reason ?? `${meta.label}（${EXCEPTION_STATUS_TAG[meta.from].label} → 下一态）`}>
                    <Button
                      type={action === 'resolve' || action === 'close' ? 'primary' : 'default'}
                      danger={action === 'close'}
                      disabled={!!reason}
                      loading={loading}
                      onClick={() => {
                        if (action === 'assign') {
                          assignMutation.mutate()
                        } else {
                          lifecycleMutation.mutate(action)
                        }
                      }}
                    >
                      {meta.label}
                    </Button>
                  </Tooltip>
                )
              })}
            </Space>
            <div style={{ marginTop: 8 }}>
              <Text type="secondary">
                动作需 returns:exception:* 权限并按状态机前置态启用；解决/关闭时后端同事务释放异常冻结
                （inventory-rules.md §4.2），前端不算库存
              </Text>
            </div>
          </section>
        </div>
      )}
    </Drawer>
  )
}
