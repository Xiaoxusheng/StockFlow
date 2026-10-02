import { useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { rbacApi, type PermissionItem, type PermissionQuery, type PermissionType } from '@/api/rbac'
import type { OnOffStatus } from '@/api/user'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatNumber } from '@/utils/format'

const TYPE_META: Record<PermissionType, { label: string; semantic: StatusSemantic }> = {
  MENU: { label: '菜单', semantic: 'processing' },
  BUTTON: { label: '按钮', semantic: 'pending' },
  API: { label: '接口', semantic: 'success' },
}

const TYPE_OPTIONS: Array<{ label: string; value: PermissionType }> = [
  { label: '菜单', value: 'MENU' },
  { label: '按钮', value: 'BUTTON' },
  { label: '接口', value: 'API' },
]

/** permissions 状态枚举为 ENABLED/DISABLED（迁移 000001 chk_permissions_status） */
function statusTagKey(status: OnOffStatus): string {
  return status === 'ENABLED' ? 'enabled' : 'disabled'
}

/** 权限列表（frontend.md 系统管理：权限）——权限点由后端种子冻结维护，页面只读 */
export default function PermissionListPage() {
  const [params, setParams] = useState<PermissionQuery>({})
  const list = usePagedList<PermissionItem, PermissionQuery>({
    queryKey: ['system', 'permissions'],
    fetch: (q) => rbacApi.permissions(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as PermissionQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<PermissionItem> = [
    { title: '权限编码', dataIndex: 'code', width: 220, fixed: 'left' },
    { title: '权限名称', dataIndex: 'name', width: 180 },
    {
      title: '类型',
      dataIndex: 'type',
      width: 90,
      render: (v: PermissionType) => {
        const meta = TYPE_META[v] ?? { label: v, semantic: 'neutral' as StatusSemantic }
        return <SfStatusTag label={meta.label} semantic={meta.semantic} />
      },
    },
    {
      title: '排序',
      dataIndex: 'sort',
      width: 80,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: OnOffStatus) => <SfStatusTag status={statusTagKey(v)} />,
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader title="权限" subtitle="权限点清单（只读，由后端权限种子维护）" />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '权限编码 / 名称' },
            { name: 'type', label: '类型', control: 'select', options: TYPE_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<PermissionItem>
          storageKey="system-permissions"
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
          emptyText="暂无权限点"
          scrollX={660}
        />
      </Card>
    </div>
  )
}
