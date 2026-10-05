import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Card, Tooltip, Typography, message } from 'antd'
import type { TableProps } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router'
import { masterdataApi, type SkuItem, type SkuQuery } from '@/api/masterdata'
import { fetchProductOptions } from '@/api/options'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { QrCodeView } from '@/components/print/QrCodeView'
import { SfQrPreviewDrawer, type SfQrSkuInfo } from '@/components/print/SfQrPreviewDrawer'
import { SfQrPrintModal, type SfQrPrintSku } from '@/components/print/SfQrPrintModal'
import { tryBuildSfqrSku } from '@/utils/qrPayload'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

const { Text } = Typography

const ENABLED_FILTER_OPTIONS = [
  { label: '已启用', value: 'true' },
  { label: '已停用', value: 'false' },
]

/**
 * SKU 行 → 二维码抽屉/打印弹窗视图（SfQrSkuInfo 结构兼容 SfQrPrintSku）。
 * 商品名：后端 SKU 列表不联表下发（omitempty，service_sku.go:43-44），
 * 经一次取全的商品数据源按 product_id 映射（SkuListPage.tsx:143-146 同口径）；
 * 主条码：is_primary 优先、次取首条（后端装配同序，printing_content.go 主条码口径）。
 */
function toSfQrSkuInfo(record: SkuItem, productNameById: Map<string, string>): SfQrSkuInfo {
  const codes = record.barcodes ?? []
  const primary = codes.find((b) => b.is_primary) ?? codes[0]
  return {
    id: record.id,
    code: record.code,
    productName: record.product_name ?? productNameById.get(String(record.product_id)),
    primaryBarcode: primary?.barcode,
    enabled: record.is_enabled,
  }
}

/**
 * 商品二维码中心（/qr-codes，frontend.md §13.1 / docs/qr-code.md §7.2 冻结）：
 * SFQR 协议身份工作台——SKU 列表 + 48px 动态预览（构造唯一点 qrPayload.tryBuildSfqrSku，
 * 不持久化）+ 详情抽屉（URL ?code= 直达承接扫码往返，qr-code.md §7.5）+ 行选择批量打印
 * （统一走 PrintTask，约束 4）。
 *
 * 数据面零新增端点：复用 masterdataApi.skus.list（列表已批量装配 barcodes）+
 * printingApi.tasks（SfQrPrintModal）；权限零新增：菜单 sku:view、打印 printing:task:create
 * fail-closed（qr-code.md §10）。
 */
