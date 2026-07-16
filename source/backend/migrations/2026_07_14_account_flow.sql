-- =====================================================================
-- Nexus-Panel 账号流程迁移
-- 日期: 2026-07-14
-- 内容: email_verified 字段 + 邀请码体系 + 邮件事件审计 + traffic_logs 唯一索引
-- =====================================================================

BEGIN;

-- 1) users 表追加 email_verified
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;

-- 唯一索引: 仅对未删除用户做 email 唯一 (兼容软删场景)
-- 用 partial index 避免历史重复数据阻断
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname='public' AND indexname='uq_users_email_lower') THEN
        BEGIN
            CREATE UNIQUE INDEX uq_users_email_lower
                ON users (LOWER(email)) WHERE is_deleted = FALSE;
            RAISE NOTICE 'Created uq_users_email_lower';
        EXCEPTION WHEN OTHERS THEN
            RAISE NOTICE 'Skip uq_users_email_lower: % (历史数据存在重复 email, 请手动清理)', SQLERRM;
        END;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_users_email_verified
    ON users (email_verified) WHERE email_verified = FALSE;

-- 2) 邀请码
-- 注: 管理员在 admins 表, 普通用户在 users 表
-- created_by 引用 admins(id) (uuid) 因为邀请码由 admin 签发
CREATE TABLE IF NOT EXISTS invite_codes (
    id              BIGSERIAL PRIMARY KEY,
    code            VARCHAR(32)  NOT NULL UNIQUE,
    created_by      UUID         NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    max_uses        INTEGER      NOT NULL DEFAULT 1,
    used_count      INTEGER      NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ,
    disabled        BOOLEAN      NOT NULL DEFAULT FALSE,
    note            VARCHAR(200),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invite_codes_created_by ON invite_codes (created_by);
CREATE INDEX IF NOT EXISTS idx_invite_codes_disabled  ON invite_codes (disabled) WHERE disabled = FALSE;

-- 3) 邀请码使用记录 (修正: ip VARCHAR(45) 兼容 IPv4/IPv6)
CREATE TABLE IF NOT EXISTS invite_code_uses (
    id              BIGSERIAL PRIMARY KEY,
    invite_code_id  BIGINT       NOT NULL REFERENCES invite_codes(id) ON DELETE CASCADE,
    code            VARCHAR(32)  NOT NULL,
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    ip              VARCHAR(45),                -- 修正: 由 INET 改为 VARCHAR(45)
    ua              VARCHAR(512)
);
CREATE INDEX IF NOT EXISTS idx_invite_code_uses_code ON invite_code_uses (code);
CREATE INDEX IF NOT EXISTS idx_invite_code_uses_user ON invite_code_uses (user_id);

-- 4) 邮件事件
-- 注: users.id 是 uuid, user_id 必须用 uuid
CREATE TABLE IF NOT EXISTS email_events (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID         REFERENCES users(id) ON DELETE SET NULL,
    email           VARCHAR(255) NOT NULL,
    event_type      VARCHAR(32)  NOT NULL,
    code_hash       VARCHAR(128),
    sent_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    success         BOOLEAN      NOT NULL,
    error_msg       VARCHAR(500)
);
CREATE INDEX IF NOT EXISTS idx_email_events_user  ON email_events (user_id);
CREATE INDEX IF NOT EXISTS idx_email_events_email ON email_events (email);
CREATE INDEX IF NOT EXISTS idx_email_events_type  ON email_events (event_type, sent_at DESC);

-- 5) 流量日志添加 log_date 派生列 + 唯一索引
DO $genfix$
DECLARE
    col_is_gen text;
BEGIN
    SELECT is_generated INTO col_is_gen
    FROM information_schema.columns
    WHERE table_name = 'traffic_logs' AND column_name = 'log_date';
    IF col_is_gen = 'ALWAYS' THEN
        ALTER TABLE traffic_logs DROP COLUMN log_date;
        RAISE NOTICE 'Dropped generated log_date, re-adding as regular';
    END IF;
END $genfix$;


ALTER TABLE traffic_logs
    ADD COLUMN IF NOT EXISTS log_date DATE;

-- 填充已有数据的 log_date（后台执行，不阻塞）
UPDATE traffic_logs SET log_date = log_time::date WHERE log_date IS NULL;

-- 设置 NOT NULL（需确保 UPDATE 后无 NULL）
ALTER TABLE traffic_logs ALTER COLUMN log_date SET NOT NULL;

-- 创建或替换唯一索引（按天聚合流量）
-- Note: unique index not created on parent partitioned table;
-- each partition gets its own unique index in ensure_traffic_partition()

COMMIT;
