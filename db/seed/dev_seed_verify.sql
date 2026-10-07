-- db/seed/dev_seed_verify.sql —— 演示数据灌数后只读自检（make seed-demo-verify）
--
-- 用途：在执行 db/seed/dev_seed.sql（make seed-demo）之后运行，断言演示集完整且口径正确：
--   1) 实体覆盖满足 requirements.md §9 / dev_seed.sql 头注的最低规模；
--   2) 库存恒等式成立（inventory-rules §2，数据库 CHECK 之外的复核）；
--   3) 期初库存与期初流水 1:1 成对（qty_before=0 → qty_after=n，business_type='期初'，
--      remark='DEV SEED'，business_no='DEV-SEED-OPEN-*'）；
--   4) 序列号台账数与序列号 SKU 的期初库存吻合（一物一行，inventory-rules §8）；
--   5) 演示补全轮（2026-10-06，dev_seed.sql §12–§17）覆盖：库存四态（锁定/冻结/待检/残次）、
--      库存锁定 5 类型×3 态、调整单 6 态、退货（销售/采购）与异常九类、设备/扫码、
--      文件/导入/导出、定时任务执行日志/通知/备份，以及各单据状态机全值域（每态都有命中行）。
--
-- 本文件只读（仅 SELECT/DO 内断言），任何断言失败即 RAISE EXCEPTION 退出（配合
-- ON_ERROR_STOP，psql 返回非 0），可安全在任何环境执行；演示数据缺失时首项断言即报错。
-- 识别约定：dev_seed.sql 全部业务行使用 9xxx 段显式主键、dev_* 账号、WH-D* 仓库编码、
-- 'DEV-SEED-OPEN-*' 流水 business_no——本脚本据此圈定演示数据范围。

\set ON_ERROR_STOP on

\echo '>>> dev_seed_verify.sql：演示数据自检开始...'

-- ---- 1) 测试账号：5 个、全部 ACTIVE、绑定仓库 ≥2 个（数据权限验证前提） ----
DO $$
DECLARE n int; whs int;
BEGIN
    SELECT count(*) INTO n FROM users
    WHERE username IN ('dev_manager', 'dev_receiver', 'dev_shipper', 'dev_stocktaker', 'dev_viewer');
    IF n <> 5 THEN
        RAISE EXCEPTION '[账号] dev 测试账号应为 5 个，实际 %（演示数据未注入或被改动，请先执行 make seed-demo）', n;
    END IF;
    SELECT count(*) INTO n FROM users
    WHERE username IN ('dev_manager', 'dev_receiver', 'dev_shipper', 'dev_stocktaker', 'dev_viewer')
      AND status = 'ACTIVE';
    IF n <> 5 THEN
        RAISE EXCEPTION '[账号] dev 测试账号存在非 ACTIVE 状态';
    END IF;
    SELECT count(DISTINCT warehouse_id) INTO whs FROM user_warehouses
    WHERE user_id IN (SELECT id FROM users WHERE username LIKE 'dev\_%');
    IF whs < 2 THEN
        RAISE EXCEPTION '[账号] dev 账号绑定仓库数应 ≥2（供验证 SPECIFIED_WAREHOUSE 数据权限），实际 %', whs;
    END IF;
END
$$;

-- ---- 2) 角色绑定：5 条（仓库经理/收货员/发货员/盘点员/财务查看） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n FROM user_roles
    WHERE user_id IN (SELECT id FROM users WHERE username LIKE 'dev\_%');
    IF n <> 5 THEN
        RAISE EXCEPTION '[角色] dev 账号角色绑定应为 5 条，实际 %', n;
    END IF;
END
$$;

