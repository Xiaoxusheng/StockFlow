/**
 * 动效时长 TS 镜像（frontend.md §31 表格动效体系）。
 * 仅 JS 定时器消费（刷新图标最短保持 / 行反馈清类 / 删除行收尾等）；CSS 一律 var(--sf-motion-*)。
 * 与 tokens.css / App.tsx「同 commit 双源同步」纪律一致（frontend.md §2.1），改时长两处同改。
 */
export const SF_MOTION_MS = {
  /** --sf-motion-fast */
  fast: 120,
  /** --sf-motion-normal */
  normal: 180,
  /** --sf-motion-slow */
  slow: 220,
  /** --sf-motion-spin：刷新图标 360° 自旋周期（任务书规格 500~700ms） */
  spin: 560,
  /** --sf-motion-feedback：行反馈淡色底动画时长（任务书规格 400~700ms） */
  feedback: 480,
  /** 行反馈类清除定时器：≥ 动画时长，防动画截断 */
  feedbackCleanup: 540,
  /** 刷新图标最短自旋保持（任务书规格 ≈450ms；绑定 loading 起停，不得无限转） */
  refreshMinHold: 450,
  /** 删除行两段动画（fade 120ms + shrink 120ms）走完 + 缓冲后，SfTable 才过滤该行 DOM */
  rowRemoveCollapse: 260,
  /** 删除行 hook 兜底自清（覆盖 refetch 失败场景：行恢复显示 = 删除未生效，如实回显） */
  rowRemoveFallback: 2500,
} as const
