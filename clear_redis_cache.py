#!/usr/bin/env python3
"""清理 Redis 节点速度快照/心跳缓存 (带密码)"""
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

def run(cmd, timeout=30, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 测试连接
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning PING', 10, "Redis 连接测试")

# 用 KEYS 查看相关 key 数量 (SCAN 在管道里有问题, 改用 KEYS)
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:speed_snap:*" | wc -l', 15, "清理前 speed_snap key 数")
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:heartbeat:*" | wc -l', 15, "清理前 heartbeat key 数")

# 清理 node:speed_snap:* (用管道 + DEL, 先把 key 列表传给 xargs)
run(f'docker exec nexus-redis sh -c \'redis-cli -a "{REDIS_PWD}" --no-auth-warning --scan --pattern "node:speed_snap:*" | xargs -r redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL\'', 30, "清理 node:speed_snap:*")
run(f'docker exec nexus-redis sh -c \'redis-cli -a "{REDIS_PWD}" --no-auth-warning --scan --pattern "node:heartbeat:*" | xargs -r redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL\'', 30, "清理 node:heartbeat:*")

# 同时清理可能存在的其他流量相关 key
run(f'docker exec nexus-redis sh -c \'redis-cli -a "{REDIS_PWD}" --no-auth-warning --scan --pattern "traffic:*" | xargs -r redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL\'', 30, "清理 traffic:*")
run(f'docker exec nexus-redis sh -c \'redis-cli -a "{REDIS_PWD}" --no-auth-warning --scan --pattern "node:configver:*" | xargs -r redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL\'', 30, "清理 node:configver:*")
run(f'docker exec nexus-redis sh -c \'redis-cli -a "{REDIS_PWD}" --no-auth-warning --scan --pattern "node:usershash:*" | xargs -r redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL\'', 30, "清理 node:usershash:*")

# 验证
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:speed_snap:*" | wc -l', 10, "清理后 speed_snap key 数")
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:heartbeat:*" | wc -l', 10, "清理后 heartbeat key 数")

# Redis 整体 key 统计
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning DBSIZE', 10, "Redis 剩余 key 总数")

c.close()
print("=== done ===")
