# StockFlow 项目文档索引

**项目名称**：StockFlow（库流智能仓储管理系统）
**英文名称**：StockFlow Intelligent Warehouse Management System
**项目定位**：能够按照真实仓库业务流程运行、并具备生产环境基础能力的现代化 WMS（仓储管理系统）
**技术栈**：后端 **Go**（高并发低延迟导向选型，见 [architecture.md §11.2](architecture.md)）；前端 **React 18+ + Ant Design 6 最新稳定版**（https://ant.design/index-cn ，见 [architecture.md §11.3](architecture.md) 与 [frontend.md §1](frontend.md)）；终端体系 **PC Web + Pad + StockFlow Scan（Android 工业 PDA）**（见 [devices.md](devices.md)）

---

## 1. 项目定位声明

StockFlow 不是"商品/入库/出库/库存 CRUD + 几个 Dashboard 卡片"的毕业设计模板，而是围绕以下完整业务闭环构建的生产级 WMS：

```text
基础资料
→ 采购/销售业务
→ 入库/收货/质检/上架
→ 库存
→ 调拨
→ 盘点
→ 出库/拣货/复核/打包/发货
→ 退货
→ 追溯
→ 报表
→ 审计
```

## 2. 核心设计原则（全项目强制）

按优先级排序：

1. 真实业务优先
2. 数据正确优先
3. 库存一致性优先
4. 安全优先
5. 可维护性优先
6. 可扩展性优先
7. 用户体验优先

**每个业务功能必须回答以下问题**（设计评审清单）：

```text
谁操作？什么时候操作？对什么数据操作？
操作前是什么？操作后是什么？
是否需要审核？是否影响库存？是否生成流水？
是否需要日志？失败如何恢复？重复提交怎么办？
```

**开发过程强制约束**：

- 任何开发任务开始前，必须先阅读项目结构、前后端架构、数据库、API、认证权限、组件、配置与文档，确认已有实现后再动手。
- 禁止：没看代码直接重写、重复实现已有模块、随意更换技术栈、删除已有稳定功能、为视觉效果破坏业务代码、用假数据冒充真实业务、做只有 UI 没有后端逻辑的功能。
- 现有代码质量较差时可做模块级重构，但必须保留有效业务能力。
- 所有功能必须形成 `前端 → API → Service → Repository → Database` 完整链路，禁止假功能（详见 [requirements.md §10](requirements.md)）。

## 3. 文档地图

| 文档 | 内容 | 对应读者 |
|---|---|---|
| [requirements.md](requirements.md) | 需求规格：功能需求总览、系统页面清单、验收场景、演示数据 | 全员 |
| [business-flow.md](business-flow.md) | 业务流程与单据规范：采购/入库/质检/上架/销售/出库/拣货/复核/打包/发货/退货/调拨/盘点/异常中心/审批/单据编号/状态机 | 后端、前端、测试 |
| [inventory-rules.md](inventory-rules.md) | 库存核心规则：库存模型、库存维度、库存流水、库存锁定、并发控制、批次/效期/序列号、库存追溯、智能能力 | 后端、测试 |
| [architecture.md](architecture.md) | 系统架构与工程规范：错误处理、Request ID、幂等、事务、并发与锁、性能、日志体系、定时任务、通知中心 | 后端 |
| [database.md](database.md) | 数据库设计规范：核心实体、通用字段、软删除、多仓模型、数据库迁移、审计不可破坏 | 后端 |
| [api.md](api.md) | API 设计规范：领域划分、统一标准、接口文档、数据校验、生产安全 | 后端、前端 |
| [permission.md](permission.md) | 权限与认证设计：角色体系、RBAC、数据权限、登录与会话管理 | 后端、前端 |
| [excel.md](excel.md) | Excel 与数据中心：导入中心、校验规则、导出规范、大数据量导出、文件中心 | 后端、前端 |
| [printing.md](printing.md) | 打印中心：打印模板、批量打印、二维码与条码、打印预览 | 后端、前端 |
| [frontend.md](frontend.md) | 前端规范（Web/Pad/Scan 三端）：Design Token 与主题、信息密度、PC Layout/Sidebar、Dashboard、表格系统、详情页、库存中心/盘点/Excel/打印/设备管理页面、Pad 与 Scan UI、状态管理、组件封装、工程规范、F1–F20 实施阶段 | 前端 |
| [scanner.md](scanner.md) | 扫码枪/条码设备深度接入：设备接入层、Scanner 架构、统一解析、交互规范、14 类业务扫码场景、箱码/托盘、验收标准 | 前端、后端、测试 |
| [devices.md](devices.md) | 设备接入与多终端体系：PC/Pad/StockFlow Scan 三端结构、工业 PDA 厂商适配（Zebra/Honeywell/UROVO）、设备生命周期与监控、离线容错、真实设备验收 | 前端、后端、测试 |
| [deployment.md](deployment.md) | 部署与运维：环境配置、健康检查、备份恢复、系统监控、告警、初始化数据 | 后端、运维 |
| [testing.md](testing.md) | 测试规范：单元/集成测试、库存专项测试、Excel/打印测试、异常恢复 | 测试 |
| [development-plan.md](development-plan.md) | 开发计划：22 个开发阶段、每阶段完成标准、开工前项目分析清单 | 全员 |
| [changelog.md](changelog.md) | 变更日志 | 全员 |

## 4. 阅读顺序建议

- **新加入的开发者**：README → requirements → development-plan → 自己负责的领域文档
- **后端开发**：inventory-rules 与 business-flow 是业务正确性的唯一依据，必须精读；architecture、database、api 为工程规范；涉及扫码业务的还需精读 scanner.md §5–7（解析 API 与业务场景接口）
- **前端开发**：frontend.md 是前端唯一依据（三端 UI/UX、组件体系、工程规范），business-flow.md 了解业务流转，api.md 了解接口约定；涉及扫码作业页面的必须精读 scanner.md，设备与 Scan 应用见 devices.md
- **测试**：testing.md + business-flow.md + inventory-rules.md（库存测试是重点）+ scanner.md §11（扫码枪现场验收）

## 5. 文档维护约定

- 文档随代码同步维护，任何业务规则变更必须先改文档再改代码。
- 每次模块交付在 [changelog.md](changelog.md) 记录。
- 库存规则（inventory-rules.md）变更属于重大变更，需在文档中记录变更原因与影响面。
