-- ============================================================
-- Nexus-Panel 数据库迁移 2026_07_16_fix_missing_updated_at.sql
-- 修复 cron_service.CleanOrphanData 报错:
--   column "updated_at" of relation "subscriptions" does not exist
-- subscriptions 在 2026-07-14 audit 时被加进 cron, 但 updated_at 列从未存在。
-- (user_nodes 已有 updated_at 列, 不用动)
-- ============================================================

SET TIME ZONE 'UTC';

-- subscriptions 加 updated_at (NOT NULL + DEFAULT NOW, 旧数据回填 NOW())
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- 索引 (供按更新时间查询/排序)
CREATE INDEX IF NOT EXISTS idx_subscriptions_updated_at ON subscriptions (updated_at);
