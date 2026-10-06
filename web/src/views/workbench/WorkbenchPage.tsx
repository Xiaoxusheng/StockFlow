import { Col, Row, Skeleton, Tooltip, Typography } from 'antd'
import {
  AuditOutlined,
  DatabaseOutlined,
  ImportOutlined,
  RightOutlined,
  SafetyCertificateOutlined,
  ScanOutlined,
  SearchOutlined,
  ShoppingOutlined,
} from '@ant-design/icons'
import type { ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  taskApi,
  type RecentOperationItem,
  type WorkbenchPriorities,
  type WorkbenchPriorityItem,
} from '@/api/task'
import { EXCEPTION_STATUS_TAG, type ExceptionStatus } from '@/api/exception'
import { SfDetailSection } from '@/components/common/SfDetailSection'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber, formatQty } from '@/utils/format'
import './workbench.css'

const { Text } = Typography

/**
 * 我的工作台（/workbench，frontend.md §15.1 PC『我现在该做什么』）：
 * 进页面即回答"该干什么"，四块紧凑高密度（2026-10-06 工作台优化改版）——
 * ① 我的工作区：待我处理 / 超时 / 异常 / 今日已完成四计数
 *    （GET /api/workbench/summary 增量字段，真数据；可直达处带真实筛选跳转）；
 * ② 优先处理：超时收货 / 库位异常 / 临期库存 / 待复核订单四组 TopN
 *    （GET /api/workbench/priorities，count 全量 + 明细尾 N 条，排序由后端 SQL 承担，
 *    组头直达对应列表页真实筛选——/inbound?status=RECEIVING、/exceptions、
 *    /inventory/alerts?level=near_expiry、/checking?status=PENDING，均为 urlSync 页可还原）；
 * ③ 最近操作：本人 operation_logs 尾 10 条（GET /api/workbench/recent-operations）；
 * ④ 快捷入口：MENU_TREE 权限码同源 + canAccess 过滤（§15.1），扫码作业为 Pad 作业端
 *    入口（/pad/home，个人入口无权限码——与 Dashboard 菜单同性质）。
 * 数据全部真实（禁止写死）；Loading/Empty/Error 齐备；仅 PC，Pad 首页不在改版范围。
 */

/** 工作区四计数口径 tooltip（口径直引 internal/reports/workbench_summary.go 聚合注释） */
const WORKSPACE_STATS: Array<{
  key: 'mine_count' | 'timeout_count' | 'exception_count' | 'today_completed_count'
  label: string
  tooltip: string
  path?: string
  /** path 对应列表页所需权限码（与 MENU_TREE 同源；缺省=个人入口放行） */
  permission?: string
  danger?: boolean
}> = [
  {
    key: 'mine_count',
    label: '待我处理',
    path: '/tasks?status=in_progress',
    permission: 'inventory:stock:view',
    tooltip: '指派给我且进行中的任务数（putaway IN_PROGRESS/PAUSED + pick CLAIMED/PICKING），点击查看任务清单',
  },
  {
    key: 'timeout_count',
    label: '超时',
    tooltip: '本人进行中且超过超时阈值的任务数（上架/拣货，阈值 task.timeout.* 可配，缺省 4 小时；checking 无超时层）',
  },
  {
    key: 'exception_count',
    label: '异常',
    path: '/exceptions',
    permission: 'exception:view',
    danger: true,
    tooltip: '未闭环异常数（RESOLVED/CLOSED 之外，全量口径——异常单无仓库列），点击进入异常中心',
  },
  {
    key: 'today_completed_count',
    label: '今日已完成',
    tooltip: '本人今日完成的任务数（putaway COMPLETED / pick PICKED / check DONE，按完成时间落在当日）',
  },
]

/** 优先处理四组（key=WorkbenchPriorities 组键；path=组头直达链接；hint=口径披露） */
const PRIORITY_GROUPS: Array<{
  key: keyof WorkbenchPriorities
  label: string
  path: string
  permission?: string
  hint: string
}> = [
  {
    key: 'overdue_receipts',
    label: '超时收货',
    path: '/inbound?status=RECEIVING',
    permission: 'inbound:view',
    hint: '收货中（RECEIVING）且创建超过收货超时阈值的入库单，按创建先后排序',
  },
  {
    key: 'bin_exceptions',
    label: '库位异常',
    path: '/exceptions',
    permission: 'exception:view',
    hint: '已定位到库位且未闭环的异常单，按创建先后排序（异常单无仓库列，全量口径）',
  },
  {
    key: 'near_expiry_stock',
    label: '临期库存',
    path: '/inventory/alerts?level=near_expiry',
    permission: 'inventory:stock:view',
    hint: '效期在临期窗口内（inventory.alert.expiry_days 最大档，缺省 30 天）且现存量为正的批次库存，按最先到期排序',
  },
  {
    key: 'pending_checks',
    label: '待复核订单',
    path: '/checking?status=PENDING',
    permission: 'checking:view',
    hint: '存在待复核（PENDING）复核任务的出库单（按单去重），按最早创建排序',
  },
]

