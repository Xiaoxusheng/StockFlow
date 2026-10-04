import { useRef, useState } from 'react'
import { Alert, Button, Card, Space, Tooltip, Typography } from 'antd'
import {
  DownloadOutlined,
  LeftOutlined,
  RightOutlined,
  ZoomInOutlined,
  ZoomOutOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router'
import {
  printingApi,
  resolveObjectTypeLabel,
  resolvePaper,
  resolvePaperLabel,
  type PrintContentRow,
  type PrintTemplateSnapshot,
} from '@/api/printing'
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
 * 按任务 ID 取详情（GET /api/prints/tasks/{id}，internal/printing/handler.go:137），
 * 用任务冻结的模板快照 + 内容行线下渲染（真实数据，前端不造数据）；支持缩放、
 * 上一页/下一页、react-to-print 打印（SfPrintButton，禁用 window.print）。
 * 后端打印域不产出任务 PDF（无 /api/prints/{id}/pdf 端点）——下载按钮如实置灰提示，
 * 不调用不存在的端点、不前端伪造 PDF 文件。
 */
export default function PrintPreviewPage() {
  const [searchParams] = useSearchParams()
  const taskId = searchParams.get('taskId')
  const navigate = useNavigate()

  const [zoomPercent, setZoomPercent] = useState(100)
  const [pageIndex, setPageIndex] = useState(0)

  /** 打印目标：全部纸张页（预览翻页只是浏览定位，打印输出完整内容） */
  const contentRef = useRef<HTMLDivElement>(null)
  /** 各页锚点：上一页/下一页滚动定位 */
  const pageRefs = useRef<Array<HTMLDivElement | null>>([])

  // 任务详情（GET /api/prints/tasks/{id}）：携带模板快照与渲染数据包；内容行在任务创建时
  // 同步装配（internal/printing/service_task.go:343-345），任意状态进入即可线下预览渲染
  const taskQuery = useQuery({
    queryKey: ['printing', 'preview', taskId],
    queryFn: () => printingApi.tasks.detail(taskId as string),
    enabled: Boolean(taskId),
  })

  const task = taskQuery.data
  const pages: PrintContentRow[] = task ? resolvePages(task) : []
  const paperSpec = task ? resolvePaper(task.paper) : undefined
  const template: PrintTemplateSnapshot | undefined = task?.template

  const gotoPage = (index: number) => {
    const clamped = Math.min(Math.max(index, 0), Math.max(pages.length - 1, 0))
    setPageIndex(clamped)
    pageRefs.current[clamped]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
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
          description="打印域接口（GET /api/prints/tasks/{id}）不可用；请检查网络或稍后重试"
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
          description="打印任务应携带创建时冻结的模板快照（GET /api/prints/tasks/{id}，printing.md §1.2）"
          onRetry={taskQuery.refetch}
        />
      )
    }
    if (pages.length === 0) {
      return <SfEmpty description="任务尚未装配内容行，无法渲染预览" />
    }

    return (
      <>
        {task.status === 'FAILED' && (
          <Alert
            type="error"
            showIcon
            message="打印任务渲染失败"
            description={task.error_message || '后端条码预生成失败；以下为任务创建时已装配的内容行，仅供预览核对'}
            style={{ marginBottom: 16 }}
          />
        )}
        {(task.status === 'QUEUED' || task.status === 'PROCESSING') && (
          <Alert
            type="info"
            showIcon
            message="任务正在排队/生成中"
            description="内容行已在任务创建时装配完成，以下为真实打印内容的线下渲染，可先行核对"
            style={{ marginBottom: 16 }}
          />
        )}
        <div style={{ maxHeight: 'calc(100vh - 260px)', overflow: 'auto', background: 'var(--sf-bg)' }}>
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
                      printedAt={task.printed_at ?? task.created_at}
                      printedBy={task.created_by}
                    />
                  </PrintPaperSheet>
                </div>
              ))}
            </div>
          </div>
        </div>
      </>
    )
  }

  const previewReady = Boolean(task && template && paperSpec && pages.length > 0)
  const pageStyle = paperSpec ? `@page { size: ${paperSpec.widthMm}mm ${paperSpec.heightMm}mm; margin: 0; }` : undefined

  return (
    <div className="sf-page">
      <SfPageHeader
        title="打印预览"
        subtitle={
          task
            ? `任务 ${task.print_no} · ${resolveObjectTypeLabel(task.object_type)} · 模板 ${task.template_name ?? '-'} · ${resolvePaperLabel(task.paper)}`
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
              documentTitle={task ? `打印任务_${task.print_no}` : undefined}
              disabled={!previewReady}
              buttonText="打印"
            />
            {/* 后端打印域无任务 PDF 下载端点（internal/printing/handler.go:145 路由收尾于
                GET /barcode）：如实置灰并提示，不调用不存在的端点、不前端伪造 PDF（printing.md §5） */}
            <Tooltip title="后端暂未提供任务 PDF 下载端点；请使用「打印」经浏览器打印层输出">
              <Button icon={<DownloadOutlined />} disabled>
                下载 PDF
              </Button>
            </Tooltip>
          </Space>
        }
      />

      <Card size="small">{renderBody()}</Card>

      <Text type="secondary" style={{ fontSize: 12 }}>
        打印走独立渲染层与 react-to-print（frontend.md §13/§30.1；printing.md §5 真实数据渲染）；
        任务 PDF 下载端点后端暂未交付，前端不生成假文件，当前打印输出经浏览器打印层完成。
      </Text>
    </div>
  )
}
