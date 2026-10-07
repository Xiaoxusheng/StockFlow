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
       (9904, 9102, 0),
       (9901, 9103, 0),
       (9901, 9104, 0),
       (9901, 9105, 0),
       (9902, 9103, 0),
       (9903, 9105, 0)
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

-- ============ 10) 电子厂场景扩展（2026-10-05：覆盖采购→收货→质检→上架、销售→出库全链、
--      调拨、盘点、打印任务与单号计数器推进；新增三仓/物料/供应商/客户，既有演示集不动） ============
-- ID 段新增分配：仓库 9103-9105 / 库区 9930-9941 / 货架 9950-9961 / 库位 9970-9999 /
--   分类 9206-9210 / 单位 9215-9218 / 商品 9311-9326 / SKU 9421-9440 / 条码 9477-9500 /
--   供应商 9504-9507 / 客户 9524-9526 / 批次 9608-9617 / 序列号 9724-9801 /
--   期初库存 9825-9842 / 期初流水 9879-9896 / 影流水 9897-9900 / 打印模板 9991-9992 /
--   打印任务行 9995-9999（打印任务/单据表为 bigserial，幂等靠业务单号唯一索引 + ON CONFLICT）。
-- 场景：深圳泰克威电子——SMT 贴片 + 整机组装。原料仓 WH-E01（元器件/辅料）、
--   半成品仓 WH-E02（PCBA）、成品仓 WH-E03（整机）。单据链状态机铺开：
--   采购（完成/部分收货/待审/草稿）→ 收货（批号/效期采集）→ 质检（抽检合格）→ 上架（完成/待办）；
--   销售（完成/部分发货/待审）→ 出库（分配/拣/复核/打包/发货全链 + 部分发货）；
--   调拨（完成/待收）、盘点（待差异审核/盘点中）、打印（已执行/排队）。
-- 自洽口径：期初库存 = 当前存量快照（全部 available，1:1 配对期初流水，沿用 §8 口径）；
--   序列号 SKU 的库位数 = IN_STOCK 序列号行数（已发部分序列号 status=OUTBOUND 留痕）；
--   已发货物料不在期初出现（历史量不回填，期初=终态）。

