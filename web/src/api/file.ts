import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import {
  EXPORT_MODULE_OPTIONS,
  IMPORT_TYPE_OPTIONS,
  type LabelValueOption,
} from './data'

// ---------- 文件中心（excel.md §7 + api.md §5） ----------
//
// 前端先行契约：api.md 路由表未列独立文件段，以下端点为前端先行定义，
// 后端契约冻结后统一回对（对齐 data.ts 数据中心契约的先行模式）：
//   GET    /api/files               文件列表（module/business_no 筛选，excel.md §7）
//   POST   /api/files               上传（multipart，api.md §5 上传安全）
//   GET    /api/files/:id/download  下载（复用 data.ts downloadFile 认证下载）
//   DELETE /api/files/:id           删除
// 后端未交付时页面呈统一错误态/空态；前端不解析、不伪造、不生成假文件。

/** 文件 ID（后端 ID 出参统一序列化为字符串，internal/database/model.go:22） */
export type FileId = string

/** 文件中心分页筛选（excel.md §7：按业务模块/关联单据过滤可见文件；参数名前端先行，冻结后回对） */
export interface FileQuery extends PageQuery {
  module?: string
  business_no?: string
}

/** 文件记录（excel.md §7 七字段 + 访问地址；字段名前端先行，冻结后回对） */
export interface FileItem {
  id: FileId
  /** 文件名（excel.md §7；存储路径由服务端生成，前端不拼接，api.md §5） */
  fileName: string
  /** 文件类型（MIME 或类型标识，如 image/png / xlsx） */
  fileType?: string
  /** 大小（字节） */
  size?: number
  /** 上传人 */
  uploader?: string
  /** 上传时间（ISO 字符串） */
  uploadedAt?: string
  /** 业务模块（excel.md §7；展示名经 resolveModuleLabel 兜底） */
  module?: string
  /** 业务模块展示名（后端下发；缺省由 resolveModuleLabel 按契约值兜底） */
  moduleName?: string
  /** 关联单据号（excel.md §7） */
  businessNo?: string
  /** 缩略图地址（图片类型；后端下发的可直访地址，加载失败时降级为类型图标） */
  thumbnailUrl?: string
  /** 下载地址（后端下发；缺省回退 /api/files/:id/download，一律经认证下载） */
  downloadUrl?: string
}

/** 上传契约（POST /api/files multipart；模块/单据随文件一起登记，excel.md §7） */
export interface FileUploadPayload {
  file: File
  /** 业务模块（FILE_MODULE_OPTIONS 值集） */
  module: string
  /** 关联单据号（选填） */
  businessNo?: string
}

/** 文件中心业务模块选项（excel.md §7 统一登记；值集 = 导入类型 ∪ 导出模块去重，
 * 复用 data.ts 既有模块契约命名，前端先行，冻结后回对） */
export const FILE_MODULE_OPTIONS: LabelValueOption[] = Array.from(
  new Map([...IMPORT_TYPE_OPTIONS, ...EXPORT_MODULE_OPTIONS].map((option) => [option.value, option])).values(),
)

/** 上传白名单前端镜像（api.md §5：图片/Excel/PDF/附件；
 * 仅作选择器提示，扩展名/MIME/大小上限以后端权威校验为准） */
export const FILE_UPLOAD_ACCEPT = '.png,.jpg,.jpeg,.gif,.webp,.bmp,.xlsx,.xls,.csv,.pdf,.doc,.docx,.zip'

/** 上传与下载耗时高于默认 15s（对齐 data.ts DATA_REQUEST_TIMEOUT_MS） */
const FILE_REQUEST_TIMEOUT_MS = 60_000

/** 下载地址：后端下发优先，缺省回退契约端点（不拼接任何用户输入，api.md §5） */
export function resolveFileDownloadPath(file: Pick<FileItem, 'id' | 'downloadUrl'>): string {
  return file.downloadUrl ?? `/api/files/${file.id}/download`
}

/**
 * 认证获取文件流并转为 objectUrl（预览用）：与 data.ts downloadFile 同一模式
 * （axios 单例携带认证头 + Blob），供受控 Image 预览展示；调用方在关闭预览时
 * URL.revokeObjectURL 释放。请求失败抛 ApiError，由调用方呈可读错误态。
 */
export async function fetchFileObjectUrl(url: string): Promise<string> {
  const blob = await http.get<Blob>(url, {
    responseType: 'blob',
    timeout: FILE_REQUEST_TIMEOUT_MS,
  })
  return URL.createObjectURL(blob)
}

export const fileApi = {
  /** 文件列表（excel.md §7：文件名/类型/大小/上传人/时间/业务模块/关联单据） */
  list: (query: FileQuery) => http.get<PageResult<FileItem>>('/api/files', { params: query }),
  /** 上传（multipart；Content-Type 与 boundary 由 axios 按 FormData 自动携带） */
  upload: (payload: FileUploadPayload) => {
    const form = new FormData()
    form.append('module', payload.module)
    if (payload.businessNo) form.append('business_no', payload.businessNo)
    form.append('file', payload.file)
    return http.post<FileItem>('/api/files', form, {
      timeout: FILE_REQUEST_TIMEOUT_MS,
    })
  },
  /** 删除（危险操作，前端经 SfConfirm 二次确认；api.md §6：敏感操作由后端记操作日志） */
  remove: (id: FileId) => http.delete<unknown>(`/api/files/${id}`),
}
