import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Button, Empty, Input, Modal, Spin, App } from 'antd'
import type { InputRef } from 'antd'
import dayjs from 'dayjs'
import {
  AppstoreOutlined,
  BarcodeOutlined,
  BlockOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  HomeOutlined,
  ProfileOutlined,
  QrcodeOutlined,
  TagOutlined,
  TeamOutlined,
} from '@ant-design/icons'
import { searchApi } from '@/api/search'
import { MENU_TREE, type MenuItem } from '@/config/menu'
import { resolveSearchTarget } from '@/config/searchTargets'
import { canAccess, type UserInfo } from '@/types/permission'
import { useAuthStore } from '@/stores/auth'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { toStatusKey } from '@/api/warehouse'
import { resolveErrorMessage } from '@/api/client'
import './global-search.css'

// ---------- 全局业务搜索命令面板（计划 §2.1 / frontend.md §15.2） ----------
// Ctrl/Cmd+K 由 ShortcutProvider 打开（全站唯一 keydown 监听点，本组件不自监听全局键盘）。
// 防抖 300ms；业务搜索 q≥2 字符才发请求（后端 q<2 返回空 groups 的契约前端同样遵守）；
// 『页面』分组=菜单名搜索（MENU_TREE+canAccess 过滤，候选降级保留——后端不可达时仍可搜页面）。
// 键盘：↑↓ 行间移动、Tab/Shift+Tab 分组切换（组间跳转）、Enter 直达；Esc 归 antd Modal；
// 鼠标点击直达。无权限项置灰。跳转经 items.navigation（kind+id）权威映射，未知 kind
// 回退单号前缀/type 映射（searchTargets.ts）。

/** 防抖窗口（计划 §2.1 冻结 300ms） */
const SEARCH_DEBOUNCE_MS = 300

/** 业务搜索最小关键词长度（后端契约：q<2 返回空 groups） */
const MIN_QUERY_LENGTH = 2

/** 每组条数（后端缺省 5 上限 20；面板取 8 提升候选密度） */
const SEARCH_LIMIT = 8

/** type → 图标（分组行渲染；分组标题用后端中文文案，图标本地映射） */
const TYPE_ICONS: Record<string, React.ReactNode> = {
  sku: <BarcodeOutlined />,
  product: <TagOutlined />,
  barcode: <QrcodeOutlined />,
  batch: <DatabaseOutlined />,
  serial: <ProfileOutlined />,
  bin: <HomeOutlined />,
  warehouse: <HomeOutlined />,
  customer: <TeamOutlined />,
  supplier: <TeamOutlined />,
  doc: <FileTextOutlined />,
  logistics: <BlockOutlined />,
  page: <AppstoreOutlined />,
}

/** 最近更新时间压缩展示（高密度 WMS 风格：MM-DD HH:mm；异常串原样回显不抛错） */
function formatCompactTime(value: string): string {
  const d = dayjs(value)
  return d.isValid() ? d.format('MM-DD HH:mm') : value
}

/** 页面分组候选（MENU_TREE 叶子 + canAccess 过滤：与侧边栏可见性同口径） */
interface PageHit {
  path: string
  label: string
  groupLabel?: string
}

function flattenMenuLeaves(items: readonly MenuItem[], user: UserInfo | null): PageHit[] {
  const out: PageHit[] = []
  for (const group of items) {
    if (!canAccess(user, group.permission)) continue
    for (const child of group.children ?? []) {
      if (canAccess(user, child.permission)) {
        out.push({ path: child.path, label: child.label, groupLabel: group.label })
      }
    }
    if (!group.children?.length) {
      // 顶层直达页（Dashboard/工作台/盘点中心/报表中心）：无 children 的分组自身即叶子
      out.push({ path: group.path, label: group.label })
    }
  }
  return out
}

