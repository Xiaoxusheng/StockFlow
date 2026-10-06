-- 000024 幂等键表（作业效率提升层一期 §2.10 追加交付：通用幂等执行权仲裁 + 响应快照）。
-- 说明：计划 §2.10 原裁决「不做通用幂等结果缓存表」（重放=inventory_ledgers 唯一索引
-- 兜底），实施批按实现 ask 升级为落表交付——为不产生 ledger 流水的写端点（批量操作/
-- 重导等）提供与库存原语同强度的一次执行保证。
-- 语义：
--   * 首次请求 INSERT 占用（status=PROCESSING）→ 真实执行 → 落响应快照（COMPLETED）；
--   * 重复提交命中唯一索引 → 回放首次响应快照（同键同端点同用户维度）；
--   * 并发同键：INSERT ... ON CONFLICT DO NOTHING 恰一方占用，余者 409
--     IDEMPOTENCY_IN_PROGRESS——数据库唯一索引为仲裁真相源，应用层不做二次判定；
--   * request_hash = 请求体 SHA-256：同键换载荷 409 IDEMPOTENCY_REQUEST_MISMATCH
--     （客户端键复用缺陷防御）；
--   * 行生命周期：PROCESSING 行由执行方 Complete/Release 收尾；孤儿行（执行进程
--     崩溃）由后续清理任务按 created_at 回收（idx_idempotency_keys_created_at）——
--     一期不建定时任务，挂账 sysops 清理作业。
--   * 通用字段遵循 database.md §3 业务表默认口径；无 deleted_at（幂等行无软删语义，
--     清理为物理删除）。
CREATE TABLE idempotency_keys (
    id                bigserial    PRIMARY KEY,
    key               varchar(64)  NOT NULL,
    user_id           bigint       NOT NULL,
    endpoint          varchar(128) NOT NULL,
    request_hash      varchar(64)  NOT NULL,
    response_snapshot jsonb        NOT NULL DEFAULT '{}'::jsonb,
    status            varchar(16)  NOT NULL DEFAULT 'PROCESSING',
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint       NOT NULL DEFAULT 0,
    updated_by        bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_idempotency_keys_key CHECK (key ~ '^[A-Za-z0-9._:-]{1,64}$'),
    CONSTRAINT chk_idempotency_keys_status CHECK (status IN ('PROCESSING', 'COMPLETED'))
);

COMMENT ON TABLE idempotency_keys IS '幂等键（效率层一期 §2.10：首次执行落响应快照、重复提交回放、并发同键唯一索引仲裁恰一执行；高风险端点经 internal/idempotency 中间件挂载）';
COMMENT ON COLUMN idempotency_keys.key IS '客户端幂等键（Idempotency-Key 头；UUID 或 [A-Za-z0-9._:-] 1-64 字符）';
COMMENT ON COLUMN idempotency_keys.user_id IS '提交人（users 裸 ID 引用，不建 FK；同键不同用户互不干扰）';
COMMENT ON COLUMN idempotency_keys.endpoint IS '端点维度（method + 路由模式，如 POST /api/receipts；同键不同端点互不干扰）';
COMMENT ON COLUMN idempotency_keys.request_hash IS '请求体 SHA-256（hex 64；同键换载荷拒绝 409）';
COMMENT ON COLUMN idempotency_keys.response_snapshot IS '首次响应快照（jsonb {status, body}；回放时改写当前 request_id）';
COMMENT ON COLUMN idempotency_keys.status IS 'PROCESSING=执行中（占用）→ COMPLETED=快照就绪（可回放）';

CREATE UNIQUE INDEX uk_idempotency_keys ON idempotency_keys (key, user_id, endpoint);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
