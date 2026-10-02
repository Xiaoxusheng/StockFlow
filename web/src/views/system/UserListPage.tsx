import { useEffect, useState } from 'react'
import {
  Button,
  Card,
  Dropdown,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  TreeSelect,
  Typography,
  message,
} from 'antd'
import { MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import type { MenuProps } from 'antd'
import { resolveErrorMessage } from '@/api/client'
import { rbacApi, type DepartmentNode } from '@/api/rbac'
import {
  userApi,
  type DataScope,
  type UserCreatePayload,
  type UserItem,
  type UserQuery,
  type UserStatus,
  type UserUpdatePayload,
} from '@/api/user'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ACTIVE' },
  { label: '已停用', value: 'DISABLED' },
]

/** 数据范围展示（M1 契约 §7.4；语义色经 SfStatusTag 统一） */
const DATA_SCOPE_META: Record<DataScope, { label: string; semantic: StatusSemantic }> = {
  ALL: { label: '全部数据', semantic: 'success' },
  SPECIFIED_WAREHOUSE: { label: '指定仓库', semantic: 'processing' },
  DEPARTMENT: { label: '本部门', semantic: 'processing' },
  SELF: { label: '仅本人', semantic: 'neutral' },
  SELF_IN_CHARGE: { label: '本人负责', semantic: 'neutral' },
}

/** 强密码策略：长度 ≥ 8 且含字母 + 数字（backend-m1-plan.md §7.2） */
const PASSWORD_PATTERN = /^(?=.*[A-Za-z])(?=.*\d)\S{8,64}$/

/** 后端单页上限（internal/response/response.go:47 MaxPageSize）——角色选项一次取全 */
const OPTIONS_PAGE_SIZE = 100

const DATA_SCOPE_OPTIONS: Array<{ label: string; value: DataScope }> = [
  { label: '全部数据', value: 'ALL' },
  { label: '指定仓库', value: 'SPECIFIED_WAREHOUSE' },
  { label: '本部门', value: 'DEPARTMENT' },
  { label: '仅本人', value: 'SELF' },
  { label: '本人负责', value: 'SELF_IN_CHARGE' },
]

interface DeptTreeOption {
  value: string
  title: string
  children?: DeptTreeOption[]
}

/** 部门树 → 筛选下拉选项（缩进表达层级） */
function flattenDepartments(nodes: DepartmentNode[], depth = 0): Array<{ label: string; value: string }> {
  return nodes.flatMap((node) => [
    { label: `${'　'.repeat(depth)}${node.name}`, value: node.id },
    ...flattenDepartments(node.children ?? [], depth + 1),
  ])
}

/** 部门树 → TreeSelect 数据 */
function toTreeOptions(nodes: DepartmentNode[]): DeptTreeOption[] {
  return nodes.map((node) => ({
    value: node.id,
    title: `${node.name}（${node.code}）`,
    children: node.children && node.children.length > 0 ? toTreeOptions(node.children) : undefined,
  }))
}

/** 部门树 → id→名称映射（列表部门列经 department_id 映射展示，后端不返回部门名） */
function collectDepartmentNames(nodes: DepartmentNode[], map = new Map<string, string>()): Map<string, string> {
  for (const node of nodes) {
    map.set(node.id, node.name)
    collectDepartmentNames(node.children ?? [], map)
  }
  return map
}

/** 业务状态 → SfStatusTag 注册表 key（types/status.ts：enabled/disabled） */
function statusTagKey(status: UserStatus): string {
  return status === 'ACTIVE' ? 'enabled' : 'disabled'
}

interface UserFormValues {
  username: string
  password: string
  real_name?: string
  phone?: string
  email?: string
  department_id?: string
  data_scope: DataScope
}

interface UserFormModalProps {
  editing: UserItem | null
  departmentTreeOptions: DeptTreeOption[]
  submitting: boolean
  onCancel: () => void
  onSubmit: (values: UserFormValues) => void
}