-- ---- 10.1 仓库 ×3（电子厂三仓）----
INSERT INTO warehouses (id, code, name, address, contact, phone, type, status, created_by)
VALUES (9103, 'WH-E01', '电子原料仓', '深圳市宝安区泰克威工业园 A 栋 1 层', '吴原料', '0755-30001001', 'NORMAL', 'ENABLED', 0),
       (9104, 'WH-E02', '半成品仓', '深圳市宝安区泰克威工业园 A 栋 2 层', '郑半品', '0755-30001002', 'NORMAL', 'ENABLED', 0),
       (9105, 'WH-E03', '成品仓', '深圳市宝安区泰克威工业园 B 栋 1 层', '冯成品', '0755-30001003', 'NORMAL', 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- 库区（含不良品隔离区 NG——结构就绪，不良库存由质检流程运行时产生）
INSERT INTO zones (id, warehouse_id, code, name, zone_type, capacity, created_by)
VALUES (9930, 9103, 'E01-STORE', '元器件存储区', 'STORAGE', 2000, 0),
       (9931, 9103, 'E01-PICK', '拣料区', 'PICKING', 800, 0),
       (9932, 9103, 'E01-RECV', '收货暂存区', 'RECEIVING', 300, 0),
       (9933, 9103, 'E01-NG', '不良品隔离区', 'QUARANTINE', 100, 0),
       (9934, 9104, 'E02-STORE', '半成品存储区', 'STORAGE', 1200, 0),
       (9935, 9104, 'E02-RECV', '收货暂存区', 'RECEIVING', 200, 0),
       (9936, 9105, 'E03-STORE', '成品存储区', 'STORAGE', 1500, 0),
       (9937, 9105, 'E03-PICK', '拣货区', 'PICKING', 600, 0),
       (9938, 9105, 'E03-RECV', '收货暂存区', 'RECEIVING', 200, 0)
ON CONFLICT (warehouse_id, code) DO NOTHING;

INSERT INTO shelves (id, warehouse_id, zone_id, code, layers, columns, capacity, created_by)
VALUES (9950, 9103, 9930, 'REEL-01', 3, 2, 600, 0),
       (9951, 9103, 9930, 'REEL-02', 3, 2, 600, 0),
       (9952, 9103, 9930, 'IC-01', 3, 2, 400, 0),
       (9953, 9103, 9931, 'PK-01', 2, 2, 200, 0),
       (9954, 9103, 9932, 'RV-01', 1, 2, 100, 0),
       (9955, 9103, 9933, 'NG-01', 1, 2, 100, 0),
       (9956, 9104, 9934, 'SA-01', 3, 2, 500, 0),
       (9957, 9104, 9934, 'SB-01', 2, 2, 300, 0),
       (9958, 9104, 9935, 'SR-01', 1, 2, 100, 0),
       (9959, 9105, 9936, 'FG-01', 3, 2, 400, 0),
       (9960, 9105, 9936, 'FG-02', 2, 2, 400, 0),
       (9961, 9105, 9937, 'FP-01', 2, 2, 200, 0)
ON CONFLICT (zone_id, code) DO NOTHING;

INSERT INTO bins (id, warehouse_id, zone_id, shelf_id, layer, column_no, code, bin_type, max_capacity, status, created_by)
VALUES (9970, 9103, 9930, 9950, 1, 1, 'REEL-01-11', 'STORAGE', 200, 'ENABLED', 0),
       (9971, 9103, 9930, 9950, 1, 2, 'REEL-01-12', 'STORAGE', 200, 'ENABLED', 0),
       (9972, 9103, 9930, 9950, 2, 1, 'REEL-01-21', 'STORAGE', 200, 'ENABLED', 0),
       (9973, 9103, 9930, 9950, 2, 2, 'REEL-01-22', 'STORAGE', 200, 'ENABLED', 0),
       (9974, 9103, 9930, 9951, 1, 1, 'REEL-02-11', 'STORAGE', 200, 'ENABLED', 0),
       (9975, 9103, 9930, 9951, 1, 2, 'REEL-02-12', 'STORAGE', 200, 'ENABLED', 0),
       (9976, 9103, 9930, 9951, 2, 1, 'REEL-02-21', 'STORAGE', 200, 'ENABLED', 0),
       (9977, 9103, 9930, 9951, 2, 2, 'REEL-02-22', 'STORAGE', 200, 'ENABLED', 0),
       (9978, 9103, 9930, 9952, 1, 1, 'IC-01-11', 'STORAGE', 150, 'ENABLED', 0),
       (9979, 9103, 9930, 9952, 1, 2, 'IC-01-12', 'STORAGE', 150, 'ENABLED', 0),
       (9980, 9103, 9931, 9953, 1, 1, 'PK-01-11', 'PICK', 150, 'ENABLED', 0),
       (9981, 9103, 9931, 9953, 1, 2, 'PK-01-12', 'PICK', 150, 'ENABLED', 0),
       (9982, 9103, 9932, 9954, 1, 1, 'RV-01-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9983, 9103, 9932, 9954, 1, 2, 'RV-01-12', 'RECEIVE', 100, 'ENABLED', 0),
       (9984, 9103, 9933, 9955, 1, 1, 'NG-01-11', 'QUARANTINE', 100, 'ENABLED', 0),
       (9985, 9103, 9933, 9955, 1, 2, 'NG-01-12', 'QUARANTINE', 100, 'ENABLED', 0),
       (9986, 9104, 9934, 9956, 1, 1, 'SA-01-11', 'STORAGE', 200, 'ENABLED', 0),
       (9987, 9104, 9934, 9956, 1, 2, 'SA-01-12', 'STORAGE', 200, 'ENABLED', 0),
       (9988, 9104, 9934, 9956, 2, 1, 'SA-01-21', 'STORAGE', 200, 'ENABLED', 0),
       (9989, 9104, 9934, 9957, 1, 1, 'SB-01-11', 'STORAGE', 150, 'ENABLED', 0),
       (9990, 9104, 9935, 9958, 1, 1, 'SR-01-11', 'RECEIVE', 100, 'ENABLED', 0),
       (9991, 9104, 9935, 9958, 1, 2, 'SR-01-12', 'RECEIVE', 100, 'ENABLED', 0),
       (9992, 9105, 9936, 9959, 1, 1, 'FG-01-11', 'STORAGE', 120, 'ENABLED', 0),
       (9993, 9105, 9936, 9959, 1, 2, 'FG-01-12', 'STORAGE', 120, 'ENABLED', 0),
       (9994, 9105, 9936, 9959, 2, 1, 'FG-01-21', 'STORAGE', 120, 'ENABLED', 0),
       (9995, 9105, 9936, 9959, 2, 2, 'FG-01-22', 'STORAGE', 120, 'ENABLED', 0),
       (9996, 9105, 9936, 9960, 1, 1, 'FG-02-11', 'STORAGE', 150, 'ENABLED', 0),
       (9997, 9105, 9936, 9960, 1, 2, 'FG-02-12', 'STORAGE', 150, 'ENABLED', 0),
       (9998, 9105, 9937, 9961, 1, 1, 'FP-01-11', 'PICK', 100, 'ENABLED', 0),
       (9999, 9105, 9937, 9961, 1, 2, 'FP-01-12', 'PICK', 100, 'ENABLED', 0)
ON CONFLICT (warehouse_id, code) DO NOTHING;

-- ---- 10.2 分类 / 单位（电子制造域）----
INSERT INTO product_categories (id, parent_id, code, name, sort, created_by)
VALUES (9206, NULL, 'C-SMT', '贴片元器件', 4, 0),
       (9207, NULL, 'C-CONN', '连接器线材', 5, 0),
       (9208, NULL, 'C-PCB', '印制电路板', 6, 0),
       (9209, NULL, 'C-ASSY', '组装成品', 7, 0),
       (9210, NULL, 'C-AST', '制程辅料', 8, 0)
ON CONFLICT (code) DO NOTHING;

INSERT INTO units (id, code, name, created_by)
VALUES (9215, 'U-REEL', '盘（编带）', 0),
       (9216, 'U-KPC', '千颗', 0),
       (9217, 'U-M', '米', 0),
       (9218, 'U-SHT', '张', 0)
ON CONFLICT (code) DO NOTHING;

-- ---- 10.3 商品 16 / SKU 20（电子物料与整机；三开关组合延续覆盖）----
INSERT INTO products (id, code, name, short_name, category_id, brand, model, spec, unit_id, weight, status, created_by)
VALUES (9311, 'P-E001', '贴片电阻 0402 10KΩ', '0402电阻', 9206, 'YAGEO 国巨', 'RC0402FR-0710KL', '10KΩ ±1% 1/16W, 5千颗/盘', 9215, 0.0003, 'ENABLED', 0),
       (9312, 'P-E002', '贴片电阻 0402 100KΩ', '0402电阻', 9206, 'YAGEO 国巨', 'RC0402FR-07100KL', '100KΩ ±1% 1/16W, 5千颗/盘', 9215, 0.0003, 'ENABLED', 0),
       (9313, 'P-E003', '贴片电容 MLCC 0603 22µF', '0603电容', 9206, '三星电机', 'CL10A226MQQNNWE', '22µF ±20% 10V X5R, 3千颗/盘', 9215, 0.0005, 'ENABLED', 0),
       (9314, 'P-E004', '贴片电容 MLCC 0805 100nF', '0805电容', 9206, '三星电机', 'CL21B104KBNNNC', '100nF ±10% 50V X7R', 9215, 0.0005, 'ENABLED', 0),
       (9315, 'P-E005', 'MCU 主控芯片', 'MCU', 9206, 'ST 意法半导体', 'STM32F103C8T6', 'LQFP48, 72MHz, 64KB Flash', 9211, 0.0030, 'ENABLED', 0),
       (9316, 'P-E006', 'DC-DC 电源芯片', '电源IC', 9206, '矽力杰', 'SY8089AAC', 'SOT-23-5, 3A 同步整流', 9211, 0.0010, 'ENABLED', 0),
       (9317, 'P-E007', '石英晶振 8MHz', '晶振', 9206, 'TXC 台晶', '8X-8.000MAAE-T', 'HC-49S, ±20ppm', 9211, 0.0020, 'ENABLED', 0),
       (9318, 'P-E008', '线对板连接器 2.0mm-4P', 'PH2.0连接器', 9207, '联捷电子', 'PH2.0-4P-L', '卧贴 4Pin, 端子镀金', 9211, 0.0040, 'ENABLED', 0),
       (9319, 'P-E009', 'FPC 软排线 0.5mm-30P', 'FPC排线', 9207, '联德电子', 'FPC-30P-80', '0.5mm 间距 30Pin, 长 80mm', 9211, 0.0060, 'ENABLED', 0),
       (9320, 'P-E010', '智能温控器主板 PCBA', '温控主板', 9208, '泰克威', 'STC-MB-V1.2', '双面贴片, 三防漆涂覆', 9211, 0.0800, 'ENABLED', 0),
       (9321, 'P-E011', 'PCB 空板 双面 FR-4', 'PCB空板', 9208, '迅捷电路', 'STC-V1.2', '100x80mm 1.6mm 沉金', 9211, 0.0600, 'ENABLED', 0),
       (9322, 'P-E012', '无铅锡膏 Sn63/Pb37', '锡膏', 9210, '唯特偶', 'SPT-6337', '500g/罐, 保质 180 天 0~10℃', 9211, 0.5200, 'ENABLED', 0),
       (9323, 'P-E013', 'SMT 钢网 420x520', '钢网', 9210, '精诚网版', 'SG-4252', '420x520mm 激光切割', 9211, 1.2000, 'ENABLED', 0),
       (9324, 'P-E014', '防静电周转托盘', 'ESD托盘', 9203, '有力防静电', 'ESD-3116', '316x316x60mm 黑色', 9211, 0.4500, 'ENABLED', 0),
       (9325, 'P-E015', '智能温控器 STC-2000', '温控器', 9209, '泰克威', 'STC-2000', '白色, NTC/继电器, 220V', 9211, 0.3200, 'ENABLED', 0),
       (9326, 'P-E016', 'WiFi 智能插座 SP-10', '智能插座', 9209, '泰克威', 'SP-10', '16A, 支持 Alexa/小爱', 9211, 0.1500, 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- 20 SKU：批次×8 / 批次+效期×2（MCU 湿敏效期、锡膏冷藏效期）/ 批次+序列号×1（PCBA）/ 序列号×3（整机）/ 全关×6
INSERT INTO skus (id, code, product_id, spec_attrs, cost_price, sale_price, safety_stock, max_stock,
                  min_replenish_qty, is_batch_managed, is_expiry_managed, is_serial_managed, is_enabled, created_by)
VALUES (9421, 'SKU-E001-01', 9311, '{"阻值": "10KΩ", "封装": "0402"}'::jsonb, 8.5000, 18.0000, 20.0000, 400.0000, 20.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9422, 'SKU-E001-02', 9312, '{"阻值": "100KΩ", "封装": "0402"}'::jsonb, 8.5000, 18.0000, 20.0000, 400.0000, 20.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9423, 'SKU-E002-01', 9313, '{"容值": "22µF", "封装": "0603"}'::jsonb, 22.0000, 46.0000, 15.0000, 300.0000, 15.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9424, 'SKU-E002-02', 9314, '{"容值": "100nF", "封装": "0805"}'::jsonb, 6.0000, 14.0000, 15.0000, 300.0000, 15.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9425, 'SKU-E003-01', 9315, '{"内核": "Cortex-M3", "闪存": "64KB"}'::jsonb, 12.6000, 25.8000, 200.0000, 5000.0000, 200.0000, TRUE, TRUE, FALSE, TRUE, 0),
       (9426, 'SKU-E004-01', 9316, '{"输出电流": "3A"}'::jsonb, 1.6500, 4.2000, 300.0000, 8000.0000, 300.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9427, 'SKU-E005-01', 9317, '{"频率": "8MHz"}'::jsonb, 0.9800, 2.8000, 200.0000, 8000.0000, 200.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9428, 'SKU-E006-01', 9318, '{"间距": "2.0mm", "Pin": "4P", "方向": "卧贴"}'::jsonb, 0.8500, 2.5000, 100.0000, 5000.0000, 100.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9429, 'SKU-E006-02', 9318, '{"间距": "2.0mm", "Pin": "4P", "方向": "立贴"}'::jsonb, 0.9000, 2.6000, 100.0000, 5000.0000, 100.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9430, 'SKU-E007-01', 9319, '{"Pin": "30P", "长度": "80mm"}'::jsonb, 2.2000, 6.5000, 100.0000, 5000.0000, 100.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9431, 'SKU-E008-01', 9320, '{"版本": "V1.2", "工艺": "三防漆"}'::jsonb, 38.0000, 0.0000, 50.0000, 500.0000, 50.0000, TRUE, FALSE, TRUE, TRUE, 0),
       (9432, 'SKU-E009-01', 9321, '{"板层": "双面", "表面处理": "沉金"}'::jsonb, 15.8000, 0.0000, 100.0000, 2000.0000, 100.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9433, 'SKU-E010-01', 9322, '{"合金": "Sn63/Pb37", "规格": "500g"}'::jsonb, 28.0000, 0.0000, 10.0000, 100.0000, 10.0000, TRUE, TRUE, FALSE, TRUE, 0),
       (9434, 'SKU-E011-01', 9323, '{"尺寸": "420x520mm"}'::jsonb, 180.0000, 0.0000, 2.0000, 20.0000, 2.0000, FALSE, FALSE, FALSE, TRUE, 0),
       (9435, 'SKU-E012-01', 9324, '{"尺寸": "316x316x60mm", "颜色": "黑色"}'::jsonb, 9.8000, 0.0000, 30.0000, 600.0000, 30.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9436, 'SKU-E013-01', 9325, '{"颜色": "白色"}'::jsonb, 68.0000, 129.0000, 20.0000, 300.0000, 20.0000, FALSE, FALSE, TRUE, TRUE, 0),
       (9437, 'SKU-E013-02', 9325, '{"颜色": "黑色"}'::jsonb, 68.0000, 129.0000, 20.0000, 300.0000, 20.0000, FALSE, FALSE, TRUE, TRUE, 0),
       (9438, 'SKU-E014-01', 9326, '{"电流": "16A", "颜色": "白色"}'::jsonb, 24.0000, 59.0000, 30.0000, 500.0000, 30.0000, FALSE, FALSE, TRUE, TRUE, 0),
       (9439, 'SKU-E015-01', 9325, '{"用途": "STC-2000 彩盒"}'::jsonb, 1.8000, 0.0000, 100.0000, 2000.0000, 100.0000, TRUE, FALSE, FALSE, TRUE, 0),
       (9440, 'SKU-E015-02', 9326, '{"用途": "STC-2000/SP-10 通用"}'::jsonb, 0.1200, 0.0000, 500.0000, 10000.0000, 500.0000, FALSE, FALSE, FALSE, TRUE, 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- 条码：20 主条码（EAN13 校验位有效，延续 6901234 前缀）+ 4 辅条码（CODE128）
INSERT INTO barcodes (id, sku_id, barcode, code_type, is_primary, created_by)
VALUES (9477, 9421, '6901234000214', 'EAN13', TRUE, 0),
       (9478, 9422, '6901234000221', 'EAN13', TRUE, 0),
       (9479, 9423, '6901234000238', 'EAN13', TRUE, 0),
       (9480, 9424, '6901234000245', 'EAN13', TRUE, 0),
       (9481, 9425, '6901234000252', 'EAN13', TRUE, 0),
       (9482, 9426, '6901234000269', 'EAN13', TRUE, 0),
       (9483, 9427, '6901234000276', 'EAN13', TRUE, 0),
       (9484, 9428, '6901234000283', 'EAN13', TRUE, 0),
       (9485, 9429, '6901234000290', 'EAN13', TRUE, 0),
       (9486, 9430, '6901234000306', 'EAN13', TRUE, 0),
       (9487, 9431, '6901234000313', 'EAN13', TRUE, 0),
       (9488, 9432, '6901234000320', 'EAN13', TRUE, 0),
       (9489, 9433, '6901234000337', 'EAN13', TRUE, 0),
       (9490, 9434, '6901234000344', 'EAN13', TRUE, 0),
       (9491, 9435, '6901234000351', 'EAN13', TRUE, 0),
       (9492, 9436, '6901234000368', 'EAN13', TRUE, 0),
       (9493, 9437, '6901234000375', 'EAN13', TRUE, 0),
       (9494, 9438, '6901234000382', 'EAN13', TRUE, 0),
       (9495, 9439, '6901234000399', 'EAN13', TRUE, 0),
       (9496, 9440, '6901234000405', 'EAN13', TRUE, 0),
       (9497, 9421, 'SF-E001-01-A', 'CODE128', FALSE, 0),
       (9498, 9425, 'SF-E003-01-A', 'CODE128', FALSE, 0),
       (9499, 9431, 'SF-E008-01-A', 'CODE128', FALSE, 0),
       (9500, 9436, 'SF-E013-01-A', 'CODE128', FALSE, 0)
ON CONFLICT (barcode) DO NOTHING;

-- ---- 10.4 供应商 4 / 客户 3（电子产业链画像）----
INSERT INTO suppliers (id, code, name, contact, phone, email, address, status, remark, created_by)
VALUES (9504, 'SUP-E001', '深圳市华科达电子科技有限公司', '周华强', '13699880001', 'sup-e001@szhkd.com',
        '深圳市福田区华强北街道赛格科技园 4 栋', 'ENABLED', 'DEV SEED 国巨/三星电机授权代理, 账期 30 天', 0),
       (9505, 'SUP-E002', '苏州精连连接器有限公司', '顾精工', '13706210002', 'sup-e002@jsjl.com',
        '苏州市吴中区精连工业坊 6 号', 'ENABLED', 'DEV SEED 连接器模具厂, MOQ 500', 0),
       (9506, 'SUP-E003', '深圳市迅捷电路科技有限公司', '罗迅捷', '13823300003', 'sup-e003@sjpcb.com',
        '深圳市光明区迅捷电路产业园 2 栋', 'ENABLED', 'DEV SEED PCB 快板厂, 48 小时出货', 0),
       (9507, 'SUP-E004', '昆山恒益包装材料有限公司', '钱恒益', '13912680004', 'sup-e004@kshy.com',
        '昆山市玉山镇恒益包装工业园 9 号', 'ENABLED', 'DEV SEED 彩盒/说明书印刷, 账期月结', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

INSERT INTO customers (id, code, name, contact, phone, email, address, shipping_address, status, created_by)
VALUES (9524, 'CUS-E001', '佛山市顺德区智控电器有限公司', '梁智控', '13929980001', 'cus-e001@sdzk.com',
        '佛山市顺德区智控电器产业园 1 栋', '佛山市顺德区智控电器产业园 3 号卸货月台', 'ENABLED', 0),
       (9525, 'CUS-E002', '杭州联物智能科技有限公司', '沈联物', '13957780002', 'cus-e002@hzlw.com',
        '杭州市余杭区联物智能大厦 5 层', '杭州市余杭区联物智能大厦东侧仓库', 'ENABLED', 0),
       (9526, 'CUS-E003', '深圳市睿创电子有限公司', '袁睿创', '13926580003', 'cus-e003@szrc.com',
        '深圳市龙华区睿创科技楼 2 层', '深圳市龙华区睿创科技楼后门收货处', 'ENABLED', 0)
ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING;

-- ---- 10.5 批次 10（含锡膏临期批次：效期 2026-11-10，供效期预警演示）----
INSERT INTO batches (id, sku_id, batch_no, supplier_id, production_date, inbound_date, expiry_date, cost_price, remark, created_by)
VALUES (9608, 9421, 'B20260928-E001', 9504, '2026-09-20', '2026-10-02', NULL, 8.5000, 'DEV SEED', 0),
       (9609, 9421, 'B20261003-E001', 9504, '2026-09-26', '2026-10-05', NULL, 8.5000, 'DEV SEED', 0),
       (9610, 9422, 'B20260928-E002', 9504, '2026-09-22', '2026-09-28', NULL, 8.5000, 'DEV SEED', 0),
       (9611, 9423, 'B20261005-E002', 9504, '2026-09-28', '2026-10-05', NULL, 22.0000, 'DEV SEED', 0),
       (9612, 9425, 'B20260915-E003', 9504, '2026-09-10', '2026-10-02', '2027-09-15', 12.6000, 'DEV SEED MSL3 湿敏, 开封 168h', 0),
       (9613, 9428, 'B20260926-E006', 9505, '2026-09-18', '2026-10-03', NULL, 0.8500, 'DEV SEED', 0),
       (9614, 9431, 'B20261004-E008', 0, '2026-10-04', '2026-10-04', NULL, 38.0000, 'DEV SEED SMT 自制半品', 0),
       (9615, 9433, 'B20260514-E010', 9504, '2026-05-14', '2026-05-20', '2026-11-10', 28.0000, 'DEV SEED 临期演示 (剩余约 35 天)', 0),
       (9616, 9435, 'B20260918-E012', 9504, '2026-09-10', '2026-09-18', NULL, 9.8000, 'DEV SEED', 0),
       (9617, 9439, 'B20261002-E015', 9507, '2026-09-25', '2026-10-02', NULL, 1.8000, 'DEV SEED', 0)
ON CONFLICT (sku_id, batch_no) DO NOTHING;

-- ---- 10.6 电子厂期初库存 18 行（当前存量快照，全 available）与期初流水 1:1 成对（§8 口径）----
INSERT INTO inventory (id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
                       total_qty, available_qty, created_by)
SELECT v.id, w.id, z.id, s.id, b.id, sku.id, COALESCE(bat.id, 0),
       v.qty, v.qty, 0
FROM (VALUES (9825, 'WH-E01', 'REEL-01-11', 'SKU-E001-01', 'B20260928-E001', 30.0000::numeric(18, 4)),
             (9826, 'WH-E01', 'REEL-01-12', 'SKU-E001-01', 'B20261003-E001', 10.0000::numeric(18, 4)),
             (9827, 'WH-E01', 'REEL-01-21', 'SKU-E001-02', 'B20260928-E002', 12.0000::numeric(18, 4)),
             (9828, 'WH-E01', 'REEL-02-11', 'SKU-E002-01', 'B20261005-E002', 25.0000::numeric(18, 4)),
             (9829, 'WH-E01', 'REEL-02-12', 'SKU-E003-01', 'B20260915-E003', 800.0000::numeric(18, 4)),
             (9830, 'WH-E01', 'IC-01-11', 'SKU-E004-01', NULL, 500.0000::numeric(18, 4)),
             (9831, 'WH-E01', 'IC-01-12', 'SKU-E005-01', NULL, 300.0000::numeric(18, 4)),
             (9832, 'WH-E01', 'PK-01-11', 'SKU-E006-01', 'B20260926-E006', 400.0000::numeric(18, 4)),
             (9833, 'WH-E01', 'PK-01-12', 'SKU-E006-02', NULL, 260.0000::numeric(18, 4)),
             (9834, 'WH-E01', 'REEL-02-21', 'SKU-E010-01', 'B20260514-E010', 18.0000::numeric(18, 4)),
             (9835, 'WH-E01', 'REEL-02-22', 'SKU-E012-01', 'B20260918-E012', 60.0000::numeric(18, 4)),
             (9836, 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 20.0000::numeric(18, 4)),
             (9837, 'WH-E02', 'SB-01-11', 'SKU-E001-01', 'B20260928-E001', 5.0000::numeric(18, 4)),
             (9838, 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 18.0000::numeric(18, 4)),
             (9839, 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 8.0000::numeric(18, 4)),
             (9840, 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 20.0000::numeric(18, 4)),
             (9841, 'WH-E03', 'FG-02-11', 'SKU-E015-01', 'B20261002-E015', 500.0000::numeric(18, 4)),
             (9842, 'WH-E03', 'FP-01-11', 'SKU-E015-02', NULL, 2000.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (warehouse_id, bin_id, sku_id, batch_id) DO NOTHING;

INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, COALESCE(bat.id, 0),
       'INBOUND', '期初', 'DEV-SEED-OPEN-' || v.id::text, 'available', 'available',
       0, v.qty, v.qty,
       0, 'dev-seed', 'dev-seed', 'DEV SEED',
       CASE WHEN v.id <= 9884 THEN '2026-10-02 10:00:00+08'::timestamptz
            WHEN v.id <= 9889 THEN '2026-10-03 11:30:00+08'::timestamptz
            WHEN v.id <= 9893 THEN '2026-10-04 15:00:00+08'::timestamptz
            ELSE '2026-10-05 09:00:00+08'::timestamptz END
FROM (VALUES (9879, 'WH-E01', 'REEL-01-11', 'SKU-E001-01', 'B20260928-E001', 30.0000::numeric(18, 4)),
             (9880, 'WH-E01', 'REEL-01-12', 'SKU-E001-01', 'B20261003-E001', 10.0000::numeric(18, 4)),
             (9881, 'WH-E01', 'REEL-01-21', 'SKU-E001-02', 'B20260928-E002', 12.0000::numeric(18, 4)),
             (9882, 'WH-E01', 'REEL-02-11', 'SKU-E002-01', 'B20261005-E002', 25.0000::numeric(18, 4)),
             (9883, 'WH-E01', 'REEL-02-12', 'SKU-E003-01', 'B20260915-E003', 800.0000::numeric(18, 4)),
             (9884, 'WH-E01', 'IC-01-11', 'SKU-E004-01', NULL, 500.0000::numeric(18, 4)),
             (9885, 'WH-E01', 'IC-01-12', 'SKU-E005-01', NULL, 300.0000::numeric(18, 4)),
             (9886, 'WH-E01', 'PK-01-11', 'SKU-E006-01', 'B20260926-E006', 400.0000::numeric(18, 4)),
             (9887, 'WH-E01', 'PK-01-12', 'SKU-E006-02', NULL, 260.0000::numeric(18, 4)),
             (9888, 'WH-E01', 'REEL-02-21', 'SKU-E010-01', 'B20260514-E010', 18.0000::numeric(18, 4)),
             (9889, 'WH-E01', 'REEL-02-22', 'SKU-E012-01', 'B20260918-E012', 60.0000::numeric(18, 4)),
             (9890, 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 20.0000::numeric(18, 4)),
             (9891, 'WH-E02', 'SB-01-11', 'SKU-E001-01', 'B20260928-E001', 5.0000::numeric(18, 4)),
             (9892, 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 18.0000::numeric(18, 4)),
             (9893, 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 8.0000::numeric(18, 4)),
             (9894, 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 20.0000::numeric(18, 4)),
             (9895, 'WH-E03', 'FG-02-11', 'SKU-E015-01', 'B20261002-E015', 500.0000::numeric(18, 4)),
             (9896, 'WH-E03', 'FP-01-11', 'SKU-E015-02', NULL, 2000.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (ledger_no) DO NOTHING;

-- 影流水（净零对，供出入库趋势非零形态；端点与库存行现值吻合）
INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, COALESCE(bat.id, 0),
       v.ctype, '演示', 'DEV-SEED-FLOW-' || v.id::text, 'available', 'available',
       v.q_before, v.q_change, v.q_after,
       0, 'dev-seed', 'dev-seed', 'DEV SEED', v.at
FROM (VALUES (9897, 'WH-E01', 'REEL-01-11', 'SKU-E001-01', 'B20260928-E001', 'INBOUND',  30.0000::numeric(18,4),  10.0000::numeric(18,4),  40.0000::numeric(18,4), '2026-10-05 08:20:00+08'::timestamptz),
             (9898, 'WH-E01', 'REEL-01-11', 'SKU-E001-01', 'B20260928-E001', 'OUTBOUND', 40.0000::numeric(18,4), -10.0000::numeric(18,4),  30.0000::numeric(18,4), '2026-10-05 08:50:00+08'::timestamptz),
             (9899, 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'INBOUND',  20.0000::numeric(18,4),  15.0000::numeric(18,4),  35.0000::numeric(18,4), '2026-10-05 09:40:00+08'::timestamptz),
             (9900, 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 35.0000::numeric(18,4), -15.0000::numeric(18,4),  20.0000::numeric(18,4), '2026-10-05 10:10:00+08'::timestamptz)) AS v(id, wh_code, bin_code, sku_code, batch_no, ctype, q_before, q_change, q_after, at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (ledger_no) DO NOTHING;

-- ---- 10.7 电子厂序列号 78 行（PCBA 20 + 白温控器 30 + 黑温控器 8 + 智能插座 40；
--      已发货部分 status=OUTBOUND 留痕，IN_STOCK 数与期初库存行吻合）----
INSERT INTO serial_numbers (id, serial_no, sku_id, batch_id, warehouse_id, bin_id, status,
                            last_source_type, last_source_no, last_event_at, created_by)
SELECT v.id, v.serial_no, sku.id, COALESCE(bat.id, 0), w.id, b.id, v.status,
       v.src_type, v.src_no, v.at, 0
FROM (VALUES
  (9724, 'SN-PCBA-B1004-0001', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9725, 'SN-PCBA-B1004-0002', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9726, 'SN-PCBA-B1004-0003', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9727, 'SN-PCBA-B1004-0004', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9728, 'SN-PCBA-B1004-0005', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9729, 'SN-PCBA-B1004-0006', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9730, 'SN-PCBA-B1004-0007', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9731, 'SN-PCBA-B1004-0008', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9732, 'SN-PCBA-B1004-0009', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9733, 'SN-PCBA-B1004-0010', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9734, 'SN-PCBA-B1004-0011', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9735, 'SN-PCBA-B1004-0012', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9736, 'SN-PCBA-B1004-0013', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9737, 'SN-PCBA-B1004-0014', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9738, 'SN-PCBA-B1004-0015', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9739, 'SN-PCBA-B1004-0016', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9740, 'SN-PCBA-B1004-0017', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9741, 'SN-PCBA-B1004-0018', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9742, 'SN-PCBA-B1004-0019', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9743, 'SN-PCBA-B1004-0020', 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 11:00:00+08'::timestamptz),
  (9744, 'SN-STC-W-A0001', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9745, 'SN-STC-W-A0002', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9746, 'SN-STC-W-A0003', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9747, 'SN-STC-W-A0004', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9748, 'SN-STC-W-A0005', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9749, 'SN-STC-W-A0006', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9750, 'SN-STC-W-A0007', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9751, 'SN-STC-W-A0008', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9752, 'SN-STC-W-A0009', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9753, 'SN-STC-W-A0010', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9754, 'SN-STC-W-A0011', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9755, 'SN-STC-W-A0012', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261003-000001', '2026-10-04 16:30:00+08'::timestamptz),
  (9756, 'SN-STC-W-A0013', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9757, 'SN-STC-W-A0014', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9758, 'SN-STC-W-A0015', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9759, 'SN-STC-W-A0016', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9760, 'SN-STC-W-A0017', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9761, 'SN-STC-W-A0018', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9762, 'SN-STC-W-A0019', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9763, 'SN-STC-W-A0020', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9764, 'SN-STC-W-A0021', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9765, 'SN-STC-W-A0022', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9766, 'SN-STC-W-A0023', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9767, 'SN-STC-W-A0024', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9768, 'SN-STC-W-A0025', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9769, 'SN-STC-W-A0026', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9770, 'SN-STC-W-A0027', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9771, 'SN-STC-W-A0028', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9772, 'SN-STC-W-A0029', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9773, 'SN-STC-W-A0030', 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:00:00+08'::timestamptz),
  (9774, 'SN-STC-B-A0001', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9775, 'SN-STC-B-A0002', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9776, 'SN-STC-B-A0003', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9777, 'SN-STC-B-A0004', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9778, 'SN-STC-B-A0005', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9779, 'SN-STC-B-A0006', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9780, 'SN-STC-B-A0007', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9781, 'SN-STC-B-A0008', 'WH-E03', 'FG-01-21', 'SKU-E013-02', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261003-000001', '2026-10-03 10:20:00+08'::timestamptz),
  (9782, 'SN-SP10-A0001', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9783, 'SN-SP10-A0002', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9784, 'SN-SP10-A0003', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9785, 'SN-SP10-A0004', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9786, 'SN-SP10-A0005', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9787, 'SN-SP10-A0006', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9788, 'SN-SP10-A0007', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9789, 'SN-SP10-A0008', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9790, 'SN-SP10-A0009', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9791, 'SN-SP10-A0010', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'OUTBOUND', 'OUTBOUND', 'OUT-20261004-000002', '2026-10-05 11:20:00+08'::timestamptz),
  (9792, 'SN-SP10-A0011', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9793, 'SN-SP10-A0012', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9794, 'SN-SP10-A0013', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9795, 'SN-SP10-A0014', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9796, 'SN-SP10-A0015', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9797, 'SN-SP10-A0016', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9798, 'SN-SP10-A0017', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9799, 'SN-SP10-A0018', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9800, 'SN-SP10-A0019', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9801, 'SN-SP10-A0020', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9802, 'SN-SP10-A0021', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9803, 'SN-SP10-A0022', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9804, 'SN-SP10-A0023', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9805, 'SN-SP10-A0024', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9806, 'SN-SP10-A0025', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9807, 'SN-SP10-A0026', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9808, 'SN-SP10-A0027', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9809, 'SN-SP10-A0028', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9810, 'SN-SP10-A0029', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz),
  (9811, 'SN-SP10-A0030', 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'IN_STOCK', 'INBOUND', 'IN-20261004-000001', '2026-10-04 09:30:00+08'::timestamptz)
) AS v(id, serial_no, wh_code, bin_code, sku_code, batch_no, status, src_type, src_no, at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (serial_no) DO NOTHING;

-- ============ 11) 电子厂单据链（单号走 docnum 真实格式并推进计数器；明细四量/状态机自洽） ============

-- ---- 11.1 采购订单 4（完成 / 部分收货 / 待审核 / 草稿）----
INSERT INTO purchase_orders (po_no, supplier_id, warehouse_id, total_amount, status, approved_by, approved_at,
                             received_at, completed_at, remark, created_at, created_by, updated_by)
SELECT v.po_no, sup.id, w.id, v.total, v.status,
       CASE WHEN v.status IN ('APPROVED', 'PARTIAL_RECEIVED', 'RECEIVED_ALL', 'COMPLETED') THEN 9901 ELSE 0 END,
       v.approved_at, v.received_at, v.completed_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('PO-20261002-000001', 'SUP-E001', 'WH-E01', 10970.0000::numeric(18, 4), 'COMPLETED',
              '2026-10-02 09:10:00+08'::timestamptz, '2026-10-02 14:20:00+08'::timestamptz, '2026-10-03 17:30:00+08'::timestamptz,
              'SMT 常规补料: 电阻/电容/MCU', '2026-10-02 09:05:00+08'::timestamptz),
             ('PO-20261003-000002', 'SUP-E002', 'WH-E01', 1404.0000::numeric(18, 4), 'PARTIAL_RECEIVED',
              '2026-10-03 10:00:00+08'::timestamptz, '2026-10-03 15:40:00+08'::timestamptz, NULL::timestamptz,
              '连接器分批到货, FPC 待供', '2026-10-03 09:50:00+08'::timestamptz),
             ('PO-20261004-000003', 'SUP-E003', 'WH-E01', 8740.0000::numeric(18, 4), 'PENDING_APPROVAL',
              NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
              'PCB V1.2 批量板 + 锡膏补库', '2026-10-04 16:00:00+08'::timestamptz),
             ('PO-20261005-000004', 'SUP-E004', 'WH-E03', 2900.0000::numeric(18, 4), 'DRAFT',
              NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
              '成品包装材料月度采购', '2026-10-05 10:30:00+08'::timestamptz)) AS v(po_no, sup_code, wh_code, total, status, approved_at, received_at, completed_at, remark, created_at)
JOIN suppliers sup ON sup.code = v.sup_code AND sup.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (po_no) DO NOTHING;

INSERT INTO purchase_order_items (po_id, line_no, sku_id, qty_ordered, qty_received, qty_rejected, qty_putaway,
                                  price, amount, remark, created_at, created_by)
SELECT p.id, v.line_no, sku.id, v.qty, v.received, 0, v.putaway, v.price, v.qty * v.price, '', v.created_at, 9901
FROM (VALUES ('PO-20261002-000001', 1, 'SKU-E001-01', 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), 8.5000::numeric(18, 4), '2026-10-02 09:05:00+08'::timestamptz),
             ('PO-20261002-000001', 2, 'SKU-E002-01', 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 22.0000::numeric(18, 4), '2026-10-02 09:05:00+08'::timestamptz),
             ('PO-20261002-000001', 3, 'SKU-E003-01', 800.0000::numeric(18, 4), 800.0000::numeric(18, 4), 800.0000::numeric(18, 4), 12.6000::numeric(18, 4), '2026-10-02 09:05:00+08'::timestamptz),
             ('PO-20261003-000002', 1, 'SKU-E006-01', 600.0000::numeric(18, 4), 400.0000::numeric(18, 4), 400.0000::numeric(18, 4), 0.8500::numeric(18, 4), '2026-10-03 09:50:00+08'::timestamptz),
             ('PO-20261003-000002', 2, 'SKU-E006-02', 260.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.9000::numeric(18, 4), '2026-10-03 09:50:00+08'::timestamptz),
             ('PO-20261003-000002', 3, 'SKU-E007-01', 300.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 2.2000::numeric(18, 4), '2026-10-03 09:50:00+08'::timestamptz),
             ('PO-20261004-000003', 1, 'SKU-E009-01', 500.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 15.8000::numeric(18, 4), '2026-10-04 16:00:00+08'::timestamptz),
             ('PO-20261004-000003', 2, 'SKU-E010-01', 30.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 28.0000::numeric(18, 4), '2026-10-04 16:00:00+08'::timestamptz),
             ('PO-20261005-000004', 1, 'SKU-E015-01', 2000.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 1.1500::numeric(18, 4), '2026-10-05 10:30:00+08'::timestamptz),
             ('PO-20261005-000004', 2, 'SKU-E015-02', 5000.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.1200::numeric(18, 4), '2026-10-05 10:30:00+08'::timestamptz)) AS v(po_no, line_no, sku_code, qty, received, putaway, price, created_at)
JOIN purchase_orders p ON p.po_no = v.po_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 11.2 入库单 2（完成全链 / 待上架）+ 收货记录 4（事件型，批号/效期采集）----
INSERT INTO inbound_orders (inbound_no, source_type, source_no, warehouse_id, status, received_at, inspected_at,
                            putaway_at, completed_at, remark, created_at, created_by, updated_by)
SELECT v.inbound_no, 'PURCHASE', v.po_no, w.id, v.status, v.received_at, v.inspected_at, v.putaway_at, v.completed_at,
       v.remark, v.created_at, 9902, 9902
FROM (VALUES ('IN-20261002-000001', 'PO-20261002-000001', 'WH-E01', 'COMPLETED',
              '2026-10-02 14:30:00+08'::timestamptz, '2026-10-03 10:30:00+08'::timestamptz, '2026-10-03 17:00:00+08'::timestamptz, '2026-10-03 17:30:00+08'::timestamptz,
              'SMT 补料入库（全链走完）', '2026-10-02 14:10:00+08'::timestamptz),
             ('IN-20261003-000002', 'PO-20261003-000002', 'WH-E01', 'AWAITING_PUTAWAY',
              '2026-10-03 15:50:00+08'::timestamptz, '2026-10-04 09:40:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz,
              '连接器到货 400, 质检合格待上架', '2026-10-03 15:30:00+08'::timestamptz)) AS v(inbound_no, po_no, wh_code, status, received_at, inspected_at, putaway_at, completed_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (inbound_no) DO NOTHING;

INSERT INTO inbound_items (inbound_id, line_no, sku_id, qty, qty_received, qty_inspected, qty_putaway, remark, created_at, created_by)
SELECT i.id, v.line_no, sku.id, v.qty, v.received, v.inspected, v.putaway, '', v.created_at, 9902
FROM (VALUES ('IN-20261002-000001', 1, 'SKU-E001-01', 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), '2026-10-02 14:10:00+08'::timestamptz),
             ('IN-20261002-000001', 2, 'SKU-E002-01', 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), '2026-10-02 14:10:00+08'::timestamptz),
             ('IN-20261002-000001', 3, 'SKU-E003-01', 800.0000::numeric(18, 4), 800.0000::numeric(18, 4), 800.0000::numeric(18, 4), 800.0000::numeric(18, 4), '2026-10-02 14:10:00+08'::timestamptz),
             ('IN-20261003-000002', 1, 'SKU-E006-01', 600.0000::numeric(18, 4), 400.0000::numeric(18, 4), 400.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-03 15:30:00+08'::timestamptz),
             ('IN-20261003-000002', 2, 'SKU-E006-02', 260.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-03 15:30:00+08'::timestamptz),
             ('IN-20261003-000002', 3, 'SKU-E007-01', 300.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-03 15:30:00+08'::timestamptz)) AS v(inbound_no, line_no, sku_code, qty, received, inspected, putaway, created_at)
JOIN inbound_orders i ON i.inbound_no = v.inbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- 收货：一收货单一批号（receipts.batch_no 语义），PO-1 三批三张 + PO-2 一张
INSERT INTO receipts (receipt_no, inbound_no, warehouse_id, batch_no, expiry_date, production_date,
                      idempotency_key, operator_id, operator_name, remark, created_at, created_by)
SELECT v.receipt_no, v.inbound_no, w.id, v.batch_no, v.expiry_date, v.production_date,
       'dev-seed-' || v.receipt_no, 9902, '李收货', 'DEV SEED', v.created_at, 9902
FROM (VALUES ('RC-20261002-000001', 'IN-20261002-000001', 'WH-E01', 'B20260928-E001', NULL::date, '2026-09-20'::date, '2026-10-02 14:30:00+08'::timestamptz),
             ('RC-20261002-000002', 'IN-20261002-000001', 'WH-E01', 'B20261005-E002', NULL::date, '2026-09-28'::date, '2026-10-02 14:50:00+08'::timestamptz),
             ('RC-20261002-000003', 'IN-20261002-000001', 'WH-E01', 'B20260915-E003', '2027-09-15'::date, '2026-09-10'::date, '2026-10-02 15:10:00+08'::timestamptz),
             ('RC-20261003-000001', 'IN-20261003-000002', 'WH-E01', 'B20260926-E006', NULL::date, '2026-09-18'::date, '2026-10-03 15:50:00+08'::timestamptz)) AS v(receipt_no, inbound_no, wh_code, batch_no, expiry_date, production_date, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (receipt_no) DO NOTHING;

INSERT INTO receipt_items (receipt_id, line_no, sku_id, qty_good, qty_rejected, exception_ref, remark, created_at, created_by)
SELECT r.id, v.line_no, sku.id, v.qty_good, 0, '', '', r.created_at, 9902
FROM (VALUES ('RC-20261002-000001', 1, 'SKU-E001-01', 40.0000::numeric(18, 4)),
             ('RC-20261002-000002', 1, 'SKU-E002-01', 25.0000::numeric(18, 4)),
             ('RC-20261002-000003', 1, 'SKU-E003-01', 800.0000::numeric(18, 4)),
             ('RC-20261003-000001', 1, 'SKU-E006-01', 400.0000::numeric(18, 4))) AS v(receipt_no, line_no, sku_code, qty_good)
JOIN receipts r ON r.receipt_no = v.receipt_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 11.3 质检 2（抽检合格，已完成）----
INSERT INTO quality_orders (qc_no, source_type, source_no, warehouse_id, inspection_type, status,
                            qty_inspected, qty_qualified, qty_defective, result, inspector_id, inspector_name,
                            inspected_at, remark, created_at, created_by)
SELECT v.qc_no, 'INBOUND', v.inbound_no, w.id, '抽检', 'COMPLETED',
       v.qty, v.qty, 0, '合格', 9904, '赵盘点', v.inspected_at, 'DEV SEED IQC 抽检 AQL 0.65', v.created_at, 9901
FROM (VALUES ('QC-20261002-000001', 'IN-20261002-000001', 'WH-E01', 865.0000::numeric(18, 4), '2026-10-03 10:30:00+08'::timestamptz, '2026-10-02 18:00:00+08'::timestamptz),
             ('QC-20261003-000001', 'IN-20261003-000002', 'WH-E01', 400.0000::numeric(18, 4), '2026-10-04 09:40:00+08'::timestamptz, '2026-10-03 18:00:00+08'::timestamptz)) AS v(qc_no, inbound_no, wh_code, qty, inspected_at, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (qc_no) DO NOTHING;

INSERT INTO quality_items (qc_id, line_no, sku_id, batch_no, qty_inspected, qty_qualified, qty_defective, remark, created_at, created_by)
SELECT q.id, v.line_no, sku.id, v.batch_no, v.qty, v.qty, 0, '', q.created_at, 9901
FROM (VALUES ('QC-20261002-000001', 1, 'SKU-E001-01', 'B20260928-E001', 40.0000::numeric(18, 4)),
             ('QC-20261002-000001', 2, 'SKU-E002-01', 'B20261005-E002', 25.0000::numeric(18, 4)),
             ('QC-20261002-000001', 3, 'SKU-E003-01', 'B20260915-E003', 800.0000::numeric(18, 4)),
             ('QC-20261003-000001', 1, 'SKU-E006-01', 'B20260926-E006', 400.0000::numeric(18, 4))) AS v(qc_no, line_no, sku_code, batch_no, qty)
JOIN quality_orders q ON q.qc_no = v.qc_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 11.4 上架任务 4（3 完成 + 1 待办）----
INSERT INTO putaway_tasks (putaway_no, inbound_no, receipt_no, sku_id, batch_id, serial_no, qty, from_state,
                           target_warehouse_id, target_zone_id, target_shelf_id, target_bin_id, status,
                           claimed_by, claimed_at, completed_at, remark, created_at, created_by)
SELECT v.putaway_no, v.inbound_no, v.receipt_no, sku.id, COALESCE(bat.id, 0), '',
       v.qty, 'available', w.id, b.zone_id, b.shelf_id, b.id, v.status,
       CASE WHEN v.status = 'COMPLETED' THEN 9902 ELSE 0 END,
       CASE WHEN v.status = 'COMPLETED' THEN v.done_at ELSE NULL END,
       CASE WHEN v.status = 'COMPLETED' THEN v.done_at ELSE NULL END,
       'DEV SEED', v.created_at, 9902
FROM (VALUES ('PW-20261003-000001', 'IN-20261002-000001', 'RC-20261002-000001', 'SKU-E001-01', 'B20260928-E001', 40.0000::numeric(18, 4), 'COMPLETED', 'WH-E01', 'REEL-01-11', '2026-10-03 16:40:00+08'::timestamptz, '2026-10-03 14:00:00+08'::timestamptz),
             ('PW-20261003-000002', 'IN-20261002-000001', 'RC-20261002-000002', 'SKU-E002-01', 'B20261005-E002', 25.0000::numeric(18, 4), 'COMPLETED', 'WH-E01', 'REEL-02-11', '2026-10-03 16:50:00+08'::timestamptz, '2026-10-03 14:05:00+08'::timestamptz),
             ('PW-20261003-000003', 'IN-20261002-000001', 'RC-20261002-000003', 'SKU-E003-01', 'B20260915-E003', 800.0000::numeric(18, 4), 'COMPLETED', 'WH-E01', 'REEL-02-12', '2026-10-03 17:00:00+08'::timestamptz, '2026-10-03 14:10:00+08'::timestamptz),
             ('PW-20261004-000001', 'IN-20261003-000002', 'RC-20261003-000001', 'SKU-E006-01', 'B20260926-E006', 400.0000::numeric(18, 4), 'PENDING', 'WH-E01', 'PK-01-11', NULL::timestamptz, '2026-10-04 10:00:00+08'::timestamptz)) AS v(putaway_no, inbound_no, receipt_no, sku_code, batch_no, qty, status, wh_code, bin_code, done_at, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (putaway_no) DO NOTHING;

-- ---- 11.5 销售订单 3（完成 / 部分发货 / 待审核）+ 出库执行链 ----
INSERT INTO sales_orders (so_no, customer_id, warehouse_id, shipping_address, delivery_method, total_amount,
                          status, approved_by, approved_at, shipped_at, completed_at, remark, created_at, created_by, updated_by)
SELECT v.so_no, cus.id, w.id, cus.shipping_address, v.delivery_method, v.total, v.status,
       CASE WHEN v.status IN ('APPROVED', 'PARTIAL_SHIPPED', 'SHIPPED_ALL', 'COMPLETED') THEN 9901 ELSE 0 END,
       v.approved_at, v.shipped_at, v.completed_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('SO-20261003-000001', 'CUS-E001', 'WH-E03', '物流专线', 1584.0000::numeric(18, 4), 'COMPLETED',
              '2026-10-03 11:20:00+08'::timestamptz, '2026-10-04 16:30:00+08'::timestamptz, '2026-10-04 16:30:00+08'::timestamptz,
              '首批温控器订单', '2026-10-03 11:00:00+08'::timestamptz),
             ('SO-20261004-000002', 'CUS-E002', 'WH-E03', '快递', 2720.0000::numeric(18, 4), 'PARTIAL_SHIPPED',
              '2026-10-04 10:10:00+08'::timestamptz, '2026-10-05 11:20:00+08'::timestamptz, NULL::timestamptz,
              '插座现货先发, 黑色温控器等下批补发', '2026-10-04 09:50:00+08'::timestamptz),
             ('SO-20261005-000003', 'CUS-E003', 'WH-E03', '物流专线', 1554.0000::numeric(18, 4), 'PENDING_APPROVAL',
              NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
              '样机 + 铺货', '2026-10-05 14:00:00+08'::timestamptz)) AS v(so_no, cus_code, wh_code, delivery_method, total, status, approved_at, shipped_at, completed_at, remark, created_at)
JOIN customers cus ON cus.code = v.cus_code AND cus.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (so_no) DO NOTHING;

INSERT INTO sales_order_items (so_id, line_no, sku_id, qty, price, amount, qty_allocated, qty_shipped, remark, created_at, created_by)
SELECT so.id, v.line_no, sku.id, v.qty, v.price, v.qty * v.price, v.allocated, v.shipped, '', v.created_at, 9901
FROM (VALUES ('SO-20261003-000001', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 129.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), '2026-10-03 11:00:00+08'::timestamptz),
             ('SO-20261003-000001', 2, 'SKU-E015-01', 20.0000::numeric(18, 4), 1.8000::numeric(18, 4), 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), '2026-10-03 11:00:00+08'::timestamptz),
             ('SO-20261004-000002', 1, 'SKU-E014-01', 40.0000::numeric(18, 4), 39.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), '2026-10-04 09:50:00+08'::timestamptz),
             ('SO-20261004-000002', 2, 'SKU-E013-02', 8.0000::numeric(18, 4), 145.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-04 09:50:00+08'::timestamptz),
             ('SO-20261005-000003', 1, 'SKU-E013-01', 6.0000::numeric(18, 4), 129.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-05 14:00:00+08'::timestamptz),
             ('SO-20261005-000003', 2, 'SKU-E014-01', 20.0000::numeric(18, 4), 39.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-05 14:00:00+08'::timestamptz)) AS v(so_no, line_no, sku_code, qty, price, allocated, shipped, created_at)
JOIN sales_orders so ON so.so_no = v.so_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

INSERT INTO outbound_orders (outbound_no, so_no, type, warehouse_id, status, picked_at, checked_at, packed_at,
                             shipped_at, remark, created_at, created_by, updated_by)
SELECT v.outbound_no, v.so_no, '销售出库', w.id, v.status, v.picked_at, v.checked_at, v.packed_at, v.shipped_at,
       v.remark, v.created_at, 9903, 9903
FROM (VALUES ('OUT-20261003-000001', 'SO-20261003-000001', 'WH-E03', 'SHIPPED_ALL',
              '2026-10-04 14:20:00+08'::timestamptz, '2026-10-04 15:10:00+08'::timestamptz, '2026-10-04 15:40:00+08'::timestamptz, '2026-10-04 16:30:00+08'::timestamptz,
              '全链完成', '2026-10-03 11:30:00+08'::timestamptz),
             ('OUT-20261004-000002', 'SO-20261004-000002', 'WH-E03', 'PARTIAL_SHIPPED',
              '2026-10-05 10:00:00+08'::timestamptz, '2026-10-05 10:40:00+08'::timestamptz, '2026-10-05 10:55:00+08'::timestamptz, '2026-10-05 11:20:00+08'::timestamptz,
              '现货部分先发, 黑色待补', '2026-10-04 10:20:00+08'::timestamptz)) AS v(outbound_no, so_no, wh_code, status, picked_at, checked_at, packed_at, shipped_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (outbound_no) DO NOTHING;

INSERT INTO outbound_items (outbound_id, line_no, sku_id, qty, qty_picked, qty_checked, qty_packed, qty_shipped, remark, created_at, created_by)
SELECT o.id, v.line_no, sku.id, v.qty, v.picked, v.checked, v.packed, v.shipped, '', v.created_at, 9903
FROM (VALUES ('OUT-20261003-000001', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), '2026-10-03 11:30:00+08'::timestamptz),
             ('OUT-20261003-000001', 2, 'SKU-E015-01', 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), '2026-10-03 11:30:00+08'::timestamptz),
             ('OUT-20261004-000002', 1, 'SKU-E014-01', 40.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), '2026-10-04 10:20:00+08'::timestamptz),
             ('OUT-20261004-000002', 2, 'SKU-E013-02', 8.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-04 10:20:00+08'::timestamptz)) AS v(outbound_no, line_no, sku_code, qty, picked, checked, packed, shipped, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- 分配记录：OUT-1 两行（FIFO）已完成；OUT-2 已发部分（lock 核销后 lock_id=0）
-- 幂等：本表除主键外无唯一索引（同一出库行可跨批次分多条分配记录），ON CONFLICT 无从触发，
--   故与 document_approvals 同口径改用 WHERE NOT EXISTS 防重跑重复追加。
INSERT INTO allocation_records (outbound_no, line_no, sku_id, batch_id, warehouse_id, bin_id, qty, strategy, reason, lock_id, created_at, created_by)
SELECT v.outbound_no, v.line_no, sku.id, COALESCE(bat.id, 0), w.id, b.id, v.qty, v.strategy,
       jsonb_build_object('hit', v.strategy, 'available_snapshot', v.qty), 0, v.created_at, 9901
FROM (VALUES ('OUT-20261003-000001', 1, 'SKU-E013-01', NULL, 'WH-E03', 'FG-01-11', 12.0000::numeric(18, 4), 'FIFO', '2026-10-03 11:40:00+08'::timestamptz),
             ('OUT-20261003-000001', 2, 'SKU-E015-01', 'B20261002-E015', 'WH-E03', 'FG-02-11', 20.0000::numeric(18, 4), 'FIFO', '2026-10-03 11:40:00+08'::timestamptz),
             ('OUT-20261004-000002', 1, 'SKU-E014-01', NULL, 'WH-E03', 'FG-01-22', 10.0000::numeric(18, 4), 'FIFO', '2026-10-04 10:40:00+08'::timestamptz)) AS v(outbound_no, line_no, sku_code, batch_no, wh_code, bin_code, qty, strategy, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
WHERE NOT EXISTS (
    SELECT 1 FROM allocation_records ar
    WHERE ar.outbound_no = v.outbound_no AND ar.line_no = v.line_no AND ar.sku_id = sku.id);

-- 拣货任务 4（2 完成 + 1 拣货中 + 1 待领）
INSERT INTO pick_tasks (pick_no, outbound_no, outbound_line_no, sku_id, batch_id, source_warehouse_id,
                        source_zone_id, source_shelf_id, source_bin_id, qty, picked_qty, status,
                        assignee_id, assignee_name, claimed_at, picked_at, scanned_code, scan_matched,
                        warehouse_id, remark, created_at, created_by)
SELECT v.pick_no, v.outbound_no, v.line_no, sku.id, COALESCE(bat.id, 0), w.id, b.zone_id, b.shelf_id, b.id,
       v.qty, v.picked, v.status,
       CASE WHEN v.status IN ('PICKED', 'PICKING') THEN 9903 ELSE 0 END,
       CASE WHEN v.status IN ('PICKED', 'PICKING') THEN '陈发货' ELSE '' END,
       CASE WHEN v.status IN ('PICKED', 'PICKING') THEN v.picked_at - interval '20 minutes' ELSE NULL END,
       CASE WHEN v.status = 'PICKED' THEN v.picked_at ELSE NULL END,
       CASE WHEN v.status = 'PICKED' THEN v.scan ELSE '' END,
       CASE WHEN v.status = 'PICKED' THEN TRUE ELSE FALSE END,
       w.id, 'DEV SEED', v.created_at, 9903
FROM (VALUES ('PK-20261003-000001', 'OUT-20261003-000001', 1, 'SKU-E013-01', NULL, 'WH-E03', 'FG-01-11', 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 'PICKED', 'SKU-E013-01', '2026-10-04 14:20:00+08'::timestamptz, '2026-10-03 13:00:00+08'::timestamptz),
             ('PK-20261003-000002', 'OUT-20261003-000001', 2, 'SKU-E015-01', 'B20261002-E015', 'WH-E03', 'FG-02-11', 20.0000::numeric(18, 4), 20.0000::numeric(18, 4), 'PICKED', 'SKU-E015-01', '2026-10-04 14:25:00+08'::timestamptz, '2026-10-03 13:00:00+08'::timestamptz),
             ('PK-20261004-000001', 'OUT-20261004-000002', 1, 'SKU-E014-01', NULL, 'WH-E03', 'FG-01-22', 40.0000::numeric(18, 4), 10.0000::numeric(18, 4), 'PICKING', 'SKU-E014-01', '2026-10-05 09:40:00+08'::timestamptz, '2026-10-04 14:00:00+08'::timestamptz),
             ('PK-20261004-000002', 'OUT-20261004-000002', 2, 'SKU-E013-02', NULL, 'WH-E03', 'FG-01-21', 8.0000::numeric(18, 4), 0.0000::numeric(18, 4), 'PENDING', '', NULL::timestamptz, '2026-10-04 14:00:00+08'::timestamptz)) AS v(pick_no, outbound_no, line_no, sku_code, batch_no, wh_code, bin_code, qty, picked, status, scan, picked_at, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (pick_no) DO NOTHING;

-- 复核任务 3（2 完成 + 1 完成；黑行未到复核环节不建）
INSERT INTO check_tasks (check_no, outbound_no, outbound_line_no, sku_id, batch_id, serial_no, qty, status,
                         result, assignee_id, assignee_name, claimed_at, done_at, warehouse_id, remark, created_at, created_by)
SELECT v.check_no, v.outbound_no, v.line_no, sku.id, COALESCE(bat.id, 0), '', v.qty, 'DONE', '',
       9903, '陈发货', v.done_at - interval '15 minutes', v.done_at, w.id, 'DEV SEED', v.created_at, 9903
FROM (VALUES ('CH-20261003-000001', 'OUT-20261003-000001', 1, 'SKU-E013-01', NULL, 12.0000::numeric(18, 4), '2026-10-04 15:10:00+08'::timestamptz, '2026-10-03 13:10:00+08'::timestamptz),
             ('CH-20261003-000002', 'OUT-20261003-000001', 2, 'SKU-E015-01', 'B20261002-E015', 20.0000::numeric(18, 4), '2026-10-04 15:15:00+08'::timestamptz, '2026-10-03 13:10:00+08'::timestamptz),
             ('CH-20261004-000001', 'OUT-20261004-000002', 1, 'SKU-E014-01', NULL, 10.0000::numeric(18, 4), '2026-10-05 10:40:00+08'::timestamptz, '2026-10-04 14:10:00+08'::timestamptz)) AS v(check_no, outbound_no, line_no, sku_code, batch_no, qty, done_at, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN warehouses w ON w.id = o.warehouse_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (check_no) DO NOTHING;

-- 包裹 2 + 包裹明细 3
INSERT INTO packing_records (package_no, outbound_no, packing_material, length, width, height, weight, volume,
                             carrier, tracking_no, warehouse_id, idempotency_key, remark, created_at, created_by)
SELECT v.package_no, v.outbound_no, v.material, v.len, v.wid, v.hei, v.weight, v.len * v.wid * v.hei / 1000000,
       v.carrier, v.tracking_no, w.id, 'dev-seed-' || v.package_no, 'DEV SEED', v.created_at, 9903
FROM (VALUES ('BP-20261003-000001', 'OUT-20261003-000001', '五层瓦楞纸箱 4 号', 35.0000::numeric(18, 4), 25.0000::numeric(18, 4), 15.0000::numeric(18, 4), 2.6000::numeric(18, 4), '顺丰速运', 'SF1380000123456', '2026-10-04 15:40:00+08'::timestamptz),
             ('BP-20261004-000002', 'OUT-20261004-000002', '五层瓦楞纸箱 3 号', 30.0000::numeric(18, 4), 20.0000::numeric(18, 4), 12.0000::numeric(18, 4), 1.9000::numeric(18, 4), '中通快递', 'ZT7583001234', '2026-10-05 10:55:00+08'::timestamptz)) AS v(package_no, outbound_no, material, len, wid, hei, weight, carrier, tracking_no, created_at)
JOIN warehouses w ON w.id = (SELECT o.warehouse_id FROM outbound_orders o WHERE o.outbound_no = v.outbound_no)
ON CONFLICT (package_no) DO NOTHING;

INSERT INTO packing_items (package_id, outbound_id, line_no, qty, remark, created_at, created_by)
SELECT p.id, o.id, v.line_no, v.qty, '', p.created_at, 9903
FROM (VALUES ('BP-20261003-000001', 'OUT-20261003-000001', 1, 12.0000::numeric(18, 4)),
             ('BP-20261003-000001', 'OUT-20261003-000001', 2, 20.0000::numeric(18, 4)),
             ('BP-20261004-000002', 'OUT-20261004-000002', 1, 10.0000::numeric(18, 4))) AS v(package_no, outbound_no, line_no, qty)
JOIN packing_records p ON p.package_no = v.package_no
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
ON CONFLICT DO NOTHING;

-- 发货单 2（SHIPPED：发货即 Deduct 正式扣减——存量快照已扣，见 10.6 口径）
INSERT INTO shipments (shipment_no, outbound_no, carrier, tracking_no, warehouse_id, shipper_id, shipper_name,
                       package_count, status, shipped_at, idempotency_key, remark, created_at, created_by)
SELECT v.shipment_no, v.outbound_no, v.carrier, v.tracking_no, w.id, 9903, '陈发货',
       1, 'SHIPPED', v.shipped_at, 'dev-seed-' || v.shipment_no, 'DEV SEED', v.created_at, 9903
FROM (VALUES ('SH-20261003-000001', 'OUT-20261003-000001', '顺丰速运', 'SF1380000123456', '2026-10-04 16:30:00+08'::timestamptz, '2026-10-04 16:00:00+08'::timestamptz),
             ('SH-20261004-000002', 'OUT-20261004-000002', '中通快递', 'ZT7583001234', '2026-10-05 11:20:00+08'::timestamptz, '2026-10-05 11:00:00+08'::timestamptz)) AS v(shipment_no, outbound_no, carrier, tracking_no, shipped_at, created_at)
JOIN warehouses w ON w.id = (SELECT o.warehouse_id FROM outbound_orders o WHERE o.outbound_no = v.outbound_no)
ON CONFLICT (shipment_no) DO NOTHING;

-- ---- 11.6 调拨 2（完成 / 待收）----
INSERT INTO transfer_orders (transfer_no, type, from_warehouse_id, to_warehouse_id, status, approved_by, approved_at,
                             outbound_at, received_at, remark, created_at, created_by, updated_by)
SELECT v.transfer_no, 'WAREHOUSE', fw.id, tw.id, v.status, 9901, v.approved_at, v.outbound_at, v.received_at,
       v.remark, v.created_at, 9901, 9901
FROM (VALUES ('TR-20261004-000001', 'WH-E01', 'WH-E02', 'COMPLETED',
              '2026-10-04 10:00:00+08'::timestamptz, '2026-10-04 14:00:00+08'::timestamptz, '2026-10-05 09:00:00+08'::timestamptz,
              'SMT 产线领料: 电阻拨至半成品仓', '2026-10-04 09:40:00+08'::timestamptz),
             ('TR-20261005-000002', 'WH-E01', 'WH-E02', 'AWAITING_RECEIPT',
              '2026-10-05 13:00:00+08'::timestamptz, '2026-10-05 15:00:00+08'::timestamptz, NULL::timestamptz,
              'MCU 调拨续批', '2026-10-05 12:40:00+08'::timestamptz)) AS v(transfer_no, from_wh, to_wh, status, approved_at, outbound_at, received_at, remark, created_at)
JOIN warehouses fw ON fw.code = v.from_wh AND fw.deleted_at IS NULL
JOIN warehouses tw ON tw.code = v.to_wh AND tw.deleted_at IS NULL
ON CONFLICT (transfer_no) DO NOTHING;

INSERT INTO transfer_items (transfer_id, line_no, sku_id, batch_id, from_warehouse_id, from_zone_id, from_shelf_id,
                            from_bin_id, to_warehouse_id, to_zone_id, to_shelf_id, to_bin_id, qty, qty_out, qty_in,
                            created_at, created_by)
SELECT t.id, v.line_no, sku.id, COALESCE(bat.id, 0), fw.id, fb.zone_id, fb.shelf_id, fb.id,
       tw.id, tb.zone_id, tb.shelf_id, tb.id, v.qty, v.qty_out, v.qty_in, v.created_at, 9901
FROM (VALUES ('TR-20261004-000001', 1, 'SKU-E001-01', 'B20260928-E001', 'WH-E01', 'REEL-01-11', 'WH-E02', 'SR-01-11', 5.0000::numeric(18, 4), 5.0000::numeric(18, 4), 5.0000::numeric(18, 4), '2026-10-04 09:40:00+08'::timestamptz),
             ('TR-20261005-000002', 1, 'SKU-E003-01', 'B20260915-E003', 'WH-E01', 'REEL-02-12', 'WH-E02', 'SR-01-11', 100.0000::numeric(18, 4), 100.0000::numeric(18, 4), 0.0000::numeric(18, 4), '2026-10-05 12:40:00+08'::timestamptz)) AS v(transfer_no, line_no, sku_code, batch_no, from_wh, from_bin, to_wh, to_bin, qty, qty_out, qty_in, created_at)
JOIN transfer_orders t ON t.transfer_no = v.transfer_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
JOIN warehouses fw ON fw.code = v.from_wh AND fw.deleted_at IS NULL
JOIN bins fb ON fb.code = v.from_bin AND fb.warehouse_id = fw.id
JOIN warehouses tw ON tw.code = v.to_wh AND tw.deleted_at IS NULL
JOIN bins tb ON tb.code = v.to_bin AND tb.warehouse_id = tw.id
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT DO NOTHING;

-- ---- 11.7 盘点 2（待差异审核 / 盘点中）----
INSERT INTO count_orders (count_no, warehouse_id, scope, status, frozen_at, reviewed_at, completed_at, remark, created_at, created_by)
SELECT v.count_no, w.id, v.scope, v.status, v.frozen_at, v.reviewed_at, v.completed_at, v.remark, v.created_at, 9904
FROM (VALUES ('CK-20261004-000001', 'WH-E01', '{"type": "BIN", "bins": ["REEL-01-11", "IC-01-11", "PK-01-11"]}'::jsonb, 'PENDING_REVIEW',
              '2026-10-04 09:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz,
              '循环盘点: MCU 账实差 -4 待审核（差异必须走调整单）', '2026-10-04 08:40:00+08'::timestamptz),
             ('CK-20261005-000002', 'WH-E01', '{"type": "BIN", "bins": ["REEL-02-11", "REEL-02-21"]}'::jsonb, 'COUNTING',
              '2026-10-05 08:30:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz,
              '电容/锡膏循环盘点中', '2026-10-05 08:20:00+08'::timestamptz)) AS v(count_no, wh_code, scope, status, frozen_at, reviewed_at, completed_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (count_no) DO NOTHING;

INSERT INTO count_items (count_id, inventory_row_id, sku_id, warehouse_id, zone_id, shelf_id, bin_id,
                         qty_system, qty_counted, counted_by, counted_at, serial_no, created_at, created_by)
SELECT c.id, i.id, sku.id, i.warehouse_id, i.zone_id, i.shelf_id, i.bin_id,
       i.available_qty, v.qty_counted,
       CASE WHEN v.qty_counted IS NOT NULL THEN 9904 ELSE 0 END,
       v.counted_at, '', c.created_at, 9904
FROM (VALUES ('CK-20261004-000001', 'REEL-01-11', 'SKU-E001-01', 30.0000::numeric(18, 4), '2026-10-04 10:30:00+08'::timestamptz),
             ('CK-20261004-000001', 'IC-01-11', 'SKU-E004-01', 796.0000::numeric(18, 4), '2026-10-04 10:50:00+08'::timestamptz),
             ('CK-20261004-000001', 'PK-01-11', 'SKU-E006-01', 400.0000::numeric(18, 4), '2026-10-04 11:10:00+08'::timestamptz),
             ('CK-20261005-000002', 'REEL-02-11', 'SKU-E002-01', 25.0000::numeric(18, 4), '2026-10-05 09:10:00+08'::timestamptz),
             ('CK-20261005-000002', 'REEL-02-21', 'SKU-E010-01', NULL::numeric(18, 4), NULL::timestamptz)) AS v(count_no, bin_code, sku_code, qty_counted, counted_at)
JOIN count_orders c ON c.count_no = v.count_no
JOIN inventory i ON i.bin_id = (SELECT b.id FROM bins b WHERE b.code = v.bin_code AND b.warehouse_id = c.warehouse_id)
                AND i.sku_id = (SELECT sku.id FROM skus sku WHERE sku.code = v.sku_code)
                AND i.warehouse_id = c.warehouse_id
JOIN skus sku ON sku.id = i.sku_id
ON CONFLICT DO NOTHING;

INSERT INTO count_differences (count_id, line_no, sku_id, warehouse_id, bin_id, batch_id, qty_system, qty_counted,
                               diff_qty, adjust_no, status, remark, created_at, created_by)
SELECT c.id, 1, sku.id, c.warehouse_id, i.bin_id, i.batch_id, 800.0000::numeric(18, 4), 796.0000::numeric(18, 4),
       -4.0000::numeric(18, 4), '', 'PENDING', '疑少 4 颗, 待审核后走调整单（inventory-rules §9 禁止直改库存）', c.frozen_at, 9904
FROM count_orders c
JOIN skus sku ON sku.code = 'SKU-E004-01'
JOIN inventory i ON i.warehouse_id = c.warehouse_id
                AND i.sku_id = sku.id
                AND i.bin_id = (SELECT b.id FROM bins b WHERE b.code = 'IC-01-11' AND b.warehouse_id = c.warehouse_id)
WHERE c.count_no = 'CK-20261004-000001'
ON CONFLICT DO NOTHING;

-- ---- 11.8 打印模板 2 / 打印任务 2（任务行含 data_id 身份快照，000019）----
INSERT INTO print_templates (id, name, object_type, paper, barcode_symbology, qrcode_enabled, fields,
                             header_text, status, remark, created_by)
VALUES (9991, 'SKU 二维码标准标签', 'SKU_LABEL', 'THERMAL_60_40', 'CODE128', TRUE,
        '{"sku_code": "SKU 编码", "product_name": "商品名称", "spec": "规格", "barcode": "主条码"}'::jsonb,
        '泰克威电子', 'ENABLED', 'DEV SEED 二维码中心默认模板', 0),
       (9992, '库位标签', 'BIN_LABEL', 'THERMAL_40_30', 'CODE128', TRUE,
        '{"bin_code": "库位编码", "warehouse_name": "仓库"}'::jsonb,
        '', 'ENABLED', 'DEV SEED 库位标识', 0)
ON CONFLICT DO NOTHING;

INSERT INTO print_tasks (print_no, object_type, template_id, template_snapshot, paper, copies, total_count,
                         status, result, printed_by, printed_at, created_at, created_by)
SELECT v.print_no, v.object_type, t.id, jsonb_build_object('name', t.name, 'object_type', t.object_type,
                       'paper', t.paper, 'barcode_symbology', t.barcode_symbology, 'qrcode_enabled', t.qrcode_enabled,
                       'fields', t.fields, 'header_text', t.header_text),
       t.paper, v.copies, v.total, v.status, v.result,
       CASE WHEN v.result IS NOT NULL THEN 9901 ELSE 0 END,
       CASE WHEN v.result IS NOT NULL THEN v.printed_at ELSE NULL END,
       v.created_at, 9901
FROM (VALUES ('PT-20261004-000001', 'SKU_LABEL', 'SKU 二维码标准标签', 2, 2, 'SUCCESS', 'SUCCESS'::varchar, '2026-10-04 17:00:00+08'::timestamptz, '2026-10-04 16:40:00+08'::timestamptz),
             ('PT-20261005-000002', 'BIN_LABEL', '库位标签', 1, 3, 'QUEUED', NULL::varchar, NULL::timestamptz, '2026-10-05 16:00:00+08'::timestamptz)) AS v(print_no, object_type, template_name, copies, total, status, result, printed_at, created_at)
JOIN print_templates t ON t.name = v.template_name
ON CONFLICT (print_no) DO NOTHING;

INSERT INTO print_task_rows (id, task_id, seq, code, data_id, values, created_at, created_by)
SELECT v.id, t.id, v.seq, v.code, v.data_id,
       jsonb_build_object('sku_code', v.code, 'product_name', v.product_name, 'spec', v.spec, 'barcode', v.barcode),
       t.created_at, 9901
FROM (VALUES (9995, 'PT-20261004-000001', 1, 'SKU-E013-01', '9436', '智能温控器 STC-2000', '白色, NTC/继电器, 220V', '6901234000368'),
             (9996, 'PT-20261004-000001', 2, 'SKU-E014-01', '9438', 'WiFi 智能插座 SP-10', '16A, 支持 Alexa/小爱', '6901234000382'),
             (9997, 'PT-20261005-000002', 1, 'REEL-01-11', '9970', '元器件存储区', '', ''),
             (9998, 'PT-20261005-000002', 2, 'REEL-02-11', '9974', '元器件存储区', '', ''),
             (9999, 'PT-20261005-000002', 3, 'FG-01-11', '9992', '成品存储区', '', '')) AS v(id, print_no, seq, code, data_id, product_name, spec, barcode)
JOIN print_tasks t ON t.print_no = v.print_no
ON CONFLICT DO NOTHING;

-- ---- 11.9 单号计数器推进（GREATEST 幂等：重跑不回退既有最大值，服务后续取号无缝衔接）----
INSERT INTO doc_number_counters (prefix, period, next_no, created_at, updated_at, created_by)
VALUES ('PO', '20261002', 2, now(), now(), 0), ('PO', '20261003', 3, now(), now(), 0),
       ('PO', '20261004', 4, now(), now(), 0), ('PO', '20261005', 5, now(), now(), 0),
       ('IN', '20261002', 2, now(), now(), 0), ('IN', '20261003', 3, now(), now(), 0),
       ('RC', '20261002', 4, now(), now(), 0), ('RC', '20261003', 2, now(), now(), 0),
       ('QC', '20261002', 2, now(), now(), 0), ('QC', '20261003', 2, now(), now(), 0),
       ('PW', '20261003', 4, now(), now(), 0), ('PW', '20261004', 2, now(), now(), 0),
       ('SO', '20261003', 2, now(), now(), 0), ('SO', '20261004', 3, now(), now(), 0),
       ('SO', '20261005', 4, now(), now(), 0),
       ('OUT', '20261003', 2, now(), now(), 0), ('OUT', '20261004', 3, now(), now(), 0),
       ('PK', '20261003', 3, now(), now(), 0), ('PK', '20261004', 3, now(), now(), 0),
       ('CH', '20261003', 3, now(), now(), 0), ('CH', '20261004', 2, now(), now(), 0),
       ('BP', '20261003', 2, now(), now(), 0), ('BP', '20261004', 3, now(), now(), 0),
       ('SH', '20261003', 2, now(), now(), 0), ('SH', '20261004', 3, now(), now(), 0),
       ('TR', '20261004', 2, now(), now(), 0), ('TR', '20261005', 3, now(), now(), 0),
       ('CK', '20261004', 2, now(), now(), 0), ('CK', '20261005', 3, now(), now(), 0),
       ('PT', '20261004', 2, now(), now(), 0), ('PT', '20261005', 3, now(), now(), 0)
ON CONFLICT (prefix, period) DO UPDATE SET next_no = GREATEST(doc_number_counters.next_no, EXCLUDED.next_no);

-- ---- 11.10 审批记录（document_approvals append-only；NOT EXISTS 防重跑重复）----
INSERT INTO document_approvals (target_type, target_no, action, result, opinion, operator_id, operator_name, created_at)
SELECT v.target_type, v.target_no, v.action, v.result, v.opinion, v.operator_id, v.operator_name, v.created_at
FROM (VALUES ('purchase_order', 'PO-20261002-000001', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-02 09:08:00+08'::timestamptz),
             ('purchase_order', 'PO-20261002-000001', 'APPROVE', 'APPROVED', '同意, 按预算执行', 9901, '王经理', '2026-10-02 09:10:00+08'::timestamptz),
             ('purchase_order', 'PO-20261003-000002', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-03 09:55:00+08'::timestamptz),
             ('purchase_order', 'PO-20261003-000002', 'APPROVE', 'APPROVED', '同意', 9901, '王经理', '2026-10-03 10:00:00+08'::timestamptz),
             ('purchase_order', 'PO-20261004-000003', 'SUBMIT', '', 'PCB 批量板请审批', 9901, '王经理', '2026-10-04 16:02:00+08'::timestamptz),
             ('sales_order', 'SO-20261003-000001', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-03 11:05:00+08'::timestamptz),
             ('sales_order', 'SO-20261003-000001', 'APPROVE', 'APPROVED', '同意, 库存充足', 9901, '王经理', '2026-10-03 11:20:00+08'::timestamptz),
             ('sales_order', 'SO-20261004-000002', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-04 10:00:00+08'::timestamptz),
             ('sales_order', 'SO-20261004-000002', 'APPROVE', 'APPROVED', '同意, 部分现货先发', 9901, '王经理', '2026-10-04 10:10:00+08'::timestamptz),
             ('sales_order', 'SO-20261005-000003', 'SUBMIT', '', '样机单请审批', 9901, '王经理', '2026-10-05 14:05:00+08'::timestamptz),
             ('transfer_order', 'TR-20261004-000001', 'SUBMIT', '', '产线领料申请', 9901, '王经理', '2026-10-04 09:45:00+08'::timestamptz),
             ('transfer_order', 'TR-20261004-000001', 'APPROVE', 'APPROVED', '同意', 9901, '王经理', '2026-10-04 10:00:00+08'::timestamptz),
             ('transfer_order', 'TR-20261005-000002', 'SUBMIT', '', 'MCU 调拨申请', 9901, '王经理', '2026-10-05 12:45:00+08'::timestamptz),
             ('transfer_order', 'TR-20261005-000002', 'APPROVE', 'APPROVED', '同意', 9901, '王经理', '2026-10-05 13:00:00+08'::timestamptz)) AS v(target_type, target_no, action, result, opinion, operator_id, operator_name, created_at)
WHERE NOT EXISTS (
    SELECT 1 FROM document_approvals da
    WHERE da.target_type = v.target_type AND da.target_no = v.target_no
      AND da.action = v.action AND da.created_at = v.created_at);

-- ============ 12) 演示数据补全轮（2026-10-06：全部页面 + 全部状态情况） ============
--
-- 目的：让每个前端页面都有可展示数据、每个业务状态机都有样例。新增段全部沿用 §8/§10 的
--   幂等口径（显式 9xxx 主键或自然键 ON CONFLICT DO NOTHING、单事务内），与既有数据零冲突
--   （新单号 / 新主键 / 新五维键）。
-- 覆盖：库存四态（锁定/冻结/待检/残次）+ 库存锁定 + 库存调整单 + 退货（销售/采购）+
--   异常九类 + 设备与扫码 + 文件/导入导出 + 定时任务执行日志/通知/备份 + 各单据缺失状态。
-- 边界（有意留白）：审计表 operation_logs / login_logs 按既有硬性约束零写入
--   （dev_seed_test.go TestDevSeedNoProductionInitPollution）——日志页数据由真实登录/操作产生；
--   files / backup_records 为登记型记录，其物理文件不在演示库（下载端 404 属预期）。

-- ---- 12.1 库存状态演示行（locked/frozen/pending_inspect/defective 四态齐备）----
-- §8/§10 期初段只写 total/available 两列；本段为「状态行」，六列全写并满足恒等式
--   total = available + locked + frozen + pending_inspect + defective（inventory-rules §2）。
-- 每行仍与一条 '期初' 流水 1:1 成对（qty=total、0→n），保证「库存变更必带流水」口径；
-- 状态流转明细由 §12.2 锁定记录承载（锁定的 qty 与本段 locked/frozen 量一一对应）。
-- 选键原则：避开 §8/§10 已占五维键与序列号管理 SKU（E008-01/E013-01/E013-02/E014-01），
--   避免扰动期初成对与「序列号一物一行」断言。
INSERT INTO inventory (id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
                       total_qty, available_qty, locked_qty, frozen_qty, pending_inspect_qty, defective_qty, created_by)
SELECT v.id, w.id, z.id, s.id, b.id, sku.id, COALESCE(bat.id, 0),
       v.qty, v.avail, v.locked, v.frozen, v.pending, v.defect, 0
FROM (VALUES (9843, 'WH-E01', 'REEL-01-22', 'SKU-E001-01', 'B20260928-E001', 50.0000::numeric(18, 4), 30.0000::numeric(18, 4), 20.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             (9844, 'WH-E01', 'RV-01-11', 'SKU-E004-01', NULL, 300.0000::numeric(18, 4), 240.0000::numeric(18, 4), 0.0000::numeric(18, 4), 60.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             (9845, 'WH-E01', 'RV-01-12', 'SKU-E012-01', 'B20260918-E012', 60.0000::numeric(18, 4), 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 20.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             (9846, 'WH-E01', 'NG-01-11', 'SKU-E011-01', NULL, 30.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 30.0000::numeric(18, 4)),
             (9847, 'WH-E03', 'FG-01-12', 'SKU-E015-01', 'B20261002-E015', 40.0000::numeric(18, 4), 25.0000::numeric(18, 4), 15.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             (9848, 'WH-E03', 'FG-02-12', 'SKU-E015-01', 'B20261002-E015', 200.0000::numeric(18, 4), 150.0000::numeric(18, 4), 0.0000::numeric(18, 4), 50.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty, avail, locked, frozen, pending, defect)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (warehouse_id, bin_id, sku_id, batch_id) DO NOTHING;

-- 状态行的期初流水（与上方同 VALUES 成对；id 9901 起避开 §8/§10 已用 9851-9900）
INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, COALESCE(bat.id, 0),
       'INBOUND', '期初', 'DEV-SEED-OPEN-' || v.id::text, 'available', 'available',
       0, v.qty, v.qty,
       0, 'dev-seed', 'dev-seed', 'DEV SEED', '2026-10-05 10:00:00+08'::timestamptz
FROM (VALUES (9901, 'WH-E01', 'REEL-01-22', 'SKU-E001-01', 'B20260928-E001', 50.0000::numeric(18, 4)),
             (9902, 'WH-E01', 'RV-01-11', 'SKU-E004-01', NULL, 300.0000::numeric(18, 4)),
             (9903, 'WH-E01', 'RV-01-12', 'SKU-E012-01', 'B20260918-E012', 60.0000::numeric(18, 4)),
             (9904, 'WH-E01', 'NG-01-11', 'SKU-E011-01', NULL, 30.0000::numeric(18, 4)),
             (9905, 'WH-E03', 'FG-01-12', 'SKU-E015-01', 'B20261002-E015', 40.0000::numeric(18, 4)),
             (9906, 'WH-E03', 'FG-02-12', 'SKU-E015-01', 'B20261002-E015', 200.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (ledger_no) DO NOTHING;

-- ---- 12.2 库存锁定（5 种 lock_type × 3 种 status 全值域覆盖）----
-- ACTIVE 锁的 qty 与 §12.1 状态行的 locked/frozen/pending_inspect 量一一对应（数据自洽）；
-- RELEASED/CONSUMED 为历史锁（已释放/已核销，不影响当前现存量）。
INSERT INTO inventory_locks (id, warehouse_id, bin_id, sku_id, batch_id, lock_type, source_type, source_no,
                             qty, status, released_at, released_by, remark, created_at, created_by)
SELECT v.id, w.id, b.id, sku.id, COALESCE(bat.id, 0), v.lock_type, v.source_type, v.source_no,
       v.qty, v.status, v.released_at, CASE WHEN v.released_at IS NULL THEN 0 ELSE 9901 END,
       v.remark, v.created_at, 9901
FROM (VALUES
    -- ACTIVE：销售预占（ORDER_HOLD → locked 20）
    (9910, 'WH-E01', 'REEL-01-22', 'SKU-E001-01', 'B20260928-E001', 'ORDER_HOLD', 'SALES_ORDER', 'SO-20261004-000002', 20.0000::numeric(18, 4), 'ACTIVE', NULL::timestamptz, 'DEV SEED 销售单预占（对应库存行 locked_qty=20）', '2026-10-05 11:00:00+08'::timestamptz),
    -- ACTIVE：手工冻结（MANUAL_FREEZE → frozen 60）
    (9911, 'WH-E01', 'RV-01-11', 'SKU-E004-01', NULL, 'MANUAL_FREEZE', 'MANUAL', 'MF-20261005-000001', 60.0000::numeric(18, 4), 'ACTIVE', NULL::timestamptz, 'DEV SEED 手工冻结待工艺确认（对应库存行 frozen_qty=60）', '2026-10-05 11:20:00+08'::timestamptz),
    -- ACTIVE：质检冻结（QC_FREEZE → pending_inspect 20）
    (9912, 'WH-E01', 'RV-01-12', 'SKU-E012-01', 'B20260918-E012', 'QC_FREEZE', 'QUALITY', 'QC-20261003-000001', 20.0000::numeric(18, 4), 'ACTIVE', NULL::timestamptz, 'DEV SEED 收货待检批次暂存锁定（对应库存行 pending_inspect_qty=20）', '2026-10-05 11:40:00+08'::timestamptz),
    -- ACTIVE：销售预占（成品仓，locked 15）
    (9913, 'WH-E03', 'FG-01-12', 'SKU-E015-01', 'B20261002-E015', 'ORDER_HOLD', 'SALES_ORDER', 'SO-20261005-000003', 15.0000::numeric(18, 4), 'ACTIVE', NULL::timestamptz, 'DEV SEED 样机单预占（对应库存行 locked_qty=15）', '2026-10-05 15:10:00+08'::timestamptz),
    -- ACTIVE：异常冻结（EXCEPTION_FREEZE → frozen 50；freeze_lock_id 回指见 §13.5 异常单）
    (9914, 'WH-E03', 'FG-02-12', 'SKU-E015-01', 'B20261002-E015', 'EXCEPTION_FREEZE', 'EXCEPTION', 'EX-20261006-000001', 50.0000::numeric(18, 4), 'ACTIVE', NULL::timestamptz, 'DEV SEED 彩盒受潮异常冻结（对应库存行 frozen_qty=50）', '2026-10-06 09:20:00+08'::timestamptz),
    -- RELEASED：盘点冻结已解冻（盘点完成）
    (9915, 'WH-E01', 'REEL-02-12', 'SKU-E003-01', 'B20260915-E003', 'COUNT_FREEZE', 'COUNT', 'CK-20261004-000001', 100.0000::numeric(18, 4), 'RELEASED', '2026-10-04 12:00:00+08'::timestamptz, 'DEV SEED 盘点冻结已解冻', '2026-10-04 09:00:00+08'::timestamptz),
    -- RELEASED：异常冻结已解冻
    (9916, 'WH-E01', 'IC-01-11', 'SKU-E004-01', NULL, 'EXCEPTION_FREEZE', 'EXCEPTION', 'EX-20261004-000002', 30.0000::numeric(18, 4), 'RELEASED', '2026-10-05 09:00:00+08'::timestamptz, 'DEV SEED 异常处理完成解冻', '2026-10-04 16:30:00+08'::timestamptz),
    -- RELEASED：手工冻结已解冻
    (9917, 'WH-E02', 'SA-01-11', 'SKU-E008-01', 'B20261004-E008', 'MANUAL_FREEZE', 'MANUAL', 'MF-20261004-000002', 5.0000::numeric(18, 4), 'RELEASED', '2026-10-05 10:00:00+08'::timestamptz, 'DEV SEED 半成品待检已放行', '2026-10-04 15:00:00+08'::timestamptz),
    -- CONSUMED：销售预占已核销（出库消耗）
    (9918, 'WH-E03', 'FG-01-11', 'SKU-E013-01', NULL, 'ORDER_HOLD', 'SALES_ORDER', 'SO-20261003-000001', 12.0000::numeric(18, 4), 'CONSUMED', '2026-10-04 16:30:00+08'::timestamptz, 'DEV SEED 预占随出库核销', '2026-10-03 11:30:00+08'::timestamptz),
    -- CONSUMED：销售预占部分核销
    (9919, 'WH-E03', 'FG-01-22', 'SKU-E014-01', NULL, 'ORDER_HOLD', 'SALES_ORDER', 'SO-20261004-000002', 10.0000::numeric(18, 4), 'CONSUMED', '2026-10-05 11:20:00+08'::timestamptz, 'DEV SEED 部分发货核销', '2026-10-04 10:20:00+08'::timestamptz)) AS v(id, wh_code, bin_code, sku_code, batch_no, lock_type, source_type, source_no, qty, status, released_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT DO NOTHING;

-- ---- 12.3 库存调整单（6 种状态 × 5 种调整类型覆盖）----
-- 单号走 docnum ADJ 前缀真实格式（ResetAll：ADJ-{YYYYMMDD}-{6 位流水}），计数器见 §12.9。
-- EXECUTED 行对应盘点差异 CK-20261004-000001 的 -4 盘亏（count_differences.adjust_no 回写见 §12.10）。
INSERT INTO inventory_adjustments (id, adjustment_no, warehouse_id, sku_id, bin_id, batch_id, adjust_type, qty,
                                   reason, status, approved_by, approved_at, executed_by, executed_at,
                                   created_at, created_by, updated_by)
SELECT v.id, v.adjustment_no, w.id, sku.id, b.id, COALESCE(bat.id, 0), v.adjust_type, v.qty,
       v.reason, v.status,
       CASE WHEN v.status IN ('APPROVED', 'EXECUTED') THEN 9901 ELSE 0 END, v.approved_at,
       CASE WHEN v.status = 'EXECUTED' THEN 9904 ELSE 0 END, v.executed_at,
       v.created_at, 9904, 9904
FROM (VALUES
    -- EXECUTED：盘点差异核销（盘亏 4）
    (9930, 'ADJ-20261004-000001', 'WH-E01', 'SKU-E004-01', 'IC-01-11', NULL, '盘亏', 4.0000::numeric(18, 4), 'CK-20261004-000001 循环盘点账实差 -4，审核通过后核销', 'EXECUTED', '2026-10-04 14:00:00+08'::timestamptz, '2026-10-04 14:30:00+08'::timestamptz, '2026-10-04 13:00:00+08'::timestamptz),
    -- PENDING_APPROVAL：盘盈待审
    (9931, 'ADJ-20261005-000002', 'WH-E01', 'SKU-E006-01', 'PK-01-11', 'B20260926-E006', '盘盈', 12.0000::numeric(18, 4), '收货尾数溢装 12，待审核后入账', 'PENDING_APPROVAL', NULL::timestamptz, NULL::timestamptz, '2026-10-05 09:30:00+08'::timestamptz),
    -- PENDING_APPROVAL：报废待审
    (9932, 'ADJ-20261005-000003', 'WH-E01', 'SKU-E011-01', 'NG-01-11', NULL, '报废', 30.0000::numeric(18, 4), '钢网变形 30 张，隔离区报废申请', 'PENDING_APPROVAL', NULL::timestamptz, NULL::timestamptz, '2026-10-05 10:10:00+08'::timestamptz),
    -- APPROVED：损耗已审待执行
    (9933, 'ADJ-20261005-000004', 'WH-E01', 'SKU-E003-01', 'REEL-02-12', 'B20260915-E003', '损耗', 6.0000::numeric(18, 4), 'MCU 静电损伤损耗 6，审核通过待执行', 'APPROVED', '2026-10-05 11:00:00+08'::timestamptz, NULL::timestamptz, '2026-10-05 10:40:00+08'::timestamptz),
    -- REJECTED：盘盈驳回
    (9934, 'ADJ-20261005-000005', 'WH-E02', 'SKU-E008-01', 'SA-01-11', 'B20261004-E008', '盘盈', 8.0000::numeric(18, 4), '半成品仓疑似盘盈 8，核查为未登记入库已驳回', 'REJECTED', '2026-10-05 15:00:00+08'::timestamptz, NULL::timestamptz, '2026-10-05 14:30:00+08'::timestamptz),
    -- DRAFT：草稿
    (9935, 'ADJ-20261006-000006', 'WH-E03', 'SKU-E015-02', 'FP-01-11', NULL, '其他', 50.0000::numeric(18, 4), '彩盒受潮 50 个待定处置方式（草稿）', 'DRAFT', NULL::timestamptz, NULL::timestamptz, '2026-10-06 09:40:00+08'::timestamptz),
    -- EXECUTED：报废已执行
    (9936, 'ADJ-20261004-000007', 'WH-E01', 'SKU-E012-01', 'REEL-02-22', 'B20260918-E012', '报废', 4.0000::numeric(18, 4), 'ESD 托盘破损报废 4，已执行', 'EXECUTED', '2026-10-04 17:00:00+08'::timestamptz, '2026-10-04 17:20:00+08'::timestamptz, '2026-10-04 16:40:00+08'::timestamptz),
    -- CANCELLED：作废
    (9937, 'ADJ-20261003-000008', 'WH-E01', 'SKU-E007-01', 'PK-01-12', NULL, '盘亏', 20.0000::numeric(18, 4), 'FPC 排线疑似盘亏，复盘后确认账实相符，作废', 'CANCELLED', NULL::timestamptz, NULL::timestamptz, '2026-10-03 16:00:00+08'::timestamptz)) AS v(id, adjustment_no, wh_code, sku_code, bin_code, batch_no, adjust_type, qty, reason, status, approved_at, executed_at, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT DO NOTHING;

-- ---- 13.1 销售退货单（type=SALES，8 态中覆盖 7 态；SHIPPED 仅采购退货适用）----
INSERT INTO return_orders (id, return_no, type, source_no, customer_id, supplier_id, warehouse_id, status,
                           approved_by, approved_at, received_at, qc_at, completed_at, cancelled_at,
                           remark, created_at, created_by, updated_by)
SELECT v.id, v.return_no, 'SALES', v.source_no, cus.id, 0, w.id, v.status,
       CASE WHEN v.status IN ('APPROVED', 'RECEIVING', 'IN_QC', 'COMPLETED') THEN 9901 ELSE 0 END,
       v.approved_at, v.received_at, v.qc_at, v.completed_at, v.cancelled_at,
       v.remark, v.created_at, 9903, 9903
FROM (VALUES
    (9950, 'RT-20261006-000001', 'SO-20261004-000002', 'CUS-E002', 'WH-E03', 'DRAFT', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '客户反馈 1 台白温控器外观瑕疵（草稿）', '2026-10-06 16:10:00+08'::timestamptz),
    (9951, 'RT-20261006-000002', 'SO-20261004-000002', 'CUS-E002', 'WH-E03', 'PENDING_APPROVAL', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '插座 2 台不通电待审核', '2026-10-06 16:40:00+08'::timestamptz),
    (9952, 'RT-20261006-000003', 'SO-20261003-000001', 'CUS-E001', 'WH-E03', 'APPROVED', '2026-10-06 17:20:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '温控器 2 台待客户退回', '2026-10-06 17:00:00+08'::timestamptz),
    (9953, 'RT-20261005-000001', 'SO-20261003-000001', 'CUS-E001', 'WH-E03', 'RECEIVING', '2026-10-06 08:40:00+08'::timestamptz, '2026-10-05 09:30:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '客户退回包裹已到仓，清点中', '2026-10-05 08:20:00+08'::timestamptz),
    (9954, 'RT-20261005-000002', 'SO-20261003-000001', 'CUS-E001', 'WH-E03', 'IN_QC', '2026-10-05 09:00:00+08'::timestamptz, '2026-10-05 09:40:00+08'::timestamptz, '2026-10-06 10:10:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '退货质检中（正常/不良判定）', '2026-10-05 08:50:00+08'::timestamptz),
    (9955, 'RT-20261004-000001', 'SO-20261003-000001', 'CUS-E001', 'WH-E03', 'COMPLETED', '2026-10-04 10:00:00+08'::timestamptz, '2026-10-04 14:00:00+08'::timestamptz, '2026-10-04 15:00:00+08'::timestamptz, '2026-10-04 16:00:00+08'::timestamptz, NULL::timestamptz, '退货入库完成，良品回架、不良转隔离', '2026-10-04 09:40:00+08'::timestamptz),
    (9956, 'RT-20261004-000002', 'SO-20261004-000002', 'CUS-E002', 'WH-E03', 'CANCELLED', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '2026-10-04 18:00:00+08'::timestamptz, '客户撤销退货申请，单作废', '2026-10-04 17:30:00+08'::timestamptz)) AS v(id, return_no, source_no, cus_code, wh_code, status, approved_at, received_at, qc_at, completed_at, cancelled_at, remark, created_at)
JOIN customers cus ON cus.code = v.cus_code AND cus.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 13.2 采购退货单（type=PURCHASE，覆盖 APPROVED / SHIPPED / COMPLETED）----
INSERT INTO return_orders (id, return_no, type, source_no, customer_id, supplier_id, warehouse_id, status,
                           approved_by, approved_at, received_at, qc_at, completed_at, cancelled_at,
                           remark, created_at, created_by, updated_by)
SELECT v.id, v.return_no, 'PURCHASE', v.source_no, 0, sup.id, w.id, v.status, 9901, v.approved_at,
       NULL::timestamptz, NULL::timestamptz, v.completed_at, NULL::timestamptz,
       v.remark, v.created_at, 9902, 9902
FROM (VALUES
    (9957, 'RT-20261004-000003', 'PO-20261002-000001', 'SUP-E001', 'WH-E01', 'APPROVED', '2026-10-04 11:00:00+08'::timestamptz, NULL::timestamptz, '来料丝印不良 20 盘待退回供应商', '2026-10-04 10:40:00+08'::timestamptz),
    (9958, 'RT-20261005-000003', 'PO-20261002-000001', 'SUP-E001', 'WH-E01', 'SHIPPED', '2026-10-05 09:00:00+08'::timestamptz, NULL::timestamptz, '不良物料已交快递退回', '2026-10-05 08:40:00+08'::timestamptz),
    (9959, 'RT-20261003-000001', 'PO-20261002-000001', 'SUP-E001', 'WH-E01', 'COMPLETED', '2026-10-03 10:00:00+08'::timestamptz, '2026-10-03 16:00:00+08'::timestamptz, '退供应商完成，供应商已确认收货', '2026-10-03 09:30:00+08'::timestamptz)) AS v(id, return_no, source_no, sup_code, wh_code, status, approved_at, completed_at, remark, created_at)
JOIN suppliers sup ON sup.code = v.sup_code AND sup.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 13.3 退货明细（各退货单 1-2 行；四量随状态推进）----
INSERT INTO return_items (id, return_id, line_no, sku_id, qty_return, qty_received, qty_inspected, qty_defective,
                          reason, remark, created_at, created_by)
SELECT v.id, r.id, v.line_no, sku.id, v.qty_return, v.qty_received, v.qty_inspected, v.qty_defective,
       v.reason, '', r.created_at, 9903
FROM (VALUES (9950, 'RT-20261006-000001', 1, 'SKU-E013-01', 1.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '外观瑕疵'),
             (9951, 'RT-20261006-000002', 1, 'SKU-E014-01', 2.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '通电异常'),
             (9952, 'RT-20261006-000003', 1, 'SKU-E013-01', 2.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '客户拒收'),
             (9953, 'RT-20261005-000001', 1, 'SKU-E013-01', 2.0000::numeric(18, 4), 2.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '客户拒收'),
             (9954, 'RT-20261005-000002', 1, 'SKU-E015-01', 5.0000::numeric(18, 4), 5.0000::numeric(18, 4), 5.0000::numeric(18, 4), 1.0000::numeric(18, 4), '来料彩盒破损'),
             (9955, 'RT-20261004-000001', 1, 'SKU-E013-01', 3.0000::numeric(18, 4), 3.0000::numeric(18, 4), 3.0000::numeric(18, 4), 0.0000::numeric(18, 4), '客户退换'),
             (9960, 'RT-20261004-000001', 2, 'SKU-E015-01', 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 2.0000::numeric(18, 4), '包装破损'),
             (9956, 'RT-20261004-000002', 1, 'SKU-E014-01', 1.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '客户撤销'),
             (9957, 'RT-20261004-000003', 1, 'SKU-E001-01', 20.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '丝印不良'),
             (9958, 'RT-20261005-000003', 1, 'SKU-E001-01', 20.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '丝印不良'),
             (9959, 'RT-20261003-000001', 1, 'SKU-E002-01', 5.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '来料容值偏差')) AS v(id, return_no, line_no, sku_code, qty_return, qty_received, qty_inspected, qty_defective, reason)
JOIN return_orders r ON r.return_no = v.return_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 13.4 异常单（九类异常 × 六态生命周期全覆盖，20 条）----
-- handle_records 追加式台账（action：freeze/assign/start/review/resolve/close/release）；
-- 9950+ 中的 EX-20261006-000001 回指 §12.2 的 EXCEPTION_FREEZE 锁 9914（异常冻结联动）。
INSERT INTO exceptions (id, exception_no, type, source_type, source_no, sku_id, bin_id, serial_no, status, detail,
                        assignee_id, assignee_name, owner_id, owner_name, handle_records, image_refs, freeze_lock_id,
                        assigned_at, resolved_at, closed_at, remark, created_at, created_by, updated_by)
SELECT v.id, v.exception_no, v.type, v.source_type, v.source_no,
       COALESCE(sku.id, 0), COALESCE(b.id, 0), v.serial_no, v.status, v.detail,
       v.assignee_id, v.assignee_name, v.owner_id, v.owner_name,
       v.handle_records::jsonb, v.image_refs::jsonb, v.freeze_lock_id,
       v.assigned_at, v.resolved_at, v.closed_at, v.remark, v.created_at, 9904, 9904
FROM (VALUES
    (9970, 'EX-20261006-000001', '库存异常', 'COUNT', 'CK-20261004-000001', 'SKU-E015-01', 'FG-02-12', '', 'PROCESSING', '成品彩盒受潮 50 个，已冻结待处置', 9902, '李收货', 9901, '王经理', '[{"at": "2026-10-06 09:20:00", "action": "freeze", "by_id": 9904, "by_name": "赵盘点", "note": "彩盒受潮冻结 50", "lock_id": 9914, "qty": "50.0000"}, {"at": "2026-10-06 09:30:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派李收货跟进"}, {"at": "2026-10-06 09:45:00", "action": "start", "by_id": 9902, "by_name": "李收货", "note": "联系供应商换货"}]', '[]', 9914, '2026-10-05 09:30:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, 'DEV SEED 异常冻结联动示例', '2026-10-06 09:20:00+08'::timestamptz),
    (9971, 'EX-20261006-000002', '收货异常', 'INBOUND', 'IN-20261003-000002', 'SKU-E006-01', 'RV-01-11', '', 'OPEN', '到货连接器外箱破损 1 箱，待处理', 0, '', 0, '', '[]', '[]', NULL::bigint, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-06 10:00:00+08'::timestamptz),
    (9972, 'EX-20261006-000003', '收货异常', 'INBOUND', 'IN-20261003-000002', 'SKU-E006-02', 'RV-01-12', '', 'ASSIGNED', '到货数量短装 10 个', 9902, '李收货', 9901, '王经理', '[{"at": "2026-10-05 14:10:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派李收货核实"}]', '[]', NULL::bigint, '2026-10-05 14:10:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 14:00:00+08'::timestamptz),
    (9973, 'EX-20261005-000001', '质检异常', 'QUALITY', 'QC-20261003-000001', 'SKU-E006-01', 'PK-01-11', '', 'PROCESSING', '抽检发现 3 个端子氧化，处理中', 9904, '赵盘点', 9902, '李收货', '[{"at": "2026-10-05 15:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派质检跟进"}, {"at": "2026-10-05 15:20:00", "action": "start", "by_id": 9904, "by_name": "赵盘点", "note": "全检排查同批次"}]', '[]', NULL::bigint, '2026-10-05 15:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 14:50:00+08'::timestamptz),
    (9974, 'EX-20261005-000002', '质检异常', 'QUALITY', 'QC-20261002-000001', 'SKU-E003-01', 'REEL-02-12', '', 'PENDING_REVIEW', 'MCU 湿敏等级需烘烤，已提交复核', 9904, '赵盘点', 9902, '李收货', '[{"at": "2026-10-05 16:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派质检"}, {"at": "2026-10-05 16:20:00", "action": "start", "by_id": 9904, "by_name": "赵盘点", "note": "烘烤方案拟定"}, {"at": "2026-10-05 17:00:00", "action": "review", "by_id": 9904, "by_name": "赵盘点", "note": "提交复核"}]', '[]', NULL::bigint, '2026-10-05 16:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 15:50:00+08'::timestamptz),
    (9975, 'EX-20261004-000001', '上架异常', 'PUTAWAY', 'PW-20261003-000001', 'SKU-E001-01', 'REEL-01-11', '', 'RESOLVED', '目标库位容量不足，已改派相邻库位', 9902, '李收货', 9902, '李收货', '[{"at": "2026-10-04 10:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派上架处理"}, {"at": "2026-10-04 10:20:00", "action": "start", "by_id": 9902, "by_name": "李收货", "note": "查找空位"}, {"at": "2026-10-04 10:50:00", "action": "review", "by_id": 9902, "by_name": "李收货", "note": "改派 REEL-01-22"}, {"at": "2026-10-04 11:10:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "确认上架完成"}]', '[]', NULL::bigint, '2026-10-04 10:00:00+08'::timestamptz, '2026-10-04 11:10:00+08'::timestamptz, NULL::timestamptz, '', '2026-10-04 09:50:00+08'::timestamptz),
    (9976, 'EX-20261004-000002', '上架异常', 'PUTAWAY', 'PW-20261003-000002', 'SKU-E002-01', 'REEL-02-11', '', 'CLOSED', '货架标签脱落，已补打并关闭', 9902, '李收货', 9902, '李收货', '[{"at": "2026-10-04 11:30:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派处理"}, {"at": "2026-10-04 11:40:00", "action": "start", "by_id": 9902, "by_name": "李收货", "note": "补打标签"}, {"at": "2026-10-04 12:00:00", "action": "review", "by_id": 9902, "by_name": "李收货", "note": "提交复核"}, {"at": "2026-10-04 12:10:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "复核通过"}, {"at": "2026-10-04 12:20:00", "action": "close", "by_id": 9901, "by_name": "王经理", "note": "关闭"}]', '[]', NULL::bigint, '2026-10-04 11:30:00+08'::timestamptz, '2026-10-04 12:10:00+08'::timestamptz, '2026-10-04 12:20:00+08'::timestamptz, '', '2026-10-04 11:20:00+08'::timestamptz),
    (9977, 'EX-20261004-000003', '库存异常', 'COUNT', 'CK-20261004-000001', 'SKU-E004-01', 'IC-01-11', '', 'RESOLVED', '循环盘点账实差 -4，已生成调整单核销', 9904, '赵盘点', 9901, '王经理', '[{"at": "2026-10-04 13:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派盘点复核"}, {"at": "2026-10-04 13:20:00", "action": "start", "by_id": 9904, "by_name": "赵盘点", "note": "复盘确认差异"}, {"at": "2026-10-04 13:50:00", "action": "review", "by_id": 9904, "by_name": "赵盘点", "note": "提交复核"}, {"at": "2026-10-04 14:00:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "生成 ADJ-20261004-000001"}]', '[]', NULL::bigint, '2026-10-04 13:00:00+08'::timestamptz, '2026-10-04 14:00:00+08'::timestamptz, NULL::timestamptz, '', '2026-10-04 12:50:00+08'::timestamptz),
    (9978, 'EX-20261005-000003', '拣货异常', 'PICK', 'PK-20261004-000002', 'SKU-E015-01', 'FG-02-11', '', 'ASSIGNED', '拣货位库存不足，缺 5 个', 9903, '陈发货', 9903, '陈发货', '[{"at": "2026-10-05 10:20:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派发货组处理"}]', '[]', NULL::bigint, '2026-10-05 10:20:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 10:10:00+08'::timestamptz),
    (9979, 'EX-20261004-000004', '拣货异常', 'PICK', 'PK-20261003-000002', 'SKU-E015-01', 'FG-02-11', '', 'CLOSED', '拣货位标签与实物不符，已更正关闭', 9903, '陈发货', 9903, '陈发货', '[{"at": "2026-10-04 15:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派处理"}, {"at": "2026-10-04 15:20:00", "action": "start", "by_id": 9903, "by_name": "陈发货", "note": "核对实物"}, {"at": "2026-10-04 15:50:00", "action": "review", "by_id": 9903, "by_name": "陈发货", "note": "更正标签"}, {"at": "2026-10-04 16:10:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "复核通过"}, {"at": "2026-10-04 16:20:00", "action": "close", "by_id": 9901, "by_name": "王经理", "note": "关闭"}]', '[]', NULL::bigint, '2026-10-04 15:00:00+08'::timestamptz, '2026-10-04 16:10:00+08'::timestamptz, '2026-10-04 16:20:00+08'::timestamptz, '', '2026-10-04 14:50:00+08'::timestamptz),
    (9980, 'EX-20261005-000004', '复核异常', 'CHECK', 'CH-20261004-000001', 'SKU-E014-01', 'FG-01-22', '', 'PENDING_REVIEW', '复核发现串码，已提交复核', 9903, '陈发货', 9903, '陈发货', '[{"at": "2026-10-05 11:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派复核组"}, {"at": "2026-10-05 11:20:00", "action": "start", "by_id": 9903, "by_name": "陈发货", "note": "追查串码批次"}, {"at": "2026-10-05 12:00:00", "action": "review", "by_id": 9903, "by_name": "陈发货", "note": "提交复核"}]', '[]', NULL::bigint, '2026-10-05 11:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 10:50:00+08'::timestamptz),
    (9981, 'EX-20261003-000001', '复核异常', 'CHECK', 'CH-20261003-000001', 'SKU-E013-01', 'FG-01-11', '', 'CLOSED', '数量复核差异 1 台，已核实并关闭', 9903, '陈发货', 9903, '陈发货', '[{"at": "2026-10-03 16:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派处理"}, {"at": "2026-10-03 16:20:00", "action": "start", "by_id": 9903, "by_name": "陈发货", "note": "二次点数"}, {"at": "2026-10-03 16:50:00", "action": "review", "by_id": 9903, "by_name": "陈发货", "note": "确认无差异"}, {"at": "2026-10-03 17:10:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "复核通过"}, {"at": "2026-10-03 17:20:00", "action": "close", "by_id": 9901, "by_name": "王经理", "note": "关闭"}]', '[]', NULL::bigint, '2026-10-03 16:00:00+08'::timestamptz, '2026-10-03 17:10:00+08'::timestamptz, '2026-10-03 17:20:00+08'::timestamptz, '', '2026-10-03 15:50:00+08'::timestamptz),
    (9982, 'EX-20261005-000005', '物流异常', 'SHIPMENT', 'SH-20261004-000001', 'SKU-E015-01', '', '', 'OPEN', '快递揽收后轨迹停滞 24h，待跟进', 0, '', 0, '', '[]', '[]', NULL::bigint, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 09:00:00+08'::timestamptz),
    (9983, 'EX-20261005-000006', '物流异常', 'SHIPMENT', 'SH-20261004-000002', 'SKU-E014-01', '', '', 'PROCESSING', '客户反馈外箱凹陷，处理中', 9903, '陈发货', 9903, '陈发货', '[{"at": "2026-10-05 13:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派发货组"}, {"at": "2026-10-05 13:20:00", "action": "start", "by_id": 9903, "by_name": "陈发货", "note": "联系快递理赔"}]', '[]', NULL::bigint, '2026-10-05 13:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 12:50:00+08'::timestamptz),
    (9984, 'EX-20261005-000007', '盘点异常', 'COUNT', 'CK-20261005-000002', 'SKU-E010-01', 'REEL-02-21', '', 'PENDING_REVIEW', '盘点发现锡膏批次已过效期，提交复核', 9904, '赵盘点', 9901, '王经理', '[{"at": "2026-10-05 09:30:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派盘点处理"}, {"at": "2026-10-05 09:50:00", "action": "start", "by_id": 9904, "by_name": "赵盘点", "note": "核查效期"}, {"at": "2026-10-05 10:20:00", "action": "review", "by_id": 9904, "by_name": "赵盘点", "note": "提交复核"}]', '[]', NULL::bigint, '2026-10-05 09:30:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-05 09:20:00+08'::timestamptz),
    (9985, 'EX-20261004-000005', '盘点异常', 'COUNT', 'CK-20261004-000001', 'SKU-E006-01', 'PK-01-11', '', 'RESOLVED', '盘点时发现库位混放，已整理归位', 9904, '赵盘点', 9904, '赵盘点', '[{"at": "2026-10-04 11:30:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派整理"}, {"at": "2026-10-04 11:40:00", "action": "start", "by_id": 9904, "by_name": "赵盘点", "note": "整理库位"}, {"at": "2026-10-04 12:10:00", "action": "review", "by_id": 9904, "by_name": "赵盘点", "note": "提交复核"}, {"at": "2026-10-04 12:30:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "确认归位"}]', '[]', NULL::bigint, '2026-10-04 11:30:00+08'::timestamptz, '2026-10-04 12:30:00+08'::timestamptz, NULL::timestamptz, '', '2026-10-04 11:20:00+08'::timestamptz),
    (9986, 'EX-20261006-000004', '系统异常', 'SYSTEM', '', '', '', '', 'OPEN', '扫码解析接口偶发超时，待排查', 0, '', 0, '', '[]', '[]', NULL::bigint, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-06 11:00:00+08'::timestamptz),
    (9987, 'EX-20261004-000006', '系统异常', 'SYSTEM', '', '', '', '', 'CLOSED', '导出任务队列积压，已扩容关闭', 9901, '王经理', 9901, '王经理', '[{"at": "2026-10-04 18:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "自行跟进"}, {"at": "2026-10-04 18:10:00", "action": "start", "by_id": 9901, "by_name": "王经理", "note": "扩容队列"}, {"at": "2026-10-04 18:30:00", "action": "review", "by_id": 9901, "by_name": "王经理", "note": "提交复核"}, {"at": "2026-10-04 18:40:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "队列恢复正常"}, {"at": "2026-10-04 18:50:00", "action": "close", "by_id": 9901, "by_name": "王经理", "note": "关闭"}]', '[]', NULL::bigint, '2026-10-04 18:00:00+08'::timestamptz, '2026-10-04 18:40:00+08'::timestamptz, '2026-10-04 18:50:00+08'::timestamptz, '', '2026-10-04 17:50:00+08'::timestamptz),
    (9988, 'EX-20261003-000002', '库存异常', 'MANUAL', '', 'SKU-E011-01', 'NG-01-11', '', 'ASSIGNED', '钢网变形 30 张待报废处置', 9904, '赵盘点', 9901, '王经理', '[{"at": "2026-10-03 15:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派盘点确认数量"}]', '[]', NULL::bigint, '2026-10-03 15:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '', '2026-10-03 14:50:00+08'::timestamptz),
    (9989, 'EX-20261002-000001', '收货异常', 'INBOUND', 'IN-20261002-000001', 'SKU-E003-01', 'RV-01-11', '', 'CLOSED', '到货外箱受潮，已开箱全检并关闭', 9902, '李收货', 9902, '李收货', '[{"at": "2026-10-02 15:00:00", "action": "assign", "by_id": 9901, "by_name": "王经理", "note": "指派全检"}, {"at": "2026-10-02 15:20:00", "action": "start", "by_id": 9902, "by_name": "李收货", "note": "开箱全检"}, {"at": "2026-10-02 16:00:00", "action": "review", "by_id": 9902, "by_name": "李收货", "note": "全检无异常"}, {"at": "2026-10-02 16:20:00", "action": "resolve", "by_id": 9901, "by_name": "王经理", "note": "复核通过"}, {"at": "2026-10-02 16:30:00", "action": "close", "by_id": 9901, "by_name": "王经理", "note": "关闭"}]', '[]', NULL::bigint, '2026-10-02 15:00:00+08'::timestamptz, '2026-10-02 16:20:00+08'::timestamptz, '2026-10-02 16:30:00+08'::timestamptz, '', '2026-10-02 14:50:00+08'::timestamptz)) AS v(id, exception_no, type, source_type, source_no, sku_code, bin_code, serial_no, status, detail, assignee_id, assignee_name, owner_id, owner_name, handle_records, image_refs, freeze_lock_id, assigned_at, resolved_at, closed_at, remark, created_at)
LEFT JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN bins b ON b.code = v.bin_code
ON CONFLICT DO NOTHING;

-- ---- 14.1 设备档案（5 种 type × 启停/激活状态覆盖）----
-- code 为管理端命名（devices.md §6：SF-{类型}-{序号}），非 docnum 单号；warehouse_id/bound_user_id
-- 为裸 ID 引用（9103=WH-E01 / 9104=WH-E02 / 9105=WH-E03；9901-9905 为演示账号）。
INSERT INTO devices (id, code, name, type, brand, model, os, warehouse_id, bound_user_id, status,
                     activation_status, activation_expires_at, activated_at, activated_by, app_version,
                     last_online_at, last_scan_at, battery_level, ip, token_version, remark,
                     created_at, created_by, updated_by)
VALUES
    (9001, 'SF-PC-001', '办公 PC-01', 'pc', 'Lenovo', 'ThinkCentre M720', 'Windows 11', 9103, 9901, 'ENABLED', 'ACTIVATED', NULL, '2026-10-02 09:00:00+08', 9901, '', '2026-10-06 11:30:00+08', NULL, NULL, '10.20.1.11', 1, 'DEV SEED 管理端固定终端', '2026-10-02 09:00:00+08', 0, 0),
    (9002, 'SF-PAD-001', '平板-收货台', 'pad', 'Samsung', 'Galaxy Tab A9+', 'Android 14', 9103, 9902, 'ENABLED', 'ACTIVATED', NULL, '2026-10-03 09:10:00+08', 9902, '1.2.0', '2026-10-06 11:28:00+08', '2026-10-06 11:25:00+08', 78, '10.20.2.21', 1, 'DEV SEED 收货作业平板', '2026-10-03 09:10:00+08', 0, 0),
    (9003, 'SF-PDA-001', 'PDA-拣货01', 'pda', 'Zebra', 'MC3300x', 'Android 11', 9105, 9903, 'ENABLED', 'ACTIVATED', NULL, '2026-10-03 09:20:00+08', 9903, '1.2.0', '2026-10-06 11:29:00+08', '2026-10-06 11:27:00+08', 64, '10.20.3.31', 2, 'DEV SEED 拣货工业 PDA', '2026-10-03 09:20:00+08', 0, 0),
    (9004, 'SF-PDA-002', 'PDA-上架02', 'pda', 'Zebra', 'MC3300x', 'Android 11', 9103, 0, 'ENABLED', 'PENDING', '2026-10-06 12:30:00+08', NULL, 0, '', NULL, NULL, NULL, '', 1, 'DEV SEED 待激活 PDA', '2026-10-06 11:00:00+08', 0, 0),
    (9005, 'SF-SCAN-001', '扫码枪-复核台', 'scanner', 'Honeywell', 'HH660', '', 9105, 9903, 'ENABLED', 'ACTIVATED', NULL, '2026-10-03 09:30:00+08', 9903, '', '2026-10-06 11:26:00+08', '2026-10-06 11:20:00+08', NULL, '10.20.3.41', 1, 'DEV SEED 复核台扫码枪', '2026-10-03 09:30:00+08', 0, 0),
    (9006, 'SF-SCAN-002', '扫码枪-备用', 'scanner', 'Honeywell', 'HH660', '', 0, 0, 'DISABLED', 'ACTIVATED', NULL, '2026-10-02 10:00:00+08', 9901, '', '2026-09-28 09:00:00+08', NULL, NULL, '', 3, 'DEV SEED 已停用备用设备', '2026-10-02 10:00:00+08', 0, 0),
    (9007, 'SF-PRT-001', '标签打印机-01', 'printer', 'Zebra', 'ZD421', '', 9103, 9902, 'ENABLED', 'ACTIVATED', NULL, '2026-10-03 09:40:00+08', 9902, '', '2026-10-06 10:50:00+08', NULL, NULL, '10.20.2.51', 1, 'DEV SEED 热敏标签打印机', '2026-10-03 09:40:00+08', 0, 0),
    (9008, 'SF-PRT-002', '标签打印机-02', 'printer', 'Zebra', 'ZD421', '', 9105, 0, 'ENABLED', 'PENDING', '2026-10-06 13:00:00+08', NULL, 0, '', NULL, NULL, NULL, '', 1, 'DEV SEED 待激活打印机', '2026-10-06 11:10:00+08', 0, 0)
ON CONFLICT DO NOTHING;

-- ---- 14.2 设备配置（device_id 一机一配置；下发项 devices.md §7.3 九键）----
INSERT INTO device_configs (id, device_id, config, version, created_at, created_by, updated_by)
VALUES (9001, 9002, '{"scan_mode": "camera", "sound": true, "vibrate": true, "auto_focus": true, "continuous_scan": false, "scan_timeout_seconds": 30, "default_warehouse_id": 9103, "task_refresh_seconds": 60, "auto_lock_minutes": 10}'::jsonb, 3, '2026-10-05 09:00:00+08', 9901, 9901),
       (9002, 9003, '{"scan_mode": "hardware", "sound": true, "vibrate": true, "auto_focus": false, "continuous_scan": true, "scan_timeout_seconds": 20, "default_warehouse_id": 9105, "task_refresh_seconds": 30, "auto_lock_minutes": 0}'::jsonb, 5, '2026-10-05 09:10:00+08', 9901, 9901),
       (9003, 9005, '{"scan_mode": "hid", "sound": false, "vibrate": false, "auto_focus": false, "continuous_scan": true, "scan_timeout_seconds": 10, "default_warehouse_id": 9105, "task_refresh_seconds": 60, "auto_lock_minutes": 0}'::jsonb, 2, '2026-10-05 09:20:00+08', 9901, 9901),
       (9004, 9007, '{"scan_mode": "hid", "sound": true, "vibrate": false, "auto_focus": false, "continuous_scan": false, "scan_timeout_seconds": 15, "default_warehouse_id": 9103, "task_refresh_seconds": 120, "auto_lock_minutes": 0}'::jsonb, 1, '2026-10-05 09:30:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

-- ---- 14.3 设备运行日志（INFO/WARN/ERROR 三级别；append-only 设备端上报）----
INSERT INTO device_logs (id, device_id, level, event_type, message, context, occurred_at, created_at)
VALUES
    (9001, 9002, 'INFO', 'BOOT', '设备启动完成，版本 1.2.0', '{"boot_ms": 1850}'::jsonb, '2026-10-06 08:00:00+08', '2026-10-06 08:00:05+08'),
    (9002, 9002, 'INFO', 'CONFIG_SYNC', '配置已同步至版本 3', '{"config_version": 3}'::jsonb, '2026-10-06 08:01:00+08', '2026-10-06 08:01:02+08'),
    (9003, 9002, 'WARN', 'BATTERY_LOW', '电量低于 20%，请及时充电', '{"battery": 18}'::jsonb, '2026-10-06 10:30:00+08', '2026-10-06 10:30:03+08'),
    (9004, 9003, 'INFO', 'SCAN', '扫码成功 6901234000368', '{"symbology": "EAN13"}'::jsonb, '2026-10-06 11:20:00+08', '2026-10-06 11:20:01+08'),
    (9005, 9003, 'ERROR', 'NETWORK', '网络请求超时，已重试 3 次', '{"endpoint": "/api/tasks", "timeout_ms": 5000}'::jsonb, '2026-10-06 11:22:00+08', '2026-10-06 11:22:30+08'),
    (9006, 9005, 'INFO', 'SCAN', '扫码成功 REEL-01-11', '{"symbology": "CODE128"}'::jsonb, '2026-10-06 11:20:00+08', '2026-10-06 11:20:01+08'),
    (9007, 9005, 'WARN', 'SCAN_SLOW', '单次识别耗时 820ms（阈值 500ms）', '{"duration_ms": 820}'::jsonb, '2026-10-06 11:21:00+08', '2026-10-06 11:21:01+08'),
    (9008, 9007, 'ERROR', 'PRINT', '打印任务 PT-20261005-000002 缺纸失败', '{"print_no": "PT-20261005-000002"}'::jsonb, '2026-10-05 16:05:00+08', '2026-10-05 16:05:10+08')
ON CONFLICT DO NOTHING;

-- ---- 14.4 扫码审计（成功/失败、设备/Web HID 双轨；device_id NULL = Web HID 场景）----
INSERT INTO scan_logs (id, device_id, device_code, user_id, username, ip, warehouse_id, raw_code, symbology,
                       resolve_type, resolve_id, resolve_code, page, business_no, success, error_code, created_at)
VALUES
    (9001, 9003, 'SF-PDA-001', 9903, 'dev_shipper', '10.20.3.31', 9105, '6901234000368', 'EAN13', 'sku', 9436, 'SKU-E013-01', '/picking', 'OUT-20261004-000002', TRUE, '', '2026-10-06 11:20:00+08'),
    (9002, 9005, 'SF-SCAN-001', 9903, 'dev_shipper', '10.20.3.41', 9105, 'REEL-01-11', 'CODE128', 'bin', 9970, 'REEL-01-11', '/checking', 'CH-20261004-000001', TRUE, '', '2026-10-06 11:20:30+08'),
    (9003, 9003, 'SF-PDA-001', 9903, 'dev_shipper', '10.20.3.31', 9105, 'OUT-20261004-000002', 'CODE128', 'doc', 0, 'OUT-20261004-000002', '/picking', 'OUT-20261004-000002', TRUE, '', '2026-10-06 11:21:00+08'),
    (9004, 9002, 'SF-PAD-001', 9902, 'dev_receiver', '10.20.2.21', 9103, '6901234000214', 'EAN13', 'sku', 9421, 'SKU-E001-01', '/pad/receive', 'IN-20261003-000002', TRUE, '', '2026-10-06 11:22:00+08'),
    (9005, 9002, 'SF-PAD-001', 9902, 'dev_receiver', '10.20.2.21', 9103, 'B20260928-E001', 'CODE128', 'batch', 9601, 'B20260928-E001', '/pad/receive', 'IN-20261003-000002', TRUE, '', '2026-10-06 11:22:20+08'),
    (9006, 9002, 'SF-PAD-001', 9902, 'dev_receiver', '10.20.2.21', 9103, 'SN-D001-0001', 'CODE128', 'serial', 9701, 'SN-D001-0001', '/pad/receive', '', TRUE, '', '2026-10-06 11:22:40+08'),
    (9007, 9002, 'SF-PAD-001', 9902, 'dev_receiver', '10.20.2.21', 9103, '9999999999999', 'EAN13', '', 0, '', '/pad/receive', '', FALSE, 'BARCODE_NOT_FOUND', '2026-10-06 11:23:00+08'),
    (9008, NULL, NULL, 9901, 'dev_manager', '10.20.1.11', 9103, 'IN-20261003-000002', 'CODE128', 'doc', 0, 'IN-20261003-000002', '/inbound', 'IN-20261003-000002', TRUE, '', '2026-10-06 11:24:00+08'),
    (9009, NULL, NULL, 9901, 'dev_manager', '10.20.1.11', 9103, 'BAD-CODE-0001', '', '', 0, '', '/inventory/stock', '', FALSE, 'UNKNOWN_BARCODE', '2026-10-06 11:25:00+08'),
    (9010, 9003, 'SF-PDA-001', 9903, 'dev_shipper', '10.20.3.31', 9105, 'CK-20261005-000002', 'CODE128', 'doc', 0, 'CK-20261005-000002', '/counts', 'CK-20261005-000002', TRUE, '', '2026-10-06 11:26:00+08')
ON CONFLICT DO NOTHING;

-- ---- 14.5 App 版本（DRAFT/PUBLISHED/DEPRECATED 三态；架构预留表）----
INSERT INTO app_versions (id, platform, version_code, version_name, release_notes, file_url, min_supported_code,
                          force_update, status, published_at, created_at, created_by, updated_by)
VALUES (9001, 'android', 100, '1.0.0', '首个正式版：收货/上架/拣货/复核/盘点五作业流', 'https://demo.dev/apk/stockflow-scan-1.0.0.apk', 100, FALSE, 'DEPRECATED', '2026-09-01 10:00:00+08', '2026-09-01 09:00:00+08', 9901, 9901),
       (9002, 'android', 120, '1.2.0', '新增连续扫码与离线任务缓存', 'https://demo.dev/apk/stockflow-scan-1.2.0.apk', 100, FALSE, 'PUBLISHED', '2026-10-01 10:00:00+08', '2026-10-01 09:00:00+08', 9901, 9901),
       (9003, 'android', 130, '1.3.0', '扫码性能优化（草稿，待发布）', '', 120, FALSE, 'DRAFT', NULL, '2026-10-06 10:00:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

-- ---- 15.1 文件中心登记（各业务模块覆盖；物理文件不在演示库，下载端 404 属预期）----
INSERT INTO files (id, file_name, stored_name, storage_path, mime_type, file_type, size_bytes, module, business_no,
                   uploader_id, uploader_name, expires_at, created_at, created_by, updated_by)
VALUES
    (9001, '商品导入模板.xlsx', 'a1b2c3d4e5f60718293a4b5c6d7e8f90.xlsx', '202610/a1b2c3d4e5f60718293a4b5c6d7e8f90.xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'xlsx', 18432, 'PRODUCT', '', 9901, '王经理', NULL, '2026-10-04 09:00:00+08', 9901, 9901),
    (9002, '商品导入_错误明细.xlsx', 'b2c3d4e5f60718293a4b5c6d7e8f9012.xlsx', '202610/b2c3d4e5f60718293a4b5c6d7e8f9012.xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'xlsx', 9216, 'PRODUCT', 'IMP-20261004-000001', 9901, '王经理', '2026-11-03 09:00:00+08', '2026-10-04 09:05:00+08', 9901, 9901),
    (9003, '库存导出_20261005.xlsx', 'c3d4e5f60718293a4b5c6d7e8f901234.xlsx', '202610/c3d4e5f60718293a4b5c6d7e8f901234.xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'xlsx', 65536, 'INVENTORY', 'EXP-20261005-000001', 9901, '王经理', '2026-11-04 10:00:00+08', '2026-10-05 10:00:00+08', 9901, 9901),
    (9004, '彩盒受潮取证.jpg', 'd4e5f60718293a4b5c6d7e8f90123456.jpg', '202610/d4e5f60718293a4b5c6d7e8f90123456.jpg', 'image/jpeg', 'jpg', 348160, 'EXCEPTION', 'EX-20261006-000001', 9904, '赵盘点', NULL, '2026-10-06 09:25:00+08', 9904, 9904),
    (9005, '供应商报价单.pdf', 'e5f60718293a4b5c6d7e8f9012345678.pdf', '202610/e5f60718293a4b5c6d7e8f9012345678.pdf', 'application/pdf', 'pdf', 512000, 'SUPPLIER', 'SUP-E003', 9901, '王经理', NULL, '2026-10-03 14:00:00+08', 9901, 9901),
    (9006, '来料检验标准.txt', 'f60718293a4b5c6d7e8f901234567890.txt', '202610/f60718293a4b5c6d7e8f901234567890.txt', 'text/plain; charset=utf-8', 'txt', 4096, 'QUALITY', 'QC-20261002-000001', 9904, '赵盘点', NULL, '2026-10-02 18:10:00+08', 9904, 9904),
    (9007, '设备离线排查记录.zip', '0718293a4b5c6d7e8f90123456789012.zip', '202610/0718293a4b5c6d7e8f90123456789012.zip', 'application/zip', 'zip', 1048576, 'devices', 'SF-PDA-002', 9901, '王经理', '2026-11-05 11:00:00+08', '2026-10-05 11:00:00+08', 9901, 9901),
    (9008, '销售订单导入模板.xlsx', '18293a4b5c6d7e8f9012345678901234.xlsx', '202610/18293a4b5c6d7e8f9012345678901234.xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'xlsx', 16384, 'SALES_ORDER', '', 9903, '陈发货', NULL, '2026-10-05 15:00:00+08', 9903, 9903)
ON CONFLICT DO NOTHING;

-- ---- 15.2 导入任务（六态覆盖；import_type 九值取样）----
INSERT INTO import_tasks (id, import_no, import_type, status, source_file_id, total_rows, valid_rows, error_rows,
                          success_rows, failed_rows, error_file_id, error_message, started_at, finished_at,
                          created_at, created_by, updated_by)
VALUES
    (9001, 'IMP-20261004-000001', 'PRODUCT', 'PARTIAL_SUCCESS', 9001, 50, 47, 3, 47, 0, 9002, '3 行校验失败（分类编码不存在）', '2026-10-04 09:05:00+08', '2026-10-04 09:06:20+08', '2026-10-04 09:00:00+08', 9901, 9901),
    (9002, 'IMP-20261005-000002', 'SUPPLIER', 'SUCCESS', 9005, 12, 12, 0, 12, 0, 0, '', '2026-10-05 10:10:00+08', '2026-10-05 10:10:40+08', '2026-10-05 10:05:00+08', 9901, 9901),
    (9003, 'IMP-20261005-000003', 'INITIAL_INVENTORY', 'VALIDATED', 9003, 30, 30, 0, 0, 0, 0, '', NULL, NULL, '2026-10-05 14:00:00+08', 9901, 9901),
    (9004, 'IMP-20261006-000004', 'SKU', 'PARSED', 9008, 25, 0, 0, 0, 0, 0, '', NULL, NULL, '2026-10-06 09:30:00+08', 9903, 9903),
    (9005, 'IMP-20261006-000005', 'SALES_ORDER', 'EXECUTING', 9008, 8, 8, 0, 5, 0, 0, '', '2026-10-06 10:00:00+08', NULL, '2026-10-06 09:50:00+08', 9903, 9903),
    (9006, 'IMP-20261006-000006', 'CUSTOMER', 'FAILED', 0, 0, 0, 0, 0, 0, 0, '文件解析失败：表头缺少「客户编码」列', NULL, '2026-10-06 10:20:00+08', '2026-10-06 10:15:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

INSERT INTO import_task_rows (id, task_id, row_no, raw, parsed, status, errors, batch_no, created_at, created_by, updated_by)
VALUES
    (9001, 9001, 1, '{"code": "P-NEW-001", "name": "演示新商品A", "category": "P-E01"}'::jsonb, '{"code": "P-NEW-001", "name": "演示新商品A"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-04 09:05:00+08', 9901, 9901),
    (9002, 9001, 2, '{"code": "P-NEW-002", "name": "演示新商品B", "category": "P-E01"}'::jsonb, '{"code": "P-NEW-002", "name": "演示新商品B"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-04 09:05:00+08', 9901, 9901),
    (9003, 9001, 3, '{"code": "P-NEW-003", "name": "演示新商品C", "category": "NOT-EXIST"}'::jsonb, '{"code": "P-NEW-003", "name": "演示新商品C"}'::jsonb, 'INVALID', '[{"row": 3, "column": "category", "message": "分类编码不存在"}]'::jsonb, 1, '2026-10-04 09:05:00+08', 9901, 9901),
    (9004, 9001, 4, '{"code": "P-NEW-004", "name": "演示新商品D", "category": "P-E01"}'::jsonb, '{"code": "P-NEW-004", "name": "演示新商品D"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-04 09:05:00+08', 9901, 9901),
    (9005, 9005, 1, '{"so_no": "SO-IMP-001", "customer": "CUS-E001", "sku": "SKU-E013-01", "qty": "5"}'::jsonb, '{"so_no": "SO-IMP-001", "qty": 5}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-06 10:00:00+08', 9903, 9903),
    (9006, 9005, 2, '{"so_no": "SO-IMP-001", "customer": "CUS-E001", "sku": "SKU-E015-01", "qty": "10"}'::jsonb, '{"so_no": "SO-IMP-001", "qty": 10}'::jsonb, 'QUEUED', NULL, 1, '2026-10-06 10:00:00+08', 9903, 9903),
    (9007, 9005, 3, '{"so_no": "SO-IMP-002", "customer": "CUS-E002", "sku": "SKU-E014-01", "qty": "3"}'::jsonb, '{"so_no": "SO-IMP-002", "qty": 3}'::jsonb, 'QUEUED', NULL, 1, '2026-10-06 10:00:00+08', 9903, 9903),
    (9008, 9003, 1, '{"wh": "WH-E01", "bin": "REEL-01-22", "sku": "SKU-E001-01", "qty": "50"}'::jsonb, '{"qty": 50}'::jsonb, 'VALID', NULL, 0, '2026-10-05 14:00:00+08', 9901, 9901),
    (9009, 9004, 1, '{"code": "SKU-NEW-001", "product": "P-NEW-001"}'::jsonb, '{"code": "SKU-NEW-001"}'::jsonb, 'RAW', NULL, 0, '2026-10-06 09:30:00+08', 9903, 9903),
    (9010, 9002, 1, '{"code": "SUP-NEW-001", "name": "演示供应商"}'::jsonb, '{"code": "SUP-NEW-001"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-05 10:10:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

-- ---- 15.3 导出任务（五态覆盖；module 十六值取样）----
INSERT INTO export_tasks (id, export_no, module, scope, params, status, progress, total_rows, file_id,
                          error_message, started_at, finished_at, created_at, created_by, updated_by)
VALUES
    (9001, 'EXP-20261005-000001', 'INVENTORY', 'BY_FILTER', '{"filters": {"warehouse_id": "9103"}}'::jsonb, 'SUCCESS', 100, 42, 9003, '', '2026-10-05 10:00:00+08', '2026-10-05 10:00:12+08', '2026-10-05 09:59:00+08', 9901, 9901),
    (9002, 'EXP-20261005-000002', 'INVENTORY_LEDGER', 'TIME_RANGE', '{"time_from": "2026-10-01 00:00:00", "time_to": "2026-10-05 23:59:59"}'::jsonb, 'SUCCESS', 100, 128, 9003, '', '2026-10-05 10:05:00+08', '2026-10-05 10:05:30+08', '2026-10-05 10:04:00+08', 9901, 9901),
    (9003, 'EXP-20261006-000003', 'EXCEPTION', 'ALL', '{}'::jsonb, 'PROCESSING', 60, 0, 0, '', '2026-10-06 11:00:00+08', NULL, '2026-10-06 10:59:00+08', 9904, 9904),
    (9004, 'EXP-20261006-000004', 'PURCHASE_ORDER', 'SELECTED', '{"ids": ["1", "2", "3"]}'::jsonb, 'QUEUED', 0, 0, 0, '', NULL, NULL, '2026-10-06 11:10:00+08', 9901, 9901),
    (9005, 'EXP-20261006-000005', 'REPORT', 'CURRENT_PAGE', '{"page": 1, "page_size": 50}'::jsonb, 'FAILED', 30, 0, 0, '导出中断：报表数据源超时', '2026-10-06 11:20:00+08', '2026-10-06 11:20:45+08', '2026-10-06 11:19:00+08', 9901, 9901),
    (9006, 'EXP-20261006-000006', 'TRANSFER', 'ALL', '{}'::jsonb, 'PARTIAL_SUCCESS', 100, 3, 9003, '部分行导出失败（1 行数据异常）', '2026-10-06 11:30:00+08', '2026-10-06 11:30:08+08', '2026-10-06 11:29:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

-- ---- 16.1 定时任务执行日志（五任务注册表；SCHEDULED/MANUAL/SKIPPED 三触发方式）----
-- scheduled_jobs 注册表行由服务启动 Scheduler.UpsertRegistry 幂等补齐（sysops/store.go），
-- 本段只补执行日志（scheduled_job_runs）——job_code 与冻结注册表同源。
INSERT INTO scheduled_job_runs (id, job_code, "trigger", start_at, end_at, success, duration_ms, message, created_at, created_by, updated_by)
VALUES
    (9001, 'inventory_low_stock_scan', 'SCHEDULED', '2026-10-06 11:00:00+08', '2026-10-06 11:00:02+08', TRUE, 2140, '扫描完成：低库存 3 条，新增通知 3 条', '2026-10-06 11:00:00+08', 0, 0),
    (9002, 'inventory_low_stock_scan', 'SCHEDULED', '2026-10-06 10:50:00+08', '2026-10-06 10:50:02+08', TRUE, 1980, '扫描完成：低库存 2 条，新增通知 2 条', '2026-10-06 10:50:00+08', 0, 0),
    (9003, 'inventory_expiry_scan', 'SCHEDULED', '2026-10-06 06:00:00+08', '2026-10-06 06:00:03+08', TRUE, 3260, '扫描完成：临期 2 批、已过期 1 批', '2026-10-06 06:00:00+08', 0, 0),
    (9004, 'inventory_stagnant_scan', 'SCHEDULED', '2026-10-06 06:00:05+08', '2026-10-06 06:00:07+08', TRUE, 2870, '扫描完成：积压 30 天档 1 条', '2026-10-06 06:00:05+08', 0, 0),
    (9005, 'task_timeout_scan', 'SCHEDULED', '2026-10-06 11:00:00+08', '2026-10-06 11:00:01+08', TRUE, 860, '扫描完成：拣货超时 1 条，已通知', '2026-10-06 11:00:00+08', 0, 0),
    (9006, 'file_cleanup', 'SCHEDULED', '2026-10-06 03:30:00+08', '2026-10-06 03:30:04+08', TRUE, 4120, '清理完成：过期文件 2 个', '2026-10-06 03:30:00+08', 0, 0),
    (9007, 'inventory_expiry_scan', 'MANUAL', '2026-10-05 15:00:00+08', '2026-10-05 15:00:03+08', TRUE, 3110, '手动触发：临期 3 批', '2026-10-05 15:00:00+08', 9901, 9901),
    (9008, 'file_cleanup', 'MANUAL', '2026-10-05 16:00:00+08', '2026-10-05 16:00:06+08', FALSE, 6020, '失败：存储目录不可写（/data/files/202609）', '2026-10-05 16:00:00+08', 9901, 9901),
    (9009, 'inventory_low_stock_scan', 'SKIPPED', '2026-10-05 16:10:00+08', '2026-10-05 16:10:00+08', FALSE, 0, '防重入跳过：上一周期仍在执行', '2026-10-05 16:10:00+08', 0, 0),
    (9010, 'task_timeout_scan', 'SCHEDULED', '2026-10-06 10:00:00+08', '2026-10-06 10:00:01+08', TRUE, 790, '扫描完成：无超时任务', '2026-10-06 10:00:00+08', 0, 0)
ON CONFLICT DO NOTHING;

-- ---- 16.2 站内通知（六类型 × 已读/未读；user_id 恒非空，dedup_key 复合收件人）----
INSERT INTO notifications (id, user_id, type, title, content, dedup_key, read, read_at, created_at, created_by, updated_by)
VALUES
    (9001, 9901, 'STOCK_ALERT', '低库存预警：SKU-E011-01', '电子原料仓 SKU-E011-01 可用量 0 低于安全库存 2，请及时补货', 'lowstock:20261006:SKU-E011-01:9103:9901', FALSE, NULL, '2026-10-06 11:00:02+08', 0, 0),
    (9002, 9901, 'STOCK_ALERT', '低库存预警：SKU-E010-01', '电子原料仓 SKU-E010-01 可用量 18 低于安全库存 10 的 2 倍线，请关注', 'lowstock:20261006:SKU-E010-01:9103:9901', TRUE, '2026-10-06 11:05:00+08', '2026-10-06 11:00:02+08', 0, 0),
    (9003, 9901, 'EXPIRY_ALERT', '效期预警：批次 B20260514-E010', '锡膏批次 B20260514-E010 已过期，请隔离处置', 'expiry:20261006:B20260514-E010:expired:9901', FALSE, NULL, '2026-10-06 06:00:03+08', 0, 0),
    (9004, 9902, 'EXPIRY_ALERT', '效期预警：批次 B20260918-E012', 'ESD 托盘批次 B20260918-E012 距到期 7 天，请优先使用', 'expiry:20261006:B20260918-E012:d7:9902', FALSE, NULL, '2026-10-06 06:00:03+08', 0, 0),
    (9005, 9901, 'EXCEPTION', '异常单待分派：EX-20261006-000002', '收货异常「到货连接器外箱破损 1 箱」待分派处理人', 'exception:EX-20261006-000002:9901', FALSE, NULL, '2026-10-06 10:00:00+08', 0, 0),
    (9006, 9902, 'EXCEPTION', '异常单已分派：EX-20261006-000001', '库存异常「成品彩盒受潮 50 个」已分派给你，请尽快处理', 'exception:EX-20261006-000001:9902', TRUE, '2026-10-06 09:40:00+08', '2026-10-06 09:30:00+08', 0, 0),
    (9007, 9901, 'APPROVAL', '待审批：采购订单 PO-20261004-000003', 'PCB V1.2 批量板 + 锡膏补库 待你审批', 'approval:PO-20261004-000003:9901', FALSE, NULL, '2026-10-04 16:02:00+08', 0, 0),
    (9008, 9901, 'APPROVAL', '待审批：销售订单 SO-20261005-000003', '样机 + 铺货 待你审批', 'approval:SO-20261005-000003:9901', TRUE, '2026-10-05 14:10:00+08', '2026-10-05 14:05:00+08', 0, 0),
    (9009, 9903, 'TASK', '拣货任务超时提醒', '拣货任务 PK-20261004-000002 已超过 4 小时未完成，请尽快处理', 'tasktimeout:PK-20261004-000002:9903', FALSE, NULL, '2026-10-06 11:00:01+08', 0, 0),
    (9010, 9902, 'TASK', '上架任务待领取', '入库单 IN-20261003-000002 有 1 条上架任务待领取', 'task:putaway:IN-20261003-000002:9902', TRUE, '2026-10-04 10:05:00+08', '2026-10-04 10:00:00+08', 0, 0),
    (9011, 9901, 'SYSTEM', '系统公告：版本升级通知', 'StockFlow 将于 2026-10-08 02:00 进行版本升级，预计停机 30 分钟', 'system:20261006:upgrade:9901', FALSE, NULL, '2026-10-06 09:00:00+08', 0, 0),
    (9012, 9905, 'SYSTEM', '欢迎使用 StockFlow', '演示账号已开通，可查看库存与报表数据', 'system:20261002:welcome:9905', TRUE, '2026-10-02 09:30:00+08', '2026-10-02 09:00:00+08', 0, 0)
ON CONFLICT DO NOTHING;

-- ---- 16.3 备份登记（REQUESTED/RUNNING/SUCCESS/FAILED 四态；混合模式）----
-- 注意：uk_backup_records_inflight 部分唯一索引限制同 trigger 至多 1 条在途
-- （REQUESTED/RUNNING）——AUTO 与 MANUAL 各留 1 条在途即上限。
INSERT INTO backup_records (id, file_name, file_path, size_bytes, "trigger", status, message,
                            started_at, finished_at, created_at, created_by, updated_by)
VALUES
    (9001, 'stockflow_20261004_023000.dump', 'backups/stockflow_20261004_023000.dump', 48234496, 'AUTO', 'SUCCESS', '自动备份成功', '2026-10-04 02:30:00+08', '2026-10-04 02:32:10+08', '2026-10-04 02:30:00+08', 0, 0),
    (9002, 'stockflow_20261005_023000.dump', 'backups/stockflow_20261005_023000.dump', 48992256, 'AUTO', 'SUCCESS', '自动备份成功', '2026-10-05 02:30:00+08', '2026-10-05 02:32:25+08', '2026-10-05 02:30:00+08', 0, 0),
    (9003, 'stockflow_manual_20261005.dump', 'backups/stockflow_manual_20261005.dump', 49012736, 'MANUAL', 'SUCCESS', '管理端手动备份成功', '2026-10-05 11:00:00+08', '2026-10-05 11:02:05+08', '2026-10-05 10:59:00+08', 9901, 9901),
    (9004, 'stockflow_manual_20261003.dump', 'backups/stockflow_manual_20261003.dump', 0, 'MANUAL', 'FAILED', '失败：磁盘剩余空间不足', '2026-10-03 18:00:00+08', '2026-10-03 18:00:35+08', '2026-10-03 17:59:00+08', 9901, 9901),
    (9005, '', '', 0, 'AUTO', 'RUNNING', '自动备份执行中', '2026-10-06 02:30:00+08', NULL, '2026-10-06 02:30:00+08', 0, 0),
    (9006, '', '', 0, 'MANUAL', 'REQUESTED', '已登记，等待部署侧执行器拾取', NULL, NULL, '2026-10-06 11:40:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

-- ============ 17) 各单据缺失状态补全（状态机全值域覆盖） ============
--
-- §11 已交付"主链走通"的单据（完成/部分/待审/草稿）；本段补齐各状态机剩余态，使每个
--   单据列表页的「状态」筛选每个选项都有命中行。新增单号统一走 20261006 日期段
--   （自 000001 起连续编号），计数器推进见 §17.9。

-- ---- 17.1 采购订单补 3 态（APPROVED / RECEIVED_ALL / CANCELLED）----
INSERT INTO purchase_orders (po_no, supplier_id, warehouse_id, total_amount, status, approved_by, approved_at,
                             received_at, completed_at, cancelled_at, remark, created_at, created_by, updated_by)
SELECT v.po_no, sup.id, w.id, v.total, v.status, v.approved_by, v.approved_at, v.received_at, v.completed_at,
       v.cancelled_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('PO-20261006-000001', 'SUP-E002', 'WH-E01', 5600.0000::numeric(18, 4), 'APPROVED', 9901, '2026-10-06 09:10:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '已审核待收货：FPC 补库', '2026-10-06 09:00:00+08'::timestamptz),
             ('PO-20261006-000002', 'SUP-E003', 'WH-E01', 7900.0000::numeric(18, 4), 'RECEIVED_ALL', 9901, '2026-10-06 09:30:00+08'::timestamptz, '2026-10-06 14:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '已全收待质检上架：PCB 板', '2026-10-06 09:20:00+08'::timestamptz),
             ('PO-20261006-000003', 'SUP-E004', 'WH-E03', 1200.0000::numeric(18, 4), 'CANCELLED', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '2026-10-06 10:30:00+08'::timestamptz, '供应商无法供货，采购取消', '2026-10-06 10:00:00+08'::timestamptz)) AS v(po_no, sup_code, wh_code, total, status, approved_by, approved_at, received_at, completed_at, cancelled_at, remark, created_at)
JOIN suppliers sup ON sup.code = v.sup_code AND sup.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (po_no) DO NOTHING;

INSERT INTO purchase_order_items (po_id, line_no, sku_id, qty_ordered, qty_received, qty_rejected, qty_putaway,
                                  price, amount, remark, created_at, created_by)
SELECT p.id, v.line_no, sku.id, v.qty, v.received, 0, v.putaway, v.price, v.qty * v.price, '', v.created_at, 9901
FROM (VALUES ('PO-20261006-000001', 1, 'SKU-E007-01', 300.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 2.2000::numeric(18, 4), '2026-10-06 09:00:00+08'::timestamptz),
             ('PO-20261006-000001', 2, 'SKU-E008-01', 50.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 38.0000::numeric(18, 4), '2026-10-06 09:00:00+08'::timestamptz),
             ('PO-20261006-000002', 1, 'SKU-E009-01', 500.0000::numeric(18, 4), 500.0000::numeric(18, 4), 500.0000::numeric(18, 4), 15.8000::numeric(18, 4), '2026-10-06 09:20:00+08'::timestamptz),
             ('PO-20261006-000003', 1, 'SKU-E015-01', 200.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 1.8000::numeric(18, 4), '2026-10-06 10:00:00+08'::timestamptz)) AS v(po_no, line_no, sku_code, qty, received, putaway, price, created_at)
JOIN purchase_orders p ON p.po_no = v.po_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 17.2 入库单补 5 态（DRAFT / RECEIVING / AWAITING_QC / CLOSED / CANCELLED）----
INSERT INTO inbound_orders (inbound_no, source_type, source_no, warehouse_id, status, received_at, inspected_at,
                            putaway_at, completed_at, remark, created_at, created_by, updated_by)
SELECT v.inbound_no, v.source_type, v.source_no, w.id, v.status, v.received_at, v.inspected_at,
       v.putaway_at, v.completed_at, v.remark, v.created_at, 9902, 9902
FROM (VALUES ('IN-20261006-000001', 'PURCHASE', 'PO-20261006-000001', 'WH-E01', 'DRAFT', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '到货登记草稿（未提交）', '2026-10-06 09:40:00+08'::timestamptz),
             ('IN-20261006-000002', 'PURCHASE', 'PO-20261006-000002', 'WH-E01', 'RECEIVING', '2026-10-06 14:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '收货中：PCB 板清点中', '2026-10-06 13:50:00+08'::timestamptz),
             ('IN-20261006-000003', 'PURCHASE', 'PO-20261002-000001', 'WH-E01', 'AWAITING_QC', '2026-10-06 15:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '已收货待质检', '2026-10-06 14:50:00+08'::timestamptz),
             ('IN-20261006-000004', 'PURCHASE', 'PO-20261002-000001', 'WH-E01', 'CLOSED', '2026-10-02 14:30:00+08'::timestamptz, '2026-10-03 10:30:00+08'::timestamptz, '2026-10-03 17:00:00+08'::timestamptz, '2026-10-03 17:30:00+08'::timestamptz, '短装差额关闭（供应商不再补货）', '2026-10-02 14:10:00+08'::timestamptz),
             ('IN-20261006-000005', 'OTHER', '', 'WH-E01', 'CANCELLED', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '重复登记，作废', '2026-10-06 11:00:00+08'::timestamptz)) AS v(inbound_no, source_type, source_no, wh_code, status, received_at, inspected_at, putaway_at, completed_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (inbound_no) DO NOTHING;

INSERT INTO inbound_items (inbound_id, line_no, sku_id, qty, qty_received, qty_inspected, qty_putaway, remark, created_at, created_by)
SELECT i.id, v.line_no, sku.id, v.qty, v.received, v.inspected, v.putaway, '', i.created_at, 9902
FROM (VALUES ('IN-20261006-000001', 1, 'SKU-E007-01', 300.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('IN-20261006-000002', 1, 'SKU-E009-01', 500.0000::numeric(18, 4), 200.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('IN-20261006-000003', 1, 'SKU-E001-01', 40.0000::numeric(18, 4), 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('IN-20261006-000004', 1, 'SKU-E002-01', 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 25.0000::numeric(18, 4), 25.0000::numeric(18, 4))) AS v(inbound_no, line_no, sku_code, qty, received, inspected, putaway)
JOIN inbound_orders i ON i.inbound_no = v.inbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 17.3 上架任务补 3 态（IN_PROGRESS / PAUSED / CANCELLED）----
INSERT INTO putaway_tasks (putaway_no, inbound_no, receipt_no, sku_id, batch_id, serial_no, qty, from_state,
                           target_warehouse_id, target_zone_id, target_shelf_id, target_bin_id, status,
                           claimed_by, claimed_at, completed_at, remark, created_at, created_by)
SELECT v.putaway_no, v.inbound_no, v.receipt_no, sku.id, COALESCE(bat.id, 0), '',
       v.qty, 'available', w.id, b.zone_id, b.shelf_id, b.id, v.status,
       CASE WHEN v.status IN ('IN_PROGRESS', 'PAUSED') THEN 9902 ELSE 0 END,
       CASE WHEN v.status IN ('IN_PROGRESS', 'PAUSED') THEN v.created_at ELSE NULL END,
       NULL::timestamptz, v.remark, v.created_at, 9902
FROM (VALUES ('PW-20261006-000001', 'IN-20261003-000002', 'RC-20261003-000001', 'SKU-E006-01', 'B20260926-E006', 200.0000::numeric(18, 4), 'IN_PROGRESS', 'WH-E01', 'PK-01-12', '上架中：已扫 120/200', '2026-10-06 10:00:00+08'::timestamptz),
             ('PW-20261006-000002', 'IN-20261003-000002', 'RC-20261003-000001', 'SKU-E006-01', 'B20260926-E006', 200.0000::numeric(18, 4), 'PAUSED', 'WH-E01', 'PK-01-12', '已暂停：目标库位待清理', '2026-10-06 10:30:00+08'::timestamptz),
             ('PW-20261006-000003', 'IN-20261006-000005', '', 'SKU-E007-01', NULL, 10.0000::numeric(18, 4), 'CANCELLED', 'WH-E01', 'REEL-01-21', '入库单作废，任务取消', '2026-10-06 11:10:00+08'::timestamptz)) AS v(putaway_no, inbound_no, receipt_no, sku_code, batch_no, qty, status, wh_code, bin_code, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (putaway_no) DO NOTHING;

-- ---- 17.4 质检单补 2 态 + 结果值域（PENDING / INSPECTING + 部分合格/不合格）----
INSERT INTO quality_orders (qc_no, source_type, source_no, warehouse_id, inspection_type, status,
                            qty_inspected, qty_qualified, qty_defective, result, inspector_id, inspector_name,
                            inspected_at, remark, created_at, created_by)
SELECT v.qc_no, v.source_type, v.source_no, w.id, v.inspection_type, v.status,
       v.qty_inspected, v.qty_qualified, v.qty_defective, v.result,
       CASE WHEN v.status = 'COMPLETED' THEN 9904 ELSE 0 END,
       CASE WHEN v.status = 'COMPLETED' THEN '赵盘点' ELSE '' END,
       v.inspected_at, v.remark, v.created_at, 9901
FROM (VALUES ('QC-20261006-000001', 'INBOUND', 'IN-20261006-000003', 'WH-E01', '抽检', 'PENDING', 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), '', NULL::timestamptz, '待检：来料 40 盘', '2026-10-06 15:00:00+08'::timestamptz),
             ('QC-20261006-000002', 'INBOUND', 'IN-20261006-000002', 'WH-E01', '全检', 'INSPECTING', 120.0000::numeric(18, 4), 118.0000::numeric(18, 4), 2.0000::numeric(18, 4), '', '2026-10-06 15:30:00+08'::timestamptz, '全检进行中：PCB 板 120/500', '2026-10-06 15:10:00+08'::timestamptz),
             ('QC-20261006-000003', 'INBOUND', 'IN-20261003-000002', 'WH-E01', '全检', 'COMPLETED', 400.0000::numeric(18, 4), 397.0000::numeric(18, 4), 3.0000::numeric(18, 4), '部分合格', '2026-10-04 09:40:00+08'::timestamptz, '全检完成：3 个端子氧化判不合格', '2026-10-04 09:00:00+08'::timestamptz)) AS v(qc_no, source_type, source_no, wh_code, inspection_type, status, qty_inspected, qty_qualified, qty_defective, result, inspected_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (qc_no) DO NOTHING;

INSERT INTO quality_items (qc_id, line_no, sku_id, batch_no, qty_inspected, qty_qualified, qty_defective, remark, created_at, created_by)
SELECT q.id, v.line_no, sku.id, v.batch_no, v.qty, v.qualified, v.defective, '', q.created_at, 9901
FROM (VALUES ('QC-20261006-000001', 1, 'SKU-E001-01', 'B20260928-E001', 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('QC-20261006-000002', 1, 'SKU-E009-01', 'B20260915-E003', 120.0000::numeric(18, 4), 118.0000::numeric(18, 4), 2.0000::numeric(18, 4)),
             ('QC-20261006-000003', 1, 'SKU-E006-01', 'B20260926-E006', 400.0000::numeric(18, 4), 397.0000::numeric(18, 4), 3.0000::numeric(18, 4))) AS v(qc_no, line_no, sku_code, batch_no, qty, qualified, defective)
JOIN quality_orders q ON q.qc_no = v.qc_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 17.5 销售订单补 5 态（DRAFT / APPROVED / SHIPPED_ALL / REJECTED / CANCELLED）----
INSERT INTO sales_orders (so_no, customer_id, warehouse_id, shipping_address, delivery_method, total_amount,
                          status, approved_by, approved_at, shipped_at, completed_at, cancelled_at, remark,
                          created_at, created_by, updated_by)
SELECT v.so_no, cus.id, w.id, cus.shipping_address, v.delivery_method, v.total, v.status, v.approved_by,
       v.approved_at, v.shipped_at, v.completed_at, v.cancelled_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('SO-20261006-000001', 'CUS-E001', 'WH-E03', '物流专线', 1290.0000::numeric(18, 4), 'DRAFT', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '草稿：10 台白温控器', '2026-10-06 09:00:00+08'::timestamptz),
             ('SO-20261006-000002', 'CUS-E003', 'WH-E03', '快递', 2720.0000::numeric(18, 4), 'APPROVED', 9901, '2026-10-06 10:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '已审核待分配：40 台插座', '2026-10-06 09:40:00+08'::timestamptz),
             ('SO-20261006-000003', 'CUS-E002', 'WH-E03', '物流专线', 1554.0000::numeric(18, 4), 'SHIPPED_ALL', 9901, '2026-10-06 10:20:00+08'::timestamptz, '2026-10-06 15:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '已全发待签收', '2026-10-06 10:10:00+08'::timestamptz),
             ('SO-20261006-000004', 'CUS-E001', 'WH-E03', '快递', 645.0000::numeric(18, 4), 'REJECTED', 9901, '2026-10-06 11:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '审核驳回：客户信用额度不足', '2026-10-06 10:40:00+08'::timestamptz),
             ('SO-20261006-000005', 'CUS-E002', 'WH-E03', '快递', 900.0000::numeric(18, 4), 'CANCELLED', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '2026-10-06 11:30:00+08'::timestamptz, '客户取消订单', '2026-10-06 11:10:00+08'::timestamptz)) AS v(so_no, cus_code, wh_code, delivery_method, total, status, approved_by, approved_at, shipped_at, completed_at, cancelled_at, remark, created_at)
JOIN customers cus ON cus.code = v.cus_code AND cus.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (so_no) DO NOTHING;

INSERT INTO sales_order_items (so_id, line_no, sku_id, qty, price, amount, qty_allocated, qty_shipped, remark, created_at, created_by)
SELECT so.id, v.line_no, sku.id, v.qty, v.price, v.qty * v.price, v.allocated, v.shipped, '', so.created_at, 9901
FROM (VALUES ('SO-20261006-000001', 1, 'SKU-E013-01', 10.0000::numeric(18, 4), 129.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('SO-20261006-000002', 1, 'SKU-E014-01', 40.0000::numeric(18, 4), 59.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('SO-20261006-000002', 2, 'SKU-E015-01', 200.0000::numeric(18, 4), 1.8000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('SO-20261006-000003', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 129.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4)),
             ('SO-20261006-000004', 1, 'SKU-E014-01', 10.0000::numeric(18, 4), 59.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('SO-20261006-000005', 1, 'SKU-E013-02', 7.0000::numeric(18, 4), 129.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4))) AS v(so_no, line_no, sku_code, qty, price, allocated, shipped)
JOIN sales_orders so ON so.so_no = v.so_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 17.6 出库单补 8 态 + 类型值域（PENDING_ALLOCATE / ALLOCATED / PICKING / PICKED / CHECKED / PACKED / CANCELLED / CLOSED）----
INSERT INTO outbound_orders (outbound_no, so_no, type, warehouse_id, status, picked_at, checked_at, packed_at,
                             shipped_at, remark, created_at, created_by, updated_by)
SELECT v.outbound_no, v.so_no, v.type, w.id, v.status, v.picked_at, v.checked_at, v.packed_at, v.shipped_at,
       v.remark, v.created_at, 9903, 9903
FROM (VALUES ('OUT-20261006-000001', 'SO-20261006-000002', '销售出库', 'WH-E03', 'PENDING_ALLOCATE', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '待分配库存', '2026-10-06 10:05:00+08'::timestamptz),
             ('OUT-20261006-000002', 'SO-20261006-000002', '销售出库', 'WH-E03', 'ALLOCATED', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '已分配，待拣货', '2026-10-06 10:10:00+08'::timestamptz),
             ('OUT-20261006-000003', 'SO-20261006-000003', '销售出库', 'WH-E03', 'PICKING', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '拣货中', '2026-10-06 10:30:00+08'::timestamptz),
             ('OUT-20261006-000004', 'SO-20261006-000003', '销售出库', 'WH-E03', 'PICKED', '2026-10-06 11:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '拣货完成待复核', '2026-10-06 10:35:00+08'::timestamptz),
             ('OUT-20261006-000005', 'SO-20261006-000003', '销售出库', 'WH-E03', 'CHECKED', '2026-10-06 11:00:00+08'::timestamptz, '2026-10-06 11:20:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '复核完成待打包', '2026-10-06 10:40:00+08'::timestamptz),
             ('OUT-20261006-000006', 'SO-20261006-000003', '销售出库', 'WH-E03', 'PACKED', '2026-10-06 11:00:00+08'::timestamptz, '2026-10-06 11:20:00+08'::timestamptz, '2026-10-06 11:40:00+08'::timestamptz, NULL::timestamptz, '打包完成待发货', '2026-10-06 10:45:00+08'::timestamptz),
             ('OUT-20261006-000007', '', '生产领料', 'WH-E01', 'CANCELLED', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '产线计划变更，出库取消', '2026-10-06 11:00:00+08'::timestamptz),
             ('OUT-20261006-000008', 'SO-20261006-000001', '销售出库', 'WH-E03', 'CLOSED', '2026-10-06 09:30:00+08'::timestamptz, '2026-10-06 09:50:00+08'::timestamptz, '2026-10-06 10:00:00+08'::timestamptz, '2026-10-06 10:20:00+08'::timestamptz, '差额关闭：客户确认不再补发', '2026-10-06 09:00:00+08'::timestamptz)) AS v(outbound_no, so_no, type, wh_code, status, picked_at, checked_at, packed_at, shipped_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (outbound_no) DO NOTHING;

INSERT INTO outbound_items (outbound_id, line_no, sku_id, qty, qty_picked, qty_checked, qty_packed, qty_shipped, remark, created_at, created_by)
SELECT o.id, v.line_no, sku.id, v.qty, v.picked, v.checked, v.packed, v.shipped, '', o.created_at, 9903
FROM (VALUES ('OUT-20261006-000001', 1, 'SKU-E014-01', 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000002', 1, 'SKU-E014-01', 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000003', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 6.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000004', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000005', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000006', 1, 'SKU-E013-01', 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 12.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('OUT-20261006-000008', 1, 'SKU-E013-01', 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 10.0000::numeric(18, 4), 8.0000::numeric(18, 4))) AS v(outbound_no, line_no, sku_code, qty, picked, checked, packed, shipped)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- ---- 17.7 拣货 / 复核 / 发货补态（对齐 000007/000008 真实 DDL：assignee_id/assignee_name、
--      done_at、warehouse_id 必填；三表状态机枚举全值域覆盖）----
-- pick_tasks 枚举 PENDING/CLAIMED/PICKING/PICKED/EXCEPTION/CANCELLED：§11 已给
--   PENDING/PICKING/PICKED，本段补 CLAIMED/EXCEPTION/CANCELLED。
INSERT INTO pick_tasks (pick_no, outbound_no, outbound_line_no, sku_id, batch_id, source_warehouse_id,
                        source_zone_id, source_shelf_id, source_bin_id, qty, picked_qty, status,
                        assignee_id, assignee_name, claimed_at, picked_at, scanned_code, scan_matched,
                        warehouse_id, remark, created_at, created_by)
SELECT v.pick_no, v.outbound_no, 1, sku.id, COALESCE(bat.id, 0), w.id, b.zone_id, b.shelf_id, b.id,
       v.qty, v.picked, v.status,
       CASE WHEN v.status <> 'PENDING' THEN 9903 ELSE 0 END,
       CASE WHEN v.status <> 'PENDING' THEN '陈发货' ELSE '' END,
       CASE WHEN v.status <> 'PENDING' THEN v.at ELSE NULL END,
       CASE WHEN v.status = 'PICKED' THEN v.at ELSE NULL END,
       '', FALSE,
       w.id, v.remark, v.at, 9903
FROM (VALUES ('PK-20261006-000001', 'OUT-20261006-000002', 'SKU-E014-01', NULL, 'WH-E03', 'FG-01-22', 40.0000::numeric(18, 4), 0.0000::numeric(18, 4), 'CLAIMED', '已领取待拣货', '2026-10-06 10:15:00+08'::timestamptz),
             ('PK-20261006-000002', 'OUT-20261006-000003', 'SKU-E013-01', NULL, 'WH-E03', 'FG-01-11', 12.0000::numeric(18, 4), 4.0000::numeric(18, 4), 'EXCEPTION', '拣货位实物与标签不符，已登记异常', '2026-10-06 10:35:00+08'::timestamptz),
             ('PK-20261006-000003', 'OUT-20261006-000007', 'SKU-E001-01', 'B20260928-E001', 'WH-E01', 'REEL-01-11', 20.0000::numeric(18, 4), 0.0000::numeric(18, 4), 'CANCELLED', '出库单作废，任务取消', '2026-10-06 11:05:00+08'::timestamptz)) AS v(pick_no, outbound_no, sku_code, batch_no, wh_code, bin_code, qty, picked, status, remark, at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (pick_no) DO NOTHING;

-- check_tasks 枚举 PENDING/DONE/EXCEPTION：§11 已给 DONE，本段补 PENDING/EXCEPTION；
-- result 值域（错货/少货/多货/批次错误/序列号错误）取样覆盖。
INSERT INTO check_tasks (check_no, outbound_no, outbound_line_no, sku_id, batch_id, serial_no, qty, status,
                         result, assignee_id, assignee_name, claimed_at, done_at, warehouse_id, remark, created_at, created_by)
SELECT v.check_no, v.outbound_no, 1, sku.id, 0, '', v.qty, v.status,
       v.result,
       CASE WHEN v.status = 'DONE' THEN 9903 ELSE 0 END,
       CASE WHEN v.status = 'DONE' THEN '陈发货' ELSE '' END,
       CASE WHEN v.status = 'DONE' THEN v.at - interval '10 minutes' ELSE NULL END,
       CASE WHEN v.status = 'DONE' THEN v.at ELSE NULL END,
       o.warehouse_id, v.remark, v.at, 9903
FROM (VALUES ('CH-20261006-000001', 'OUT-20261006-000005', 'SKU-E013-01', 12.0000::numeric(18, 4), 'PENDING', '', '待复核领取', '2026-10-06 11:05:00+08'::timestamptz),
             ('CH-20261006-000002', 'OUT-20261006-000006', 'SKU-E013-01', 12.0000::numeric(18, 4), 'EXCEPTION', '序列号错误', '复核发现串码，已登记异常', '2026-10-06 11:25:00+08'::timestamptz)) AS v(check_no, outbound_no, sku_code, qty, status, result, remark, at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT (check_no) DO NOTHING;

-- shipments 枚举 PENDING/SHIPPED/IN_TRANSIT/SIGNED/ABNORMAL：§11 已给 SHIPPED，
-- 本段补 PENDING/IN_TRANSIT/SIGNED/ABNORMAL（000008 无 signed_at 列，签收时间即 shipped_at）。
INSERT INTO shipments (shipment_no, outbound_no, carrier, tracking_no, warehouse_id, shipper_id, shipper_name,
                       package_count, status, shipped_at, idempotency_key, remark, created_at, created_by)
SELECT v.shipment_no, v.outbound_no, v.carrier, v.tracking_no, o.warehouse_id, 9903, '陈发货',
       1, v.status, v.shipped_at, 'dev-seed-' || v.shipment_no, v.remark, v.created_at, 9903
FROM (VALUES ('SH-20261006-000001', 'OUT-20261006-000006', '顺丰速运', 'SF0000000000001', 'PENDING', NULL::timestamptz, '待揽收', '2026-10-06 11:45:00+08'::timestamptz),
             ('SH-20261006-000002', 'OUT-20261003-000001', '顺丰速运', 'SF0000000000002', 'IN_TRANSIT', '2026-10-04 16:30:00+08'::timestamptz, '运输中', '2026-10-04 16:20:00+08'::timestamptz),
             ('SH-20261006-000003', 'OUT-20261003-000001', '德邦物流', 'DB0000000000003', 'ABNORMAL', '2026-10-04 16:30:00+08'::timestamptz, '物流异常：外箱破损待理赔', '2026-10-04 16:25:00+08'::timestamptz),
             ('SH-20261006-000004', 'OUT-20261004-000002', '京东物流', 'JD0000000000004', 'SIGNED', '2026-10-05 11:20:00+08'::timestamptz, '客户已签收', '2026-10-05 11:10:00+08'::timestamptz)) AS v(shipment_no, outbound_no, carrier, tracking_no, status, shipped_at, remark, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
ON CONFLICT (shipment_no) DO NOTHING;

-- ---- 17.8 调拨单补 5 态（DRAFT / PENDING_APPROVAL / APPROVED / TRANSFERRING / CANCELLED）----
INSERT INTO transfer_orders (transfer_no, type, from_warehouse_id, to_warehouse_id, status, approved_by, approved_at,
                             outbound_at, received_at, cancelled_at, remark, created_at, created_by, updated_by)
SELECT v.transfer_no, v.type, fw.id, tw.id, v.status, v.approved_by, v.approved_at, v.outbound_at, v.received_at,
       v.cancelled_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('TR-20261006-000001', 'BIN', 'WH-E01', 'WH-E01', 'DRAFT', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '草稿：库位整理搬迁', '2026-10-06 09:00:00+08'::timestamptz),
             ('TR-20261006-000002', 'WAREHOUSE', 'WH-E01', 'WH-E02', 'PENDING_APPROVAL', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '待审核：电阻拨料', '2026-10-06 09:30:00+08'::timestamptz),
             ('TR-20261006-000003', 'WAREHOUSE', 'WH-E01', 'WH-E02', 'APPROVED', 9901, '2026-10-06 10:00:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '已审核待出库', '2026-10-06 09:40:00+08'::timestamptz),
             ('TR-20261006-000004', 'WAREHOUSE', 'WH-E01', 'WH-E02', 'TRANSFERRING', 9901, '2026-10-06 10:20:00+08'::timestamptz, '2026-10-06 10:40:00+08'::timestamptz, NULL::timestamptz, NULL::timestamptz, '调拨在途', '2026-10-06 10:10:00+08'::timestamptz),
             ('TR-20261006-000005', 'WAREHOUSE', 'WH-E02', 'WH-E03', 'CANCELLED', 0, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '2026-10-06 11:00:00+08'::timestamptz, '计划调整，调拨取消', '2026-10-06 10:50:00+08'::timestamptz)) AS v(transfer_no, type, from_wh, to_wh, status, approved_by, approved_at, outbound_at, received_at, cancelled_at, remark, created_at)
JOIN warehouses fw ON fw.code = v.from_wh AND fw.deleted_at IS NULL
JOIN warehouses tw ON tw.code = v.to_wh AND tw.deleted_at IS NULL
ON CONFLICT (transfer_no) DO NOTHING;

INSERT INTO transfer_items (transfer_id, line_no, sku_id, batch_id, from_warehouse_id, from_zone_id, from_shelf_id,
                            from_bin_id, to_warehouse_id, to_zone_id, to_shelf_id, to_bin_id, qty, qty_out, qty_in,
                            created_at, created_by)
SELECT t.id, v.line_no, sku.id, COALESCE(bat.id, 0), fw.id, fb.zone_id, fb.shelf_id, fb.id,
       tw.id, tb.zone_id, tb.shelf_id, tb.id, v.qty, v.qty_out, v.qty_in, t.created_at, 9901
FROM (VALUES ('TR-20261006-000001', 1, 'SKU-E001-01', 'B20260928-E001', 'WH-E01', 'REEL-01-11', 'WH-E01', 'REEL-01-22', 10.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('TR-20261006-000002', 1, 'SKU-E001-02', 'B20260928-E002', 'WH-E01', 'REEL-01-21', 'WH-E02', 'SR-01-11', 6.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('TR-20261006-000003', 1, 'SKU-E002-01', 'B20261005-E002', 'WH-E01', 'REEL-02-11', 'WH-E02', 'SR-01-12', 5.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('TR-20261006-000004', 1, 'SKU-E003-01', 'B20260915-E003', 'WH-E01', 'REEL-02-12', 'WH-E02', 'SA-01-12', 50.0000::numeric(18, 4), 50.0000::numeric(18, 4), 0.0000::numeric(18, 4)),
             ('TR-20261006-000005', 1, 'SKU-E008-01', 'B20261004-E008', 'WH-E02', 'SA-01-11', 'WH-E03', 'FG-01-12', 5.0000::numeric(18, 4), 0.0000::numeric(18, 4), 0.0000::numeric(18, 4))) AS v(transfer_no, line_no, sku_code, batch_no, from_wh, from_bin, to_wh, to_bin, qty, qty_out, qty_in)
JOIN transfer_orders t ON t.transfer_no = v.transfer_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
JOIN warehouses fw ON fw.code = v.from_wh AND fw.deleted_at IS NULL
JOIN bins fb ON fb.code = v.from_bin AND fb.warehouse_id = fw.id
JOIN warehouses tw ON tw.code = v.to_wh AND tw.deleted_at IS NULL
JOIN bins tb ON tb.code = v.to_bin AND tb.warehouse_id = tw.id
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT DO NOTHING;

-- ---- 17.9 盘点单补 3 态（DRAFT / COMPLETED / CANCELLED）+ 差异状态全值域 ----
INSERT INTO count_orders (count_no, warehouse_id, scope, status, frozen_at, reviewed_at, completed_at,
                          cancelled_at, remark, created_at, created_by)
SELECT v.count_no, w.id, v.scope, v.status, v.frozen_at, v.reviewed_at, v.completed_at, v.cancelled_at,
       v.remark, v.created_at, 9904
FROM (VALUES ('CK-20261006-000001', 'WH-E03', '{"type": "WAREHOUSE"}'::jsonb, 'DRAFT', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '成品仓全盘计划（草稿）', '2026-10-06 09:00:00+08'::timestamptz),
             ('CK-20261006-000002', 'WH-E01', '{"type": "BIN", "bins": ["REEL-01-22", "RV-01-11", "RV-01-12"]}'::jsonb, 'COMPLETED', '2026-10-06 08:00:00+08'::timestamptz, '2026-10-06 09:00:00+08'::timestamptz, '2026-10-06 09:30:00+08'::timestamptz, NULL::timestamptz, '循环盘点完成，3 项差异已分别驳回/审核/核销', '2026-10-06 07:50:00+08'::timestamptz),
             ('CK-20261006-000003', 'WH-E02', '{"type": "ZONE", "zone": "E02-STORE"}'::jsonb, 'CANCELLED', NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, '2026-10-06 10:00:00+08'::timestamptz, '生产计划冲突，盘点取消', '2026-10-06 09:50:00+08'::timestamptz)) AS v(count_no, wh_code, scope, status, frozen_at, reviewed_at, completed_at, cancelled_at, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (count_no) DO NOTHING;

-- 盘点明细：按盘点点范围（3 个库位）从现存库存行取快照，账实一致（qty_counted = qty_system）
INSERT INTO count_items (count_id, inventory_row_id, sku_id, warehouse_id, zone_id, shelf_id, bin_id,
                         qty_system, qty_counted, counted_by, counted_at, serial_no, created_at, created_by)
SELECT c.id, i.id, sku.id, i.warehouse_id, i.zone_id, i.shelf_id, i.bin_id,
       i.total_qty, i.total_qty, 9904, c.frozen_at, '', c.created_at, 9904
FROM count_orders c
JOIN inventory i ON i.warehouse_id = c.warehouse_id
                AND i.bin_id IN (SELECT b.id FROM bins b
                                 WHERE b.code IN ('REEL-01-22', 'RV-01-11', 'RV-01-12')
                                   AND b.warehouse_id = c.warehouse_id)
JOIN skus sku ON sku.id = i.sku_id
WHERE c.count_no = 'CK-20261006-000002'
ON CONFLICT DO NOTHING;

-- 盘点差异补全 4 态中的 3 态（PENDING 见 §11.7）：REJECTED / APPROVED / EXECUTED 各 1 行，
-- 与上方 count_items 的 3 个库位一一对应（uk_count_differences_count_line 保证 (盘点,行号) 唯一）。
INSERT INTO count_differences (count_id, line_no, sku_id, warehouse_id, bin_id, batch_id, qty_system, qty_counted,
                               diff_qty, adjust_no, status, remark, created_at, created_by)
SELECT c.id, v.line_no, sku.id, c.warehouse_id, i.bin_id, i.batch_id, v.qty_sys, v.qty_cnt,
       v.qty_cnt - v.qty_sys, v.adjust_no, v.status, v.remark, c.frozen_at, 9904
FROM (VALUES (1, 'SKU-E001-01', 'REEL-01-22', 50.0000::numeric(18, 4), 50.0000::numeric(18, 4), '', 'REJECTED', '复盘点确认账实相符，差异记录驳回'),
             (2, 'SKU-E004-01', 'RV-01-11', 300.0000::numeric(18, 4), 296.0000::numeric(18, 4), '', 'APPROVED', 'MOS 管实盘少 4，审核通过待生成调整单'),
             (3, 'SKU-E012-01', 'RV-01-12', 60.0000::numeric(18, 4), 56.0000::numeric(18, 4), 'ADJ-20261004-000001', 'EXECUTED', 'ESD 托盘少 4，已由调整单核销')) AS v(line_no, sku_code, bin_code, qty_sys, qty_cnt, adjust_no, status, remark)
JOIN count_orders c ON c.count_no = 'CK-20261006-000002'
JOIN skus sku ON sku.code = v.sku_code
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = c.warehouse_id
JOIN inventory i ON i.warehouse_id = c.warehouse_id AND i.bin_id = b.id AND i.sku_id = sku.id
ON CONFLICT DO NOTHING;

-- ---- 17.10 单号计数器推进（20261006 日期段；GREATEST 幂等）----
INSERT INTO doc_number_counters (prefix, period, next_no, created_at, updated_at, created_by)
VALUES ('PO', '20261006', 4, now(), now(), 0), ('IN', '20261006', 6, now(), now(), 0),
       ('PW', '20261006', 4, now(), now(), 0), ('QC', '20261006', 4, now(), now(), 0),
       ('SO', '20261006', 6, now(), now(), 0), ('OUT', '20261006', 9, now(), now(), 0),
       ('PK', '20261006', 5, now(), now(), 0), ('CH', '20261006', 3, now(), now(), 0),
       ('SH', '20261006', 4, now(), now(), 0), ('TR', '20261006', 6, now(), now(), 0),
       ('CK', '20261006', 4, now(), now(), 0), ('RT', '20261006', 4, now(), now(), 0),
       ('EX', '20261006', 5, now(), now(), 0), ('ADJ', 'ALL', 9, now(), now(), 0),
       ('IMP', '20261006', 7, now(), now(), 0), ('EXP', '20261006', 7, now(), now(), 0)
ON CONFLICT (prefix, period) DO UPDATE SET next_no = GREATEST(doc_number_counters.next_no, EXCLUDED.next_no);

-- ---- 17.11 审批记录补充（退货 / 调整 / 盘点差异；append-only NOT EXISTS 防重跑）----
INSERT INTO document_approvals (target_type, target_no, action, result, opinion, operator_id, operator_name, created_at)
SELECT v.target_type, v.target_no, v.action, v.result, v.opinion, v.operator_id, v.operator_name, v.created_at
FROM (VALUES ('purchase_order', 'PO-20261006-000001', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 09:05:00+08'::timestamptz),
             ('purchase_order', 'PO-20261006-000001', 'APPROVE', 'APPROVED', '同意，按需到货', 9901, '王经理', '2026-10-06 09:10:00+08'::timestamptz),
             ('purchase_order', 'PO-20261006-000003', 'CANCEL', 'CANCELLED', '供应商缺货，取消', 9901, '王经理', '2026-10-06 10:30:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000002', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 09:45:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000002', 'APPROVE', 'APPROVED', '同意，库存充足', 9901, '王经理', '2026-10-06 10:00:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000003', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 10:15:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000003', 'APPROVE', 'APPROVED', '同意，现货直发', 9901, '王经理', '2026-10-06 10:20:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000004', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 10:50:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000004', 'APPROVE', 'REJECTED', '客户信用额度不足，驳回', 9901, '王经理', '2026-10-06 11:00:00+08'::timestamptz),
             ('sales_order', 'SO-20261006-000005', 'CANCEL', 'CANCELLED', '客户取消', 9901, '王经理', '2026-10-06 11:30:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000002', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 09:35:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000003', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 09:45:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000003', 'APPROVE', 'APPROVED', '同意拨料', 9901, '王经理', '2026-10-06 10:00:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000004', 'SUBMIT', '', '提交审核', 9901, '王经理', '2026-10-06 10:15:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000004', 'APPROVE', 'APPROVED', '同意', 9901, '王经理', '2026-10-06 10:20:00+08'::timestamptz),
             ('transfer_order', 'TR-20261006-000005', 'CANCEL', 'CANCELLED', '计划调整取消', 9901, '王经理', '2026-10-06 11:00:00+08'::timestamptz),
             ('inventory_adjustment', 'ADJ-20261004-000001', 'APPROVE', 'APPROVED', '同意核销盘亏 4', 9901, '王经理', '2026-10-04 14:00:00+08'::timestamptz),
             ('inventory_adjustment', 'ADJ-20261004-000007', 'APPROVE', 'APPROVED', '同意报废 4', 9901, '王经理', '2026-10-04 17:00:00+08'::timestamptz),
             ('inventory_adjustment', 'ADJ-20261005-000005', 'APPROVE', 'REJECTED', '核查为未登记入库，驳回', 9901, '王经理', '2026-10-05 15:00:00+08'::timestamptz),
             ('return_order', 'RT-20261004-000001', 'SUBMIT', '', '提交退货申请', 9903, '陈发货', '2026-10-04 09:45:00+08'::timestamptz),
             ('return_order', 'RT-20261004-000001', 'APPROVE', 'APPROVED', '同意退货', 9901, '王经理', '2026-10-04 10:00:00+08'::timestamptz),
             ('return_order', 'RT-20261004-000002', 'CANCEL', 'CANCELLED', '客户撤销', 9903, '陈发货', '2026-10-04 18:00:00+08'::timestamptz),
             ('return_order', 'RT-20261005-000001', 'SUBMIT', '', '提交退货申请', 9903, '陈发货', '2026-10-05 08:45:00+08'::timestamptz),
             ('return_order', 'RT-20261005-000001', 'APPROVE', 'APPROVED', '同意退回', 9901, '王经理', '2026-10-05 09:00:00+08'::timestamptz)) AS v(target_type, target_no, action, result, opinion, operator_id, operator_name, created_at)
WHERE NOT EXISTS (
    SELECT 1 FROM document_approvals da
    WHERE da.target_type = v.target_type AND da.target_no = v.target_no
      AND da.action = v.action AND da.created_at = v.created_at);

-- ============ 18) 演示数据补全轮二（2026-10-07：效期预警 / 待复核 / 超储 / 流水类型） ============
--
-- 目的（docs/plans/2026-10-07-demo-data-round2.md）：§0–§17 后仍空的四个页面级数据点——
--   ①效期批次为零（临期/过期预警页、工作台临期组全空）；②待复核队列为空（check_tasks 全 DONE）；
--   ③超储预警零候选；④流水仅 INBOUND/OUTBOUND/INSPECT_PASS（按 TRANSFER_*/MOVE/ADJUST 筛选全空）。
-- 幂等口径沿 §8/§10/§11：显式 9xxx 主键或自然键 ON CONFLICT DO NOTHING、单事务内、
--   与既有数据零冲突（新批次号 / 新五维键 / 新 check_no / 空闲 id 段——现库 max：
--   batches 9617 / inventory 9848 / ledger 9906）。
-- 效期三档日期用 CURRENT_DATE ± n 相对表达式：灌数时点相对计算，演示数据不随时间腐烂
--   （+5/+29 落 30 天临期窗内、+45 窗外正常对照、−3 已过期；窗口 = inventory.alert.expiry_days
--   最大档，运行期缺省 30——sysops configSeeds 同源）。

-- ---- 18.1 效期批次 6（挂批次+效期双开且非序列号的 SKU；生产/入库/到期日全相对化）----
-- 序列号管理 SKU（D005-01 等）不可灌普通库存（一物一行守卫），过期档改用 D008-01。
INSERT INTO batches (id, sku_id, batch_no, supplier_id, production_date, inbound_date, expiry_date, cost_price, remark, created_by)
VALUES (9621, 9433, 'B20260928-E010', 9504, (CURRENT_DATE - 9)::date,  (CURRENT_DATE - 4)::date,  (CURRENT_DATE + 5)::date,  28.0000::numeric(18, 4), 'DEV SEED 临期演示（剩余约 5 天）', 0),
       (9622, 9433, 'B20260910-E010', 9504, (CURRENT_DATE - 27)::date, (CURRENT_DATE - 20)::date, (CURRENT_DATE + 29)::date, 28.0000::numeric(18, 4), 'DEV SEED 临期演示（30 天窗口边缘）', 0),
       (9623, 9425, 'B20261001-E003', 9504, (CURRENT_DATE - 16)::date, (CURRENT_DATE - 6)::date,  (CURRENT_DATE + 12)::date, 12.6000::numeric(18, 4), 'DEV SEED 临期演示 MSL3 湿敏（剩余约 12 天）', 0),
       (9624, 9425, 'B20260825-E003', 9504, (CURRENT_DATE - 40)::date, (CURRENT_DATE - 30)::date, (CURRENT_DATE + 45)::date, 12.6000::numeric(18, 4), 'DEV SEED 效期正常批次（窗口外对照）', 0),
       (9625, 9407, 'B20260912-D004', 9503, (CURRENT_DATE - 25)::date, (CURRENT_DATE - 12)::date, (CURRENT_DATE + 25)::date, 8.0000::numeric(18, 4),  'DEV SEED 临期演示（剩余约 25 天）', 0),
       (9626, 9415, 'B20260710-D008', 9502, (CURRENT_DATE - 90)::date, (CURRENT_DATE - 45)::date, (CURRENT_DATE - 3)::date,  22.0000::numeric(18, 4), 'DEV SEED 已过期演示（expired 预警档）', 0)
ON CONFLICT (sku_id, batch_no) DO NOTHING;

-- ---- 18.2 效期批次期初库存 7 行（6 效期 + 1 超储；§8 口径只写 total/available）与期初流水 1:1 成对 ----
-- 超储行：SKU-D001-02（批次/效期/序列号三关全关）WH-D02 合计 10+520=530 > max_stock 500
--   → 库存预警 overstock 档有命中（不可选序列号管理 SKU——一物一行与灌数守卫冲突）。
INSERT INTO inventory (id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
                       total_qty, available_qty, created_by)
SELECT v.id, w.id, z.id, s.id, b.id, sku.id, COALESCE(bat.id, 0),
       v.qty, v.qty, 0
FROM (VALUES (9851, 'WH-E01', 'REEL-02-21', 'SKU-E010-01', 'B20260928-E010', 40.0000::numeric(18, 4)),
             (9852, 'WH-E01', 'REEL-02-22', 'SKU-E010-01', 'B20260910-E010', 25.0000::numeric(18, 4)),
             (9853, 'WH-E01', 'REEL-02-12', 'SKU-E003-01', 'B20261001-E003', 60.0000::numeric(18, 4)),
             (9854, 'WH-E01', 'IC-01-12',   'SKU-E003-01', 'B20260825-E003', 30.0000::numeric(18, 4)),
             (9855, 'WH-D01', 'S-01-22',    'SKU-D004-01', 'B20260912-D004', 150.0000::numeric(18, 4)),
             (9856, 'WH-D01', 'P-02-12',    'SKU-D008-01', 'B20260710-D008', 45.0000::numeric(18, 4)),
             (9857, 'WH-D02', 'S-01-22',    'SKU-D001-02', NULL, 520.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (warehouse_id, bin_id, sku_id, batch_id) DO NOTHING;

INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, COALESCE(bat.id, 0),
       'INBOUND', '期初', 'DEV-SEED-OPEN-' || v.id::text, 'available', 'available',
       0, v.qty, v.qty,
       0, 'dev-seed', 'dev-seed', 'DEV SEED', '2026-10-05 09:00:00+08'::timestamptz
FROM (VALUES (9911, 'WH-E01', 'REEL-02-21', 'SKU-E010-01', 'B20260928-E010', 40.0000::numeric(18, 4)),
             (9912, 'WH-E01', 'REEL-02-22', 'SKU-E010-01', 'B20260910-E010', 25.0000::numeric(18, 4)),
             (9913, 'WH-E01', 'REEL-02-12', 'SKU-E003-01', 'B20261001-E003', 60.0000::numeric(18, 4)),
             (9914, 'WH-E01', 'IC-01-12',   'SKU-E003-01', 'B20260825-E003', 30.0000::numeric(18, 4)),
             (9915, 'WH-D01', 'S-01-22',    'SKU-D004-01', 'B20260912-D004', 150.0000::numeric(18, 4)),
             (9916, 'WH-D01', 'P-02-12',    'SKU-D008-01', 'B20260710-D008', 45.0000::numeric(18, 4)),
             (9917, 'WH-D02', 'S-01-22',    'SKU-D001-02', NULL, 520.0000::numeric(18, 4))) AS v(id, wh_code, bin_code, sku_code, batch_no, qty)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (ledger_no) DO NOTHING;

-- ---- 18.3 待复核任务（PENDING 挂唯一 PICKED 出库单；复核中心待处理队列 / 工作台待复核组）----
INSERT INTO check_tasks (check_no, outbound_no, outbound_line_no, sku_id, batch_id, serial_no, qty, status,
                         result, assignee_id, assignee_name, claimed_at, done_at, warehouse_id, remark, created_at, created_by)
SELECT v.check_no, v.outbound_no, v.line_no, sku.id, COALESCE(bat.id, 0), '', v.qty, 'PENDING', '',
       0, '', NULL::timestamptz, NULL::timestamptz, w.id, 'DEV SEED', v.created_at, 9903
FROM (VALUES ('CH-20261007-000001', 'OUT-20261006-000004', 1, 'SKU-E013-01', NULL, 12.0000::numeric(18, 4), '2026-10-07 09:30:00+08'::timestamptz)) AS v(check_no, outbound_no, line_no, sku_code, batch_no, qty, created_at)
JOIN outbound_orders o ON o.outbound_no = v.outbound_no
JOIN warehouses w ON w.id = o.warehouse_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (check_no) DO NOTHING;

INSERT INTO doc_number_counters (prefix, period, next_no, created_at, updated_at, created_by)
VALUES ('CH', '20261007', 2, now(), now(), 0),
       ('IMP', '20261007', 3, now(), now(), 0),
       ('EXP', '20261007', 2, now(), now(), 0),
       ('PO', '20261007', 2, now(), now(), 0),
       ('PW', '20261007', 2, now(), now(), 0),
       ('CK', '20261007', 2, now(), now(), 0)
ON CONFLICT (prefix, period) DO UPDATE SET next_no = GREATEST(doc_number_counters.next_no, EXCLUDED.next_no);

-- ---- 18.4 演示流水（净零对，§10 补影红线：Σ qty_change = 0 不改现存量锚点）----
-- 覆盖 TRANSFER_OUT/TRANSFER_IN（回指真实调拨单 TR-20261006-000003）、MOVE（库位间移位）、
-- ADJUST（盘盈/盘亏，回指真实调整单）、LOCK/RELEASE（锁定/释放对）——流水页各类型筛选有命中。
INSERT INTO inventory_ledgers (id, ledger_no, sku_id, warehouse_id, zone_id, shelf_id, bin_id, batch_id,
                               change_type, business_type, business_no, status_from, status_to,
                               qty_before, qty_change, qty_after,
                               operator_id, operator_name, request_id, remark, created_at)
SELECT v.id, 'LED-DEV-' || v.id::text, sku.id, w.id, z.id, s.id, b.id, 0,
       v.ctype, '演示', 'DEV-SEED-FLOW-' || v.id::text, v.st_from, v.st_to,
       v.q_before, v.q_change, v.q_after,
       0, 'dev-seed', 'dev-seed', 'DEV SEED', v.at
FROM (VALUES (9921, 'WH-E01', 'IC-01-11', 'SKU-E004-01', 'TRANSFER_OUT', 'available', 'available', 500.0000::numeric(18, 4),  -30.0000::numeric(18, 4), 470.0000::numeric(18, 4), '2026-10-06 10:20:00+08'::timestamptz),
             (9922, 'WH-E02', 'SB-01-11', 'SKU-E004-01', 'TRANSFER_IN',  'available', 'available', 0.0000::numeric(18, 4),    30.0000::numeric(18, 4),  30.0000::numeric(18, 4), '2026-10-06 14:40:00+08'::timestamptz),
             (9923, 'WH-E01', 'REEL-01-11', 'SKU-E001-01', 'MOVE', 'available', 'available', 30.0000::numeric(18, 4),  -10.0000::numeric(18, 4), 20.0000::numeric(18, 4), '2026-10-06 16:10:00+08'::timestamptz),
             (9924, 'WH-E01', 'REEL-01-12', 'SKU-E001-01', 'MOVE', 'available', 'available', 10.0000::numeric(18, 4),   10.0000::numeric(18, 4), 20.0000::numeric(18, 4), '2026-10-06 16:10:30+08'::timestamptz),
             (9925, 'WH-D01', 'S-01-11', 'SKU-D001-01', 'ADJUST', 'available', 'available', 12.0000::numeric(18, 4),   2.0000::numeric(18, 4),  14.0000::numeric(18, 4), '2026-10-06 17:30:00+08'::timestamptz),
             (9926, 'WH-D01', 'R-01-12', 'SKU-D009-01', 'ADJUST', 'available', 'available', 300.0000::numeric(18, 4), -2.0000::numeric(18, 4), 298.0000::numeric(18, 4), '2026-10-06 17:35:00+08'::timestamptz),
             (9927, 'WH-E01', 'REEL-02-11', 'SKU-E002-01', 'LOCK',    'available', 'locked',    25.0000::numeric(18, 4),  -5.0000::numeric(18, 4), 20.0000::numeric(18, 4), '2026-10-07 09:00:00+08'::timestamptz),
             (9928, 'WH-E01', 'REEL-02-11', 'SKU-E002-01', 'RELEASE', 'locked',    'available', 20.0000::numeric(18, 4),   5.0000::numeric(18, 4), 25.0000::numeric(18, 4), '2026-10-07 09:40:00+08'::timestamptz)) AS v(id, wh_code, bin_code, sku_code, ctype, st_from, st_to, q_before, q_change, q_after, at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN zones z ON z.id = b.zone_id
JOIN shelves s ON s.id = b.shelf_id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT (ledger_no) DO NOTHING;

-- ---- 18.5 数据域状态补全（§15 既有导入缺 EXECUTING/PARTIAL_SUCCESS 态、导出缺 PROCESSING 态
--      —— 导入/导出任务列表页状态筛选与 verify §14 断言的全值域收口）----
INSERT INTO import_tasks (id, import_no, import_type, status, source_file_id, total_rows, valid_rows, error_rows,
                          success_rows, failed_rows, started_at, finished_at, created_at, created_by)
VALUES (9011, 'IMP-20261007-000001', 'SKU', 'EXECUTING', 9008, 40, 40, 0, 18, 0,
        '2026-10-07 10:00:00+08'::timestamptz, NULL::timestamptz, '2026-10-07 09:58:00+08'::timestamptz, 9903),
       (9012, 'IMP-20261007-000002', 'SUPPLIER', 'PARTIAL_SUCCESS', 9008, 20, 20, 3, 17, 3,
        '2026-10-07 11:00:00+08'::timestamptz, '2026-10-07 11:01:00+08'::timestamptz, '2026-10-07 10:58:00+08'::timestamptz, 9901)
ON CONFLICT DO NOTHING;

INSERT INTO import_task_rows (id, task_id, row_no, raw, parsed, status, errors, batch_no, created_at, created_by, updated_by)
VALUES (9015, 9011, 1, '{"code": "SKU-NEW-010", "product": "P-NEW-001"}'::jsonb, '{"code": "SKU-NEW-010"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-07 10:00:00+08', 9903, 9903),
       (9016, 9011, 2, '{"code": "SKU-NEW-011", "product": "P-NEW-001"}'::jsonb, '{"code": "SKU-NEW-011"}'::jsonb, 'QUEUED', NULL, 1, '2026-10-07 10:00:00+08', 9903, 9903),
       (9017, 9012, 1, '{"code": "SUP-NEW-002", "name": "演示供应商乙"}'::jsonb, '{"code": "SUP-NEW-002", "name": "演示供应商乙"}'::jsonb, 'INVALID', '[{"row": 1, "column": "code", "message": "供应商编码已存在"}]'::jsonb, 1, '2026-10-07 11:00:00+08', 9901, 9901),
       (9018, 9012, 2, '{"code": "SUP-NEW-003", "name": "演示供应商丙"}'::jsonb, '{"code": "SUP-NEW-003", "name": "演示供应商丙"}'::jsonb, 'SUCCESS', NULL, 1, '2026-10-07 11:00:00+08', 9901, 9901)
ON CONFLICT DO NOTHING;

INSERT INTO export_tasks (id, export_no, module, scope, params, status, progress, total_rows, file_id,
                          error_message, started_at, created_at, created_by)
VALUES (9013, 'EXP-20261007-000001', 'INVENTORY_LEDGER', 'TIME_RANGE',
        '{"time_from": "2026-10-01 00:00:00", "time_to": "2026-10-07 23:59:59"}'::jsonb,
        'PROCESSING', 40, 0, 9003, '', '2026-10-07 12:00:00+08'::timestamptz, '2026-10-07 11:59:00+08'::timestamptz, 9901)
ON CONFLICT DO NOTHING;

-- ---- 18.6 单据状态补漏（verify §16 断言缺口：§17 的 APPROVED 采购单 / §11 的 PENDING 上架 /
--      §17.9 的 DRAFT 盘点单曾被运行期同号单据 ON CONFLICT 静默跳过——改用全新 20261007 段）----
INSERT INTO purchase_orders (po_no, supplier_id, warehouse_id, total_amount, status, approved_by, approved_at,
                             remark, created_at, created_by, updated_by)
SELECT v.po_no, sup.id, w.id, v.total, v.status, 9901, v.approved_at, v.remark, v.created_at, 9901, 9901
FROM (VALUES ('PO-20261007-000001', 'SUP-E002', 'WH-E01', 4300.0000::numeric(18, 4), 'APPROVED',
              '2026-10-07 09:05:00+08'::timestamptz, '已审核待收货：连接器补库（演示补漏）', '2026-10-07 09:00:00+08'::timestamptz)) AS v(po_no, sup_code, wh_code, total, status, approved_at, remark, created_at)
JOIN suppliers sup ON sup.code = v.sup_code AND sup.deleted_at IS NULL
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (po_no) DO NOTHING;

INSERT INTO purchase_order_items (po_id, line_no, sku_id, qty_ordered, qty_received, qty_rejected, qty_putaway,
                                  price, amount, remark, created_at, created_by)
SELECT p.id, v.line_no, sku.id, v.qty, 0, 0, 0, v.price, v.qty * v.price, '', v.created_at, 9901
FROM (VALUES ('PO-20261007-000001', 1, 'SKU-E006-01', 2000.0000::numeric(18, 4), 0.8500::numeric(18, 4), '2026-10-07 09:00:00+08'::timestamptz)) AS v(po_no, line_no, sku_code, qty, price, created_at)
JOIN purchase_orders p ON p.po_no = v.po_no
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
ON CONFLICT DO NOTHING;

INSERT INTO putaway_tasks (putaway_no, inbound_no, receipt_no, sku_id, batch_id, serial_no, qty, from_state,
                           target_warehouse_id, target_zone_id, target_shelf_id, target_bin_id, status,
                           claimed_by, claimed_at, completed_at, remark, created_at, created_by)
SELECT v.putaway_no, v.inbound_no, v.receipt_no, sku.id, COALESCE(bat.id, 0), '',
       v.qty, 'available', w.id, b.zone_id, b.shelf_id, b.id, v.status,
       0, NULL::timestamptz, NULL::timestamptz, v.remark, v.created_at, 9902
FROM (VALUES ('PW-20261007-000001', 'IN-20261003-000002', 'RC-20261003-000001', 'SKU-E006-01', 'B20260926-E006', 100.0000::numeric(18, 4), 'PENDING', 'WH-E01', 'IC-01-12', 'DEV SEED 待领取上架', '2026-10-07 08:30:00+08'::timestamptz)) AS v(putaway_no, inbound_no, receipt_no, sku_code, batch_no, qty, status, wh_code, bin_code, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
JOIN bins b ON b.code = v.bin_code AND b.warehouse_id = w.id
JOIN skus sku ON sku.code = v.sku_code AND sku.deleted_at IS NULL
LEFT JOIN batches bat ON bat.sku_id = sku.id AND bat.batch_no = v.batch_no
ON CONFLICT (putaway_no) DO NOTHING;

INSERT INTO count_orders (count_no, warehouse_id, scope, status, frozen_at, reviewed_at, completed_at,
                          cancelled_at, remark, created_at, created_by)
SELECT v.count_no, w.id, v.scope, v.status, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
       v.remark, v.created_at, 9904
FROM (VALUES ('CK-20261007-000001', 'WH-E01', '{"type": "WAREHOUSE"}'::jsonb, 'DRAFT', '电子仓全盘计划草稿（演示补漏）', '2026-10-07 08:00:00+08'::timestamptz)) AS v(count_no, wh_code, scope, status, remark, created_at)
JOIN warehouses w ON w.code = v.wh_code AND w.deleted_at IS NULL
ON CONFLICT (count_no) DO NOTHING;

COMMIT;

\echo '>>> dev_seed.sql：演示数据注入完成（幂等，可重复执行；请执行 make seed-demo-verify 自检）。'
\endif