-- ---- 3) 仓库四级结构：2 仓 × 每仓库区 ≥2 × 每库区货架 ≥2 × 每货架库位 ≥2 ----
DO $$
DECLARE n int; minz int; mins int; minb int;
BEGIN
    SELECT count(*) INTO n FROM warehouses
    WHERE id IN (9101, 9102) AND code IN ('WH-D01', 'WH-D02');
    IF n <> 2 THEN
        RAISE EXCEPTION '[仓库] 演示仓库应为 2 个（WH-D01/WH-D02），实际 %', n;
    END IF;
    SELECT COALESCE(min(cnt), 0) INTO minz FROM (
        SELECT count(*) AS cnt FROM zones WHERE warehouse_id IN (9101, 9102) GROUP BY warehouse_id);
    IF minz < 2 THEN
        RAISE EXCEPTION '[仓库] 每仓库区数应 ≥2，实际最少 %', minz;
    END IF;
    SELECT COALESCE(min(cnt), 0) INTO mins FROM (
        SELECT count(*) AS cnt FROM shelves WHERE zone_id IN (SELECT id FROM zones WHERE warehouse_id IN (9101, 9102)) GROUP BY zone_id);
    IF mins < 2 THEN
        RAISE EXCEPTION '[仓库] 每库区货架数应 ≥2，实际最少 %', mins;
    END IF;
    SELECT COALESCE(min(cnt), 0) INTO minb FROM (
        SELECT count(*) AS cnt FROM bins WHERE shelf_id IN (
            SELECT id FROM shelves WHERE zone_id IN (SELECT id FROM zones WHERE warehouse_id IN (9101, 9102))) GROUP BY shelf_id);
    IF minb < 2 THEN
        RAISE EXCEPTION '[仓库] 每货架库位数应 ≥2，实际最少 %', minb;
    END IF;
END
$$;

-- ---- 4) 主数据：商品 ≥10 / SKU ≥20（四种三开关组合齐备）/ 条码 ≥20 / 供应商 ≥3 / 客户 ≥3 ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n FROM products WHERE id BETWEEN 9000 AND 9999;
    IF n < 10 THEN
        RAISE EXCEPTION '[商品] 演示商品应 ≥10，实际 %', n;
    END IF;
    SELECT count(*) INTO n FROM skus WHERE id BETWEEN 9000 AND 9999;
    IF n < 20 THEN
        RAISE EXCEPTION '[商品] 演示 SKU 应 ≥20，实际 %', n;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM skus WHERE id BETWEEN 9000 AND 9999
                   AND NOT is_batch_managed AND NOT is_expiry_managed AND NOT is_serial_managed)
       OR NOT EXISTS (SELECT 1 FROM skus WHERE id BETWEEN 9000 AND 9999
                      AND is_batch_managed AND NOT is_expiry_managed AND NOT is_serial_managed)
       OR NOT EXISTS (SELECT 1 FROM skus WHERE id BETWEEN 9000 AND 9999
                      AND is_batch_managed AND is_expiry_managed AND NOT is_serial_managed)
       OR NOT EXISTS (SELECT 1 FROM skus WHERE id BETWEEN 9000 AND 9999
                      AND NOT is_batch_managed AND NOT is_expiry_managed AND is_serial_managed) THEN
        RAISE EXCEPTION '[商品] SKU 未覆盖 批次/效期/序列号/全关 四种组合';
    END IF;
    SELECT count(*) INTO n FROM barcodes WHERE id BETWEEN 9000 AND 9999;
    IF n < 20 THEN
        RAISE EXCEPTION '[商品] 演示条码应 ≥20（每 SKU 主条码），实际 %', n;
    END IF;
    SELECT count(*) INTO n FROM suppliers WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN
        RAISE EXCEPTION '[往来] 演示供应商应 ≥3，实际 %', n;
    END IF;
    SELECT count(*) INTO n FROM customers WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN
        RAISE EXCEPTION '[往来] 演示客户应 ≥3，实际 %', n;
    END IF;
END
$$;

-- ---- 5) 库存恒等式（inventory-rules §2；CHECK 之外的独立复核，覆盖全表） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n FROM inventory
    WHERE total_qty <> available_qty + locked_qty + frozen_qty + pending_inspect_qty + defective_qty;
    IF n <> 0 THEN
        RAISE EXCEPTION '[恒等式] 存在 % 行库存违反 total = available + locked + frozen + pending_inspect + defective', n;
    END IF;
END
$$;

