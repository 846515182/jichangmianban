#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署 v3 保守模式 cron 到服务器"""
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

def run(cmd, timeout=300, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 先确认 panel 还活着
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "1. panel 当前状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "1b. healthz")

# 2. git pull 拉新代码
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 30, "2. git pull")

# 3. 验证新代码确实有"保守模式"关键字
run('grep -c "保守模式" /root/nexus-panel/backend/internal/service/cron_service.go', 5, "3. 验证保守模式代码已到位")

# 4. 看 docker-compose.yml 里 panel 的 build-arg VERSION 是否正确(用于注入版本号)
run('grep -A 5 "panel:" /root/nexus-panel/docker-compose.yml | head -20', 5, "4a. docker-compose panel 配置")
run('grep -E "VERSION|build-arg|ldflags" /root/nexus-panel/backend/Dockerfile 2>&1', 5, "4b. Dockerfile VERSION 注入")

# 5. build panel 镜像(注入新版本号)
print("\n=== 5. 开始 build panel (耗时 3-5 分钟, 请等) ===")
run('cd /root/nexus-panel && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel 2>&1 | tail -20', 600, "5. docker compose build panel")

# 6. 重建 panel 容器
run('cd /root/nexus-panel && docker compose up -d --no-deps panel 2>&1', 60, "6. docker compose up -d panel")

# 7. 等 15 秒让容器起来
print("\n=== 等 15 秒 ===")
time.sleep(15)

# 8. 验证
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "7a. panel 状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "7b. healthz")
run('docker logs nexus-panel --tail 20 2>&1 | grep -E "版本一致性|定时任务已启动|HTTP 服务启动|收到退出信号" | tail -10', 10, "7c. 启动日志")

# 9. 验证版本号已注入
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^124b45f$\\" || echo NOT_FOUND_124b45f"', 10, "7d. 二进制版本号验证")

# 10. 清理之前的禁用标记(我之前手动 reset 到 83dfa1a 留的)
run('rm -f /root/nexus-panel/.update-state/VERSION_CRON_DISABLED 2>&1; echo "已清理禁用标记"', 5, "8. 清理禁用标记")

c.close()
print("\n=== 部署完成 ===")
