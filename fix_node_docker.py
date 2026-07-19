#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""启动节点 docker, 重启 agent 容器, 查看日志"""
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
    """通过面板免密 SSH 到节点执行命令"""
    print(f"\n=== {label or cmd[:60]} ===")
    full = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 {NODE_USER}@{NODE_HOST} {repr(cmd)} 2>&1'
    _, o, _ = panel.exec_command(full, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    print(out)
    return out

# 1. 检查 docker 服务状态
run_via_panel('systemctl status docker --no-pager 2>&1 | head -20; echo "---"; systemctl is-enabled docker 2>&1', label="1. docker 服务状态")

# 2. 启动 docker
run_via_panel('systemctl start docker 2>&1; sleep 3; systemctl status docker --no-pager 2>&1 | head -10', label="2. 启动 docker")

# 3. 设置开机自启
run_via_panel('systemctl enable docker 2>&1', label="3. docker 开机自启")

# 4. 等待 docker 就绪
time.sleep(3)
run_via_panel('docker info 2>&1 | head -10', label="4. docker 就绪检查")

# 5. 查看所有 nexus 容器
run_via_panel('docker ps -a --format "table {{.Names}}\\t{{.Status}}\\t{{.Image}}" | head', label="5. 容器列表")

# 6. 查看部署目录
run_via_panel('ls -la /root/node-agent-1e8b92e3/ 2>/dev/null | head -15', label="6. 部署目录")

# 7. 检查 .env.node
run_via_panel('cat /root/node-agent-1e8b92e3/.env.node 2>/dev/null', label="7. .env.node 配置")

panel.close()
print("\n✓ 完成")
