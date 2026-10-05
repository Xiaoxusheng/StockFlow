-- 000019: print_task_rows 增行业务身份快照列 data_id
-- 依据：docs/qr-code.md §7.4 重打（fail-closed）——重打以既有任务行为取数依据创建
--       新 PrintTask，取数依据 = 任务行快照 data_id（行业务身份，创建任务时由
--       装配结果 ContentRow.ID 持久化）；修复 print_task_rows 无业务 ID 列
--       （000012 冻结 DDL）导致行身份在落库时丢失的建模缺口。
-- 约定：
--   * data_id = 行对象（SKU）数字 ID 的十进制文本（data_ids 通道纪律恒为数字 ID，
--     qr-code.md §8——SKU 编码/SFQR 协议载荷绝不入该通道）；
--   * 可空兼容存量行：旧行 NULL → 前端读作空串，凡无 data_id 的任务行一律不可
--     自动重打（fail-closed，不做回填造假——旧行身份不可恢复，设计如此）；
--   * 纯加列，不动任何数据。

ALTER TABLE print_task_rows
    ADD COLUMN IF NOT EXISTS data_id varchar(64);

COMMENT ON COLUMN print_task_rows.data_id IS '行业务身份快照（SKU 数字 ID 十进制文本；SFQR 闭环重打依据，qr-code.md §7.4；可空兼容存量行，NULL=不可自动重打）';
