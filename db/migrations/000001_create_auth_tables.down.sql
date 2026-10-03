-- 000001 auth 域回滚：DROP 顺序与 000001 up 的依赖相反
-- （先 join 表/子表，再被引用表；database.md §9：每个迁移本地升/降级双向验证）

DROP INDEX IF EXISTS idx_user_warehouses_warehouse_id;
DROP INDEX IF EXISTS idx_role_permissions_permission_id;
DROP INDEX IF EXISTS idx_user_roles_role_id;

ALTER TABLE user_warehouses DROP CONSTRAINT IF EXISTS fk_user_warehouses_user;
ALTER TABLE role_permissions DROP CONSTRAINT IF EXISTS fk_role_permissions_permission;
ALTER TABLE role_permissions DROP CONSTRAINT IF EXISTS fk_role_permissions_role;
ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS fk_user_roles_role;
ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS fk_user_roles_user;

DROP TABLE IF EXISTS user_warehouses;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS user_roles;

DROP INDEX IF EXISTS idx_users_department_id;
DROP INDEX IF EXISTS uk_users_username;

DROP TABLE IF EXISTS users;
DROP INDEX IF EXISTS idx_permissions_parent_id;
DROP INDEX IF EXISTS uk_permissions_code;
DROP TABLE IF EXISTS permissions;
DROP INDEX IF EXISTS uk_roles_code;
DROP TABLE IF EXISTS roles;
DROP INDEX IF EXISTS idx_departments_parent_id;
DROP INDEX IF EXISTS uk_departments_code;
DROP TABLE IF EXISTS departments;
