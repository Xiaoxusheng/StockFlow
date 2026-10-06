import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { resolveErrorMessage } from '@/api/client'
import {
  savedViewsApi,
  type SavedView,
  type SavedViewCreatePayload,
  type SavedViewUpdatePayload,
} from '@/api/savedViews'

/**
 * 保存视图 CRUD（计划 §2.2 B5，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * TanStack Query 读 + 失效；写路径统一经本 hook（创建/重命名/删除/设默认/恢复默认），
 * 视图「应用」不在本 hook——由 SfViewBar 按 mode 经 usePagedList 公开 API 落地
 * （urlSync 页写 URL 展开参数、旧形态页置 params，禁止绕过 hook 直改 URL/state）。
 *
 * 错误处理：写失败经 onError 回调抛给调用方呈可读错误（重名 409 等由后端 message 承载），
 * hook 不吞不弹（SfViewBar 统一 message）。
 */
export function useSavedViews(pageKey: string) {
  const queryClient = useQueryClient()
  const queryKey = ['user', 'views', pageKey]

  const listQuery = useQuery({
    queryKey,
    queryFn: () => savedViewsApi.list(pageKey),
    enabled: pageKey.length > 0,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey })
  }

  const createMutation = useMutation<SavedView, Error, SavedViewCreatePayload>({
    mutationFn: (payload) => savedViewsApi.create(payload),
    onSuccess: invalidate,
  })

  const updateMutation = useMutation<SavedView, Error, { id: string; payload: SavedViewUpdatePayload }>({
    mutationFn: ({ id, payload }) => savedViewsApi.update(id, payload),
    onSuccess: invalidate,
  })

  const removeMutation = useMutation<void, Error, string>({
    mutationFn: (id) => savedViewsApi.remove(id),
    onSuccess: invalidate,
  })

  return {
    views: listQuery.data ?? [],
    isLoading: listQuery.isPending,
    error: listQuery.error,
    refetch: listQuery.refetch,
    create: createMutation.mutate,
    createAsync: createMutation.mutateAsync,
    update: updateMutation.mutate,
    updateAsync: updateMutation.mutateAsync,
    remove: removeMutation.mutate,
    removeAsync: removeMutation.mutateAsync,
    isWriting: createMutation.isPending || updateMutation.isPending || removeMutation.isPending,
    /** 写路径统一错误提取（调用方 message 呈现） */
    resolveWriteError: resolveErrorMessage,
  }
}