export default function QrCodeCenterPage() {
  const navigate = useNavigate()
  const [params, setParams] = useState<SkuQuery>({})
  const [selectedKeys, setSelectedKeys] = useState<React.Key[]>([])
  const [drawerSku, setDrawerSku] = useState<SkuItem | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [printSkus, setPrintSkus] = useState<SfQrPrintSku[]>([])
  const [printOpen, setPrintOpen] = useState(false)
  const [messageApi, contextHolder] = message.useMessage()
  const user = useAuthStore((s) => s.user)

  const list = usePagedList<SkuItem, SkuQuery>({
    queryKey: ['masterdata', 'skus', 'qr-center'],
    fetch: (q) => masterdataApi.skus.list(q),
    params,
  })

  // 商品名 options map（列表不联表下发，同一 API 的真实数据兜底；失败降级 '-' 不阻塞页面）
  const products = useQuery({
    queryKey: ['masterdata', 'products', 'options'],
    queryFn: fetchProductOptions,
  })
  const productNameById = useMemo(
    () => new Map((products.data ?? []).map((item) => [String(item.id), item.name])),
    [products.data],
  )

  // 扫码直达（qr-code.md §7.5）：?code=<SKU 编码> 自动开抽屉；keyword 模糊检索后精确命中，
  // 查无如实提示（不造假数据，requirements.md §10）。命中后清参避免重复打开。
  const [searchParams, setSearchParams] = useSearchParams()
  const directCode = searchParams.get('code') ?? ''
  const directQuery = useQuery({
    queryKey: ['masterdata', 'skus', 'qr-direct', directCode],
    queryFn: () => masterdataApi.skus.list({ keyword: directCode, page: 1, pageSize: 20 }),
    enabled: directCode.length > 0,
  })
  useEffect(() => {
    if (!directCode) return
    if (directQuery.isSuccess) {
      const hit = (directQuery.data?.items ?? []).find((row) => row.code === directCode)
      if (hit) {
        setDrawerSku(hit)
        setDrawerOpen(true)
      } else {
        messageApi.warning(`未找到编码为「${directCode}」的 SKU（可能已删除或编码不存在）`)
      }
      setSearchParams({}, { replace: true })
    } else if (directQuery.isError) {
      messageApi.error(resolveErrorMessage(directQuery.error))
      setSearchParams({}, { replace: true })
    }
  }, [directCode, directQuery.isSuccess, directQuery.isError, directQuery.data, directQuery.error, messageApi, setSearchParams])

  // —— 行选择（跨页保留：自维护 Map 快照，批量打印以快照为提交集，杜绝跨页丢行静默少打）——
  const selectedRowsRef = useRef(new Map<string, SkuItem>())
  const rowSelection: TableProps<SkuItem>['rowSelection'] = {
    selectedRowKeys: selectedKeys,
    preserveSelectedRowKeys: true,
    onChange: (keys, rows) => {
      const keep = new Set(keys.map(String))
      const next = new Map<string, SkuItem>()
      for (const [key, row] of selectedRowsRef.current) {
        if (keep.has(key)) next.set(key, row)
      }
      for (const row of rows) next.set(String(row.id), row)
      selectedRowsRef.current = next
      setSelectedKeys([...keys])
    },
  }
  const selectedSkus = useMemo(
    () =>
      selectedKeys
        .map((key) => selectedRowsRef.current.get(String(key)))
        .filter((row): row is SkuItem => Boolean(row)),
    [selectedKeys],
  )
  // 不可打印 K：已选中行内停用数（预筛提示；最终裁决在后端任务装配，qr-code.md §9）
  const disabledCount = selectedSkus.filter((row) => !row.is_enabled).length
  const canPrint = canAccess(user, 'printing:task:create')

  const openDrawer = (record: SkuItem) => {
    setDrawerSku(record)
    setDrawerOpen(true)
  }

  const openPrint = (skus: SfQrPrintSku[]) => {
    setPrintSkus(skus)
    setPrintOpen(true)
  }

  const openBatchPrint = () => {
    if (selectedSkus.length === 0) return
    openPrint(selectedSkus.map((row) => toSfQrSkuInfo(row, productNameById)))
  }

  // 动效 #4 批量工具栏（frontend.md §31）：批量操作交 SfTable bulkActions——选中>0 时工具栏
  // 左区自动切换「已选择 N 条 + 批量操作」（清空走 rowSelection.onChange 空数组，本页 onChange
  // 同步重建快照 Map 为空，跨页快照语义不变；页面级 SfBatchBar 条件渲染已被替换）。
  const bulkActions = (
    <>
      {disabledCount > 0 && <Text type="danger">不可打印 {disabledCount} 个（商品已停用）</Text>}
      <Button type="primary" disabled={selectedSkus.length === 0} onClick={openBatchPrint}>
        批量打印二维码
      </Button>
    </>
  )

  const columns: ColumnsType<SkuItem> = [
    { title: 'SKU 编码', dataIndex: 'code', width: 140, fixed: 'left' },
    {
      title: '商品名',
      key: 'product_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: SkuItem) => {
        const name = record.product_name ?? productNameById.get(String(record.product_id)) ?? '-'
        return name === '-' ? (
          '-'
        ) : (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '主条码',
      key: 'primary_barcode',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: SkuItem) => {
        const codes = record.barcodes ?? []
        if (codes.length === 0) return '-'
        const primary = codes.find((b) => b.is_primary) ?? codes[0]
        const all = codes.map((b) => b.barcode).join('、')
        return (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: all }}>
            {primary.barcode}
          </Text>
        )
      },
    },
    {
      title: '状态',
      dataIndex: 'is_enabled',
      width: 90,
      align: 'center',
      render: (v: boolean) => <SfStatusTag status={v ? 'enabled' : 'disabled'} />,
    },
    {
      title: '二维码',
      key: 'qr',
      width: 100,
      align: 'center',
      render: (_: unknown, record: SkuItem) => {
        // 构造唯一点（qr-code.md §7.1）；非法编码降级 '-'，不中断整页渲染
        const payload = tryBuildSfqrSku(record.code)
        if (!payload) return '-'
        return (
          <Tooltip title={payload} placement="left">
            <span
              style={{ display: 'inline-block', lineHeight: 0, cursor: 'pointer', padding: 4 }}
              onClick={() => openDrawer(record)}
            >
              <QrCodeView value={payload} size={48} />
            </span>
          </Tooltip>
        )
      },
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 150,
      render: (_: unknown, record: SkuItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button type="link" size="small" onClick={() => openDrawer(record)}>
            详情
          </Button>
          {/* 打印入口 fail-closed（frontend.md §13.1）；批量归行选择，此处仅单打 */}
          {canPrint && (
            <Button
              type="link"
              size="small"
              onClick={() => openPrint([toSfQrSkuInfo(record, productNameById)])}
            >
              打印标签
            </Button>
          )}
        </span>
      ),
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="商品二维码"
        subtitle="SFQR 协议身份：一个 SKU 一个二维码，扫码直达 / 标签打印统一走打印任务（SFQR|1|SKU|<SKU 编码>）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU 编码 / 商品名称' },
            { name: 'enabled', label: '启停', control: 'select', options: ENABLED_FILTER_OPTIONS },
          ]}
          onSearch={(values) => {
            // SfSearchForm 下拉值为字符串，enabled 转布尔（后端 strconv.ParseBool）
            const raw = values as Record<string, unknown>
            const enabledRaw = raw['enabled']
            setParams({
              keyword: typeof raw['keyword'] === 'string' ? raw['keyword'] : undefined,
              enabled: enabledRaw === 'true' ? true : enabledRaw === 'false' ? false : undefined,
            })
            list.resetToFirstPage()
          }}
        />
        <SfTable<SkuItem>
          storageKey="qr-codes"
          rowKey="id"
          rowSelection={rowSelection}
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="暂无 SKU，可先到 SKU 管理页创建"
          emptyAction={
            <Button type="primary" size="small" onClick={() => navigate('/skus')}>
              前往 SKU 管理
            </Button>
          }
          bulkActions={bulkActions}
          scrollX={840}
        />
      </Card>

      {/* 详情抽屉（SfQrSkuInfo 结构兼容 SfQrPrintSku，primaryBarcode 为附加展示字段） */}
      <SfQrPreviewDrawer
        open={drawerOpen}
        sku={drawerSku ? toSfQrSkuInfo(drawerSku, productNameById) : null}
        onClose={() => setDrawerOpen(false)}
      />

      {/* 打印配置弹窗：批量（行选择快照）与单打（操作列）共用 */}
      <SfQrPrintModal open={printOpen} skus={printSkus} onClose={() => setPrintOpen(false)} />
    </div>
  )
}
