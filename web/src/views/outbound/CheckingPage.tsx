import { useMemo, useState } from 'react'
import { Card, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundTaskApi,
  type CheckTask,
  type CheckTaskQuery,
  type CheckTaskStatus,
} from '@/api/outbound'
import { toStatusKey } from '@/api/masterdata'
import { buildSkuMaps, buildWarehouseMaps, fetchSkuOptions, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 复核任务状态 → SfStatusTag（models.go:315-319 三值，无 CLAIMED——领取为原子指派不迁移状态；
 * DONE/EXCEPTION 注册表暂无键，以 label/semantic 兜底） */
const CHECK_STATUS_TAG: Record<CheckTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待复核', semantic: 'pending' },
  DONE: { label: '已复核', semantic: 'success' },
  EXCEPTION: { label: '复核异常', semantic: 'danger' },
}

function CheckStatusTag({ status }: { status: CheckTaskStatus }) {
  const meta = CHECK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listChecks：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: CheckTaskStatus }> = [
  { label: '待复核', value: 'PENDING' },
  { label: '已复核', value: 'DONE' },
  { label: '复核异常', value: 'EXCEPTION' },
]

/**
 * 复核管理（GET /api/checks，后端 M2 已交付）：列表列回对 CheckTask 裸模型——
 * 复核重新确认 SKU/条码/数量/批次/序列号/订单（business-flow.md §8.3）；
 * result 为空串 = 复核通过，异常为五类中文值域（错货/少货/多货/批次错误/序列号错误）直出。
 * SKU/仓库 ID 经基础资料 options 本地映射，失败降级为 ID；
 * 领取/确认写端点（PUT /api/checks/{id}/claim|confirm）已注册，交互设计不在本轮范围，列表只读。
 */
export default function CheckingPage() {
  const [params, setParams] = useState<CheckTaskQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<CheckTask, CheckTaskQuery>({
    queryKey: ['outbound', 'checks'],
    fetch: (q) => outboundTaskApi.checks.list(q),
    params,
    // §26.3：分页经 persistKey 持久化，进详情返回后恢复离开前分页
    persistKey: 'outbound-checks',
  })

  // SKU / 仓库 ID → 编码/名称（options.ts：一次取全基础资料，失败降级为 ID）
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as CheckTaskQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<CheckTask> = [
    { title: '复核任务号', dataIndex: 'check_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: CheckTask) => (
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
      render: (_: unknown, record: CheckTask) => {
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
    // 序列号 SKU 一行一件（models.go:143-164），非序列号任务为空串
    { title: '序列号', dataIndex: 'serial_no', width: 140, render: (v: string) => v || '-' },
    {
      title: '复核数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '复核结果',
      dataIndex: 'result',
      width: 110,
      render: (v: string) => v || '-',
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: CheckTaskStatus) => <CheckStatusTag status={v} />,
    },
    { title: '复核人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '复核时间',
      dataIndex: 'done_at',
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
        title="复核管理"
        subtitle="复核确认：SKU / 条码 / 数量 / 批次 / 序列号 / 订单"
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
        <SfTable<CheckTask>
          storageKey="outbound-checks"
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
          emptyText="当前筛选条件下没有复核任务"
          scrollX={1650}
        />
      </Card>
    </div>
  )
}
