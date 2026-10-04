-- 000011 datax 平台支撑表回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS idx_files_uploader_created;
DROP INDEX IF EXISTS idx_files_expires_at;
DROP INDEX IF EXISTS idx_files_module_business;
DROP TABLE IF EXISTS files;

DROP INDEX IF EXISTS idx_export_tasks_status;
DROP INDEX IF EXISTS idx_export_tasks_creator_created;
DROP INDEX IF EXISTS idx_export_tasks_module_created;
DROP INDEX IF EXISTS uk_export_tasks_no;
DROP TABLE IF EXISTS export_tasks;

DROP INDEX IF EXISTS idx_import_task_rows_task_batch;
DROP INDEX IF EXISTS idx_import_task_rows_task_status;
DROP INDEX IF EXISTS uk_import_task_rows_task_row;
DROP TABLE IF EXISTS import_task_rows;

DROP INDEX IF EXISTS idx_import_tasks_status;
DROP INDEX IF EXISTS idx_import_tasks_creator_created;
DROP INDEX IF EXISTS idx_import_tasks_type_created;
DROP INDEX IF EXISTS uk_import_tasks_no;
DROP TABLE IF EXISTS import_tasks;
