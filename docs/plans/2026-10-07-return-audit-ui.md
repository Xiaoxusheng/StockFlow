# 退货单审核 UI 接入（销售退货 + 采购退货）

> 2026-10-07。后端退货域 submit/approve 接口已交付（internal/returns/handler.go:119-132），
> 前端两个退货列表页只有创建入口，单据停在「待审核」后界面上无人能审。本计划补齐审核 UI。

## 目标

在销售退货（/sales/returns）与采购退货（/purchases/returns）列表页接入单据级审核操作，
打通「草稿 → 提交审核 → 待审核 → 审核通过/驳回」前端闭环的第一段。

## 当前实现与问题

- 后端已就绪：
  - `POST /api/returns/{id}/submit`（DRAFT→PENDING_APPROVAL，perm `returns:salesreturn:submit`）
  - `POST /api/purchase-returns/{id}/submit`（perm `returns:purchasereturn:submit`）
  - `POST /api/returns/{id}/approve` / `POST /api/purchase-returns/{id}/approve`
    （入参 `ApproveInput {approved bool, opinion string}`：approved=true → APPROVED，
    false → 退回 DRAFT；perm `returns:salesreturn:approve` / `returns:purchasereturn:approve`；
    internal/returns/service.go:91-109 状态机、service_sales.go:381-430 ApproveReturn）
- 前端缺口：
  - api/sales.ts `salesApi.returns` 仅 list/create；api/purchase.ts `purchaseApi.returns` 同
  - 两个列表页无操作列、无详情页，审核无入口
- 复用模式：PurchaseOrderActions.tsx（详情页操作区）的 useMutation + SfConfirm + ReasonModal
  （驳回意见 TextArea）+ canAccess 权限×状态双重门控，本任务将同构模式收敛为列表行内共享组件。

## 实现方案

1. **API 层**（web/src/api/sales.ts、web/src/api/purchase.ts）：
   - 补权限常量 `*_RETURN_SUBMIT_PERMISSION` / `*_RETURN_APPROVE_PERMISSION`（internal/auth/permissions.go:252-268 同源）；
   - `salesApi.returns` / `purchaseApi.returns` 增 `submit(id)` 与 `approve(id, {approved, opinion?})`，
     响应均为 ReturnOrderView。
2. **共享组件** web/src/components/common/SfReturnAuditActions.tsx（新建）：
   - props `{ type: 'sales'|'purchase', orderId, status, onChanged }`，内部按 type 切换 api 与权限点；
   - DRAFT 行 + submit 权限 →「提交审核」（SfConfirm）；
   - PENDING_APPROVAL 行 + approve 权限 →「通过」（SfConfirm）+「驳回」（ReasonModal 弹窗，
     意见可选——后端 ApproveInput.Opinion 无必填校验，与采购订单驳回必填不同，如实按契约）；
   - 无可用操作时渲染「-」；成功后 message 提示 + onChanged()（列表失效重取）。
3. **列表页**（SalesReturnListPage.tsx、PurchaseReturnListPage.tsx）：
   - 增「操作」列（key=actions，fixed right，width 150，nowrap link 按钮，masterdata 列页同构）；
   - 当前用户同时不持 submit/approve 权限时整列不渲染；
   - scrollX 相应 +150；onChanged 复用创建抽屉的失效+refetch 逻辑。

## 做什么 / 不做什么

**做**：上列 3 项；docs/changelog.md 交付记录。

**不做**：不做退货详情页/时间线；不做收货、质检、出库、完成等后续执行动作的 UI（等后续批次）；
不做「取消退货」入口；不改后端；不动采购订单/销售订单既有审核实现；不引入新依赖。

## 修改文件

- web/src/api/sales.ts（+常量 +2 方法）
- web/src/api/purchase.ts（+常量 +2 方法）
- web/src/components/common/SfReturnAuditActions.tsx（新建）
- web/src/views/sales/SalesReturnListPage.tsx（+操作列）
- web/src/views/purchase/PurchaseReturnListPage.tsx（+操作列）
- docs/changelog.md

## 测试方案

- `cd web && npm run build`（tsc -b strict + vite build）通过；
- 浏览器实测（后端可连时）：草稿行「提交审核」→ 状态变待审核；待审核行「通过」→ 已审核；
  「驳回」填意见 → 退回草稿；权限收窄账号操作列收敛。

## 风险

- 驳回意见前后端约束不一致（采购订单后端必填、退货后端可选）——按各自后端契约如实呈现，
  不在前端擅自加严或放松；
- 并发状态变更后端返回 409 域内码，前端 resolveErrorMessage 展示即可，列表刷新后状态归真。
