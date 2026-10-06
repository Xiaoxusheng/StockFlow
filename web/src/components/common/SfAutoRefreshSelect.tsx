import { Select, Tooltip } from 'antd'
import { AUTO_REFRESH_OPTIONS, type AutoRefreshSeconds } from '@/hooks/useAutoRefresh'

export interface SfAutoRefreshSelectProps {
  /** 当前档位（秒；0=关）——useAutoRefresh().seconds */
  value: AutoRefreshSeconds
  /** 档位切换——useAutoRefresh().setSeconds */
  onChange: (seconds: AutoRefreshSeconds) => void
  disabled?: boolean
}

/**
 * 自动刷新档位选择（计划 §2.11：关/10/30/60 秒，默认关）——
 * 任务型列表页工具栏（SfTable actions 区或 SfToolbar extra 区）与 useAutoRefresh 成对使用：
 * 页面把 useAutoRefresh().refetchInterval 直传 useQuery 即完成接线。
 */
export function SfAutoRefreshSelect({ value, onChange, disabled }: SfAutoRefreshSelectProps) {
  return (
    <Tooltip title="自动刷新：页签切走时暂停，连续失败自动停轮">
      <Select<AutoRefreshSeconds>
        disabled={disabled}
        value={value}
        onChange={onChange}
        options={AUTO_REFRESH_OPTIONS}
        style={{ minWidth: 86 }}
        aria-label="自动刷新间隔"
      />
    </Tooltip>
  )
}
