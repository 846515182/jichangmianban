-- ============================================================
-- Nexus-Panel 数据库迁移 004: 工单系统
-- 数据库: PostgreSQL 15+
-- 说明:
--   1. 新建 tickets 表(用户向管理员发起的问题)
--   2. 新建 ticket_replies 表(用户/管理员回复, 按 ticket_id 分组)
-- 约定: 主键使用 gen_random_uuid(), 时间统一 UTC(TIMESTAMPTZ),
--       统一 is_deleted 逻辑删除
-- ============================================================

SET TIME ZONE 'UTC';

-- ============================================================
-- 1. 工单表 tickets: 补全字段（若表已存在则 ALTER ADD）
-- ============================================================
DO $$ BEGIN
    CREATE TABLE IF NOT EXISTS tickets (
        id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
        user_id         UUID         NOT NULL,
        subject         VARCHAR(255) NOT NULL,
        status          VARCHAR(32)  NOT NULL DEFAULT 'open',
        priority        SMALLINT     NOT NULL DEFAULT 0,
        is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
        created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
        updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
    );
END $$;

-- 补全 001_init 中缺失的字段（幂等 ADD COLUMN IF NOT EXISTS）
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS content         TEXT;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS category        VARCHAR(32)  NOT NULL DEFAULT 'other';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS priority        VARCHAR(16)  NOT NULL DEFAULT 'normal';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS last_reply_by   VARCHAR(16);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS last_reply_at   TIMESTAMPTZ;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS closed_at       TIMESTAMPTZ;

-- 迁移旧表的 priority 值（SMALLINT -> VARCHAR）
UPDATE tickets SET priority = CASE
    WHEN priority::int = 1 THEN 'low'
    WHEN priority::int = 2 THEN 'normal'
    WHEN priority::int = 3 THEN 'high'
    ELSE 'normal'
END WHERE priority ~ '^\d+$';

CREATE INDEX IF NOT EXISTS idx_tickets_user    ON tickets (user_id)        WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_tickets_status  ON tickets (status)         WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_tickets_created ON tickets (created_at DESC) WHERE is_deleted = FALSE;

-- ============================================================
-- 2. 工单回复表 ticket_replies (使用新表, 与 ticket_messages 并存)
-- ============================================================
CREATE TABLE IF NOT EXISTS ticket_replies (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id       UUID         NOT NULL,
    reply_type      VARCHAR(16)  NOT NULL,
    replier_id      UUID,
    replier_name    VARCHAR(64),
    content         TEXT,
    is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ticket_replies_ticket ON ticket_replies (ticket_id, created_at ASC) WHERE is_deleted = FALSE;

-- ============================================================
-- 结束: 004_tickets.sql
-- ============================================================
