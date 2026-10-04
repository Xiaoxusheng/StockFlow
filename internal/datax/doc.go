// Package datax Excel 导入导出与文件中心（backend-m3-plan §2.1 Scope X、excel.md §1–§7、api.md §5）。
//
// 职责：
//   - 导入中心：九类导入完整向导状态机（excel §1.2：模板下载 → 上传解析 → 校验 → 预览 →
//     确认导入 → 结果），校验管线分结构层（本包：必填/类型/日期/数字/金额/文件内重复）与
//     业务层（各域 Writer：存在性/库内重复/业务关系），错误定位到行列并生成错误 Excel；
//   - 导出中心：16 模块异步导出（QUEUED → PROCESSING → SUCCESS/FAILED），excelize
//     StreamWriter 按 keyset 游标分批流式写（禁止 offset 深翻页、禁止整表驻留内存），
//     进度每批回写 export_tasks.progress；
//   - 文件中心：上传（api.md §5 全量安全校验经 internal/storage）/ 下载（权限 + 审计）/ 预览 /
//     列表 / 软删除；存储名与路径一律服务端生成。
//
// 异步任务经 internal/asynqx 基座（Queue 接口 + datax:import:commit / datax:export:run
// 两个冻结任务类型）；任务进度与状态真相源是任务表（plan §4.1），不依赖队列内存 API。
// 跨域数据访问只经 §12.2 冻结窄接口（ImportWriter / ExportSource）：接口与值类型在本包
// 定义，实现落各域包冻结新增文件（masterdata/datax_import.go 等），router 装配注入——
// 本包禁止 import 任何业务域包（plan §2.3 判据 2）。
//
// 写 SQL 白名单（plan §2.3 判据 3）：import_tasks / import_task_rows / export_tasks / files。
// 库存数据零旁路：本包对库存零读写，期初库存导入经 inventory 域 Writer 内部走库存原语。
package datax
