-- 000017 down：还原 000007 冻结的四态 CHECK（PAUSED 态回滚前业务侧须保证无 PAUSED 行，
-- 否则约束重建将失败——回滚属于人工运维动作，先清态再降版）。

ALTER TABLE putaway_tasks DROP CONSTRAINT chk_putaway_tasks_status;

ALTER TABLE putaway_tasks ADD CONSTRAINT chk_putaway_tasks_status
    CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED'));
