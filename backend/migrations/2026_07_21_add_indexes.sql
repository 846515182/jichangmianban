-- 2026_07_21_add_indexes.sql
-- 修复 P2-索引: 后台管理列表 ORDER BY created_at DESC 深分页慢
-- orders / users / nodes 三张核心表均无 created_at 索引, 随数据量增长
-- 后台分页查询退化为全表扫描 + Sort, 响应时间劣化。
-- 新增 partial index 覆盖各表后台列表的 ORDER BY created_at DESC 路径,
-- 与 is_deleted = false 过滤组合, 命中率高、写入开销小。

-- 仅在索引不存在时创建(IF NOT EXISTS), 已部署实例重跑迁移也安全。

-- orders: 后台订单列表 ORDER BY created_at DESC
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders (created_at DESC)
    WHERE is_deleted = FALSE;

-- users: 后台用户列表 ORDER BY created_at DESC
CREATE INDEX IF NOT EXISTS idx_users_created_at
    ON users (created_at DESC)
    WHERE is_deleted = FALSE;

-- nodes: 后台节点列表 ORDER BY created_at DESC
CREATE INDEX IF NOT EXISTS idx_nodes_created_at
    ON nodes (created_at DESC)
    WHERE is_deleted = FALSE;