-- ---- 6) 期初库存 ↔ 期初流水：数量相等、双向无孤儿、口径一致（0 → n，DEV SEED 标记） ----
DO $$
DECLARE n_inv int; n_led int; n_orphan int; n_bad int;
BEGIN
    SELECT count(*) INTO n_inv FROM inventory WHERE id BETWEEN 9000 AND 9999;
    SELECT count(*) INTO n_led FROM inventory_ledgers
    WHERE id BETWEEN 9000 AND 9999 AND business_type = '期初';
    IF n_led <> n_inv THEN
        RAISE EXCEPTION '[期初成对] 期初库存行数 % 与期初流水行数 % 不相等（必须 1:1 成对）', n_inv, n_led;
    END IF;
    SELECT count(*) INTO n_orphan FROM inventory i
    WHERE i.id BETWEEN 9000 AND 9999
      AND NOT EXISTS (SELECT 1 FROM inventory_ledgers l
                      WHERE l.business_type = '期初'
                        AND l.warehouse_id = i.warehouse_id AND l.bin_id = i.bin_id
                        AND l.sku_id = i.sku_id AND l.batch_id = i.batch_id);
    IF n_orphan <> 0 THEN
        RAISE EXCEPTION '[期初成对] % 行期初库存缺少对应期初流水', n_orphan;
    END IF;
    SELECT count(*) INTO n_orphan FROM inventory_ledgers l
    WHERE l.id BETWEEN 9000 AND 9999 AND l.business_type = '期初'
      AND NOT EXISTS (SELECT 1 FROM inventory i
                      WHERE i.warehouse_id = l.warehouse_id AND i.bin_id = l.bin_id
                        AND i.sku_id = l.sku_id AND i.batch_id = l.batch_id);
    IF n_orphan <> 0 THEN
        RAISE EXCEPTION '[期初成对] % 行期初流水缺少对应期初库存行（孤儿流水）', n_orphan;
    END IF;
    SELECT count(*) INTO n_bad FROM inventory_ledgers
    WHERE id BETWEEN 9000 AND 9999 AND business_type = '期初'
      AND (change_type <> 'INBOUND' OR status_from <> 'available' OR status_to <> 'available'
           OR qty_before <> 0 OR qty_after <> qty_change
           OR remark <> 'DEV SEED' OR business_no NOT LIKE 'DEV-SEED-OPEN-%');
    IF n_bad <> 0 THEN
        RAISE EXCEPTION '[期初口径] % 行期初流水不满足 0→n/INBOUND/available/DEV SEED 标记', n_bad;
    END IF;
END
$$;

-- ---- 7) 序列号：一物一行数量与该 SKU 期初库存吻合（inventory-rules §8） ----
DO $$
DECLARE n_bad int;
BEGIN
    SELECT count(*) INTO n_bad FROM (
        SELECT wh.id AS wh_id, sku.id AS sku_id,
               (SELECT count(*) FROM serial_numbers sn
                WHERE sn.sku_id = sku.id AND sn.warehouse_id = wh.id AND sn.status = 'IN_STOCK') AS sn_cnt,
               (SELECT COALESCE(sum(i.available_qty), 0) FROM inventory i
                WHERE i.sku_id = sku.id AND i.warehouse_id = wh.id) AS inv_qty
        FROM (VALUES ('SKU-D001-01', 'WH-D01'), ('SKU-D002-01', 'WH-D01'), ('SKU-D006-01', 'WH-D02'))
                 AS v(sku_code, wh_code)
        JOIN skus sku ON sku.code = v.sku_code
        JOIN warehouses wh ON wh.code = v.wh_code) t
    WHERE sn_cnt <> inv_qty;
    IF n_bad <> 0 THEN
        RAISE EXCEPTION '[序列号] % 组 序列号 IN_STOCK 数与期初库存数不符（应一物一行吻合）', n_bad;
    END IF;
END
$$;

