-- db/grants/app_grants.sql —— 审计数据账号权限分层（database.md §7.2、backend-m1-plan §6.4）
--
-- 目的：库存流水（inventory_ledgers）、关键操作日志（operation_logs）、登录日志（login_logs）
--       属审计数据（database.md §7：可查询、可追溯、不可随意篡改）。应用层不提供这些表的
--       UPDATE/DELETE 接口之外，数据库侧再做一层硬约束：业务运行账号对审计表只有
--       SELECT + INSERT，无 UPDATE/DELETE/TRUNCATE（双保险）。
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
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON inventory_ledgers, operation_logs, login_logs
    FROM :"app_user";
GRANT SELECT, INSERT
    ON inventory_ledgers, operation_logs, login_logs
    TO :"app_user";

-- 注：ALTER DEFAULT PRIVILEGES 无法按“表是否审计表”区分，新建审计表须人工重跑本脚本
-- （或在本迁移交付时同步更新本文件并列入发布检查单，见 deployment.md §4）。
