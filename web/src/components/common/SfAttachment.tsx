import { useState } from 'react'
import { Button, Image, List, theme, Tooltip, Typography, Upload } from 'antd'
import {
  DeleteOutlined,
  DownloadOutlined,
  EyeOutlined,
  FileOutlined,
  UploadOutlined,
} from '@ant-design/icons'
import type { ReactNode } from 'react'
import { SfConfirm } from './SfConfirm'
import { SfEmpty } from './SfEmpty'
import { formatDateTime, formatFileSize } from '@/utils/format'

const { Text } = Typography

/** 附件条目通用形状（文件中心 FileItem 等结构兼容即可直接传入） */
export interface AttachmentItem {
  id: string | number
  fileName: string
  /** 文件类型（MIME 或类型标识，如 image/png / xlsx） */
  fileType?: string
  /** 大小（字节） */
  size?: number
  uploader?: string
  /** 上传时间（ISO 字符串） */
  uploadedAt?: string
  /** 缩略图地址（图片类型；后端下发的可直访地址，加载失败降级为类型图标） */
  thumbnailUrl?: string
}

const IMAGE_EXTENSIONS = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp']

/** 图片类型判定：MIME image/* / IMAGE 分类 / 常见图片扩展名；
 * 缩略图与预览动作仅对图片条目生效（frontend.md §15.4） */
export function isImageAttachment(file?: {
  fileType?: string | null
  fileName?: string | null
}): boolean {
  const type = file?.fileType ?? ''
  if (type.toLowerCase().startsWith('image/') || type.toUpperCase() === 'IMAGE') return true
  const name = file?.fileName ?? ''
  const dot = name.lastIndexOf('.')
  return dot >= 0 && IMAGE_EXTENSIONS.includes(name.slice(dot + 1).toLowerCase())
}

export interface SfAttachmentProps<T extends AttachmentItem> {
  items: T[]
  loading?: boolean
  /** 块标题（如「待上传文件」「质检照片」） */
  title?: ReactNode
  /** 标题行右侧附加区 */
  extra?: ReactNode
  /** 图片判定（默认 isImageAttachment） */
  isImage?: (item: T) => boolean
  /** 预览（仅图片条目展示）：返回图片地址（objectUrl 或可直访 URL）即打开受控
   * antd Image 预览（缩放/全屏由预览工具栏提供）；请求失败由消费方提示后 resolve。 */
  onPreview?: (item: T) => Promise<string | void>
  previewingId?: string | number | null
  /** 认证下载（消费方经 downloadFile 等实现；未提供则不展示下载动作） */
  onDownload?: (item: T) => void
  downloadingId?: string | number | null
  /** 删除（组件内经 SfConfirm 危险确认后调用；未提供则不展示删除动作） */
  onDelete?: (item: T) => void
  deletingId?: string | number | null
  /** 上传入口：选择文件后回调（multipart 提交、白名单权威校验由消费方与后端负责，api.md §5） */
  onUpload?: (files: File[]) => void
  uploading?: boolean
  /** 上传 input accept（前端镜像 api.md §5 白名单，后端为权威校验） */
  uploadAccept?: string
  uploadMultiple?: boolean
  /** 上传入口文案 */
  uploadLabel?: string
  /** 隐藏上传入口（只读视图） */
  showUploadEntry?: boolean
  emptyText?: string
}

/**
 * 可复用附件块（frontend.md §15.4：上传/预览/下载/删除，图片缩略图/预览/全屏）。
 * 纯展示 + 交互壳：数据获取与提交全部经回调注入，可嵌入文件中心、单据详情等任意业务上下文。
 */
