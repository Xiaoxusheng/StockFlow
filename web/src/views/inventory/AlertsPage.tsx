import { Button, Card, Typography } from 'antd'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import { DateCell, SfRowActions } from '@/components/table/cells'
import { inventoryApi, type StockAlertItem, type StockAlertLevel, type StockAlertQuery } from '@/api/inventory'
import { PURCHASE_CREATE_PERMISSION } from '@/api/purchase'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

const LEVEL_OPTIONS: Array<{ label: string; value: StockAlertLevel }> = [
  { label: '低库存', value: 'low_stock' },
  { label: '超储', value: 'overstock' },
  { label: '临期', value: 'near_expiry' },
  { label: '过期', value: 'expired' },
  { label: '积压', value: 'slow_moving' },
]

/**
 * 阈值单位随预警级别而异（internal/reports/repository_alerts.go 分支定义）：
 * low_stock=安全库存 / overstock=库存上限（数量），near_expiry/expired=剩余效期天数
 * （0=已过期），slow_moving=最小积压天数档位——天数类补「天」后缀，数量类原样呈现。
 */
function isDayThreshold(level: StockAlertLevel): boolean {
  return level === 'near_expiry' || level === 'expired' || level === 'slow_moving'
}

const COLUMNS: ColumnsType<StockAlertItem> = [
  {
    title: '预警类型',
    dataIndex: 'level',
    width: 100,
    fixed: 'left',
    render: (v: StockAlertLevel) => <SfStatusTag status={v} />,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  {
    title: '仓库',
    dataIndex: 'warehouse_name',
    width: 120,
    render: (v: string, record: StockAlertItem) =>
      record.warehouse_code ? `${v}（${record.warehouse_code}）` : v,
  },
  {
    title: '当前库存',
    dataIndex: 'current_qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '阈值',
    dataIndex: 'threshold',
    width: 100,
    align: 'right',
    render: (v: number, record: StockAlertItem) => (
      <span className="sf-num">
        {formatNumber(v)}
        {isDayThreshold(record.level) ? ' 天' : ''}
      </span>
    ),
  },
  {
    // 批次号仅效期类预警携带（AlertItem.batch_no omitempty，repository.go:417），其余「-」
    title: '批次',
    dataIndex: 'batch_no',
    width: 130,
    render: (v?: string) => v || '-',
  },
  {
    // 末次移动时间仅积压预警携带（last_moved_at omitempty，repository.go:419）
    title: '末次移动',
    dataIndex: 'last_moved_at',
    width: 160,
    render: (v?: string | null) =>
      v ? <DateCell value={v} /> : '-',
  },
  {
    // 提示列必须显式 width：本表唯一弹性列曾无 width，被 scroll.x 压缩后内部 Text 的
    // maxWidth:320 与实际列宽（200）脱节，文本连同省略号溢出 td、冒到 fixed 右列
    // （操作列）右侧——用户可见「更多」样式的溢出点（2026-10-07 修复）。
    // maxWidth:'100%' 随列宽自适应，不再硬编码像素（column.ellipsis 的 td title 兜底提示）。
    title: '提示',
    dataIndex: 'message',
    width: 280,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
]

/**
 * 预警行无独立 id（AlertItem 无主键字段），用级别+仓库+SKU+批次组合键；
 * 同一仓库+SKU 可同时命中多级别（不同 level 键不同），组合键在结果集内唯一。
 */
function alertRowKey(record: StockAlertItem): string {
  return [record.level, record.warehouse_id, record.sku_id, record.batch_no ?? ''].join('-')
}

/**
 * 库存预警（低库存 / 超储 / 临期 / 过期 / 积压，frontend.md §10.1）：
 * GET /api/inventory/alerts 为 reports 实现、inventory 前缀挂载
 * （internal/reports/routes.go:48；权限点 reports:report:read），响应为 snake_case
 * AlertItem（repository.go:407-421）；筛选参数 level/keyword（handler.go:191-204）。
 * 联动（2026-10-07 批次一 L3）：行「查看库存」（/inventory/stock?sku_id=&warehouse_id=，
 * 筛选参数与 inventory:stock:view 权限码同 relations.tsx stock 实体项 :281）+
 * 「去补货」（仅低库存行；仅带 sku_id/warehouse_id，数量留空人工填——禁止前端算库存）。
 */
export default function AlertsPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canViewStock = canAccess(user, 'inventory:stock:view')
  const canCreatePurchase = canAccess(user, PURCHASE_CREATE_PERMISSION)
  const list = usePagedList<StockAlertItem, StockAlertQuery>({
    queryKey: ['inventory', 'alerts'],
    fetch: (q) => inventoryApi.alerts(q),
    urlSync: true,
  })

  const columns: ColumnsType<StockAlertItem> = [
    ...COLUMNS,
    ...(canViewStock || canCreatePurchase
      ? ([
          {
            title: '操作',
            key: 'actions',
            fixed: 'right',
            // 170 = 查看库存+去补货（134px inline-flex）+ 左右 padding 16 + 余量。
            // 150 时 div 恰好顶满内容盒，亚像素溢出触发 td 的 text-overflow 在按钮右侧
            // 画出幽灵「…」并被表缘裁切（用户误读为「更多」溢出，2026-10-07 修复）。
            width: 170,
            render: (_, record) => {
              const stockQuery = record.warehouse_id
                ? `sku_id=${record.sku_id}&warehouse_id=${record.warehouse_id}`
                : `sku_id=${record.sku_id}`
              return (
                <SfRowActions>
                  {canViewStock && (
                    <Button
                      key="stock"
                      type="link"
                      size="small"
                      onClick={() => navigate(`/inventory/stock?${stockQuery}`)}
                    >
                      查看库存
                    </Button>
                  )}
                  {canCreatePurchase && record.level === 'low_stock' && (
                    <Button
                      key="replenish"
                      type="link"
                      size="small"
                      onClick={() =>
                        navigate(
                          record.warehouse_id
                            ? `/purchases/new?warehouse_id=${record.warehouse_id}&items=${record.sku_id}`
                            : `/purchases/new?items=${record.sku_id}`,
                        )
                      }
                    >
                      去补货
                    </Button>
                  )}
                </SfRowActions>
              )
            },
          },
        ] as ColumnsType<StockAlertItem>)
      : []),
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存预警"
        subtitle="低库存 / 超储 / 临期 / 过期 / 积压"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU 编码 / 商品名称' },
            { name: 'level', label: '预警类型', control: 'select', options: LEVEL_OPTIONS },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<StockAlertItem>
          storageKey="inventory-alerts"
          rowKey={alertRowKey}
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前没有库存预警"
        />
      </Card>
    </div>
  )
}
