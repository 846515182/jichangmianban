#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""修复 dockerd 路径不一致: 建立符号链接 /usr/bin/dockerd -> /usr/local/bin/dockerd, 然后启动 docker, 启动 agent 容器"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

NODE_HOST = '38.59.246.203'
NODE_USER = 'root'

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

def run_via_panel(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    full = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 {NODE_USER}@{NODE_HOST} {repr(cmd)} 2>&1'
    _, o, _ = panel.exec_command(full, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    print(out)
    return out

# 1. 确认 dockerd 在 /usr/local/bin
run_via_panel('ls -la /usr/local/bin/dockerd 2>&1; /usr/local/bin/dockerd --version 2>&1', label="1. 确认 dockerd 位置")

# 2. 建立符号链接
run_via_panel('ln -sf /usr/local/bin/dockerd /usr/bin/dockerd && ls -la /usr/bin/dockerd', label="2. 建立符号链接")

# 3. 重置失败状态 + 启动 docker
run_via_panel('systemctl reset-failed docker; systemctl daemon-reload; systemctl start docker 2>&1; sleep 5; systemctl is-active docker 2>&1', label="3. 启动 docker")

# 4. 验证 docker 运行
time.sleep(3)
run_via_panel('docker info 2>&1 | head -20', label="4. docker info")

# 5. 查看 nexus 容器
run_via_panel('docker ps -a --format "table {{.Names}}\\t{{.Status}}\\t{{.Image}}" 2>&1', label="5. 所有容器")

# 6. 启动 agent 容器(如果存在)
run_via_panel('cd /root/node-agent-1e8b92e3 && docker compose -f docker-compose.node.yml --env-file .env.node up -d --build 2>&1 | tail -40', timeout=300, label="6. 启动 agent 容器")

# 7. 等待容器启动
time.sleep(15)
run_via_panel('docker ps --format "table {{.Names}}\\t{{.Status}}\\t{{.Ports}}" 2>&1', label="7. 容器状态")

# 8. 查看 agent 日志
run_via_panel('docker logs --tail 50 nexus-agent-1e8b92e3 2>&1', label="8. agent 日志")

panel.close()
print("\n✓ 完成")
