-- 2026_07_17_perf_indexes.sql
-- 修复 PERF-IDX-01: nodes.last_seen_at 无索引
-- MarkStaleNodesOffline 每 1 分钟跑一次:
--   WHERE is_deleted=false AND online=true AND (last_seen_at IS NULL OR last_seen_at < ?)
-- 旧版无索引会退化为扫描所有 online=true 的行, 节点数多时每分钟一次全表扫描。
-- 新增 partial index 覆盖该查询路径。

-- 仅在索引不存在时创建(IF NOT EXISTS), 已部署实例重跑迁移也安全。
CREATE INDEX IF NOT EXISTS idx_nodes_last_seen
    ON nodes (last_seen_at)
    WHERE is_deleted = false AND online = true;

-- 修复 PERF-IDX-02: traffic_logs.log_date 无独立索引
-- DailyTraffic 按天聚合 GROUP BY log_date, 无索引需 Sort 节点。
-- 该列在 2026_07_14_account_flow.sql 引入但未建索引。
CREATE INDEX IF NOT EXISTS idx_traffic_logs_log_date
    ON traffic_logs (log_date);

-- 修复 PERF-IDX-03: subscriptions.created_at 无索引, 后台列表 ORDER BY created_at DESC 深分页慢
CREATE INDEX IF NOT EXISTS idx_subscriptions_created_at
    ON subscriptions (created_at DESC)
    WHERE is_deleted = false;

-- 修复 PERF-IDX-04: admin_actions 无 created_at 索引, 审计日志无限增长后深分页慢
-- admin_actions 由 GORM AutoMigrate 创建, 无任何索引。
CREATE INDEX IF NOT EXISTS idx_admin_actions_created_at
    ON admin_actions (created_at DESC);