/** 快捷入口（MENU_TREE 权限码同源 + canAccess 过滤；扫码作业=Pad 作业端入口） */
const QUICK_ENTRIES: Array<{ label: string; path: string; permission?: string; icon: ReactNode }> = [
  { label: '扫码作业', path: '/pad/home', icon: <ScanOutlined /> },
  { label: '收货', path: '/purchases/receipts', permission: 'purchase:receipt:view', icon: <ImportOutlined /> },
  { label: '上架', path: '/tasks?task_type=putaway', permission: 'inventory:stock:view', icon: <DatabaseOutlined /> },
  { label: '拣货', path: '/picking', permission: 'picking:view', icon: <ShoppingOutlined /> },
  { label: '复核', path: '/checking', permission: 'checking:view', icon: <SafetyCertificateOutlined /> },
  { label: '盘点', path: '/counts', permission: 'count:view', icon: <AuditOutlined /> },
  { label: '库存查询', path: '/inventory/stock', permission: 'inventory:stock:view', icon: <SearchOutlined /> },
]

/** 最近操作列（GET /api/workbench/recent-operations：本人 operation_logs 尾 N 条） */
const OP_COLUMNS: ColumnsType<RecentOperationItem> = [
  { title: '时间', dataIndex: 'time', width: 150, render: (v: string) => formatDateTime(v) },
  { title: '动作', dataIndex: 'action', width: 120, ellipsis: true },
  { title: '模块', dataIndex: 'module', width: 110, ellipsis: true },
  {
    title: '对象',
    key: 'object',
    width: 150,
    ellipsis: true,
    render: (_: unknown, r: RecentOperationItem) =>
      r.object_type ? `${r.object_type}${r.object_id ? ` #${r.object_id}` : ''}` : '-',
  },
  {
    title: '结果',
    dataIndex: 'success',
    width: 100,
    render: (ok: boolean, r: RecentOperationItem) =>
      ok ? (
        <SfStatusTag label="成功" semantic="success" />
      ) : (
        <SfStatusTag label={r.error_code ?? '失败'} semantic="danger" />
      ),
  },
]

/** 异常组原态 → 标签（EXCEPTION_STATUS_TAG 唯一映射；未登记原态兜底中性标签） */
function renderExceptionStatus(status: string) {
  const meta = EXCEPTION_STATUS_TAG[status as ExceptionStatus]
  return (
    <SfStatusTag
      label={meta?.label ?? status}
      semantic={(meta?.semantic ?? 'neutral') as StatusSemantic}
    />
  )
}

