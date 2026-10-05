import { Button, Col, Flex, Progress, Row, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { dashboardApi, type DashboardWarehouseStock } from '@/api/dashboard'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfChartCard, SfHBarChart } from '@/components/charts'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/** 图表统一高度（紧凑卡片适配，任务书 §45；与 DashboardCharts.tsx DASHBOARD_CHART_HEIGHT 同值口径） */
const ANALYTICS_CHART_HEIGHT = 280

/** 卡片右上刷新（§45 图表卡 extra：时间档 / 刷新；DashboardPage 同款范式） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Tooltip title="刷新">
      <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} />
    </Tooltip>
  )
}

/**
 * 库位利用率行列表（与 DashboardCharts.BinUtilizationList 同构形态：名称 + Progress + SKU 数）。
 * 取代旧「≤3 仓逐仓 Gauge」方案——仪表盘针式表盘信息密度低、视觉重；Progress 行在任意仓数下
 * 一行一仓纵读，占用率与 SKU 数同排直达（§37 利用率呈现 Progress/Gauge 中的 Progress 分支）。
 * 数据同源 warehouse-stock.bin_utilization（0-100，dashboard.go 直出），无前端换算。
 */
function UtilizationList({ items }: { items: DashboardWarehouseStock[] }) {
  return (
    <Flex vertical gap={16} justify="center" style={{ minHeight: ANALYTICS_CHART_HEIGHT - 32 }}>
      {items.map((w) => (
        <Flex key={w.warehouse_code} align="center" gap={12}>
          <Text style={{ width: 110 }} ellipsis>
            {w.warehouse_name}
          </Text>
          <Progress
            percent={w.bin_utilization}
            size="small"
            style={{ flex: 1, marginBottom: 0 }}
            format={(p) => `${p ?? 0}%`}
          />
          <Text type="secondary" className="sf-num" style={{ fontSize: 12, width: 64, textAlign: 'right' }}>
            {formatNumber(w.sku_count)} SKU
          </Text>
        </Flex>
      ))}
    </Flex>
  )
}

/**
 * 仓库分析（任务书 §44 仓库分析：仓库库存容量 / 仓库利用率 / 仓库作业量；蓝图 key=warehouse，
 * 与 inbound/outbound 分析页同构）。
 *
 * 数据源（唯一）：GET /api/reports/dashboard/warehouse-stock（internal/reports/routes.go:60，
 * 权限 auth.PermInventoryList = inventory:inventory:list，permissions.go:120）——复用
 * api/dashboard.ts dashboardApi.warehouseStock（:110-111），本域不新建 api 文件（蓝图 scopeFiles 注）。
 * 三图映射：
 * 1. 仓库库存容量  横向排行 SfHBarChart（现存总量降序，§35）——数量与 SKU 数量级悬殊，
 *    双序列混轴会把 SKU 压成不可见细条，故 SKU 数移至右侧利用率行列表呈现，两图分工不重复；
 * 2. 库位利用率    Progress 行列表（§37 利用率呈现 Progress 分支）——每行一仓：名称 +
 *    占用率进度条 + SKU 数，任意仓数下纵读可扩展，取代视觉重、信息密度低的针式仪表盘；
 * 3. 仓库作业量    EMPTY：报表域无按仓作业量聚合端点（/reports/inbound-stats|outbound-stats
 *    仅按 stat_date 日聚合，FlowStatRow 无仓库维度——api/reports.ts 实测；routes.go:45-67
 *    全量清单核实），呈真实空态，禁止前端拼算伪造（AGENTS.md 规则 8 / 任务书 §54）。
 *
 * 三态由 SfChart 内核 / SfChartCard 统一处理（§51-53：单卡失败不拖垮整页）；只消费
 * components/charts 业务组件，主题色来自图表色板，页面零色值字面量。路由 /warehouses/analytics
 * 与菜单挂载（仓库中心组，permission inventory:stock:view 经 RESOURCE_ALIASES stock→inventory
 * 命中端点同款 PermInventoryList，与实时库存/库存预警同码同源，menu.tsx:59,72 先例）走 sharedChanges。
 */
export default function WarehouseAnalyticsPage() {
  const warehouseStock = useQuery({
    queryKey: ['warehouse', 'analytics', 'warehouse-stock'],
    queryFn: () => dashboardApi.warehouseStock(),
  })
  const items = warehouseStock.data ?? []

  return (
    <div className="sf-page">
      <SfPageHeader title="仓库分析" subtitle="仓库库存容量 / 库位利用率 / 仓库作业量" />

      <Flex vertical gap={16}>
        {/* L1 仓库库存容量（横向排行）+ 库位利用率（Progress 行列表，任意仓数） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={14}>
            <SfChartCard
              title="仓库库存容量"
              subtitle="各仓库现存总量排行（SKU 数见右侧利用率列表）"
              extra={<RefreshButton onClick={() => void warehouseStock.refetch()} />}
            >
              <SfHBarChart
                data={items.map((w) => ({
                  name: w.warehouse_name,
                  total_qty: w.total_qty,
                }))}
                categoryField="name"
                valueField="total_qty"
                height={ANALYTICS_CHART_HEIGHT}
                loading={warehouseStock.isPending}
                error={warehouseStock.error}
                onRetry={() => void warehouseStock.refetch()}
                emptyText="当前没有仓库库存数据"
              />
            </SfChartCard>
          </Col>
          <Col xs={24} lg={10}>
            <SfChartCard
              title="库位利用率"
              subtitle="各仓库库位占用（0–100%）"
              loading={warehouseStock.isPending}
              loadingHeight={ANALYTICS_CHART_HEIGHT}
              extra={<RefreshButton onClick={() => void warehouseStock.refetch()} />}
            >
              {warehouseStock.error ? (
                <SfError error={warehouseStock.error} onRetry={() => void warehouseStock.refetch()} />
              ) : items.length === 0 ? (
                <SfEmpty description="当前没有仓库库位利用率数据" />
              ) : (
                <UtilizationList items={items} />
              )}
            </SfChartCard>
          </Col>
        </Row>

        {/* L2 仓库作业量（蓝图标注 EMPTY：无按仓聚合端点，真实空态，不接图不伪造） */}
        <Row gutter={[16, 16]}>
          <Col span={24}>
            <SfChartCard title="仓库作业量" subtitle="按仓出入库作业量（单量 / 行量）">
              <SfEmpty description="按仓作业量聚合端点尚未提供（出入库统计仅按日聚合），端点交付后展示真实作业量" />
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
