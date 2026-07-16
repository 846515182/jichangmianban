-- ============================================================
-- Nexus-Panel 数据库初始化脚本 001
-- 数据库: PostgreSQL 15+
-- 说明: 机场面板系统完整表结构、索引与默认数据
-- 约定: 主键使用 gen_random_uuid()，时间统一 UTC(TIMESTAMPTZ)，
--       统一 is_deleted 逻辑删除，外键 ON DELETE CASCADE
-- ============================================================

-- 启用 pgcrypto 扩展以使用 gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 设置时区为 UTC（仅作用于本会话，应用层应同样使用 UTC）
SET TIME ZONE 'UTC';

-- ============================================================
-- 1. 管理员表 admins
-- ============================================================
CREATE TABLE admins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        VARCHAR(64)  NOT NULL,
    password_hash   VARCHAR(255) NOT NULL,
    email           VARCHAR(255),
    role            VARCHAR(32)  NOT NULL DEFAULT 'admin',
    status          SMALLINT     NOT NULL DEFAULT 1,    -- 1=正常 0=禁用
    lock_until      TIMESTAMPTZ,                         -- 锁定截止时间
    last_login_at   TIMESTAMPTZ,                         -- 最近登录时间
    last_login_ip   INET,                                -- 最近登录 IP
    is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 用户名唯一索引（仅未删除的记录）
CREATE UNIQUE INDEX uk_admins_username ON admins (username) WHERE is_deleted = FALSE;
CREATE INDEX idx_admins_email ON admins (email) WHERE is_deleted = FALSE;
CREATE INDEX idx_admins_status ON admins (status) WHERE is_deleted = FALSE;

-- ============================================================
-- 2. 用户表 users
-- ============================================================
CREATE TABLE users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username          VARCHAR(64)  NOT NULL,
    password_hash     VARCHAR(255),
    email             VARCHAR(255),
    traffic_limit     BIGINT       NOT NULL DEFAULT 0,    -- 流量上限(字节)
    traffic_used      BIGINT       NOT NULL DEFAULT 0,    -- 已用流量(字节)
    upload_bytes      BIGINT       NOT NULL DEFAULT 0,    -- 上传字节
    download_bytes    BIGINT       NOT NULL DEFAULT 0,    -- 下载字节
    expired_at        TIMESTAMPTZ,                          -- 到期时间
    status            VARCHAR(32)  NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','disabled','expired','traffic_exhausted','locked')),
    lock_until        TIMESTAMPTZ,                          -- 锁定截止时间
    remark            TEXT,                                 -- 备注
    is_deleted        BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uk_users_username ON users (username) WHERE is_deleted = FALSE;
CREATE INDEX idx_users_email ON users (email) WHERE is_deleted = FALSE;
CREATE INDEX idx_users_status ON users (status) WHERE is_deleted = FALSE;
CREATE INDEX idx_users_expired_at ON users (expired_at) WHERE is_deleted = FALSE;

-- ============================================================
-- 3. 节点表 nodes
-- ============================================================
CREATE TABLE nodes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    country_code    VARCHAR(8)   NOT NULL DEFAULT 'XX',  -- 国家代码
    protocol        VARCHAR(32)  NOT NULL,                -- vmess/vless/trojan/shadowsocks
    server_address  VARCHAR(255) NOT NULL,                -- 服务器地址
    port            INTEGER      NOT NULL CHECK (port BETWEEN 1 AND 65535),
    server_config   JSONB        NOT NULL DEFAULT '{}'::jsonb,  -- Xray 服务端配置
    traffic_limit   BIGINT       NOT NULL DEFAULT 0,      -- 节点流量上限
    traffic_used    BIGINT       NOT NULL DEFAULT 0,      -- 节点已用流量
    is_enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
    node_token      VARCHAR(128) NOT NULL,                -- 节点通信令牌
    grpc_port       INTEGER      NOT NULL DEFAULT 50051,  -- gRPC 端口
    last_seen_at    TIMESTAMPTZ,                          -- 最后心跳时间
    online          BOOLEAN      NOT NULL DEFAULT FALSE,  -- 在线状态
    version         VARCHAR(64),                          -- node-agent 版本
    is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uk_nodes_token ON nodes (node_token) WHERE is_deleted = FALSE;
