import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Dropdown,
  Form,
  Input,
  Modal,
  Row,
  Typography,
  message,
} from 'antd'
import { MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import type { MenuProps } from 'antd'
import { CodeCell, DateCell } from '@/components/table/cells'
import {
  masterdataApi,
  toStatusKey,
  type EnabledStatus,
  type SupplierItem,
  type SupplierQuery,
  type SupplierSavePayload,
} from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { useCrudPermissions } from '@/hooks/useCrudPermissions'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Text, Paragraph } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

interface SupplierFormValues {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  remark?: string
}

function toFormValues(record: SupplierItem): SupplierFormValues {
  return {
    code: record.code,
    name: record.name,
    contact: record.contact,
    phone: record.phone,
    email: record.email,
    address: record.address,
    remark: record.remark,
  }
}

/** 提交契约（SupplierCreateInput/SupplierUpdateInput，service_partner.go:87-105）：
 * 后端无 status 字段——创建恒 ENABLED、启停走 PUT /suppliers/:id/status，编码创建后不可改 */
function toPayload(values: SupplierFormValues): SupplierSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
    contact: values.contact,
    phone: values.phone,
    email: values.email,
    address: values.address,
    remark: values.remark,
  }
}

/** 供应商管理（/suppliers，backend-m1-plan §5.4：已产生业务记录不可删只停用，business-flow §1.4） */
export default function SupplierListPage() {
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<SupplierItem | null>(null)
  const [form] = Form.useForm<SupplierFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限（无权限则隐藏入口，后端仍会独立校验）
  const { canCreate, canUpdate, canDelete, canStatus } = useCrudPermissions('supplier')
  // 动效 #7/删除行（frontend.md §31）：行淡色反馈 / 删除行 fade→收缩——仅在 API 成败回调后触发
  const fb = useTableRowFeedback()

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<SupplierItem, SupplierQuery>({
    queryKey: ['masterdata', 'suppliers'],
    fetch: (q) => masterdataApi.suppliers.list(q),
    urlSync: true,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'suppliers'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: SupplierSavePayload) =>
      editing
        ? masterdataApi.suppliers.update(editing.id, payload)
        : masterdataApi.suppliers.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: SupplierItem['id']; status: EnabledStatus }) =>
      masterdataApi.suppliers.setStatus(id, { status }),
    onSuccess: (data, { id }) => {
      messageApi.success(data.status === 'ENABLED' ? '已启用' : '已停用')
      invalidate()
      fb.trigger(id, 'success')
    },
    onError: (error, { id }) => {
      messageApi.error(resolveErrorMessage(error))
      fb.trigger(id, 'error')
    },
  })

  const removeMutation = useMutation({
    mutationFn: (id: SupplierItem['id']) => masterdataApi.suppliers.remove(id),
    onSuccess: (_data, id) => {
      // 删除行动效：fade→收缩→过滤 DOM（refetch 成功自愈 / 失败 2.5s 兜底恢复显示）
      fb.triggerRemove(id)
      invalidate()
    },
    onError: (error, id) => {
      messageApi.error(resolveErrorMessage(error))
      fb.trigger(id, 'error')
    },
  })

  // 「更多」菜单内 停用/启用/删除 的二次确认：SfConfirm 为 Popconfirm 形态，无法锚定在点击后
  // 即关闭的 Dropdown 菜单项内——改用同语义声明式 Modal（danger ok + confirmLoading）
  const [rowConfirm, setRowConfirm] = useState<{ kind: 'toggle' | 'remove'; record: SupplierItem } | null>(null)

  /** 行内「更多」菜单（SkuListPage buildRowMenu 同构）：停用/启用/删除收进更多，操作列只留 编辑 + 更多。
   *  菜单项按权限点过滤：无 supplier:status 不出「停用/启用」、无 supplier:delete 不出「删除」；
   *  两项皆无权限时调用方不渲染「更多」按钮（见操作列 canMore 判定）。 */
  const buildRowMenu = (record: SupplierItem): MenuProps => {
    const disabling = record.status === 'ENABLED'
    const items: NonNullable<MenuProps['items']> = []
    if (canStatus) {
      items.push({ key: 'toggle', label: disabling ? '停用' : '启用', danger: disabling })
    }
    if (canDelete) {
      if (items.length > 0) items.push({ type: 'divider' })
      items.push({ key: 'remove', label: '删除', danger: true })
    }
    return {
      items,
      onClick: ({ key }) => {
        if (key === 'toggle') setRowConfirm({ kind: 'toggle', record })
        if (key === 'remove') setRowConfirm({ kind: 'remove', record })
      },
    }
  }

  const handleRowConfirmOk = () => {
    if (!rowConfirm) return
    const { kind, record } = rowConfirm
    if (kind === 'toggle') {
      statusMutation.mutate(
        { id: record.id, status: record.status === 'ENABLED' ? 'DISABLED' : 'ENABLED' },
        { onSuccess: () => setRowConfirm(null) },
      )
    } else {
      removeMutation.mutate(record.id, { onSuccess: () => setRowConfirm(null) })
    }
  }

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: SupplierItem) => {
    saveMutation.reset()
    setEditing(record)
    setModalOpen(true)
    form.setFieldsValue(toFormValues(record))
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => saveMutation.mutate(toPayload(values)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  /** 「更多」是否可出（停用/启用 或 删除 至少一项有权限） */
  const canMore = canStatus || canDelete

  /** 操作列按权限装配：无编辑权限不出「编辑」，无状态/删除权限不出「更多」 */
  const actionColumn: ColumnsType<SupplierItem>[number] = {
    title: '操作',
    key: 'actions',
    fixed: 'right',
    width: 150,
    render: (_: unknown, record: SupplierItem) => (
      <span style={{ whiteSpace: 'nowrap' }}>
        {canUpdate && (
          <Button type="link" size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
        )}
        {canMore && (
          <Dropdown menu={buildRowMenu(record)} trigger={['click']}>
            <Button type="link" size="small" aria-label="更多操作">
              更多<MoreOutlined style={{ marginLeft: 2 }} />
            </Button>
          </Dropdown>
        )}
      </span>
    ),
  }

  const columns: ColumnsType<SupplierItem> = [
    {
      title: '供应商编码',
      dataIndex: 'code',
      width: 130,
      fixed: 'left',
      render: (v: string) => <CodeCell value={v} label="供应商编码" />,
    },
    {
      title: '供应商名称',
      dataIndex: 'name',
      width: 180,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    { title: '联系人', dataIndex: 'contact', width: 110, render: (v?: string) => v ?? '-' },
    {
      title: '联系电话',
      dataIndex: 'phone',
      width: 140,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{v ?? '-'}</span>,
    },
    { title: '邮箱', dataIndex: 'email', width: 180, render: (v?: string) => v ?? '-' },
    {
      title: '地址',
      dataIndex: 'address',
      width: 200,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 150,
      ellipsis: true,
      render: (v?: string) =>
        v ? <Text style={{ maxWidth: 150 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: EnabledStatus) => <SfStatusTag status={toStatusKey(v)} />,
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      render: (v?: string) => <DateCell value={v} />,
    },
    ...(canUpdate || canMore ? [actionColumn] : []),
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="供应商管理"
        subtitle="供应商档案与联系信息（business-flow §1.4）"
        extra={
          canCreate ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              新建供应商
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '供应商编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<SupplierItem>
          storageKey="masterdata-suppliers"
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
          emptyText={canCreate ? '暂无供应商，点击右上角「新建供应商」创建' : '暂无供应商'}
          emptyAction={
            canCreate ? (
              <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate}>
                新建供应商
              </Button>
            ) : undefined
          }
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          removingRowKeys={fb.removingRowKeys}
          scrollX={1500}
        />
      </Card>

      <Modal
        title={editing ? '编辑供应商' : '新建供应商'}
        open={modalOpen}
        width={640}
        forceRender
        confirmLoading={saveMutation.isPending}
        okText={editing ? '保存' : '创建'}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        {saveMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(saveMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form<SupplierFormValues> form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="code"
                label="供应商编码"
                rules={[{ required: true, message: '请输入供应商编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={64} disabled={editing !== null} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="供应商名称" rules={[{ required: true, message: '请输入供应商名称' }]}>
                <Input placeholder="请输入供应商名称" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="contact" label="联系人">
                <Input placeholder="请输入联系人" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="phone" label="联系电话">
                <Input placeholder="请输入联系电话" maxLength={32} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="email"
                label="邮箱"
                rules={[{ type: 'email', message: '邮箱格式不正确' }]}
              >
                <Input placeholder="请输入邮箱" maxLength={128} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="address" label="地址">
                <Input.TextArea rows={2} placeholder="请输入地址" maxLength={255} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="remark" label="备注">
                <Input.TextArea rows={2} placeholder="请输入备注" maxLength={500} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>

      {/* 「更多」菜单 停用/启用/删除 的二次确认（文案与原行内 SfConfirm 逐字一致；
          危险动作 danger ok + confirmLoading 防重复） */}
      <Modal
        title={
          rowConfirm?.kind === 'remove'
            ? '确认删除该供应商？'
            : rowConfirm?.record.status === 'ENABLED'
              ? '确认停用该供应商？'
              : '确认启用该供应商？'
        }
        open={rowConfirm !== null}
        width={440}
        confirmLoading={rowConfirm?.kind === 'remove' ? removeMutation.isPending : statusMutation.isPending}
        okText={
          rowConfirm?.kind === 'remove' ? '删除' : rowConfirm?.record.status === 'ENABLED' ? '停用' : '启用'
        }
        okButtonProps={{
          danger: rowConfirm?.kind === 'remove' || rowConfirm?.record.status === 'ENABLED',
        }}
        onOk={handleRowConfirmOk}
        onCancel={() => setRowConfirm(null)}
      >
        <Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {rowConfirm?.kind === 'remove'
            ? '已产生采购业务的供应商后端将拒绝删除，建议改用停用。'
            : rowConfirm?.record.status === 'ENABLED'
              ? '停用后不可再被新采购业务引用，已有业务记录不受影响。'
              : '启用后供应商可重新参与采购业务。'}
        </Paragraph>
      </Modal>
    </div>
  )
}
