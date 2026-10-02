import { http } from './client'

// ---------- 报表域（前端先行契约：backend-m1-plan.md §11——报表、智能能力属阶段 17–18，
// 依赖业务流水积累，后端 M1/M2 不交付，页面呈统一错误态/空态。
// 端点路径与字段为前端提案（同 api/sales.ts 先例），待后端冻结后回对 JSON tag） ----------

/** 报表目录项（requirements.md §2.4：库存/单据/分析/效率四类报表，均支持筛选/导出/打印） */
export interface ReportCatalogItem {
  /** 报表标识（如 inventory-summary，后端冻结前为前端提案编码） */
  key: string
  /** 报表名称 */
  name: string
  /** 报表分类（库存 / 单据 / 分析 / 效率） */
  category: string
  /** 报表说明（可选） */
  description?: string
}

export const reportsApi = {
  /** 报表目录：GET /api/reports */
  catalog: () => http.get<ReportCatalogItem[]>('/api/reports'),
}