CREATE UNIQUE INDEX uk_nodes_name ON nodes (name) WHERE is_deleted = FALSE;
CREATE INDEX idx_nodes_protocol ON nodes (protocol) WHERE is_deleted = FALSE;
CREATE INDEX idx_nodes_enabled ON nodes (is_enabled) WHERE is_deleted = FALSE;
CREATE INDEX idx_nodes_online ON nodes (online) WHERE is_deleted = FALSE;

-- ============================================================
-- 4. 用户-节点关联表 user_nodes
-- ============================================================
CREATE TABLE user_nodes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id     UUID         NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    is_deleted  BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 同一用户对同一节点（在未删除范围内）唯一
CREATE UNIQUE INDEX uk_user_nodes_pair ON user_nodes (user_id, node_id) WHERE is_deleted = FALSE;
CREATE INDEX idx_user_nodes_user ON user_nodes (user_id) WHERE is_deleted = FALSE;
CREATE INDEX idx_user_nodes_node ON user_nodes (node_id) WHERE is_deleted = FALSE;

-- ============================================================
-- 5. 订阅表 subscriptions
-- ============================================================
CREATE TABLE subscriptions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sub_token     VARCHAR(128) NOT NULL,                  -- 订阅令牌
    sub_type      VARCHAR(32)  NOT NULL DEFAULT 'clash',  -- clash/singbox/v2ray
    disable_uri   BOOLEAN      NOT NULL DEFAULT FALSE,    -- 是否禁用分享链接
    expires_at    TIMESTAMPTZ,                            -- 订阅过期时间
    is_deleted    BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uk_subscriptions_token ON subscriptions (sub_token) WHERE is_deleted = FALSE;
CREATE INDEX idx_subscriptions_user ON subscriptions (user_id) WHERE is_deleted = FALSE;
CREATE INDEX idx_subscriptions_expires ON subscriptions (expires_at) WHERE is_deleted = FALSE;

-- ============================================================
-- 6. 流量日志表 traffic_logs（按月分区）
--    id BIGSERIAL，total_bytes 为生成列
-- ============================================================
CREATE TABLE traffic_logs (
    id              BIGSERIAL,
    user_id         UUID        NOT NULL,
    node_id         UUID        NOT NULL,
    upload_bytes    BIGINT      NOT NULL DEFAULT 0,
    download_bytes  BIGINT      NOT NULL DEFAULT 0,
    total_bytes     BIGINT      GENERATED ALWAYS AS (upload_bytes + download_bytes) STORED,
    log_time        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, log_time)
) PARTITION BY RANGE (log_time);

-- 创建当前月与下月分区（当前为 2026-07）
CREATE TABLE traffic_logs_2026_07 PARTITION OF traffic_logs
    FOR VALUES FROM ('2026-07-01 00:00:00+00') TO ('2026-08-01 00:00:00+00');
CREATE TABLE traffic_logs_2026_08 PARTITION OF traffic_logs
    FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');

-- 分区表上的索引（会自动级联到各子分区）
CREATE INDEX idx_traffic_logs_user_node_time ON traffic_logs (user_id, node_id, log_time);
CREATE INDEX idx_traffic_logs_node_time ON traffic_logs (node_id, log_time);
CREATE INDEX idx_traffic_logs_user_time ON traffic_logs (user_id, log_time);
CREATE INDEX idx_traffic_logs_log_time ON traffic_logs (log_time);

-- ============================================================
-- 7. 实时流量表 traffic_realtime
--    复合主键，记录时间窗口内的实时流量
-- ============================================================
CREATE TABLE traffic_realtime (
    node_id        UUID         NOT NULL,
    user_id        UUID         NOT NULL,
    upload_bytes   BIGINT       NOT NULL DEFAULT 0,
    download_bytes BIGINT       NOT NULL DEFAULT 0,
    window_start   TIMESTAMPTZ  NOT NULL,                -- 时间窗口起点
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (node_id, user_id, window_start)
);

