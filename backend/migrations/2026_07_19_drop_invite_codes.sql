-- ============================================================
-- Nexus-Panel 迁移 2026_07_19:
-- 移除邀请码功能, DROP invite_codes / invite_code_uses 表
-- (email_events 表保留, 由 EmailEvent model 继续使用)
-- ============================================================
BEGIN;
DROP TABLE IF EXISTS invite_code_uses CASCADE;
DROP TABLE IF EXISTS invite_codes CASCADE;
COMMIT;
