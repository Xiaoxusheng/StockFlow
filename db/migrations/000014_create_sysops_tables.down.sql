-- 000014 平台运维表回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS uk_backup_records_inflight;
DROP INDEX IF EXISTS idx_backup_records_status_created;
DROP TABLE IF EXISTS backup_records;

DROP INDEX IF EXISTS idx_notifications_user_read_created;
DROP INDEX IF EXISTS uk_notifications_dedup;
DROP TABLE IF EXISTS notifications;

DROP TABLE IF EXISTS system_configs;

DROP INDEX IF EXISTS idx_scheduled_job_runs_start;
DROP INDEX IF EXISTS idx_scheduled_job_runs_job_start;
DROP TABLE IF EXISTS scheduled_job_runs;

DROP INDEX IF EXISTS uk_scheduled_jobs_code;
DROP TABLE IF EXISTS scheduled_jobs;
