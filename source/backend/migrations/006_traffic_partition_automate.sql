-- ============================================================
-- Nexus-Panel 数据库迁移 004
-- 修复 F-21: traffic_logs 分区自动创建
-- 说明:
--   001_init.sql 仅手动创建了 2026-07 / 2026-08 两个分区,
--   8 月过后写入会因找不到目标分区而报错, 导致流量日志丢失。
--   本迁移:
--   1. 创建函数 ensure_traffic_partition(), 自动建下月分区
--   2. 创建触发器, 在 INSERT 前自动调用, 保证永远有分区可写
--   不删除/不修改已有分区, 零数据风险
-- ============================================================

SET TIME ZONE 'UTC';

-- ============================================================
-- 1. 创建自动分区函数
--    入参 p_log_time: 即将写入的 log_time
--    逻辑: 计算 log_time 所在月的下一个月, 若分区不存在则创建
--    分区命名规则: traffic_logs_YYYY_MM
-- ============================================================
CREATE OR REPLACE FUNCTION ensure_traffic_partition(p_log_time TIMESTAMPTZ)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    v_start    TIMESTAMPTZ;
    v_end      TIMESTAMPTZ;
    v_part     TEXT;
    v_exists   BOOLEAN;
BEGIN
    -- 当月起点
    v_start := date_trunc('month', p_log_time);
    -- 下月起点(分区上界)
    v_end   := v_start + INTERVAL '1 month';
    -- 分区表名
    v_part  := 'traffic_logs_' || to_char(v_start, 'YYYY_MM');

    -- 检查分区是否已存在
    SELECT EXISTS (
        SELECT 1 FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema()
          AND c.relname = v_part
    ) INTO v_exists;

    IF NOT v_exists THEN
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF traffic_logs FOR VALUES FROM (%L) TO (%L)',
            v_part, v_start, v_end
        );
        -- Create unique index on partition for upsert support
        EXECUTE format(
            'CREATE UNIQUE INDEX IF NOT EXISTS %I ON %I (user_id, node_id, log_date)',
            v_part || '_uq_user_node_date', v_part
        );
    END IF;
END;
$$;

-- ============================================================
-- 2. 创建 INSERT BEFORE 触发器
--    每次 INSERT 前调用 ensure_traffic_partition(),
--    确保目标分区一定存在
--    注意: 分区表上的触发器会作用于父表, 但 BEFORE INSERT
--    触发器在分区路由前执行, 这里用 PL/pgSQL 函数包装
-- ============================================================
CREATE OR REPLACE FUNCTION traffic_logs_before_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM ensure_traffic_partition(NEW.log_time);
    RETURN NEW;
END;
$$;

-- 触发器名使用 IF NOT EXISTS 语义(CREATE TRIGGER 不支持, 用 DO 块判断)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'trg_traffic_logs_ensure_partition'
          AND tgrelid = 'traffic_logs'::regclass
    ) THEN
        CREATE TRIGGER trg_traffic_logs_ensure_partition
            BEFORE INSERT ON traffic_logs
            FOR EACH ROW
            EXECUTE FUNCTION traffic_logs_before_insert();
    END IF;
END;
$$;

-- ============================================================
-- 3. 补建当前月与下月分区(若已存在则跳过, IF NOT EXISTS 保证幂等)
--    防止部署时触发器尚未生效期间的写入失败
-- ============================================================
DO $$
DECLARE
    v_now TIMESTAMPTZ := NOW();
    v_cur_start TIMESTAMPTZ := date_trunc('month', v_now);
    v_nxt_start TIMESTAMPTZ := v_cur_start + INTERVAL '1 month';
BEGIN
    PERFORM ensure_traffic_partition(v_now);
    PERFORM ensure_traffic_partition(v_nxt_start + INTERVAL '1 day');
END;
$$;

-- ============================================================
-- 结束: 004_traffic_partition_automate.sql
-- ============================================================
