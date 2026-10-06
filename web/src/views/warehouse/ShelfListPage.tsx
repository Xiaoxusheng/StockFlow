import { useMemo, useState } from 'react'
import { Alert, Button, Card, Drawer, Form, Input, InputNumber, Popconfirm, Select, Space, Spin, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  RESOURCE_STATUS_OPTIONS,
  shelfApi,
  toStatusKey,
  warehouseApi,
  zoneApi,
  type ResourceStatus,
  type ShelfItem,
  type ShelfQuery,
  type ShelfUpdatePayload,
  type WarehouseSpaceId,
} from '@/api/warehouse'
// 全量取数走 OPTIONS_PAGE_SIZE=100——后端 ParsePage 上限 MaxPageSize=100（response.go:47），
// 超 100 直接 400 COMMON_INVALID_PARAM（2026-10-05 货架页 pageSize=500 实测 400）
import { DateCell } from '@/components/table/cells'
import { OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm, type SearchField } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

const SEARCH_FIELDS: SearchField[] = [
  { name: 'keyword', label: '关键词', control: 'input', placeholder: '货架编码' },
  { name: 'warehouseId', label: '所属仓库', control: 'select' },
  { name: 'status', label: '状态', control: 'select', options: RESOURCE_STATUS_OPTIONS },
]

const COLUMNS: ColumnsType<ShelfItem> = [
  { title: '货架编码', dataIndex: 'code', width: 140, fixed: 'left' },
  {
    title: '层数',
    dataIndex: 'layers',
    width: 80,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '列数',
    dataIndex: 'columns',
    width: 80,
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
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: string) => <SfStatusTag status={toStatusKey(v)} />,
  },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    // 内容为 YYYY-MM-DD HH:mm（约 109px），150 足够；富余宽度让给仓库/库区列
    width: 150,
    render: (v?: string) => <DateCell value={v} />,
  },
]

/** 表单值与 ShelfPayload 同构：zone_id/warehouse_id 以 number 提交（dto.go:188 binding:required） */
interface ShelfFormValues {
  warehouse_id: number
  zone_id: number
  code: string
  layers?: number
  columns?: number
  capacity?: number
}

