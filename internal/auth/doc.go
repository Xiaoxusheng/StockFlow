// Package auth 认证权限域（backend-m1-plan §2/§5.1，scope C）。
//
// 职责：登录/登出/刷新/会话/踢下线、JWT、RBAC、数据权限、登录保护、
// users/roles/permissions/departments API、空库初始化委托（plan §5.4、§7）。
//
// 交付形态（导出签名与 plan §5.1 冻结契约一致）：
//   - RegisterPublicRoutes：POST /api/auth/login、POST /api/auth/refresh；
//   - RegisterProtectedRoutes：/auth/logout、/auth/me、/auth/password、/auth/sessions、
//     /users、/roles、/permissions、/departments（各自挂 RequirePermission，plan §5.4）；
//   - AuthRequired：Bearer JWT（HS256，golang-jwt/v5）+ Redis 会话双轨校验
//     （被踢/过期→401 SESSION_INVALID，plan §7.2），注入 UserContext 与会话快照；
//   - RequirePermission：RBAC 权限点校验（权限点清单见 permissions.go，与
//     plan §5.4.1 冻结清单及权限种子同源），失败 403；
//   - CurrentUser / WarehouseScope：用户上下文与数据权限范围快照；
//     ApplyWarehouseScope / SelfScope / DepartmentScope：数据权限过滤助手
//     （permission.md §4，供三个业务域 Service 直接使用）；
//   - BootstrapIfEmpty：按 Orchestrator 裁决委托 internal/database.BootstrapIfEmpty
//     （禁止第二套种子逻辑）。
//
// 运行配置（SF_AUTH_* 环境变量，域名内解析——auth 禁止 import config，plan §3）：
//   - SF_AUTH_JWT_SECRET：HS256 签名密钥（≥16 字节）。release 模式缺失即启动失败
//     （deployment.md §3 禁止带病启动；禁止硬编码 Secret）；debug/test 缺失时降级为
//     进程内随机密钥（重启即全部 Token 失效，仅限本地冒烟与单测）；
//   - SF_AUTH_ACCESS_TTL（默认 2h）、SF_AUTH_REFRESH_TTL（默认 168h=7d 滑动）、
//     SF_AUTH_MAX_LOGIN_FAILURES（默认 5）、SF_AUTH_LOCK_DURATION（默认 15m）。
//
// 安全基线：bcrypt cost 12；登录失败计数 Redis + 锁定落库；防账号枚举（统一错误码 +
// 恒定代价假比较）；日志/快照禁密码与 Token（architecture.md §6）；权限缓存 fail-closed。
package auth
