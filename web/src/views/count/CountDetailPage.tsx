import { useState } from 'react'
import type { ReactNode } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Steps,
  Timeline,
  Tooltip,
  Typography,
  Upload,
  message,
} from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import type { TimelineProps, UploadFile } from 'antd'
import {
  COUNT_SCOPE_TYPE_LABEL,
  COUNT_TYPE_LABEL,
  countApi,
  type CountId,
  type CountItem,
  type CountItemQuery,
  type CountItemSavePayload,
  type CountItemStatus,
  type CountStatus,
} from '@/api/count'
import { resolveErrorMessage } from '@/api/client'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfLoading } from '@/components/common/SfLoading'
import { SfError } from '@/components/common/SfError'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

const { Text } = Typography

/** 盘点单七态状态 → SfStatusTag（frontend.md §10.5 状态机 + types/status.ts 注册表；待复核走 pending_recheck） */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  PENDING_EXECUTE: { key: 'pending_execute', label: '待执行', semantic: 'pending' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  PENDING_APPROVAL: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 明细行状态 → SfStatusTag（pending/counted 均已注册：待盘复用注册表「待处理」文案，已盘为 counted） */
const COUNT_ITEM_STATUS_TAG: Record<CountItemStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  PENDING: { key: 'pending', label: '待盘', semantic: 'pending' },
  COUNTED: { key: 'counted', label: '已盘', semantic: 'success' },
}

