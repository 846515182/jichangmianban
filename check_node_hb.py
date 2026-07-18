#!/usr/bin/env python3
"""查看节点 heartbeat 完整内容"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD  = 'eH62M3CcaSep59J8lZ'
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080
REDIS_PWD = 'n3xus_r3dis_2026'

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)

def run(cmd, timeout=15, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 节点 ID (美国01, 唯一在线节点)
NODE_ID = "1e8b92e3-ff15-49d5-b6b8-b7db1f12aeb4"

# 1. heartbeat 完整内容
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGETALL "node:heartbeat:{NODE_ID}"', 10, "heartbeat 完整内容")

# 2. 当前时间戳 (对比 ts)
run('date +%s', 5, "当前 Unix 时间戳")

# 3. snap 当前内容
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGETALL "node:speed_snap:{NODE_ID}"', 10, "speed_snap 当前内容")

# 4. 查节点配置 (确认 traffic_limit=0 是不是预期)
run("docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT name, traffic_limit, traffic_used, server_address, port FROM nodes WHERE name LIKE '%美国01%' LIMIT 5;\"", 15, "节点配置 (确认 traffic_limit)")

# 5. 当前 DB 节点表完整字段
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, traffic_used, traffic_limit, online, last_seen_at FROM nodes WHERE is_deleted=false;"', 15, "所有节点状态")

c.close()
print("=== done ===")
