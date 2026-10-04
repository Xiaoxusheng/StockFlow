import { useState } from 'react'
import { Alert, Button, Card, Drawer, Form, Input, InputNumber, Popconfirm, Select, Space } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  RESOURCE_STATUS_OPTIONS,
  ZONE_TYPE_LABEL,
  toStatusKey,
  warehouseApi,
  zoneApi,
  type ResourceStatus,
  type ZoneItem,
  type ZoneQuery,
  type ZoneUpdatePayload,
} from '@/api/warehouse'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const ZONE_TYPE_OPTIONS = Object.entries(ZONE_TYPE_LABEL).map(([value, label]) => ({ label, value }))

const SEARCH_FIELDS: SearchField[] = [
  { name: 'keyword', label: '关键词', control: 'input', placeholder: '库区编码 / 名称' },
  { name: 'warehouseId', label: '所属仓库', control: 'select' },
  { name: 'zoneType', label: '库区类型', control: 'select', options: ZONE_TYPE_OPTIONS },
  { name: 'status', label: '状态', control: 'select', options: RESOURCE_STATUS_OPTIONS },
]

const COLUMNS: ColumnsType<ZoneItem> = [
  { title: '库区编码', dataIndex: 'code', width: 130, fixed: 'left' },
  { title: '名称', dataIndex: 'name', width: 180, ellipsis: true },
  {
    title: '类型',
    dataIndex: 'zone_type',
    width: 110,
    render: (v?: string) => (v ? (ZONE_TYPE_LABEL[v] ?? v) : '-'),
  },
  {
    title: '容量',
    dataIndex: 'capacity',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
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

/** 表单值与 ZonePayload 同构：warehouse_id 以 number 提交（dto.go:170 binding:required，
 *  传字符串会被后端 int64 拒绝 400） */
interface ZoneFormValues {
  warehouse_id: number
  code: string
  name: string
  zone_type?: string
  capacity?: number
}

/** 库区管理（frontend.md §27 库区；M1 契约：/api/zones CRUD + 启停，无删除——zones 停用即下线） */
export default function ZoneListPage() {
  const [params, setParams] = useState<ZoneQuery>({})
  const list = usePagedList<ZoneItem, ZoneQuery>({
    queryKey: ['warehouse', 'zones'],
    fetch: (q) => zoneApi.list(q),
    params,
  })

  // 仓库选项（搜索与表单共用，走真实 /api/warehouses）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['warehouse', 'warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: 100 }),
  })
  const warehouseOptions = (warehouseOptionsQuery.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: Number(item.id),
  }))
  const searchFields: SearchField[] = SEARCH_FIELDS.map((field) =>
    field.name === 'warehouseId' ? { ...field, options: warehouseOptions.map((o) => ({ label: o.label, value: String(o.value) })) } : field,
  )

  const [form] = Form.useForm<ZoneFormValues>()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<ZoneItem | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<unknown>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [togglingId, setTogglingId] = useState<number | string | null>(null)

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ZoneQuery)
    list.resetToFirstPage()
  }

  const openCreate = () => {
    setEditing(null)
    setFormError(null)
    form.resetFields()
    setDrawerOpen(true)
  }

  const openEdit = (record: ZoneItem) => {
    setEditing(record)
    setFormError(null)
    form.resetFields()
    form.setFieldsValue({
      warehouse_id: Number(record.warehouse_id),
      code: record.code,
      name: record.name,
      zone_type: record.zone_type,
      capacity: record.capacity,
    })
    setDrawerOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null)
    if (!values) return
    setSubmitting(true)
    setFormError(null)
    try {
      if (editing) {
        // 更新面不含 warehouse_id（dto.go ZoneUpdateInput：层级锚点创建后不可变更）
        const payload: ZoneUpdatePayload = {
          code: values.code,
          name: values.name,
          zone_type: values.zone_type,
          capacity: values.capacity,
        }
        await zoneApi.update(editing.id, payload)
      } else {
        await zoneApi.create(values)
      }
      setDrawerOpen(false)
      await list.refetch()
    } catch (err) {
      setFormError(err)
    } finally {
      setSubmitting(false)
    }
  }

  const handleToggleStatus = async (record: ZoneItem) => {
    const next: ResourceStatus = record.status?.toUpperCase() === 'ENABLED' ? 'DISABLED' : 'ENABLED'
    setTogglingId(record.id)
    setActionError(null)
    try {
      await zoneApi.setStatus(record.id, next)
      await list.refetch()
    } catch (err) {
      setActionError(err)
    } finally {
      setTogglingId(null)
    }
  }

  const columns: ColumnsType<ZoneItem> = [
    ...COLUMNS,
    {
      title: '操作',
      key: 'actions',
      width: 90,
      fixed: 'right',
      render: (_, record) => {
        const enabled = record.status?.toUpperCase() === 'ENABLED'
        return (
          <Space size={0}>
            <Button type="link" size="small" style={{ paddingInline: 4 }} onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Popconfirm
              title={enabled ? '确认停用该库区？' : '确认启用该库区？'}
              description={enabled ? '下属货架/库位将级联停用' : undefined}
              onConfirm={() => void handleToggleStatus(record)}
            >
              <Button type="link" size="small" style={{ paddingInline: 4 }} loading={togglingId === record.id}>
                {enabled ? '停用' : '启用'}
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
        title="库区管理"
        subtitle="仓库 → 库区：按类型划分收货/存储/拣货作业区域"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建库区
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm fields={searchFields} onSearch={handleSearch} />
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
        <SfTable<ZoneItem>
          storageKey="warehouse-zones"
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
          emptyText="当前筛选条件下没有库区"
          scrollX={860}
        />
      </Card>

      <Drawer
        title={editing ? '编辑库区' : '新建库区'}
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
        <Form form={form} layout="vertical">
          <Form.Item
            name="warehouse_id"
            label="所属仓库"
            rules={[{ required: true, message: '请选择所属仓库' }]}
            extra={editing ? '所属仓库创建后不可修改' : undefined}
          >
            <Select
              options={warehouseOptions}
              loading={warehouseOptionsQuery.isFetching}
              showSearch
              optionFilterProp="label"
              placeholder="请选择所属仓库"
              disabled={editing !== null}
            />
          </Form.Item>
          <Form.Item name="code" label="库区编码" rules={[{ required: true, message: '请输入库区编码' }]}>
            <Input placeholder="如 A-STORE" maxLength={64} />
          </Form.Item>
          <Form.Item name="name" label="库区名称" rules={[{ required: true, message: '请输入库区名称' }]}>
            <Input maxLength={255} />
          </Form.Item>
          <Form.Item name="zone_type" label="库区类型">
            <Select options={ZONE_TYPE_OPTIONS} allowClear />
          </Form.Item>
          <Form.Item name="capacity" label="容量">
            <InputNumber style={{ width: '100%' }} min={0} precision={2} />
          </Form.Item>
        </Form>
      </Drawer>
    </div>
  )
}
