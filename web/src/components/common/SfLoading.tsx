import { Skeleton } from 'antd'

export interface SfLoadingProps {
  rows?: number
}

/** 统一加载态：非表格区域的轻量骨架（frontend.md §9.5：不做全屏大 Loading） */
export function SfLoading({ rows = 4 }: SfLoadingProps) {
  return (
    <div style={{ padding: '8px 0' }}>
      <Skeleton active paragraph={{ rows }} />
    </div>
  )
}
