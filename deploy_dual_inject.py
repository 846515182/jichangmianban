#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署 caefb72: 双重注入版本号 + 启动日志打印"""
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

def run(cmd, timeout=900, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:1500])

# 1. git pull
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 60, "1. git pull")

# 2. build (用 --build-arg VERSION)
print("\n=== 2. 构建新 panel 镜像(双重注入 ldflags) ===")
run('cd /root/nexus-panel && VER=$(git rev-parse --short HEAD) && echo "Using VERSION=$VER" && docker compose build --build-arg VERSION=$VER panel 2>&1 | tail -15', 900, None)

# 3. 重建容器
run('cd /root/nexus-panel && docker compose up -d --no-deps panel 2>&1', 60, "3. 重建 panel 容器")

# 4. 等 15 秒
print("\n=== 等待 15 秒让容器启动 ===")
time.sleep(15)

# 5. 看启动日志(必须看到 [VERSION] main.Version="caefb72" app.Version="caefb72")
run('docker logs nexus-panel --tail 50 2>&1 | grep -E "VERSION|版本一致性兜底|定时任务已启动|Nexus-Panel 启动"', 30, "5. 启动日志(必须看到 [VERSION] 行)")

# 6. 在二进制里直接 grep 版本号
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^caefb72$\\""', 30, "6. 在二进制里查找 caefb72")

# 7. 容器状态
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "7. 容器状态")

# 8. 等 3 分 30 秒看 cron 巡检日志
print("\n=== 等待 3 分 30 秒让启动宽限期过去, 看 cron 巡检日志 ===")
time.sleep(210)
run('docker logs nexus-panel --since 6m 2>&1 | grep -E "版本一致性兜底|巡检"', 30, "8. 版本一致性 cron 日志(启动 3:30 后)")

# 9. 完整最新 15 行
run('docker logs nexus-panel --tail 15 2>&1', 30, "9. 完整最新日志")

c.close()
print("\n=== 部署完成 ===")