-- ---- 8) 库存状态覆盖：locked/frozen/pending_inspect/defective 四态齐备（§12.1） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n FROM inventory
    WHERE id BETWEEN 9000 AND 9999 AND locked_qty > 0;
    IF n = 0 THEN RAISE EXCEPTION '[库存状态] 缺少锁定态样例（locked_qty > 0）'; END IF;
    SELECT count(*) INTO n FROM inventory
    WHERE id BETWEEN 9000 AND 9999 AND frozen_qty > 0;
    IF n = 0 THEN RAISE EXCEPTION '[库存状态] 缺少冻结态样例（frozen_qty > 0）'; END IF;
    SELECT count(*) INTO n FROM inventory
    WHERE id BETWEEN 9000 AND 9999 AND pending_inspect_qty > 0;
    IF n = 0 THEN RAISE EXCEPTION '[库存状态] 缺少待检态样例（pending_inspect_qty > 0）'; END IF;
    SELECT count(*) INTO n FROM inventory
    WHERE id BETWEEN 9000 AND 9999 AND defective_qty > 0;
    IF n = 0 THEN RAISE EXCEPTION '[库存状态] 缺少残次态样例（defective_qty > 0）'; END IF;
END
$$;

-- ---- 9) 库存锁定：lock_type ≥5 值 / status 3 值全值域（§12.2） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT lock_type) INTO n FROM inventory_locks WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[库存锁定] lock_type 应覆盖 ≥5 种，实际 %', n; END IF;
    SELECT count(DISTINCT status) INTO n FROM inventory_locks WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[库存锁定] status 应覆盖 ACTIVE/RELEASED/CONSUMED 三态，实际 %', n; END IF;
END
$$;

-- ---- 10) 库存调整单：status 6 态 / adjust_type ≥5 类（§12.3） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT status) INTO n FROM inventory_adjustments WHERE id BETWEEN 9000 AND 9999;
    IF n < 6 THEN RAISE EXCEPTION '[库存调整] status 应覆盖 ≥6 态，实际 %', n; END IF;
    SELECT count(DISTINCT adjust_type) INTO n FROM inventory_adjustments WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[库存调整] adjust_type 应覆盖 ≥5 类，实际 %', n; END IF;
END
$$;

-- ---- 11) 退货单：销售/采购双类型 + status ≥6 态 + 明细 ≥5 行（§13.1-13.3） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT type) INTO n FROM return_orders WHERE id BETWEEN 9000 AND 9999;
    IF n < 2 THEN RAISE EXCEPTION '[退货] type 应覆盖 SALES/PURCHASE 两类，实际 %', n; END IF;
    SELECT count(DISTINCT status) INTO n FROM return_orders WHERE id BETWEEN 9000 AND 9999;
    IF n < 6 THEN RAISE EXCEPTION '[退货] status 应覆盖 ≥6 态，实际 %', n; END IF;
    SELECT count(*) INTO n FROM return_items WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[退货] 退货明细应 ≥5 行，实际 %', n; END IF;
END
$$;

-- ---- 12) 异常单：九类异常 × 六态生命周期（§13.4） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT type) INTO n FROM exceptions WHERE id BETWEEN 9000 AND 9999;
    IF n < 9 THEN RAISE EXCEPTION '[异常] type 应覆盖 ≥9 类，实际 %', n; END IF;
    SELECT count(DISTINCT status) INTO n FROM exceptions WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[异常] status 应覆盖 ≥5 态，实际 %', n; END IF;
END
$$;

-- ---- 13) 设备与扫码：设备类型 / 配置 / 日志级别 / 扫码审计 / App 版本（§14） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT type) INTO n FROM devices WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[设备] type 应覆盖 ≥3 种，实际 %', n; END IF;
    SELECT count(DISTINCT activation_status) INTO n FROM devices WHERE id BETWEEN 9000 AND 9999;
    IF n < 2 THEN RAISE EXCEPTION '[设备] activation_status 应覆盖 ≥2 态（ACTIVATED/PENDING），实际 %', n; END IF;
    SELECT count(*) INTO n FROM device_configs WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[设备] 设备配置应 ≥3 行，实际 %', n; END IF;
    SELECT count(DISTINCT level) INTO n FROM device_logs WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[设备] 运行日志级别应覆盖 INFO/WARN/ERROR，实际 %', n; END IF;
    SELECT count(*) INTO n FROM scan_logs WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[扫码] 扫码审计应 ≥5 行，实际 %', n; END IF;
    IF NOT EXISTS (SELECT 1 FROM scan_logs WHERE id BETWEEN 9000 AND 9999 AND success)
       OR NOT EXISTS (SELECT 1 FROM scan_logs WHERE id BETWEEN 9000 AND 9999 AND NOT success) THEN
        RAISE EXCEPTION '[扫码] 扫码成功/失败样例应齐备';
    END IF;
    SELECT count(DISTINCT status) INTO n FROM app_versions WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[设备] App 版本状态应覆盖 DRAFT/PUBLISHED/DEPRECATED，实际 %', n; END IF;
