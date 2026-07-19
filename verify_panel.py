#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""验证 panel 容器内 node_agent 代码, 检查部署机制"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock(host, port):
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((host, port))
    return s

print("=== 登录面板 ===")
panel = paramiko.SSHClient()
panel.set_missing_host_key_policy(paramiko.AutoAddPolicy())
panel.connect(PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PWD,
              sock=make_sock(PANEL_HOST, PANEL_PORT), timeout=30, banner_timeout=30, auth_timeout=30)
print("✓ 面板登录成功")

def run(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    _, o, e = panel.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    if out: print(out)
    if err.strip(): print("STDERR:", err[:500])
    return out

# 1. 检查容器列表
run('docker ps --format "table {{.Names}}\t{{.Status}}" | head', label="1. 容器列表")

# 2. 检查 nexus-panel 容器内 node_agent
run('docker exec nexus-panel ls /app/node_agent/ 2>&1 | head', label="2. 容器内 node_agent 目录")
run('docker exec nexus-panel grep -c "tryRecoverFromFatal" /app/node_agent/main.go 2>&1', label="3. 容器内可见修复行数")
run('docker exec nexus-panel grep "recoverTicker := time.NewTicker" /app/node_agent/main.go 2>&1', label="4. 容器内可见 recoverTicker")

# 3. 检查节点当前状态(数据库)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, name, server_address, port, online, last_seen_at, version FROM nodes WHERE is_deleted=false;" 2>&1', label="5. 节点状态(数据库)")

# 4. 验证 panel 还在跑(用健康检查)
run('curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/healthz', label="6. Panel 健康检查")

# 5. 检查 gRPC 端口 50051 是否监听
run('ss -tlnp 2>/dev/null | grep 50051 || netstat -tlnp 2>/dev/null | grep 50051', label="7. gRPC 50051 监听状态")

# 6. 检查 panel 是否需要重启来加载新代码(node_agent 是挂载的, 不需要重启)
run('docker exec nexus-panel cat /app/node_agent/main.go | head -5', label="8. 容器内 main.go 前5行")

panel.close()
print("\n✓ 完成")
