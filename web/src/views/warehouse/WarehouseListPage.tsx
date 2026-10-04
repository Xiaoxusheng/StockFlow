import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Spin,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  RESOURCE_STATUS_OPTIONS,
  WAREHOUSE_TYPE_LABEL,
  toStatusKey,
  warehouseApi,
  type ResourceStatus,
  type WarehouseItem,
  type WarehousePayload,
  type WarehouseQuery,
  type WarehouseSpaceId,
} from '@/api/warehouse'
import { buildUserNameMap, fetchUserOptions, idKey } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const WAREHOUSE_TYPE_OPTIONS = Object.entries(WAREHOUSE_TYPE_LABEL).map(([value, label]) => ({
  label,
  value,
}))

const SEARCH_FIELDS: SearchField[] = [
  { name: 'keyword', label: '关键词', control: 'input', placeholder: '编码 / 名称 / 地址' },
  { name: 'type', label: '仓库类型', control: 'select', options: WAREHOUSE_TYPE_OPTIONS },
  { name: 'status', label: '状态', control: 'select', options: RESOURCE_STATUS_OPTIONS },
]

const COLUMNS: ColumnsType<WarehouseItem> = [
  { title: '编码', dataIndex: 'code', width: 120, fixed: 'left' },
  { title: '名称', dataIndex: 'name', width: 160, ellipsis: true },
  {
    title: '类型',
    dataIndex: 'type',
    width: 90,
    render: (v?: string) => (v ? (WAREHOUSE_TYPE_LABEL[v] ?? v) : '-'),
  },
  {
    title: '面积(㎡)',
    dataIndex: 'area',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '容量',
    dataIndex: 'capacity',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '地址', dataIndex: 'address', width: 200, ellipsis: true, render: (v?: string) => v ?? '-' },
  { title: '联系人', dataIndex: 'contact', width: 90, render: (v?: string) => v ?? '-' },
  { title: '电话', dataIndex: 'phone', width: 130, render: (v?: string) => v ?? '-' },
]

/** 尾列：仓管员列之后拼接（仓管员列依赖用户 options 映射，见组件内） */
const TAIL_COLUMNS: ColumnsType<WarehouseItem> = [
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: string) => <SfStatusTag status={toStatusKey(v)} />,
  },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 仓管员展示（manager_user_id，dto.go:24 database.ID 字符串）：0/空 = 未指定；
 *  用户映射失败降级 #ID，不造假（api/options.ts 降级口径，DeviceListPage 同款） */
function renderManagerRef(value: WarehouseSpaceId | undefined, names: Map<string, string> | undefined): string {
  if (!value || value === '0' || value === 0) return '-'
  const key = idKey(value)
  return names?.get(key) ?? `#${key}`
}

interface WarehouseFormValues {
  code: string
  name: string
  type?: string
  address?: string
  contact?: string
  phone?: string
  /** 用户 ID 字符串（选项 value），提交时转 number（dto.go:151 int64） */
  manager_user_id?: string
  area?: number
  capacity?: number
}

