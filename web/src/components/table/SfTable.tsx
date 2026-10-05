import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Button,
  Checkbox,
  ConfigProvider,
  Dropdown,
  Popover,
  Skeleton,
  Space,
  Table,
  Tooltip,
  theme,
} from 'antd'
import {
  ColumnHeightOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  ReloadOutlined,
  SettingOutlined,
} from '@ant-design/icons'
import type { TableProps, ThemeConfig } from 'antd'
import type { Key, ReactNode } from 'react'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SF_MOTION_MS } from '@/styles/motion'
import { SfToolbar } from './SfToolbar'

type Density = 'small' | 'middle' | 'large'

const DENSITY_LABEL: Record<Density, string> = {
  small: '紧凑',
  middle: '默认',
  large: '舒适',
}

function columnKey(column: NonNullable<TableProps<Record<string, unknown>>['columns']>[number]): string | null {
  const key = (column as { key?: Key }).key
  if (typeof key === 'string') return key
  const dataIndex = (column as { dataIndex?: unknown }).dataIndex
  if (typeof dataIndex === 'string') return dataIndex
  if (Array.isArray(dataIndex) && typeof dataIndex[0] === 'string') return dataIndex[0]
  return null
}

/** 任务书 §18 对齐兜底（2026-10-05 设计复审面5）+ 表格动效 #2 操作列注入（frontend.md §31）：
 * 未显式给 align 时按列语义补默认——操作列（title「操作」或 dataIndex 'operation'）→ right；
 * 状态列（title 以「状态」结尾或 dataIndex 以 status 结尾，如 status / occupancy_status / qc_status）→ center。
 * 动效：操作列一律注入 sf-table-actions-cell（行 hover 渐显 / 触摸端恒显）——注入先于 align 早退，
 * 显式 align:'right' 的操作列同样生效，全站行为一致；仅按顶层列判定（分组子列不递归，站内无此形态）。 */
function applyAlignDefaults<T extends object>(
  columns: NonNullable<TableProps<T>['columns']>,
): NonNullable<TableProps<T>['columns']> {
  return columns.map((column) => {
    const title = typeof column.title === 'string' ? column.title : ''
    const raw = (column as { dataIndex?: unknown }).dataIndex
    const dataKey =
      typeof raw === 'string' ? raw : Array.isArray(raw) ? raw.map(String).join('.') : ''
    const isOperation = title === '操作' || dataKey === 'operation'
    const next = isOperation
      ? ({
          ...column,
          className: [column.className, 'sf-table-actions-cell'].filter(Boolean).join(' '),
        } as typeof column)
      : column
    if (next.align) return next
    if (isOperation) {
      return { ...next, align: 'right' } as typeof column
    }
    if (/状态$/.test(title) || /(?:^|_)status$/.test(dataKey)) {
      return { ...next, align: 'center' } as typeof column
    }
    return next
  })
}

