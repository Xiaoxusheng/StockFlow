-- 000018：质检单 result CHECK 放宽空串逃生门（手动建单运行时 500 修复）。
--
-- 背景：quality_orders.result 列 NOT NULL DEFAULT ''（000007），而
-- chk_quality_orders_result 只允许九类中文处理结果、无空串逃生门。质检单状态机
-- PENDING → INSPECTING → COMPLETED，result 在 COMPLETED 才落定——创建期（PENDING）
-- 与检验中（INSPECTING）result 语义即"未检"，模型零值空串（QualityOrder.Result
-- string，internal/purchase/models.go:290）是唯一诚实取值。
--
-- 缺陷实测（e2e 冒烟，request_id 316a1b79）：POST /api/quality 手动建单必 500
-- 「violates check constraint chk_quality_orders_result」——两条创建路径
-- internal/purchase/service_quality.go CreateQC（:138 起，入库质检）与
-- QCCreatorService.CreateQC（:521 起，退货质检）构造 QualityOrder 均不设 Result，
-- INSERT 零值空串违反 CHECK。
--
-- 修复口径：给 CHECK 补 result = '' 逃生门（未检语义），不动列默认值、不动九类
-- 值域、不发明"未检"伪结果值；质检完成动作（UpdateQC cols）仍由业务侧保证落定为
-- 九类之一。实现方式：CHECK 约束不可 ALTER，"删旧建新"（先例 000017）；down 还原
-- 000007 原约束。现存数据 result 值域 ⊆ {''} ∪ 九类，重放安全（up/down 各自成对
-- 执行一次，golang-migrate version 幂等）。

ALTER TABLE quality_orders DROP CONSTRAINT chk_quality_orders_result;

ALTER TABLE quality_orders ADD CONSTRAINT chk_quality_orders_result
    CHECK (result = '' OR result IN ('合格', '部分合格', '不合格', '退供应商', '报废', '返工', '降级', '转不良品仓', '特批放行'));
