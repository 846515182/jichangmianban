-- ============================================================
-- 2026_07_18_node_unique_constraint.sql
-- 修复 NODE-DUP-01: nodes 表无唯一约束, 允许无限创建同名同 IP 同端口的重复节点
-- 现象: 测试期间反复创建"美国01"导致 DB 里堆积 18 条同 (name, server_address, port) 节点,
--       只有 1 个 online=true, 其余 17 个 is_enabled=true 但永远 online=false,
--       前端按 server_address 聚合显示所以看不出来, 但 DB 和节点总数被严重污染。
-- 兜底设计: 对未软删除的节点加 partial unique index, 同 (name, server_address, port)
--          组合只允许存在一条 is_deleted=false 记录。软删除后可重建, 不影响业务。
--
-- 注意: 该索引创建前必须先清理已存在的重复行, 否则 CREATE UNIQUE INDEX 会失败。
-- 这里用 ON CONFLICT DO NOTHING 配合手动清理脚本, 已部署实例需先清重复再跑迁移。
-- 迁移幂等: IF NOT EXISTS 保证重跑安全。
-- ============================================================
BEGIN;

-- 清理重复节点: 保留每组 (name, server_address, port) 中最新的 1 条, 删掉其余
-- 用 ctid (PostgreSQL 物理行标识) 精确定位要删的行, 避免 PK 扫描
-- 仅清理 is_deleted=false 的重复 (软删除的本来就排除在 unique 约束外)
DELETE FROM nodes
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY name, server_address, port
                   ORDER BY
                       -- 优先保留 online=true 的 (真实在线的节点)
                       (CASE WHEN online = true THEN 0 ELSE 1 END),
                       -- 其次保留有套餐绑定的
                       (CASE WHEN EXISTS (
                           SELECT 1 FROM node_plan_bindings b WHERE b.node_id = nodes.id
                       ) THEN 0 ELSE 1 END),
                       created_at DESC
                   ) AS rn
        FROM nodes
        WHERE is_deleted = false
    ) t
    WHERE t.rn > 1
);

-- 对未软删除的节点加 partial unique index
-- 同 (name, server_address, port) 只允许一条 is_deleted=false
CREATE UNIQUE INDEX IF NOT EXISTS uq_nodes_name_addr_port_active
    ON nodes (name, server_address, port)
    WHERE is_deleted = false;

COMMIT;
