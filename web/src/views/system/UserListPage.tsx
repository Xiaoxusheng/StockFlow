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
import { PASSWORD_RULE } from '@/api/auth'
import { fetchAllPaged, MAX_PAGE_SIZE, rbacApi, type DepartmentNode } from '@/api/rbac'
import { warehouseApi, type WarehouseItem } from '@/api/warehouse'
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
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
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

/** 用户名规则（后端唯一依据 usernameRe，internal/auth/service_rbac.go:22：
 * 2-64 位，字母开头，可含数字/下划线/点/连字符） */
const USERNAME_PATTERN = /^[A-Za-z][A-Za-z0-9_.-]{1,63}$/

/** 强密码策略：直接使用 PASSWORD_RULE 单一来源（@/api/auth，顶部 import），
 * 精确镜像后端 ValidatePasswordStrength（internal/auth/password.go）——Unicode 字母
 * （unicode.IsLetter）/数字、代码点 ≥ 8、UTF-8 字节 ≤ 72；此前本地 ASCII 正则已
 * 收敛删除，避免同一根因两处再漂移 */

/** 手机号宽松格式（后端唯一依据 phoneRe，internal/auth/service_rbac.go:1290：
 * 5-32 位，可 + 前缀，数字与连字符——允许 +86 / 座机等形态） */
const PHONE_PATTERN = /^\+?[0-9][0-9-]{4,31}$/

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
  /** 绑定仓库（ID 字符串形态，提交转数字）；仅数据范围=指定仓库时生效 */
  warehouse_ids?: string[]
}

interface UserFormModalProps {
  editing: UserItem | null
  departmentTreeOptions: DeptTreeOption[]
  /** 仓库选项（数据范围=指定仓库时的绑定候选；全量拉取自 /api/warehouses） */
  warehouseOptions: Array<{ label: string; value: string }>
  warehouseLoading: boolean
  submitting: boolean
  onCancel: () => void
  onSubmit: (values: UserFormValues) => void
}

/**
 * 新建 / 编辑用户弹窗（新建含初始密码，编辑不允许改用户名与密码）。
 * 数据范围=指定仓库时必选绑定仓库（后端 service_rbac.go:105 创建强制非空，
 * 编辑路径以表单显式提交非空 warehouse_ids 规避「改 scope 不带绑定 → 静默空仓库集」）。
 */
