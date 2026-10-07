/**
 * 最近库存异动表（任务书 §27 第四层）
 *
 * 数据来自 GET /api/inventory-ledgers（append-only 库存流水，后端 ORDER BY created_at DESC,
 * id DESC——internal/inventory/repository.go:814，"最新在前"由后端保证，前端不排序拼装）。
 * 统一走 SfTable（AGENTS.md 规则 4），紧凑预览：默认第 1 页 10 条，完整筛选在库存流水页。
 * 方向色沿用库存流水页口径（qty_change 正=入/负=出，色值走 --sf-* Token）。
 */
import { Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined, MinusOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { inventoryApi, type InventoryChangeType, type LedgerItem, type LedgerQuery } from '@/api/inventory'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfTable } from '@/components/table/SfTable'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/** 变更类型文案（inventory_ledgers CHECK 值域，与 LedgerPage/StockDetailPage 同源） */
const CHANGE_TYPE_LABEL: Record<InventoryChangeType, string> = {
  INBOUND: '入库',
  OUTBOUND: '出库',
  TRANSFER_OUT: '调拨出库',
  TRANSFER_IN: '调拨入库',
  LOCK: '锁定',
  RELEASE: '释放',
  MOVE: '移库',
  INSPECT_PASS: '质检合格',
  INSPECT_DEFECTIVE: '质检不合格',
  ADJUST: '调整',
}

/** 方向由变更数量正负驱动（LedgerView 无独立 direction 字段） */
function DirectionText({ qtyChange }: { qtyChange: number }) {
  if (qtyChange > 0) {
    return (
      <Text style={{ color: 'var(--sf-success)' }}>
        <ArrowDownOutlined /> 入
      </Text>
    )
  }
  if (qtyChange < 0) {
    return (
      <Text style={{ color: 'var(--sf-danger)' }}>
        <ArrowUpOutlined /> 出
      </Text>
    )
  }
  return (
    <Text type="secondary">
      <MinusOutlined /> 无
    </Text>
  )
}

const COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'created_at',
    width: 150,
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '单据编号',
    dataIndex: 'business_no',
    width: 150,
    render: (v: string) => v || '-',
  },
  {
    title: '变更类型',
    dataIndex: 'change_type',
    width: 100,
    render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
  },
  {
    title: '方向',
    key: 'direction',
    width: 70,
    align: 'center',
    render: (_: unknown, record: LedgerItem) => <DirectionText qtyChange={record.qty_change} />,
  },
  {
    title: '数量',
    dataIndex: 'qty_change',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动后',
    dataIndex: 'qty_after',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '操作人',
    dataIndex: 'operator_name',
    width: 100,
    render: (v: string) => v || '-',
  },
]

const NO_FILTER: LedgerQuery = {}

/** 紧凑预览：固定 pageSize 10（SfTable 分页翻页真实请求后端）。
 * 联动批次二 L10：行点击（business_no 非空）→ /inventory/ledger?business_no= 预筛
 * （frontend.md §33.4；inventory:stock:view 门控，无权限不可点）。 */
export function DashboardMovements() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const clickable = canAccess(user, 'inventory:stock:view')
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['dashboard', 'movements'],
    fetch: (q) => inventoryApi.ledger(q),
    params: NO_FILTER,
    defaultPageSize: 10,
  })

  return (
    <SfTable<LedgerItem>
      rowKey="id"
      columns={COLUMNS}
      dataSource={list.items}
      loading={list.isFetching}
      error={list.error}
      onRetry={list.refetch}
      pagination={list.pagination}
      total={list.total}
      onPageChange={list.onPageChange}
      showToolbar={false}
      emptyText="当前没有库存异动记录"
      scrollX={750}
      onRow={
        clickable
          ? (record) => ({
              onClick: () => {
                if (record.business_no) navigate(`/inventory/ledger?business_no=${encodeURIComponent(record.business_no)}`)
              },
              style: { cursor: record.business_no ? 'pointer' : 'default' },
            })
          : undefined
      }
    />
  )
}
