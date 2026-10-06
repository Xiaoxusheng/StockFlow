import { useMemo, useState } from 'react'
import { Button, message } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  inventoryApi,
  type InventoryChangeType,
  type InventoryId,
  type LedgerItem,
  type StockItem,
  type StockQuery,
  type StockSummary,
} from '@/api/inventory'
import { masterdataApi, OPTIONS_PAGE_SIZE, type SkuItem } from '@/api/masterdata'
import { binApi } from '@/api/warehouse'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { ScanInput } from '@/components/scanner/ScanInput'
import { useSmartScanNext } from '@/hooks/useSmartScanNext'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import './inventory.css'

/** 库存流水类型中文文案（api/inventory.ts InventoryChangeType = inventory_ledgers CHECK 值域） */
const CHANGE_TYPE_LABEL: Record<InventoryChangeType, string> = {
  INBOUND: '入库',
  OUTBOUND: '出库',
  TRANSFER_OUT: '移出',
  TRANSFER_IN: '移入',
  LOCK: '锁定',
  RELEASE: '释放',
  MOVE: '移库',
  INSPECT_PASS: '质检合格',
  INSPECT_DEFECTIVE: '质检不合格',
  ADJUST: '调整',
}

/** 汇总条七计数（api/inventory.ts StockSummary，对齐 GET /api/inventory/summary 的 snake_case DashboardSummary；失败呈「-」不拖垮整页） */
const SUMMARY_CELLS: Array<{ label: string; key: keyof StockSummary; danger?: boolean }> = [
  { label: 'SKU 数', key: 'sku_count' },
  { label: '库存总量', key: 'total_qty' },
  { label: '可用', key: 'available_qty' },
  { label: '锁定', key: 'locked_qty' },
  { label: '冻结', key: 'frozen_qty' },
  { label: '临期', key: 'near_expiry_qty', danger: true },
  { label: '异常', key: 'abnormal_qty', danger: true },
]

interface StockLabels {
  skuCode: string
  productName: string
  binCode: string
}

/**
 * Pad 库存查询页（frontend.md §20.2 卡片化 / §20.3 三栏 / §20.4 竖屏；唯一含真实端点的查询页）：
 * - 顶部汇总条：inventoryApi.stockSummary 七计数大字，失败「-」占位不拖垮整页（§9.5）；
 * - 横屏三栏：左=库存卡片列表（inventoryApi.stock，大字 total/available/locked），
 *   中=选中 SKU 商品信息 PadInfoCard，右=该 SKU 库存流水（inventoryApi.ledger）+ ScanInput 扫码定位
 *   （扫码作业优化 2026-10-06：resolve 识别 SKU/库位 → 过滤库存列表 = 定位库存）；
 * - 竖屏堆叠：汇总条 → 选中详情内联 → 库存卡列表 → PadActionBar [返回][扫码][异常]。
 * 五维定位以 ID 呈现（InventoryView 无联表编码，PC 端 SfInventoryTable 同口径），
 * SKU / 库位编码经基础资料 options 端点本地映射补充，映射失败降级为 ID，不造假数据。
 */
