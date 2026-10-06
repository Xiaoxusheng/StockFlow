import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { Alert, Button, Card, Input, InputNumber, Modal, Select, Tag, Typography, message } from 'antd'
import { PlusOutlined, PrinterOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import {
  PACKING_EXECUTE_PERMISSION,
  outboundApi,
  outboundTaskApi,
  type OutboundOrderItem,
  type PackingRecord,
  type PackingRecordQuery,
} from '@/api/outbound'
import { buildSkuMaps, buildWarehouseMaps, fetchSkuOptions, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfQrPrintModal, type SfQrPrintSku } from '@/components/print/SfQrPrintModal'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 出库单状态中文（弹窗内提示用；完整状态列见 OutboundPage OB_STATUS_TAG） */
const OB_STATUS_LABEL: Record<string, string> = {
  PENDING_ALLOCATE: '待分配',
  ALLOCATED: '已分配',
  PICKING: '拣货中',
  PICKED: '已拣货',
  CHECKED: '已复核',
  PACKED: '已打包',
  PARTIAL_SHIPPED: '部分发货',
  SHIPPED_ALL: '全部发货',
  CANCELLED: '已取消',
  CLOSED: '已关闭',
}

/**
 * 打包管理（GET /api/packing，后端 M2 已交付）：列表列回对 PackingRecord 裸模型——
 * 包裹编号/包装材料/长宽高/重量/体积/快递公司/快递单号（business-flow.md §8.4 全列），
 * 一单允许多包裹。模型无状态字段，不渲染状态列（裸模型回对，不虚构状态）。
 * 打包写端点（POST /api/packing，sales:packing:execute）：出库单须 CHECKED（service_outbound.go:791），
 * 逐行填本包打包量（≤ qty - qty_packed，ErrPackExceed），全部明细打包完成推进 CHECKED→PACKED；
 * 幂等键 randomUUID 防双击重提（后端 Idempotency-Key 命中回放 replay=true）。
 * 仓库 ID 经基础资料 options 本地映射，失败降级为 ID。
 */
export default function PackingPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const canPack = canAccess(user, PACKING_EXECUTE_PERMISSION)

  /** 打包弹窗开关 */
  const [packOpen, setPackOpen] = useState(false)
  /**
   * 箱码标签打印（CARTON_CODE）：data_ids 传**包裹编号原文**——箱码由 printing 包
   * 内置 builtinReader 承接（ports.go:27：托盘/箱码业务域未建，data_ids 即码值原文），
   * 不需要业务对象 ID，故与 SKU/BIN 的"数字 ID"通道不同。
   */
  const [printTargets, setPrintTargets] = useState<SfQrPrintSku[]>([])
  const [printOpen, setPrintOpen] = useState(false)
  const [selectedPackKeys, setSelectedPackKeys] = useState<Array<string | number>>([])
  /** 批量打印箱码：选中包裹的编号原文作为 data_ids（builtinReader 直吃码值） */
  const openBulkCartonPrint = () => {
    setPrintTargets(
      list.items
        .filter((item) => selectedPackKeys.some((key) => String(key) === String(item.id)))
        .map((item) => ({
          id: item.package_no,
          code: item.package_no,
          productName: item.carrier || item.outbound_no,
          enabled: true,
        })),
    )
    setPrintOpen(true)
  }
  const canPrintLabel = canAccess(user, 'printing:task:create')
  /** 打包弹窗：选中的出库单号（undefined=未选） */
  const [packNo, setPackNo] = useState<string>()
  /** 逐行本包打包量（line_no → qty；详情到位时初始化为剩余量） */
  const [rowQty, setRowQty] = useState<Record<number, number>>({})
  /** 可选物流/材料字段 */
  const [material, setMaterial] = useState('')
  const [carrier, setCarrier] = useState('')
  const [trackingNo, setTrackingNo] = useState('')
  const [remark, setRemark] = useState('')

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<PackingRecord, PackingRecordQuery>({
    queryKey: ['outbound', 'packing'],
    fetch: (q) => outboundTaskApi.packing.list(q),
    urlSync: true,
  })

  // 仓库 ID → 名称（options.ts：一次取全基础资料，失败降级为 ID）
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])

  /** 可打包出库单（CHECKED；remote options，前端限前 50 单，精确单号可手搜过滤） */
  const packable = useQuery({
    queryKey: ['outbound', 'packable-orders'],
    queryFn: () => outboundApi.list({ status: 'CHECKED', page: 1, pageSize: 50 }),
    enabled: canPack,
  })

  /** 选中出库单的详情（items 供逐行填打包量） */
  const detail = useQuery({
    queryKey: ['outbound', 'pack-detail', packNo],
    queryFn: () => outboundApi.get(packNo!),
    enabled: !!packNo,
  })
  const detailItems: OutboundOrderItem[] = detail.data?.items ?? []
  const detailStatus = detail.data?.outbound.status
  const packableOrder = detailStatus === 'CHECKED'

  /** 详情到位 → 行打包量初始化为剩余量（qty - qty_packed，全量打包为最常见操作） */
  useEffect(() => {
    if (!detail.data) return
    const init: Record<number, number> = {}
    for (const it of detail.data.items) {
      const remain = it.qty - it.qty_packed
      if (remain > 0) init[it.line_no] = remain
    }
    setRowQty(init)
  }, [detail.data])

  const resetModal = () => {
    setPackNo(undefined)
    setRowQty({})
    setMaterial('')
    setCarrier('')
    setTrackingNo('')
    setRemark('')
  }

  const packMutation = useMutation({
    mutationFn: (payload: Parameters<typeof outboundTaskApi.packing.pack>[0]) =>
      outboundTaskApi.packing.pack(payload),
    onSuccess: (r) => {
      message.success(
        r.replay
          ? `打包请求为幂等重放，包裹 ${r.package.package_no} 已存在`
          : `打包完成：包裹 ${r.package.package_no}`,
      )
      queryClient.invalidateQueries({ queryKey: ['outbound', 'packing'] })
      queryClient.invalidateQueries({ queryKey: ['outbound', 'packable-orders'] })
      setPackOpen(false)
      resetModal()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  const totalLines = detailItems.filter((it) => (rowQty[it.line_no] ?? 0) > 0).length
  const submitPack = () => {
    if (!packNo) return
    const lines = detailItems
      .map((it) => ({ line_no: it.line_no, qty: rowQty[it.line_no] ?? 0 }))
      .filter((l) => l.qty > 0)
    if (lines.length === 0) {
      message.warning('请至少为一行填写大于 0 的打包数量')
      return
    }
    packMutation.mutate({
      outbound_no: packNo,
      lines,
      ...(material.trim() ? { packing_material: material.trim() } : {}),
      ...(carrier.trim() ? { carrier: carrier.trim() } : {}),
      ...(trackingNo.trim() ? { tracking_no: trackingNo.trim() } : {}),
      ...(remark.trim() ? { remark: remark.trim() } : {}),
      idempotency_key: crypto.randomUUID(),
    })
  }

  const columns: ColumnsType<PackingRecord> = [
    { title: '包裹编号', dataIndex: 'package_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: PackingRecord) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    {
      title: '包装材料',
      dataIndex: 'packing_material',
      width: 110,
      render: (v: string) => v || '-',
    },
    {
      title: '尺寸（长×宽×高）',
      dataIndex: 'length',
      key: 'dimensions',
      width: 160,
      align: 'right',
      render: (_: unknown, record: PackingRecord) => (
        <span className="sf-num" style={{ whiteSpace: 'nowrap' }}>
          {formatNumber(record.length)} × {formatNumber(record.width)} × {formatNumber(record.height)}
        </span>
      ),
    },
    {
      title: '重量',
      dataIndex: 'weight',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '体积',
      dataIndex: 'volume',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    { title: '快递公司', dataIndex: 'carrier', width: 120, render: (v: string) => v || '-' },
    { title: '快递单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || '-' },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      render: (v: number) => {
        const name = warehouseNames.get(idKey(v)) ?? idKey(v)
        return (
          <Text style={{ maxWidth: 110 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      // PackingRecord 无独立打包时间字段，created_at 即打包落库时间（models.go:167-186）
      title: '打包时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v: string) => v || '-' },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="打包管理"
        subtitle="打包记录：包裹 / 包装材料 / 快递信息（一单允许多包裹）"
        extra={
          canPack ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setPackOpen(true)}>
              新建打包
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: idKey(w.id),
              })),
            },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 批量打印箱码按钮与「查询/重置」同行（用户口径：批量按钮上移到搜索行） */
          extraActions={
            canPrintLabel && selectedPackKeys.length > 0 ? (
              <Button type="primary" icon={<PrinterOutlined />} onClick={openBulkCartonPrint}>
                批量打印箱码（{selectedPackKeys.length}）
              </Button>
            ) : undefined
          }
        />
        <SfTable<PackingRecord>
          storageKey="outbound-packing"
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
          emptyText="当前筛选条件下没有打包记录"
          emptyAction={
            /* 空状态 CTA：打包动作由本页发起（选 CHECKED 出库单），有权限时引导直达 */
            canPack ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setPackOpen(true)}>
                新建打包
              </Button>
            ) : undefined
          }
          /* 批量打印箱码：勾选多行 → 包裹编号原文作为 data_ids（builtinReader 直吃码值）；
             批量按钮已上移至搜索行 extraActions */
          rowSelection={{
            selectedRowKeys: selectedPackKeys,
            onChange: (keys) => setSelectedPackKeys(keys as Array<string | number>),
          }}
          scrollX={1470}
        />
      </Card>

      {/* 新建打包弹窗：选 CHECKED 出库单 → 逐行填本包打包量（默认剩余量）→ 可选物流字段。
          状态非 CHECKED 时禁提交（后端 service_outbound.go:791 同口径守卫，前端拦截仅为体验） */}
      <Modal
        open={packOpen}
        title="新建打包"
        okText="确认打包"
        okButtonProps={{ disabled: !!packNo && !packableOrder, loading: packMutation.isPending }}
        onCancel={() => {
          setPackOpen(false)
          resetModal()
        }}
        onOk={submitPack}
        width={720}
      >
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
          <div>
            <div style={{ marginBottom: 4 }}>出库单（仅已复核 CHECKED）</div>
            <Select
              showSearch
              placeholder="选择出库单"
              style={{ width: '100%' }}
              value={packNo}
              loading={packable.isFetching}
              filterOption={(input, option) =>
                String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
              }
              onChange={(v: string) => {
                setPackNo(v)
                setRowQty({})
              }}
              options={(packable.data?.items ?? []).map((o) => ({
                label: `${o.outbound_no}（${o.so_no || '-'}）`,
                value: o.outbound_no,
              }))}
              notFoundContent={
                packable.isError
                  ? `出库单列表加载失败：${resolveErrorMessage(packable.error)}（需 sales:outbound:list）`
                  : packable.isFetching
                    ? '加载中…'
                    : '没有可打包的出库单'
              }
            />
          </div>
          <div>
            <div style={{ marginBottom: 4 }}>包装材料 / 快递</div>
            <Input
              placeholder="包装材料（可选）"
              value={material}
              onChange={(e) => setMaterial(e.target.value)}
            />
          </div>
        </div>

        {packNo && detail.isFetching && <div style={{ padding: '16px 0' }}>明细加载中…</div>}
        {packNo && detail.isError && (
          <Alert
            type="error"
            showIcon
            style={{ marginBottom: 12 }}
            message="出库单明细加载失败"
            description={`${resolveErrorMessage(detail.error)}（需出库单查看权限 sales:outbound:read）`}
          />
        )}
        {packNo && detailStatus && !packableOrder && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message={`出库单当前为「${OB_STATUS_LABEL[detailStatus] ?? detailStatus}」，仅已复核（CHECKED）状态可打包`}
          />
        )}
        {packNo && detailStatus && packableOrder && (
          <>
            <div style={{ marginBottom: 8, display: 'flex', gap: 8, alignItems: 'center' }}>
              <Tag color="processing">{OB_STATUS_LABEL[detailStatus]}</Tag>
              <Text type="secondary">打包量默认取该行剩余量（应拣 − 已打包），可按需改小；本次将打包 {totalLines} 行</Text>
            </div>
            <div style={{ maxHeight: 300, overflow: 'auto', border: '1px solid var(--sf-border, #f0f0f0)', borderRadius: 6 }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr style={{ textAlign: 'left', color: 'var(--sf-text-secondary, #888)' }}>
                    <th style={thStyle}>行号</th>
                    <th style={thStyle}>SKU</th>
                    <th style={{ ...thStyle, textAlign: 'right' }}>应拣 / 已打包</th>
                    <th style={{ ...thStyle, textAlign: 'right' }}>本包打包量</th>
                  </tr>
                </thead>
                <tbody>
                  {detailItems.map((it) => {
                    const remain = it.qty - it.qty_packed
                    return (
                      <tr key={it.line_no}>
                        <td style={tdStyle}>{it.line_no}</td>
                        <td style={tdStyle}>{skuMaps.code.get(idKey(it.sku_id)) ?? idKey(it.sku_id)}</td>
                        <td style={{ ...tdStyle, textAlign: 'right' }} className="sf-num">
                          {formatNumber(it.qty)} / {formatNumber(it.qty_packed)}
                        </td>
                        <td style={{ ...tdStyle, textAlign: 'right' }}>
                          <InputNumber
                            size="small"
                            min={0}
                            max={remain}
                            step={1}
                            value={rowQty[it.line_no]}
                            disabled={remain <= 0}
                            onChange={(v) =>
                              setRowQty((prev) => ({ ...prev, [it.line_no]: Number(v ?? 0) }))
                            }
                          />
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 12 }}>
              <Input
                placeholder="快递公司（可选）"
                value={carrier}
                onChange={(e) => setCarrier(e.target.value)}
              />
              <Input
                placeholder="快递单号（可选）"
                value={trackingNo}
                onChange={(e) => setTrackingNo(e.target.value)}
              />
            </div>
            <Input.TextArea
              rows={2}
              style={{ marginTop: 12 }}
              placeholder="备注（可选）"
              value={remark}
              onChange={(e) => setRemark(e.target.value)}
            />
          </>
        )}
      </Modal>

      {/* 箱码标签打印（CARTON_CODE）：复用标签打印弹窗（按 objectType 参数化），
          打印对象为勾选行的包裹编号原文（builtinReader 直接承接码值） */}
      <SfQrPrintModal
        open={printOpen}
        skus={printTargets}
        objectType="CARTON_CODE"
        noun="箱码"
        onClose={() => setPrintOpen(false)}
      />
    </div>
  )
}

const thStyle: CSSProperties = { padding: '8px 12px', borderBottom: '1px solid var(--sf-border, #f0f0f0)', position: 'sticky', top: 0, background: 'var(--sf-bg-container, #fff)' }
const tdStyle: CSSProperties = { padding: '6px 12px', borderBottom: '1px solid var(--sf-border, #f0f0f0)' }
