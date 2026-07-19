#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""诊断并修复节点 dockerd 启动失败 (status=203/EXEC)"""
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

# 1. 检查 dockerd 二进制是否存在/可执行
run_via_panel('ls -la /usr/bin/dockerd 2>&1; file /usr/bin/dockerd 2>&1; which dockerd 2>&1', label="1. dockerd 二进制")

# 2. 检查 docker.service 文件
run_via_panel('cat /lib/systemd/system/docker.service 2>&1 | head -25', label="2. docker.service")

# 3. 检查 containerd
run_via_panel('ls -la /usr/bin/containerd* 2>&1; systemctl status containerd --no-pager 2>&1 | head -10', label="3. containerd 状态")

# 4. 检查 journalctl 详细错误
run_via_panel('journalctl -u docker.service --no-pager -n 30 2>&1', label="4. docker journal 日志")

# 5. 手动执行 dockerd 看错误
run_via_panel('/usr/bin/dockerd --version 2>&1; /usr/bin/dockerd --help 2>&1 | head -3', label="5. 手动执行 dockerd")

# 6. 检查 docker-ce 包安装状态
run_via_panel('dpkg -l | grep -i docker 2>&1; echo "---"; apt list --installed 2>/dev/null | grep -i docker', label="6. docker 包")

# 7. 重启 systemd 并重试
run_via_panel('systemctl daemon-reload; systemctl reset-failed docker; systemctl start docker 2>&1; sleep 5; systemctl is-active docker 2>&1', label="7. 重置并重试启动")

# 8. 再次看 journal
run_via_panel('journalctl -u docker.service --no-pager -n 20 --since "1 minute ago" 2>&1', label="8. 最新 journal 日志")

panel.close()
print("\n✓ 完成")
