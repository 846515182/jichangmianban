#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""排查 502 错误, 检查 panel 容器状态和更新流程"""
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

def run(cmd, timeout=60, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:1500])

# 1. 所有容器状态
run('docker ps -a --filter name=nexus --format "{{.Names}} | {{.Image}} | {{.Status}} | {{.CreatedAt}}"', 10, "1. 所有 nexus 容器状态")

# 2. panel 容器最近 80 行日志
run('docker logs nexus-panel --tail 80 2>&1', 30, "2. panel 容器最近 80 行日志")

# 3. 看 git pull 状态文件(是否正在更新中)
run('ls -la /root/nexus-panel/.update-state/ 2>&1 && echo "---" && cat /root/nexus-panel/.update-state/git-pull.state 2>/dev/null && echo "" && echo "---log tail---" && tail -30 /root/nexus-panel/.update-state/git-pull.log 2>/dev/null', 10, "3. 一键更新状态文件")

# 4. 看是否有 docker build / git 进程在跑
run('ps auxf 2>&1 | grep -E "docker|git|nexus-panel" | grep -v grep | head -30', 10, "4. docker/git 相关进程")

# 5. nginx 错误日志最近 30 行
run('docker logs nexus-frontend --tail 30 2>&1 | grep -E "error|502|upstream"', 10, "5. nginx 最近错误日志")

# 6. 直接 curl 后端
run('curl -s -o /dev/null -w "panel: HTTP %{http_code} (connect=%{time_connect}s total=%{time_total}s)\\n" http://127.0.0.1:8080/healthz', 10, "6. 直接 curl 后端 panel")

# 7. curl 前端
run('curl -s -o /dev/null -w "frontend: HTTP %{http_code}\\n" -k https://127.0.0.1/', 10, "7. curl 前端")

# 8. 看 panel 容器是否在重启循环
run('docker events --since 10m --until 1s --filter container=nexus-panel 2>&1 | tail -20', 15, "8. 最近 10 分钟 panel 容器事件")

c.close()
print("\n=== 排查完成 ===")
