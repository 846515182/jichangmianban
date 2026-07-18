#!/usr/bin/env python3
"""检查节点当前实际状态: DB + Redis + 面板进程"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

from ops_config import NODE_HOST as HOST
PORT = 22
USER = 'root'
from ops_config import NODE_SSH_PWD as PWD
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080
from ops_config import REDIS_PWD

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)

def run(cmd, timeout=20, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 1. DB 节点流量字段
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, traffic_used, traffic_limit, online, is_enabled FROM nodes WHERE is_deleted=false ORDER BY name LIMIT 10;"', 15, "DB nodes.traffic_used")

# 2. Redis 所有 node: 相关 key
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:*" | head -30', 10, "Redis node:* keys")

# 3. Redis 所有 traffic:* 相关 key
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "traffic:*" | head -30', 10, "Redis traffic:* keys")

# 4. 面板进程启动时间 (确认是否需要重启加载新数据)
run('docker exec nexus-panel ps -eo pid,etime,cmd | grep nexus-panel | head -3', 10, "panel 进程启动时间")

# 5. 美国01 节点的 speed_snap 内容 (如果存在)
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:speed_snap:*" | while read key; do echo "--- $key ---"; docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGETALL "$key"; done', 15, "speed_snap 内容")

c.close()
print("=== done ===")
