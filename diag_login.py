#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""排查注册后登录失败"""
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

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 看 panel 最近 30 行日志, 找注册/登录请求
run('docker logs nexus-panel --tail 50 2>&1 | grep -E "register|login|auth|error|ERROR|panic" | tail -30', 10, "1. 最近注册/登录相关日志")

# 2. 看 users 表当前数据
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, username, email, status, is_deleted, created_at FROM users ORDER BY created_at DESC LIMIT 5"', 10, "2. users 表最近 5 条")

# 3. 看是否有 login_audit 记录
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT * FROM login_audit ORDER BY created_at DESC LIMIT 5"', 10, "3. 最近登录审计")

# 4. 直接查最新注册的用户状态
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, username, email, status, password_hash IS NOT NULL AS has_pwd, created_at FROM users WHERE created_at > NOW() - INTERVAL \'10 minutes\'"', 10, "4. 最近 10 分钟注册的用户")

# 5. 模拟一次登录请求看返回(用最新用户名)
# 先查用户名
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -t -c "SELECT username FROM users ORDER BY created_at DESC LIMIT 1"', 10, "5. 最新用户名")

c.close()
print("\n=== 完成 ===")
