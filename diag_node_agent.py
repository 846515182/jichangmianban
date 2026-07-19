#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""登录节点服务器查 agent 状态"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

# 节点服务器(美国01): 38.59.246.203
# 但我们没节点服务器的 SSH 凭证, 只能从面板侧看
# 退而求其次: 从面板服务器 SSH 到节点(如果配了免密), 或者直接看面板侧的 gRPC 日志

HOST = '192.129.242.242'  # 面板服务器
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
print("=== SSH 连接成功(面板服务器) ===\n")

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 尝试 SSH 到节点服务器(用面板服务器的 SSH key, 看是否配了免密)
run('ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o PasswordAuthentication=no root@38.59.246.203 "docker ps --format \'{{.Names}} | {{.Status}}\' | grep -i nexus; echo \'---\'; docker logs nexus-node-agent --tail 30 2>&1 | tail -20" 2>&1', 15, "1. SSH 到节点服务器查 agent 状态")

# 2. 如果免密不行, 尝试用面板数据库里节点的密码字段(如果有)
# 先看节点表有没有 ssh 凭证字段
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "\\d nodes" 2>&1 | head -30', 10, "2. nodes 表结构(看有无 ssh 凭证字段)")

# 3. 直接从面板侧看 gRPC 连接状态
# gRPC server 端可以看到当前连接的客户端
run('docker exec nexus-panel sh -c "ss -tnp 2>/dev/null | grep 50051 || netstat -tnp 2>/dev/null | grep 50051"', 10, "3. 面板侧 gRPC 50051 当前连接")

# 4. 看 gRPC server 启动后的所有连接日志
run('docker logs nexus-panel 2>&1 | grep -iE "grpc.*connect|grpc.*stream|node.*register|节点.*注册" | tail -20', 10, "4. gRPC 连接/注册日志")

c.close()
print("\n=== 完成 ===")
