-- 000023 down：逆序回滚任务优先级列（先索引、后约束、最后列）。

DROP INDEX idx_check_tasks_next;
DROP INDEX idx_pick_tasks_next;
DROP INDEX idx_putaway_tasks_next;
ALTER TABLE check_tasks    DROP CONSTRAINT chk_check_tasks_priority;
ALTER TABLE pick_tasks     DROP CONSTRAINT chk_pick_tasks_priority;
ALTER TABLE putaway_tasks DROP CONSTRAINT chk_putaway_tasks_priority;
ALTER TABLE check_tasks    DROP COLUMN priority;
ALTER TABLE pick_tasks     DROP COLUMN priority;
ALTER TABLE putaway_tasks DROP COLUMN priority;