END
$$;

-- ---- 14) 数据域：文件登记 / 导入 / 导出（§15） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT module) INTO n FROM files WHERE id BETWEEN 9000 AND 9999;
    IF n < 4 THEN RAISE EXCEPTION '[文件] 文件登记模块应覆盖 ≥4 个，实际 %', n; END IF;
    SELECT count(DISTINCT status) INTO n FROM import_tasks WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[导入] status 应覆盖 ≥5 态，实际 %', n; END IF;
    SELECT count(*) INTO n FROM import_task_rows WHERE id BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[导入] 导入行应 ≥5 行，实际 %', n; END IF;
    SELECT count(DISTINCT status) INTO n FROM export_tasks WHERE id BETWEEN 9000 AND 9999;
    IF n < 4 THEN RAISE EXCEPTION '[导出] status 应覆盖 ≥4 态，实际 %', n; END IF;
END
$$;

-- ---- 15) 运维：定时任务执行日志 / 站内通知 / 备份登记（§16） ----
DO $$
DECLARE n int;
BEGIN
    SELECT count(DISTINCT "trigger") INTO n FROM scheduled_job_runs WHERE id BETWEEN 9000 AND 9999;
    IF n < 2 THEN RAISE EXCEPTION '[定时任务] trigger 应覆盖 ≥2 种（SCHEDULED/MANUAL），实际 %', n; END IF;
    SELECT count(DISTINCT type) INTO n FROM notifications WHERE id BETWEEN 9000 AND 9999;
    IF n < 4 THEN RAISE EXCEPTION '[通知] type 应覆盖 ≥4 类，实际 %', n; END IF;
    IF NOT EXISTS (SELECT 1 FROM notifications WHERE id BETWEEN 9000 AND 9999 AND read)
       OR NOT EXISTS (SELECT 1 FROM notifications WHERE id BETWEEN 9000 AND 9999 AND NOT read) THEN
        RAISE EXCEPTION '[通知] 已读/未读样例应齐备';
    END IF;
    SELECT count(DISTINCT status) INTO n FROM backup_records WHERE id BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[备份] status 应覆盖 ≥3 态，实际 %', n; END IF;
END
$$;