/** 统一渲染行：业务命中与页面命中拍平成同一键盘导航序列 */
interface FlatRow {
  key: string
  type: string
  groupTitle: string
  title: string
  subtitle?: string
  status?: string
  summary?: string
  /** YYYY-MM-DD HH:mm:ss（后端原串，展示压缩为 MM-DD HH:mm，title 保留全量） */
  updatedAt?: string
  /** 置灰原因（无权限）；有值则不可点 */
  disabledReason?: string
  navigateTo: string | null
}

export function GlobalSearchModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate()
  const { message: messageApi } = App.useApp()
  const user = useAuthStore((s) => s.user)
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<InputRef>(null)
  const listRef = useRef<HTMLDivElement>(null)

  // 关闭即清态：下次打开不残留上次关键词与高亮；打开时显式聚焦输入框
  useEffect(() => {
    if (!open) {
      setKeyword('')
      setDebounced('')
      setActiveIndex(0)
    } else {
      const timer = setTimeout(() => inputRef.current?.focus(), 50)
      return () => clearTimeout(timer)
    }
  }, [open])

  // 300ms 防抖
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [keyword])

  // 业务搜索：q≥2 才发（后端 q<2 返回空 groups 的契约，前端不发请求省一跳）
  const searchEnabled = open && debounced.length >= MIN_QUERY_LENGTH
  const { data, isFetching, isError, error, refetch } = useQuery({
    queryKey: ['global-search', debounced],
    queryFn: () => searchApi.search({ q: debounced, limit: SEARCH_LIMIT }),
    enabled: searchEnabled,
    staleTime: 30_000,
    placeholderData: (prev) => prev,
  })

  /** 页面分组候选（菜单名搜索，本地过滤；无需 q≥2 下限） */
  const pageHits = useMemo<PageHit[]>(() => {
    const kw = debounced.toLowerCase()
    if (!kw) return []
    return flattenMenuLeaves(MENU_TREE, user).filter(
      (p) => p.label.toLowerCase().includes(kw) || p.path.toLowerCase().includes(kw),
    )
  }, [debounced, user])

  /** 拍平渲染序列（↑↓ 循环导航的索引基准） */
  const rows = useMemo<FlatRow[]>(() => {
    const out: FlatRow[] = []
    for (const group of data?.groups ?? []) {
      for (const item of group.items) {
        const target = resolveSearchTarget(item)
        const allowed = !target?.permission || canAccess(user, target.permission)
        out.push({
          key: `${group.type}-${item.id}`,
          type: group.type,
          groupTitle: group.title,
          disabledReason: allowed ? undefined : '没有访问权限',
          title: item.title,
          subtitle: item.code,
          status: item.status,
          summary: item.summary,
          updatedAt: item.updated_at,
          navigateTo: target && allowed ? target.path : null,
        })
      }
    }
    for (const page of pageHits) {
      out.push({
        key: `page-${page.path}`,
        type: 'page',
        groupTitle: '页面',
        title: page.label,
        summary: page.groupLabel,
        navigateTo: page.path,
      })
    }
    return out
  }, [data, pageHits, user])

  // 结果变化后重置高亮并回滚滚动
  useEffect(() => {
    setActiveIndex(0)
    listRef.current?.scrollTo({ top: 0 })
  }, [rows])

  /** 各分组首条索引（Tab 分组切换的跳转锚点；groupTitle 变化即组界） */
  const groupStarts = useMemo<number[]>(() => {
    const starts: number[] = []
    let prev: string | null = null
    rows.forEach((row, index) => {
      if (row.groupTitle !== prev) {
        starts.push(index)
        prev = row.groupTitle
      }
    })
    return starts
  }, [rows])

  // 高亮行跟随滚动（键盘导航可见性）
  useEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-row-index="${activeIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex])

  const jumpOrWarn = (row: FlatRow) => {
    if (row.disabledReason) {
      // 无权限置灰项：真实反馈不跳转（frontend.md §15.2 / 计划 §2.9 G 系同口径）
      void messageApi.warning(row.disabledReason)
      return
    }
    if (!row.navigateTo) return
    onClose()
    navigate(row.navigateTo)
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((i) => (rows.length === 0 ? 0 : (i + 1) % rows.length))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((i) => (rows.length === 0 ? 0 : (i - 1 + rows.length) % rows.length))
    } else if (e.key === 'Tab') {
      // Tab/Shift+Tab=分组切换：跳到下/上一组首条（循环）；↑↓ 才是逐行移动
      e.preventDefault()
      if (groupStarts.length === 0) return
      let groupIdx = 0
      groupStarts.forEach((start, i) => {
        if (start <= activeIndex) groupIdx = i
      })
      const next = e.shiftKey
        ? (groupIdx - 1 + groupStarts.length) % groupStarts.length
        : (groupIdx + 1) % groupStarts.length
      setActiveIndex(groupStarts[next])
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const row = rows[activeIndex]
      if (row) jumpOrWarn(row)
    }
    // Esc 归 antd Modal 自身（计划 §2.9：不双抢）
  }

  const showEmpty = searchEnabled && rows.length === 0 && !isFetching && !isError
  const hintTooShort = debounced.length > 0 && debounced.length < MIN_QUERY_LENGTH

  return (
    <Modal
      title={null}
      open={open}
      onCancel={onClose}
      footer={null}
      width={640}
      closable={false}
      styles={{ body: { padding: 0 } }}
    >
      <div className="sf-global-search">
        <Input
          ref={inputRef}
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          onKeyDown={handleKeyDown}
          variant="borderless"
          allowClear
          size="large"
          placeholder="搜索 SKU / 单据 / 库位 / SN / 页面"
          aria-label="全局搜索"
          suffix={isFetching ? <Spin size="small" /> : null}
        />
        <div className="sf-global-search__list" ref={listRef}>
          {isError && searchEnabled && (
            <div className="sf-global-search__error" role="alert">
              <span className="sf-global-search__error-text">
                搜索失败：{resolveErrorMessage(error)}
              </span>
              <Button size="small" onClick={() => void refetch()}>
                重试
              </Button>
            </div>
          )}
          {rows.map((row, index) => (
            <button
              type="button"
              key={row.key}
              data-row-index={index}
              className={`sf-global-search__item${index === activeIndex ? ' sf-global-search__item--active' : ''}${row.disabledReason ? ' sf-global-search__item--disabled' : ''}`}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => jumpOrWarn(row)}
              aria-disabled={!!row.disabledReason}
            >
              <span className="sf-global-search__item-main">
                <span className="sf-global-search__item-title">{row.title}</span>
                {row.subtitle && <span className="sf-global-search__item-code">{row.subtitle}</span>}
                {row.summary && (
                  <span className="sf-global-search__item-summary">{row.summary}</span>
                )}
              </span>
              <span className="sf-global-search__item-side">
                {row.status && <SfStatusTag status={toStatusKey(row.status)} />}
                {row.updatedAt && (
                  <span
                    className="sf-global-search__item-time"
                    title={`最近更新：${row.updatedAt}`}
                  >
                    {formatCompactTime(row.updatedAt)}
                  </span>
                )}
                <span className="sf-global-search__item-group">
                  {TYPE_ICONS[row.type]}
                  {row.groupTitle}
                </span>
              </span>
            </button>
          ))}
          {showEmpty && (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={`未找到与「${debounced}」匹配的结果`}
              className="sf-global-search__empty"
            />
          )}
          {hintTooShort && (
            <div className="sf-global-search__empty">
              输入至少 {MIN_QUERY_LENGTH} 个字符搜索业务数据；页面名称仍可直接匹配
            </div>
          )}
          {!showEmpty && !hintTooShort && !isError && rows.length === 0 && (
            <div className="sf-global-search__empty">
              输入关键词搜索业务数据，或输入页面名称直达菜单页
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}
