#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急修复 502: 杀掉卡住的 build, 清理 state, 部署最新修复版"""
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

# 1. 看当前状态
run('docker ps -a --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 10, "1. 当前 panel 容器状态")
run('cat /root/nexus-panel/.update-state/git-pull.state 2>/dev/null', 5, "1b. 当前 git-pull state")
run('ps aux | grep -E "docker.*build|buildkit|git.*pull" | grep -v grep', 10, "1c. 是否有 docker build / git pull 进程在跑")

# 2. 清理已死的 git-pull state 文件(让 cron 知道没在更新中)
run('rm -f /root/nexus-panel/.update-state/git-pull.state && echo "已清理 git-pull.state"', 5, "2. 清理卡死的 git-pull state")

# 3. 看是否有 docker buildkitd 进程在跑(可能还在 build)
run('docker ps --filter name=buildx --format "{{.Names}} | {{.Status}}" 2>&1', 5, "3a. buildx 容器状态")
# 停掉可能的 buildkitd 容器(避免 build 任务残留)
run('docker stop $(docker ps -q --filter ancestor=moby/buildkit) 2>/dev/null; echo "buildkit stop done"', 30, "3b. 停掉残留 buildkit")

# 4. git pull 拉最新代码(83dfa1a)
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 60, "4. git pull 拉最新修复版")

# 5. 重新 build panel 镜像(用新代码, --build-arg VERSION)
print("\n=== 5. 重新构建 panel 镜像(5-10 分钟) ===")
run('cd /root/nexus-panel && VER=$(git rev-parse --short HEAD) && echo "Using VERSION=$VER" && docker compose build --build-arg VERSION=$VER panel 2>&1 | tail -15', 900, None)

# 6. 重建 panel 容器
run('cd /root/nexus-panel && docker compose up -d --no-deps panel 2>&1', 60, "6. 重建 panel 容器")

# 7. 等 15 秒
print("\n=== 等待 15 秒 ===")
time.sleep(15)

# 8. 验证
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "8a. panel 容器状态")
run('docker logs nexus-panel --tail 30 2>&1', 30, "8b. panel 启动日志")
run('curl -s -o /dev/null -w "panel: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 10, "8c. panel healthz")
run('curl -s -o /dev/null -w "frontend: HTTP %{http_code}\\n" -k https://127.0.0.1/', 10, "8d. frontend 整体")

c.close()
print("\n=== 修复完成 ===")