/** 我的工作台（GET /api/workbench/summary + /priorities + /recent-operations 真实数据） */
export default function WorkbenchPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)

  const summary = useQuery({ queryKey: ['workbench', 'summary'], queryFn: taskApi.summary })
  /** 优先处理四组（count 全量 + 明细尾 5 条；后端路由未接线时呈现统一错误态——预期行为） */
  const priorities = useQuery({
    queryKey: ['workbench', 'priorities'],
    queryFn: () => taskApi.priorities(5),
  })
  /** 最近操作（只读端点；失败时呈现统一错误态，不造假记录） */
  const recentOps = useQuery({
    queryKey: ['workbench', 'recent-operations'],
    queryFn: () => taskApi.recentOperations(10),
  })

  return (
    <div className="sf-page">
      <SfPageHeader title="我的工作台" subtitle="我现在该做什么" />

      {/* ① 我的工作区：四计数（真实统计；可直达处跳对应列表真实筛选，否则仅口径披露） */}
      <SfDetailSection
        title="我的工作区"
        extra={<Text type="secondary">待我处理 / 超时 / 异常 / 今日已完成</Text>}
      >
        {summary.isPending ? (
          <div className="sf-wb-stats">
            {WORKSPACE_STATS.map((s) => (
              <Skeleton.Node key={s.key} active style={{ width: '100%', height: 48 }} />
            ))}
          </div>
        ) : summary.error ? (
          <SfError
            error={summary.error}
            onRetry={summary.refetch}
            description="工作台汇总接口（/api/workbench/summary）暂不可用"
          />
        ) : (
          <div className="sf-wb-stats">
            {WORKSPACE_STATS.map((s) => {
              const clickable = !!s.path && canAccess(user, s.permission)
              return (
                <div
                  key={s.key}
                  className={clickable ? 'sf-wb-stat sf-wb-stat--link' : 'sf-wb-stat'}
                  onClick={clickable ? () => navigate(s.path!) : undefined}
                  role={clickable ? 'button' : undefined}
                  tabIndex={clickable ? 0 : undefined}
                  onKeyDown={
                    clickable
                      ? (e) => {
                          if (e.key === 'Enter') navigate(s.path!)
                        }
                      : undefined
                  }
                >
                  <Tooltip title={s.tooltip}>
                    <span className="sf-wb-stat__label">
                      {s.label}
                      <RightOutlined aria-hidden className="sf-wb-stat__arrow" />
                    </span>
                  </Tooltip>
                  <span
                    className={s.danger ? 'sf-wb-stat__value sf-wb-stat__value--danger' : 'sf-wb-stat__value'}
                  >
                    {formatNumber(summary.data?.[s.key])}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </SfDetailSection>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={16}>
          {/* ② 优先处理：四组 TopN（count 全量 + 明细尾 5 条；组头直达对应列表页） */}
          <SfDetailSection
            title="优先处理"
            extra={<Text type="secondary">超时收货 / 库位异常 / 临期库存 / 待复核订单</Text>}
          >
            {priorities.isPending ? (
              <Row gutter={[12, 12]}>
                {PRIORITY_GROUPS.map((g) => (
                  <Col key={g.key} xs={24} md={12} xxl={6}>
                    <Skeleton.Node active style={{ width: '100%', height: 120 }} />
                  </Col>
                ))}
              </Row>
            ) : priorities.error ? (
              <SfError
                error={priorities.error}
                onRetry={priorities.refetch}
                description="优先处理接口（/api/workbench/priorities）暂不可用"
              />
            ) : (
              <Row gutter={[12, 12]}>
                {PRIORITY_GROUPS.map((g) => {
                  const group = priorities.data?.[g.key]
                  const items = group?.items ?? []
                  const clickable = canAccess(user, g.permission)
                  return (
                    <Col key={g.key} xs={24} md={12} xxl={6}>
                      <div className="sf-wb-group">
                        <div className="sf-wb-group__head">
                          <Tooltip title={g.hint}>
                            <span className="sf-wb-group__label">{g.label}</span>
                          </Tooltip>
                          {clickable ? (
                            <button
                              type="button"
                              className="sf-wb-group__link"
                              onClick={() => navigate(g.path)}
                            >
                              {formatNumber(group?.count)} 条
                              <RightOutlined aria-hidden className="sf-wb-group__arrow" />
                            </button>
                          ) : (
                            <span className="sf-wb-group__count">{formatNumber(group?.count)} 条</span>
                          )}
                        </div>
                        {items.length === 0 ? (
                          <div className="sf-wb-group__empty">暂无待处理</div>
                        ) : (
                          <ul className="sf-wb-items">
                            {items.map((it) => (
                              <PriorityItemRow key={`${it.id}-${it.title}`} item={it} />
                            ))}
                          </ul>
                        )}
                      </div>
                    </Col>
                  )
                })}
              </Row>
            )}
          </SfDetailSection>
        </Col>
        <Col xs={24} xl={8}>
          {/* ④ 快捷入口：MENU_TREE 权限码同源 + canAccess 过滤 */}
          <SfDetailSection title="快捷入口" extra={<Text type="secondary">按权限过滤</Text>}>
            <div className="sf-wb-quick">
              {QUICK_ENTRIES.filter((e) => canAccess(user, e.permission)).map((e) => (
                <button
                  key={e.path}
                  type="button"
                  className="sf-wb-quick__item"
                  onClick={() => navigate(e.path)}
                >
                  <span className="sf-wb-quick__icon" aria-hidden>
                    {e.icon}
                  </span>
                  <span>{e.label}</span>
                </button>
              ))}
            </div>
          </SfDetailSection>
        </Col>
      </Row>

      {/* ③ 最近操作：本人 operation_logs 尾 10 条（时间/动作/对象/结果） */}
      <div style={{ marginTop: 16 }}>
        <SfDetailSection title="最近操作" extra={<Text type="secondary">本人最近 10 条</Text>}>
          <SfTable<RecentOperationItem>
            variant="nested"
            rowKey={(r) => `${r.time}-${r.action}-${r.object_id ?? ''}-${r.request_id ?? ''}`}
            columns={OP_COLUMNS}
            dataSource={recentOps.data?.items ?? []}
            loading={recentOps.isFetching}
            error={recentOps.error}
            onRetry={recentOps.refetch}
            emptyText="暂无操作记录（业务动作经审计中间件写入 operation_logs）"
            scrollX={630}
          />
        </SfDetailSection>
      </div>
    </div>
  )
}

/** 优先处理明细行（统一行形状：标题+状态 / 副题 / 明细+时间；纯展示不伪装行级详情） */
function PriorityItemRow({ item }: { item: WorkbenchPriorityItem }) {
  return (
    <li className="sf-wb-item">
      <div className="sf-wb-item__main">
        <span className="sf-wb-item__title" title={item.title}>
          {item.title}
        </span>
        {item.status ? renderExceptionStatus(item.status) : null}
      </div>
      {item.subtitle ? (
        <div className="sf-wb-item__meta" title={item.subtitle}>
          {item.subtitle}
        </div>
      ) : null}
      <div className="sf-wb-item__foot">
        <span className="sf-wb-item__detail" title={item.detail}>
          {item.detail}
          {item.qty !== undefined ? ` · ${formatQty(item.qty)}` : ''}
        </span>
        {item.time ? <span className="sf-wb-item__time">{formatDateTime(item.time)}</span> : null}
      </div>
    </li>
  )
}
