import { useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { Image, Space, Spin } from 'antd'
import { PictureOutlined } from '@ant-design/icons'
import type { ExceptionId, ExceptionItem, ExceptionStatus } from '@/api/exception'
import { EXCEPTION_EXECUTE_PERMISSION, EXCEPTION_STATUS_TAG, exceptionApi } from '@/api/exception'
import { fetchFileObjectUrl, fileApi } from '@/api/file'
import { resolveErrorMessage } from '@/api/client'

// ---------- 异常取证图片消费编排（异常域专属消费代码；共享 api 零改动只消费） ----------
//
// 后端契约（docs/api.md「既有端点接线回对」节 + internal/returns/service_exception_images.go，
// 2026-10-05 已在运行进程上实测通过）：
//   两段式——先 POST /api/files（module=exception、business_no=异常单号，后端侧建议）取得
//   文件 ID，再 POST /api/exceptions/{id}/images 提交 {file_ids:[...]}；服务端校验存在/
//   未过期/image/*，追加 image_refs（jsonb 去重、累计 ≤20）+ 处理记录 + 审计同事务；
//   OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW 可挂接，RESOLVED/CLOSED 409 拒绝（生命周期终点）；
//   权限 returns:exception:execute。
// image_refs 引用形态 "/api/files/{id}/download"（exceptionImageRef 同源），经认证 Blob
// 取流展示（api/file.ts fetchFileObjectUrl——文件中心预览同款通路，裸 <img src> 不带认证头不可用）。
// 消费方：PC 异常中心详情抽屉（ExceptionDetailDrawer）与 Pad 异常页（/pad/exception）——
// 异常域两视图共用本实现，避免第二套上传/取图逻辑漂移。

/** 异常图片累计上限（后端 exceptionImageLimit，service_exception_images.go:38；前端预检仅提示，后端权威校验） */
export const EXCEPTION_IMAGE_LIMIT = 20

/** 图片白名单 accept（api/file.ts FILE_UPLOAD_ACCEPT 的 image 子集——storage 白名单图片段，
 * internal/storage/whitelist.go:20-32；扩展名/MIME 权威校验在后端） */
export const EXCEPTION_IMAGE_ACCEPT = '.png,.jpg,.jpeg,.gif,.webp,.bmp'

/** 可挂接生命周期（service_exception_images.go:100-102：RESOLVED/CLOSED 终点态拒绝） */
export const EXCEPTION_IMAGE_ATTACHABLE_STATUSES: ReadonlyArray<ExceptionStatus> = [
  'OPEN',
  'ASSIGNED',
  'PROCESSING',
  'PENDING_REVIEW',
]

/** 挂接可用性守卫（权限 fail-closed + 生命周期前置态，给出具体原因——死按钮门禁口径） */
export function exceptionImagesDisabledReason(
  detail: Pick<ExceptionItem, 'status'> | null | undefined,
  canExecute: boolean,
): string | undefined {
  if (detail == null) return '详情未加载'
  if (!canExecute) return `缺少 ${EXCEPTION_EXECUTE_PERMISSION} 权限`
  if (!EXCEPTION_IMAGE_ATTACHABLE_STATUSES.includes(detail.status)) {
    const attachableLabels = EXCEPTION_IMAGE_ATTACHABLE_STATUSES.map(
      (status) => EXCEPTION_STATUS_TAG[status].label,
    ).join('/')
    const currentLabel = EXCEPTION_STATUS_TAG[detail.status]?.label ?? detail.status
    return `仅「${attachableLabels}」状态可挂接取证图片（当前 ${currentLabel}）`
  }
  return undefined // 累计 ≤20 的数量预检由调用方按 image_refs.length + files.length 把关（后端兜底）
}

/**
 * 两段式挂接编排：逐张上传文件中心（module=exception、business_no=异常单号，后端侧建议
 * service_exception_images.go:7）→ 一次挂接（追加式，服务端按引用去重）。
 * 中途上传失败即抛错中止：已上传文件留存文件中心（无假删），未挂接部分由用户重试；
 * 重复上传同一张图会产生两条不同文件记录（服务端仅按引用 URL 去重），不前端拼算。
 * 累计 ≤20 预检由调用方按 detail.image_refs.length + files.length 把关（后端兜底 400/409）。
 */
export async function uploadAndAttachExceptionImages(
  id: ExceptionId,
  exceptionNo: string,
  files: ReadonlyArray<File>,
): Promise<ExceptionItem> {
  if (files.length === 0) throw new Error('请先选择要挂接的图片')
  const uploadedIds: Array<number | string> = []
  for (const file of files) {
    const item = await fileApi.upload({ file, module: 'exception', businessNo: exceptionNo })
    uploadedIds.push(item.id)
  }
  return exceptionApi.attachImages(id, { file_ids: uploadedIds })
}

export interface ExceptionImageItem {
  /** 后端 image_refs 原始引用（/api/files/{id}/download） */
  ref: string
  status: 'loading' | 'ready' | 'failed'
  /** 认证 Blob objectUrl（ready 时存在；仅在组件卸载时统一 revoke，见下） */
  url?: string
  /** 取流失败原因（后端 message / 网络错误文案）——如实呈现便于定位（401？404？过期？） */
  error?: string
}

export interface ExceptionImageUrlsResult {
  items: ExceptionImageItem[]
  /** 重新拉取全部图片（失败占位上的「重试」入口） */
  reload: () => void
}

/**
 * image_refs → 认证 objectUrl 列表。
 *
 * **URL 生命周期**：所有 objectUrl 累积在 ref Set 中，**仅在组件卸载时**统一 revoke。
 * 不在 refsKey 变化时 revoke —— 详情刷新（自动刷新 / 窗口聚焦 refetch）会产生新的 refs
 * 数组，此时若立即 revoke 旧 URL，正被 `<img>`/antd 预览层引用的 blob 会失效成破图
 * （「再次点开图片消失」的成因之一）。代价是刷新期间旧 URL 短暂多占内存，可接受。
 *
 * 失败单项降级 failed（带 error 文案）+ 可 reload 重试；不整组报错、不造假图。
 */
export function useExceptionImageUrls(refs: ReadonlyArray<string>): ExceptionImageUrlsResult {
  const refsKey = useMemo(() => refs.join('\n'), [refs])
  const refsRef = useRef(refs)
  refsRef.current = refs
  const [reloadToken, setReloadToken] = useState(0)
  const [items, setItems] = useState<ExceptionImageItem[]>(() =>
    refs.filter((ref) => ref.trim() !== '').map((ref) => ({ ref, status: 'loading' as const })),
  )

  /** 活着的 objectUrl 集合（仅在卸载时统一释放） */
  const urlsRef = useRef<Set<string>>(new Set())
  useEffect(
    () => () => {
      urlsRef.current.forEach((url) => URL.revokeObjectURL(url))
      urlsRef.current.clear()
    },
    [],
  )

  useEffect(() => {
    const list = refsRef.current.filter((ref) => ref.trim() !== '')
    if (list.length === 0) {
      setItems([])
      return
    }
    setItems(list.map((ref) => ({ ref, status: 'loading' as const })))
    let cancelled = false
    void Promise.all(
      list.map(async (ref): Promise<ExceptionImageItem> => {
        try {
          const url = await fetchFileObjectUrl(ref)
          urlsRef.current.add(url)
          return { ref, status: 'ready', url }
        } catch (error) {
          return { ref, status: 'failed', error: resolveErrorMessage(error) }
        }
      }),
    ).then((resolved) => {
      if (!cancelled) setItems(resolved)
    })
    return () => {
      cancelled = true
    }
  }, [refsKey, reloadToken])

  const reload = useMemo(() => () => setReloadToken((token) => token + 1), [])
  return { items, reload }
}

export interface ExceptionImageStripProps {
  refs: ReadonlyArray<string>
  width?: number
  height?: number
}

/**
 * 取证图片缩略图条（antd Image.PreviewGroup 全屏预览；frontend.md §20.6 拍照/预览）。
 * 加载中 Spin 占位、失败 PictureOutlined 占位（title 注明原始引用，如实呈现不造假图）；
 * refs 为空渲染 null（空态文案由调用方承担）。后端无图片移除端点——不提供假删除。
 */
export function ExceptionImageStrip({ refs, width = 72, height = 72 }: ExceptionImageStripProps) {
  const { items, reload } = useExceptionImageUrls(refs)
  if (items.length === 0) return null
  const boxStyle: CSSProperties = {
    width,
    height,
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 2,
    padding: 0,
    borderRadius: 4,
    border: '1px dashed var(--sf-border)',
    background: 'none',
    color: 'var(--sf-text-muted)',
  }
  return (
    <Image.PreviewGroup>
      <Space wrap size={8}>
        {items.map((item, index) =>
          item.status === 'ready' && item.url ? (
            <Image
              key={`${item.ref}-${index}`}
              src={item.url}
              width={width}
              height={height}
              style={{ objectFit: 'cover', borderRadius: 4 }}
            />
          ) : item.status === 'loading' ? (
            <div key={`${item.ref}-${index}`} style={boxStyle} aria-label="图片加载中">
              <Spin size="small" />
            </div>
          ) : (
            /* 失败态：如实给出**原因**并提供**重试**入口——原实现仅一个图标，
               用户只看到「图片不见了」却无从判断是权限、过期还是网络问题。 */
            <button
              type="button"
              key={`${item.ref}-${index}`}
              style={{ ...boxStyle, cursor: 'pointer', font: 'inherit' }}
              title={`图片加载失败：${item.error ?? '未知原因'}\n引用：${item.ref}\n点击重试`}
              aria-label="图片加载失败，点击重试"
              onClick={reload}
            >
              <PictureOutlined />
              <span style={{ fontSize: 11, lineHeight: '14px' }}>失败，重试</span>
            </button>
          ),
        )}
      </Space>
    </Image.PreviewGroup>
  )
}
