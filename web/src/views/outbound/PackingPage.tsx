import { useMemo } from 'react'
import { Card, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { outboundTaskApi, type PackingRecord, type PackingRecordQuery } from '@/api/outbound'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/**
 * 打包管理（GET /api/packing，后端 M2 已交付）：列表列回对 PackingRecord 裸模型——
 * 包裹编号/包装材料/长宽高/重量/体积/快递公司/快递单号（business-flow.md §8.4 全列），
 * 一单允许多包裹。模型无状态字段，不渲染状态列（裸模型回对，不虚构状态）。
 * 仓库 ID 经基础资料 options 本地映射，失败降级为 ID；
 * 打包写端点（POST /api/packing）已注册，交互设计不在本轮范围，列表只读。
 */
export default function PackingPage() {
  const navigate = useNavigate()
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<PackingRecord, PackingRecordQuery>({
    queryKey: ['outbound', 'packing'],
    fetch: (q) => outboundTaskApi.packing.list(q),
    urlSync: true,
  })

  // 仓库 ID → 名称（options.ts：一次取全基础资料，失败降级为 ID）
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )

  const columns: ColumnsType<PackingRecord> = [
    { title: '包裹编号', dataIndex: 'package_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: PackingRecord) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    {
      title: '包装材料',
      dataIndex: 'packing_material',
      width: 110,
      render: (v: string) => v || '-',
    },
    {
      title: '尺寸（长×宽×高）',
      dataIndex: 'length',
      key: 'dimensions',
      width: 160,
      align: 'right',
      render: (_: unknown, record: PackingRecord) => (
        <span className="sf-num" style={{ whiteSpace: 'nowrap' }}>
          {formatNumber(record.length)} × {formatNumber(record.width)} × {formatNumber(record.height)}
        </span>
      ),
    },
    {
      title: '重量',
      dataIndex: 'weight',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '体积',
      dataIndex: 'volume',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    { title: '快递公司', dataIndex: 'carrier', width: 120, render: (v: string) => v || '-' },
    { title: '快递单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || '-' },
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
      // PackingRecord 无独立打包时间字段，created_at 即打包落库时间（models.go:167-186）
      title: '打包时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v: string) => v || '-' },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="打包管理"
        subtitle="打包记录：包裹 / 包装材料 / 快递信息（一单允许多包裹）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
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
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<PackingRecord>
          storageKey="outbound-packing"
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
          emptyText="当前筛选条件下没有打包记录"
          scrollX={1470}
        />
      </Card>
    </div>
  )
}
