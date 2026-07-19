#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署用户删除联动修复版"""
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
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "0. panel 当前状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "0b. healthz")

# 1. git pull
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 30, "1. git pull")

# 2. 先看历史软删用户(确认有重复 email 隐患)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, username, email, is_deleted FROM users WHERE is_deleted = true LIMIT 10"', 10, "2. 当前软删用户列表")

# 3. build panel + frontend
print("\n=== 3. build panel + frontend (耗时 5-10 分钟) ===")
run('cd /root/nexus-panel && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel frontend 2>&1 | tail -10', 900, "3. build")

# 4. 启动 helper 容器重建 panel + frontend
run('docker rm -f nexus-panel-restarter 2>&1; echo cleaned', 5, "4a. 清理旧 helper")

helper_cmd = (
    'docker run -d --name nexus-panel-restarter '
    '-v /var/run/docker.sock:/var/run/docker.sock '
    '-v /root/nexus-panel:/root/nexus-panel '
    '-w /root/nexus-panel '
    'alpine:latest '
    'sh -c "apk add --no-cache docker-cli docker-cli-compose >/dev/null 2>&1 && '
    'sleep 3 && '
    'docker compose up -d --no-deps panel frontend && '
    'docker rm -f nexus-panel-restarter"'
)
run(helper_cmd, 60, "4b. 启动 helper 容器")

# 等 helper 跑完
print("\n=== 等 30 秒 ===")
time.sleep(30)

# 5. 验证
run('docker ps --filter name=nexus --format "{{.Names}} | {{.Status}}"', 5, "5a. 所有容器状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "5b. healthz")
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^bce094c$\\" || echo NOT_FOUND"', 10, "5c. 二进制版本(应该是 bce094c)")

# 6. 看 migration 是否自动跑了(启动时会执行)
run('docker logs nexus-panel --tail 50 2>&1 | grep -E "migration|fix_soft_deleted|硬删除|email" | tail -10', 10, "6. migration 执行日志")

# 7. 再次查软删用户, 确认 email 已加 _del_ 后缀
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, username, email, is_deleted FROM users WHERE is_deleted = true LIMIT 10"', 10, "7. 软删用户 email 应已加 _del_ 后缀")

# 8. 验证 schema_migrations 有新记录
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT * FROM schema_migrations WHERE version LIKE \\"%2026_07_19%\\""', 10, "8. migrations 记录")

c.close()
print("\n=== 部署完成 ===")
