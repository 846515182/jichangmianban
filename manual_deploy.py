#!/usr/bin/env python3
"""构建新 panel 镜像 + 重启容器 (手动部署一次让自动重启代码生效)"""
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

def run(cmd, timeout=600, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:500])
    print()

# 1. 确认代码已最新
run('cd /root/nexus-panel && git log --oneline -3', 10, "服务器仓库版本")

# 2. 构建新 panel 镜像 (用 backend/Dockerfile)
run('cd /root/nexus-panel && docker build -t nexus-panel:latest ./backend 2>&1 | tail -30', 600, "构建新镜像 (5-10 分钟)")

# 3. 重启 panel 容器 (用新镜像)
run('cd /root/nexus-panel && docker compose up -d panel 2>&1', 60, "重启 panel 容器")

# 4. 等待 10 秒让容器起来
import time
time.sleep(10)

# 5. 确认容器状态
run('docker ps --filter name=nexus-panel --format "table {{.Names}}\\t{{.Status}}\\t{{.Image}}"', 10, "容器状态")

# 6. 看新二进制时间
run('docker exec nexus-panel ls -la /app/nexus-panel 2>&1', 10, "新二进制时间")

# 7. 看启动日志
run('docker logs nexus-panel --tail 15 2>&1', 10, "启动日志")

c.close()
print("=== done ===")
