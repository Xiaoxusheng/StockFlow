import { useState } from 'react'
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
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  BIN_TYPE_LABEL,
  RESOURCE_STATUS_OPTIONS,
  binApi,
  shelfApi,
  toStatusKey,
  warehouseApi,
  zoneApi,
  type BinItem,
  type BinQuery,
  type BinUpdatePayload,
  type ResourceStatus,
} from '@/api/warehouse'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const BIN_TYPE_OPTIONS = Object.entries(BIN_TYPE_LABEL).map(([value, label]) => ({ label, value }))

const SEARCH_FIELDS: SearchField[] = [
  { name: 'keyword', label: '关键词', control: 'input', placeholder: '库位编码' },
  { name: 'warehouseId', label: '所属仓库', control: 'select' },
  { name: 'binType', label: '库位类型', control: 'select', options: BIN_TYPE_OPTIONS },
  { name: 'status', label: '状态', control: 'select', options: RESOURCE_STATUS_OPTIONS },
]

const COLUMNS: ColumnsType<BinItem> = [
  { title: '库位编码', dataIndex: 'code', width: 160, fixed: 'left' },
  {
    title: '层',
    dataIndex: 'layer',
    width: 60,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '列',
    dataIndex: 'column_no',
    width: 60,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '类型',
    dataIndex: 'bin_type',
    width: 90,
    render: (v?: string) => (v ? (BIN_TYPE_LABEL[v] ?? v) : '-'),
  },
  {
    title: '最大容量',
    dataIndex: 'max_capacity',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '当前容量',
    dataIndex: 'current_capacity',
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

/** 表单值与 BinPayload 同构：shelf_id 等关联 ID 以 number 提交（dto.go:207 binding:required） */
interface BinFormValues {
  warehouse_id: number
  zone_id: number
  shelf_id: number
  layer?: number
  column_no?: number
  code: string
  bin_type?: string
  max_capacity?: number
}

/** 库位管理（frontend.md §27 库位；M1 契约：/api/bins CRUD + 启停/删除；当前容量由上架/移库业务维护，禁止直改） */
export default function BinListPage() {
  const [params, setParams] = useState<BinQuery>({})
  const list = usePagedList<BinItem, BinQuery>({
    queryKey: ['warehouse', 'bins'],
    fetch: (q) => binApi.list(q),
    params,
  })

  // 仓库选项（搜索与表单共用）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['warehouse', 'warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: 200 }),
  })
  const warehouseOptions = (warehouseOptionsQuery.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: Number(item.id),
  }))
  const searchFields: SearchField[] = SEARCH_FIELDS.map((field) =>
    field.name === 'warehouseId'
      ? { ...field, options: warehouseOptions.map((o) => ({ label: o.label, value: String(o.value) })) }
      : field,
  )

  const [form] = Form.useForm<BinFormValues>()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<BinItem | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<unknown>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [togglingId, setTogglingId] = useState<number | string | null>(null)
  const [removingId, setRemovingId] = useState<number | string | null>(null)

  // 表单内级联：仓库 → 库区 → 货架
  const formWarehouseId = Form.useWatch('warehouse_id', form)
  const formZoneId = Form.useWatch('zone_id', form)
  const zoneOptionsQuery = useQuery({
    queryKey: ['warehouse', 'zones', 'options', formWarehouseId],
    queryFn: () => zoneApi.list({ warehouseId: formWarehouseId, page: 1, pageSize: 200 }),
    enabled: formWarehouseId !== undefined && formWarehouseId !== null,
  })
  const zoneOptions = (zoneOptionsQuery.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: Number(item.id),
  }))
  const shelfOptionsQuery = useQuery({
    queryKey: ['warehouse', 'shelves', 'options', formZoneId],
    queryFn: () => shelfApi.list({ zoneId: formZoneId, page: 1, pageSize: 200 }),
    enabled: formZoneId !== undefined && formZoneId !== null,
  })
  const shelfOptions = (shelfOptionsQuery.data?.items ?? []).map((item) => ({
    label: item.code,
    value: Number(item.id),
  }))

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as BinQuery)
    list.resetToFirstPage()
  }

  const openCreate = () => {
    setEditing(null)
    setFormError(null)
    form.resetFields()
    setDrawerOpen(true)
  }

  const openEdit = (record: BinItem) => {
    setEditing(record)
    setFormError(null)
    form.resetFields()
    form.setFieldsValue({
      warehouse_id: Number(record.warehouse_id),
      zone_id: Number(record.zone_id),
      shelf_id: Number(record.shelf_id),
      layer: record.layer,
      column_no: record.column_no,
      code: record.code,
      bin_type: record.bin_type,
      max_capacity: record.max_capacity,
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
        // 更新面不含 zone_id/shelf_id/warehouse_id（dto.go BinUpdateInput：层级锚点不可变更，
        // current_capacity 由上架/移库业务维护）
        const payload: BinUpdatePayload = {
          code: values.code,
          bin_type: values.bin_type,
          layer: values.layer,
          column_no: values.column_no,
          max_capacity: values.max_capacity,
        }
        await binApi.update(editing.id, payload)
      } else {
        await binApi.create(values)
      }
      setDrawerOpen(false)
      await list.refetch()
    } catch (err) {
      setFormError(err)
    } finally {
      setSubmitting(false)
    }
  }

  const handleToggleStatus = async (record: BinItem) => {
    const next: ResourceStatus = record.status?.toUpperCase() === 'ENABLED' ? 'DISABLED' : 'ENABLED'
    setTogglingId(record.id)
    setActionError(null)
    try {
      await binApi.setStatus(record.id, next)
      await list.refetch()
    } catch (err) {
      setActionError(err)
    } finally {
      setTogglingId(null)
    }
  }

  const handleRemove = async (record: BinItem) => {
    setRemovingId(record.id)
    setActionError(null)
    try {
      await binApi.remove(record.id)
      await list.refetch()
    } catch (err) {
      setActionError(err)
    } finally {
      setRemovingId(null)
    }
  }

  const columns: ColumnsType<BinItem> = [
    ...COLUMNS,
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
              title={enabled ? '确认停用该库位？' : '确认启用该库位？'}
              onConfirm={() => void handleToggleStatus(record)}
            >
              <Button type="link" size="small" style={{ paddingInline: 4 }} loading={togglingId === record.id}>
                {enabled ? '停用' : '启用'}
              </Button>
            </Popconfirm>
            <Popconfirm
              title="确认删除该库位？"
              description="已有库存数据时后端将拒绝删除（级联校验）"
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
        title="库位管理"
        subtitle="货架 → 库位：库存五维定位的锚点"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建库位
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
        <SfTable<BinItem>
          storageKey="warehouse-bins"
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
          emptyText="当前筛选条件下没有库位"
          scrollX={920}
        />
      </Card>

      <Drawer
        title={editing ? '编辑库位' : '新建库位'}
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
          >
            <Select
              options={warehouseOptions}
              loading={warehouseOptionsQuery.isFetching}
              showSearch
              optionFilterProp="label"
              placeholder="请选择所属仓库"
              disabled={editing !== null}
              onChange={() => {
                form.setFieldValue('zone_id', undefined)
                form.setFieldValue('shelf_id', undefined)
              }}
            />
          </Form.Item>
          <Form.Item
            name="zone_id"
            label="所属库区"
            rules={[{ required: true, message: '请选择所属库区' }]}
          >
            <Select
              options={zoneOptions}
              loading={zoneOptionsQuery.isFetching}
              showSearch
              optionFilterProp="label"
              placeholder="请先选择仓库，再选择库区"
              disabled={editing !== null}
              onChange={() => form.setFieldValue('shelf_id', undefined)}
            />
          </Form.Item>
          <Form.Item
            name="shelf_id"
            label="所属货架"
            rules={[{ required: true, message: '请选择所属货架' }]}
            extra={editing ? '所属仓库/库区/货架创建后不可修改' : undefined}
          >
            <Select
              options={shelfOptions}
              loading={shelfOptionsQuery.isFetching}
              showSearch
              optionFilterProp="label"
              placeholder="请先选择库区，再选择货架"
              disabled={editing !== null}
            />
          </Form.Item>
          <Form.Item name="code" label="库位编码" rules={[{ required: true, message: '请输入库位编码' }]}>
            <Input placeholder="如 A-STORE-S01-02-03（仓内唯一）" maxLength={64} />
          </Form.Item>
          <Form.Item name="layer" label="层">
            <InputNumber style={{ width: '100%' }} min={1} precision={0} />
          </Form.Item>
          <Form.Item name="column_no" label="列">
            <InputNumber style={{ width: '100%' }} min={1} precision={0} />
          </Form.Item>
          <Form.Item name="bin_type" label="库位类型">
            <Select options={BIN_TYPE_OPTIONS} allowClear />
          </Form.Item>
          <Form.Item name="max_capacity" label="最大容量">
            <InputNumber style={{ width: '100%' }} min={0} precision={2} />
          </Form.Item>
        </Form>
      </Drawer>
    </div>
  )
}