/** 新建 / 编辑用户弹窗（新建含初始密码，编辑不允许改用户名与密码） */
function UserFormModal({ editing, departmentTreeOptions, submitting, onCancel, onSubmit }: UserFormModalProps) {
  const isEdit = editing !== null
  const [form] = Form.useForm<UserFormValues>()

  return (
    <Modal
      title={isEdit ? `编辑用户：${editing.username}` : '新建用户'}
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
        initialValues={
          editing
            ? {
                real_name: editing.real_name,
                phone: editing.phone,
                email: editing.email,
                department_id: editing.department_id ?? undefined,
                data_scope: editing.data_scope,
              }
            : undefined
        }
      >
        <Form.Item
          name="username"
          label="用户名"
          rules={[
            { required: true, message: '请输入用户名' },
            { pattern: /^[a-zA-Z0-9_]{3,32}$/, message: '3–32 位字母、数字或下划线' },
          ]}
        >
          <Input disabled={isEdit} placeholder="登录账号" autoComplete="off" />
        </Form.Item>
        {!isEdit && (
          <Form.Item
            name="password"
            label="初始密码"
            rules={[
              { required: true, message: '请输入初始密码' },
              { pattern: PASSWORD_PATTERN, message: '长度至少 8 位且需包含字母和数字' },
            ]}
            extra="首次登录将要求修改密码"
          >
            <Input.Password placeholder="长度 ≥ 8 位，含字母和数字" autoComplete="new-password" />
          </Form.Item>
        )}
        <Form.Item name="real_name" label="姓名">
          <Input placeholder="真实姓名" allowClear />
        </Form.Item>
        <Form.Item name="phone" label="手机号" rules={[{ pattern: /^1\d{10}$/, message: '请输入 11 位手机号' }]}>
          <Input placeholder="手机号" allowClear maxLength={11} />
        </Form.Item>
        <Form.Item name="email" label="邮箱" rules={[{ type: 'email', message: '邮箱格式不正确' }]}>
          <Input placeholder="邮箱" allowClear />
        </Form.Item>
        <Form.Item name="department_id" label="所属部门">
          <TreeSelect
            treeData={departmentTreeOptions}
            placeholder="请选择部门"
            allowClear
            showSearch
            treeNodeFilterProp="title"
            dropdownStyle={{ maxHeight: 400, overflow: 'auto' }}
          />
        </Form.Item>
        <Form.Item name="data_scope" label="数据范围" rules={[{ required: true, message: '请选择数据范围' }]}>
          <Select options={DATA_SCOPE_OPTIONS} placeholder="请选择数据范围" />
        </Form.Item>
      </Form>
    </Modal>
  )
}

interface AssignRolesModalProps {
  user: UserItem
  roleOptions: Array<{ label: string; value: string }>
  submitting: boolean
  onCancel: () => void
  onSubmit: (roleIds: number[]) => void
}

/**
 * 分配角色弹窗（PUT /api/users/{id}/roles，AssignRolesInput.role_ids 全量替换语义）。
 * 绑定结果以 GET /api/users/:id 详情的 role_ids 为准预选——列表项不含角色绑定，
 * 直接以空集提交会静默清空用户全部角色。
 */
function AssignRolesModal({ user, roleOptions, submitting, onCancel, onSubmit }: AssignRolesModalProps) {
  const [roleIds, setRoleIds] = useState<string[]>([])

  const detailQuery = useQuery({
    queryKey: ['system', 'user', user.id],
    queryFn: () => userApi.user(user.id),
  })

  useEffect(() => {
    if (detailQuery.data) setRoleIds(detailQuery.data.role_ids ?? [])
  }, [detailQuery.data])

  return (
    <Modal
      title={`分配角色：${user.username}`}
      open
      confirmLoading={submitting}
      onCancel={onCancel}
      onOk={() => onSubmit(roleIds.map(Number))}
      okText="保存"
      okButtonProps={{ disabled: detailQuery.isPending }}
      maskClosable={false}
    >
      <Form layout="vertical">
        <Form.Item
          label="角色"
          required
          style={{ marginBottom: 8 }}
          validateStatus={detailQuery.error ? 'error' : undefined}
          help={detailQuery.error ? resolveErrorMessage(detailQuery.error) : undefined}
        >
          <Select
            mode="multiple"
            options={roleOptions}
            value={roleIds}
            onChange={setRoleIds}
            placeholder={detailQuery.isPending ? '正在加载已绑定角色…' : '请选择角色'}
            optionFilterProp="label"
            allowClear
          />
        </Form.Item>
      </Form>
      {roleOptions.length === 0 && (
        <Text type="secondary">暂无可选角色，请先到「角色管理」创建角色</Text>
      )}
    </Modal>
  )
}

interface ResetPasswordModalProps {
  user: UserItem
  submitting: boolean
  onCancel: () => void
  onSubmit: (newPassword: string) => void
}