export interface SfTableProps<T extends object> extends TableProps<T> {
  columns: TableProps<T>['columns']
  /** page=独立列表页（默认：工具栏+分页）；nested=详情页/抽屉内嵌表（仅统一密度/空态/错误态/首载骨架） */
  variant?: 'page' | 'nested'
  /** 受控分页（配合 usePagedList）；current 缺省走 antd 非受控内部分页（nested 本地数据表）；整体缺省时关闭分页 */
  pagination?: { current?: number; pageSize: number }
  total?: number
  onPageChange?: (page: number, pageSize: number) => void
  loading?: boolean
  /** 传入时整表切换为统一错误态（保留工具栏可刷新） */
  error?: unknown
  onRetry?: () => void
  onRefresh?: () => void
  /** 工具栏左侧动作区（新建/导入/导出等） */
  actions?: ReactNode
  /** 上下文空态文案（frontend.md §9.2） */
  emptyText?: string
  /** 列显示与密度持久化 key（frontend.md §26.3） */
  storageKey?: string
  /** 横向滚动基准宽度：全部可见列均显式声明 width 时自动按「列宽总和+10」推导（2026-10-06），
   * 本值仅在存在未声明 width 的弹性列时作为其宽度预留使用；有固定列的表仍建议声明，
   * 保证窄屏横向滚动可用 */
  scrollX?: number
  /** 表格边框（frontend.md §6.2）：默认开启全边框（外框+内格线，颜色走 antd colorBorderSecondary，
   * App.tsx 已映射 --sf-border 同值，Dark 自适应）；传 false 回退无边框形态 */
  bordered?: boolean
  showToolbar?: boolean
  showDensity?: boolean
  showColumnSetting?: boolean
  showFullscreen?: boolean
  /** 动效 #4 批量工具栏（frontend.md §31）：选中>0 时左区自动切换「已选择 N 条 + 批量操作」，需受控 rowSelection */
  bulkActions?: ReactNode
  /** 动效 #8 空态 CTA（配合 emptyText，经 SfEmpty footer 区渲染） */
  emptyAction?: ReactNode
  /** 动效 #7 行反馈：等于 rowKey 值的行加淡色底（先 API 后反馈，配合 useTableRowFeedback）；
   * 接受 null——与 hook 的 useState<Key | null> 返回值对齐（页面直传 fb.rowKey），null=无反馈 */
  feedbackRowKey?: Key | null
  /** 行反馈色调，默认 'success' */
  feedbackTone?: 'success' | 'error'
  /** 删除行动效：fade→收缩→隐藏；传正在删除的 rowKey 数组，260ms 后内部过滤 DOM，dataSource 变化自愈，
   * 服务器分页不受影响；多行并行删除逐 key 独立定时互不中断（与 useTableRowFeedback.removingRowKeys 对齐），
   * 空数组/缺省=无 */
  removingRowKeys?: Key[]
  /** 批量面板「清空」覆盖实现（默认调 rowSelection.onChange([], [], { type: 'none' })） */
  onClearSelection?: () => void
}

/**
 * 统一 DataTable（frontend.md §6.1）：
 * 分页 / 密度 / 列显示 / 全屏 / 刷新 / 上下文空态 / 统一错误态。
 * 数据获取不在这里做——页面通过 usePagedList + TanStack Query 驱动。
 *
 * 列对齐契约（任务书 §18）：文字左（默认）/ 数字金额 align:'right' / 状态 align:'center' /
 * 操作列 align:'right' 固定——数字由页面在 columns 上显式声明；状态/操作列页面普遍漏声明
 * （2026-10-05 设计复审面5：31 操作列 0 显式 align），由本组件按列语义兜底（applyAlignDefaults），
 * 页面显式声明的 align 永不被覆盖。
 *
 * 表格动效体系（frontend.md §31）：#1 骨架→数据淡入（骨架行数随 pageSize）/#2 行 hover 与操作区渐显 /
 * #3 勾选缩放与选中底 / #4 批量工具栏（bulkActions + 受控 rowSelection 自动驱动）/#5 刷新图标自旋绑定 loading /
 * #6 数据更新轻 opacity / #7 行反馈（feedbackRowKey + feedbackTone，先 API 后反馈）/#8 空态进场（emptyAction）/
 * 删除行（removingRowKeys：fade→收缩→过滤 DOM，多行并行互不中断，total/分页不动）。时长/缓动/颜色全走 Token，
 * 作用域类与 reduced-motion 见 styles/global.css「表格动效体系」节。
 */
