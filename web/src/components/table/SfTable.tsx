import { useEffect, useMemo, useRef, useState } from 'react'
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

/** 任务书 §18 对齐兜底（2026-10-05 设计复审面5）：数字 Right 页面已广泛显式声明，
 * 状态/操作列普遍漏声明致 antd 默认左对齐——未显式给 align 时按列语义补默认：
 * 操作列（title「操作」或 dataIndex 'operation'）→ right；状态列（title 以「状态」结尾
 * 或 dataIndex 以 status 结尾，如 status / occupancy_status / qc_status）→ center。
 * 显式声明的 align 永不被覆盖；仅按顶层列判定（分组子列不递归，站内无此形态）。 */
function applyAlignDefaults<T extends object>(
  columns: NonNullable<TableProps<T>['columns']>,
): NonNullable<TableProps<T>['columns']> {
  return columns.map((column) => {
    if (column.align) return column
    const title = typeof column.title === 'string' ? column.title : ''
    const raw = (column as { dataIndex?: unknown }).dataIndex
    const dataKey =
      typeof raw === 'string' ? raw : Array.isArray(raw) ? raw.map(String).join('.') : ''
    if (title === '操作' || dataKey === 'operation') {
      return { ...column, align: 'right' } as typeof column
    }
    if (/状态$/.test(title) || /(?:^|_)status$/.test(dataKey)) {
      return { ...column, align: 'center' } as typeof column
    }
    return column
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
  /** 横向滚动宽度：列多时必填，保证固定列与横向滚动可用 */
  scrollX?: number
  showToolbar?: boolean
  showDensity?: boolean
  showColumnSetting?: boolean
  showFullscreen?: boolean
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

  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === wrapperRef.current)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

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
          <Button type="text" size="small" icon={<ReloadOutlined style={iconStyle} />} onClick={onRefresh} />
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
      style={fullscreen ? { background: token.colorBgLayout, padding: 'var(--sf-space-4)' } : undefined}
    >
      {(showToolbar && variant === 'page') && <SfToolbar extra={viewTools}>{actions}</SfToolbar>}
      {error !== undefined && error !== null ? (
        <SfError error={error} onRetry={onRetry ?? onRefresh} />
      ) : (
        <ConfigProvider theme={tableTheme}>
          <Table<T>
            size={density}
            columns={visibleColumns}
            loading={loading}
            locale={{
              // 任务书 §31：首载（loading 且无数据）以骨架条占位保持表头结构，不闪空态；
              // 刷新（已有数据）走 antd 自带 Spin 覆盖层，行结构保持。
              emptyText: () => (loading ? <SfTableSkeleton /> : <SfEmpty description={emptyText} />),
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
            scroll={scrollX !== undefined ? { x: scrollX } : undefined}
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

/** 首载骨架（任务书 §31）：等宽骨架条模拟行结构，表头由 Table 本体保持 */
function SfTableSkeleton() {
  return (
    <div style={{ padding: 'var(--sf-space-3) 0' }} aria-busy>
      <Skeleton active title={false} paragraph={{ rows: 5, width: '100%' }} />
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
