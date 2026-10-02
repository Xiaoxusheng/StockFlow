import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

export interface NotificationQuery extends PageQuery {
  read?: boolean
}

export interface NotificationItem {
  id: number | string
  type: string
  title: string
  content: string
  read: boolean
  createdAt: string
}

export const notificationApi = {
  unreadCount: () => http.get<number>('/api/notifications/unread-count'),
  list: (query: NotificationQuery) =>
    http.get<PageResult<NotificationItem>>('/api/notifications', { params: query }),
  markRead: (id: number | string) => http.post<void>(`/api/notifications/${id}/read`),
  markAllRead: () => http.post<void>('/api/notifications/read-all'),
}
