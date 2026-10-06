import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, InputNumber, Modal, Select, Space, Typography, message } from 'antd'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router'
import {
  PRINT_OPTIONS_PAGE_SIZE,
  printingApi,
  resolvePaperLabel,
  type BatchResult,
  type PrintTemplateItem,
} from '@/api/printing'
import { resolveErrorMessage } from '@/api/client'
import { BatchResultDrawer } from '@/components/batch/BatchResultDrawer'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

const { Text } = Typography

/**
 * 单任务 data_ids 上限（internal/printing/service.go:40 MaxDataIDs 冻结值；
 * 前端预先禁用创建，后端 PRINT_TOO_MANY_DATA_IDS 仍为最终裁决）。
 */
const MAX_DATA_IDS = 500

/** 打印对象（SKU）。二维码中心批量与详情抽屉单打共用 */
export interface SfQrPrintSku {
  /** SKU 数字 ID——data_ids 通道唯一合法形态为十进制文本 String(sku.id)（qr-code.md §8
   * 通道纪律：SFQR 载荷与 SKU 编码绝不进入该通道，纯数字编码混入会错绑同数字 ID 的 SKU） */
  id: number | string
  /** SKU 编码（SFQR 载荷身份段；不可打印清单展示键，后端 details.disabled_ids 同为编码） */
  code: string
  /** 商品名（展示用；后端 SKU 列表不联表下发，调用方经 options map 传入） */
  productName?: string
  /** 启用状态（前端预筛提示；可打印性最终裁决在后端任务装配，qr-code.md §9） */
  enabled?: boolean
}

export interface SfQrPrintModalProps {
  open: boolean
  /** 打印对象：单打传单元素、批量传全部已选 */
  skus: SfQrPrintSku[]
  onClose: () => void
}

/**
 * 从 PRINT_SKU_DISABLED（409）错误的 details 中提取停用 SKU 编码清单
 * （internal/printing/errors.go:70-74 NewDataDisabledError：details.disabled_ids = 编码数组）。
 */
/**
 * 从批量结果中提取「停用 SKU」失败项的 data_id（=SKU 数字 id 文本）。
 * 2026-10-06 效率层一期：POST /api/prints/tasks 改为批量结果形态（api.md §9），
 * 原 409 + details.disabled_ids 整体拒绝语义废止——不可打印对象逐条 failed(reason=PRINT_SKU_DISABLED)。
 */
const PRINT_SKU_DISABLED = 'PRINT_SKU_DISABLED'
function failedIdsByReason(result: BatchResult | null, reason: string): string[] {
  return (result?.results ?? [])
    .filter((item) => item.status === 'failed' && item.reason === reason)
    .map((item) => item.id)
}

/**
 * SKU 标签打印配置弹窗（frontend.md §13.2 冻结；单打/批打共用）：
 * 模板下拉（SKU_LABEL + 启用）→ 份数 1~100 → 总张数 = 可用数 × 份数 明示 → 创建 PrintTask。
 *
 * 纪律：
 * - data_ids 恒传 String(sku.id) 十进制文本（qr-code.md §8）；
 * - 不可打印清单逐条「{code}：商品已停用」，前端 enabled 预筛 + 后端 409 裁决合并展示，
 *   「仅打印可用」剔除停用后重新提交（约束 10，禁止静默跳过）；
 * - 创建按钮 loading 防重复提交；成功展示任务号并跳转打印预览页（/data/printing/preview?taskId=）；
 * - 历史重打与本弹窗无关（fail-closed 逻辑见 PrintingCenterPage，重打只认行快照 data_id）。
 */
