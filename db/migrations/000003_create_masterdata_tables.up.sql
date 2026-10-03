-- 000003 masterdata 域：product_categories/units/products/skus/barcodes/suppliers/customers
-- 依据：backend-m1-plan.md §6.1/§6.2、database.md §3/§5.1、business-flow.md §1.1–§1.5（字段全量落列）。
-- 约定：
--   * 通用字段 id/created_at/updated_at/created_by/updated_by；软删除 deleted_at 仅限
--     database.md §5.1 清单内的 products/skus/suppliers/customers（分类/单位/条码走停用/解绑）。
--   * 商品/SKU 编码唯一为部分唯一索引（WHERE deleted_at IS NULL），软删后编码可复用。
--   * 金额/数量一律 numeric(18,4)（plan §1：禁 float，保证数量/金额精确）。
--   * 域内外键：products→分类/单位、skus→products、barcodes→skus、分类自引用；
--     供应商/客户为组织级数据，无引用。

CREATE TABLE product_categories (
    id          bigserial    PRIMARY KEY,
    parent_id   bigint,
    code        varchar(64)  NOT NULL,
    name        varchar(128) NOT NULL,
    sort        integer      NOT NULL DEFAULT 0,
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT fk_product_categories_parent FOREIGN KEY (parent_id) REFERENCES product_categories (id),
    CONSTRAINT chk_product_categories_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE product_categories IS '商品分类（树形，business-flow §1.1；分类/单位为组织级下拉数据源）';

CREATE UNIQUE INDEX uk_product_categories_code ON product_categories (code);
CREATE INDEX idx_product_categories_parent_id ON product_categories (parent_id);

CREATE TABLE units (
    id          bigserial    PRIMARY KEY,
    code        varchar(32)  NOT NULL,
    name        varchar(64)  NOT NULL,
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_units_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE units IS '计量单位（business-flow §1.1）';

CREATE UNIQUE INDEX uk_units_code ON units (code);

CREATE TABLE products (
    id          bigserial    PRIMARY KEY,
    code        varchar(64)  NOT NULL,
    name        varchar(255) NOT NULL,
    short_name  varchar(128) NOT NULL DEFAULT '',
    category_id bigint,
    brand       varchar(128) NOT NULL DEFAULT '',
    model       varchar(128) NOT NULL DEFAULT '',
    spec        varchar(255) NOT NULL DEFAULT '',
    unit_id     bigint,
    weight      numeric(18, 4) NOT NULL DEFAULT 0,
    length      numeric(18, 4) NOT NULL DEFAULT 0,
    width       numeric(18, 4) NOT NULL DEFAULT 0,
    height      numeric(18, 4) NOT NULL DEFAULT 0,
    volume      numeric(18, 4) NOT NULL DEFAULT 0,
    image_urls  jsonb        NOT NULL DEFAULT '[]'::jsonb,
    description text         NOT NULL DEFAULT '',
    remark      text         NOT NULL DEFAULT '',
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    deleted_at  timestamptz,
    CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES product_categories (id),
    CONSTRAINT fk_products_unit FOREIGN KEY (unit_id) REFERENCES units (id),
    CONSTRAINT chk_products_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE products IS '商品（business-flow §1.1 全量字段；image_urls 为文件中心占位，上传接口随阶段 14 交付）';

CREATE UNIQUE INDEX uk_products_code ON products (code) WHERE deleted_at IS NULL;
CREATE INDEX idx_products_category_id ON products (category_id);
CREATE INDEX idx_products_status ON products (status);

CREATE TABLE skus (
    id                 bigserial      PRIMARY KEY,
    code               varchar(64)    NOT NULL,
    product_id         bigint         NOT NULL,
    spec_attrs         jsonb          NOT NULL DEFAULT '{}'::jsonb,
    cost_price         numeric(18, 4) NOT NULL DEFAULT 0,
    sale_price         numeric(18, 4) NOT NULL DEFAULT 0,
    safety_stock       numeric(18, 4) NOT NULL DEFAULT 0,
    max_stock          numeric(18, 4) NOT NULL DEFAULT 0,
    min_replenish_qty  numeric(18, 4) NOT NULL DEFAULT 0,
    is_batch_managed   boolean        NOT NULL DEFAULT FALSE,
    is_expiry_managed  boolean        NOT NULL DEFAULT FALSE,
    is_serial_managed  boolean        NOT NULL DEFAULT FALSE,
    is_enabled         boolean        NOT NULL DEFAULT TRUE,
    created_at         timestamptz    NOT NULL DEFAULT now(),
    updated_at         timestamptz    NOT NULL DEFAULT now(),
    created_by         bigint         NOT NULL DEFAULT 0,
    updated_by         bigint         NOT NULL DEFAULT 0,
    deleted_at         timestamptz,
    CONSTRAINT fk_skus_product FOREIGN KEY (product_id) REFERENCES products (id)
);

COMMENT ON TABLE skus IS 'SKU（business-flow §1.2；批次/效期/序列号三开关决定入库、库存、出库业务分支）';
COMMENT ON COLUMN skus.safety_stock IS '安全库存（库存预警阈值，阶段 19 预警扫描消费）';

CREATE UNIQUE INDEX uk_skus_code ON skus (code) WHERE deleted_at IS NULL;
CREATE INDEX idx_skus_product_id ON skus (product_id);

CREATE TABLE barcodes (
    id          bigserial    PRIMARY KEY,
    sku_id      bigint       NOT NULL,
    barcode     varchar(128) NOT NULL,
    code_type   varchar(32)  NOT NULL DEFAULT 'CODE128',
    is_primary  boolean      NOT NULL DEFAULT FALSE,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT fk_barcodes_sku FOREIGN KEY (sku_id) REFERENCES skus (id)
);

COMMENT ON TABLE barcodes IS 'SKU 条码（扫码解析热点：barcode 唯一即业务键；一码一 SKU）';

CREATE UNIQUE INDEX uk_barcodes_barcode ON barcodes (barcode);
CREATE INDEX idx_barcodes_sku_id ON barcodes (sku_id);

CREATE TABLE suppliers (
    id          bigserial    PRIMARY KEY,
    code        varchar(64)  NOT NULL,
    name        varchar(255) NOT NULL,
    contact     varchar(64)  NOT NULL DEFAULT '',
    phone       varchar(32)  NOT NULL DEFAULT '',
    email       varchar(128) NOT NULL DEFAULT '',
    address     varchar(512) NOT NULL DEFAULT '',
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    remark      text         NOT NULL DEFAULT '',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    deleted_at  timestamptz,
    CONSTRAINT chk_suppliers_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE suppliers IS '供应商（business-flow §1.4：已产生业务记录不可删只停用，删除引用校验随 M2 采购单据落地）';

CREATE UNIQUE INDEX uk_suppliers_code ON suppliers (code) WHERE deleted_at IS NULL;

CREATE TABLE customers (
    id                bigserial    PRIMARY KEY,
    code              varchar(64)  NOT NULL,
    name              varchar(255) NOT NULL,
    contact           varchar(64)  NOT NULL DEFAULT '',
    phone             varchar(32)  NOT NULL DEFAULT '',
    email             varchar(128) NOT NULL DEFAULT '',
    address           varchar(512) NOT NULL DEFAULT '',
    shipping_address  varchar(512) NOT NULL DEFAULT '',
    status            varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint       NOT NULL DEFAULT 0,
    updated_by        bigint       NOT NULL DEFAULT 0,
    deleted_at        timestamptz,
    CONSTRAINT chk_customers_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE customers IS '客户（business-flow §1.5；历史订单/历史出库为关联查询视图，不冗余存储）';

CREATE UNIQUE INDEX uk_customers_code ON customers (code) WHERE deleted_at IS NULL;
