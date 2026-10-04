-- 000018 down：还原 000007 冻结的九类 CHECK（无空串逃生门）。回滚前业务侧须保证
-- quality_orders 无 result = '' 行（即全部质检单已检完），否则约束重建将失败——
-- 回滚属于人工运维动作，先补齐结果值再降版。

ALTER TABLE quality_orders DROP CONSTRAINT chk_quality_orders_result;

ALTER TABLE quality_orders ADD CONSTRAINT chk_quality_orders_result
    CHECK (result IN ('合格', '部分合格', '不合格', '退供应商', '报废', '返工', '降级', '转不良品仓', '特批放行'));
