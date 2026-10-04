-- 000012 打印中心表回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS uk_print_task_rows_task_seq;
DROP TABLE IF EXISTS print_task_rows;

DROP INDEX IF EXISTS idx_print_tasks_creator_created;
DROP INDEX IF EXISTS idx_print_tasks_status;
DROP INDEX IF EXISTS idx_print_tasks_type_created;
DROP INDEX IF EXISTS uk_print_tasks_no;
DROP TABLE IF EXISTS print_tasks;

DROP INDEX IF EXISTS idx_print_templates_creator;
DROP INDEX IF EXISTS idx_print_templates_type_status;
DROP TABLE IF EXISTS print_templates;
