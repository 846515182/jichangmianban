#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""快速看当前状态, 不动任何东西"""
import paramiko, sys, io, socket, socks
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
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)
print("=== SSH 连接成功 ===\n")

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:500])

# 1. 所有容器状态
run('docker ps -a --filter name=nexus --format "{{.Names}} | {{.Status}}"', 5, "1. 所有容器")

# 2. panel 当前日志最后 30 行
run('docker logs nexus-panel --tail 30 2>&1', 10, "2. panel 日志")

# 3. curl 测试
run('curl -s -o /dev/null -w "panel healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "3. panel healthz")
run('curl -s -o /dev/null -w "frontend: HTTP %{http_code}\\n" -k https://127.0.0.1/', 5, "4. frontend")
run('curl -s -o /dev/null -w "frontend login page: HTTP %{http_code}\\n" -k https://127.0.0.1/login', 5, "5. frontend login")

# 4. nginx 最近错误
run('docker logs nexus-frontend --tail 10 2>&1 | grep -E "error|502" | tail -5', 5, "6. nginx 最近错误")

c.close()
