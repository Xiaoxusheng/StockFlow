-- db/grants/app_grants.sql —— 审计数据账号权限分层（database.md §7.2、backend-m1-plan §6.4）
--
-- 目的：库存流水（inventory_ledgers）、审批记录（document_approvals）、关键操作日志
--       （operation_logs）、登录日志（login_logs）属审计数据（database.md §7：可查询、
--       可追溯、不可随意篡改）。应用层不提供这些表的 UPDATE/DELETE 接口之外，数据库侧
--       再做一层硬约束：业务运行账号对审计表只有 SELECT + INSERT，
--       无 UPDATE/DELETE/TRUNCATE（双保险）。
--
-- 用法（生产首次部署前，由 DBA 以超级用户执行）：
--   psql -d stockflow -v app_user="<业务运行账号>" -f db/grants/app_grants.sql
-- 开发环境使用属主账号（plan §6.4），可跳过本脚本，不影响迁移验证。
--
-- 维护约定：M2+ 新增审计类表（如审批记录）时，必须同步把表名追加到下方 REVOKE/GRANT 清单。

\set ON_ERROR_STOP on

-- 业务运行账号对全库业务表的基础 DML 权限
GRANT USAGE ON SCHEMA public TO :"app_user";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO :"app_user";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO :"app_user";

-- 审计表分层：收回一切写通路，仅保留查询与追加
-- （M3 追加 scan_logs/device_logs：backend-m3-plan §5 000013 append-only 审计表 + grants 纪律）
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON inventory_ledgers, document_approvals, operation_logs, login_logs, scan_logs, device_logs
    FROM :"app_user";
GRANT SELECT, INSERT
    ON inventory_ledgers, document_approvals, operation_logs, login_logs, scan_logs, device_logs
    TO :"app_user";

-- 注：ALTER DEFAULT PRIVILEGES 无法按“表是否审计表”区分，新建审计表须人工重跑本脚本
-- （或在本迁移交付时同步更新本文件并列入发布检查单，见 deployment.md §4）。

-- ====== M3 追加：清理维护角色段（backend-m3-plan §5 grants 纪律、§4.3/§15） ======
-- 审计日志/扫码日志的保留期清理由部署侧维护脚本以独立维护角色执行（database.md §7.2/§7.3）：
--   * 应用运行账号对审计表仍无 UPDATE/DELETE（上方分层不破——log_cleanup 不在应用内执行的
--     通路依据，plan §15"审计日志/扫码日志应用内清理不做"）；
--   * 维护角色仅持审计四表的 SELECT（定位过期行）+ DELETE（删除），无其他表权限；
--   * 角色密码/有效期由 DBA 创建后 ALTER ROLE 设置（本脚本不触碰凭据）。
-- 可选段：仅当 -v maint_user="<清理维护账号>" 传入时执行；重跑幂等（角色已存在则跳过创建）。
\if :{?maint_user}
SET sf.maint_user = :'maint_user';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_setting('sf.maint_user')) THEN
        EXECUTE format('CREATE ROLE %I LOGIN', current_setting('sf.maint_user'));
    END IF;
END
$$;
GRANT USAGE ON SCHEMA public TO :"maint_user";
GRANT SELECT, DELETE
    ON operation_logs, login_logs, scan_logs, device_logs
    TO :"maint_user";
\endif