function UserFormModal({
  editing,
  departmentTreeOptions,
  warehouseOptions,
  warehouseLoading,
  submitting,
  onCancel,
  onSubmit,
}: UserFormModalProps) {
  const isEdit = editing !== null
  const [form] = Form.useForm<UserFormValues>()
  const dataScope = Form.useWatch('data_scope', form)

  // 编辑态绑定仓库仅详情返回（service_rbac.go GetUserDetail）——拉详情回填预选
  const detailQuery = useQuery({
    queryKey: ['system', 'user', editing?.id],
    queryFn: () => userApi.user(editing!.id),
    enabled: isEdit,
  })

  useEffect(() => {
    if (detailQuery.data?.warehouse_ids) {
      form.setFieldsValue({ warehouse_ids: detailQuery.data.warehouse_ids.map(String) })
    }
  }, [detailQuery.data, form])

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
      mask={{ closable: false }}
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
            { pattern: USERNAME_PATTERN, message: '2–64 位，字母开头，可含数字、下划线、点或连字符' },
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
              PASSWORD_RULE,
            ]}
            extra="首次登录将要求修改密码"
          >
            <Input.Password placeholder="长度 8–72 位，含字母和数字" autoComplete="new-password" />
          </Form.Item>
        )}
        <Form.Item name="real_name" label="姓名">
          <Input placeholder="真实姓名" allowClear />
        </Form.Item>
        <Form.Item
          name="phone"
          label="手机号"
          rules={[{ pattern: PHONE_PATTERN, message: '5–32 位，可 + 开头、含数字与连字符（支持 +86 / 座机）' }]}
        >
          <Input placeholder="手机号" allowClear maxLength={32} />
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
        {dataScope === 'SPECIFIED_WAREHOUSE' && (
          <Form.Item
            name="warehouse_ids"
            label="绑定仓库"
            rules={[{ required: true, type: 'array', message: '数据范围为指定仓库时必须绑定至少一个仓库' }]}
            validateStatus={isEdit && detailQuery.error ? 'error' : undefined}
            extra={
              isEdit && detailQuery.error
                ? `已绑定仓库加载失败：${resolveErrorMessage(detailQuery.error)}，请重新选择后保存`
                : undefined
            }
          >
            <Select
              mode="multiple"
              loading={warehouseLoading}
              options={warehouseOptions}
              placeholder={warehouseLoading ? '正在加载仓库…' : '选择该用户可见的仓库（可多选）'}
              optionFilterProp="label"
              allowClear
            />
          </Form.Item>
        )}
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
      mask={{ closable: false }}
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
      mask={{ closable: false }}
    >
      <Form form={form} layout="vertical" onFinish={(values) => onSubmit(values.newPassword)}>
        <Form.Item
          name="newPassword"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            PASSWORD_RULE,
          ]}
        >
          <Input.Password placeholder="长度 8–72 位，含字母和数字" autoComplete="new-password" />
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

  // 行反馈动效（frontend.md §31 #7）：启停/解锁先 API 后反馈，对应行淡色底 480ms 自动回落
  const fb = useTableRowFeedback()

  // 部门（筛选项 / 表单选择 / 部门列映射）、角色（分配角色 / 角色列映射）与
  // 仓库（数据范围=指定仓库的绑定候选）选项数据——均一次取全，防单页 100 静默截断
  const departmentsQuery = useQuery({ queryKey: ['system', 'departments'], queryFn: rbacApi.departmentTree })
  const rolesQuery = useQuery({
    queryKey: ['system', 'roles', 'all'],
    queryFn: rbacApi.rolesAll,
  })
  const warehousesQuery = useQuery({
    queryKey: ['warehouse', 'options'],
    queryFn: () =>
      fetchAllPaged((page) => warehouseApi.list({ page, pageSize: MAX_PAGE_SIZE })),
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
      fb.trigger(variables.id)
      message.success(variables.status === 'ACTIVE' ? '用户已启用' : '用户已停用')
      void invalidate()
    },
    onError: (error, variables) => {
      fb.trigger(variables.id, 'error')
      message.error(resolveErrorMessage(error))
    },
  })

  const unlockMutation = useMutation({
    mutationFn: (id: string) => userApi.unlock(id),
    onSuccess: (_data, id) => {
      fb.trigger(id)
      message.success('账户已解锁')
      void invalidate()
    },
    onError: (error, id) => {
      fb.trigger(id, 'error')
      message.error(resolveErrorMessage(error))
    },
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
      const payload: UserUpdatePayload = {
        real_name: values.real_name,
        phone: values.phone,
        email: values.email,
        // 编辑态总是传数字：清空选择 → 0 = 显式清空部门（service_rbac.go:219-225 三态语义）
        department_id: values.department_id ? Number(values.department_id) : 0,
        data_scope: values.data_scope,
      }
      // 指定仓库：显式提交全量绑定（warehouse_ids 为全量替换语义，service_rbac.go:235-242）；
      // 其他范围不传该字段，避免无意义的绑定重写
      if (values.data_scope === 'SPECIFIED_WAREHOUSE' && values.warehouse_ids) {
        payload.warehouse_ids = values.warehouse_ids.map(Number)
      }
      updateMutation.mutate({ id: modal.user.id, payload })
    } else if (modal?.kind === 'create') {
      createMutation.mutate({
        username: values.username,
        password: values.password,
        real_name: values.real_name,
        phone: values.phone,
        email: values.email,
        department_id: values.department_id ? Number(values.department_id) : undefined,
        data_scope: values.data_scope,
        // 指定仓库必绑（后端 service_rbac.go:105 强制非空；表单 required 已拦截空集）
        ...(values.data_scope === 'SPECIFIED_WAREHOUSE' && values.warehouse_ids
          ? { warehouse_ids: values.warehouse_ids.map(Number) }
          : {}),
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
  const roles = rolesQuery.data ?? []
  const roleNames = new Map(roles.map((role) => [role.id, role.name]))
  const roleOptions = roles.map((role) => ({ label: role.name, value: role.id }))
  const warehouseOptions = (warehousesQuery.data ?? []).map((w: WarehouseItem) => ({
    label: `${w.name}（${w.code}）`,
    value: String(w.id),
  }))

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
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          emptyText="暂无用户"
          scrollX={1490}
        />
      </Card>

      {modal && (modal.kind === 'create' || modal.kind === 'edit') && (
        <UserFormModal
          editing={modal.kind === 'edit' ? modal.user : null}
          departmentTreeOptions={departmentTreeOptions}
          warehouseOptions={warehouseOptions}
          warehouseLoading={warehousesQuery.isPending}
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
