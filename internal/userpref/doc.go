// Package userpref 用户态个性化域（效率提升层一期计划 §2.2/§2.3，B1 波次）。
//
// 两类个人数据，仅认证即可访问（无权限点，notifications 个人收件箱同口径——
// 个人数据自见原则）：
//
//   - 保存视图 user_saved_views（迁移 000020）：GET/POST /api/user/views、
//     PUT/DELETE /api/user/views/:id。严格用户隔离——全部查询强制 user_id=当前用户，
//     PUT/DELETE 先按 (id, user_id) 查归属，非本人一律 404（防探测）。
//   - 用户偏好 user_preferences（迁移 000021）：GET /api/user/preferences?keys=a,b、
//     PUT /api/user/preferences/:key。key 服务端白名单恰 7 个；recent_visits
//     服务端裁剪至 20 条。不挂 Audit 中间件（recent_visits 高频写会刷屏审计日志）。
//
// 本包不含 router.go 装配——由主会话（集成波次 I）调用 RegisterRoutes 挂 protected 组。
package userpref
