import { useState } from 'react'
import type { ReactNode } from 'react'
import { Button } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useNavigate, useLocation } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  DEVICE_TYPE_BY_PATH,
  DEVICE_TYPE_LABEL,
  deviceApi,
  type DeviceItem,
  type DeviceQuery,
  type DeviceType,
} from '@/api/device'
import { warehouseApi } from '@/api/warehouse'
import { OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { usePagedList } from '@/hooks/usePagedList'
import { SfDeviceStatus } from '@/components/device/SfDeviceStatus'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime } from '@/utils/format'

/** 在线筛选选项（字符串形态，提交前经 toDeviceQuery 转回契约的 boolean） */
const ONLINE_OPTIONS = [
  { label: '在线', value: 'true' },
  { label: '离线', value: 'false' },
]

function toDeviceQuery(values: Record<string, unknown>): DeviceQuery {
  const { online, ...rest } = values
  return {
    ...(rest as DeviceQuery),
    online: online === 'true' ? true : online === 'false' ? false : undefined,
  }
}

/** 文本列统一空值占位（utils/format EMPTY_TEXT 约定） */
function renderText(value?: string | null): string {
  return value ?? '-'
}

function renderDateTime(value?: string | null): ReactNode {
  return <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(value)}</span>
}

/**
 * 设备中心列表页（frontend.md §14.1 十列 / devices.md §7.1）。
 * 四条菜单路由复用同一组件，deviceType 由路由参数化：
 * /devices/scanners、/devices/pda、/devices/pads、/devices/printers（config/menu.tsx:141-144），
 * 也可通过 props 显式传入。行点击进入设备详情 /devices/:id。
 * 后端 /api/devices 域未交付时整表呈统一错误态。
 */
export default function DeviceListPage({ deviceType: deviceTypeProp }: { deviceType?: DeviceType } = {}) {
  const location = useLocation()
  const navigate = useNavigate()
  const deviceType = deviceTypeProp ?? DEVICE_TYPE_BY_PATH[location.pathname]
  const [params, setParams] = useState<DeviceQuery>({})

  const typeLabel = deviceType ? DEVICE_TYPE_LABEL[deviceType] : ''

  const list = usePagedList<DeviceItem, DeviceQuery>({
    queryKey: ['devices', 'list', deviceType],
    fetch: (query) => deviceApi.list({ ...query, type: deviceType }),
    params,
    persistKey: deviceType ? `devices-${deviceType}` : undefined,
    enabled: Boolean(deviceType),
  })

  // 仓库筛选下拉：一次取全；接口失败降级为空数组，不阻塞其余筛选（ProductListPage 同款处理）
  const warehouses = useQuery({
    queryKey: ['warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const warehouseOptions = (warehouses.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: String(item.id),
  }))

  // 十列 = frontend.md §14.1 全集，列名逐字一致
  const columns: ColumnsType<DeviceItem> = [
    { title: '设备名称', dataIndex: 'name', width: 160, ellipsis: true },
    {
      title: '设备类型',
      dataIndex: 'type',
      width: 100,
      render: (value: DeviceType) => DEVICE_TYPE_LABEL[value] ?? value,
    },
    { title: '品牌', dataIndex: 'brand', width: 110, render: renderText },
    { title: '型号', dataIndex: 'model', width: 110, render: renderText },
    { title: '仓库', dataIndex: 'warehouse_name', width: 110, render: renderText },
    { title: '绑定用户', dataIndex: 'bound_user_name', width: 110, render: renderText },
    {
      title: '在线状态',
      dataIndex: 'online',
      width: 90,
      render: (value: boolean) => <SfDeviceStatus online={value} />,
    },
    { title: '最后在线', dataIndex: 'last_online_at', width: 160, render: renderDateTime },
    { title: 'App 版本', dataIndex: 'app_version', width: 100, render: renderText },
    { title: '最后扫码', dataIndex: 'last_scan_at', width: 160, render: renderDateTime },
  ]

  if (!deviceType) {
    return (
      <div className="sf-page">
        <SfError error={new Error(`未知的设备路由：${location.pathname}`)} />
      </div>
    )
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title={typeLabel}
        subtitle="设备登记 / 激活 / 绑定（devices.md §6–7）"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/devices/new')}>
            新建设备
          </Button>
        }
      />
      <SfSearchForm
        fields={[
          { name: 'keyword', label: '关键词', control: 'input', placeholder: '设备编号 / 名称' },
          { name: 'warehouse_id', label: '仓库', control: 'select', options: warehouseOptions },
          { name: 'online', label: '在线状态', control: 'select', options: ONLINE_OPTIONS },
        ]}
        onSearch={(values) => {
          setParams(toDeviceQuery(values))
          list.resetToFirstPage()
        }}
        loading={list.isFetching}
      />
      <SfTable<DeviceItem>
        storageKey="device-list"
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
        emptyText={`暂无${typeLabel}，点击右上角「新建设备」登记`}
        scrollX={1220}
        onRow={(record) => ({
          onClick: () => navigate(`/devices/${encodeURIComponent(String(record.id))}`),
          style: { cursor: 'pointer' },
        })}
      />
    </div>
  )
}
