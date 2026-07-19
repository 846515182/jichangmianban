#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""删除面板上缓存的旧 agent 二进制, 强制 auto-deploy 重新编译(含本次 fatalShutdown 修复)"""
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

# 1. 查看当前缓存的 agent 二进制
run('ls -la /root/nexus-panel/node_agent/agent 2>&1', label="1. 当前缓存的 agent 二进制")
run('docker exec nexus-panel ls -la /app/node_agent/agent 2>&1', label="2. 容器内可见的 agent 二进制")

# 2. 删除缓存的二进制, 强制下次部署重新编译
run('rm -f /root/nexus-panel/node_agent/agent && echo "删除成功"', label="3. 删除宿主机上的缓存二进制")
run('docker exec nexus-panel ls /app/node_agent/agent 2>&1', label="4. 验证容器内也已删除(挂载)")

# 3. 预先编译新二进制(避免用户点重新部署时等 2-3 分钟编译)
print("\n=== 5. 预编译新 agent 二进制(包含 fatalShutdown 修复) ===")
build_cmd = (
    'docker run --rm '
    '-v /root/nexus-panel/node_agent:/build -w /build '
    'golang:1.21-alpine '
    'sh -c \'apk add --no-cache git >/dev/null 2>&1; go mod download && '
    'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /build/agent . 2>&1\' && '
    'ls -lh /root/nexus-panel/node_agent/agent'
)
run(build_cmd, timeout=300, label="5. 预编译新 agent 二进制")

# 4. 验证新二进制已生成
run('ls -la /root/nexus-panel/node_agent/agent 2>&1 && file /root/nexus-panel/node_agent/agent 2>&1', label="6. 验证新二进制")

# 5. 验证新二进制包含修复(用 strings 查找关键字符串)
run('strings /root/nexus-panel/node_agent/agent 2>/dev/null | grep -E "recovery|tryRecoverFromFatal|fatalShutdown" | head -10', label="7. 验证新二进制含修复字符串")

panel.close()
print("\n✓ 完成 - 用户可以在面板后台点「重新部署节点」, 会用刚编译的新二进制(含 fatalShutdown 修复)")
