import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Row,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { SfConfirm } from '@/components/common/SfConfirm'
import {
  masterdataApi,
  toStatusKey,
  type EnabledStatus,
  type UnitItem,
  type UnitQuery,
  type UnitSavePayload,
} from '@/api/masterdata'
import { resolveErrorMessage } from '@/api/client'
import { useCrudPermissions } from '@/hooks/useCrudPermissions'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

interface UnitFormValues {
  code: string
  name: string
}

/** 提交契约（UnitCreateInput/UnitUpdateInput，service_category.go:320-323/356-358）：
 * 仅 code/name——创建恒 ENABLED、启停走 PUT /units/:id/status，编码创建后不可改 */
function toPayload(values: UnitFormValues): UnitSavePayload {
  return {
    code: values.code.trim(),
    name: values.name.trim(),
  }
}

/** 计量单位（/units，backend-m1-plan §5.4：code / name / status；无删除接口——
 * 停用即生命周期终点，masterdata.go:22/73） */
export default function UnitListPage() {
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<UnitItem | null>(null)
  const [form] = Form.useForm<UnitFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限（无权限则隐藏入口，后端仍会独立校验）；
  // 后端 unit 域未冻结 delete 码，canDelete 恒 false，本页本就无删除入口
  const { canCreate, canUpdate, canStatus } = useCrudPermissions('unit')
  // 动效 #7（frontend.md §31）：启停成功/失败行淡色反馈——仅在 API 回调后触发
  const fb = useTableRowFeedback()

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<UnitItem, UnitQuery>({
    queryKey: ['masterdata', 'units'],
    fetch: (q) => masterdataApi.units.list(q),
    urlSync: true,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['masterdata', 'units'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: UnitSavePayload) =>
      editing ? masterdataApi.units.update(editing.id, payload) : masterdataApi.units.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: UnitItem['id']; status: EnabledStatus }) =>
      masterdataApi.units.setStatus(id, { status }),
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

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: UnitItem) => {
    saveMutation.reset()
    setEditing(record)
    setModalOpen(true)
    form.setFieldsValue({ code: record.code, name: record.name })
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => saveMutation.mutate(toPayload(values)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  /** 操作列按权限装配：无编辑权限不出「编辑」，无状态权限不出「停用/启用」 */
  const actionColumn: ColumnsType<UnitItem>[number] = {
    title: '操作',
    key: 'actions',
    fixed: 'right',
    width: 120,
    render: (_: unknown, record: UnitItem) => {
      const disabling = record.status === 'ENABLED'
      return (
        <span style={{ whiteSpace: 'nowrap' }}>
          {canUpdate && (
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
          )}
          {canStatus && (
            <SfConfirm
              title={disabling ? '确认停用该单位？' : '确认启用该单位？'}
              description={
                disabling
                  ? '被商品引用（含停用商品）时后端将拒绝停用；建议先调整引用。'
                  : '启用后单位可重新被商品引用。'
              }
              okText={disabling ? '停用' : '启用'}
              confirming={statusMutation.isPending}
              onConfirm={() =>
                statusMutation.mutate({ id: record.id, status: disabling ? 'DISABLED' : 'ENABLED' })
              }
            >
              <Button type="link" size="small" danger={disabling}>
                {disabling ? '停用' : '启用'}
              </Button>
            </SfConfirm>
          )}
        </span>
      )
    },
  }

  const columns: ColumnsType<UnitItem> = [
    { title: '单位编码', dataIndex: 'code', width: 130, fixed: 'left' },
    {
      title: '单位名称',
      dataIndex: 'name',
      width: 200,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
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
    ...(canUpdate || canStatus ? [actionColumn] : []),
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="计量单位"
        subtitle="商品计量单位（个 / 箱 / 千克等）维护；单位无删除，停用即终点"
        extra={
          canCreate ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              新建单位
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '单位编码 / 名称' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<UnitItem>
          storageKey="masterdata-units"
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
          emptyText={canCreate ? '暂无计量单位，点击右上角「新建单位」创建' : '暂无计量单位'}
          emptyAction={
            canCreate ? (
              <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate}>
                新建单位
              </Button>
            ) : undefined
          }
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          scrollX={810}
        />
      </Card>

      <Modal
        title={editing ? '编辑单位' : '新建单位'}
        open={modalOpen}
        width={480}
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
        <Form<UnitFormValues> form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="code"
                label="单位编码"
                rules={[{ required: true, message: '请输入单位编码' }]}
                extra={editing ? '编码创建后不可修改' : undefined}
              >
                <Input placeholder="唯一编码" maxLength={32} disabled={editing !== null} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="name" label="单位名称" rules={[{ required: true, message: '请输入单位名称' }]}>
                <Input placeholder="如：个 / 箱 / 千克" maxLength={64} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>
    </div>
  )
}
