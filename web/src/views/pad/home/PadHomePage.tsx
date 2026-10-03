import { useNavigate } from 'react-router'
import { Button } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { inventoryApi } from '@/api/inventory'
import { taskApi } from '@/api/task'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PAD_NAV_ITEMS } from '@/layouts/pad'
import { formatNumber } from '@/utils/format'

function MetricCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="sf-pad-metric-cell">
      <span className="sf-pad-metric">{value}</span>
      <span className="sf-pad-info-label">{label}</span>
    </div>
  )
}

/**
 * Pad 首页（frontend.md §20.5：任务 / 快速作业 / 预警 / 最近操作 四块，不做 PC Dashboard 缩放）。
 * 横屏 2×2 区块（.sf-pad-home 网格），竖屏单列堆叠（pad.css @media 切换）。
 * 数据全部走真实 API：taskApi.summary / inventoryApi.stockSummary 均为前端先行契约，
 * 失败分别呈 SfError / 「-」占位，不拖垮整页（§9.5）；最近操作无端点，呈上下文占位说明。
 */
export default function PadHomePage() {
  const navigate = useNavigate()

  const workbench = useQuery({
    queryKey: ['pad', 'home', 'workbench-summary'],
    queryFn: taskApi.summary,
  })

  const stockSummary = useQuery({
    queryKey: ['pad', 'home', 'stock-summary'],
    queryFn: inventoryApi.stockSummary,
  })

  const taskCells = workbench.data
    ? [
        { label: '我的待办', value: workbench.data.todoCount },
        { label: '我的审批', value: workbench.data.approvalCount },
        { label: '我的任务', value: workbench.data.taskCount },
        { label: '我的异常', value: workbench.data.exceptionCount },
      ]
    : []

  return (
    <div className="sf-pad-home">
      <section className="sf-pad-card" aria-label="任务">
        <h3 className="sf-pad-card-title">任务</h3>
        {workbench.isPending ? (
          <SfLoading rows={2} />
        ) : workbench.error ? (
          <SfError error={workbench.error} onRetry={() => void workbench.refetch()} />
        ) : (
          <>
            <div className="sf-pad-metric-grid">
              {taskCells.map((cell) => (
                <MetricCell key={cell.label} label={cell.label} value={formatNumber(cell.value)} />
              ))}
            </div>
            <p className="sf-pad-muted-note">
              计数口径为工作台四块（GET /api/workbench/summary）；
              待收货 / 待质检 / 待上架 / 待盘点分项计数待后端任务域交付后补
            </p>
          </>
        )}
      </section>

      <section className="sf-pad-card" aria-label="快速作业">
        <h3 className="sf-pad-card-title">快速作业</h3>
        <div className="sf-pad-quick-grid">
          {PAD_NAV_ITEMS.map((item) => (
            <Button
              key={item.path}
              size="large"
              icon={item.icon}
              className="sf-pad-quick-cell"
              onClick={() => navigate(item.path)}
            >
              {item.label}
            </Button>
          ))}
        </div>
      </section>

      {/* 预警卡：局部汇总条，失败呈「-」占位不拖垮整页（frontend.md §9.5） */}
      <section className="sf-pad-card" aria-label="预警">
        <h3 className="sf-pad-card-title">预警</h3>
        <div className="sf-pad-metric-grid">
          <MetricCell
            label="临期数量"
            value={stockSummary.data ? formatNumber(stockSummary.data.nearExpiryQty) : '-'}
          />
          <MetricCell
            label="异常数量"
            value={stockSummary.data ? formatNumber(stockSummary.data.abnormalQty) : '-'}
          />
        </div>
        {stockSummary.error && (
          <p className="sf-pad-muted-note">库存汇总接口暂不可用，数值以「-」占位，不影响其他区块</p>
        )}
      </section>

      {/* 最近操作：后端未交付（无端点），呈上下文占位说明，禁止写死数据（requirements.md §10） */}
      <section className="sf-pad-card" aria-label="最近操作">
        <h3 className="sf-pad-card-title">最近操作</h3>
        <SfEmpty description="最近操作的后端端点尚未交付，交付后在此展示当前账号的最近作业流水" />
      </section>
    </div>
  )
}
