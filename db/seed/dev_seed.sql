-- db/seed/dev_seed.sql —— 演示数据集（database.md §8.2 / requirements.md §9，仅开发环境）
--
-- 硬性要求：演示数据与生产初始化逻辑完全分离（database.md §8.2、requirements.md §9、
--   deployment.md §6、backend-m1-plan §7.5），生产环境不可能注入演示数据。
--
-- 三层门禁（缺一不可，生产误灌防线）：
--   1) 命令命名与入口：仅限手工执行 `make seed-demo`（Makefile 目标强制 SF_ENV=dev）；
--   2) 本文件 is_dev psql 变量门禁：不显式 `-v is_dev=1` 时 \if :is_dev 按假值处理
--      （psql 对无法判定为真/假的表达式告警并按假跳过，PG15 文档 \if 节），整个数据块不执行；
--   3) 前置守卫：目标库必须已完成生产安全初始化（internal/database/seed.go BootstrapIfEmpty
--      写入的 16 内置角色存在），全新空库（生产首次部署形态）直接报错退出。
--   启动路径（cmd/server、internal/database/seed.go）绝不加载本文件——由
--   internal/database/dev_seed_test.go 静态自查强制（扫描 cmd/、internal/ 生产代码零引用）。
--
-- 演示集内容（对应 requirements.md §9 演示数据要求的 M1 可交付部分；采购/入库/销售/出库/
--   调拨/盘点等单据演示随 M2 单据域表迁移追加，backend-m1-plan §9：M1 不建单据表）：
--   测试账号 5 个（管理员外 4 种角色：仓库经理/收货员/发货员/盘点员 + 财务查看，≥2 账号
--     绑定不同仓库供验证 SPECIFIED_WAREHOUSE 数据权限，permission.md §4）；
--   仓库 2 个 × 库区 2–3 × 每库区货架 2 × 每货架库位 2–6；部门 3；
--   商品 10 / SKU 20（覆盖 批次/效期/序列号/全关 四种组合及 批次+序列号、批次+效期+序列号）/条码 26；
--   供应商 3 / 客户 3 / 批次 7 / 序列号 23；
--   期初库存 24 行——只写 total/available 两列（其余四列默认 0，恒等式
--     total = available + locked + frozen + pending_inspect + defective 成立），
--     与期初流水 24 行同文件同事务（BEGIN/COMMIT）一一成对插入：流水 qty_before=0 →
--     qty_after=n，change_type='INBOUND'，business_type='期初'，remark='DEV SEED'。
--     生产环境库存唯一变更入口是 internal/inventory.Service（inventory-rules §1），
--     期初建账在演示库直接成对插入流水，与"库存变更必带流水"口径一致。
--
-- 幂等：全部业务行使用固定显式主键（9xxx 段，避开 bigserial 自增与生产初始化 1xx 段），
--   每条 INSERT 带自然键 ON CONFLICT DO NOTHING，可重复执行，不覆盖已有数据。
--   ID 段位分配：9001 部门 / 9101 仓库 / 9111 库区 / 9121 货架 / 9141 库位 / 9201 分类 /
--   9211 单位 / 9301 商品 / 9401 SKU / 9451 条码 / 9501 供应商 / 9521 客户 / 9601 批次 /
--   9701 序列号 / 9801 期初库存 / 9851 期初流水 / 9901 用户。
--
-- 追踪标记：期初流水 business_no 统一 'DEV-SEED-OPEN-*' 前缀、remark='DEV SEED'；测试账号
--   统一 dev_* 命名——误灌可被 db/seed/dev_seed_verify.sql（make seed-demo-verify）检出。
--
-- 已知披露：旧版 dev_seed.sql 注入过的演示数据（WH-DEV/SKU-1001-01 等，无配套流水）不在
--   本文件清理范围（seed 禁止 DELETE），旧开发库重跑会新旧并存，需要干净演示集请用新库。
--
-- 用法（服务器/本地开发库）：
--   SF_ENV=dev make seed-demo           # 注入（等价 psql "$PG_URL" -v is_dev=1 -f db/seed/dev_seed.sql）
--   make seed-demo-verify               # 灌数后只读自检（实体覆盖/恒等式/期初库存-流水成对）
-- 前置条件：目标库已执行 db/migrations 迁移且启动过一次服务（生产安全初始化已写入内置角色）。

\set ON_ERROR_STOP on

\if :is_dev
\echo '>>> dev_seed.sql：演示数据注入（仅限开发库；误灌请立即回滚事务并核对连接串）...'
\else
\echo '拒绝执行：dev_seed.sql 仅限开发环境。如确需执行请显式加 -v is_dev=1 并确认连接的是开发库。'
\endif
\if :is_dev

BEGIN;

-- ============ 0) 前置守卫：必须已完成生产安全初始化（内置角色就绪） ============
-- 生产初始化（internal/database/seed.go BootstrapIfEmpty）负责 16 内置角色/权限点/默认管理员，
-- 本文件只消费角色、绝不写入 roles/permissions/role_permissions（无生产初始化污染）。
DO $$
BEGIN
    IF (SELECT count(*) FROM roles
        WHERE code IN ('warehouse_manager', 'receiver', 'shipper', 'stocktaker', 'viewer')) < 5 THEN
        RAISE EXCEPTION '内置角色缺失：请先对目标库执行迁移并启动一次服务完成生产安全初始化（BootstrapIfEmpty），再注入演示数据';
    END IF;
