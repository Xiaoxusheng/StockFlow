import { useRef, useState } from 'react'
import { Alert, Button, Card, Space, Typography, message } from 'antd'
import {
  DownloadOutlined,
  LeftOutlined,
  RightOutlined,
  ZoomInOutlined,
  ZoomOutOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router'
import { downloadPrintPdf, isPrintTaskFinished, printingApi, resolveObjectTypeLabel, resolvePaperLabel, resolvePaper, type PrintContentRow, type PrintTemplateSnapshot } from '@/api/printing'
import { resolveErrorMessage } from '@/api/client'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { PrintContentRenderer } from '@/components/print/PrintContentRenderer'
import { PrintPaperSheet } from '@/components/print/PrintPaperSheet'
import { SfPrintButton } from '@/components/print/SfPrintButton'

const { Text } = Typography

const ZOOM_MIN = 50
const ZOOM_MAX = 200
const ZOOM_STEP = 10

/** 预览内容行兜底：空行也保证可翻页查看页数，不凭空造内容 */
function resolvePages(task: { rows?: PrintContentRow[] }): PrintContentRow[] {
  return task.rows ?? []
}

/**
 * 独立打印预览页（/data/printing/preview，无菜单路由，frontend.md §13 / printing.md §5）：
 * 真实数据渲染（任务内容行由后端按模板绑定装配，前端不造数据）、缩放、上一页/下一页、
 * react-to-print 打印（SfPrintButton，禁用 window.print）、下载 PDF（GET /api/prints/:id/pdf，
 * 后端未交付呈统一错误态，不伪造 PDF）。
 */
export default function PrintPreviewPage() {
  const [searchParams] = useSearchParams()
  const taskId = searchParams.get('taskId')
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()

  const [zoomPercent, setZoomPercent] = useState(100)
  const [pageIndex, setPageIndex] = useState(0)
  const [downloading, setDownloading] = useState(false)

  /** 打印目标：全部纸张页（预览翻页只是浏览定位，打印输出完整内容） */
  const contentRef = useRef<HTMLDivElement>(null)
  /** 各页锚点：上一页/下一页滚动定位 */
  const pageRefs = useRef<Array<HTMLDivElement | null>>([])

  const taskQuery = useQuery({
    queryKey: ['printing', 'preview', taskId],
    queryFn: () => printingApi.tasks.list({ id: taskId ?? '', page: 1, pageSize: 1 }),
    enabled: Boolean(taskId),
  })

  const task = taskQuery.data?.items?.[0]
  const pages = task ? resolvePages(task) : []
  const paperSpec = task ? resolvePaper(task.paper) : undefined
  const template: PrintTemplateSnapshot | undefined = task?.template

  const gotoPage = (index: number) => {
    const clamped = Math.min(Math.max(index, 0), Math.max(pages.length - 1, 0))
    setPageIndex(clamped)
    pageRefs.current[clamped]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  const handleDownload = async () => {
    if (!task) return
    setDownloading(true)
    try {
      await downloadPrintPdf(task)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloading(false)
    }
  }

  const renderBody = () => {
    if (!taskId) {
      return <SfEmpty description="缺少任务 ID：请从打印中心「打印任务」列表的「预览」进入" />
    }
    if (taskQuery.isPending) {
      return <SfLoading rows={6} />
    }
    if (taskQuery.error) {
      return (
        <SfError
          error={taskQuery.error}
          onRetry={taskQuery.refetch}
          description="打印域接口（GET /api/prints/tasks）未交付或不可用；后端打印域交付后本页自动展示预览"
        />
      )
    }
    if (!task) {
      return <SfEmpty description="未找到该打印任务，可能已被清理或无权访问" />
    }
    if (!paperSpec) {
      return <SfError error={new Error(`任务纸张规格无法识别：${task.paper}`)} onRetry={taskQuery.refetch} />
    }
    if (!template) {
      return (
        <SfError
          error={new Error('任务缺少模板快照，无法渲染预览内容')}
          description="打印任务应携带创建时冻结的模板快照（前端先行契约，后端冻结后回对）"
          onRetry={taskQuery.refetch}
        />
      )
    }
    if (!isPrintTaskFinished(task.status)) {
      return (
        <SfEmpty description="任务正在排队/处理中，内容装配完成后即可预览与下载 PDF（printing.md §1.2 任务模型）" />
      )
    }
    if (task.status === 'FAILED') {
      return <SfError error={new Error(task.errorMessage ?? '打印任务失败，无预览内容')} onRetry={taskQuery.refetch} />
    }
    if (pages.length === 0) {
      return <SfEmpty description="任务成功但后端未返回内容行，无法渲染预览" />
    }

    return (
      <div style={{ maxHeight: 'calc(100vh - 260px)', overflow: 'auto', background: 'var(--sf-color-bg-layout, #f5f5f5)' }}>
        {/* 缩放仅作用于屏幕预览容器，位于打印目标之外，不影响打印输出 */}
        <div style={{ zoom: zoomPercent / 100, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 16, padding: 16, minWidth: 'fit-content' }}>
          <div ref={contentRef}>
            {pages.map((row, index) => (
              <div
                key={row.id}
                ref={(el) => {
                  pageRefs.current[index] = el
                }}
                style={{ scrollMarginTop: 16 }}
              >
                <PrintPaperSheet paper={paperSpec} paddingMm={6} pageBreak={index < pages.length - 1}>
                  <PrintContentRenderer
                    template={template}
                    row={row}
                    printedAt={task.finishedAt ?? task.createdAt}
                    printedBy={task.createdBy}
                  />
                </PrintPaperSheet>
              </div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  const previewReady = Boolean(task && template && paperSpec && isPrintTaskFinished(task.status) && task.status === 'SUCCESS' && pages.length > 0)
  const pageStyle = paperSpec ? `@page { size: ${paperSpec.widthMm}mm ${paperSpec.heightMm}mm; margin: 0; }` : undefined

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="打印预览"
        subtitle={
          task
            ? `任务 ${task.id} · ${resolveObjectTypeLabel(task.objectType)} · 模板 ${task.templateName ?? '-'} · ${resolvePaperLabel(task.paper)}`
            : undefined
        }
        onBack={() => navigate('/data/printing')}
        extra={
          <Space wrap size={8}>
            <Space.Compact>
              <Button
                icon={<ZoomOutOutlined />}
                disabled={!previewReady || zoomPercent <= ZOOM_MIN}
                onClick={() => setZoomPercent((z) => Math.max(ZOOM_MIN, z - ZOOM_STEP))}
              />
              <Button style={{ pointerEvents: 'none' }}>{zoomPercent}%</Button>
              <Button
                icon={<ZoomInOutlined />}
                disabled={!previewReady || zoomPercent >= ZOOM_MAX}
                onClick={() => setZoomPercent((z) => Math.min(ZOOM_MAX, z + ZOOM_STEP))}
              />
            </Space.Compact>
            <Space.Compact>
              <Button
                icon={<LeftOutlined />}
                disabled={!previewReady || pageIndex <= 0}
                onClick={() => gotoPage(pageIndex - 1)}
              >
                上一页
              </Button>
              <Button style={{ pointerEvents: 'none' }}>
                第 {Math.min(pageIndex + 1, Math.max(pages.length, 1))} / 共 {pages.length} 页
              </Button>
              <Button
                disabled={!previewReady || pageIndex >= pages.length - 1}
                onClick={() => gotoPage(pageIndex + 1)}
              >
                下一页
                <RightOutlined />
              </Button>
            </Space.Compact>
            <SfPrintButton
              contentRef={contentRef}
              pageStyle={pageStyle}
              documentTitle={task ? `打印任务_${task.id}` : undefined}
              disabled={!previewReady}
              buttonText="打印"
            />
            <Button
              icon={<DownloadOutlined />}
              disabled={task?.status !== 'SUCCESS'}
              loading={downloading}
              onClick={() => void handleDownload()}
            >
              下载 PDF
            </Button>
          </Space>
        }
      />

      <Card size="small">
        {task && task.errorMessage && isPrintTaskFinished(task.status) && task.status === 'FAILED' && (
          <Alert type="error" showIcon message={task.errorMessage} style={{ marginBottom: 16 }} />
        )}
        {renderBody()}
      </Card>

      <Text type="secondary" style={{ fontSize: 12 }}>
        打印走独立渲染层与 react-to-print（frontend.md §13/§30.1）；PDF 由后端按任务生成（GET /api/prints/:id/pdf），
        后端未交付时呈现错误态，前端不生成假文件（printing.md §5–§6）。
      </Text>
    </div>
  )
}
