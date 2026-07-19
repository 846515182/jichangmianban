-- ============================================================
-- 2026_07_19_add_plan_is_trial.sql
-- 新增 plans 表 is_trial 字段, 用于区分试用套餐和可购买套餐
-- 修复: 试用套餐不应出现在用户购买列表中
-- 迁移幂等: IF NOT EXISTS 保证重跑安全
-- ============================================================
BEGIN;

-- 为 plans 表添加 is_trial 字段, 默认 false(兼容历史数据)
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS is_trial BOOLEAN DEFAULT false;

-- 对现有名称含"试用"的套餐标记为试用套餐(数据迁移)
UPDATE plans
SET is_trial = true
WHERE name LIKE '%试用%' AND is_deleted = false;

COMMIT;