-- 000006 M2 单据平台共享表：doc_number_counters / document_approvals
-- 依据：backend-m2-plan.md §4.1/§5（000006 冻结 DDL）、business-flow.md §12.2/§13.1、database.md §3/§7。
-- 约定：
--   * doc_number_counters 为单据编号计数表（docnum 引擎落点，plan §4.1 冻结发放机制）：
--     复合主键 prefix+period，无 bigserial 代理键；取号 = 事务内
--     INSERT ... ON CONFLICT DO NOTHING + UPDATE ... RETURNING next_no（行锁原子）。
--     主键为内联 PRIMARY KEY 声明（约束命名规约 chk_/fk_ 不覆盖主键，M1 同款内联写法）。
--   * document_approvals 为审批记录统一落位（business-flow §12.2：审批人/时间/意见/结果
--     不可修改删除）：纯审计 append-only——仅 created_at，无 updated_*/deleted_at；
--     应用层无 UPDATE/DELETE 通路，DB 账号分层见 db/grants/app_grants.sql（本表仅 SELECT+INSERT）。
--   * 本域无任何外键：target/target 单号为跨域逻辑引用，一致性由 Service 层校验（plan §5 通用规则）。

CREATE TABLE doc_number_counters (
    prefix     varchar(16) NOT NULL,
    period     varchar(8)  NOT NULL,
    next_no    bigint      NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint      NOT NULL DEFAULT 0,
    updated_by bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (prefix, period),
    CONSTRAINT chk_doc_number_counters_next_no CHECK (next_no >= 0)
);

COMMENT ON TABLE doc_number_counters IS '单据编号计数器（backend-m2-plan §4.1：发放=事务内 UPDATE RETURNING，行锁原子；首号 1，号码不回收，事务回滚产生的空洞符合 business-flow §13.1 不连续口径）';
COMMENT ON COLUMN doc_number_counters.prefix IS '单据前缀（M2 冻结注册表：PO/IN/RC/QC/PW/SO/OUT/PK/CH/BP/SH/TR/CK/RT/EX + M1 存量 LED/ADJ，internal/docnum 冻结）';
COMMENT ON COLUMN doc_number_counters.period IS '取号周期：YYYYMMDD 按日重置 / ALL 永不重置（business-flow §13.1 流水按日从 000001 起；LED/ADJ 承接为 ALL，plan §4.1）';
COMMENT ON COLUMN doc_number_counters.next_no IS '下一个待发流水号（发放即 +1；updated_at 记录最近发放时间）';

CREATE TABLE document_approvals (
    id            bigserial   PRIMARY KEY,
    target_type   varchar(32) NOT NULL,
    target_no     varchar(64) NOT NULL,
    action        varchar(16) NOT NULL,
    result        varchar(16) NOT NULL DEFAULT '',
    opinion       text        NOT NULL DEFAULT '',
    operator_id   bigint      NOT NULL DEFAULT 0,
    operator_name varchar(64) NOT NULL DEFAULT '',
    request_id    varchar(64) NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_document_approvals_action CHECK (action IN ('SUBMIT', 'APPROVE', 'REJECT', 'CANCEL'))
);

COMMENT ON TABLE document_approvals IS '审批记录（business-flow §12：采购/销售/调拨/退货/库存调整/盘点差异审批统一落位；append-only 审计数据，database.md §7——不可修改删除）';
COMMENT ON COLUMN document_approvals.target_type IS '审批对象单据类型（purchase_order/sales_order/transfer_order/return_order/inventory_adjustment/count_difference 等，随域扩展由应用层取值）';
COMMENT ON COLUMN document_approvals.target_no IS '审批对象单号（跨域逻辑引用，不建 FK）';
COMMENT ON COLUMN document_approvals.action IS '动作（plan §5：SUBMIT 提交/APPROVE 通过/REJECT 驳回/CANCEL 撤销）';
COMMENT ON COLUMN document_approvals.result IS '动作结果（APPROVED/REJECTED/CANCELLED 等；SUBMIT 落空串，由后续动作承载结果）';
COMMENT ON COLUMN document_approvals.operator_id IS '操作人（users 逻辑引用；与 operation_logs 口径一致，跨域不建 FK）';
COMMENT ON COLUMN document_approvals.request_id IS '请求链路 ID（RequestID 中间件，与 operation_logs/库存流水一致的可追溯性字段）';
COMMENT ON COLUMN document_approvals.created_at IS '审批时间（business-flow §12.2 审批必须记录审批时间）';

CREATE INDEX idx_document_approvals_target ON document_approvals (target_type, target_no);
