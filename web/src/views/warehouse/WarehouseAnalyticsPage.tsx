import { useMemo, useState } from 'react'
import { Button, Col, Flex, Progress, Row, Segmented, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { analyticsApi, type WarehouseWorkloadRow } from '@/api/analytics'
import { dashboardApi, type DashboardWarehouseStock } from '@/api/dashboard'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfBarChart, SfChartCard, SfHBarChart } from '@/components/charts'
import { formatNumber, formatQty } from '@/utils/format'

const { Text } = Typography

/** 图表统一高度（紧凑卡片适配，任务书 §45；与 DashboardCharts.tsx DASHBOARD_CHART_HEIGHT 同值口径） */
const ANALYTICS_CHART_HEIGHT = 280

/** 作业量时间档（后端 requireRange：time_from/time_to YYYY-MM-DD、上限 366 天——InboundAnalyticsPage 同款范式） */
const RANGE_OPTIONS = [
  { label: '近7天', value: 7 },
  { label: '近30天', value: 30 },
  { label: '近90天', value: 90 },
]

/** 作业量双序列（流水量口径：Σ|qty_change|，INBOUND/OUTBOUND 流水——workbench.go:106-114） */
const WORKLOAD_SERIES = [
  { key: 'inbound_qty', name: '入库量' },
  { key: 'outbound_qty', name: '出库量' },
]

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
 * 作业量图数据映射（GET /api/warehouses/workload，workbench.go:106-114）：后端已按两
 * qty 之和降序（可见仓全集 LEFT JOIN，零作业仓返回零行）——前端保持原序不重排、不拼算
 * 总量（AGENTS.md 规则 8）。类目取 warehouse_name（与容量卡同键）；单量口径不走图，
 * 由卡内明细行直出。
 */
function buildWorkloadData(rows: WarehouseWorkloadRow[]) {
  return rows.map((w) => ({
    warehouse: w.warehouse_name,
    inbound_qty: w.inbound_qty,
    outbound_qty: w.outbound_qty,
  }))
}

/**
 * 仓库分析（任务书 §44 仓库分析：仓库库存容量 / 仓库利用率 / 仓库作业量；蓝图 key=warehouse，
 * 与 inbound/outbound 分析页同构）。
 *
 * 数据源（api/analytics.ts 统一收口文件 + 共享 api，消费点禁直连 http——AGENTS.md 规则 6）：
 * - GET /api/reports/dashboard/warehouse-stock（internal/reports/routes.go:60，
 *   权限 auth.PermInventoryList = inventory:inventory:list，permissions.go:120）——复用
 *   api/dashboard.ts dashboardApi.warehouseStock（:110-111）；
 * - GET /api/warehouses/workload（internal/reports/routes.go:100，同码
 *   inventory:inventory:list——2026-10-05 分析卡片轮交付）——api/analytics.ts
 *   warehouseWorkload 消费（收口统一接线文件，本域不新建 api 文件）。
 * 三图映射：
 * 1. 仓库库存容量  横向排行 SfHBarChart（现存总量降序，§35）——数量与 SKU 数量级悬殊，
 *    双序列混轴会把 SKU 压成不可见细条，故 SKU 数移至右侧利用率行列表呈现，两图分工不重复；
 * 2. 库位利用率    Progress 行列表（§37 利用率呈现 Progress 分支）——每行一仓：名称 +
 *    占用率进度条 + SKU 数，任意仓数下纵读可扩展，取代视觉重、信息密度低的针式仪表盘；
 * 3. 仓库作业量    分组柱状图（流水量双序列 §34）+ 单量明细行 · GET /api/warehouses/
 *    workload：inbound/outbound_qty=Σ|qty_change|（INBOUND/OUTBOUND 流水）、
 *    *_order_count=COUNT(DISTINCT business_no)，双口径同端点返回（图呈现流水量、
 *    明细行呈现单量）；可见仓全集 LEFT JOIN 零作业仓返回零行、按两 qty 之和降序，
 *    前端保持原序不重排不拼算（AGENTS.md 规则 8 / 任务书 §54）——销旧 EMPTY 挂账项。
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

  // 作业量统计窗口（后端 requireRange：time_from/time_to YYYY-MM-DD，上限 366 天——ReportRangeQuery）
  const [days, setDays] = useState(30)
  const workloadRange = useMemo(
    () => ({
      time_from: dayjs().subtract(days - 1, 'day').format('YYYY-MM-DD'),
      time_to: dayjs().format('YYYY-MM-DD'),
    }),
    [days],
  )
  // GET /api/warehouses/workload（inventory:inventory:list）：免分页直出，可见仓全集
  const workload = useQuery({
    queryKey: ['warehouse', 'analytics', 'workload', workloadRange],
    queryFn: () => analyticsApi.warehouseWorkload(workloadRange),
  })
  const workloadRows = workload.data ?? []

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

        {/* L2 仓库作业量：GET /api/warehouses/workload（routes.go:100，2026-10-05 分析卡片轮
            交付，销旧 EMPTY 挂账）——流水量双序列分组柱（§34 多序列 group）+ 单量明细行；
            Loading/Empty/Error 三态由 SfBarChart 内聚处理（§51-53），明细行仅在成功且非空时出 */}
        <Row gutter={[16, 16]}>
          <Col span={24}>
            <SfChartCard
              title="仓库作业量"
              subtitle={`按仓出入库流水量（Σ|qty_change| · 近 ${days} 天）；单量（去重单号数）见各仓明细`}
              extra={
                <Flex gap={8} align="center">
                  <Segmented
                    size="small"
                    options={RANGE_OPTIONS}
                    value={days}
                    onChange={(v) => setDays(v as number)}
                  />
                  <RefreshButton onClick={() => void workload.refetch()} />
                </Flex>
              }
            >
              <Flex vertical gap={12}>
                <SfBarChart
                  data={buildWorkloadData(workloadRows)}
                  xField="warehouse"
                  yField="inbound_qty"
                  series={WORKLOAD_SERIES}
                  height={ANALYTICS_CHART_HEIGHT}
                  loading={workload.isPending}
                  error={workload.error}
                  onRetry={() => void workload.refetch()}
                  emptyText="所选时间范围内暂无仓库作业数据"
                />
                {!workload.isPending && !workload.error && workloadRows.length > 0 ? (
                  <Flex vertical>
                    {workloadRows.map((w) => (
                      <Flex
                        key={w.warehouse_code}
                        justify="space-between"
                        gap={12}
                        style={{
                          padding: '6px 4px',
                          borderBottom: '1px solid var(--sf-border-subtle)',
                        }}
                      >
                        <Text strong>{w.warehouse_name}</Text>
                        <Flex gap={16}>
                          <Text type="secondary" className="sf-num">
                            入库 {formatNumber(w.inbound_order_count)} 单 / {formatQty(w.inbound_qty)}
                          </Text>
                          <Text type="secondary" className="sf-num">
                            出库 {formatNumber(w.outbound_order_count)} 单 / {formatQty(w.outbound_qty)}
                          </Text>
                        </Flex>
                      </Flex>
                    ))}
                  </Flex>
                ) : null}
              </Flex>
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
