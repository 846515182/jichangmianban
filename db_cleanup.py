#!/usr/bin/env python3
"""清理测试数据, 保留 admin/nodes/plans/settings/migrations"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

from ops_config import NODE_HOST as HOST
PORT = 22
USER = 'root'
from ops_config import NODE_SSH_PWD as PWD
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)

def run(cmd, timeout=60, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

def psql_count(label=None):
    sql = ("SELECT 'users' AS t, COUNT(*) FROM users "
           "UNION ALL SELECT 'subscriptions', COUNT(*) FROM subscriptions "
           "UNION ALL SELECT 'orders', COUNT(*) FROM orders "
           "UNION ALL SELECT 'tickets', COUNT(*) FROM tickets "
           "UNION ALL SELECT 'ticket_replies', COUNT(*) FROM ticket_replies "
           "UNION ALL SELECT 'ticket_messages', COUNT(*) FROM ticket_messages "
           "UNION ALL SELECT 'announcements', COUNT(*) FROM announcements "
           "UNION ALL SELECT 'traffic_logs_2026_07', COUNT(*) FROM traffic_logs_2026_07 "
           "UNION ALL SELECT 'traffic_logs', COUNT(*) FROM traffic_logs "
           "UNION ALL SELECT 'traffic_realtime', COUNT(*) FROM traffic_realtime "
           "UNION ALL SELECT 'daily_traffic_summary', COUNT(*) FROM daily_traffic_summary "
           "UNION ALL SELECT 'login_audit', COUNT(*) FROM login_audit "
           "UNION ALL SELECT 'admin_actions', COUNT(*) FROM admin_actions "
           "UNION ALL SELECT 'email_events', COUNT(*) FROM email_events "
           "UNION ALL SELECT 'coupons', COUNT(*) FROM coupons "
           "UNION ALL SELECT 'user_nodes', COUNT(*) FROM user_nodes;")
    cmd = f"docker exec nexus-postgres psql -U nexus -d nexus_panel -t -c \"{sql}\""
    run(cmd, 30, label)

def psql_keep(label=None):
    sql = ("SELECT 'admins' AS t, COUNT(*) FROM admins "
           "UNION ALL SELECT 'nodes', COUNT(*) FROM nodes "
           "UNION ALL SELECT 'plans', COUNT(*) FROM plans "
           "UNION ALL SELECT 'node_plan_bindings', COUNT(*) FROM node_plan_bindings "
           "UNION ALL SELECT 'settings', COUNT(*) FROM settings "
           "UNION ALL SELECT 'schema_migrations', COUNT(*) FROM schema_migrations;")
    cmd = f"docker exec nexus-postgres psql -U nexus -d nexus_panel -t -c \"{sql}\""
    run(cmd, 15, label)

# 清理前快照
psql_count("清理前")

# 用单个 BEGIN/COMMIT 事务包裹所有 TRUNCATE (单行 SQL, 避免 heredoc 问题)
tables = [
    'traffic_logs_2026_07',
    'traffic_logs',
    'traffic_realtime',
    'daily_traffic_summary',
    'login_audit',
    'admin_actions',
    'email_events',
    'ticket_replies',
    'tickets',
    'ticket_messages',
    'orders',
    'subscriptions',
    'user_nodes',
    'users',
    'announcements',
    'coupons',
]
sql_block = "BEGIN; " + " ".join(f"TRUNCATE TABLE {t} CASCADE;" for t in tables) + " COMMIT;"
# 用单引号包整个 -c 参数, 避免双引号被 shell 解析 (SQL 内无单引号)
cmd = f"docker exec nexus-postgres psql -U nexus -d nexus_panel -c '{sql_block}'"
run(cmd, 60, "执行清理 (TRUNCATE CASCADE)")

# 验证清理结果
psql_count("清理后")

# 保留项确认
psql_keep("保留项确认")

# 验证管理员仍可登录
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, username, email FROM admins;"', 5, "管理员账号 (保留)")

c.close()
print("=== done ===")
