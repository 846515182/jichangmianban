#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署 + 测试 helper 容器方案(不 kill panel 测试, 避免再次闯祸)"""
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

# 0. 当前 panel 状态(应该活着)
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "0. panel 当前状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "0b. healthz")

# 1. git pull
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 30, "1. git pull")

# 2. build 新镜像(注入版本号 410a30f)
print("\n=== 2. build panel (耗时 3-5 分钟) ===")
run('cd /root/nexus-panel && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel 2>&1 | tail -5', 600, "2. build panel")

# 3. 启动 helper 容器(模拟 gitPull 流程最后一步)
run('docker rm -f nexus-panel-restarter 2>&1; echo cleaned', 5, "3a. 清理旧 helper")

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
run(helper_cmd, 60, "3b. 启动 helper 容器")

# 等 helper 跑完 + panel 启动
print("\n=== 等 30 秒让 helper 跑完 + panel 重建 + 启动 ===")
time.sleep(30)

# 4. 验证
run('docker ps -a --filter name=restarter --format "{{.Names}} | {{.Status}}"', 5, "4a. helper 容器(应该已自动 rm 或 Exited 0)")
run('docker logs nexus-panel-restarter 2>&1 | tail -20', 10, "4b. helper 日志(看是否成功)")
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "4c. panel 状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "4d. panel healthz")
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^410a30f$\\" || echo NOT_FOUND"', 10, "4e. 二进制版本验证(应该是 410a30f)")
run('docker inspect nexus-panel --format "{{.HostConfig.RestartPolicy.Name}}"', 5, "4f. panel restart policy(应该是 unless-stopped)")

# 5. 看新 panel 启动日志
run('docker logs nexus-panel --tail 10 2>&1', 10, "4g. panel 启动日志")

c.close()
print("\n=== 部署完成 ===")
