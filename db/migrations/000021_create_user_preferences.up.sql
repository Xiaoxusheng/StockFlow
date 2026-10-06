-- 000021 用户偏好（效率提升层一期 §3.2，B1 波次）。
-- 复合主键即全量索引，无冗余索引；无 created_by（个人高频态，不挂审计——计划 §2.3）。
CREATE TABLE user_preferences (
    user_id    bigint      NOT NULL,
    pref_key   varchar(64) NOT NULL,
    pref_value jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pref_key),
    CONSTRAINT chk_user_preferences_key CHECK (pref_key ~ '^[a-z0-9_.]{1,64}$')
);
