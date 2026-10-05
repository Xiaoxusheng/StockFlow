import { useEffect, useMemo, useState } from 'react'
import { Button, Card, Dropdown, Form, Input, Modal, TreeSelect, Typography, message } from 'antd'
import { MoreOutlined, PlusOutlined } from '@ant-design/icons'
import type { MenuProps } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { resolveErrorMessage } from '@/api/client'
import { rbacApi, type DepartmentNode } from '@/api/rbac'
import type { OnOffStatus } from '@/api/user'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'

/** departments 状态枚举为 ENABLED/DISABLED（迁移 000001 chk_departments_status） */
function statusTagKey(status: OnOffStatus): string {
  return status === 'ENABLED' ? 'enabled' : 'disabled'
}

interface DeptTreeOption {
  value: string
  title: string
  children?: DeptTreeOption[]
}

/** 部门树 → TreeSelect 数据；excludeId 用于编辑时排除自身子树，防止把自己挂到后代下 */
function toTreeOptions(nodes: DepartmentNode[], excludeId?: string): DeptTreeOption[] {
  return nodes
    .filter((node) => node.id !== excludeId)
    .map((node) => {
      const children = toTreeOptions(node.children ?? [], excludeId)
      return {
        value: node.id,
        title: `${node.name}（${node.code}）`,
        children: children.length > 0 ? children : undefined,
      }
    })
}

/** 收集所有存在子级的节点 key（默认展开用） */
function collectParentKeys(nodes: DepartmentNode[]): string[] {
  return nodes.flatMap((node) => {
    const keys = node.children && node.children.length > 0 ? [node.id, ...collectParentKeys(node.children)] : []
    return keys
  })
}

interface DepartmentFormValues {
  parentId?: string
  code: string
  name: string
}

interface DepartmentFormModalProps {
  editing: DepartmentNode | null
  /** 新建子部门时的父级预置 */
  parent: DepartmentNode | null
  treeOptions: DeptTreeOption[]
  submitting: boolean
  onCancel: () => void
  onSubmit: (values: DepartmentFormValues) => void
}

/**
 * 新建 / 编辑部门弹窗（M1 契约：parent_id + code + name；无删除，走停用）。
 * 更新入参仅 parent_id/name（DeptUpdateInput，code 不可变——service_rbac.go:809-812）。
 */
function DepartmentFormModal({ editing, parent, treeOptions, submitting, onCancel, onSubmit }: DepartmentFormModalProps) {
  const isEdit = editing !== null
  const [form] = Form.useForm<DepartmentFormValues>()
  return (
    <Modal
      title={isEdit ? `编辑部门：${editing.name}` : parent ? `新增子部门：${parent.name}` : '新增部门'}
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
          isEdit
            ? {
                parentId: editing.parent_id ?? undefined,
                code: editing.code,
                name: editing.name,
              }
            : parent
              ? { parentId: parent.id }
              : undefined
        }
      >
        <Form.Item name="parentId" label="上级部门">
          <TreeSelect
            treeData={treeOptions}
            placeholder="不选择则作为根部门"
            allowClear
            showSearch
            treeNodeFilterProp="title"
            treeDefaultExpandAll
            dropdownStyle={{ maxHeight: 400, overflow: 'auto' }}
          />
        </Form.Item>
        <Form.Item
          name="code"
          label="部门编码"
          rules={[
            { required: true, message: '请输入部门编码' },
            // 对齐后端 deptCodeRe（internal/auth/service_rbac.go:24）：大写字母开头，
            // 2-64 位大写字母/数字/下划线/连字符——小写或数字开头后端必 400
            { pattern: /^[A-Z][A-Z0-9_-]{1,63}$/, message: '2–64 位大写字母、数字、下划线或中划线，以大写字母开头' },
          ]}
          extra="编码全局唯一（大写字母开头，如 WH-NORTH），创建后不可修改"
        >
          <Input disabled={isEdit} placeholder="如 WH-NORTH" autoComplete="off" />
        </Form.Item>
        <Form.Item name="name" label="部门名称" rules={[{ required: true, message: '请输入部门名称' }]}>
          {/* 后端 maxNameLen=64（service_rbac.go:27） */}
          <Input placeholder="如 华北仓运营部" allowClear maxLength={64} />
        </Form.Item>
      </Form>
    </Modal>
  )
}

type ModalState = { kind: 'create'; parent: DepartmentNode | null } | { kind: 'edit'; dept: DepartmentNode } | null

