#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""检查面板上是否已有节点的 SSH 访问配置"""
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

# 1. 检查 known_hosts
print("\n=== 1. 检查 known_hosts ===")
_, o, _ = panel.exec_command(f'grep {NODE_HOST} /root/.ssh/known_hosts 2>/dev/null | head -3', timeout=10)
print(o.read().decode('utf-8','replace') or "(无)")

# 2. 检查所有 SSH 私钥
print("\n=== 2. 列出 /root/.ssh 下所有文件 ===")
_, o, _ = panel.exec_command('ls -la /root/.ssh/', timeout=10)
print(o.read().decode('utf-8','replace'))

# 3. 尝试每个私钥 SSH 到节点
print("\n=== 3. 尝试用每个私钥 SSH 到节点 ===")
_, o, _ = panel.exec_command('ls /root/.ssh/id_* 2>/dev/null | grep -v ".pub"', timeout=10)
keys = [k.strip() for k in o.read().decode('utf-8','replace').split('\n') if k.strip()]
print(f"找到私钥: {keys}")
for k in keys:
    print(f"\n--- 尝试 {k} ---")
    cmd = f'ssh -i {k} -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=publickey -o PasswordAuthentication=no root@{NODE_HOST} "echo KEY_OK && hostname && docker ps --format \'{{{{.Names}}}} {{{{.Status}}}}\'" 2>&1'
    _, o, _ = panel.exec_command(cmd, timeout=20)
    out = o.read().decode('utf-8','replace')
    print(out[:500])

# 4. 检查面板是否有节点配置 (比如 nexus 相关)
print("\n=== 4. 检查面板上的 nexus 相关文件 ===")
_, o, _ = panel.exec_command('ls /root/nexus-panel/node_agent/ 2>/dev/null; ls /root/.nexus-node* 2>/dev/null; find /root -name ".env" -maxdepth 3 2>/dev/null | head', timeout=10)
print(o.read().decode('utf-8','replace'))

# 5. 检查节点的 .env (里面可能有节点服务器的真实信息)
print("\n=== 5. 检查面板上的 .env 中的节点信息 ===")
_, o, _ = panel.exec_command('cat /root/nexus-panel/.env 2>/dev/null | grep -iE "node|ssh|host" | head -20', timeout=10)
print(o.read().decode('utf-8','replace'))

# 6. 检查是否有 ssh key 在 docker volume 里
print("\n=== 6. 检查 panel 容器里是否有 SSH key ===")
_, o, _ = panel.exec_command('docker exec panel ls /root/.ssh/ 2>/dev/null', timeout=10)
print(o.read().decode('utf-8','replace'))

panel.close()
