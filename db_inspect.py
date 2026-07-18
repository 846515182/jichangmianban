#!/usr/bin/env python3
"""清理前: 先看所有表的数据量, 确认要清哪些"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD  = 'eH62M3CcaSep59J8lZ'
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

def psql(sql):
    return f'''docker exec nexus-postgres psql -U nexus -d nexus_panel -c "{sql}"'''

def run(cmd, timeout=30, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 列所有业务表及其行数
run(psql("SELECT schemaname||'.'||relname AS tbl, n_live_tup AS rows FROM pg_stat_user_tables ORDER BY n_live_tup DESC;"), 15, "1. 所有表行数")

# 关键业务表分别确认
run(psql("SELECT COUNT(*) AS users FROM users;"), 5, "2. users")
run(psql("SELECT COUNT(*) AS traffic_logs FROM traffic_logs;"), 5, "3. traffic_logs")
run(psql("SELECT COUNT(*) AS subscriptions FROM subscriptions;"), 5, "4. subscriptions")
run(psql("SELECT COUNT(*) AS orders FROM orders;"), 5, "5. orders")
run(psql("SELECT COUNT(*) AS tickets FROM tickets;"), 5, "6. tickets")
run(psql("SELECT COUNT(*) AS ticket_messages FROM ticket_messages;"), 5, "7. ticket_messages")
run(psql("SELECT COUNT(*) AS announcements FROM announcements;"), 5, "8. announcements")
run(psql("SELECT COUNT(*) AS nodes FROM nodes;"), 5, "9. nodes")
run(psql("SELECT COUNT(*) AS plans FROM plans;"), 5, "10. plans")
run(psql("SELECT COUNT(*) AS node_plan_bindings FROM node_plan_bindings;"), 5, "11. node_plan_bindings")
run(psql("SELECT COUNT(*) AS user_nodes FROM user_nodes;"), 5, "12. user_nodes")
run(psql("SELECT COUNT(*) AS login_logs FROM login_logs;"), 5, "13. login_logs (如有)")

# 看一下用户表 (确认是否有保留的 admin 用户, 不能误删)
run(psql("SELECT id, username, role, status FROM users ORDER BY role DESC, created_at ASC LIMIT 10;"), 5, "14. users 列表 (确认 admin 不能误删)")

c.close()
print("=== done ===")
