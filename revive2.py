#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急: 拉起 panel + 移除兜底 cron(它自己就是问题源头)"""
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
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)
print("=== SSH 连接成功 ===\n")

def run(cmd, timeout=60, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:500])

# 1. 先看 docker compose up -d 为什么只 Create 不 Start
# 用 --wait 选项让 docker compose 等到容器 healthy
# 但先手动 start 拉起来
run('docker start nexus-panel 2>&1', 30, "1. 手动 start panel")
time.sleep(8)
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "1b. 启动后状态")
run('curl -s -o /dev/null -w "panel: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "1c. healthz")

# 2. 看下 docker compose 版本
run('docker compose version 2>&1', 5, "2. docker compose 版本")

# 3. 看 panel 容器为什么 docker compose up -d 之后不 start
# 这可能是因为 docker compose 走的是 recreate 流程, 旧容器还在, 新容器 Created 后
# 等 Start 但 Start 失败. 看下事件
run('docker events --since 15m --until 1s --filter container=nexus-panel 2>&1 | tail -20', 15, "3. panel 容器最近事件")

c.close()
print("\n=== 完成 ===")
