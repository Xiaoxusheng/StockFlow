-- 000023：三任务表补 priority 优先级列（作业效率提升层一期 B3，docs/plans/2026-10-06-efficiency-layer-phase1.md §3.4 原文）。
--
-- 用途：/api/tasks/next 的 priority DESC 排序层与 PUT /api/{putaway|picks|checks}/:id/priority
-- 写入端点的数据来源（§2.4：priority 写入者，否则高优先级层无数据属假能力）。
-- smallint + CHECK 0-9 兜底值域；服务端同样校验 0-9（双保险）。
-- 部分索引：仅候选池活动态（B7 候选池口径）——putaway 五态含 PAUSED（000017 放宽后值域）、
-- pick 六态、check 三态；终态（COMPLETED/CANCELLED/PICKED/DONE/EXCEPTION 等）不入索引。
-- down：逆序 DROP 三索引 + 三约束 + 三列。

ALTER TABLE putaway_tasks ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE pick_tasks     ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE check_tasks    ADD COLUMN priority smallint NOT NULL DEFAULT 0;
ALTER TABLE putaway_tasks ADD CONSTRAINT chk_putaway_tasks_priority CHECK (priority BETWEEN 0 AND 9);
ALTER TABLE pick_tasks     ADD CONSTRAINT chk_pick_tasks_priority     CHECK (priority BETWEEN 0 AND 9);
ALTER TABLE check_tasks    ADD CONSTRAINT chk_check_tasks_priority    CHECK (priority BETWEEN 0 AND 9);
CREATE INDEX idx_putaway_tasks_next ON putaway_tasks (status, priority DESC, created_at)
  WHERE status IN ('PENDING', 'IN_PROGRESS', 'PAUSED');
CREATE INDEX idx_pick_tasks_next ON pick_tasks (status, priority DESC, created_at)
  WHERE status IN ('PENDING', 'CLAIMED', 'PICKING');
CREATE INDEX idx_check_tasks_next ON check_tasks (status, priority DESC, created_at)
  WHERE status = 'PENDING';
