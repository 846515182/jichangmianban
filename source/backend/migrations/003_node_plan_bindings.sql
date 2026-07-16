-- ============================================================
-- Nexus-Panel 数据库迁移 003
-- 1. 补充 plans 表的 features / limitations 列(历史库可能缺失)
-- 2. 新建 node_plan_bindings 表(节点-套餐多对多绑定)
-- 3. 数据迁移: 把旧 node_level 映射成 node_plan_bindings 记录
--    规则: 节点 node_level=N → 绑定所有 sort_order>=N 的套餐
--    这样保证老节点继续对原用户群体可见，不会出现"全员失联"
-- ============================================================

SET TIME ZONE 'UTC';

-- ============================================================
-- 1. plans 表补充列(若已存在则跳过)
-- ============================================================
ALTER TABLE plans ADD COLUMN IF NOT EXISTS features TEXT;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS limitations TEXT;

-- ============================================================
-- 2. node_plan_bindings 表
--    一个节点可绑定多个套餐；用户 plan_id 命中任一绑定即可见
-- ============================================================
CREATE TABLE IF NOT EXISTS node_plan_bindings (
    node_id    UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    plan_id    UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (node_id, plan_id)
);

CREATE INDEX IF NOT EXISTS idx_npb_plan ON node_plan_bindings (plan_id);
CREATE INDEX IF NOT EXISTS idx_npb_node ON node_plan_bindings (node_id);

-- ============================================================
-- 3. 历史数据迁移: node_level → node_plan_bindings
--    只为尚未建立绑定的节点补数据，避免重复
--    映射规则: 节点 level=N → 绑定 sort_order >= N 的全部启用套餐
--    (sort_order 1=基础, 2=标准, 3=VIP，与 node_level 语义对齐)
-- ============================================================
INSERT INTO node_plan_bindings (node_id, plan_id)
SELECT n.id, p.id
FROM nodes n
JOIN plans p ON p.is_deleted = FALSE AND p.sort_order >= n.node_level
WHERE n.is_deleted = FALSE
  AND NOT EXISTS (
    SELECT 1 FROM node_plan_bindings b WHERE b.node_id = n.id AND b.plan_id = p.id
  );

-- ============================================================
-- 结束: 003_node_plan_bindings.sql
-- ============================================================
