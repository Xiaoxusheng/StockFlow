import { Dropdown, Empty, Tooltip } from 'antd'
import { HistoryOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { useRecentVisitsList } from '@/hooks/usePreferences'

// ---------- 顶部『最近访问』下拉（计划 §2.5 / frontend.md §23 组件清单） ----------
// 读 user_preferences.recent_visits 偏好（useRecentVisits 写入器维护），点击回详情；
// 空态展示引导文案。挂 PcLayout Header 右区（通知按钮旁）。样式走 --sf-* Token。

const TIME_RECENT_MS = 60_000

/** visited_at（YYYY-MM-DD HH:mm:ss）→ 本地相对描述（超 1 分钟显示原时间） */
function formatVisitedAt(visitedAt: string): string {
  const ts = new Date(visitedAt.replace(' ', 'T')).getTime()
  if (Number.isNaN(ts)) return visitedAt
  const delta = Date.now() - ts
  if (delta >= 0 && delta < TIME_RECENT_MS) return '刚刚'
  return visitedAt
}

export function RecentVisitsDropdown() {
  const navigate = useNavigate()
  const visits = useRecentVisitsList()

  const listItems =
    visits.length === 0
      ? [
          {
            key: 'empty',
            disabled: true,
            label: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无最近访问"
                style={{ margin: 0, padding: 'var(--sf-space-2) 0' }}
              />
            ),
          },
        ]
      : visits.map((v, i) => ({
          key: `visit-${i}`,
          label: (
            <span className="sf-recent-visits__item">
              <span className="sf-recent-visits__title">{v.title}</span>
              <span className="sf-recent-visits__time">{formatVisitedAt(v.visited_at)}</span>
            </span>
          ),
        }))

  const menu = {
    items: listItems,
    onClick: ({ key }: { key: string }) => {
      const index = Number(key.split('-').pop())
      const visit = visits[index]
      if (!visit) return
      // 同路径重复点击不重复入栈（navigate 相同 path 无害，直接跳）
      navigate(visit.path)
    },
  }

  return (
    <Tooltip title="最近访问">
      <Dropdown menu={menu} trigger={['click']} placement="bottomRight">
        <button
          type="button"
          className="sf-header-icon-btn"
          aria-label="最近访问"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: 30,
            height: 30,
            border: 'none',
            borderRadius: 'var(--sf-radius-md)',
            background: 'transparent',
            color: 'var(--sf-text-secondary)',
            cursor: 'pointer',
          }}
        >
          <HistoryOutlined />
        </button>
      </Dropdown>
    </Tooltip>
  )
}