export function SfTable<T extends object>({
  columns,
  pagination,
  total,
  onPageChange,
  loading,
  error,
  onRetry,
  onRefresh,
  actions,
  emptyText,
  storageKey,
  scrollX,
  variant = 'page',
  showToolbar = true,
  showDensity = true,
  showColumnSetting = true,
  showFullscreen = true,
  // 表格动效体系（frontend.md §31）：以下全部在 {...rest} 之前解构——rowSelection/dataSource/
  // rowClassName/rowKey 显式回传 antd Table，新 props 永不透传进 rest（兼作 TS rest 纯化）
  rowSelection,
  dataSource,
  rowClassName: pageRowClassName,
  rowKey,
  bulkActions,
  emptyAction,
  feedbackRowKey,
  feedbackTone = 'success',
  removingRowKeys,
  bordered = true,
  onClearSelection: onClearSelectionProp,
  ...rest
}: SfTableProps<T>) {
  const { token } = theme.useToken()
  const wrapperRef = useRef<HTMLDivElement>(null)
  const [fullscreen, setFullscreen] = useState(false)
  // 任务书 §23：密度三档 紧凑/默认/舒适，WMS 默认紧凑
  const [density, setDensity] = useState<Density>(
    () => readStored<Density>(storageKey, 'density') ?? 'small',
  )
  const [hiddenKeys, setHiddenKeys] = useState<string[]>(
    () => readStored<string[]>(storageKey, 'hidden-columns') ?? [],
  )

  // 动效 #1/#6：loading 归一化（类型上仅 boolean，运行时防御个别调用方传 SpinProps 对象形态）
  const isSpinning =
    typeof loading === 'object' && loading !== null
      ? (loading as { spinning?: unknown }).spinning !== false
      : Boolean(loading)
  // dataSource 传入时才做 loading 三态接管；未传保持旧行为（loading 原样透传 antd，向后兼容）
  const hasData = Array.isArray(dataSource) ? dataSource.length > 0 : undefined
  // 首载骨架与刷新压暗期间不再叠加 antd Spin 蒙层（暗色下亮色蒙层叠加尤脏）
  const suppressSpinOverlay = hasData !== undefined && isSpinning
  // 动效 #1：骨架行数跟随当前 pageSize（cap 20 防 pageSize=100 骨架墙）；nested 无 pagination 保持 5
  const skeletonRows = Math.min(pagination?.pageSize ?? 5, 20)

  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === wrapperRef.current)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

  // 动效 #5：刷新图标自旋——绑定归一化 loading 起停，最短保持 refreshMinHold 防闪烁，不无限转
  const [refreshSpinning, setRefreshSpinning] = useState(false)
  const refreshActiveRef = useRef(false)
  const refreshStartRef = useRef(0)
  const refreshStopTimerRef = useRef<number>()

  useEffect(() => {
    if (isSpinning) {
      if (refreshStopTimerRef.current !== undefined) {
        window.clearTimeout(refreshStopTimerRef.current)
        refreshStopTimerRef.current = undefined
      }
      refreshActiveRef.current = true
      refreshStartRef.current = Date.now()
      setRefreshSpinning(true)
      return
    }
    if (!refreshActiveRef.current) return
    refreshActiveRef.current = false
    const elapsed = Date.now() - refreshStartRef.current
    const remain = Math.max(0, SF_MOTION_MS.refreshMinHold - elapsed)
    refreshStopTimerRef.current = window.setTimeout(() => {
      setRefreshSpinning(false)
      refreshStopTimerRef.current = undefined
    }, remain)
  }, [isSpinning])

  useEffect(
    () => () => {
      if (refreshStopTimerRef.current !== undefined) window.clearTimeout(refreshStopTimerRef.current)
    },
    [],
  )

  const toggleFullscreen = () => {
    if (document.fullscreenElement === wrapperRef.current) {
      void document.exitFullscreen()
    } else {
      void wrapperRef.current?.requestFullscreen()
    }
  }

  const identityColumns = useMemo(
    () =>
      (columns ?? [])
        .map((column) => ({ column, key: columnKey(column as never) }))
        .filter((entry): entry is { column: NonNullable<TableProps<T>['columns']>[number]; key: string } =>
          Boolean(entry.key),
        ),
    [columns],
  )

  const visibleColumns = useMemo(
    () =>
      applyAlignDefaults(columns ?? []).filter((column) => {
        const key = columnKey(column as never)
        return !key || !hiddenKeys.includes(key)
      }),
    [columns, hiddenKeys],
  )

  // scroll.x 自适应（2026-10-06）：全部可见列均显式声明 width 时按「列宽总和+10」推导——
  // 页面声明的 scrollX 普遍虚高（机动余量 80~200），视口本可容纳时提前出横向滚动条
  // （用户反馈：仓库列表内容区 ~1390 vs 声明 1490）；min-width:100% 保证宽屏仍撑满容器，
  // 仅「真正放不下」才出滚动条。存在未声明 width 的弹性列时仍用声明值（预留语义）；
  // 列设置隐藏列后随可见列实时收缩。
  const resolvedScrollX = useMemo(() => {
    if (scrollX === undefined) return undefined
    const cols = visibleColumns as Array<{ width?: unknown }>
    if (cols.length === 0 || !cols.every((c) => typeof c.width === 'number')) return scrollX
    return cols.reduce<number>((acc, c) => acc + (c.width as number), 0) + 10
  }, [scrollX, visibleColumns])

  // 动效 #4：批量工具栏——读受控 rowSelection，selectedRowKeys>0 由 SfToolbar 自动切换批量面板
  const selectedRowKeys =
    rowSelection && Array.isArray(rowSelection.selectedRowKeys) ? rowSelection.selectedRowKeys : undefined
  const selectedKeySet = useMemo(
    () => (selectedRowKeys ? new Set<Key>(selectedRowKeys) : undefined),
    [selectedRowKeys],
  )

  // 行 key 解析复用 rowKey（函数形式直接调用，字符串/keyof 形式取 record[rowKey]）
  const rowKeyGetter = useMemo(() => {
    if (typeof rowKey === 'function') return rowKey
    return (record: T): Key => (record as unknown as Record<string, unknown>)[rowKey as string] as Key
  }, [rowKey])

  // 动效 #3/#7/删除行：合成行类名——页面 rowClassName（全站此前零使用，若传则前置拼接）+
  // sf-table-row 与 --selected / --feedback-(success|error) / --removing 修饰。
  // 每行一次字符串拼接不建对象，20~100 行无压力；selectedKeySet/removingKeySet 每渲染至多构建一次。
  const removingKeySet = useMemo(() => new Set(removingRowKeys ?? []), [removingRowKeys])
  const composedRowClassName = useCallback(
    (record: T, index: number, indent: number): string => {
      let className = 'sf-table-row'
      const pageClass =
        typeof pageRowClassName === 'function' ? pageRowClassName(record, index, indent) : pageRowClassName
      if (pageClass) className += ` ${pageClass}`
      const key = rowKeyGetter(record)
      if (selectedKeySet?.has(key)) className += ' sf-table-row--selected'
      if (feedbackRowKey != null && key === feedbackRowKey) {
        className +=
          feedbackTone === 'error' ? ' sf-table-row--feedback-error' : ' sf-table-row--feedback-success'
      }
      if (removingKeySet.has(key)) className += ' sf-table-row--removing'
      return className
    },
    [pageRowClassName, rowKeyGetter, selectedKeySet, feedbackRowKey, feedbackTone, removingKeySet],
  )

  // 删除行动效（frontend.md §31）：removingRowKeys 逐 key 挂 --removing 类播 fade→收缩两段动画，
  // 260ms（rowRemoveCollapse）后过滤该行 DOM——仅视图层过滤，total/pagination 全程不动；
  // refetch 先回（dataSource 已不含该行）→ 自愈提前清除；refetch 失败 → hook 2.5s 兜底恢复显示。
  // 多行并行删除：逐 key 定时记账于 Map（重渲染不重置已排程 key，260ms 窗口内连删互不中断）。
  const [collapsedKeys, setCollapsedKeys] = useState<Key[]>([])
  const collapsedKeySet = useMemo(() => new Set(collapsedKeys), [collapsedKeys])
  const collapseTimersRef = useRef(new Map<Key, number>())

  useEffect(() => {
    removingKeySet.forEach((key) => {
      if (collapseTimersRef.current.has(key)) return
      collapseTimersRef.current.set(
        key,
        window.setTimeout(() => {
          collapseTimersRef.current.delete(key)
          setCollapsedKeys((prev) => (prev.includes(key) ? prev : [...prev, key]))
        }, SF_MOTION_MS.rowRemoveCollapse),
      )
    })
  }, [removingKeySet])

  useEffect(
    () => () => {
      collapseTimersRef.current.forEach((timer) => window.clearTimeout(timer))
      collapseTimersRef.current.clear()
    },
    [],
  )

  // 收场：key 从 removing 集合摘除（hook 2.5s 兜底）或 refetch 后 dataSource 已不含该行时，
  // 同步从 collapsed 集合清除；函数式 setState 内容不变时返回原引用，避免 effect 空转成环。
  useEffect(() => {
    if (collapsedKeys.length === 0) return
    setCollapsedKeys((prev) => {
      const next = prev.filter(
        (key) =>
          removingKeySet.has(key) &&
          (dataSource ? dataSource.some((item) => rowKeyGetter(item) === key) : true),
      )
      return next.length === prev.length ? prev : next
    })
  }, [removingKeySet, collapsedKeys, dataSource, rowKeyGetter])

  const displayData = useMemo(() => {
    if (collapsedKeySet.size === 0 || !dataSource) return dataSource
    return dataSource.filter((item) => !collapsedKeySet.has(rowKeyGetter(item)))
  }, [collapsedKeySet, dataSource, rowKeyGetter])

  // 动效 #4：清空选择——默认走 rowSelection.onChange 空数组（antd 6 RowSelectMethod 含 'none'，
  // antd/es/table/interface.d.ts:153 实证）；页面可用 onClearSelection 覆盖
  const handleClearSelection = useCallback(() => {
    if (onClearSelectionProp) {
      onClearSelectionProp()
      return
    }
    rowSelection?.onChange?.([], [], { type: 'none' })
  }, [onClearSelectionProp, rowSelection])

  const updateDensity = (value: Density) => {
    setDensity(value)
    writeStored(storageKey, 'density', value)
  }

  const toggleColumn = (key: string, checked: boolean) => {
    setHiddenKeys((prev) => {
      const next = checked ? prev.filter((k) => k !== key) : [...new Set([...prev, key])]
      writeStored(storageKey, 'hidden-columns', next)
      return next
    })
  }

  const resetColumns = () => {
    setHiddenKeys([])
    writeStored(storageKey, 'hidden-columns', [])
  }

  const iconStyle = { fontSize: 15, color: token.colorTextSecondary }

  /** 任务书 §18：数据 13px（13~14）、表头 12~13（继承同值）；
   * 行高经密度分档 padding 落地——紧凑 10px（≈41~43px，40~44）、默认 12px（≈45~47px，44~48）。
   * antd small 档默认 8px 行高不足 40，故校正。嵌套主题与 App.tsx 按组件合并，暗色算法继承。 */
  const tableTheme = useMemo<ThemeConfig>(
    () => ({
      components: {
        Table: {
          cellFontSize: 13,
          cellFontSizeMD: 13,
          cellFontSizeSM: 13,
          ...(density === 'small'
            ? { cellPaddingBlockSM: 10 }
            : density === 'middle'
              ? { cellPaddingBlockMD: 12 }
              : {}),
        },
      },
    }),
    [density],
  )

  const viewTools = (
    <Space size={0}>
      {onRefresh && (
        <Tooltip title="刷新">
          <Button
            type="text"
            size="small"
            className="sf-table-refresh"
            icon={
              <ReloadOutlined
                style={iconStyle}
                className={refreshSpinning ? 'sf-table-refresh__icon--spin' : undefined}
              />
            }
            onClick={onRefresh}
          />
        </Tooltip>
      )}
      {showColumnSetting && identityColumns.length > 0 && (
        <Popover
          trigger="click"
          placement="bottomRight"
          content={
            <div style={{ minWidth: 160 }}>
              <div
                style={{
                  maxHeight: 320,
                  overflowY: 'auto',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 'var(--sf-space-2)',
                }}
              >
                {identityColumns.map(({ key, column }) => (
                  <Checkbox
                    key={key}
                    checked={!hiddenKeys.includes(key)}
                    onChange={(e) => toggleColumn(key, e.target.checked)}
                  >
                    {renderColumnTitle<T>(column)}
                  </Checkbox>
                ))}
              </div>
              {/* 任务书 §22：列设置支持显示/隐藏 + 恢复默认（排序维持列定义顺序，不做拖拽） */}
              <div
                style={{
                  marginTop: 'var(--sf-space-2)',
                  paddingTop: 'var(--sf-space-1)',
                  borderTop: '1px solid var(--sf-border-subtle)',
                  textAlign: 'center',
                }}
              >
                <Button
                  type="link"
                  size="small"
                  disabled={hiddenKeys.length === 0}
                  onClick={resetColumns}
                >
                  恢复默认
                </Button>
              </div>
            </div>
          }
        >
          <Button
            type="text"
            size="small"
            icon={<SettingOutlined style={iconStyle} />}
          />
        </Popover>
      )}
      {showDensity && (
        <Dropdown
          menu={{
            selectable: true,
            selectedKeys: [density],
            items: (['small', 'middle', 'large'] as Density[]).map((d) => ({
              key: d,
              label: DENSITY_LABEL[d],
            })),
            onClick: ({ key }) => updateDensity(key as Density),
          }}
        >
          <Tooltip title="密度">
            <Button type="text" size="small" icon={<ColumnHeightOutlined style={iconStyle} />} />
          </Tooltip>
        </Dropdown>
      )}
      {showFullscreen && (
        <Tooltip title={fullscreen ? '退出全屏' : '全屏'}>
          <Button
            type="text"
            size="small"
            icon={
              fullscreen ? (
                <FullscreenExitOutlined style={iconStyle} />
              ) : (
                <FullscreenOutlined style={iconStyle} />
              )
            }
            onClick={toggleFullscreen}
          />
        </Tooltip>
      )}
    </Space>
  )

  return (
    <div
      ref={wrapperRef}
      // 动效作用域锚点：.sf-table 全部子样式作用域；.sf-table-data-loading loading 为真即挂
      //（首载骨架 + 刷新共用：tbody 压暗 0.8，数据到达摘除 → 0.8→1 淡入 180ms，见 global.css）
      className={['sf-table', variant === 'nested' && 'sf-table--nested', isSpinning && 'sf-table-data-loading']
        .filter(Boolean)
        .join(' ')}
      style={fullscreen ? { background: token.colorBgLayout, padding: 'var(--sf-space-4)' } : undefined}
    >
      {(showToolbar && variant === 'page') && (
        <SfToolbar
          extra={viewTools}
          selectedCount={bulkActions ? (selectedRowKeys?.length ?? 0) : undefined}
          bulkActions={bulkActions}
          onClearSelection={handleClearSelection}
        >
          {actions}
        </SfToolbar>
      )}
      {error !== undefined && error !== null ? (
        <SfError error={error} onRetry={onRetry ?? onRefresh} />
      ) : (
        <ConfigProvider theme={tableTheme}>
          <Table<T>
            size={density}
            bordered={bordered}
            columns={visibleColumns}
            rowKey={rowKey}
            rowSelection={rowSelection}
            dataSource={displayData}
            rowClassName={composedRowClassName}
            // 动效 #1/#5/#6：dataSource 传入时接管 antd Spin——首载骨架与刷新压暗均不叠加 Spin 蒙层；
            // dataSource 未传（防御形态）保持 loading 原样透传，旧行为兼容
            loading={suppressSpinOverlay ? false : loading}
            locale={{
              // 任务书 §31：首载（loading 且无数据）以骨架条占位保持表头结构，不闪空态，
              // 行数跟随当前 pageSize；数据到达后 data-loading 类摘除 → tbody 0.8→1 淡入（动效 #1）。
              // 刷新（已有数据）走压暗 + 刷新图标自旋（动效 #6/#5），不再叠加 Spin 蒙层。
              emptyText: () =>
                isSpinning ? (
                  <SfTableSkeleton rows={skeletonRows} />
                ) : (
                  <SfEmpty
                    description={emptyText}
                    action={emptyAction}
                    className="sf-table-empty"
                  />
                ),
            }}
            pagination={
              pagination
                ? {
                    ...pagination,
                    total: total ?? 0,
                    showSizeChanger: true,
                    showQuickJumper: true,
                    pageSizeOptions: [10, 20, 50, 100],
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: onPageChange,
                  }
                : false
            }
            scroll={resolvedScrollX !== undefined ? { x: resolvedScrollX } : undefined}
            {...rest}
          />
        </ConfigProvider>
      )}
    </div>
  )
}

function readStored<T>(storageKey: string | undefined, field: string): T | null {
  if (!storageKey) return null
  try {
    const raw = localStorage.getItem(`sf.table.${storageKey}.${field}`)
    return raw ? (JSON.parse(raw) as T) : null
  } catch {
    return null
  }
}

/** 首载骨架（任务书 §31）：等宽骨架条模拟行结构，表头由 Table 本体保持；行数跟随当前 pageSize */
function SfTableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div style={{ padding: 'var(--sf-space-3) 0' }} aria-busy>
      <Skeleton active title={false} paragraph={{ rows, width: '100%' }} />
    </div>
  )
}

function writeStored(storageKey: string | undefined, field: string, value: unknown) {
  if (!storageKey) return
  try {
    localStorage.setItem(`sf.table.${storageKey}.${field}`, JSON.stringify(value))
  } catch {
    // 存储不可用时静默降级为会话内状态
  }
}

function renderColumnTitle<T extends object>(
  column: NonNullable<TableProps<T>['columns']>[number],
): ReactNode {
  const title = (column as { title?: unknown }).title
  if (typeof title === 'function') return null
  return (title as ReactNode) ?? null
}
