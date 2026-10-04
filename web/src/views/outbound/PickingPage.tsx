import { useMemo, useState } from 'react'
import { Card, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundTaskApi,
  type PickTask,
  type PickTaskQuery,
  type PickTaskStatus,
} from '@/api/outbound'
import { toStatusKey } from '@/api/masterdata'
import {
  buildBinCodeMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 拣货任务状态 → SfStatusTag（models.go:294-301 六值；
 * CLAIMED/EXCEPTION 注册表暂无键，以 label/semantic 兜底） */
const PICK_STATUS_TAG: Record<PickTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待领取', semantic: 'pending' },
  CLAIMED: { label: '已领取', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  EXCEPTION: { label: '拣货异常', semantic: 'danger' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

function PickStatusTag({ status }: { status: PickTaskStatus }) {
  const meta = PICK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listPicks：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: PickTaskStatus }> = [
  { label: '待领取', value: 'PENDING' },
  { label: '已领取', value: 'CLAIMED' },
  { label: '拣货中', value: 'PICKING' },
  { label: '已拣货', value: 'PICKED' },
  { label: '拣货异常', value: 'EXCEPTION' },
  { label: '已取消', value: 'CANCELLED' },
]

/** 可选维度 ID 0 值显示占位符（0=非批次/无来源库位，LedgerPage 同口径） */
function renderIdOrDash(value: number): string {
  return String(value) === '0' ? '-' : String(value)
}

/**
 * 拣货管理（GET /api/picks，后端 M2 已交付）：列表列回对 PickTask 裸模型——
 * 任务内容按 business-flow.md §8.2：SKU → 来源库位 → 数量 → 操作人 → 完成时间；
 * SKU/库位/仓库 ID 经基础资料 options 本地映射，映射失败降级为 ID。
 * 领取/确认写端点（PUT /api/picks/{id}/claim|confirm）已注册，交互设计不在本轮范围，列表只读。
 */
export default function PickingPage() {
  const [params, setParams] = useState<PickTaskQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<PickTask, PickTaskQuery>({
    queryKey: ['outbound', 'picks'],
    fetch: (q) => outboundTaskApi.picks.list(q),
    params,
    // §26.3：分页经 persistKey 持久化，进详情返回后恢复离开前分页
    persistKey: 'outbound-picks',
  })

  // SKU / 仓库 / 库位 ID → 编码/名称（options.ts：一次取全基础资料，失败降级为 ID）
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const binOptions = useQuery({
    queryKey: ['outbound', 'bin-options'],
    queryFn: fetchBinOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const binCodes = useMemo(() => buildBinCodeMap(binOptions.data ?? []), [binOptions.data])

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as PickTaskQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<PickTask> = [
    { title: '拣货任务号', dataIndex: 'pick_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: PickTask) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: PickTask) => {
        const name = skuMaps.name.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '来源库位',
      dataIndex: 'source_bin_id',
      width: 110,
      render: (v: number) => binCodes.get(idKey(v)) ?? renderIdOrDash(v),
    },
    { title: '批次 ID', dataIndex: 'batch_id', width: 90, render: renderIdOrDash },
    {
      title: '应拣数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已拣数量',
      dataIndex: 'picked_qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: PickTaskStatus) => <PickStatusTag status={v} />,
    },
    { title: '领取人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '拣货时间',
      dataIndex: 'picked_at',
      width: 170,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      render: (v: number) => {
        const name = warehouseNames.get(idKey(v)) ?? idKey(v)
        return (
          <Text style={{ maxWidth: 110 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="拣货管理"
        subtitle="拣货任务：SKU → 来源库位 → 数量 → 操作人 → 完成时间"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: idKey(w.id),
              })),
            },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<PickTask>
          storageKey="outbound-picks"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有拣货任务"
          scrollX={1700}
        />
      </Card>
    </div>
  )
}
