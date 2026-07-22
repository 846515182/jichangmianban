-- 节点容量管理 + 策略控制: 智能负载调度 + 自动踢人保护 + 限速 + 用途控制
-- 容量字段: 0=不限(不参与调度)
-- 限速字段: speed_limit_mbps 单用户限速, 0=不限
-- 用途字段: usage_type general通用/browsing仅浏览/video视频/download允许下载
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_clients INT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_bandwidth_mbps INT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS cpu_threshold INT NOT NULL DEFAULT 80;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS load_status VARCHAR(20) NOT NULL DEFAULT 'idle';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS speed_limit_mbps INT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS usage_type VARCHAR(20) NOT NULL DEFAULT 'general';

CREATE INDEX IF NOT EXISTS idx_nodes_load_status ON nodes (load_status) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_nodes_usage_type ON nodes (usage_type) WHERE is_deleted = false;
