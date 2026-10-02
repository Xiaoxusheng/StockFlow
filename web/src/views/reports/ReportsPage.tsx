import { Card, Col, Flex, List, Row, Tag, Typography } from 'antd'
import { BarChartOutlined, DatabaseOutlined, FileSyncOutlined } from '@ant-design/icons'
import type { ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { reportsApi } from '@/api/reports'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfPageHeader } from '@/components/common/SfPageHeader'

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
]

/** 报表中心（/reports，menu.tsx 既有菜单；GET /api/reports 前端先行契约，后端报表域阶段 17–18 交付） */
export default function ReportsPage() {
  const navigate = useNavigate()
  const catalog = useQuery({ queryKey: ['reports', 'catalog'], queryFn: reportsApi.catalog })

  return (
    <div className="sf-page">
      <SfPageHeader title="报表中心" subtitle="报表目录、导出与打印（requirements.md §2.4）" />

      {/* 已交付真实入口：纯导航，指向真实页面 */}
      <Card size="small" title="库存分析与数据工具" style={{ marginBottom: 16 }}>
        <Row gutter={[16, 16]}>
          {REAL_ENTRIES.map((entry) => (
            <Col key={entry.key} xs={24} md={8}>
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

      {/* 报表域目录：前端先行契约，后端未交付呈统一错误态/空态 */}
      <Card size="small" title="报表目录">
        {catalog.isPending ? (
          <SfLoading rows={3} />
        ) : catalog.error ? (
          <SfError
            error={catalog.error}
            onRetry={catalog.refetch}
            description="报表域接口（GET /api/reports）未交付或不可用；后端报表域交付后本页自动展示报表目录"
          />
        ) : (catalog.data ?? []).length === 0 ? (
          <SfEmpty description="后端尚未提供任何报表目录" />
        ) : (
          <List
            size="small"
            dataSource={catalog.data}
            rowKey={(item) => item.key}
            renderItem={(item) => (
              <List.Item>
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
