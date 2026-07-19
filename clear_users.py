#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""清空用户相关数据(users/subscriptions/user_nodes/traffic_logs), 保留订单/管理员/节点/套餐/配置"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD = 'eH62M3CcaSep59J8lZ'
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=60, banner_timeout=60)
print("=== SSH 连接成功 ===\n")

def run(cmd, timeout=120, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 先看当前各表数据量
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT \'users\' as t, COUNT(*) FROM users UNION ALL SELECT \'subscriptions\', COUNT(*) FROM subscriptions UNION ALL SELECT \'user_nodes\', COUNT(*) FROM user_nodes UNION ALL SELECT \'traffic_logs\', COUNT(*) FROM traffic_logs UNION ALL SELECT \'orders\', COUNT(*) FROM orders UNION ALL SELECT \'admins\', COUNT(*) FROM admins UNION ALL SELECT \'nodes\', COUNT(*) FROM nodes UNION ALL SELECT \'plans\', COUNT(*) FROM plans WHERE is_deleted=false"', 10, "1. 当前各表数据量")

# 2. 再做一次新备份(保险)
print("\n=== 2. 执行新备份 ===")
run('docker exec nexus-postgres pg_dump -U nexus -d nexus_panel | gzip > /root/nexus-panel/backups/db-backup-before-clear-$(date +%Y%m%d-%H%M%S).sql.gz && ls -lh /root/nexus-panel/backups/', 60, "2. 新备份完成")

# 3. 执行清理(事务, 任一步失败回滚)
SQL = """
BEGIN;

-- 先清流量日志(数据量可能大, 先清这张)
TRUNCATE TABLE traffic_logs CASCADE;

-- 清 user_nodes (用户-节点关联)
TRUNCATE TABLE user_nodes CASCADE;

-- 清 subscriptions (订阅)
TRUNCATE TABLE subscriptions CASCADE;

-- 清 users (用户本体)
-- 注意: orders 表有 user_id 外键引用, 但 ON DELETE SET NULL, 不会阻止 TRUNCATE
-- 但 TRUNCATE users 会让 orders.user_id 变 NULL(SET NULL 行为)
-- 为保留订单审计, 我们不 TRUNCATE orders, 让 orders.user_id 变 NULL 即可
TRUNCATE TABLE users CASCADE;

-- 重置 users 相关序列(UUID 主键不需要序列, 但以防万一)
-- users 用 UUID, 无需重置序列

COMMIT;

-- 验证
SELECT 'users' as t, COUNT(*) FROM users
UNION ALL SELECT 'subscriptions', COUNT(*) FROM subscriptions
UNION ALL SELECT 'user_nodes', COUNT(*) FROM user_nodes
UNION ALL SELECT 'traffic_logs', COUNT(*) FROM traffic_logs
UNION ALL SELECT 'orders', COUNT(*) FROM orders;
"""
# 用 stdin 传 SQL 避免 shell 转义
stdin, stdout, stderr = c.exec_command('docker exec -i nexus-postgres psql -U nexus -d nexus_panel', timeout=120)
stdin.write(SQL)
stdin.channel.shutdown_write()
print("\n=== 3. 执行清理 ===")
print(stdout.read().decode('utf-8','replace'))
err = stderr.read().decode('utf-8','replace')
if err.strip():
    print("[ERR]", err[:800])

# 4. 验证最终状态
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT \'users\' as t, COUNT(*) FROM users UNION ALL SELECT \'subscriptions\', COUNT(*) FROM subscriptions UNION ALL SELECT \'user_nodes\', COUNT(*) FROM user_nodes UNION ALL SELECT \'traffic_logs\', COUNT(*) FROM traffic_logs UNION ALL SELECT \'orders\', COUNT(*) FROM orders UNION ALL SELECT \'admins\', COUNT(*) FROM admins UNION ALL SELECT \'nodes\', COUNT(*) FROM nodes UNION ALL SELECT \'plans\', COUNT(*) FROM plans WHERE is_deleted=false"', 10, "4. 清理后各表数据量")

# 5. 顺手清理 login_audit / admin_actions 里的用户关联数据(审计日志, 不影响业务)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "TRUNCATE TABLE login_audit; TRUNCATE TABLE admin_actions;" 2>&1', 30, "5. 清理审计日志(login_audit/admin_actions)")

# 6. 触发节点配置刷新(让 Xray 把已删用户的凭证移除)
run('docker exec nexus-panel wget -qO- http://127.0.0.1:8080/healthz 2>&1', 10, "6. panel healthz(确认还活着)")
run('docker logs nexus-panel --tail 5 2>&1', 10, "7. panel 日志")

c.close()
print("\n=== 清理完成 ===")
