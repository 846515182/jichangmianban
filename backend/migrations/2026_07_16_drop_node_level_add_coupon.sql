-- ============================================================
-- Nexus-Panel 迁移 2026_07_16:
-- 1. orders 表新增 coupon_id / coupon_code 列(优惠券审计)
-- 2. 删除 nodes / users / plans 表的 node_level 列(已废弃, 改用 node_plan_bindings)
-- ============================================================

-- 1. orders 表新增优惠券审计列
ALTER TABLE orders ADD COLUMN IF NOT EXISTS coupon_id UUID;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS coupon_code VARCHAR(32);
CREATE INDEX IF NOT EXISTS idx_orders_coupon ON orders (coupon_id) WHERE is_deleted = FALSE AND coupon_id IS NOT NULL;

-- 2. 删除 node_level 相关索引和列
DROP INDEX IF EXISTS idx_nodes_level;
ALTER TABLE nodes DROP COLUMN IF EXISTS node_level;

ALTER TABLE users DROP COLUMN IF EXISTS node_level;

-- plans 表的 node_level 列已通过 GORM AutoMigrate 自行管理, 但 DB 中可能残留
ALTER TABLE plans DROP COLUMN IF EXISTS node_level;
