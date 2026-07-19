#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急: 拉起 panel + 看 helper 失败原因 + 用 docker compose up 让 restart policy 生效"""
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

def run(cmd, timeout=120, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 看 helper 失败日志
run('docker logs nexus-panel-restarter 2>&1 | tail -30', 10, "1. helper 失败日志")

# 2. 紧急拉起 panel
run('docker start nexus-panel 2>&1', 30, "2. 紧急 start panel")
time.sleep(10)
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "2b. panel 状态")
run('curl -s -o /dev/null -w "healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "2c. healthz")

# 3. 清理失败的 helper
run('docker rm -f nexus-panel-restarter 2>&1', 10, "3. 清理 helper")

c.close()
print("\n=== 完成 ===")
