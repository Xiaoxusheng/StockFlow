-- 000020 用户保存视图（效率提升层一期 §3.1，B1 波次）。
-- 用户隔离由应用层强制 user_id=当前用户；is_default 部分唯一索引兜底每
-- (user_id, page_key) 至多一个默认视图（服务层「设为默认」仍在单事务内清除旧默认）。
CREATE TABLE user_saved_views (
    id           bigserial    PRIMARY KEY,
    user_id      bigint       NOT NULL,
    page_key     varchar(64)  NOT NULL,
    name         varchar(64)  NOT NULL,
    filters_json jsonb        NOT NULL DEFAULT '{}'::jsonb,
    sort_json    jsonb        NOT NULL DEFAULT '{}'::jsonb,
    columns_json jsonb        NOT NULL DEFAULT '[]'::jsonb,
    page_size    integer      NOT NULL DEFAULT 20,   -- 上限对齐 API 分页 1-100（workbench.go:883 口径），存 200 会导致应用视图时列表端点 400
    is_default   boolean      NOT NULL DEFAULT FALSE,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint       NOT NULL DEFAULT 0,
    updated_by   bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_user_saved_views_page_key CHECK (page_key ~ '^[a-z0-9._-]{1,64}$'),
    CONSTRAINT chk_user_saved_views_page_size CHECK (page_size BETWEEN 1 AND 100)
);
CREATE UNIQUE INDEX uk_user_saved_views_name    ON user_saved_views (user_id, page_key, name);
CREATE UNIQUE INDEX uk_user_saved_views_default ON user_saved_views (user_id, page_key) WHERE is_default;
