import { Button, Card, Flex, Result } from 'antd'
import { useNavigate } from 'react-router'
import { findMenuItem } from '@/config/menu'
import { useLocation } from 'react-router'
import { SfPageHeader } from '@/components/common/SfPageHeader'

/**
 * 未接入模块统一占位页（frontend.md §1.1：后端未就绪时允许页面骨架）。
 * 诚实呈现：说明模块已纳入路由与权限体系，等待对应后端 API 交付。
 */
export function ModulePlaceholder() {
  const location = useLocation()
  const navigate = useNavigate()
  const menuItem = findMenuItem(location.pathname)

  return (
    <div className="sf-page">
      <SfPageHeader title={menuItem?.label ?? '页面'} />
      <Card styles={{ body: { padding: 0 } }}>
        <Result
          icon={<ModuleIcon />}
          title="模块建设中"
          subTitle={`「${menuItem?.label ?? '该模块'}」已完成路由与权限接入，将随对应后端 API 一起交付，当前无可用数据。`}
          extra={
            <Button type="primary" onClick={() => navigate('/dashboard')}>
              返回 Dashboard
            </Button>
          }
        />
      </Card>
    </div>
  )
}

function ModuleIcon() {
  return (
    <Flex
      align="center"
      justify="center"
      style={{
        width: 72,
        height: 72,
        borderRadius: 'var(--sf-radius-lg)',
        background: 'var(--sf-border-subtle)',
        margin: '0 auto',
      }}
    >
      <svg width="40" height="40" viewBox="0 0 32 32" aria-hidden>
        <rect width="32" height="32" rx="7" fill="var(--sf-border)" />
        <path
          d="M9 11h10a3 3 0 0 1 0 6H11a3 3 0 0 0 0 6h12"
          stroke="var(--sf-text-muted)"
          strokeWidth="2.6"
          fill="none"
          strokeLinecap="round"
        />
      </svg>
    </Flex>
  )
}
