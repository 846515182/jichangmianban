#!/usr/bin/env python3
"""重置节点流量计数 + 清理 Redis 速度快照缓存
保留 nodes.traffic_limit (节点配置) 和其他字段, 只清 traffic_used
同时清掉 node:speed_snap:* / node:heartbeat:* 让面板速度显示归零"""
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

def run(cmd, timeout=30, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 清理前: 查看节点当前流量
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, name, traffic_used, traffic_limit FROM nodes ORDER BY name LIMIT 10;"', 15, "清理前 nodes.traffic_used")

# 1. 重置 nodes.traffic_used = 0 (保留 traffic_limit 和其他配置)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "UPDATE nodes SET traffic_used = 0 WHERE traffic_used > 0;"', 30, "重置 nodes.traffic_used = 0")

# 清理后验证
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT COUNT(*) FILTER (WHERE traffic_used > 0) AS nodes_with_traffic, COUNT(*) AS total_nodes FROM nodes;"', 15, "清理后 nodes.traffic_used")

# 2. 清理 Redis 速度快照 + 心跳缓存 (让面板显示 0 B/s)
run('docker exec nexus-redis redis-cli --scan --pattern "node:speed_snap:*" | xargs -r docker exec -i nexus-redis redis-cli DEL', 30, "清理 Redis node:speed_snap:*")
run('docker exec nexus-redis redis-cli --scan --pattern "node:heartbeat:*" | xargs -r docker exec -i nexus-redis redis-cli DEL', 30, "清理 Redis node:heartbeat:*")

# 3. 验证 Redis 中相关 key 是否清空
run('docker exec nexus-redis redis-cli --scan --pattern "node:speed_snap:*" | wc -l', 10, "Redis node:speed_snap:* 剩余 key 数")
run('docker exec nexus-redis redis-cli --scan --pattern "node:heartbeat:*" | wc -l', 10, "Redis node:heartbeat:* 剩余 key 数")

# 4. 保留项再次确认
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT COUNT(*) AS nodes_count, COUNT(*) FILTER (WHERE is_enabled) AS enabled, COUNT(*) FILTER (WHERE online) AS online FROM nodes;"', 10, "节点配置 (保留)")

c.close()
print("=== done ===")
