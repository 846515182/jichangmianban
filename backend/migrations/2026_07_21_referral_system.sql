-- ============================================================
-- Nexus-Panel 迁移 2026_07_21: 邀请返利系统
-- 新增表: referrals(邀请关系) / referral_rewards(返利记录)
-- 修改表: users(新增 invite_code 邀请码列) / orders(新增 inviter_id 邀请人列)
-- ============================================================
BEGIN;

-- 1. users 表新增邀请码字段
ALTER TABLE users ADD COLUMN IF NOT EXISTS invite_code VARCHAR(16);
COMMENT ON COLUMN users.invite_code IS '邀请码(唯一, 永久有效)';

-- 2. orders 表新增邀请人ID(用于返利结算)
ALTER TABLE orders ADD COLUMN IF NOT EXISTS inviter_id UUID;
COMMENT ON COLUMN orders.inviter_id IS '邀请人ID(注册时绑定, 首单支付后发放返利)';

-- 3. 邀请关系表(记录谁邀请了谁)
CREATE TABLE IF NOT EXISTS referrals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    inviter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invitee_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    reward_cents BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    -- pending: 被邀请人未首单
    -- completed: 返利已发放
    -- expired: 邀请失效(如被邀请人退款)
    reward_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_referrals_inviter_id ON referrals(inviter_id);
CREATE INDEX IF NOT EXISTS idx_referrals_invitee_id ON referrals(invitee_id);
CREATE INDEX IF NOT EXISTS idx_referrals_status ON referrals(status);

-- 4. 返利记录表(每笔返利明细, 用于对账和用户展示)
CREATE TABLE IF NOT EXISTS referral_rewards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    invitee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_cents BIGINT NOT NULL,
    order_amount_cents BIGINT NOT NULL,
    reward_rate NUMERIC(5,2) NOT NULL,
    description VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_referral_rewards_user_id ON referral_rewards(user_id);
CREATE INDEX IF NOT EXISTS idx_referral_rewards_order_id ON referral_rewards(order_id);
CREATE INDEX IF NOT EXISTS idx_referral_rewards_created_at ON referral_rewards(created_at);

-- 5. 用户邀请码唯一索引(放在最后, 避免已有数据冲突)
-- 注意: 旧用户 invite_code 为 NULL, NULL 不触发唯一索引冲突
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_invite_code ON users(invite_code)
    WHERE invite_code IS NOT NULL;

COMMIT;
