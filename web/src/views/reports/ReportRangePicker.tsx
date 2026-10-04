import { DatePicker } from 'antd'
import type { Dayjs } from 'dayjs'
import type { ReportRangeQuery } from '@/api/reports'

/** 报表时间范围选择（time_from/time_to，YYYY-MM-DD；清空 = 走后端缺省近 30 天，
 * 范围上限 366 天由后端校验——internal/reports/handler.go:26-53） */
export function ReportRangePicker({
  value,
  onChange,
}: {
  value: [Dayjs, Dayjs] | null
  onChange: (value: [Dayjs, Dayjs] | null) => void
}) {
  return (
    <DatePicker.RangePicker
      size="small"
      value={value}
      onChange={(values) =>
        onChange(values && values[0] && values[1] ? [values[0], values[1]] : null)
      }
      placeholder={['开始日期', '结束日期']}
    />
  )
}

/** [Dayjs, Dayjs] → 报表时间参数；未选择返回空对象（后端缺省近 30 天） */
export function rangeToParams(
  range: [Dayjs, Dayjs] | null,
): Pick<ReportRangeQuery, 'time_from' | 'time_to'> {
  if (!range) return {}
  return {
    time_from: range[0].format('YYYY-MM-DD'),
    time_to: range[1].format('YYYY-MM-DD'),
  }
}
