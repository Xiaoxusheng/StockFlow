import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 通知个人收件箱（后端 M3 已交付：internal/sysops/routes.go 个人收件箱路由组；
// 认证即可用、无权限点——个人数据自见原则，plan §10.6） ----------
//
// 出参为 inboxItem snake_case（internal/sysops/notifications.go）。

/** 通知类型（db/migrations/000014 chk_notifications_type：SYSTEM/APPROVAL/STOCK_ALERT/
 * EXPIRY_ALERT/EXCEPTION/TASK 大写值域） */
export type NotificationType =
  | 'SYSTEM'
  | 'APPROVAL'
  | 'STOCK_ALERT'
  | 'EXPIRY_ALERT'
  | 'EXCEPTION'
  | 'TASK'

export interface NotificationQuery extends PageQuery {
  /** 已读筛选（true=仅已读 / false=仅未读，后端 listNotifications 解析 query 参数） */
  read?: boolean
}

export interface NotificationItem {
  id: number | string
  type: NotificationType
  title: string
  content: string
  read: boolean
  created_at: string
}

export const notificationApi = {
  unreadCount: () => http.get<number>('/api/notifications/unread-count'),
  list: (query: NotificationQuery) =>
    http.get<PageResult<NotificationItem>>('/api/notifications', { params: query }),
  markRead: (id: number | string) => http.post<{ marked: number }>(`/api/notifications/${id}/read`),
  markAllRead: () => http.post<{ marked: number }>('/api/notifications/read-all'),
}
