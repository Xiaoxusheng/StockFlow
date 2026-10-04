-- 000006 单据平台共享表回滚：先审批记录后计数器（与 up 相反序）

DROP INDEX IF EXISTS idx_document_approvals_target;
DROP TABLE IF EXISTS document_approvals;

DROP TABLE IF EXISTS doc_number_counters;