/** 部门管理（frontend.md 系统管理：部门）：树形展示 + 新增根/子部门、编辑、启停 */
export default function DepartmentPage() {
  const [modal, setModal] = useState<ModalState>(null)
  const [expandedKeys, setExpandedKeys] = useState<string[]>([])
  const [pagination, setPagination] = useState({ current: 1, pageSize: 20 })
  const queryClient = useQueryClient()

  // 全量组树一次加载（api 层循环拉取分页信封后按 parent_id 组树），表格内做树形展示
  const treeQuery = useQuery({ queryKey: ['system', 'departments'], queryFn: rbacApi.departmentTree })
  const tree = useMemo(() => treeQuery.data ?? [], [treeQuery.data])

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['system', 'departments'] })

  const createMutation = useMutation({
    mutationFn: (values: DepartmentFormValues) =>
      rbacApi.createDepartment({
        parent_id: values.parentId ? Number(values.parentId) : undefined,
        code: values.code,
        name: values.name,
      }),
    onSuccess: () => {
      message.success('部门已创建')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, values }: { id: string; values: DepartmentFormValues }) =>
      rbacApi.updateDepartment(id, {
        // 清空上级选择 → 0 = 提升为顶级（parent_id 三态语义，service_rbac.go:827-868）
        parent_id: values.parentId ? Number(values.parentId) : 0,
        name: values.name,
      }),
    onSuccess: () => {
      message.success('部门已保存')
      setModal(null)
      void invalidate()
    },
    onError: (error) => message.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: string; status: OnOffStatus }) => rbacApi.setDepartmentStatus(id, status),
    onSuccess: (_data, variables) => {
      fb.trigger(variables.id)
      message.success(variables.status === 'ENABLED' ? '部门已启用' : '部门已停用')
      void invalidate()
    },
    onError: (error, variables) => {
      fb.trigger(variables.id, 'error')
      message.error(resolveErrorMessage(error))
    },
  })

  // 「更多」菜单内 停用/启用 的二次确认：Popconfirm 形态无法锚定在 Dropdown 菜单项内——
  // 改用同语义声明式 Modal（danger ok + confirmLoading），文案逐字保留
  const [rowConfirm, setRowConfirm] = useState<DepartmentNode | null>(null)
  // 行反馈动效（frontend.md §31 #7）：部门启停先 API 后反馈，对应行淡色底 480ms 自动回落
  const fb = useTableRowFeedback()
  const handleRowConfirmOk = () => {
    if (!rowConfirm) return
    statusMutation.mutate(
      { id: rowConfirm.id, status: rowConfirm.status === 'ENABLED' ? 'DISABLED' : 'ENABLED' },
      { onSuccess: () => setRowConfirm(null) },
    )
  }

  const parentKeys = useMemo(() => collectParentKeys(tree), [tree])
  useEffect(() => {
    setExpandedKeys(parentKeys)
  }, [parentKeys])

  const treeOptions = toTreeOptions(tree, modal?.kind === 'edit' ? modal.dept.id : undefined)

  const columns: ColumnsType<DepartmentNode> = [
    { title: '部门名称', dataIndex: 'name', width: 220, fixed: 'left' },
    { title: '部门编码', dataIndex: 'code', width: 160 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: OnOffStatus) => <SfStatusTag status={statusTagKey(v)} />,
    },
    {
      title: '操作',
      key: 'actions',
      width: 220,
      fixed: 'right',
      render: (_, record) => {
        const disabling = record.status === 'ENABLED'
        const rowMenu: MenuProps = {
          items: [{ key: 'toggle', label: disabling ? '停用' : '启用', danger: disabling }],
          onClick: ({ key }) => {
            if (key === 'toggle') setRowConfirm(record)
          },
        }
        return (
          <>
            <Button type="link" size="small" onClick={() => setModal({ kind: 'create', parent: record })}>
              新增子部门
            </Button>
            <Button type="link" size="small" onClick={() => setModal({ kind: 'edit', dept: record })}>
              编辑
            </Button>
            <Dropdown menu={rowMenu} trigger={['click']}>
              <Button type="link" size="small" aria-label="更多操作">
                更多<MoreOutlined style={{ marginLeft: 2 }} />
              </Button>
            </Dropdown>
          </>
        )
      },
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="部门"
        subtitle="组织架构（树形）"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal({ kind: 'create', parent: null })}>
            新增部门
          </Button>
        }
      />
      <Card size="small">
        <SfTable<DepartmentNode>
          storageKey="system-departments"
          rowKey="id"
          columns={columns}
          dataSource={tree}
          loading={treeQuery.isFetching}
          error={treeQuery.error}
          onRetry={treeQuery.refetch}
          onRefresh={treeQuery.refetch}
          pagination={{ current: pagination.current, pageSize: pagination.pageSize }}
          total={tree.length}
          onPageChange={(page, pageSize) => setPagination({ current: page, pageSize })}
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          emptyText="暂无部门"
          scrollX={690}
          expandable={{
            expandedRowKeys: expandedKeys,
            onExpand: (expanded, record) => {
              const key = record.id
              setExpandedKeys((prev) => (expanded ? [...prev, key] : prev.filter((k) => k !== key)))
            },
          }}
        />
      </Card>

      {modal && (
        <DepartmentFormModal
          editing={modal.kind === 'edit' ? modal.dept : null}
          parent={modal.kind === 'create' ? modal.parent : null}
          treeOptions={treeOptions}
          submitting={createMutation.isPending || updateMutation.isPending}
          onCancel={() => setModal(null)}
          onSubmit={(values) => {
            if (modal.kind === 'edit') updateMutation.mutate({ id: modal.dept.id, values })
            else createMutation.mutate(values)
          }}
        />
      )}

      {/* 「更多 → 停用/启用」的二次确认（文案与原行内 Popconfirm 逐字一致） */}
      <Modal
        title={rowConfirm?.status === 'ENABLED' ? '确认停用该部门？' : '确认启用该部门？'}
        open={rowConfirm !== null}
        width={440}
        confirmLoading={statusMutation.isPending}
        okText={rowConfirm?.status === 'ENABLED' ? '停用' : '启用'}
        okButtonProps={{ danger: rowConfirm?.status === 'ENABLED' }}
        onOk={handleRowConfirmOk}
        onCancel={() => setRowConfirm(null)}
      >
        <Typography.Text type="secondary">
          {rowConfirm?.status === 'ENABLED' ? '停用后该部门不可再被新用户选择' : '启用后该部门恢复可选。'}
        </Typography.Text>
      </Modal>
    </div>
  )
}