function ItemStatusTag({ status }: { status: CountItemStatus }) {
  const meta = COUNT_ITEM_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态机进度（business-flow.md §10.2 流程的六步线性段；已取消为终止态单独提示） */
const COUNT_STEPS: Array<{ key: CountStatus; title: string }> = [
  { key: 'DRAFT', title: '草稿' },
  { key: 'PENDING_EXECUTE', title: '待执行' },
  { key: 'COUNTING', title: '盘点中' },
  { key: 'PENDING_REVIEW', title: '待复核' },
  { key: 'PENDING_APPROVAL', title: '待审核' },
  { key: 'COMPLETED', title: '已完成' },
]

/** 差异原因选项（前端先行：devices.md §10.3 差异必须进入原因流程，枚举未冻结，后端交付时对齐） */
const REASON_OPTIONS = [
  { label: '计量误差', value: '计量误差' },
  { label: '收货少入', value: '收货少入' },
  { label: '出库错发', value: '出库错发' },
  { label: '货损', value: '货损' },
  { label: '丢失', value: '丢失' },
  { label: '其他', value: '其他' },
]

/** 实盘登记表单值：photos 仅作占位 UI，不随提交（照片上传接口未交付） */
interface CountRegisterFormValues {
  countedQty: number
  reason: string
  remark: string
  photos?: UploadFile[]
}

/** 差异列：实盘未登记显示 -；有差异带符号并以语义警示色突出（不用红绿好坏判断盘盈盘亏） */
function diffCell(countedQty: number | undefined, systemQty: number): ReactNode {
  if (countedQty === undefined || countedQty === null) return '-'
  const diff = countedQty - systemQty
  if (diff === 0) return <span className="sf-num">{formatNumber(diff)}</span>
  return (
    <Text type="warning" className="sf-num">
      {diff > 0 ? `+${formatNumber(diff)}` : formatNumber(diff)}
    </Text>
  )
}

/** 业务流程 Timeline 项（frontend.md §7：每一步显示时间，未发生显示待进行） */
function flowItem(
  title: string,
  time?: string,
  description?: string,
): NonNullable<TimelineProps['items']>[number] {
  return {
    color: time ? 'green' : 'gray',
    children: (
      <>
        <Text strong>{title}</Text>
        <Text type="secondary" style={{ marginLeft: 8 }}>
          {time ? formatDateTime(time) : '待进行'}
        </Text>
        {description && (
          <div>
            <Text type="secondary">{description}</Text>
          </div>
        )}
      </>
    ),
  }
}

/**
 * 盘点详情（/counts/:id，frontend.md §10.5「不能做成普通 CRUD 表格」）：
 * 页头（单号 + 状态 + 操作）→ 硬性规则提示 → SfSummaryBar（系统/实盘/差异/完成率）
 * → Steps 状态机进度 → 盘点范围 → 基础信息 → 盘点明细（行内实盘登记，真实 mutation）
 * → 业务流程 Timeline。差异一律走库存调整单 + 审批链路，页面不提供修改系统库存入口。
 */
export default function CountDetailPage() {
  const { id } = useParams<{ id: string }>()
  const countId: CountId = id ?? ''
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const [form] = Form.useForm<CountRegisterFormValues>()
  const [registering, setRegistering] = useState<CountItem | null>(null)

  const detailQuery = useQuery({
    queryKey: ['counts', countId, 'detail'],
    queryFn: () => countApi.detail(countId),
    enabled: countId !== '',
  })

  const items = usePagedList<CountItem, CountItemQuery>({
    queryKey: ['counts', countId, 'items'],
    fetch: (q) => countApi.items(countId, q),
    params: {},
    enabled: countId !== '',
  })

  const registerMutation = useMutation({
    mutationFn: ({ itemId, payload }: { itemId: CountId; payload: CountItemSavePayload }) =>
      countApi.registerItem(countId, itemId, payload),
    onSuccess: () => {
      messageApi.success('实盘登记已提交')
      setRegistering(null)
      void queryClient.invalidateQueries({ queryKey: ['counts', countId] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 差异预览：实盘数量与系统数量不等时给出调整单链路提示（business-flow.md §10.2）
  const watchedQty = Form.useWatch('countedQty', form)
  const previewDiff =
    registering && typeof watchedQty === 'number' ? watchedQty - registering.systemQty : null

  const openRegister = (record: CountItem) => {
    registerMutation.reset()
    setRegistering(record)
    form.resetFields()
  }

  const handleRegister = () => {
    if (!registering) return
    form
      .validateFields()
      .then((values) =>
        registerMutation.mutate({
          itemId: registering.id,
          payload: {
            countedQty: values.countedQty,
            reason: values.reason,
            remark: values.remark.trim(),
          },
        }),
      )
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const handleRefresh = () => {
    void detailQuery.refetch()
    void items.refetch()
  }

  if (countId === '') {
    return (
      <div className="sf-page">
        <SfPageHeader title="盘点详情" onBack={() => navigate('/counts')} />
        <SfError error={new Error('缺少盘点单 ID')} description="请从盘点中心列表进入详情页" />
      </div>
    )
  }

  const detail = detailQuery.data

  const renderBody = () => {
    if (detailQuery.isPending) {
      return <SfLoading rows={6} />
    }
    if (detailQuery.isError || !detail) {
      return (
        <SfError
          error={detailQuery.error ?? new Error('盘点详情加载失败')}
          onRetry={detailQuery.refetch}
          description={`盘点域接口未交付或不可用（GET /api/counts/${countId}），后端盘点域交付后本页自动恢复`}
        />
      )
    }

    const summary = detail.summary
    const stepIndex = COUNT_STEPS.findIndex((step) => step.key === detail.status)
    const cancelled = detail.status === 'CANCELLED'
    const counting = detail.status === 'COUNTING'

    const itemColumns: ColumnsType<CountItem> = [
      { title: 'SKU 编码', dataIndex: 'skuCode', width: 130, fixed: 'left' },
      {
        title: '商品名称',
        dataIndex: 'productName',
        width: 180,
        ellipsis: true,
        render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
      },
      { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
      { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
      {
        title: '系统数量',
        dataIndex: 'systemQty',
        width: 100,
        align: 'right',
        render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '实盘数量',
        dataIndex: 'countedQty',
        width: 100,
        align: 'right',
        render: (v?: number) =>
          v === undefined || v === null ? '-' : <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '差异',
        key: 'diff',
        width: 90,
        align: 'right',
        render: (_: unknown, record: CountItem) => diffCell(record.countedQty, record.systemQty),
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 90,
        render: (v: CountItemStatus) => <ItemStatusTag status={v} />,
      },
      {
        title: '实盘时间',
        dataIndex: 'countedAt',
        width: 160,
        render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
      },
      {
        title: '操作',
        key: 'actions',
        fixed: 'right',
        width: 100,
        render: (_: unknown, record: CountItem) =>
          record.status === 'PENDING' ? (
            <Tooltip title={counting ? undefined : '仅「盘点中」状态可登记实盘'}>
              <Button type="link" size="small" disabled={!counting} onClick={() => openRegister(record)}>
                实盘登记
              </Button>
            </Tooltip>
          ) : (
            <Text type="secondary">-</Text>
          ),
      },
    ]

    const timelineItems = [
      flowItem('创建盘点', detail.createdAt, `负责人：${detail.ownerName ?? '-'}`),
      flowItem('开始执行（冻结范围，范围内库位/库存锁定）', detail.startedAt),
      flowItem('实盘完成（系统对比生成差异）', detail.completedAt),
      flowItem(
        '差异复核 / 差异审核',
        undefined,
        '审核通过后生成库存调整单并走审批链路，禁止直接修改系统库存（business-flow.md §10.2）',
      ),
      flowItem('完成', undefined),
    ]

    return (
      <>
        {/* 硬性规则明示（business-flow.md §10.2）：页面不提供任何直接修改系统库存的入口 */}
        <Alert
          type="warning"
          showIcon
          message="盘点差异禁止直接修改系统库存"
          description="差异经复核 / 审核通过后由系统生成库存调整单并走审批链路落库（business-flow.md §10.2 硬性规则）"
          style={{ marginBottom: 16 }}
        />
        <SfSummaryBar
          items={[
            { label: '系统库存', value: formatNumber(summary.systemQty) },
            { label: '实盘库存', value: formatNumber(summary.countedQty) },
            { label: '差异数', value: formatNumber(summary.diffQty) },
            { label: '完成率', value: formatPercent(summary.completionRate) },
            {
              label: '已盘 / 明细行',
              value: `${formatNumber(summary.countedItems)} / ${formatNumber(summary.totalItems)}`,
            },
          ]}
        />
        <Card size="small" style={{ marginTop: 16 }}>
          <Steps
            size="small"
            current={cancelled || stepIndex < 0 ? 0 : stepIndex}
            status={cancelled ? 'error' : undefined}
            items={COUNT_STEPS.map((step) => ({ title: step.title }))}
          />
          {cancelled && (
            <Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
              该盘点单已取消，状态机流程终止
            </Text>
          )}
        </Card>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="盘点范围">
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="范围类型">
                {COUNT_SCOPE_TYPE_LABEL[detail.scopeType] ?? detail.scopeType}
              </Descriptions.Item>
              <Descriptions.Item label="范围明细">
                {detail.scopeType === 'ALL' ? '—' : (detail.scopeValue ?? '-')}
              </Descriptions.Item>
              <Descriptions.Item label="范围冻结" span={2}>
                <Text type="secondary">
                  盘点创建后冻结范围内库位 / 库存（COUNT_FREEZE），实盘期间相关库存锁定，结束后按审核结果解冻
                </Text>
              </Descriptions.Item>
            </Descriptions>
          </SfDetailSection>
        </div>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="基础信息">
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="盘点单号">{detail.countNo}</Descriptions.Item>
              <Descriptions.Item label="仓库">
                {detail.warehouseName}
                {detail.warehouseCode ? `（${detail.warehouseCode}）` : ''}
              </Descriptions.Item>
              <Descriptions.Item label="盘点类型">
                {COUNT_TYPE_LABEL[detail.countType] ?? detail.countType}
              </Descriptions.Item>
              <Descriptions.Item label="负责人">{detail.ownerName ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="创建时间">{formatDateTime(detail.createdAt)}</Descriptions.Item>
              <Descriptions.Item label="开始时间">{formatDateTime(detail.startedAt)}</Descriptions.Item>
              <Descriptions.Item label="完成时间">{formatDateTime(detail.completedAt)}</Descriptions.Item>
              <Descriptions.Item label="备注">{detail.remark ?? '-'}</Descriptions.Item>
            </Descriptions>
          </SfDetailSection>
        </div>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="盘点明细">
            <SfTable<CountItem>
              storageKey="count-detail-items"
              rowKey="id"
              columns={itemColumns}
              dataSource={items.items}
              loading={items.isFetching}
              error={items.error}
              onRetry={items.refetch}
              onRefresh={items.refetch}
              pagination={items.pagination}
              total={items.total}
              onPageChange={items.onPageChange}
              emptyText="该盘点单暂无明细行（盘点范围为空或接口未返回）"
              scrollX={1180}
              showFullscreen={false}
            />
          </SfDetailSection>
        </div>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="业务流程">
            <Timeline items={timelineItems} />
          </SfDetailSection>
        </div>
      </>
    )
  }

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title={detail?.countNo ?? '盘点详情'}
        subtitle={detail ? <CountStatusTag status={detail.status} /> : undefined}
        onBack={() => navigate('/counts')}
        extra={
          <Button icon={<ReloadOutlined />} onClick={handleRefresh}>
            刷新
          </Button>
        }
      />
      {renderBody()}

      <Modal
        title={registering ? `实盘登记：${registering.skuCode}` : '实盘登记'}
        open={registering !== null}
        width={560}
        confirmLoading={registerMutation.isPending}
        okText="提交实盘"
        onOk={handleRegister}
        onCancel={() => setRegistering(null)}
      >
        {registering && (
          <>
            {registerMutation.isError && (
              <Alert
                type="error"
                showIcon
                message={resolveErrorMessage(registerMutation.error)}
                description="盘点域接口未交付或不可用（PUT /api/counts/{id}/items/{itemId}），后端交付后实盘登记自动生效"
                style={{ marginBottom: 16 }}
              />
            )}
            {previewDiff !== null && previewDiff !== 0 && (
              <Alert
                type="warning"
                showIcon
                message={`与系统数量存在差异（${previewDiff > 0 ? '+' : ''}${formatNumber(previewDiff)}）`}
                description="差异不可直接修改系统库存，复核 / 审核通过后生成库存调整单并走审批链路"
                style={{ marginBottom: 16 }}
              />
            )}
            <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="商品">{registering.productName}</Descriptions.Item>
              <Descriptions.Item label="库位">{registering.binCode ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="批次">{registering.batchNo ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="系统数量">
                <span className="sf-num">{formatNumber(registering.systemQty)}</span>
              </Descriptions.Item>
            </Descriptions>
            <Form<CountRegisterFormValues> form={form} layout="vertical">
              <Form.Item
                name="countedQty"
                label="实盘数量"
                rules={[
                  { required: true, message: '请输入实盘数量' },
                  { type: 'number', min: 0, message: '实盘数量不能为负数' },
                ]}
              >
                <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="请输入实际清点数量" />
              </Form.Item>
              <Form.Item
                name="reason"
                label="差异原因"
                rules={[{ required: true, message: '必须选择差异原因' }]}
              >
                <Select options={REASON_OPTIONS} placeholder="请选择差异原因" />
              </Form.Item>
              <Form.Item
                name="remark"
                label="备注"
                rules={[{ required: true, message: '必须填写备注说明' }]}
              >
                <Input.TextArea rows={3} maxLength={500} showCount placeholder="补充差异说明（必填）" />
              </Form.Item>
              <Form.Item
                name="photos"
                label="差异照片（占位）"
                extra="照片上传接口未交付：此处仅本地选择预览占位，不随实盘登记提交"
              >
                <Upload
                  listType="picture-card"
                  maxCount={3}
                  accept="image/*"
                  beforeUpload={() => false}
                >
                  <div>
                    <PlusOutlined />
                    <div style={{ marginTop: 8 }}>选择照片</div>
                  </div>
                </Upload>
              </Form.Item>
            </Form>
          </>
        )}
      </Modal>
    </div>
  )
}
