-- 000003 masterdata 域回滚：DROP 顺序与 up 相反（子表 → 主表）

DROP INDEX IF EXISTS uk_customers_code;
DROP TABLE IF EXISTS customers;

DROP INDEX IF EXISTS uk_suppliers_code;
DROP TABLE IF EXISTS suppliers;

DROP INDEX IF EXISTS idx_barcodes_sku_id;
DROP INDEX IF EXISTS uk_barcodes_barcode;
DROP TABLE IF EXISTS barcodes;

DROP INDEX IF EXISTS idx_skus_product_id;
DROP INDEX IF EXISTS uk_skus_code;
DROP TABLE IF EXISTS skus;

DROP INDEX IF EXISTS idx_products_status;
DROP INDEX IF EXISTS idx_products_category_id;
DROP INDEX IF EXISTS uk_products_code;
DROP TABLE IF EXISTS products;

DROP INDEX IF EXISTS uk_units_code;
DROP TABLE IF EXISTS units;

DROP INDEX IF EXISTS idx_product_categories_parent_id;
DROP INDEX IF EXISTS uk_product_categories_code;
DROP TABLE IF EXISTS product_categories;
