import { useEffect, useState } from 'react'
import { Button, Card, Form, Input, Modal, Popconfirm, Spin, Tree, Typography, message } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import type { TreeDataNode } from 'antd'
import { resolveErrorMessage } from '@/api/client'
import { rbacApi, type PermissionItem, type RoleItem, type RoleQuery } from '@/api/rbac'
import type { CommonStatus } from '@/api/user'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ACTIVE' },
  { label: '已停用', value: 'DISABLED' },
]

function statusTagKey(status: CommonStatus): string {
  return status === 'ACTIVE' ? 'enabled' : 'disabled'
}

/** 权限平铺列表 → 树（按 parentId 归组；父级缺失时按根节点处理） */
function buildPermissionTree(items: PermissionItem[]): TreeDataNode[] {
  const nodes = new Map<string, TreeDataNode & { children?: TreeDataNode[] }>()
  for (const item of items) {
    nodes.set(String(item.id), { key: String(item.id), title: `${item.name}（${item.code}）` })
  }
  const roots: TreeDataNode[] = []
  for (const item of items) {
    const node = nodes.get(String(item.id))
    if (!node) continue
    const parentKey = item.parentId != null ? String(item.parentId) : null
    const parent = parentKey ? nodes.get(parentKey) : undefined
    if (parent && parent !== node) {
      parent.children = parent.children ?? []
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }
  return roots
}

interface RoleFormValues {
  code: string
  name: string
}

interface RoleFormModalProps {
  editing: RoleItem | null
  submitting: boolean
  onCancel: () => void
  onSubmit: (values: RoleFormValues) => void
}

/** 新建 / 编辑角色弹窗（编码唯一，编辑时不可改） */
function RoleFormModal({ editing, submitting, onCancel, onSubmit }: RoleFormModalProps) {
  const isEdit = editing !== null
  const [form] = Form.useForm<RoleFormValues>()
  return (
    <Modal
      title={isEdit ? `编辑角色：${editing.name}` : '新建角色'}
      open
      confirmLoading={submitting}
      onCancel={onCancel}
      onOk={() => {
        void form.submit()
      }}
      okText={isEdit ? '保存' : '创建'}
      maskClosable={false}
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={(values) => onSubmit(values)}
        initialValues={editing ? { code: editing.code, name: editing.name } : undefined}
      >
        <Form.Item
          name="code"
          label="角色编码"
          rules={[
            { required: true, message: '请输入角色编码' },
            { pattern: /^[a-z][a-z0-9_]{2,31}$/, message: '3–32 位小写字母、数字或下划线，以字母开头' },
          ]}
          extra="编码用于权限判断（如 warehouse_manager），创建后不可修改"
        >
          <Input disabled={isEdit} placeholder="如 warehouse_manager" autoComplete="off" />
        </Form.Item>
        <Form.Item name="name" label="角色名称" rules={[{ required: true, message: '请输入角色名称' }]}>
          <Input placeholder="如 仓库管理员" allowClear maxLength={32} />
        </Form.Item>
      </Form>
    </Modal>
  )
}

interface AssignPermissionsModalProps {
  role: RoleItem
  submitting: boolean
  onCancel: () => void
  onSubmit: (permissionIds: string[]) => void
}

/** 绑定权限弹窗（PUT /api/roles/{id}/permissions；权限点来自 GET /api/permissions 全量） */
function AssignPermissionsModal({ role, submitting, onCancel, onSubmit }: AssignPermissionsModalProps) {
  const [checkedKeys, setCheckedKeys] = useState<string[]>([])
  const [expandedKeys, setExpandedKeys] = useState<string[]>([])

  const detailQuery = useQuery({
    queryKey: ['system', 'role', String(role.id)],
    queryFn: () => rbacApi.role(String(role.id)),
  })
  const permissionsQuery = useQuery({
    queryKey: ['system', 'permissions', 'all'],
    queryFn: () => rbacApi.permissions({ page: 1, pageSize: 1000 }),
  })

  // 角色已绑定权限 → 勾选态；权限树就绪后默认展开全部父节点
  useEffect(() => {
    if (detailQuery.data) setCheckedKeys(detailQuery.data.permissionIds.map(String))
  }, [detailQuery.data])

  useEffect(() => {
    const items = permissionsQuery.data?.items
    if (!items) return
    const parentIds = new Set(items.filter((item) => item.parentId != null).map((item) => String(item.parentId)))
    setExpandedKeys(items.filter((item) => parentIds.has(String(item.id))).map((item) => String(item.id)))
  }, [permissionsQuery.data])

  const treeData = buildPermissionTree(permissionsQuery.data?.items ?? [])

  return (
    <Modal
      title={`绑定权限：${role.name}`}
      open
      confirmLoading={submitting}
      onCancel={onCancel}
      onOk={() => onSubmit(checkedKeys)}
      okText="保存"
      okButtonProps={{ disabled: detailQuery.isPending }}
      maskClosable={false}
      width={520}
    >
      {permissionsQuery.isPending ? (
        <div style={{ textAlign: 'center', padding: '32px 0' }}>
          <Spin />
        </div>
      ) : permissionsQuery.error ? (
        <Text type="danger">{resolveErrorMessage(permissionsQuery.error)}</Text>
      ) : treeData.length === 0 ? (
        <Text type="secondary">暂无权限点（权限点由后端权限种子维护）</Text>
      ) : (
        <div style={{ maxHeight: 420, overflowY: 'auto' }}>
          <Tree
            checkable
            treeData={treeData}
            checkedKeys={checkedKeys}
            expandedKeys={expandedKeys}
            onExpand={(keys) => setExpandedKeys(keys.map(String))}
            onCheck={(checked) => {
              if (Array.isArray(checked)) setCheckedKeys(checked.map(String))
            }}
          />
        </div>
      )}
    </Modal>
  )
}

type ModalState = { kind: 'create' } | { kind: 'edit'; role: RoleItem } | { kind: 'permissions'; role: RoleItem } | null

/** 角色管理（frontend.md 系统管理：角色）：列表 + 新建/编辑/启停/绑定权限 */
export default function RoleListPage() {
  const [params, setParams] = useState<RoleQuery>({})
  const [modal, setModal] = useState<ModalState>(null)
  const queryClient = useQueryClient()

  const list = usePagedList<RoleItem, RoleQuery>({
    queryKey: ['system', 'roles'],
    fetch: (q) => rbacApi.roles(q),
    params,
  })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['system', 'roles'] })

  const createMutation = useMutation({
    mutationFn: (payload: RoleFormValues) => rbacApi.createRole(payload),
    onSuccess: () => {
      message.success('角色已创建')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: RoleFormValues }) => rbacApi.updateRole(id, payload),
    onSuccess: () => {
      message.success('角色已保存')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: string; status: CommonStatus }) => rbacApi.setRoleStatus(id, status),
    onSuccess: (_data, variables) => {
      message.success(variables.status === 'ACTIVE' ? '角色已启用' : '角色已停用')
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const assignPermissionsMutation = useMutation({
    mutationFn: ({ id, permissionIds }: { id: string; permissionIds: string[] }) =>
      rbacApi.assignRolePermissions(id, permissionIds),
    onSuccess: () => {
      message.success('权限已更新')
      setModal(null)
      void queryClient.invalidateQueries({ queryKey: ['system', 'role'] })
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as RoleQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<RoleItem> = [
    { title: '角色编码', dataIndex: 'code', width: 150, fixed: 'left' },
    { title: '角色名称', dataIndex: 'name', width: 180 },
    {
      title: '类型',
      dataIndex: 'isSystem',
      width: 90,
      render: (v: boolean) =>
        v ? <SfStatusTag label="内置" semantic="processing" /> : <SfStatusTag label="自定义" semantic="neutral" />,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: CommonStatus) => <SfStatusTag status={statusTagKey(v)} />,
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 190,
      fixed: 'right',
      render: (_, record) => {
        const id = String(record.id)
        return (
          <>
            <Button type="link" size="small" onClick={() => setModal({ kind: 'edit', role: record })}>
              编辑
            </Button>
            <Button type="link" size="small" onClick={() => setModal({ kind: 'permissions', role: record })}>
              权限
            </Button>
            {record.status === 'ACTIVE' ? (
              <Popconfirm
                title="确认停用该角色？"
                description="停用后关联用户将失去该角色的权限"
                onConfirm={() => statusMutation.mutate({ id, status: 'DISABLED' })}
              >
                <Button type="link" size="small" danger>
                  停用
                </Button>
              </Popconfirm>
            ) : (
              <Button type="link" size="small" onClick={() => statusMutation.mutate({ id, status: 'ACTIVE' })}>
                启用
              </Button>
            )}
          </>
        )
      },
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="角色"
        subtitle="角色 / 权限绑定"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal({ kind: 'create' })}>
            新建角色
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '角色编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<RoleItem>
          storageKey="system-roles"
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
          emptyText="暂无角色"
          scrollX={860}
        />
      </Card>

      {modal && (modal.kind === 'create' || modal.kind === 'edit') && (
        <RoleFormModal
          editing={modal.kind === 'edit' ? modal.role : null}
          submitting={createMutation.isPending || updateMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={(values) => {
            if (modal.kind === 'edit') updateMutation.mutate({ id: String(modal.role.id), payload: values })
            else createMutation.mutate(values)
          }}
        />
      )}
      {modal?.kind === 'permissions' && (
        <AssignPermissionsModal
          role={modal.role}
          submitting={assignPermissionsMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={(permissionIds) =>
            assignPermissionsMutation.mutate({ id: String(modal.role.id), permissionIds })
          }
        />
      )}
    </div>
  )
}