END
$$;

-- ============ 1) 部门 ============
INSERT INTO departments (id, parent_id, code, name, status, created_by)
VALUES (9001, NULL, 'DEP-D01', '仓储部', 'ENABLED', 0),
       (9002, 9001, 'DEP-D01-SZ', '深圳仓运营', 'ENABLED', 0),
       (9003, 9001, 'DEP-D01-GZ', '广州仓运营', 'ENABLED', 0)
ON CONFLICT (code) DO NOTHING;

-- ============ 2) 测试账号 5 个 ============
-- 明文密码（仅 dev 测试，严禁用于任何真实环境；bcrypt cost 12 与 BcryptCost 一致）：
--   dev_manager   DevMg#2026（仓库经理，绑定演示一号仓）
--   dev_receiver  DevRc#2026（收货员，绑定演示一号仓）
--   dev_shipper   DevSp#2026（发货员，绑定演示二号仓）
--   dev_stocktaker DevSt#2026（盘点员，绑定两仓，演示多仓绑定集）
--   dev_viewer    DevVw#2026（财务/查看人员，data_scope=ALL 只读）
-- must_change_password=FALSE：演示账号免改密直登（与生产管理员强制改密策略不同，见上方披露）。
INSERT INTO users (id, username, password_hash, real_name, phone, email, department_id, data_scope,
                   status, must_change_password, created_by)
VALUES (9901, 'dev_manager', '$2a$12$PzhbErX.ceig7csLDjmFJuF3.S.FuqjLp.2BBdAt6hZlAg4Xp9mbu',
        '王经理', '13800001001', 'dev-manager@demo.dev', 9002, 'SPECIFIED_WAREHOUSE', 'ACTIVE', FALSE, 0),
       (9902, 'dev_receiver', '$2a$12$zEUkn8jkx/ouYozCENL1xuLBvToLn1jgQdNIlPhjONj1lu6234Jh2',
        '李收货', '13800001002', 'dev-receiver@demo.dev', 9002, 'SPECIFIED_WAREHOUSE', 'ACTIVE', FALSE, 0),
       (9903, 'dev_shipper', '$2a$12$x00cIIkkrkuyJ4XkG162j.EJUQODUsn8Da1QrYzwtmdcAB2SBFTh.',
        '陈发货', '13800001003', 'dev-shipper@demo.dev', 9003, 'SPECIFIED_WAREHOUSE', 'ACTIVE', FALSE, 0),
       (9904, 'dev_stocktaker', '$2a$12$P1C7lhN8mxktlMCM3NLq8O9/Qo5IWKTbzQNW/.0t4HM7o7YDzv7FW',
        '赵盘点', '13800001004', 'dev-stocktaker@demo.dev', 9001, 'SPECIFIED_WAREHOUSE', 'ACTIVE', FALSE, 0),
       (9905, 'dev_viewer', '$2a$12$atfycaBGkl8sPSBMoqVjoOTQJOyOtsSKgAdSBPQGBxCMMareCj2dq',
        '孙查看', '13800001005', 'dev-viewer@demo.dev', 9001, 'ALL', 'ACTIVE', FALSE, 0)
ON CONFLICT (username) WHERE deleted_at IS NULL DO NOTHING;

-- 角色绑定（role id 由生产初始化写入、编码唯一，按 code 解析；M1 四角色外的权限映射随 M2 补配，
-- plan §7.1——演示账号可登录与验证数据权限，菜单权限点暂少属预期）
INSERT INTO user_roles (user_id, role_id, created_by)
SELECT u.id, r.id, 0
FROM (VALUES ('dev_manager', 'warehouse_manager'),
             ('dev_receiver', 'receiver'),
             ('dev_shipper', 'shipper'),
             ('dev_stocktaker', 'stocktaker'),
             ('dev_viewer', 'viewer')) AS v(username, role_code)
JOIN users u ON u.username = v.username AND u.deleted_at IS NULL
JOIN roles r ON r.code = v.role_code
ON CONFLICT DO NOTHING;

-- 仓库绑定（permission.md §4：SPECIFIED_WAREHOUSE 数据权限的仓库集；
-- dev_receiver→一号仓 与 dev_shipper→二号仓 构成"不同仓库"对照）
INSERT INTO user_warehouses (user_id, warehouse_id, created_by)
VALUES (9901, 9101, 0),
       (9902, 9101, 0),
       (9903, 9102, 0),
       (9904, 9101, 0),
       (9904, 9102, 0)
ON CONFLICT DO NOTHING;

