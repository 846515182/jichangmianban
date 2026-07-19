#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""通过面板 verbose 诊断节点 SSH 认证失败原因"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

NODE_HOST = '38.59.246.203'

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

# 1. 用 ssh -v 看 verbose 输出
print("\n=== 1. ssh -v verbose 输出(看支持哪些认证方式) ===")
cmd = f'ssh -v -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=none -o NumberOfPasswordPrompts=0 root@{NODE_HOST} 2>&1 | head -50'
_, o, _ = panel.exec_command(cmd, timeout=20)
print(o.read().decode('utf-8','replace'))

# 2. 检查 sshd_config 是否允许 root 登录
print("\n=== 2. 检查节点 sshd_config (如果能连) ===")
cmd2 = f'sshpass -p "3Cxeg14SKol9fp43LZ" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} "cat /etc/ssh/sshd_config | grep -iE \'PermitRootLogin|PasswordAuthentication\' | head" 2>&1'
_, o, _ = panel.exec_command(cmd2, timeout=15)
print(o.read().decode('utf-8','replace'))

# 3. 试用面板密码(也许用户用了同一个密码)
print("\n=== 3. 试用面板密码登录节点 ===")
for pwd in ['eH62M3CcaSep59J8lZ', '3Cxeg14SKol9fp43LZ!', '3Cxeg14SKol9fp43LZ1']:
    cmd3 = f'sshpass -p {repr(pwd)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=password -o PubkeyAuthentication=no root@{NODE_HOST} "echo OK" 2>&1'
    _, o, _ = panel.exec_command(cmd3, timeout=15)
    out = o.read().decode('utf-8','replace')
    print(f"  {repr(pwd[:20])}... -> {out.strip()[:100]}")

# 4. 试常见用户名
print("\n=== 4. 试常见用户名 ===")
for user in ['root', 'admin', 'ubuntu', 'debian', 'centos']:
    cmd4 = f'sshpass -p "3Cxeg14SKol9fp43LZ" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=8 -o PreferredAuthentications=password -o PubkeyAuthentication=no {user}@{NODE_HOST} "whoami" 2>&1'
    _, o, _ = panel.exec_command(cmd4, timeout=15)
    out = o.read().decode('utf-8','replace')
    print(f"  user={user} -> {out.strip()[:120]}")

# 5. nmap 扫一下节点开了哪些端口
print("\n=== 5. 检查节点常见端口 ===")
cmd5 = f'for p in 22 80 443 50051 50052 2375 2376; do timeout 3 bash -c "echo > /dev/tcp/{NODE_HOST}/$p" 2>/dev/null && echo "port $p: OPEN" || echo "port $p: closed"; done'
_, o, _ = panel.exec_command(cmd5, timeout=30)
print(o.read().decode('utf-8','replace'))

# 6. 检查面板的 deploy audit log
print("\n=== 6. 检查面板上的部署日志(看以前用什么密码部署的) ===")
cmd6 = 'docker exec panel ls /app/logs/ 2>/dev/null; docker exec panel find /app -name "*.log" 2>/dev/null | head -5'
_, o, _ = panel.exec_command(cmd6, timeout=10)
print(o.read().decode('utf-8','replace'))

panel.close()