/** 重置密码弹窗（PUT /api/users/{id}/reset-password，请求体 new_password 由 api 层对齐） */
function ResetPasswordModal({ user, submitting, onCancel, onSubmit }: ResetPasswordModalProps) {
  const [form] = Form.useForm<{ newPassword: string }>()
  return (
    <Modal
      title={`重置密码：${user.username}`}
      open
      confirmLoading={submitting}
      onCancel={onCancel}
      onOk={() => {
        void form.submit()
      }}
      okText="重置"
      maskClosable={false}
    >
      <Form form={form} layout="vertical" onFinish={(values) => onSubmit(values.newPassword)}>
        <Form.Item
          name="newPassword"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            { pattern: PASSWORD_PATTERN, message: '长度至少 8 位且需包含字母和数字' },
          ]}
        >
          <Input.Password placeholder="长度 ≥ 8 位，含字母和数字" autoComplete="new-password" />
        </Form.Item>
        <Text type="secondary">重置后请通知该用户重新登录，并建议其尽快自行修改密码。</Text>
      </Form>
    </Modal>
  )
}

type ModalState =
  | { kind: 'create' }
  | { kind: 'edit'; user: UserItem }
  | { kind: 'roles'; user: UserItem }
  | { kind: 'resetPassword'; user: UserItem }
  | null