-- ============ 3) 仓库 / 库区 / 货架 / 库位（business-flow §1.6 四级结构） ============
INSERT INTO warehouses (id, code, name, address, contact, phone, type, status, created_by)
VALUES (9101, 'WH-D01', '演示一号仓', '深圳市南山区演示路 1 号', '王经理', '0755-10000001', 'NORMAL', 'ENABLED', 0),
       (9102, 'WH-D02', '演示二号仓', '广州市黄埔区演示大道 2 号', '陈发货', '020-20000002', 'NORMAL', 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

INSERT INTO zones (id, warehouse_id, code, name, zone_type, capacity, created_by)
VALUES (9111, 9101, 'ZD01-STORE', '存储区', 'STORAGE', 1000, 0),
       (9112, 9101, 'ZD01-PICK', '拣货区', 'PICKING', 500, 0),
       (9113, 9101, 'ZD01-RECV', '收货暂存区', 'RECEIVING', 200, 0),
       (9114, 9102, 'ZD02-STORE', '存储区', 'STORAGE', 800, 0),
       (9115, 9102, 'ZD02-RECV', '收货暂存区', 'RECEIVING', 200, 0)
ON CONFLICT (warehouse_id, code) DO NOTHING;

INSERT INTO shelves (id, warehouse_id, zone_id, code, layers, columns, capacity, created_by)
VALUES (9121, 9101, 9111, 'S-01', 3, 2, 400, 0),
       (9122, 9101, 9111, 'S-02', 2, 2, 400, 0),
       (9123, 9101, 9112, 'P-01', 1, 2, 200, 0),
       (9124, 9101, 9112, 'P-02', 1, 2, 200, 0),
       (9125, 9101, 9113, 'R-01', 1, 2, 100, 0),
       (9126, 9101, 9113, 'R-02', 1, 2, 100, 0),
       (9127, 9102, 9114, 'S-01', 2, 3, 400, 0),
       (9128, 9102, 9114, 'S-02', 2, 3, 400, 0),
       (9129, 9102, 9115, 'R-01', 1, 2, 100, 0),
       (9130, 9102, 9115, 'R-02', 1, 2, 100, 0)
ON CONFLICT (zone_id, code) DO NOTHING;

INSERT INTO bins (id, warehouse_id, zone_id, shelf_id, layer, column_no, code, bin_type, max_capacity, status, created_by)
VALUES (9141, 9101, 9111, 9121, 1, 1, 'S-01-11', 'STORAGE', 100, 'ENABLED', 0),
       (9142, 9101, 9111, 9121, 1, 2, 'S-01-12', 'STORAGE', 100, 'ENABLED', 0),
       (9143, 9101, 9111, 9121, 2, 1, 'S-01-21', 'STORAGE', 100, 'ENABLED', 0),
       (9144, 9101, 9111, 9121, 2, 2, 'S-01-22', 'STORAGE', 100, 'ENABLED', 0),
       (9145, 9101, 9111, 9122, 1, 1, 'S-02-11', 'STORAGE', 100, 'ENABLED', 0),
       (9146, 9101, 9111, 9122, 1, 2, 'S-02-12', 'STORAGE', 100, 'ENABLED', 0),
       (9147, 9101, 9111, 9122, 2, 1, 'S-02-21', 'STORAGE', 100, 'ENABLED', 0),
       (9148, 9101, 9111, 9122, 2, 2, 'S-02-22', 'STORAGE', 100, 'ENABLED', 0),
       (9149, 9101, 9112, 9123, 1, 1, 'P-01-11', 'PICK', 100, 'ENABLED', 0),
       (9150, 9101, 9112, 9123, 1, 2, 'P-01-12', 'PICK', 100, 'ENABLED', 0),
       (9151, 9101, 9112, 9124, 1, 1, 'P-02-11', 'PICK', 100, 'ENABLED', 0),
       (9152, 9101, 9112, 9124, 1, 2, 'P-02-12', 'PICK', 100, 'ENABLED', 0),
       (9153, 9101, 9113, 9125, 1, 1, 'R-01-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9154, 9101, 9113, 9125, 1, 2, 'R-01-12', 'RECEIVE', 100, 'ENABLED', 0),
       (9155, 9101, 9113, 9126, 1, 1, 'R-02-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9156, 9101, 9113, 9126, 1, 2, 'R-02-12', 'RECEIVE', 100, 'ENABLED', 0),
       (9157, 9102, 9114, 9127, 1, 1, 'S-01-11', 'STORAGE', 100, 'ENABLED', 0),
       (9158, 9102, 9114, 9127, 1, 2, 'S-01-12', 'STORAGE', 100, 'ENABLED', 0),
       (9159, 9102, 9114, 9127, 1, 3, 'S-01-13', 'STORAGE', 100, 'ENABLED', 0),
       (9160, 9102, 9114, 9127, 2, 1, 'S-01-21', 'STORAGE', 100, 'ENABLED', 0),
       (9161, 9102, 9114, 9127, 2, 2, 'S-01-22', 'STORAGE', 100, 'ENABLED', 0),
       (9162, 9102, 9114, 9127, 2, 3, 'S-01-23', 'STORAGE', 100, 'ENABLED', 0),
       (9163, 9102, 9114, 9128, 1, 1, 'S-02-11', 'STORAGE', 100, 'ENABLED', 0),
       (9164, 9102, 9114, 9128, 1, 2, 'S-02-12', 'STORAGE', 100, 'ENABLED', 0),
       (9165, 9102, 9114, 9128, 1, 3, 'S-02-13', 'STORAGE', 100, 'ENABLED', 0),
       (9166, 9102, 9114, 9128, 2, 1, 'S-02-21', 'STORAGE', 100, 'ENABLED', 0),
       (9167, 9102, 9114, 9128, 2, 2, 'S-02-22', 'STORAGE', 100, 'ENABLED', 0),
       (9168, 9102, 9114, 9128, 2, 3, 'S-02-23', 'STORAGE', 100, 'ENABLED', 0),
       (9169, 9102, 9115, 9129, 1, 1, 'R-01-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9170, 9102, 9115, 9129, 1, 2, 'R-01-12', 'RECEIVE', 100, 'ENABLED', 0),
       (9171, 9102, 9115, 9130, 1, 1, 'R-02-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9172, 9102, 9115, 9130, 1, 2, 'R-02-12', 'RECEIVE', 100, 'ENABLED', 0)
ON CONFLICT (warehouse_id, code) DO NOTHING;

-- ============ 4) 商品分类 / 单位 ============
INSERT INTO product_categories (id, parent_id, code, name, sort, created_by)
VALUES (9201, NULL, 'C-ELEC', '电子设备', 1, 0),
       (9202, NULL, 'C-ACC', '配件耗材', 2, 0),
       (9203, NULL, 'C-LOG', '物流器具', 3, 0),
       (9204, 9201, 'C-ELEC-SCANNER', '扫描采集设备', 1, 0),
       (9205, 9201, 'C-ELEC-PRINT', '打印设备', 2, 0)
ON CONFLICT (code) DO NOTHING;

INSERT INTO units (id, code, name, created_by)
VALUES (9211, 'U-PCS', '个', 0),
       (9212, 'U-BOX', '箱', 0),
       (9213, 'U-ROLL', '卷', 0),
       (9214, 'U-SET', '套', 0)
ON CONFLICT (code) DO NOTHING;

-- ============ 5) 商品 10 / SKU 20（批次/效期/序列号四种组合覆盖） ============
INSERT INTO products (id, code, name, short_name, category_id, brand, model, spec, unit_id, weight, status, created_by)
VALUES (9301, 'P-D001', '无线条码扫描枪', '扫描枪', 9204, 'ScanFlow', 'SF-200', '2.4G 无线, USB 接收器', 9211, 0.2500, 'ENABLED', 0),
       (9302, 'P-D002', '工业级PDA数据采集器', 'PDA', 9204, 'StockScan', 'P-6800', 'Android 13, 2D 扫描', 9211, 0.4500, 'ENABLED', 0),
       (9303, 'P-D003', 'Type-C 数据线', '数据线', 9202, 'LinkFast', 'LC-100', '1m 编织', 9211, 0.0500, 'ENABLED', 0),
       (9304, 'P-D004', '热敏标签纸', '标签纸', 9202, 'LabelPro', 'LP-4030', '40x30mm 500 张/卷', 9213, 0.8000, 'ENABLED', 0),
       (9305, 'P-D005', '热敏碳带', '碳带', 9202, 'RibbonPro', 'RB-60', '60m 蜡基', 9213, 0.6000, 'ENABLED', 0),
       (9306, 'P-D006', '蓝牙热敏打印机', '蓝牙打印机', 9205, 'PrintGo', 'B-320', '蓝牙 5.0, 203dpi', 9211, 0.7200, 'ENABLED', 0),
       (9307, 'P-D007', '川字塑料托盘', '托盘', 9203, 'PalletPro', 'T-1210', '1200x1000x150mm', 9211, 8.5000, 'ENABLED', 0),
       (9308, 'P-D008', '塑料周转箱', '周转箱', 9203, 'BoxPro', 'B-433', '400x330x280mm', 9211, 1.6000, 'ENABLED', 0),
       (9309, 'P-D009', '打包胶带', '胶带', 9202, 'TapePro', 'TP-600', '60mmx150m, 6 卷/组', 9212, 0.2500, 'ENABLED', 0),
       (9310, 'P-D010', '防静电手套', '手套', 9202, 'GloveSafe', 'ESD-01', '均码, 12 双/包', 9212, 0.3000, 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- 三开关组合分布：全关×9 / 批次×5 / 批次+效期×2 / 序列号×2 / 批次+序列号×1 / 批次+效期+序列号×1
INSERT INTO skus (id, code, product_id, spec_attrs, cost_price, sale_price, safety_stock, max_stock,
                  min_replenish_qty, is_batch_managed, is_expiry_managed, is_serial_managed, is_enabled, created_by)
VALUES (9401, 'SKU-D001-01', 9301, '{"颜色": "黑色"}'::jsonb, 180.0000, 299.0000, 10.0000, 500.0000, 20.0000, FALSE, FALSE, TRUE, TRUE, 0),
       (9402, 'SKU-D001-02', 9301, '{"颜色": "白色"}'::jsonb, 175.0000, 289.0000, 10.0000, 500.0000, 20.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9403, 'SKU-D002-01', 9302, '{"配置": "标准版"}'::jsonb, 1200.0000, 1980.0000, 5.0000, 200.0000, 10.0000, TRUE, FALSE, TRUE, TRUE, 0),
       (9404, 'SKU-D002-02', 9302, '{"配置": "豪华版"}'::jsonb, 1350.0000, 2280.0000, 5.0000, 200.0000, 10.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9405, 'SKU-D003-01', 9303, '{"接口": "Type-C", "长度": "1m"}'::jsonb, 3.5000, 12.9000, 200.0000, 10000.0000, 500.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9406, 'SKU-D003-02', 9303, '{"接口": "Type-C", "长度": "2m"}'::jsonb, 5.0000, 18.9000, 200.0000, 10000.0000, 500.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9407, 'SKU-D004-01', 9304, '{"规格": "40x30mm"}'::jsonb, 8.0000, 25.0000, 50.0000, 2000.0000, 100.0000, TRUE, TRUE, FALSE, TRUE, 0),
       (9408, 'SKU-D004-02', 9304, '{"规格": "60x40mm"}'::jsonb, 12.0000, 35.0000, 50.0000, 2000.0000, 100.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9409, 'SKU-D005-01', 9305, '{"宽度": "60mm"}'::jsonb, 15.0000, 42.0000, 30.0000, 1000.0000, 60.0000, TRUE, TRUE, TRUE, TRUE, 0),
       (9410, 'SKU-D005-02', 9305, '{"宽度": "80mm"}'::jsonb, 18.0000, 50.0000, 30.0000, 1000.0000, 60.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9411, 'SKU-D006-01', 9306, '{"颜色": "黑色"}'::jsonb, 260.0000, 429.0000, 5.0000, 200.0000, 10.0000, FALSE, FALSE, TRUE, TRUE, 0),
       (9412, 'SKU-D006-02', 9306, '{"颜色": "白色"}'::jsonb, 255.0000, 419.0000, 5.0000, 200.0000, 10.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9413, 'SKU-D007-01', 9307, '{"材质": "全新料"}'::jsonb, 65.0000, 120.0000, 20.0000, 500.0000, 50.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9414, 'SKU-D007-02', 9307, '{"材质": "回料"}'::jsonb, 45.0000, 90.0000, 20.0000, 500.0000, 50.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9415, 'SKU-D008-01', 9308, '{"颜色": "蓝色"}'::jsonb, 22.0000, 48.0000, 50.0000, 1000.0000, 100.0000, TRUE, TRUE, FALSE, TRUE, 0),
       (9416, 'SKU-D008-02', 9308, '{"颜色": "灰色"}'::jsonb, 20.0000, 45.0000, 50.0000, 1000.0000, 100.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9417, 'SKU-D009-01', 9309, '{"宽度": "60mm"}'::jsonb, 2.2000, 8.5000, 100.0000, 5000.0000, 200.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9418, 'SKU-D009-02', 9309, '{"宽度": "45mm"}'::jsonb, 1.8000, 7.5000, 100.0000, 5000.0000, 200.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9419, 'SKU-D010-01', 9310, '{"尺码": "均码"}'::jsonb, 6.0000, 19.0000, 100.0000, 5000.0000, 200.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9420, 'SKU-D010-02', 9310, '{"尺码": "L"}'::jsonb, 6.5000, 20.0000, 100.0000, 5000.0000, 200.0000, TRUE, FALSE, FALSE, TRUE, 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- 条码：20 主条码（EAN13，校验位有效）+ 6 辅条码（CODE128）
INSERT INTO barcodes (id, sku_id, barcode, code_type, is_primary, created_by)
VALUES (9451, 9401, '6901234000016', 'EAN13', TRUE, 0),
       (9452, 9402, '6901234000023', 'EAN13', TRUE, 0),
       (9453, 9403, '6901234000030', 'EAN13', TRUE, 0),
       (9454, 9404, '6901234000047', 'EAN13', TRUE, 0),
       (9455, 9405, '6901234000054', 'EAN13', TRUE, 0),
       (9456, 9406, '6901234000061', 'EAN13', TRUE, 0),
       (9457, 9407, '6901234000078', 'EAN13', TRUE, 0),
       (9458, 9408, '6901234000085', 'EAN13', TRUE, 0),
       (9459, 9409, '6901234000092', 'EAN13', TRUE, 0),
       (9460, 9410, '6901234000108', 'EAN13', TRUE, 0),
       (9461, 9411, '6901234000115', 'EAN13', TRUE, 0),
       (9462, 9412, '6901234000122', 'EAN13', TRUE, 0),
       (9463, 9413, '6901234000139', 'EAN13', TRUE, 0),
       (9464, 9414, '6901234000146', 'EAN13', TRUE, 0),
       (9465, 9415, '6901234000153', 'EAN13', TRUE, 0),
       (9466, 9416, '6901234000160', 'EAN13', TRUE, 0),
       (9467, 9417, '6901234000177', 'EAN13', TRUE, 0),
       (9468, 9418, '6901234000184', 'EAN13', TRUE, 0),
       (9469, 9419, '6901234000191', 'EAN13', TRUE, 0),
       (9470, 9420, '6901234000207', 'EAN13', TRUE, 0),
       (9471, 9401, 'SF-D001-01-A', 'CODE128', FALSE, 0),
       (9472, 9403, 'SF-D002-01-A', 'CODE128', FALSE, 0),
       (9473, 9405, 'SF-D003-01-A', 'CODE128', FALSE, 0),
       (9474, 9407, 'SF-D004-01-A', 'CODE128', FALSE, 0),
       (9475, 9409, 'SF-D005-01-A', 'CODE128', FALSE, 0),
       (9476, 9413, 'SF-D007-01-A', 'CODE128', FALSE, 0)
ON CONFLICT (barcode) DO NOTHING;

-- ============ 6) 供应商 3 / 客户 3 ============
INSERT INTO suppliers (id, code, name, contact, phone, email, address, status, remark, created_by)
VALUES (9501, 'SUP-D001', '华东演示电子有限公司', '王华东', '13800002001', 'sup-d001@demo.dev',
        '上海市浦东新区演示路 10 号', 'ENABLED', 'DEV SEED', 0),
       (9502, 'SUP-D002', '华南演示配件有限公司', '李广达', '13800002002', 'sup-d002@demo.dev',
        '东莞市演示工业区 8 号', 'ENABLED', 'DEV SEED', 0),
       (9503, 'SUP-D003', '演示耗材贸易有限公司', '张耗材', '13800002003', 'sup-d003@demo.dev',
        '佛山市演示工业园 6 号', 'ENABLED', 'DEV SEED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

INSERT INTO customers (id, code, name, contact, phone, email, address, shipping_address, status, created_by)
VALUES (9521, 'CUS-D001', '星辰商贸有限公司', '张星辰', '13900002001', 'cus-d001@demo.dev',
        '广州市天河区演示大道 1 号', '广州市天河区演示大道 1 号 A 栋 2 层', 'ENABLED', 0),
       (9522, 'CUS-D002', '迅捷零售连锁有限公司', '赵迅捷', '13900002002', 'cus-d002@demo.dev',
        '佛山市禅城区演示街 5 号', '佛山市禅城区演示街 5 号仓库西门', 'ENABLED', 0),
       (9523, 'CUS-D003', '云仓科技有限公司', '钱云仓', '13900002003', 'cus-d003@demo.dev',
        '深圳市宝安区演示科技园 3 栋', '深圳市宝安区演示科技园 3 栋收货月台', 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- ============ 7) 批次（启用批次管理的 SKU；效期 SKU 带到期日，供 FEFO/效期预警演示） ============
INSERT INTO batches (id, sku_id, batch_no, supplier_id, production_date, inbound_date, expiry_date, cost_price, remark, created_by)
VALUES (9601, 9403, 'B2609-D002', 9501, '2026-08-20', '2026-09-01', NULL, 1200.0000, 'DEV SEED', 0),
       (9602, 9405, 'B2609-D003', 9502, '2026-09-01', '2026-09-10', NULL, 3.5000, 'DEV SEED', 0),
       (9603, 9407, 'B2609-D004', 9503, '2026-08-15', '2026-09-01', '2027-09-01', 8.0000, 'DEV SEED', 0),
       (9604, 9407, 'B2610-D004', 9503, '2026-09-15', '2026-09-28', '2027-10-01', 8.2000, 'DEV SEED', 0),
       (9605, 9409, 'B2609-D005', 9502, '2026-08-01', '2026-09-05', '2027-06-30', 15.0000, 'DEV SEED', 0),
       (9606, 9413, 'B2609-D007', 9503, '2026-07-20', '2026-08-30', NULL, 65.0000, 'DEV SEED', 0),
       (9607, 9415, 'B2609-D008', 9502, '2026-08-10', '2026-09-12', '2027-12-31', 22.0000, 'DEV SEED', 0)
ON CONFLICT (sku_id, batch_no) DO NOTHING;

-- ============ 8) 期初库存（只写 total/available 两列）与期初流水 1:1 成对 ============
-- 24 行库存：五维定位 warehouse+bin+sku(+batch)；六列恒等式由 CHECK 强制（inventory-rules §2），
-- 本段只写 total/available（locked/frozen/pending_inspect/defective 默认 0）。
-- 流水同事务成对（下方同 VALUES）：change_type='INBOUND'、business_type='期初'、
-- business_no='DEV-SEED-OPEN-*'、remark='DEV SEED'、qty_before=0 → qty_after=n。
-- (id, 仓库, 库位, SKU, 批次号(非批次为 NULL), 期初数量)
INSERT INTO inventory (id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
                       total_qty, available_qty, created_by)
SELECT v.id, w.id, z.id, s.id, b.id, sku.id, COALESCE(bat.id, 0),
       v.qty, v.qty, 0
FROM (VALUES (9801, 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL, 12.0000::numeric(18, 4)),
             (9802, 'WH-D01', 'S-01-11', 'SKU-D001-02', NULL, 30.0000::numeric(18, 4)),
             (9803, 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002', 6.0000::numeric(18, 4)),
             (9804, 'WH-D01', 'S-01-12', 'SKU-D002-02', NULL, 15.0000::numeric(18, 4)),
             (9805, 'WH-D01', 'S-01-21', 'SKU-D003-01', 'B2609-D003', 200.0000::numeric(18, 4)),
             (9806, 'WH-D01', 'S-01-21', 'SKU-D003-02', NULL, 150.0000::numeric(18, 4)),
             (9807, 'WH-D01', 'S-01-22', 'SKU-D004-01', 'B2609-D004', 100.0000::numeric(18, 4)),
             (9808, 'WH-D01', 'S-02-11', 'SKU-D004-01', 'B2610-D004', 120.0000::numeric(18, 4)),
             (9809, 'WH-D01', 'S-02-12', 'SKU-D005-01', 'B2609-D005', 40.0000::numeric(18, 4)),
             (9810, 'WH-D01', 'P-01-11', 'SKU-D005-02', NULL, 60.0000::numeric(18, 4)),
             (9811, 'WH-D01', 'P-01-12', 'SKU-D007-01', 'B2609-D007', 25.0000::numeric(18, 4)),
             (9812, 'WH-D01', 'P-02-11', 'SKU-D007-02', NULL, 18.0000::numeric(18, 4)),
             (9813, 'WH-D01', 'P-02-12', 'SKU-D008-01', 'B2609-D008', 80.0000::numeric(18, 4)),
             (9814, 'WH-D01', 'R-01-11', 'SKU-D008-02', NULL, 45.0000::numeric(18, 4)),
             (9815, 'WH-D01', 'R-01-12', 'SKU-D009-01', NULL, 300.0000::numeric(18, 4)),
             (9816, 'WH-D01', 'R-02-11', 'SKU-D009-02', NULL, 200.0000::numeric(18, 4)),
             (9817, 'WH-D01', 'R-02-12', 'SKU-D010-01', NULL, 100.0000::numeric(18, 4)),
             (9818, 'WH-D02', 'S-01-11', 'SKU-D001-02', NULL, 10.0000::numeric(18, 4)),
             (9819, 'WH-D02', 'S-01-12', 'SKU-D003-02', NULL, 80.0000::numeric(18, 4)),
             (9820, 'WH-D02', 'S-01-21', 'SKU-D004-01', 'B2609-D004', 60.0000::numeric(18, 4)),
             (9821, 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL, 5.0000::numeric(18, 4)),
             (9822, 'WH-D02', 'S-02-11', 'SKU-D007-02', NULL, 22.0000::numeric(18, 4)),
             (9823, 'WH-D02', 'S-02-12', 'SKU-D008-02', NULL, 33.0000::numeric(18, 4)),
             (9824, 'WH-D02', 'R-01-11', 'SKU-D010-01', NULL, 66.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (warehouse_id, bin_id, sku_id, batch_id) DO NOTHING;

-- 期初流水（与上方库存行同 VALUES 成对；inventory_ledgers 为 append-only 审计数据，
-- database.md §7：业务运行账号无 UPDATE/DELETE 权限，本段仅 INSERT）。
-- 2026-10-05 联调轮（docs/database.md §8.2 演示数据趋势形态）：期初建账时间由集中灌数
-- 日（now()）改为按 id 段分散近 7 天（SELECT 内 CASE 定值，VALUES 仍 6 列、期初-流水
-- 仍 1:1 成对；verify §6 与 seed 守卫均不校验日期）——日末库存锚点回推曲线呈自然爬升。
INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, COALESCE(bat.id, 0),
       'INBOUND', '期初', 'DEV-SEED-OPEN-' || v.id::text, 'available', 'available',
       0, v.qty, v.qty,
       0, 'dev-seed', 'dev-seed', 'DEV SEED',
       CASE WHEN v.id <= 9854 THEN '2026-09-29 09:30:00+08'::timestamptz
            WHEN v.id <= 9858 THEN '2026-09-30 10:10:00+08'::timestamptz
            WHEN v.id <= 9862 THEN '2026-10-01 11:20:00+08'::timestamptz
            WHEN v.id <= 9866 THEN '2026-10-02 14:00:00+08'::timestamptz
            WHEN v.id <= 9870 THEN '2026-10-03 15:30:00+08'::timestamptz
            ELSE '2026-10-04 08:40:00+08'::timestamptz END
FROM (VALUES              (9851, 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL, 12.0000::numeric(18, 4)),
             (9852, 'WH-D01', 'S-01-11', 'SKU-D001-02', NULL, 30.0000::numeric(18, 4)),
             (9853, 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002', 6.0000::numeric(18, 4)),
             (9854, 'WH-D01', 'S-01-12', 'SKU-D002-02', NULL, 15.0000::numeric(18, 4)),
             (9855, 'WH-D01', 'S-01-21', 'SKU-D003-01', 'B2609-D003', 200.0000::numeric(18, 4)),
             (9856, 'WH-D01', 'S-01-21', 'SKU-D003-02', NULL, 150.0000::numeric(18, 4)),
             (9857, 'WH-D01', 'S-01-22', 'SKU-D004-01', 'B2609-D004', 100.0000::numeric(18, 4)),
             (9858, 'WH-D01', 'S-02-11', 'SKU-D004-01', 'B2610-D004', 120.0000::numeric(18, 4)),
             (9859, 'WH-D01', 'S-02-12', 'SKU-D005-01', 'B2609-D005', 40.0000::numeric(18, 4)),
             (9860, 'WH-D01', 'P-01-11', 'SKU-D005-02', NULL, 60.0000::numeric(18, 4)),
             (9861, 'WH-D01', 'P-01-12', 'SKU-D007-01', 'B2609-D007', 25.0000::numeric(18, 4)),
             (9862, 'WH-D01', 'P-02-11', 'SKU-D007-02', NULL, 18.0000::numeric(18, 4)),
             (9863, 'WH-D01', 'P-02-12', 'SKU-D008-01', 'B2609-D008', 80.0000::numeric(18, 4)),
             (9864, 'WH-D01', 'R-01-11', 'SKU-D008-02', NULL, 45.0000::numeric(18, 4)),
             (9865, 'WH-D01', 'R-01-12', 'SKU-D009-01', NULL, 300.0000::numeric(18, 4)),
             (9866, 'WH-D01', 'R-02-11', 'SKU-D009-02', NULL, 200.0000::numeric(18, 4)),
             (9867, 'WH-D01', 'R-02-12', 'SKU-D010-01', NULL, 100.0000::numeric(18, 4)),
             (9868, 'WH-D02', 'S-01-11', 'SKU-D001-02', NULL, 10.0000::numeric(18, 4)),
             (9869, 'WH-D02', 'S-01-12', 'SKU-D003-02', NULL, 80.0000::numeric(18, 4)),
             (9870, 'WH-D02', 'S-01-21', 'SKU-D004-01', 'B2609-D004', 60.0000::numeric(18, 4)),
             (9871, 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL, 5.0000::numeric(18, 4)),
             (9872, 'WH-D02', 'S-02-11', 'SKU-D007-02', NULL, 22.0000::numeric(18, 4)),
             (9873, 'WH-D02', 'S-02-12', 'SKU-D008-02', NULL, 33.0000::numeric(18, 4)),
             (9874, 'WH-D02', 'R-01-11', 'SKU-D010-01', NULL, 66.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (ledger_no) DO NOTHING;

-- 2026-10-05 联调轮补影流水（id 9875-9878，business_type='演示'、business_no=
-- 'DEV-SEED-FLOW-*'）：两对先入后出净零对（+15/-15、+30/-30，同键相消 Σ 净变化=0），
-- 出库趋势线与周转率获得非零真实值；现存量锚点、恒等式与期初成对断言均不受影响。
INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, 0,
       v.ctype, '演示', 'DEV-SEED-FLOW-' || v.id::text, 'available', 'available',
       v.q_before, v.q_change, v.q_after,
       0, 'dev-seed', 'dev-seed', 'DEV SEED', v.at
FROM (VALUES (9875, 'WH-D01', 'S-01-21', 'SKU-D003-02', 'INBOUND',  150.0000::numeric(18,4),  15.0000::numeric(18,4), 165.0000::numeric(18,4), '2026-10-04 15:30:00+08'::timestamptz),
             (9876, 'WH-D01', 'S-01-21', 'SKU-D003-02', 'OUTBOUND', 165.0000::numeric(18,4), -15.0000::numeric(18,4), 150.0000::numeric(18,4), '2026-10-05 00:30:00+08'::timestamptz),
             (9877, 'WH-D01', 'R-01-12', 'SKU-D009-01', 'INBOUND',  300.0000::numeric(18,4),  30.0000::numeric(18,4), 330.0000::numeric(18,4), '2026-10-05 00:50:00+08'::timestamptz),
             (9878, 'WH-D01', 'R-01-12', 'SKU-D009-01', 'OUTBOUND', 330.0000::numeric(18,4), -30.0000::numeric(18,4), 300.0000::numeric(18,4), '2026-10-05 01:10:00+08'::timestamptz)) AS v(id, wh_code, bin_code, sku_code, ctype, q_before, q_change, q_after, at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT (ledger_no) DO NOTHING;



-- ============ 9) 序列号（启用序列号管理的 SKU，一物一行，数量与期初库存吻合） ============
-- SKU-D001-01@一号仓 12 件 / SKU-D002-01@一号仓 6 件（批次 B2609-D002）/ SKU-D006-01@二号仓 5 件
INSERT INTO serial_numbers (id, serial_no, sku_id, batch_id, warehouse_id, bin_id, status,
                            last_source_type, last_source_no, last_event_at, created_by)
SELECT v.id, v.serial_no, sku.id, COALESCE(bat.id, 0), w.id, b.id, 'IN_STOCK',
       'INBOUND', 'DEV-SEED-OPEN', now(), 0
FROM (VALUES (9701, 'SN-D001-0001', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9702, 'SN-D001-0002', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9703, 'SN-D001-0003', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9704, 'SN-D001-0004', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9705, 'SN-D001-0005', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9706, 'SN-D001-0006', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9707, 'SN-D001-0007', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9708, 'SN-D001-0008', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9709, 'SN-D001-0009', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9710, 'SN-D001-0010', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9711, 'SN-D001-0011', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9712, 'SN-D001-0012', 'WH-D01', 'S-01-11', 'SKU-D001-01', NULL),
             (9713, 'SN-D002-0001', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9714, 'SN-D002-0002', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9715, 'SN-D002-0003', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9716, 'SN-D002-0004', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9717, 'SN-D002-0005', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9718, 'SN-D002-0006', 'WH-D01', 'S-01-12', 'SKU-D002-01', 'B2609-D002'),
             (9719, 'SN-D006-0001', 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL),
             (9720, 'SN-D006-0002', 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL),
             (9721, 'SN-D006-0003', 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL),
             (9722, 'SN-D006-0004', 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL),
             (9723, 'SN-D006-0005', 'WH-D02', 'S-01-21', 'SKU-D006-01', NULL)) AS v(id, serial_no, wh_code, bin_code, sku_code, batch_no)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (serial_no) DO NOTHING;

COMMIT;

\echo '>>> dev_seed.sql：演示数据注入完成（幂等，可重复执行；请执行 make seed-demo-verify 自检）。'
\endif