export function SfQrPrintModal({ open, skus, onClose }: SfQrPrintModalProps) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const [messageApi, contextHolder] = message.useMessage()

  const [templateId, setTemplateId] = useState<string>()
  const [copies, setCopies] = useState<number | null>(1)
  /** 「仅打印可用」剔除的 SKU 编码集合（qr-code.md §9 降级提交） */
  const [excludedCodes, setExcludedCodes] = useState<ReadonlySet<string>>(new Set())
  /** 后端 409 PRINT_SKU_DISABLED 裁决返回的停用编码（与前端预筛合并展示） */
  const [backendDisabledCodes, setBackendDisabledCodes] = useState<string[]>([])

  // 每次打开重置会话内状态，避免上一次提交的剔除清单/错误残留
  useEffect(() => {
    if (open) {
      setExcludedCodes(new Set())
      setBackendDisabledCodes([])
      setCopies(1)
    }
  }, [open])

  const templatesQuery = useQuery({
    queryKey: ['printing', 'templates', 'options', 'SKU_LABEL'],
    queryFn: () =>
      printingApi.templates.list({ object_type: 'SKU_LABEL', status: 'ENABLED', page: 1, pageSize: PRINT_OPTIONS_PAGE_SIZE }),
    enabled: open,
  })
  const templates = templatesQuery.data?.items ?? []

  // 前端已知停用（页面快照）∪ 后端 409 裁决（实时数据），均仅为提示；最终以后端装配为准
  const disabledCodes = useMemo(() => {
    const set = new Set(backendDisabledCodes)
    for (const sku of skus) {
      if (sku.enabled === false) set.add(sku.code)
    }
    return [...set]
  }, [backendDisabledCodes, skus])

  const submitSkus = useMemo(() => skus.filter((s) => !excludedCodes.has(s.code)), [skus, excludedCodes])
  const copiesValue = copies ?? 1
  const totalCount = submitSkus.length * copiesValue

  const overLimit = submitSkus.length > MAX_DATA_IDS

  const [batchResult, setBatchResult] = useState<BatchResult | null>(null)
  const [resultOpen, setResultOpen] = useState(false)
  /** data_id（SKU 数字 id 文本）→ SKU 编码：把后端逐条结果还原成可读清单 */
  const codeById = useMemo(() => new Map(skus.map((s) => [String(s.id), s.code])), [skus])

  const createMutation = useMutation({
    mutationFn: (targets: SfQrPrintSku[]) =>
      printingApi.tasks.create({
        template_id: Number(templateId),
        // 通道纪律（qr-code.md §8）：恒为 SKU 数字 ID 十进制文本，绝不传编码/协议载荷
        data_ids: targets.map((s) => String(s.id)),
        copies: copiesValue,
      }),
    onSuccess: (result) => {
      // 批量结果形态（api.md §9 效率层节）：逐条 success/failed/skipped，统一经 BatchResultDrawer 呈现
      setBatchResult(result)
      setResultOpen(true)
      const disabled = failedIdsByReason(result, PRINT_SKU_DISABLED)
        .map((id) => codeById.get(id))
        .filter((code): code is string => !!code)
      if (disabled.length > 0) setBackendDisabledCodes(disabled)
    },
    onError: () => {
      // 整请求参数错误 / 网络错误 → 下方 Alert 如实展示（409 整体拒绝分支已废止）
    },
  })

  /**
   * 结果抽屉关闭：成功项已建任务 → 关闭弹窗并引导到打印中心。
   * 后端批量结果不返回新任务 ID（api.md §9 冻结形状仅逐条 data_id 结果），
   * 无法直达 /data/printing/preview——由打印中心任务列表进入预览（口径变更已披露）。
   */
  const handleResultClose = () => {
    setResultOpen(false)
    const success = batchResult?.success_count ?? 0
    createMutation.reset()
    if (success > 0) {
      messageApi.success(`打印任务已创建（成功 ${success} 张）`)
      onClose()
      if (canAccess(user, 'print:view')) navigate('/data/printing')
    }
  }

  /** 仅重试失败项：以失败 data_ids 重新发起同一创建端点（成功项绝不重跑） */
  const handleRetryFailed = (failedIds: string[]) => {
    const targets = skus.filter((s) => failedIds.includes(String(s.id)))
    if (targets.length === 0 || !templateId) return
    createMutation.mutate(targets)
  }

  const submit = (targets: SfQrPrintSku[]) => {
    if (!templateId) {
      messageApi.warning('请先选择打印模板')
      return
    }
    createMutation.mutate(targets)
  }

  const handlePrintOnlyAvailable = () => {
    const next = new Set(disabledCodes)
    setExcludedCodes(next)
    submit(skus.filter((s) => !next.has(s.code)))
  }

  const canOpenPrintingCenter = canAccess(user, 'print:view')
  const templateEmpty = templatesQuery.isSuccess && templates.length === 0
  const selectedTemplate = templates.find((t) => String(t.id) === templateId)

  return (
    <>
    <Modal
      title="打印二维码标签"
      open={open}
      width={560}
      onCancel={() => {
        createMutation.reset()
        onClose()
      }}
      footer={
        <Space>
          <Button onClick={() => { createMutation.reset(); onClose() }}>取消</Button>
          <Button
            type="primary"
            loading={createMutation.isPending}
            disabled={!templateId || templateEmpty || submitSkus.length === 0 || overLimit}
            onClick={() => submit(submitSkus)}
          >
            创建打印任务
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        {contextHolder}

        {/* 打印对象清单（ask 要点「打印对象/已选数量」；500 行封顶防御 DOM 爆炸） */}
        <div>
          <Text strong>打印对象：{skus.length} 个 SKU</Text>
          <div
            style={{
              marginTop: 4,
              maxHeight: 160,
              overflowY: 'auto',
              padding: '6px 10px',
              border: '1px solid var(--sf-border-subtle)',
              borderRadius: 'var(--sf-radius-md)',
            }}
          >
            {skus.slice(0, 100).map((sku) => (
              <div key={String(sku.id)} style={{ lineHeight: '22px' }}>
                {sku.code}
                {sku.productName ? <Text type="secondary"> · {sku.productName}</Text> : null}
                {sku.enabled === false && <Text type="danger">（已停用）</Text>}
              </div>
            ))}
            {skus.length > 100 && <Text type="secondary">其余 {skus.length - 100} 个不再逐条展示</Text>}
          </div>
        </div>

        {/* 模板选择（printing:template:list 后端校验；空态如实引导，不造假选项） */}
        <div>
          <Text strong>打印模板（SKU 标签）</Text>
          <Select
            style={{ width: '100%', marginTop: 4 }}
            placeholder="请选择启用中的 SKU 标签模板"
            value={templateId}
            onChange={setTemplateId}
            loading={templatesQuery.isPending}
            disabled={templates.length === 0}
            showSearch
            optionFilterProp="label"
            options={templates.map((t: PrintTemplateItem) => ({
              label: `${t.name}（${resolvePaperLabel(t.paper)}）`,
              value: String(t.id),
            }))}
          />
          {templateEmpty && (
            <Alert
              type="info"
              showIcon
              style={{ marginTop: 8 }}
              message="暂无启用中的 SKU 标签模板"
              description={
                canOpenPrintingCenter ? (
                  <span>
                    请先到{' '}
                    <Link to="/data/printing" onClick={() => onClose()}>
                      打印中心
                    </Link>{' '}
                    新建业务类型为「SKU标签」的模板并启用。
                  </span>
                ) : (
                  '请联系管理员到打印中心新建业务类型为「SKU标签」的模板并启用。'
                )
              }
            />
          )}
          {selectedTemplate && (
            <Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
              纸张：{resolvePaperLabel(selectedTemplate.paper)}（二维码标签内容以模板快照为准）
            </Text>
          )}
        </div>

        {/* 份数 1~100（约束 10） */}
        <div>
          <Text strong>打印份数（1~100）</Text>
          <div style={{ marginTop: 4 }}>
            <InputNumber
              min={1}
              max={100}
              precision={0}
              value={copies}
              onChange={(v) => setCopies(v)}
              style={{ width: 160 }}
            />
          </div>
        </div>

        {/* 不可打印清单（约束 10：逐条列出原因，允许「仅打印可用」，禁止静默跳过） */}
        {disabledCodes.length > 0 && (
          <Alert
            type="warning"
            showIcon
            message={`不可打印清单（${disabledCodes.length} 个）：以下 SKU 已停用，创建时将被逐条标记失败`}
            description={
              <>
                <div style={{ maxHeight: 120, overflowY: 'auto' }}>
                  {disabledCodes.map((code) => (
                    <div key={code}>{code}：商品已停用</div>
                  ))}
                </div>
                <Button
                  size="small"
                  style={{ marginTop: 8 }}
                  disabled={submitSkus.length === 0}
                  loading={createMutation.isPending}
                  onClick={handlePrintOnlyAvailable}
                >
                  仅打印可用（{submitSkus.length} 个）
                </Button>
              </>
            }
          />
        )}

        {/* 整请求错误如实展示（resolveErrorMessage 取后端 message）；逐条失败/跳过见结果抽屉 */}
        {createMutation.isError && (
          <Alert type="error" showIcon message={resolveErrorMessage(createMutation.error)} />
        )}

        {/* 总张数 = 可用数 × 份数，加粗明示（约束 10 / qr-code.md §7.3 口径） */}
        <Text>
          预计总张数：<Text strong>{totalCount}</Text>
          <Text type="secondary">（可打印 {submitSkus.length} 个 × 份数 {copiesValue}）</Text>
        </Text>

        {overLimit && (
          <Alert
            type="error"
            showIcon
            message={`可打印对象 ${submitSkus.length} 个超过单任务上限 ${MAX_DATA_IDS} 个，请分批打印`}
          />
        )}
      </Space>
    </Modal>

    {/* 批量结果统一承载面（§2.7）：计数条 + 逐条三态 + 仅重试失败——全站唯一，禁各页自写 */}
    <BatchResultDrawer
      open={resultOpen}
      result={batchResult}
      onRetry={handleRetryFailed}
      retrying={createMutation.isPending}
      onClose={handleResultClose}
    />
    </>
  )
}
