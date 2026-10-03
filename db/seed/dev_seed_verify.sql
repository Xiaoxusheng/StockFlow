-- db/seed/dev_seed_verify.sql —— 演示数据灌数后只读自检（make seed-demo-verify）
--
-- 用途：在执行 db/seed/dev_seed.sql（make seed-demo）之后运行，断言演示集完整且口径正确：
--   1) 实体覆盖满足 requirements.md §9 / dev_seed.sql 头注的最低规模；
--   2) 库存恒等式成立（inventory-rules §2，数据库 CHECK 之外的复核）；
--   3) 期初库存与期初流水 1:1 成对（qty_before=0 → qty_after=n，business_type='期初'，
--      remark='DEV SEED'，business_no='DEV-SEED-OPEN-*'）；
--   4) 序列号台账数与序列号 SKU 的期初库存吻合（一物一行，inventory-rules §8）。
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
       (SELECT count(*) FROM serial_numbers WHERE id BETWEEN 9000 AND 9999) AS "序列号";
