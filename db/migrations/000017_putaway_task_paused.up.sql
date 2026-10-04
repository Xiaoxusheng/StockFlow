-- 000017：上架任务状态机补 PAUSED（暂停/恢复，PadPutawayPage 暂停占位接线前置）。
--
-- 背景：business-flow §5.1 冻结三态（待上架→上架中→已完成），M2 实现追加 CANCELLED
-- 取消态；作业端（Pad）需要"暂停当前任务"能力——暂停语义 = 任务保留领取人归属、
-- 退出执行态可恢复，属作业过程状态而非终态，故以 PAUSED 独立状态承载：
--   IN_PROGRESS → PAUSED（暂停，仅领取人/超管）
--   PAUSED → IN_PROGRESS（恢复，仅领取人/超管）
-- PAUSED 视为活动态：入库单完成推进/差额关闭的"存在进行中任务"守卫同样命中。
--
-- 实现方式：CHECK 约束不可 ALTER，采用"删旧建新"同语句组；down 还原四态约束。
-- 现存数据值域 ⊆ {PENDING, IN_PROGRESS, COMPLETED, CANCELLED}，重放安全（PG 会在
-- 约束已存在时报错，本迁移按 golang-migrate version 幂等，不依赖 IF NOT EXISTS——
-- CHECK 约束无 IF NOT EXISTS 语法，up/down 各自成对执行一次）。

ALTER TABLE putaway_tasks DROP CONSTRAINT chk_putaway_tasks_status;

ALTER TABLE putaway_tasks ADD CONSTRAINT chk_putaway_tasks_status
    CHECK (status IN ('PENDING', 'IN_PROGRESS', 'PAUSED', 'COMPLETED', 'CANCELLED'));

COMMENT ON CONSTRAINT chk_putaway_tasks_status ON putaway_tasks IS
    '上架任务状态机（PENDING 待领取 / IN_PROGRESS 上架中 / PAUSED 已暂停 / COMPLETED 已完成 / CANCELLED 已取消）';