export function SfAttachment<T extends AttachmentItem>({
  items,
  loading,
  title,
  extra,
  isImage,
  onPreview,
  previewingId,
  onDownload,
  downloadingId,
  onDelete,
  deletingId,
  onUpload,
  uploading,
  uploadAccept,
  uploadMultiple = true,
  uploadLabel = '上传附件',
  showUploadEntry = true,
  emptyText = '暂无附件',
}: SfAttachmentProps<T>) {
  const { token } = theme.useToken()
  const [preview, setPreview] = useState<{ open: boolean; src: string }>({ open: false, src: '' })
  const imageOf = isImage ?? isImageAttachment

  const openPreview = (item: T) => {
    if (!onPreview) return
    void Promise.resolve(onPreview(item))
      .then((url) => {
        if (url) setPreview({ open: true, src: url })
      })
      .catch(() => {
        // 失败提示由消费方负责（约定 onPreview 内部呈可读错误态）
      })
  }

  /** 关闭受控预览并释放 objectUrl（非 objectUrl 时 revoke 为无操作，统一安全） */
  const closePreview = (open: boolean) => {
    if (!open) {
      if (preview.src) URL.revokeObjectURL(preview.src)
      setPreview({ open: false, src: '' })
    }
  }

  const actionsFor = (item: T): ReactNode[] => {
    const actions: ReactNode[] = []
    if (onPreview && imageOf(item)) {
      actions.push(
        <Tooltip key="preview" title="预览">
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            loading={previewingId === item.id}
            onClick={() => openPreview(item)}
          />
        </Tooltip>,
      )
    }
    if (onDownload) {
      actions.push(
        <Tooltip key="download" title="下载">
          <Button
            type="link"
            size="small"
            icon={<DownloadOutlined />}
            loading={downloadingId === item.id}
            onClick={() => onDownload(item)}
          />
        </Tooltip>,
      )
    }
    if (onDelete) {
      actions.push(
        <SfConfirm
          key="delete"
          title="确认删除该附件？"
          description="删除后文件不可恢复。"
          okText="删除"
          confirming={deletingId === item.id}
          onConfirm={() => onDelete(item)}
        >
          <Button type="link" size="small" danger icon={<DeleteOutlined />} />
        </SfConfirm>,
      )
    }
    return actions
  }

  const uploadEntry = onUpload && showUploadEntry && (
    <Upload
      accept={uploadAccept}
      multiple={uploadMultiple}
      showUploadList={false}
      disabled={uploading}
      beforeUpload={(file, fileList) => {
        // 阻止 antd 自动上传；多选时 beforeUpload 逐文件触发，仅以首个文件为准回调一次
        if (fileList[0] === file) onUpload([...fileList])
        return false
      }}
    >
      <Button icon={<UploadOutlined />} loading={uploading}>
        {uploadLabel}
      </Button>
    </Upload>
  )

  return (
    <div>
      {(title || uploadEntry) && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginBottom: 8 }}>
          <Text strong>{title}</Text>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {extra}
            {uploadEntry}
          </div>
        </div>
      )}
      <List
        size="small"
        loading={loading}
        dataSource={items}
        rowKey={(item) => String(item.id)}
        locale={{ emptyText: <SfEmpty description={emptyText} /> }}
        renderItem={(item) => (
          <List.Item actions={actionsFor(item)}>
            <List.Item.Meta
              avatar={
                imageOf(item) && item.thumbnailUrl ? (
                  <Image
                    width={36}
                    height={36}
                    src={item.thumbnailUrl}
                    preview={false}
                    style={{ objectFit: 'cover', borderRadius: 4 }}
                  />
                ) : (
                  <FileOutlined style={{ fontSize: 24, color: token.colorTextSecondary }} />
                )
              }
              title={
                <Text style={{ maxWidth: 420 }} ellipsis={{ tooltip: item.fileName }}>
                  {item.fileName}
                </Text>
              }
              description={
                <Text type="secondary">
                  {[formatFileSize(item.size), item.uploader, formatDateTime(item.uploadedAt)]
                    .filter((part) => part && part !== '-')
                    .join(' · ') || '-'}
                </Text>
              }
            />
          </List.Item>
        )}
      />
      <Image
        style={{ display: 'none' }}
        src={preview.src || undefined}
        preview={{
          open: preview.open,
          src: preview.src || undefined,
          onOpenChange: closePreview,
        }}
      />
    </div>
  )
}