/** 货架管理（frontend.md §27 货架；M1 契约：/api/shelves CRUD + 启停，无删除——shelves 停用即下线） */
export default function ShelfListPage() {
  const [params, setParams] = useState<ShelfQuery>({})
  const list = usePagedList<ShelfItem, ShelfQuery>({
    queryKey: ['warehouse', 'shelves'],
    fetch: (q) => shelfApi.list(q),
    params,
  })

  // 仓库选项（搜索与表单共用）
  const warehouseOptionsQuery = useQuery({
    queryKey: ['warehouse', 'warehouses', 'options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: 100 }),
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

  // 全量库区清单（列表「所属库区」列映射 zone_id → 名称/编码；pageSize 对齐后端上限 100，
  // 超出部分按既有约定降级为仅前 100 条映射——全站选项取数同口径）
  const allZonesQuery = useQuery({
    queryKey: ['warehouse', 'zones', 'all-for-map'],
    queryFn: () => zoneApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
    staleTime: 60_000,
  })
  const zoneNameById = useMemo(() => {
    const map = new Map<string, string>()
    for (const z of allZonesQuery.data?.items ?? []) {
      map.set(String(z.id), `${z.name}（${z.code}）`)
    }
    return map
  }, [allZonesQuery.data])
  const warehouseNameById = useMemo(() => {
    const map = new Map<string, string>()
    for (const o of warehouseOptions) {
      map.set(String(o.value), o.label)
    }
    return map
  }, [warehouseOptions])

  const [form] = Form.useForm<ShelfFormValues>()
  const [drawerOpen, setDrawerOpen] = useState(false)
  // 行反馈动效（frontend.md §31 #7）：启停先 API 后反馈，hook 内 480ms 自动回落
  const fb = useTableRowFeedback()
  const [editing, setEditing] = useState<ShelfItem | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [formError, setFormError] = useState<unknown>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [togglingId, setTogglingId] = useState<number | string | null>(null)

  // 表单内级联：选中仓库后加载其库区选项
  const formWarehouseId = Form.useWatch('warehouse_id', form)
  const zoneOptionsQuery = useQuery({
    queryKey: ['warehouse', 'zones', 'options', formWarehouseId],
    queryFn: () => zoneApi.list({ warehouseId: formWarehouseId, page: 1, pageSize: 100 }),
    enabled: formWarehouseId !== undefined && formWarehouseId !== null,
  })
  const zoneOptions = (zoneOptionsQuery.data?.items ?? []).map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: Number(item.id),
  }))

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ShelfQuery)
    list.resetToFirstPage()
  }

  const openCreate = () => {
    setEditing(null)
    setFormError(null)
    form.resetFields()
    setDrawerOpen(true)
  }

  /** 表单回填（列表行 / 详情单条共用一份字段集） */
  const fillForm = (record: ShelfItem) => {
    form.setFieldsValue({
      warehouse_id: Number(record.warehouse_id),
      zone_id: Number(record.zone_id),
      code: record.code,
      layers: record.layers,
      columns: record.columns,
      capacity: record.capacity,
    })
  }

  const refreshDetail = async (id: WarehouseSpaceId) => {
    setDetailLoading(true)
    try {
      const item = await shelfApi.detail(id)
      setEditing(item)
      fillForm(item)
      setFormError(null)
    } catch (err) {
      // 详情刷新失败不阻断编辑：保留列表行数据，以非阻断 Alert 提示
      setFormError(err)
    } finally {
      setDetailLoading(false)
    }
  }

  const openEdit = (record: ShelfItem) => {
    setEditing(record)
    setFormError(null)
    form.resetFields()
    fillForm(record)
    setDrawerOpen(true)
    // 详情局部刷新：以 GET /api/shelves/:id 最新数据覆盖列表行（列表行可能过期）
    void refreshDetail(record.id)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields().catch(() => null)
    if (!values) return
    setSubmitting(true)
    setFormError(null)
    try {
      if (editing) {
        // 更新面不含 zone_id/warehouse_id（dto.go ShelfUpdateInput：层级锚点创建后不可变更）
        const payload: ShelfUpdatePayload = {
          code: values.code,
          layers: values.layers,
          columns: values.columns,
          capacity: values.capacity,
        }
        await shelfApi.update(editing.id, payload)
      } else {
        await shelfApi.create(values)
      }
      setDrawerOpen(false)
      await list.refetch()
    } catch (err) {
      setFormError(err)
    } finally {
      setSubmitting(false)
    }
  }

  const handleToggleStatus = async (record: ShelfItem) => {
    const next: ResourceStatus = record.status?.toUpperCase() === 'ENABLED' ? 'DISABLED' : 'ENABLED'
    setTogglingId(record.id)
    setActionError(null)
    try {
      await shelfApi.setStatus(record.id, next)
      fb.trigger(record.id, 'success')
      await list.refetch()
    } catch (err) {
      fb.trigger(record.id, 'error')
      setActionError(err)
    } finally {
      setTogglingId(null)
    }
  }

  const columns: ColumnsType<ShelfItem> = [
    ...COLUMNS,
    {
      title: '所属仓库',
      key: 'warehouse_name',
      width: 200,
      /* 名称常超列宽（如「演示一号仓（WH-D01）」）——截断处必须能回看完整值。
         用原生 title 而非列级 ellipsis.showTitle：单元格内容是 React 节点
         （Typography.Text），rc-table 的 showTitle 只能转字符串、不会生成 title。 */
      render: (_, record) => {
        const name = warehouseNameById.get(String(record.warehouse_id)) ?? String(record.warehouse_id)
        return (
          <Text type="secondary" title={name} ellipsis style={{ maxWidth: 180 }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '所属库区',
      key: 'zone_name',
      width: 200,
      /* 同所属仓库：库区名含编码后缀，截断处同样提供完整值回看 */
      render: (_, record) => {
        const name = zoneNameById.get(String(record.zone_id)) ?? String(record.zone_id)
        return (
          <Text type="secondary" title={name} ellipsis style={{ maxWidth: 180 }}>
            {name}
          </Text>
        )
      },
    },
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
              title={enabled ? '确认停用该货架？' : '确认启用该货架？'}
              description={enabled ? '下属库位将级联停用' : undefined}
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
        title="货架管理"
        subtitle="库区 → 货架：层 × 列构成库位骨架"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建货架
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
        <SfTable<ShelfItem>
          storageKey="warehouse-shelves"
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
          emptyText="当前筛选条件下没有货架"
          scrollX={710}
        />
      </Card>

      <Drawer
        title={editing ? '编辑货架' : '新建货架'}
        size={440}
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
              onChange={() => form.setFieldValue('zone_id', undefined)}
            />
          </Form.Item>
          <Form.Item
            name="zone_id"
            label="所属库区"
            rules={[{ required: true, message: '请选择所属库区' }]}
            extra={editing ? '所属仓库/库区创建后不可修改' : undefined}
          >
            <Select
              options={zoneOptions}
              loading={zoneOptionsQuery.isFetching}
              showSearch
              optionFilterProp="label"
              placeholder="请先选择仓库，再选择库区"
              disabled={editing !== null}
            />
          </Form.Item>
          <Form.Item name="code" label="货架编码" rules={[{ required: true, message: '请输入货架编码' }]}>
            <Input placeholder="如 A-STORE-S01" maxLength={64} />
          </Form.Item>
          <Form.Item name="layers" label="层数">
            <InputNumber style={{ width: '100%' }} min={1} precision={0} />
          </Form.Item>
          <Form.Item name="columns" label="列数">
            <InputNumber style={{ width: '100%' }} min={1} precision={0} />
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