/** 用户管理（frontend.md 系统管理：用户）：列表 + 新建/编辑/启停/分配角色/重置密码/解锁 */
export default function UserListPage() {
  const [params, setParams] = useState<UserQuery>({})
  const [modal, setModal] = useState<ModalState>(null)
  const queryClient = useQueryClient()

  const list = usePagedList<UserItem, UserQuery>({
    queryKey: ['system', 'users'],
    fetch: (q) => userApi.users(q),
    params,
  })

  // 部门（筛选项 / 表单选择 / 部门列映射）与角色（分配角色 / 角色列映射）选项数据
  const departmentsQuery = useQuery({ queryKey: ['system', 'departments'], queryFn: rbacApi.departmentTree })
  const rolesQuery = useQuery({
    queryKey: ['system', 'roles', 'options'],
    queryFn: () => rbacApi.roles({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['system', 'users'] })

  const createMutation = useMutation({
    mutationFn: (payload: UserCreatePayload) => userApi.createUser(payload),
    onSuccess: () => {
      message.success('用户已创建')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: UserUpdatePayload }) =>
      userApi.updateUser(id, payload),
    onSuccess: () => {
      message.success('用户已保存')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: string; status: UserStatus }) =>
      userApi.setUserStatus(id, status),
    onSuccess: (_data, variables) => {
      message.success(variables.status === 'ACTIVE' ? '用户已启用' : '用户已停用')
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const unlockMutation = useMutation({
    mutationFn: (id: string) => userApi.unlock(id),
    onSuccess: () => {
      message.success('账户已解锁')
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const assignRolesMutation = useMutation({
    mutationFn: ({ id, roleIds }: { id: string; roleIds: number[] }) => userApi.assignRoles(id, roleIds),
    onSuccess: () => {
      message.success('角色已更新')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const resetPasswordMutation = useMutation({
    mutationFn: ({ id, newPassword }: { id: string; newPassword: string }) =>
      userApi.resetPassword(id, newPassword),
    onSuccess: () => {
      message.success('密码已重置')
      setModal(null)
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as UserQuery)
    list.resetToFirstPage()
  }

  const handleUserFormSubmit = (values: UserFormValues) => {
    if (modal?.kind === 'edit') {
      updateMutation.mutate({
        id: modal.user.id,
        payload: {
          real_name: values.real_name,
          phone: values.phone,
          email: values.email,
          // 编辑态总是传数字：清空选择 → 0 = 显式清空部门（service_rbac.go:219-225 三态语义）
          department_id: values.department_id ? Number(values.department_id) : 0,
          data_scope: values.data_scope,
        },
      })
    } else if (modal?.kind === 'create') {
      createMutation.mutate({
        username: values.username,
        password: values.password,
        real_name: values.real_name,
        phone: values.phone,
        email: values.email,
        department_id: values.department_id ? Number(values.department_id) : undefined,
        data_scope: values.data_scope,
      })
    }
  }

  const buildRowMenu = (record: UserItem): MenuProps => ({
    items: [
      { key: 'roles', label: '分配角色' },
      { key: 'resetPassword', label: '重置密码' },
      { key: 'unlock', label: '解锁账户', disabled: !record.locked_until },
    ],
    onClick: ({ key }) => {
      if (key === 'roles') setModal({ kind: 'roles', user: record })
      if (key === 'resetPassword') setModal({ kind: 'resetPassword', user: record })
      if (key === 'unlock') unlockMutation.mutate(record.id)
    },
  })

  const departmentTree = departmentsQuery.data ?? []
  const departmentOptions = flattenDepartments(departmentTree)
  const departmentTreeOptions = toTreeOptions(departmentTree)
  const departmentNames = collectDepartmentNames(departmentTree)
  const roleNames = new Map((rolesQuery.data?.items ?? []).map((role) => [role.id, role.name]))
  const roleOptions = (rolesQuery.data?.items ?? []).map((role) => ({ label: role.name, value: role.id }))

  const columns: ColumnsType<UserItem> = [
    { title: '用户名', dataIndex: 'username', width: 120, fixed: 'left' },
    { title: '姓名', dataIndex: 'real_name', width: 100, render: (v?: string) => v ?? '-' },
    { title: '手机号', dataIndex: 'phone', width: 130, render: (v?: string) => v ?? '-' },
    {
      title: '部门',
      dataIndex: 'department_id',
      width: 130,
      render: (v?: string | null) => (v ? (departmentNames.get(v) ?? v) : '-'),
    },
    {
      title: '数据范围',
      dataIndex: 'data_scope',
      width: 110,
      render: (v: DataScope) => {
        const meta = DATA_SCOPE_META[v] ?? { label: v, semantic: 'neutral' as StatusSemantic }
        return <SfStatusTag label={meta.label} semantic={meta.semantic} />
      },
    },
    {
      // 列表项不含 role_ids（service_rbac.go GetUsers 仅 viewUser，详情才返回）——缺失时显示 '-'
      title: '角色',
      dataIndex: 'role_ids',
      width: 170,
      ellipsis: true,
      render: (roleIds?: string[]) => {
        const names = (roleIds ?? []).map((id) => roleNames.get(id) ?? id).join('、')
        return names ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: names }}>
            {names}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: UserStatus) => <SfStatusTag status={statusTagKey(v)} />,
    },
    {
      title: '锁定至',
      dataIndex: 'locked_until',
      width: 150,
      render: (v?: string | null) =>
        v ? <span style={{ whiteSpace: 'nowrap', color: 'var(--sf-danger)' }}>{formatDateTime(v)}</span> : '-',
    },
    {
      title: '最近登录',
      dataIndex: 'last_login_at',
      width: 160,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{v ? formatDateTime(v) : '-'}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 170,
      fixed: 'right',
      render: (_, record) => (
        <>
          <Button type="link" size="small" onClick={() => setModal({ kind: 'edit', user: record })}>
            编辑
          </Button>
          {record.status === 'ACTIVE' ? (
            <Popconfirm
              title="确认停用该用户？"
              description="停用后该用户将无法登录系统"
              onConfirm={() => statusMutation.mutate({ id: record.id, status: 'DISABLED' })}
            >
              <Button type="link" size="small" danger>
                停用
              </Button>
            </Popconfirm>
          ) : (
            <Button type="link" size="small" onClick={() => statusMutation.mutate({ id: record.id, status: 'ACTIVE' })}>
              启用
            </Button>
          )}
          <Dropdown menu={buildRowMenu(record)} trigger={['click']}>
            <Button type="link" size="small" icon={<MoreOutlined />} aria-label="更多操作" />
          </Dropdown>
        </>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="用户"
        subtitle="账号 / 角色 / 数据范围"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal({ kind: 'create' })}>
            新建用户
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '用户名 / 姓名 / 手机号' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'department_id', label: '部门', control: 'select', options: departmentOptions },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<UserItem>
          storageKey="system-users"
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
          emptyText="暂无用户"
          scrollX={1490}
        />
      </Card>

      {modal && (modal.kind === 'create' || modal.kind === 'edit') && (
        <UserFormModal
          editing={modal.kind === 'edit' ? modal.user : null}
          departmentTreeOptions={departmentTreeOptions}
          submitting={createMutation.isPending || updateMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={handleUserFormSubmit}
        />
      )}
      {modal?.kind === 'roles' && (
        <AssignRolesModal
          user={modal.user}
          roleOptions={roleOptions}
          submitting={assignRolesMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={(roleIds) => assignRolesMutation.mutate({ id: modal.user.id, roleIds })}
        />
      )}
      {modal?.kind === 'resetPassword' && (
        <ResetPasswordModal
          user={modal.user}
          submitting={resetPasswordMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={(newPassword) => resetPasswordMutation.mutate({ id: modal.user.id, newPassword })}
        />
      )}
    </div>
  )
}
