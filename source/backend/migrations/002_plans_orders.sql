-- ============================================================
-- Nexus-Panel 数据库迁移 002: 套餐/订单/优惠券/EPay 配置
-- 数据库: PostgreSQL 15+
-- 说明:
--   1. 新建 plans / orders / coupons 表
--   2. 给 nodes 表添加 node_level 列
--   3. 给 users 表添加 plan_id, node_level 列
--   4. 在 settings 表插入 EPay 默认配置
--   5. 插入 3 个默认套餐
-- 约定: 主键使用 gen_random_uuid(), 时间统一 UTC(TIMESTAMPTZ),
--       统一 is_deleted 逻辑删除
-- ============================================================

SET TIME ZONE 'UTC';

-- ============================================================
-- 1. 套餐表 plans
-- ============================================================
CREATE TABLE IF NOT EXISTS plans (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 VARCHAR(128) NOT NULL,
    description          TEXT,
    traffic_limit        BIGINT       NOT NULL DEFAULT 0,    -- 流量配额(字节), 0=不限
    duration_days        INTEGER      NOT NULL DEFAULT 30,   -- 有效期天数, 0=不限
    price_cents          BIGINT       NOT NULL,              -- 价格(分)
    original_price_cents BIGINT       NOT NULL DEFAULT 0,    -- 原价(分, 用于显示划线价)
    node_level           INTEGER      NOT NULL DEFAULT 1,    -- 节点等级 1=基础 2=标准 3=VIP
    device_limit         INTEGER      NOT NULL DEFAULT 3,    -- 设备数限制, 0=不限
    sort_order           INTEGER      NOT NULL DEFAULT 0,    -- 排序
    is_enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
    is_deleted           BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_plans_enabled ON plans (is_enabled) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_plans_sort ON plans (sort_order) WHERE is_deleted = FALSE;

-- ============================================================
-- 2. 订单表 orders
-- ============================================================
CREATE TABLE IF NOT EXISTS orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_no       VARCHAR(64)  NOT NULL,                       -- 订单号 NP+时间戳+随机
    user_id        UUID         NOT NULL,
    plan_id        UUID         NOT NULL,
    plan_name      VARCHAR(128),                                -- 冗余快照
    amount_cents   BIGINT       NOT NULL,                       -- 实付金额(分)
    status         VARCHAR(16)  NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','paid','cancelled','refunded','expired')),
    payment_method VARCHAR(32),                                 -- epay_alipay/epay_wechat/epay_qq
    trade_no       VARCHAR(128),                                -- 第三方支付流水号
    paid_at        TIMESTAMPTZ,
    expired_at     TIMESTAMPTZ NOT NULL,                         -- 订单过期时间(15分钟)
    is_deleted     BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_orders_order_no ON orders (order_no) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders (user_id) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders (status) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_orders_expired ON orders (expired_at) WHERE is_deleted = FALSE AND status = 'pending';

-- ============================================================
-- 3. 优惠券表 coupons
-- ============================================================
CREATE TABLE IF NOT EXISTS coupons (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code            VARCHAR(32)  NOT NULL,
    type            VARCHAR(16)  NOT NULL,                       -- percent / fixed
    value           BIGINT       NOT NULL,                       -- percent: 1-100(%)  fixed: 金额(分)
    min_amount_cents BIGINT      NOT NULL DEFAULT 0,             -- 最低消费(分)
    max_uses        INTEGER      NOT NULL DEFAULT 0,             -- 0=不限
    used_count      INTEGER      NOT NULL DEFAULT 0,
    expire_at       TIMESTAMPTZ,
    is_enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
    is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_coupons_code ON coupons (code) WHERE is_deleted = FALSE;

-- ============================================================
-- 4. 给 nodes 表添加 node_level 列
-- ============================================================
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS node_level INTEGER NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_nodes_level ON nodes (node_level) WHERE is_deleted = FALSE;

-- ============================================================
-- 5. 给 users 表添加 plan_id, node_level 列
-- ============================================================
ALTER TABLE users ADD COLUMN IF NOT EXISTS plan_id UUID;
ALTER TABLE users ADD COLUMN IF NOT EXISTS node_level INTEGER NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_users_plan ON users (plan_id) WHERE is_deleted = FALSE;

-- ============================================================
-- 6. 在 settings 表插入 EPay 默认配置(若不存在)
-- ============================================================
INSERT INTO settings (key, value) VALUES
    ('epay_pid',     '"0"'::jsonb),
    ('epay_key',     '""'::jsonb),
    ('epay_api_url', '""'::jsonb),
    ('epay_enabled', 'false'::jsonb),
    ('epay_notify_url', '""'::jsonb),
    ('epay_return_url', '""'::jsonb)
ON CONFLICT (key) DO NOTHING;

-- ============================================================
-- 7. 插入 3 个默认套餐(若不存在)
--    基础版(10GB/30天/¥9.9/等级1)
--    标准版(50GB/30天/¥19.9/等级2)
--    VIP版(200GB/30天/¥39.9/等级3)
--    价格单位: 分  流量单位: 字节
-- ============================================================
INSERT INTO plans (name, description, traffic_limit, duration_days, price_cents, original_price_cents, node_level, device_limit, sort_order, is_enabled)
SELECT '基础版', '10GB 流量 / 30 天 / 基础节点', 10737418240, 30, 990, 1290, 1, 3, 1, TRUE
WHERE NOT EXISTS (SELECT 1 FROM plans WHERE name = '基础版' AND is_deleted = FALSE);

INSERT INTO plans (name, description, traffic_limit, duration_days, price_cents, original_price_cents, node_level, device_limit, sort_order, is_enabled)
SELECT '标准版', '50GB 流量 / 30 天 / 标准节点', 53687091200, 30, 1990, 2590, 2, 5, 2, TRUE
WHERE NOT EXISTS (SELECT 1 FROM plans WHERE name = '标准版' AND is_deleted = FALSE);

INSERT INTO plans (name, description, traffic_limit, duration_days, price_cents, original_price_cents, node_level, device_limit, sort_order, is_enabled)
SELECT 'VIP版', '200GB 流量 / 30 天 / VIP 节点', 214748364800, 30, 3990, 5990, 3, 10, 3, TRUE
WHERE NOT EXISTS (SELECT 1 FROM plans WHERE name = 'VIP版' AND is_deleted = FALSE);

-- ============================================================
-- 结束: 002_plans_orders.sql
-- ============================================================
