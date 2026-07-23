-- 2026-07-23: admin_actions 表增加 success 字段, 用于区分成功/失败的管理员操作审计
-- 失败审计(P0-AUDIT)对追踪越权/误操作/攻击行为至关重要

ALTER TABLE admin_actions
    ADD COLUMN IF NOT EXISTS success BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_admin_actions_success
    ON admin_actions (success);
