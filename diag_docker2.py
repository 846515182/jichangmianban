#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""查看 docker 启动失败的详细原因"""
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

# 1. 看 journal 最新日志
run_via_panel('journalctl -u docker.service --no-pager -n 30 --since "2 minutes ago" 2>&1', label="1. 最新 journal 日志")

# 2. 手动执行 dockerd 看错误
run_via_panel('/usr/local/bin/dockerd 2>&1 | head -30 &; sleep 5; kill %1 2>/dev/null; wait 2>/dev/null', timeout=15, label="2. 手动执行 dockerd")

# 3. 检查 docker.socket
run_via_panel('systemctl status docker.socket --no-pager 2>&1 | head -10; ls -la /var/run/docker.sock 2>&1', label="3. docker.socket")

# 4. 检查 iptables/内核模块
run_via_panel('iptables --version 2>&1; lsmod | grep -E "br_netfilter|overlay|nf_nat" 2>&1; uname -r', label="4. iptables/内核模块")

# 5. 检查 cgroup
run_via_panel('mount | grep cgroup 2>&1 | head; cat /sys/fs/cgroup/cgroup.controllers 2>&1', label="5. cgroup")

# 6. 检查 /etc/docker/daemon.json
run_via_panel('cat /etc/docker/daemon.json 2>&1', label="6. daemon.json")

# 7. 重置 + 启动并立即看状态
run_via_panel('systemctl reset-failed docker; systemctl start docker; sleep 8; systemctl status docker --no-pager 2>&1 | head -25', label="7. 启动 + 状态")

# 8. journal 再看一次
run_via_panel('journalctl -u docker.service --no-pager -n 40 --since "30 seconds ago" 2>&1', label="8. 启动后 journal")

panel.close()
print("\n✓ 完成")