CREATE INDEX idx_traffic_realtime_window ON traffic_realtime (window_start);
CREATE INDEX idx_traffic_realtime_user ON traffic_realtime (user_id);
CREATE INDEX idx_traffic_realtime_node ON traffic_realtime (node_id);

-- ============================================================
-- 8. 工单表 tickets
-- ============================================================
CREATE TABLE tickets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    subject     VARCHAR(255) NOT NULL,
    status      VARCHAR(32)  NOT NULL DEFAULT 'open',    -- open/pending/closed
    priority    SMALLINT     NOT NULL DEFAULT 1,         -- 优先级
    is_deleted  BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tickets_user ON tickets (user_id) WHERE is_deleted = FALSE;
CREATE INDEX idx_tickets_status ON tickets (status) WHERE is_deleted = FALSE;
CREATE INDEX idx_tickets_created_at ON tickets (created_at DESC) WHERE is_deleted = FALSE;

-- ============================================================
-- 9. 工单消息表 ticket_messages
-- ============================================================
CREATE TABLE ticket_messages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id    UUID         NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    sender_type  VARCHAR(32)  NOT NULL,                  -- user/admin/system
    content      TEXT         NOT NULL,
    is_deleted   BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ticket_messages_ticket ON ticket_messages (ticket_id, created_at) WHERE is_deleted = FALSE;

-- ============================================================
-- 10. 公告表 announcements
-- ============================================================
CREATE TABLE announcements (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       VARCHAR(255) NOT NULL,
    content     TEXT         NOT NULL,
    is_pinned   BOOLEAN      NOT NULL DEFAULT FALSE,
    is_deleted  BOOLEAN      NOT NULL DEFAULT FALSE,
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_announcements_published ON announcements (published_at DESC) WHERE is_deleted = FALSE;
CREATE INDEX idx_announcements_pinned ON announcements (is_pinned, published_at DESC) WHERE is_deleted = FALSE;

-- ============================================================
-- 11. 系统设置表 settings（键值对，JSONB 值）
-- ============================================================
CREATE TABLE settings (
    key         VARCHAR(128) PRIMARY KEY,
    value       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_settings_key ON settings (key);

-- ============================================================
-- 12. 登录审计表 login_audit
-- ============================================================
CREATE TABLE login_audit (
    id           BIGSERIAL PRIMARY KEY,
    target_type  VARCHAR(32)  NOT NULL,                  -- admin/user
    target_id    UUID,                                   -- 目标ID
    ip           INET,                                   -- 登录IP
    user_agent   TEXT,                                   -- 客户端UA
    location     VARCHAR(255),                           -- 地理位置
    success      BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_login_audit_target ON login_audit (target_type, target_id, created_at DESC);
CREATE INDEX idx_login_audit_ip ON login_audit (ip, created_at DESC);
CREATE INDEX idx_login_audit_created_at ON login_audit (created_at DESC);

-- ============================================================
-- 注意：初始管理员由 main.go ensureSuperAdmin() 通过
--       INIT_ADMIN_USERNAME / INIT_ADMIN_PASSWORD 环境变量创建，
--       不再在此处硬编码密码哈希。
-- ============================================================

-- ============================================================
-- 默认数据: 系统设置
-- ============================================================
INSERT INTO settings (key, value) VALUES
    ('site',          '{"title":"Nexus-Panel","description":"机场管理面板","subtitle":"Nexus Panel"}'::jsonb),
    ('subscribe',     '{"default_type":"clash","update_interval":3600,"max_connections":3}'::jsonb),
    ('traffic',       '{"reset_day":1,"warn_threshold":0.9,"over_limit_action":"block"}'::jsonb),
    ('security',      '{"login_retry_limit":5,"lock_duration_minutes":30,"session_timeout_minutes":120}'::jsonb),
    ('notification',  '{"email_enabled":false,"telegram_enabled":false}'::jsonb),
    ('node',          '{"heartbeat_interval":30,"offline_threshold":90,"default_grpc_port":50051}'::jsonb);

-- ============================================================
-- 结束: 001_init.sql
-- ============================================================
