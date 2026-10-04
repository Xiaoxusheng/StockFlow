import { Button, Card, Col, Flex, List, Row, Tag, Typography } from 'antd'
import {
  ArrowLeftOutlined,
  BarChartOutlined,
  DatabaseOutlined,
  FileSyncOutlined,
  PrinterOutlined,
  RightOutlined,
} from '@ant-design/icons'
import type { ReactNode } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { reportsApi } from '@/api/reports'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import ReportInventorySummary from './ReportInventorySummary'
import ReportFlowStats from './ReportFlowStats'
import ReportInventoryTurnover from './ReportInventoryTurnover'
import ReportStagnantStock from './ReportStagnantStock'
import ReportReplenishment from './ReportReplenishment'

const { Text } = Typography

/** 已交付真实入口：只链到可用的真实页面，不放写死数据（requirements.md §10） */
interface ReportEntry {
  key: string
  title: string
  description: string
  icon: ReactNode
  path: string
}

const REAL_ENTRIES: ReportEntry[] = [
  {
    key: 'analytics',
    title: '库存分析',
    description: '库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势',
    icon: <BarChartOutlined />,
    path: '/inventory/analytics',
  },
  {
    key: 'ledger',
    title: '库存流水',
    description: '库存变化完整轨迹，与库存保持一致',
    icon: <DatabaseOutlined />,
    path: '/inventory/ledger',
  },
  {
    key: 'exports',
    title: 'Excel 导出',
    description: '导出任务列表与认证文件下载',
    icon: <FileSyncOutlined />,
    path: '/data/exports',
  },
  {
    key: 'printing',
    title: '打印中心',
    description: '打印模板 / 打印任务 / 打印历史 / 打印预览',
    icon: <PrinterOutlined />,
    path: '/data/printing',
  },
]

/** 报表 key → 视图组件（与后端冻结目录一一对应，internal/reports/catalog.go:18-55；
 * 出入库统计共用一个视图组件，两目录项各自挂真实端点） */
function renderReportView(key: string): ReactNode {
  switch (key) {
    case 'inventory-summary':
      return <ReportInventorySummary />
    case 'inbound-stats':
      return <ReportFlowStats kind="inbound" />
    case 'outbound-stats':
      return <ReportFlowStats kind="outbound" />
    case 'inventory-turnover':
      return <ReportInventoryTurnover />
    case 'stagnant-stock':
      return <ReportStagnantStock />
    case 'replenishment-suggestions':
      return <ReportReplenishment />
    default:
      return null
  }
}

/**
 * 报表中心（/reports，menu.tsx 既有菜单）：目录来自 GET /api/reports（后端 M3 已交付，
 * internal/reports/routes.go:38 代码内冻结注册表）；点击目录项经 ?report=<key> 进入
 * 报表视图真实查询（页内子视图，不新增路由）；数据权限以后端会话仓库范围快照为准。
 * 目录项点击经 canAccess(user, 'report:read') 做体验级过滤（前端权限仅是体验优化，
 * 后端仍按 reports:report:list/read 校验——permission.md §5）。
 */
export default function ReportsPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const [searchParams, setSearchParams] = useSearchParams()
  const activeKey = searchParams.get('report')
  const catalog = useQuery({ queryKey: ['reports', 'catalog'], queryFn: reportsApi.catalog })

  const catalogItems = catalog.data ?? []
  const activeReport = activeKey ? catalogItems.find((item) => item.key === activeKey) : undefined
  const canRead = canAccess(user, 'report:read')

  const openReport = (key: string) => setSearchParams({ report: key })
  const backToCatalog = () => setSearchParams({})

  const backHeader = (title: string, subtitle?: string) => (
    <SfPageHeader
      title={title}
      subtitle={subtitle}
      extra={
        <Button icon={<ArrowLeftOutlined />} onClick={backToCatalog}>
          返回报表中心
        </Button>
      }
    />
  )

  // 报表视图模式：/reports?report=<key>
  if (activeKey) {
    if (activeReport) {
      return (
        <div className="sf-page">
          {backHeader(activeReport.name, activeReport.description)}
          {renderReportView(activeReport.key)}
        </div>
      )
    }
    if (catalog.isPending) {
      return (
        <div className="sf-page">
          {backHeader('报表中心')}
          <Card size="small">
            <SfLoading rows={3} />
          </Card>
        </div>
      )
    }
    if (catalog.error) {
      return (
        <div className="sf-page">
          {backHeader('报表中心')}
          <Card size="small">
            <SfError error={catalog.error} onRetry={catalog.refetch} />
          </Card>
        </div>
      )
    }
    return (
      <div className="sf-page">
        {backHeader('报表中心')}
        <Card size="small">
          <SfEmpty description="请求的报表不存在或已下线" />
        </Card>
      </div>
    )
  }

  // 目录模式：/reports
  return (
    <div className="sf-page">
      <SfPageHeader title="报表中心" subtitle="报表目录、导出与打印（requirements.md §2.4）" />

      {/* 已交付真实入口：纯导航，指向真实页面 */}
      <Card size="small" title="库存分析与数据工具" style={{ marginBottom: 16 }}>
        <Row gutter={[16, 16]}>
          {REAL_ENTRIES.map((entry) => (
            <Col key={entry.key} xs={24} md={12} lg={6}>
              <Card
                size="small"
                hoverable
                style={{ height: '100%' }}
                onClick={() => navigate(entry.path)}
              >
                <Flex align="center" gap={12}>
                  <span style={{ fontSize: 20, color: 'var(--sf-primary)' }}>{entry.icon}</span>
                  <Flex vertical style={{ minWidth: 0 }}>
                    <Text strong>{entry.title}</Text>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {entry.description}
                    </Text>
                  </Flex>
                </Flex>
              </Card>
            </Col>
          ))}
        </Row>
      </Card>

      {/* 报表域目录：GET /api/reports 冻结注册表（后端 M3 已交付，routes.go:38） */}
      <Card size="small" title="报表目录">
        {catalog.isPending ? (
          <SfLoading rows={3} />
        ) : catalog.error ? (
          <SfError
            error={catalog.error}
            onRetry={catalog.refetch}
            description="报表目录加载失败：请检查网络连接后重试；若持续失败请联系管理员确认已授予报表查看权限"
          />
        ) : catalogItems.length === 0 ? (
          <SfEmpty description="暂无可用报表" />
        ) : (
          <List
            size="small"
            dataSource={catalogItems}
            rowKey={(item) => item.key}
            renderItem={(item) => (
              <List.Item
                style={{ cursor: canRead ? 'pointer' : 'not-allowed' }}
                onClick={() => canRead && openReport(item.key)}
                actions={
                  canRead
                    ? [<RightOutlined key="go" style={{ color: 'var(--sf-text-secondary)' }} />]
                    : [<Tag key="no-perm">无查看权限</Tag>]
                }
              >
                <Flex align="center" gap={12} style={{ minWidth: 0 }}>
                  <Tag style={{ marginInlineEnd: 0 }}>{item.category}</Tag>
                  <Text strong>{item.name}</Text>
                  {item.description && (
                    <Text type="secondary" style={{ fontSize: 12 }} ellipsis>
                      {item.description}
                    </Text>
                  )}
                </Flex>
              </List.Item>
            )}
          />
        )}
      </Card>
    </div>
  )
}
