-- ============================================================
-- Nexus-Panel 数据库迁移 005
-- 修复 F-22: admins.status 类型对齐
-- 背景:
--   001_init.sql 定义 status SMALLINT NOT NULL DEFAULT 1 (1=正常, 0=禁用)
--   但 model/admin.go 中 Status 为 string 类型 (gorm:"type:varchar(16)"),
--   代码中 ensureSuperAdmin 写入 "active", 登录校验也用字符串,
--   导致 SMALLINT 列被 GORM 写入字符串值时隐式转换, 查询时类型不匹配。
--   本迁移:
--   1. 将 status 列 SMALLINT → VARCHAR(16)
--   2. 数据转换: 1→'active', 0→'disabled', 其它→'active'
--   3. 修正 DEFAULT 为 'active' (原 DEFAULT 1 转换后会变成 '1')
--   4. 重建索引 (status 列上的索引会因类型变更失效)
-- 仅 ALTER, 不删除任何列, 保留全部数据
-- ============================================================

SET TIME ZONE 'UTC';

-- ============================================================
-- 1. 类型转换 + 数据迁移
--    USING 子句把 SMALLINT 值映射为字符串
-- ============================================================
-- 修复 P0-MIGRATION-005: GORM AutoMigrate 先于 SQL 迁移执行,
-- 用最新 model 创建 admins 表时 status 已经是 VARCHAR(model 中是 string)。
-- 旧版无条件执行 ALTER TYPE ... USING CASE WHEN status = 1 会报
-- "operator does not exist: character varying = integer"(VARCHAR 列不能和
-- integer 1 比较)。用 DO 块 + information_schema 检查列类型,
-- 只在 status 还是 smallint 时执行类型转换, 已是 VARCHAR 则跳过。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'admins' AND column_name = 'status' AND data_type = 'smallint'
    ) THEN
        ALTER TABLE admins
            ALTER COLUMN status TYPE VARCHAR(16)
            USING CASE
                WHEN status = 1 THEN 'active'
                WHEN status = 0 THEN 'disabled'
                ELSE 'active'
            END;

        -- 修正默认值: ALTER TYPE 后旧默认值(1)强转为 VARCHAR 得 '1', 需显式重置
        ALTER TABLE admins ALTER COLUMN status SET DEFAULT 'active';
        ALTER TABLE admins ALTER COLUMN status SET NOT NULL;

        -- 索引重建: 001_init.sql 中 idx_admins_status 因类型变更失效
        DROP INDEX IF EXISTS idx_admins_status;
        CREATE INDEX idx_admins_status ON admins (status) WHERE is_deleted = FALSE;
    END IF;
END $$;

-- ============================================================
-- 验证: 查看转换后的数据分布
-- ============================================================
-- SELECT status, COUNT(*) FROM admins GROUP BY status;

-- ============================================================
-- 结束: 005_admin_status_fix.sql
-- ============================================================
