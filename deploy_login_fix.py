#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署登录锁定修复版"""
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

def run(cmd, timeout=600, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 0. 当前状态
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "0. panel 状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "0b. healthz")

# 1. 清空 Redis 登录锁(让用户能立即登录)
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning DEL loginfail:acc:ceshi1 loginlock:acc:ceshi1 loginfail:acc:admin:ceshi1 loginlock:acc:admin:ceshi1 2>&1', 10, "1. 清空 ceshi1 的登录锁")

# 2. git pull
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 30, "2. git pull")

# 3. build panel
print("\n=== 3. build panel (耗时 3-5 分钟) ===")
run('cd /root/nexus-panel && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel 2>&1 | tail -5', 600, "3. build panel")

# 4. helper 容器重建 panel
run('docker rm -f nexus-panel-restarter 2>&1; echo cleaned', 5, "4a. 清理旧 helper")
helper_cmd = (
    'docker run -d --name nexus-panel-restarter '
    '-v /var/run/docker.sock:/var/run/docker.sock '
    '-v /root/nexus-panel:/root/nexus-panel '
    '-w /root/nexus-panel '
    'alpine:latest '
    'sh -c "apk add --no-cache docker-cli docker-cli-compose >/dev/null 2>&1 && '
    'sleep 3 && '
    'docker compose up -d --no-deps panel && '
    'docker rm -f nexus-panel-restarter"'
)
run(helper_cmd, 60, "4b. 启动 helper 容器")

print("\n=== 等 25 秒 ===")
time.sleep(25)

# 5. 验证
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "5a. panel 状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "5b. healthz")
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^9eec620$\\" || echo NOT_FOUND"', 10, "5c. 二进制版本(应该是 9eec620)")

# 6. 再清一次 Redis(部署过程中可能有新的失败计数)
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning EVAL "return redis.call(\'keys\', \'loginfail:*\')" 0 2>&1', 10, "6a. 当前所有登录失败计数 keys")
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning EVAL "return redis.call(\'keys\', \'loginlock:*\')" 0 2>&1', 10, "6b. 当前所有登录锁 keys")
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning EVAL "local ks=redis.call(\'keys\',\'loginlock:*\') for i=1,#ks do redis.call(\'del\',ks[i]) end local ks2=redis.call(\'keys\',\'loginfail:*\') for i=1,#ks2 do redis.call(\'del\',ks2[i]) end return #ks+#ks2" 0 2>&1', 10, "6c. 清空所有登录锁/失败计数")

c.close()
print("\n=== 部署完成, 请重新登录 ===")