-- ---- 16) 单据状态机全值域：各单据列表页的每个状态筛选都有命中行（§11 + §17） ----
DO $$
DECLARE n int;
BEGIN
    -- 采购订单：DRAFT/PENDING_APPROVAL/APPROVED/PARTIAL_RECEIVED/RECEIVED_ALL/COMPLETED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM purchase_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 7 THEN RAISE EXCEPTION '[单据状态] purchase_orders 应覆盖 7 态，实际 %', n; END IF;
    -- 入库单：DRAFT/RECEIVING/AWAITING_QC/AWAITING_PUTAWAY/COMPLETED/CLOSED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM inbound_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 7 THEN RAISE EXCEPTION '[单据状态] inbound_orders 应覆盖 7 态，实际 %', n; END IF;
    -- 上架任务：PENDING/IN_PROGRESS/PAUSED/COMPLETED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM putaway_tasks WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[单据状态] putaway_tasks 应覆盖 5 态，实际 %', n; END IF;
    -- 质检单：PENDING/INSPECTING/COMPLETED
    SELECT count(DISTINCT status) INTO n FROM quality_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[单据状态] quality_orders 应覆盖 3 态，实际 %', n; END IF;
    -- 销售订单：DRAFT/PENDING_APPROVAL/APPROVED/REJECTED/PARTIAL_SHIPPED/SHIPPED_ALL/COMPLETED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM sales_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 7 THEN RAISE EXCEPTION '[单据状态] sales_orders 应覆盖 ≥7 态，实际 %', n; END IF;
    -- 出库单：PENDING_ALLOCATE/ALLOCATED/PICKING/PICKED/CHECKED/PACKED/PARTIAL_SHIPPED/SHIPPED_ALL/CANCELLED/CLOSED
    SELECT count(DISTINCT status) INTO n FROM outbound_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 8 THEN RAISE EXCEPTION '[单据状态] outbound_orders 应覆盖 ≥8 态，实际 %', n; END IF;
    -- 调拨单：DRAFT/PENDING_APPROVAL/APPROVED/TRANSFERRING/AWAITING_RECEIPT/COMPLETED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM transfer_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 6 THEN RAISE EXCEPTION '[单据状态] transfer_orders 应覆盖 ≥6 态，实际 %', n; END IF;
    -- 盘点单：DRAFT/COUNTING/PENDING_REVIEW/COMPLETED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM count_orders WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 5 THEN RAISE EXCEPTION '[单据状态] count_orders 应覆盖 5 态，实际 %', n; END IF;
    -- 拣货任务：PENDING/PICKING/PICKED/CANCELLED
    SELECT count(DISTINCT status) INTO n FROM pick_tasks WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[单据状态] pick_tasks 应覆盖 ≥3 态，实际 %', n; END IF;
    -- 发货单：PENDING/SHIPPED/DELIVERED/EXCEPTION
    SELECT count(DISTINCT status) INTO n FROM shipments WHERE created_by BETWEEN 9000 AND 9999;
    IF n < 3 THEN RAISE EXCEPTION '[单据状态] shipments 应覆盖 ≥3 态，实际 %', n; END IF;
    -- 审批留痕：document_approvals 覆盖多目标类型（采购/销售/退货/调整/盘点差异）
    SELECT count(DISTINCT target_type) INTO n FROM document_approvals WHERE operator_id BETWEEN 9000 AND 9999;
    IF n < 2 THEN RAISE EXCEPTION '[审批] document_approvals 目标类型应覆盖 ≥2 类，实际 %', n; END IF;
END
$$;

-- ---- 17) 补全轮二（§18）：效期三档 / 待复核队列 / 超储触发 / 流水类型与净零段 ----
DO $$
DECLARE n int; net numeric;
BEGIN
    -- 效期批次（batches 9621-9626，到期日相对 CURRENT_DATE）：过期 / 30 天窗内 / 窗外对照齐备
    SELECT count(*) INTO n FROM batches WHERE id BETWEEN 9621 AND 9626 AND expiry_date <= CURRENT_DATE;
    IF n < 1 THEN RAISE EXCEPTION '[效期] 已过期演示批次应 ≥1（expired 预警档），实际 %', n; END IF;
    SELECT count(*) INTO n FROM batches WHERE id BETWEEN 9621 AND 9626
      AND expiry_date > CURRENT_DATE AND expiry_date <= CURRENT_DATE + 30;
    IF n < 4 THEN RAISE EXCEPTION '[效期] 30 天临期窗内演示批次应 ≥4，实际 %', n; END IF;
    SELECT count(*) INTO n FROM batches WHERE id BETWEEN 9621 AND 9626 AND expiry_date > CURRENT_DATE + 30;
    IF n < 1 THEN RAISE EXCEPTION '[效期] 窗口外正常效期对照批次应 ≥1，实际 %', n; END IF;
    -- 待复核队列：PENDING 任务在（复核中心 ?status=PENDING / 工作台待复核组数据源）
    SELECT count(*) INTO n FROM check_tasks WHERE check_no = 'CH-20261007-000001' AND status = 'PENDING';
    IF n <> 1 THEN RAISE EXCEPTION '[待复核] CH-20261007-000001 应恰有 1 条 PENDING，实际 %', n; END IF;
    -- 超储触发：WH-D02 × SKU-D001-02 仓级合计 available > max_stock
    SELECT count(*) INTO n FROM (
        SELECT SUM(i.available_qty) AS avail, MAX(s.max_stock) AS max_stock
        FROM inventory i
        JOIN skus s ON s.id = i.sku_id
        JOIN warehouses w ON w.id = i.warehouse_id
        WHERE w.code = 'WH-D02' AND s.code = 'SKU-D001-02'
    ) t WHERE t.avail > t.max_stock;
    IF n <> 1 THEN RAISE EXCEPTION '[超储] WH-D02/SKU-D001-02 合计应 > max_stock（overstock 预警档）'; END IF;
    -- 流水类型：§18.4 覆盖 6 种此前缺失的 change_type
    SELECT count(DISTINCT change_type) INTO n FROM inventory_ledgers WHERE id BETWEEN 9921 AND 9928;
    IF n < 6 THEN RAISE EXCEPTION '[流水] §18.4 应覆盖 ≥6 种 change_type（TRANSFER_*/MOVE/ADJUST/LOCK/RELEASE），实际 %', n; END IF;
    -- 净零红线：§18.4 演示段 Σ qty_change = 0（不改现存量锚点）
    SELECT COALESCE(SUM(qty_change), 0) INTO net FROM inventory_ledgers WHERE id BETWEEN 9921 AND 9928;
    IF net <> 0 THEN RAISE EXCEPTION '[流水] §18.4 净零段 Σ qty_change 应为 0，实际 %', net; END IF;
