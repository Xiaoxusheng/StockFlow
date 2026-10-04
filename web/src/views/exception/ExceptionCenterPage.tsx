import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ExceptionItem, ExceptionQuery, ExceptionStatus, ExceptionType } from '@/api/exception'
import {
  EXCEPTION_CREATE_PERMISSION,
  EXCEPTION_STATUS_TAG,
  EXCEPTION_TYPES,
  exceptionApi,
} from '@/api/exception'
import { buildSkuMaps, fetchSkuOptions } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfToolbar } from '@/components/table/SfToolbar'
import { formatDateTime } from '@/utils/format'
import ExceptionCreateModal from './ExceptionCreateModal'
import ExceptionDetailDrawer from './ExceptionDetailDrawer'

const { Text } = Typography

/** 类型选项（九类中文值域即文案：internal/returns/models.go:48-51 + chk_exceptions_type） */
const TYPE_OPTIONS: Array<{ label: string; value: ExceptionType }> = EXCEPTION_TYPES.map((value) => ({
  label: value,
  value,
}))

/** 状态筛选选项与列内标签同源（api/exception.ts EXCEPTION_STATUS_TAG 六态唯一映射） */
const STATUS_OPTIONS: Array<{ label: string; value: ExceptionStatus }> = (
  Object.keys(EXCEPTION_STATUS_TAG) as ExceptionStatus[]
).map((value) => ({ label: EXCEPTION_STATUS_TAG[value].label, value }))

/**
 * 异常单状态 → SfStatusTag：六态值域 internal/returns/models.go:39-44（迁移 000010
 * chk_exceptions_status 同源）。注册表（types/status.ts）以小写键收录部分同语义键，
 * 大写原始值不命中注册表（resolveStatus 精确匹配），label/semantic 兜底接管
 * （api/transfer.ts TRANSFER_STATUS_TAG 同款覆盖路径）；后端返回未知值时同样兜底
 * 中性灰 + 原始文案，不崩溃。
 */
function ExceptionStatusTag({ status }: { status: ExceptionStatus }) {
  const meta = EXCEPTION_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/**
 * 异常中心（frontend.md §5.1 仓储中心模块；菜单 /exceptions，config/menu.tsx:47）。
 * 后端 returns 域已交付 8 端点（internal/returns/handler.go:139-146 实测）：GET /api/exceptions
 * 列表、POST 创建、GET /:id 详情、POST assign/start/review/resolve/close——本页全部接线。
 * 出参为 ExceptionView snake_case（internal/returns/service_exception.go:64-88），列表筛选
 * 仅支持 type/status/source_type/source_no（handler.go:510-514，keyword 模糊搜索后端不支持）。
 * 九类异常（收货/质检/上架/库存/拣货/复核/物流/盘点/系统，中文值域）与生命周期六态
 * （待处理→已分派→处理中→待复核→已解决→已关闭）对齐 business-flow.md §11.2；
 * 处理记录/异常冻结经详情抽屉呈现（追加式台账，inventory-rules.md §4.2）；
 * 图片能力后端有列无写入路径，详情只读注明，不提供假上传入口（requirements.md §10）。
 */
export default function ExceptionCenterPage() {
  const [params, setParams] = useState<ExceptionQuery>({})
  const [createOpen, setCreateOpen] = useState(false)
  const [detailId, setDetailId] = useState<ExceptionItem['id'] | null>(null)

  // 按钮级权限码经 canAccess fail-closed 过滤（permission.md §5：前端仅体验优化）
  const user = useAuthStore((state) => state.user)
  const canCreate = canAccess(user, EXCEPTION_CREATE_PERMISSION)

  const list = usePagedList<ExceptionItem, ExceptionQuery>({
    queryKey: ['exception', 'center'],
    fetch: (q) => exceptionApi.list(q),
    params,
  })

  // SKU 编码映射（出参仅 sku_id 裸 ID，基础资料 options 本地映射，失败降级为 #id）
  const skuOptions = useQuery({ queryKey: ['exception', 'center', 'sku-options'], queryFn: fetchSkuOptions })
  const skuMaps = buildSkuMaps(skuOptions.data ?? [])
  const skuCodeOf = (id: number) =>
    String(id) === '0' ? '-' : (skuMaps.code.get(String(id)) ?? `#${String(id)}`)

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ExceptionQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<ExceptionItem> = [
    { title: '异常单号', dataIndex: 'exception_no', width: 160, fixed: 'left' },
    {
      title: '异常类型',
      dataIndex: 'type',
      width: 100,
      render: (v: ExceptionType) => v,
    },
    { title: '来源类型', dataIndex: 'source_type', width: 110, render: (v: string) => v || '-' },
    { title: '来源单据', dataIndex: 'source_no', width: 160, render: (v: string) => v || '-' },
    { title: 'SKU 编码', dataIndex: 'sku_id', width: 130, render: (v: number) => skuCodeOf(v) },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: ExceptionStatus) => <ExceptionStatusTag status={v} />,
    },
    {
      title: '问题描述',
      dataIndex: 'detail',
      width: 220,
      ellipsis: true,
      render: (v: string) => (
        <Text style={{ maxWidth: 220 }} ellipsis={{ tooltip: v }}>{v || '-'}</Text>
      ),
    },
    { title: '责任人', dataIndex: 'owner_name', width: 100, render: (v: string) => v || '-' },
    { title: '处理人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '登记时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '解决时间',
      dataIndex: 'resolved_at',
      width: 160,
      render: (v: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 80,
      fixed: 'right',
      render: (_, record) => (
        <Button type="link" size="small" onClick={() => setDetailId(record.id)}>
          详情
        </Button>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="异常中心"
        subtitle="九类异常统一处理：收货 / 质检 / 上架 / 库存 / 拣货 / 复核 / 物流 / 盘点 / 系统（business-flow.md §11.2）"
      />
      <Card size="small">
        <SfToolbar
          title={
            <Button
              type="primary"
              icon={<PlusOutlined />}
              disabled={!canCreate}
              title={canCreate ? undefined : `缺少 ${EXCEPTION_CREATE_PERMISSION} 权限`}
              onClick={() => setCreateOpen(true)}
            >
              登记异常
            </Button>
          }
        />
        <SfSearchForm
          fields={[
            { name: 'source_no', label: '来源单号', control: 'input', placeholder: '来源单号（如入库单/质检单号）' },
            { name: 'source_type', label: '来源类型', control: 'input', placeholder: '收货 / 拣货 / 复核 / 盘点等' },
            { name: 'type', label: '异常类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<ExceptionItem>
          storageKey="exception-center"
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
          emptyText="当前筛选条件下没有异常单"
          scrollX={1620}
        />
      </Card>
      <ExceptionCreateModal open={createOpen} onClose={() => setCreateOpen(false)} />
      <ExceptionDetailDrawer
        open={detailId != null}
        exceptionId={detailId}
        onClose={() => setDetailId(null)}
      />
    </div>
  )
}
