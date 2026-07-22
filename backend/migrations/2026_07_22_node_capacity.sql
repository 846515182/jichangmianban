-- 节点容量管理: 智能负载调度 + 自动踢人保护
-- 新增 4 个字段: max_clients/max_bandwidth_mbps/cpu_threshold/load_status
-- 0 = 不限(不参与容量调度), 默认值保证存量节点行为不变
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_clients INT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_bandwidth_mbps INT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS cpu_threshold INT NOT NULL DEFAULT 80;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS load_status VARCHAR(20) NOT NULL DEFAULT 'idle';

-- 加索引: 订阅调度时按 load_status 过滤, WHERE is_deleted=false
CREATE INDEX IF NOT EXISTS idx_nodes_load_status ON nodes (load_status) WHERE is_deleted = false;
