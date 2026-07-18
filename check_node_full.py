#!/usr/bin/env python3
"""全面诊断节点问题: DB + Redis + 面板日志 + agent 连接状态"""
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

# 1. DB 节点完整状态
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, online, is_enabled, traffic_used, traffic_limit, last_seen_at, version FROM nodes WHERE is_deleted=false ORDER BY name;"', 15, "DB 节点状态")

# 2. 面板最近日志 (ERROR/WARN)
run('docker logs nexus-panel --tail 80 2>&1 | grep -iE "error|warn|panic|fatal|node" | tail -40', 15, "面板日志 (错误/节点相关)")

# 3. 面板最近启动时间 + 当前版本
run('docker exec nexus-panel ps -eo pid,etime,cmd | grep nexus-panel | head -3', 10, "panel 进程")

# 4. gRPC 端口监听
run('docker exec nexus-panel ss -tlnp 2>/dev/null | grep -E "9090|50051" || docker exec nexus-panel netstat -tlnp 2>/dev/null | grep -E "9090|50051"', 10, "gRPC 端口监听")

# 5. Redis 所有 node: 相关 key
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:*" | sort', 10, "Redis node:* keys")

# 6. 每个 heartbeat 的完整内容
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:heartbeat:*" | while read key; do echo "--- $key ---"; docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGETALL "$key"; done', 15, "所有 heartbeat 内容")

# 7. 面板日志最后 30 行 (全部)
run('docker logs nexus-panel --tail 30 2>&1', 10, "面板最后日志")

c.close()
print("=== done ===")
