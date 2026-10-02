import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  EXCEPTION_STATUS_TAG,
  EXCEPTION_TYPE_LABEL,
  exceptionApi,
  type ExceptionItem,
  type ExceptionQuery,
  type ExceptionStatus,
  type ExceptionType,
} from '@/api/exception'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

const TYPE_OPTIONS: Array<{ label: string; value: ExceptionType }> = [
  { label: '收货异常', value: 'receiving' },
  { label: '质检异常', value: 'quality' },
  { label: '上架异常', value: 'putaway' },
  { label: '库存异常', value: 'inventory' },
  { label: '拣货异常', value: 'picking' },
  { label: '复核异常', value: 'checking' },
  { label: '物流异常', value: 'logistics' },
  { label: '盘点异常', value: 'counting' },
  { label: '系统异常', value: 'system' },
]

const STATUS_OPTIONS: Array<{ label: string; value: ExceptionStatus }> = [
  { label: '发现', value: 'discovered' },
  { label: '已创建', value: 'created' },
  { label: '已分派', value: 'assigned' },
  { label: '处理中', value: 'processing' },
  { label: '待复核', value: 'pending_recheck' },
  { label: '已解决', value: 'resolved' },
  { label: '已关闭', value: 'closed' },
]

/**
 * 生命周期状态 → SfStatusTag：对齐 business-flow.md §11.2
 * （发现 → 创建 → 分派 → 处理中 → 待复核 → 已解决 → 已关闭）；
 * processing/closed 命中 types/status.ts 注册表，其余 key 由
 * EXCEPTION_STATUS_TAG 的 label/semantic 兜底；后端返回未知值时兜底中性灰 + 原始文案，不崩溃。
 */
function ExceptionStatusTag({ status }: { status: ExceptionStatus }) {
  const meta = EXCEPTION_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

const COLUMNS: ColumnsType<ExceptionItem> = [
  { title: '异常单号', dataIndex: 'exceptionNo', width: 150, fixed: 'left' },
  {
    title: '异常类型',
    dataIndex: 'exceptionType',
    width: 100,
    render: (v: ExceptionType) => EXCEPTION_TYPE_LABEL[v] ?? v,
  },
  {
    title: '异常标题',
    dataIndex: 'title',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130, render: (v?: string) => v ?? '-' },
  { title: '来源单据', dataIndex: 'bizNo', width: 150, render: (v?: string) => v ?? '-' },
  { title: '责任人', dataIndex: 'ownerName', width: 100, render: (v?: string) => v ?? '-' },
  { title: '处理人', dataIndex: 'handlerName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: ExceptionStatus) => <ExceptionStatusTag status={v} />,
  },
  {
    title: '发现时间',
    dataIndex: 'discoveredAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '解决时间',
    dataIndex: 'resolvedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 异常中心（frontend.md §5.1 仓储中心模块；菜单 /exceptions，config/menu.tsx:47）。
 * GET /api/exceptions 为前端先行契约：M1 后端未交付异常域（backend-m1-plan.md §13），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 九类异常（收货/质检/上架/库存/拣货/复核/物流/盘点/系统）与生命周期状态
 * （发现 → 创建 → 分派 → 处理中 → 待复核 → 已解决 → 已关闭）对齐 business-flow.md §11.2；
 * 异常单支持图片、附件、评论、处理人、责任人、处理记录（详情随领域冻结后补齐）。
 */
export default function ExceptionCenterPage() {
  const [params, setParams] = useState<ExceptionQuery>({})
  const list = usePagedList<ExceptionItem, ExceptionQuery>({
    queryKey: ['exception', 'center'],
    fetch: (q) => exceptionApi.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ExceptionQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="异常中心"
        subtitle="九类异常统一处理：收货 / 质检 / 上架 / 库存 / 拣货 / 复核 / 物流 / 盘点 / 系统（business-flow.md §11.2）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '异常单号 / 标题 / 来源单据' },
            { name: 'exceptionType', label: '异常类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<ExceptionItem>
          storageKey="exception-center"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有异常单"
          scrollX={1560}
        />
      </Card>
    </div>
  )
}
