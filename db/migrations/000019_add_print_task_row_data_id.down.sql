-- 000019 回滚：删除 print_task_rows.data_id 列（与 000015/000016 成对先例同款，
-- 纯加列回滚，无数据变动）

ALTER TABLE print_task_rows
    DROP COLUMN IF EXISTS data_id;
