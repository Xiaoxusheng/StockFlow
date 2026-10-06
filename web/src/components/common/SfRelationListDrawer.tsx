import { useCallback, useEffect, useState } from 'react'
import { Drawer } from 'antd'
import type { PageQuery, PageResult } from '@/types/api'
import type { RelationListSpec } from '@/config/relations'
import { SfTable } from '@/components/table/SfTable'

export interface SfRelationListDrawerProps {
  open: boolean
  /** 内嵌列表配置（null=不渲染内容）；配置变化由父级经 key 重挂载（分页状态随之重置） */
  spec: RelationListSpec | null
  onClose: () => void
}

const PAGE_SIZE = 10

/* any 为受控逃逸口（RelationListSpec 契约边界，见 relations.tsx 注释）：多形态列表
 * 统一以 any 承接，列定义在配置侧保留具体 Item 类型 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const NestedTable = SfTable<any>

/**
 * 关联业务 Drawer 内嵌列表（计划 §2.6 / ask「优先 Drawer 内嵌，不强制离开当前页」）：
 * 复用 SfTable variant=nested（统一密度/空态/错误态/首载骨架——禁止第二套表格），
 * 受控分页直查目标列表端点（过滤参数由 relations.tsx 工厂闭包按上下文预填，
 * 全部取后端真实支持的白名单键）。错误态经 SfTable 内建 error/onRetry 呈现。
 */
export function SfRelationListDrawer({ open, spec, onClose }: SfRelationListDrawerProps) {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<PageResult<unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const fetchPage = useCallback(
    async (targetPage: number) => {
      if (!spec) return
      setLoading(true)
      setError(null)
      const query: PageQuery = { page: targetPage, pageSize: PAGE_SIZE }
      try {
        const res = await spec.fetch(query)
        setData(res)
        setError(null)
      } catch (e) {
        setError(e)
      } finally {
        setLoading(false)
      }
    },
    [spec],
  )

  useEffect(() => {
    if (open && spec) {
      void fetchPage(page)
    }
  }, [open, spec, page, fetchPage])

  const handlePageChange = (next: number) => {
    setPage(next)
  }

  return (
    <Drawer
      title={spec?.title ?? '关联业务'}
      open={open}
      onClose={onClose}
      width={760}
      destroyOnHidden
    >
      {spec && (
        <NestedTable
          variant="nested"
          rowKey="id"
          columns={spec.columns}
          dataSource={data?.items ?? []}
          loading={loading}
          error={error}
          onRetry={() => {
            void fetchPage(page)
          }}
          pagination={{ current: page, pageSize: PAGE_SIZE }}
          total={data?.total ?? 0}
          onPageChange={handlePageChange}
          emptyText="暂无关联数据"
        />
      )}
    </Drawer>
  )
}
