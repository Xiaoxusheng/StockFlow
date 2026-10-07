import { useState } from 'react'
import { Button, Card, Typography, message } from 'antd'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import { reportsApi, type ReplenishmentQuery, type ReplenishmentSuggestion } from '@/api/reports'
import { PURCHASE_CREATE_PERMISSION } from '@/api/purchase'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatQty } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<ReplenishmentSuggestion> = [
  {
    title: '仓库',
    dataIndex: 'warehouse_code',
    width: 150,
    fixed: 'left',
    render: (v: string, record: ReplenishmentSuggestion) => `${record.warehouse_name}（${v}）`,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '可用库存', dataIndex: 'current_available', width: 100, align: 'right', render: qty },
  {
    title: '日均销量',
    dataIndex: 'daily_avg_sales',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatQty(v)}</span>,
  },
  { title: '安全库存', dataIndex: 'safety_stock', width: 100, align: 'right', render: qty },
  { title: '在途', dataIndex: 'incoming_qty', width: 90, align: 'right', render: qty },
  { title: '目标库存', dataIndex: 'target_stock', width: 100, align: 'right', render: qty },
  {
    title: '建议补货量',
    dataIndex: 'suggested_qty',
    width: 110,
    align: 'right',
    render: (v: number) => <Text strong className="sf-num">{formatQty(v)}</Text>,
  },
  {
    title: '计算依据',
    dataIndex: 'basis_text',
    ellipsis: true,
    render: (v: string) => (
      <Text type="secondary" style={{ maxWidth: 360, fontSize: 12 }} ellipsis={{ tooltip: v }}>
        {v}
      </Text>
    ),
  },
]

function qty(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

/** 行键用 code 对组合键（sku_id 曾因 GORM 扫描列名恒 0 的历史遗留写法；2026-10-07 实测
 * 后端显式 column tag 已修复、sku_id 有值——预填链路直接用 record.sku_id，行键保持不动） */
function rowKeyOf(record: ReplenishmentSuggestion): string {
  return `${record.warehouse_code}-${record.sku_code}`
}

/**
 * 补货建议 → 采购新建预填 URL（frontend.md §33 契约 1）：
 * /purchases/new?warehouse_id=<id>&items=<sku_id>:<qty>[,…]
 * 同批行必须同仓库（采购单表头仅一个收货仓库），由调用方保证。
 */
function buildPurchasePrefillQuery(rows: ReplenishmentSuggestion[]): string {
  const params = new URLSearchParams()
  params.set('warehouse_id', String(rows[0].warehouse_id))
  params.set('items', rows.map((r) => `${r.sku_id}:${r.suggested_qty}`).join(','))
  return params.toString()
}

/**
 * 智能补货建议报表（GET /api/reports/replenishment-suggestions，reports:report:read）：
 * 建议量 = max(0, max(安全库存, 日均销量×(采购周期+缓冲)) − 可用 − 在途)
 * （ReplenishmentSuggestion，service.go:156-163；只读建议，不自动生成任何单据、
 * 不改任何库存——inventory-rules §11 红线）。basis_text 为后端组装的人读计算依据。
 * only_shortage=false 查看全部参与计算的行，缺省仅缺货行（handler.go:210-213）。
 * 联动（2026-10-07 批次一）：行「去补货」/勾选批量「生成采购单」带 SKU+建议量+仓库
 * 预填跳采购新建（需 purchase:purchase:create；预填后仍人工确认提交，不违背只读红线）。
 */
export default function ReportReplenishment() {
  const [params, setParams] = useState<ReplenishmentQuery>({ only_shortage: true })
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)
  const canCreatePurchase = canAccess(user, PURCHASE_CREATE_PERMISSION)
  const [selectedRowKeys, setSelectedRowKeys] = useState<Array<string | number>>([])

  const list = usePagedList<ReplenishmentSuggestion, ReplenishmentQuery>({
    queryKey: ['reports', 'replenishment-suggestions'],
    fetch: (q) => reportsApi.replenishmentSuggestions(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    // SfSearchForm 的 select 值为 string：'false' = 查看全部行，其余（含重置缺省）= 仅缺货行
    setParams({ only_shortage: values.only_shortage !== 'false' })
    list.resetToFirstPage()
  }

  /** 单行去补货（仅建议补货量 > 0 的行有意义，列渲染已过滤） */
  const goReplenish = (rows: ReplenishmentSuggestion[]) => {
    if (rows.length === 0) return
    navigate(`/purchases/new?${buildPurchasePrefillQuery(rows)}`)
  }

  /** 批量生成采购单：同批行必须同仓库（表头单仓），跨仓整体拦截提示分仓操作 */
  const handleBatchCreate = () => {
    const rows = selectedRowKeys
      .map((key) => list.items.find((item) => rowKeyOf(item) === String(key)))
      .filter((item): item is ReplenishmentSuggestion => item != null)
    if (rows.length === 0) return
    const warehouseSet = new Set(rows.map((r) => r.warehouse_id))
    if (warehouseSet.size > 1) {
      messageApi.warning(`勾选行跨 ${warehouseSet.size} 个仓库，采购单一次只能属一个仓库，请按仓库分别勾选`)
      return
    }
    const replenishable = rows.filter((r) => r.suggested_qty > 0)
    if (replenishable.length === 0) {
      messageApi.warning('勾选行均无建议补货量（建议补货量 > 0 才可生成采购）')
      return
    }
    goReplenish(replenishable)
  }

  const columns: ColumnsType<ReplenishmentSuggestion> = [
    ...COLUMNS,
    ...(canCreatePurchase
      ? ([
          {
            title: '操作',
            key: 'actions',
            fixed: 'right',
            width: 90,
            render: (_, record) =>
              record.suggested_qty > 0 ? (
                <Button type="link" size="small" onClick={() => goReplenish([record])}>
                  去补货
                </Button>
              ) : (
                '-'
              ),
          },
        ] as ColumnsType<ReplenishmentSuggestion>)
      : []),
  ]

  return (
    <Card size="small" title="智能补货建议">
      {contextHolder}
      <SfSearchForm
        loading={list.isFetching}
        fields={[
          {
            name: 'only_shortage',
            label: '建议范围',
            control: 'select',
            options: [
              { label: '仅缺货行（建议补货量 > 0）', value: 'true' },
              { label: '全部参与计算行', value: 'false' },
            ],
            placeholder: '仅缺货行',
          },
        ]}
        onSearch={handleSearch}
        /* 批量生成采购单与查询/重置同行（用户口径：批量按钮不占独立面板） */
        extraActions={
          canCreatePurchase &&
          selectedRowKeys.length > 0 && (
            <Button type="primary" onClick={handleBatchCreate}>
              生成采购单（{selectedRowKeys.length}）
            </Button>
          )
        }
      />
      <SfTable<ReplenishmentSuggestion>
        storageKey="report-replenishment"
        rowKey={rowKeyOf}
        rowSelection={
          canCreatePurchase
            ? {
                selectedRowKeys,
                onChange: (keys) => setSelectedRowKeys(keys as Array<string | number>),
              }
            : undefined
        }
        columns={columns}
        dataSource={list.items}
        loading={list.isFetching}
        error={list.error}
        onRetry={list.refetch}
        onRefresh={list.refetch}
        pagination={list.pagination}
        total={list.total}
        onPageChange={list.onPageChange}
        emptyText="当前没有补货建议（可用库存与在途已满足安全库存与近期出库需求）"
        scrollX={1310}
      />
    </Card>
  )
}
