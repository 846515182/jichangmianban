#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署修复版本: 用 --build-arg VERSION 重新构建 panel 镜像"""
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
        print("[ERR]", err.rstrip()[:1500])

# 0. 部署前状态
run('cd /root/nexus-panel && git log -1 --oneline', 10, "0a. 部署前服务器代码版本")
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "0b. 部署前 panel 容器状态")

# 1. git pull 拉最新代码
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git log -1 --oneline', 60, "1. git pull 拉最新代码")

# 2. 取本次部署版本号
run('cd /root/nexus-panel && git rev-parse --short HEAD', 10, "2. 本次部署版本号(git HEAD short)")

# 3. 构建新 panel 镜像, 显式传 --build-arg VERSION
print("\n=== 3. 构建新 panel 镜像 (5-10 分钟, 显式传 --build-arg VERSION) ===")
run('cd /root/nexus-panel && VER=$(git rev-parse --short HEAD) && echo "Using VERSION=$VER" && docker compose build --build-arg VERSION=$VER panel 2>&1 | tail -25', 900, None)

# 4. 重建 panel 容器用新镜像
run('cd /root/nexus-panel && docker compose up -d --no-deps panel 2>&1', 60, "4. 重建 panel 容器")

# 5. 等 20 秒让容器启动
print("\n=== 等待 20 秒让容器启动 ===")
time.sleep(20)

# 6. 验证容器状态
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "6a. 部署后 panel 容器状态")
run('docker logs nexus-panel --tail 50 2>&1 | grep -E "版本一致性兜底|定时任务已启动|Nexus-Panel 启动中|ERROR|panic"', 30, "6b. 启动日志")

# 7. 验证 app.Version (从二进制 strings 提取, 应为本次 git HEAD)
run('cd /root/nexus-panel && echo "代码 HEAD: $(git rev-parse --short HEAD)"', 10, "7a. 代码 HEAD")
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^[a-f0-9]{7}$\\" | sort -u | head -5"', 30, "7b. 运行二进制 strings 提取的版本号(应包含代码 HEAD)")

# 8. 验证 containers 接口
run('curl -s -o /dev/null -w "HTTP %{http_code}\\n" http://127.0.0.1:8080/api/v1/admin/system/containers', 10, "8. containers 接口 HTTP 状态码 (401=正常)")

# 9. 等 3 分 30 秒让启动宽限期过去, 看版本一致性 cron 第一次巡检日志
# (启动宽限期 3 分钟, 之后会立即跑一次 CheckVersionConsistency)
print("\n=== 等待 3 分 30 秒让启动宽限期过去, 然后看版本一致性 cron 第一次巡检日志 ===")
print("(启动后 3 分钟内不巡检, 给一键更新流程留时间, 等 3:30 后看 cron 巡检结果)")
time.sleep(210)
run('docker logs nexus-panel --since 5m 2>&1 | grep -E "版本一致性兜底|巡检"', 30, "9. 版本一致性 cron 巡检日志(3:30 后)")

# 10. 完整最新 20 行日志
run('docker logs nexus-panel --tail 20 2>&1', 30, "10. 完整最新日志")

c.close()
print("\n=== 部署完成 ===")