END
$$;

-- ---- 自检通过：输出演示集摘要 ----
\echo '>>> 自检通过。演示集摘要：'
SELECT (SELECT count(*) FROM users WHERE username LIKE 'dev\_%')            AS "测试账号",
       (SELECT count(*) FROM warehouses WHERE id BETWEEN 9000 AND 9999)     AS "仓库",
       (SELECT count(*) FROM zones WHERE warehouse_id IN (9101, 9102))      AS "库区",
       (SELECT count(*) FROM shelves WHERE warehouse_id IN (9101, 9102))    AS "货架",
       (SELECT count(*) FROM bins WHERE warehouse_id IN (9101, 9102))       AS "库位",
       (SELECT count(*) FROM products WHERE id BETWEEN 9000 AND 9999)       AS "商品",
       (SELECT count(*) FROM skus WHERE id BETWEEN 9000 AND 9999)           AS "SKU",
       (SELECT count(*) FROM barcodes WHERE id BETWEEN 9000 AND 9999)       AS "条码",
       (SELECT count(*) FROM suppliers WHERE id BETWEEN 9000 AND 9999)      AS "供应商",
       (SELECT count(*) FROM customers WHERE id BETWEEN 9000 AND 9999)      AS "客户",
       (SELECT count(*) FROM batches WHERE id BETWEEN 9000 AND 9999)        AS "批次",
       (SELECT count(*) FROM inventory WHERE id BETWEEN 9000 AND 9999)      AS "期初库存行",
       (SELECT count(*) FROM inventory_ledgers WHERE id BETWEEN 9000 AND 9999
                AND business_type = '期初')                                  AS "期初流水行",
       (SELECT count(*) FROM serial_numbers WHERE id BETWEEN 9000 AND 9999) AS "序列号",
       (SELECT count(*) FROM inventory_locks WHERE id BETWEEN 9000 AND 9999)        AS "库存锁定",
       (SELECT count(*) FROM inventory_adjustments WHERE id BETWEEN 9000 AND 9999)  AS "库存调整",
       (SELECT count(*) FROM return_orders WHERE id BETWEEN 9000 AND 9999)          AS "退货单",
       (SELECT count(*) FROM exceptions WHERE id BETWEEN 9000 AND 9999)             AS "异常单",
       (SELECT count(*) FROM devices WHERE id BETWEEN 9000 AND 9999)                AS "设备",
       (SELECT count(*) FROM files WHERE id BETWEEN 9000 AND 9999)                  AS "文件",
       (SELECT count(*) FROM import_tasks WHERE id BETWEEN 9000 AND 9999)           AS "导入任务",
       (SELECT count(*) FROM export_tasks WHERE id BETWEEN 9000 AND 9999)           AS "导出任务",
       (SELECT count(*) FROM notifications WHERE id BETWEEN 9000 AND 9999)          AS "通知",
       (SELECT count(*) FROM backup_records WHERE id BETWEEN 9000 AND 9999)         AS "备份记录";