/** 仓库管理（frontend.md §27 仓库；M1 契约：/api/warehouses CRUD + 启停/删除/详情） */
export default function WarehouseListPage() {
  const [params, setParams] = useState<WarehouseQuery>({})
  const list = usePagedList<WarehouseItem, WarehouseQuery>({
    queryKey: ['warehouse', 'warehouses'],
    fetch: (q) => warehouseApi.list(q),
    params,
  })

  // 仓管员 options（GET /api/users，后端权限点 auth:user:list）：拉取失败降级
  // 空下拉 / #ID 展示，不阻塞页面（api/options.ts 约定，DeviceListPage 同款）
  const users = useQuery({
    queryKey: ['warehouse', 'options', 'users'],
    queryFn: fetchUserOptions,
  })
  const userNames = useMemo(() => buildUserNameMap(users.data ?? []), [users.data])
  const userOptions = useMemo(
    () =>
      (users.data ?? []).map((u) => ({
        label: u.real_name ? `${u.real_name}（${u.username}）` : u.username,
        value: String(u.id),
      })),
    [users.data],
  )

  const [form] = Form.useForm<WarehouseFormValues>()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<WarehouseItem | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [formError, setFormError] = useState<unknown>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [togglingId, setTogglingId] = useState<number | string | null>(null)
  const [removingId, setRemovingId] = useState<number | string | null>(null)

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as WarehouseQuery)
    list.resetToFirstPage()
  }

  const openCreate = () => {
    setEditing(null)
    setFormError(null)
    form.resetFields()
    form.setFieldsValue({ type: 'NORMAL' })
    setDrawerOpen(true)
  }

  /** 表单回填（列表行 / 详情单条共用一份字段集） */
  const fillForm = (record: WarehouseItem) => {
    form.setFieldsValue({
      code: record.code,
      name: record.name,
      type: record.type,
      address: record.address,
      contact: record.contact,
      phone: record.phone,
      area: record.area,
      capacity: record.capacity,
      manager_user_id: record.manager_user_id && record.manager_user_id !== '0'
        ? String(record.manager_user_id)
        : undefined,
    })
  }

  const refreshDetail = async (id: WarehouseSpaceId) => {
    setDetailLoading(true)
    try {
      const item = await warehouseApi.detail(id)
      setEditing(item)
      fillForm(item)
      setFormError(null)
    } catch (err) {
      // 详情刷新失败不阻断编辑：保留列表行数据，以非阻断 Alert 提示（呈错误态不静默）
      setFormError(err)
    } finally {
      setDetailLoading(false)
    }
  }

  const openEdit = (record: WarehouseItem) => {
    setEditing(record)
    setFormError(null)
    form.resetFields()
    fillForm(record)
    setDrawerOpen(true)
    // 详情局部刷新：以 GET /api/warehouses/:id 最新数据覆盖列表行（列表行可能过期）
    void refreshDetail(record.id)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null)
    if (!values) return
    setSubmitting(true)
    setFormError(null)
    // 所见即所得：未选提交 0（CreateInput int64 0 = 无仓管员；UpdateInput 指针显式 0
    // 即清空，service_warehouse.go:169-173；负数被 service :55 拒绝）
    const payload: WarehousePayload = {
      ...values,
      manager_user_id: values.manager_user_id ? Number(values.manager_user_id) : 0,
    }
    try {
      if (editing) {
        await warehouseApi.update(editing.id, payload)
      } else {
        await warehouseApi.create(payload)
      }
      setDrawerOpen(false)
      await list.refetch()
    } catch (err) {
      setFormError(err)
    } finally {
      setSubmitting(false)
    }
  }

  const handleToggleStatus = async (record: WarehouseItem) => {
    const next: ResourceStatus = record.status?.toUpperCase() === 'ENABLED' ? 'DISABLED' : 'ENABLED'
    setTogglingId(record.id)
    setActionError(null)
    try {
      await warehouseApi.setStatus(record.id, next)
      await list.refetch()
    } catch (err) {
      setActionError(err)
    } finally {
      setTogglingId(null)
    }
  }

  const handleRemove = async (record: WarehouseItem) => {
    setRemovingId(record.id)
    setActionError(null)
    try {
      await warehouseApi.remove(record.id)
      await list.refetch()
    } catch (err) {
      setActionError(err)
    } finally {
      setRemovingId(null)
    }
  }

  const columns: ColumnsType<WarehouseItem> = [
    ...COLUMNS,
    {
      title: '仓管员',
      dataIndex: 'manager_user_id',
      width: 110,
      ellipsis: true,
      render: (value?: WarehouseSpaceId) => renderManagerRef(value, userNames),
    },
    ...TAIL_COLUMNS,
    {
      title: '操作',
      key: 'actions',
      width: 130,
      fixed: 'right',
      render: (_, record) => {
        const enabled = record.status?.toUpperCase() === 'ENABLED'
        return (
          <Space size={0}>
            <Button type="link" size="small" style={{ paddingInline: 4 }} onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Popconfirm
              title={enabled ? '确认停用该仓库？' : '确认启用该仓库？'}
              description={enabled ? '下属库区/货架/库位将级联停用' : undefined}
              onConfirm={() => void handleToggleStatus(record)}
            >
              <Button type="link" size="small" style={{ paddingInline: 4 }} loading={togglingId === record.id}>
                {enabled ? '停用' : '启用'}
              </Button>
            </Popconfirm>
            <Popconfirm
              title="确认删除该仓库？"
              description="已有业务数据时后端将拒绝删除（级联校验）"
              okButtonProps={{ danger: true }}
              onConfirm={() => void handleRemove(record)}
            >
              <Button type="link" size="small" danger style={{ paddingInline: 4 }} loading={removingId === record.id}>
                删除
              </Button>
            </Popconfirm>
          </Space>
        )
      },
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="仓库管理"
        subtitle="仓库 → 库区 → 货架 → 库位 四级结构的第一级"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建仓库
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm fields={SEARCH_FIELDS} onSearch={handleSearch} />
        {actionError !== null && (
          <Alert
            type="error"
            showIcon
            closable
            style={{ marginBottom: 12 }}
            message={resolveErrorMessage(actionError)}
            onClose={() => setActionError(null)}
          />
        )}
        <SfTable<WarehouseItem>
          storageKey="warehouse-warehouses"
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
          emptyText="当前筛选条件下没有仓库"
          scrollX={1490}
        />
      </Card>

      <Drawer
        title={editing ? '编辑仓库' : '新建仓库'}
        width={440}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        extra={
          <Space>
            <Button onClick={() => setDrawerOpen(false)}>取消</Button>
            <Button type="primary" loading={submitting} onClick={() => void handleSubmit()}>
              保存
            </Button>
          </Space>
        }
      >
        {formError !== null && (
          <Alert
            type="error"
            showIcon
            closable
            style={{ marginBottom: 16 }}
            message={resolveErrorMessage(formError)}
            onClose={() => setFormError(null)}
          />
        )}
        <Spin spinning={detailLoading}>
          <Form form={form} layout="vertical" initialValues={{ type: 'NORMAL' }}>
            <Form.Item
              name="code"
              label="仓库编码"
              rules={[{ required: true, message: '请输入仓库编码' }]}
            >
              <Input placeholder="如 WH-001" maxLength={64} />
            </Form.Item>
            <Form.Item
              name="name"
              label="仓库名称"
              rules={[{ required: true, message: '请输入仓库名称' }]}
            >
              <Input maxLength={255} />
            </Form.Item>
            <Form.Item name="type" label="仓库类型">
              <Select options={WAREHOUSE_TYPE_OPTIONS} allowClear />
            </Form.Item>
            <Form.Item name="address" label="地址">
              <Input maxLength={512} />
            </Form.Item>
            <Form.Item name="contact" label="联系人">
              <Input maxLength={64} />
            </Form.Item>
            <Form.Item name="phone" label="电话">
              <Input maxLength={32} />
            </Form.Item>
            <Form.Item name="manager_user_id" label="仓管员">
              <Select
                showSearch
                allowClear
                optionFilterProp="label"
                loading={users.isFetching}
                options={userOptions}
                placeholder="选择仓管员（可选）"
              />
            </Form.Item>
            <Form.Item name="area" label="面积(㎡)">
              <InputNumber style={{ width: '100%' }} min={0} precision={2} />
            </Form.Item>
            <Form.Item name="capacity" label="容量">
              <InputNumber style={{ width: '100%' }} min={0} precision={2} />
            </Form.Item>
          </Form>
        </Spin>
      </Drawer>
    </div>
  )
}
