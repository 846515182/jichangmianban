-- ============================================================
-- Nexus-Panel 数据库迁移 004: 工单系统
-- ============================================================
SET TIME ZONE 'UTC';

DO $$ BEGIN
    CREATE TABLE IF NOT EXISTS tickets (
        id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
        user_id         UUID         NOT NULL,
        subject         VARCHAR(255) NOT NULL,
        status          VARCHAR(32)  NOT NULL DEFAULT 'open',
        priority        VARCHAR(16)  NOT NULL DEFAULT 'normal',
        is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
        created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
        updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
    );
END $$;

ALTER TABLE tickets ADD COLUMN IF NOT EXISTS content         TEXT;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS category        VARCHAR(32)  NOT NULL DEFAULT 'other';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS last_reply_by   VARCHAR(16);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS last_reply_at   TIMESTAMPTZ;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS closed_at       TIMESTAMPTZ;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'tickets' AND column_name = 'priority'
        AND data_type = 'smallint'
    ) THEN
        ALTER TABLE tickets ALTER COLUMN priority TYPE VARCHAR(16) USING
            CASE
                WHEN priority = 1 THEN 'low'
                WHEN priority = 2 THEN 'normal'
                WHEN priority = 3 THEN 'high'
                ELSE 'normal'
            END;
    END IF;
END $$;

ALTER TABLE tickets ALTER COLUMN priority SET DEFAULT 'normal';

CREATE INDEX IF NOT EXISTS idx_tickets_user    ON tickets (user_id)        WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_tickets_status  ON tickets (status)         WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_tickets_created ON tickets (created_at DESC) WHERE is_deleted = FALSE;

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
