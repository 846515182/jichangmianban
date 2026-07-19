#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""杀僵尸 dockerd 进程, 删除 pid 文件, 启动 docker, 启动 agent"""
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

panel = paramiko.SSHClient()
panel.set_missing_host_key_policy(paramiko.AutoAddPolicy())
panel.connect(PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PWD,
              sock=make_sock(PANEL_HOST, PANEL_PORT), timeout=30, banner_timeout=30, auth_timeout=30)

def run_via_panel(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    full = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 {NODE_USER}@{NODE_HOST} {repr(cmd)} 2>&1'
    _, o, _ = panel.exec_command(full, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    print(out)
    return out

# 1. 检查僵尸进程
run_via_panel('ps aux | grep -E "dockerd|containerd" | grep -v grep 2>&1', label="1. dockerd/containerd 进程")
run_via_panel('ls -la /var/run/docker.pid /var/run/docker.sock 2>&1; cat /var/run/docker.pid 2>/dev/null', label="2. pid 文件")

# 2. 杀僵尸进程 + 清理
run_via_panel('pkill -9 dockerd 2>/dev/null; sleep 2; rm -f /var/run/docker.pid; ps aux | grep dockerd | grep -v grep', label="3. 杀僵尸 dockerd + 清理 pid")

# 3. 重置 docker 服务并启动
run_via_panel('systemctl reset-failed docker; systemctl reset-failed docker.socket; systemctl start docker.socket; systemctl start docker 2>&1; sleep 5; systemctl is-active docker 2>&1', label="4. 启动 docker")

# 4. 验证 docker 运行
time.sleep(3)
run_via_panel('docker info 2>&1 | head -25', label="5. docker info")

# 5. 查看容器
run_via_panel('docker ps -a --format "table {{.Names}}\\t{{.Status}}\\t{{.Image}}" 2>&1', label="6. 容器列表")

# 6. 启动 agent 容器
run_via_panel('cd /root/node-agent-1e8b92e3 && docker compose -f docker-compose.node.yml --env-file .env.node up -d --build 2>&1 | tail -40', timeout=300, label="7. 启动 agent 容器")

# 7. 等待容器启动
time.sleep(20)
run_via_panel('docker ps --format "table {{.Names}}\\t{{.Status}}\\t{{.Ports}}" 2>&1', label="8. 容器状态")

# 8. agent 日志
run_via_panel('docker logs --tail 60 nexus-agent-1e8b92e3 2>&1', label="9. agent 日志")

panel.close()
print("\n✓ 完成")
