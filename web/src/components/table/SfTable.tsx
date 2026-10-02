import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Button,
  Checkbox,
  Dropdown,
  Popover,
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
import type { TableProps } from 'antd'
import type { Key, ReactNode } from 'react'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfToolbar } from './SfToolbar'

type Density = 'small' | 'middle' | 'large'

const DENSITY_LABEL: Record<Density, string> = {
  small: '紧凑',
  middle: '标准',
  large: '宽松',
}

function columnKey(column: NonNullable<TableProps<Record<string, unknown>>['columns']>[number]): string | null {
  const key = (column as { key?: Key }).key
  if (typeof key === 'string') return key
  const dataIndex = (column as { dataIndex?: unknown }).dataIndex
  if (typeof dataIndex === 'string') return dataIndex
  if (Array.isArray(dataIndex) && typeof dataIndex[0] === 'string') return dataIndex[0]
  return null
}

export interface SfTableProps<T extends object> extends TableProps<T> {
  columns: TableProps<T>['columns']
  /** 受控分页（配合 usePagedList） */
  pagination: { current: number; pageSize: number }
  total: number
  onPageChange: (page: number, pageSize: number) => void
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
  showToolbar = true,
  showDensity = true,
  showColumnSetting = true,
  showFullscreen = true,
  ...rest
}: SfTableProps<T>) {
  const { token } = theme.useToken()
  const wrapperRef = useRef<HTMLDivElement>(null)
  const [fullscreen, setFullscreen] = useState(false)
  const [density, setDensity] = useState<Density>(
    () => readStored<Density>(storageKey, 'density') ?? 'middle',
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
      (columns ?? []).filter((column) => {
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

  const iconStyle = { fontSize: 15, color: token.colorTextSecondary }

  const viewTools = (
    <Space size={0}>
      {onRefresh && (
        <Tooltip title="刷新">
          <Button type="text" size="small" icon={<ReloadOutlined style={iconStyle} />} onClick={onRefresh} />
        </Tooltip>
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
      {showColumnSetting && identityColumns.length > 0 && (
        <Popover
          trigger="click"
          placement="bottomRight"
          content={
            <div style={{ maxHeight: 320, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 6 }}>
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
          }
        >
          <Button
            type="text"
            size="small"
            icon={<SettingOutlined style={iconStyle} />}
          />
        </Popover>
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
    <div ref={wrapperRef} style={fullscreen ? { background: token.colorBgLayout, padding: 16 } : undefined}>
      {showToolbar && <SfToolbar extra={viewTools}>{actions}</SfToolbar>}
      {error !== undefined && error !== null ? (
        <SfError error={error} onRetry={onRetry ?? onRefresh} />
      ) : (
        <Table<T>
          size={density}
          columns={visibleColumns}
          loading={loading}
          locale={{ emptyText: () => <SfEmpty description={emptyText} /> }}
          pagination={{
            ...pagination,
            total,
            showSizeChanger: true,
            showQuickJumper: true,
            pageSizeOptions: [10, 20, 50, 100],
            showTotal: (t) => `共 ${t} 条`,
            onChange: onPageChange,
          }}
          scroll={scrollX !== undefined ? { x: scrollX } : undefined}
          {...rest}
        />
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
