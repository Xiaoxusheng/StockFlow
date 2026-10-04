import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import {
  EXPORT_MODULE_OPTIONS,
  IMPORT_TYPE_OPTIONS,
  type LabelValueOption,
} from './data'

// ---------- 文件中心（后端 M3 已交付：GET/POST /api/files + /{id}/download|preview + DELETE，
// internal/datax/handler.go:118-124；出参 FileItem snake_case，service_file.go:36-50） ----------

/** 文件 ID（后端 int64 出参为数字；保留 string 兼容——backend-m1-plan §1 全局约定） */
export type FileId = number | string

/** 文件中心分页筛选（handler.go:311-324：module/business_no/keyword） */
export interface FileQuery extends PageQuery {
  module?: string
  business_no?: string
  /** 文件名模糊匹配（handler.go:318） */
  keyword?: string
}

/** 文件记录（FileItem，internal/datax/service_file.go:36-50 字段全量；
 * excel.md §7 记录字段 + 访问地址） */
export interface FileItem {
  id: FileId
  /** 文件名（存储路径由服务端生成，前端不拼接，api.md §5） */
  file_name: string
  /** 扩展名白名单值（.xlsx 等） */
  file_type: string
  mime_type: string
  /** 大小（字节） */
  size_bytes: number
  /** 业务模块（展示名经 resolveModuleLabel 兜底） */
  module: string
  /** 业务模块展示名（后端下发） */
  module_name?: string
  /** 关联单据号 */
  business_no?: string
  uploader_id?: number
  uploader_name: string
  /** 下载地址（后端下发；一律经认证 Blob 下载） */
  download_url: string
  /** 文件有效期（过期自动清理） */
  expires_at?: string
  created_at: string
}

/** 上传契约（POST /api/files multipart 字段：module / business_no / file，
 * FileUploadInput，service_file.go:26-30） */
export interface FileUploadPayload {
  file: File
  /** 业务模块（FILE_MODULE_OPTIONS 值集） */
  module: string
  /** 关联单据号（选填） */
  businessNo?: string
}

/** 文件中心业务模块选项（excel.md §7 统一登记；值集 = 导入类型 ∪ 导出模块去重，
 * 复用 data.ts 模块契约命名） */
export const FILE_MODULE_OPTIONS: LabelValueOption[] = Array.from(
  new Map([...IMPORT_TYPE_OPTIONS, ...EXPORT_MODULE_OPTIONS].map((option) => [option.value, option])).values(),
)

/** 上传白名单前端镜像（internal/storage/whitelist.go:20-32 冻结白名单逐值同源：
 * 图片/.xlsx/.csv/.pdf/.txt/.zip——Excel 仅收 .xlsx，excelize v2 不支持旧版 .xls，
 * .doc/.docx 完全不在白名单（2026-10-04 实测 .doc 上传 400 STORAGE_TYPE_NOT_ALLOWED）。
 * 仅作选择器提示，扩展名/MIME/大小上限以后端权威校验为准） */
export const FILE_UPLOAD_ACCEPT = '.png,.jpg,.jpeg,.gif,.webp,.bmp,.xlsx,.csv,.pdf,.txt,.zip'

/** 上传与下载耗时高于默认 15s（对齐 data.ts DATA_REQUEST_TIMEOUT_MS） */
const FILE_REQUEST_TIMEOUT_MS = 60_000

/** 下载地址：后端下发优先，缺省回退契约端点（不拼接任何用户输入，api.md §5） */
export function resolveFileDownloadPath(file: Pick<FileItem, 'id' | 'download_url'>): string {
  return file.download_url ?? `/api/files/${file.id}/download`
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
  /** 文件列表（GET /api/files：文件名/类型/大小/上传人/时间/业务模块/关联单据） */
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
  /** 预览（GET /api/files/{id}/preview，handler.go:121——仅图片原样流式返回，不做缩略图） */
  preview: (id: FileId) => `/api/files/${id}/preview`,
  /** 删除（软删 + 审计；危险操作，前端经 SfConfirm 二次确认，api.md §6） */
  remove: (id: FileId) => http.delete<unknown>(`/api/files/${id}`),
}
