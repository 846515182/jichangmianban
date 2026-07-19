#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急排查当前服务器状态"""
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
run('docker ps -a --filter name=nexus --format "{{.Names}} | {{.Status}} | {{.CreatedAt}}"', 10, "1. 所有 nexus 容器状态")

# 2. panel 容器最近 100 行日志
run('docker logs nexus-panel --tail 100 2>&1', 30, "2. panel 容器最近 100 行日志")

# 3. nginx 错误日志
run('docker logs nexus-frontend --tail 50 2>&1 | grep -E "error|502|upstream|connect" | tail -20', 10, "3. nginx 错误日志")

# 4. git-pull state
run('cat /root/nexus-panel/.update-state/git-pull.state 2>/dev/null; echo ""; ls -la /root/nexus-panel/.update-state/ 2>&1', 5, "4. git-pull state")

# 5. 当前代码版本
run('cd /root/nexus-panel && git rev-parse --short HEAD && git log -3 --oneline', 5, "5. 当前代码版本")

# 6. 是否有 build 进程在跑
run('ps aux | grep -E "docker.*build|buildkit|git.*pull|nexus-panel" | grep -v grep', 10, "6. 相关进程")

# 7. curl 测试
run('curl -s -o /dev/null -w "panel: HTTP %{http_code} (time=%{time_total}s)\\n" http://127.0.0.1:8080/healthz', 10, "7a. panel healthz")
run('curl -s -o /dev/null -w "frontend: HTTP %{http_code}\\n" -k https://127.0.0.1/', 10, "7b. frontend")

# 8. 磁盘/内存
run('df -h / && echo "---" && free -m', 10, "8. 磁盘/内存")

c.close()
print("\n=== 排查完成 ===")
