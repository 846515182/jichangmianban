#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""SSH 到面板服务器, git pull 最新代码, 验证 node_agent 修复已生效"""
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

# 1. 检查 git 当前版本
run('cd /root/nexus-panel && git log --oneline -3 && git status -s | head', label="1. 面板当前 git 状态")

# 2. git pull 拉最新代码
run('cd /root/nexus-panel && git pull origin main 2>&1 | tail -20', label="2. git pull 最新代码")

# 3. 验证 node_agent/main.go 已包含修复
run('cd /root/nexus-panel && grep -A2 "tryRecoverFromFatal" node_agent/main.go | head -10', label="3. 验证 tryRecoverFromFatal 已在代码中")
run('cd /root/nexus-panel && grep "recoverTicker" node_agent/main.go', label="4. 验证 recoverTicker 已在代码中")
run('cd /root/nexus-panel && git log --oneline -3', label="5. 拉取后 git log")

# 4. 验证 panel 容器能看到新代码(通过挂载)
run('docker exec panel ls /app/node_agent/ 2>&1 | head -5', label="6. 验证 panel 容器挂载的 node_agent")
run('docker exec panel grep -A1 "tryRecoverFromFatal" /app/node_agent/main.go 2>&1 | head -5', label="7. 验证容器内可见修复")

panel.close()
print("\n✓ 完成")