export default function PadInventoryPage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, messageContext] = message.useMessage()
  const [selected, setSelected] = useState<StockItem | null>(null)
  const [skuIdFilter, setSkuIdFilter] = useState<InventoryId | undefined>(undefined)
  /** 扫码定位库位过滤（StockQuery.bin_id 真实端点过滤，handler.go:230-245） */
  const [binIdFilter, setBinIdFilter] = useState<InventoryId | undefined>(undefined)

  // 汇总条（GET /api/inventory/summary snake_case）：失败呈「-」，不阻塞列表
  const summary = useQuery({
    queryKey: ['pad', 'inventory', 'summary'],
    queryFn: inventoryApi.stockSummary,
  })

  // SKU / 库位编码映射（基础资料 options 一次取全；失败降级为 ID 显示，不阻塞库存列表）
  const skuOptions = useQuery({
    queryKey: ['pad', 'inventory', 'sku-options'],
    queryFn: () => masterdataApi.skus.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const binOptions = useQuery({
    queryKey: ['pad', 'inventory', 'bin-options'],
    queryFn: () => binApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const skuMap = useMemo(() => {
    const map = new Map<string, SkuItem>()
    for (const sku of skuOptions.data?.items ?? []) map.set(String(sku.id), sku)
    return map
  }, [skuOptions.data])
  const binMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const bin of binOptions.data?.items ?? []) map.set(String(bin.id), bin.code)
    return map
  }, [binOptions.data])

  const stockList = usePagedList<StockItem, StockQuery>({
    queryKey: ['pad', 'inventory', 'stock'],
    fetch: (q) => inventoryApi.stock(q),
    params: useMemo<StockQuery>(
      () => ({ sku_id: skuIdFilter, bin_id: binIdFilter }),
      [skuIdFilter, binIdFilter],
    ),
    defaultPageSize: 20,
  })

  // 选中 SKU 的库存流水（真实端点 GET /api/inventory-ledgers 按 sku_id 过滤）
  const ledger = useQuery({
    queryKey: ['pad', 'inventory', 'ledger', String(selected?.sku_id ?? '')],
    queryFn: () => inventoryApi.ledger({ sku_id: selected!.sku_id, page: 1, pageSize: 10 }),
    enabled: selected != null,
  })
  const ledgerItems: LedgerItem[] = ledger.data?.items ?? []
  const ledgerTotal = ledger.data?.total ?? 0

  const labelsOf = (item: StockItem): StockLabels => {
    const sku = skuMap.get(String(item.sku_id))
    return {
      skuCode: sku?.code ?? `SKU #${String(item.sku_id)}`,
      productName: sku?.product_name ?? EMPTY_TEXT,
      binCode: binMap.get(String(item.bin_id)) ?? `#${String(item.bin_id)}`,
    }
  }
  const selectedLabels = selected ? labelsOf(selected) : null

  /**
   * 智能下一步（扫码作业优化 2026-10-06）：扫→判→定位库存。统一走 POST /api/scanner/resolve
   * 识别（前端不做业务解析），SKU → 按 sku_id 过滤、库位 → 按 bin_id 过滤（均为真实端点
   * 过滤，handler.go:230-245）；识别为单据 → 跳转对应作业页。resolve 请求失败（网络 /
   * 无 scanner:resolve:list 权限）时降级本地映射反查（数字按 SKU ID、文本按 SKU 编码），
   * 未命中如实提示。纯查询定位类低风险动作（autoAdvance='safe'）。
   */
  const { resolveNext } = useSmartScanNext({ context: 'inventory', page: '/pad/inventory' })

  const handleCodeSubmit = async (code: string) => {
    const value = code.trim()
    if (!value) return
    const { directive } = await resolveNext(value)
    if (directive.kind === 'locate-item' && directive.objectType !== 'sku') {
      // 批次 / 序列号：Pad 库存页只做 SKU / 库位定位，不做伪定位（scanner.md §7.13 序列号
      // 追溯与批次分布归 PC 追溯页 / Scan 端承接）
      messageApi.info(`${directive.message}；Pad 库存页支持 SKU / 库位定位，批次与序列号请至 PC「库存追溯」查询`)
      return
    }
    if (directive.kind === 'locate-item' || directive.kind === 'locate-bin') {
      const isBin = directive.kind === 'locate-bin'
      setSkuIdFilter(isBin ? undefined : directive.id)
      setBinIdFilter(isBin ? directive.id : undefined)
      setSelected(null)
      stockList.resetToFirstPage()
      messageApi.success(`${directive.message}：已过滤库存列表`)
      return
    }
    if (directive.kind === 'navigate') {
      messageApi.success(directive.message)
      navigate(directive.path)
      return
    }
    if (directive.kind === 'resolve-failed') {
      // 降级：resolve 不可用（网络/权限）时按既有本地映射反查，不虚构识别结果
      if (/^\d+$/.test(value)) {
        setSkuIdFilter(value)
        setBinIdFilter(undefined)
        setSelected(null)
        stockList.resetToFirstPage()
        messageApi.info(`${directive.message}；已按本地兜底以 SKU ID ${value} 过滤库存列表`)
        return
      }
      const hit = [...skuMap.values()].find((sku) => sku.code === value)
      if (hit) {
        setSkuIdFilter(String(hit.id))
        setBinIdFilter(undefined)
        setSelected(null)
        stockList.resetToFirstPage()
        messageApi.info(`${directive.message}；已按本地兜底以 SKU 编码 ${hit.code} 过滤库存列表`)
        return
      }
    }
    messageApi.warning(directive.message)
  }

  const summaryNode = (
    <section className="sf-pad-card" aria-label="库存汇总">
      <h3 className="sf-pad-card-title">库存汇总</h3>
      <div className="sf-pad-inv-summary">
        {SUMMARY_CELLS.map((cell) => (
          <div
            key={cell.key}
            className={`sf-pad-inv-summary-cell${cell.danger ? ' sf-pad-inv-summary-cell--danger' : ''}`}
          >
            <span className={summary.data ? 'sf-pad-metric' : 'sf-pad-info-value'}>
              {summary.data ? formatNumber(summary.data[cell.key]) : EMPTY_TEXT}
            </span>
            <span className="sf-pad-info-label">{cell.label}</span>
          </div>
        ))}
      </div>
      {summary.error && (
        <p className="sf-pad-muted-note">
          库存汇总接口暂不可用，数值以「-」占位，不影响库存列表
        </p>
      )}
    </section>
  )

  const totalPage = Math.max(1, Math.ceil(stockList.total / stockList.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-tasks-cards">
        {stockList.isPending ? (
          <SfLoading rows={6} />
        ) : stockList.error ? (
          <SfError error={stockList.error} onRetry={() => void stockList.refetch()} />
        ) : stockList.items.length === 0 ? (
          <SfEmpty
            description={
              skuIdFilter || binIdFilter
                ? '当前过滤条件下没有库存记录，试试重新扫码或清除过滤'
                : '当前仓库暂无库存记录'
            }
          />
        ) : (
          stockList.items.map((item) => {
            const labels = labelsOf(item)
            const isSelected = selected != null && String(selected.id) === String(item.id)
            return (
              <div
                key={String(item.id)}
                role="button"
                tabIndex={0}
                aria-pressed={isSelected}
                className={`sf-pad-stock-card${isSelected ? ' sf-pad-stock-card--selected' : ''}`}
                onClick={() => setSelected(item)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    setSelected(item)
                  }
                }}
              >
                <div className="sf-pad-stock-card__head">
                  <span>{labels.skuCode}</span>
                  {String(item.batch_id) !== '0' && (
                    <span className="sf-pad-info-label">批次 #{String(item.batch_id)}</span>
                  )}
                </div>
                <div className="sf-pad-stock-card__loc">
                  仓 #{String(item.warehouse_id)} · 区{' '}
                  {String(item.zone_id) === '0' ? EMPTY_TEXT : `#${String(item.zone_id)}`} · 架{' '}
                  {String(item.shelf_id) === '0' ? EMPTY_TEXT : `#${String(item.shelf_id)}`} · 位 {labels.binCode}
                </div>
                <div className="sf-pad-stock-card__qty-row">
                  <span className="sf-pad-stock-card__qty-item">
                    <span className="sf-pad-metric">{formatNumber(item.total_qty)}</span>
                    <span className="sf-pad-stock-card__qty-label">总量</span>
                  </span>
                  <span className="sf-pad-stock-card__qty-item">
                    <span className="sf-pad-stock-card__qty-strong">{formatNumber(item.available_qty)}</span>
                    <span className="sf-pad-stock-card__qty-label">可用</span>
                  </span>
                  <span className="sf-pad-stock-card__qty-item">
                    <span className="sf-pad-stock-card__qty-strong">{formatNumber(item.locked_qty)}</span>
                    <span className="sf-pad-stock-card__qty-label">锁定</span>
                  </span>
                </div>
                <div className="sf-pad-stock-card__qty-sub">
                  冻结 {formatNumber(item.frozen_qty)} · 待检 {formatNumber(item.pending_inspect_qty)} · 不良{' '}
                  {formatNumber(item.defective_qty)}
                </div>
              </div>
            )
          })
        )}
      </div>
      <div className="sf-pad-tasks-pager">
        <span className="sf-pad-info-label">
          共 {stockList.total} 条 · 第 {stockList.pagination.current}/{totalPage} 页
        </span>
        <div className="sf-pad-pager-btns">
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={stockList.pagination.current <= 1}
            onClick={() => stockList.onPageChange(stockList.pagination.current - 1, stockList.pagination.pageSize)}
          >
            上一页
          </Button>
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={stockList.pagination.current >= totalPage}
            onClick={() => stockList.onPageChange(stockList.pagination.current + 1, stockList.pagination.pageSize)}
          >
            下一页
          </Button>
        </div>
      </div>
    </div>
  )

  const detailNode = selectedLabels ? (
    <PadInfoCard
      title={`商品信息 · ${selectedLabels.skuCode}`}
      items={[
        { label: 'SKU 编码', value: selectedLabels.skuCode },
        { label: '商品名称', value: selectedLabels.productName },
        { label: 'SKU ID', value: String(selected!.sku_id) },
        {
          label: '批次',
          value: String(selected!.batch_id) === '0' ? '非批次' : `#${String(selected!.batch_id)}`,
        },
        { label: '仓库 ID', value: String(selected!.warehouse_id) },
        {
          label: '库区 ID',
          value: String(selected!.zone_id) === '0' ? EMPTY_TEXT : String(selected!.zone_id),
        },
        {
          label: '货架 ID',
          value: String(selected!.shelf_id) === '0' ? EMPTY_TEXT : String(selected!.shelf_id),
        },
        { label: '库位', value: selectedLabels.binCode },
        { label: '总库存', value: formatNumber(selected!.total_qty), emphasis: true },
        { label: '可用', value: formatNumber(selected!.available_qty), emphasis: true },
        { label: '锁定', value: formatNumber(selected!.locked_qty) },
        { label: '冻结', value: formatNumber(selected!.frozen_qty) },
        { label: '待质检', value: formatNumber(selected!.pending_inspect_qty) },
        { label: '不良', value: formatNumber(selected!.defective_qty) },
        { label: '更新时间', value: formatDateTime(selected!.updated_at) },
      ]}
    />
  ) : (
    <SfEmpty description="从左侧选择一张库存卡片，此处展示该 SKU 的商品与五维定位信息" />
  )

  const ledgerNode = (
    <section className="sf-pad-card" aria-label="库存流水">
      <h3 className="sf-pad-card-title">库存流水{selectedLabels ? ` · ${selectedLabels.skuCode}` : ''}</h3>
      {!selected ? (
        <SfEmpty description="选中库存卡片后展示该 SKU 的库存流水（GET /api/inventory-ledgers）" />
      ) : ledger.isPending ? (
        <SfLoading rows={4} />
      ) : ledger.error ? (
        <SfError error={ledger.error} onRetry={() => void ledger.refetch()} />
      ) : ledgerItems.length === 0 ? (
        <SfEmpty description="该 SKU 暂无库存流水记录" />
      ) : (
        <div className="sf-pad-ledger-list">
          {ledgerItems.map((row) => (
            <div key={String(row.id)} className="sf-pad-ledger-card">
              <div className="sf-pad-ledger-card__head">
                <span>{CHANGE_TYPE_LABEL[row.change_type] ?? row.change_type}</span>
                <span
                  className={`sf-pad-ledger-card__delta${
                    row.qty_change > 0
                      ? ' sf-pad-ledger-card__delta--in'
                      : row.qty_change < 0
                        ? ' sf-pad-ledger-card__delta--out'
                        : ''
                  }`}
                >
                  {row.qty_change > 0 ? '+' : ''}
                  {formatNumber(row.qty_change)}
                </span>
              </div>
              <div className="sf-pad-ledger-card__meta">
                <span>
                  {formatNumber(row.qty_before)} → {formatNumber(row.qty_after)}
                </span>
                <span>库位 #{String(row.bin_id)}</span>
                {row.business_no && <span>{row.business_no}</span>}
                <span>{row.operator_name || EMPTY_TEXT}</span>
              </div>
              <div className="sf-pad-ledger-card__meta">
                <span>{row.ledger_no}</span>
                <span>{formatDateTime(row.created_at)}</span>
              </div>
            </div>
          ))}
          {ledgerTotal > ledgerItems.length && (
            <p className="sf-pad-muted-note">
              仅展示最近 {ledgerItems.length} 条，共 {formatNumber(ledgerTotal)} 条流水
            </p>
          )}
        </div>
      )}
    </section>
  )

  const scanNode = (
    <section className="sf-pad-card" aria-label="扫码查询">
      <h3 className="sf-pad-card-title">扫码查询</h3>
      <ScanInput
        autoFocus={false}
        autoAdvance="safe"
        placeholder="扫入 SKU / 库位条码（HID 扫码枪或手工输入）"
        hint="识别 SKU → 过滤库存列表；识别库位 → 按库位过滤库存"
        onScan={(code) => void handleCodeSubmit(code)}
      />
      <p className="sf-pad-muted-note">
        识别经 POST /api/scanner/resolve（前端不做业务解析，scanner.md §5.3）；纯查询定位低风险动作
      </p>
    </section>
  )

  const actionBar = <PadActionBar hideSlots={['pause', 'finish']} onScanSubmit={handleCodeSubmit} />

  return (
    <>
      {messageContext}
      {orientation === 'landscape' ? (
        // 横屏：顶部汇总条（全宽，失败「-」不拖垮整页）+ 三栏（左库存卡片列表 / 中商品信息 / 右流水 + 扫码占位）
        <>
          {summaryNode}
          <PadPageShell
            ratios={[34, 30, 36]}
            tasksSlot={listNode}
            contentSlot={detailNode}
            actionSlot={
              <>
                {ledgerNode}
                {scanNode}
              </>
            }
          />
          {actionBar}
        </>
      ) : (
        // 竖屏堆叠：汇总条 → 选中详情内联 → 库存卡列表（规格：顶部汇总条 → 卡列表滚动区 → 底部操作栏）
        <>
          <PadPageShell
            ratios={[34, 30, 36]}
            tasksSlot={
              <>
                {summaryNode}
                {selected && (
                  <div className="sf-pad-tasks-detail">
                    {detailNode}
                    {ledgerNode}
                  </div>
                )}
                {listNode}
              </>
            }
            contentSlot={null}
          />
          {actionBar}
        </>
      )}
    </>
  )
}
