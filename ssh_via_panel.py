#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""通过面板服务器跳板登录节点服务器"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

NODE_HOST = '38.59.246.203'
NODE_PORT = 22
NODE_USER = 'root'
NODE_PWD_CANDIDATES = [
    '3Cxeg14SKol9fp43LZ',
    '3Cxeg14SKol9fp43LZ ',
    ' 3Cxeg14SKol9fp43LZ',
]

PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock(host, port):
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((host, port))
    return s

# Step 1: 登录面板
print(f"=== 1. 登录面板服务器 {PANEL_HOST} ===")
panel = paramiko.SSHClient()
panel.set_missing_host_key_policy(paramiko.AutoAddPolicy())
try:
    panel.connect(PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PWD,
                  sock=make_sock(PANEL_HOST, PANEL_PORT), timeout=30, banner_timeout=30, auth_timeout=30)
    print("✓ 面板登录成功")
except Exception as e:
    print(f"✗ 面板登录失败: {e}")
    sys.exit(1)

# Step 2: 在面板上检查是否能直连节点
print(f"\n=== 2. 从面板测试节点 SSH 端口 ===")
_, o, e = panel.exec_command(f'timeout 10 bash -c "echo > /dev/tcp/{NODE_HOST}/22" 2>&1 && echo OK || echo FAIL', timeout=15)
print("结果:", o.read().decode('utf-8','replace'))

# Step 3: 检查面板有没有 sshpass
print(f"\n=== 3. 检查面板是否有 sshpass/expect ===")
_, o, _ = panel.exec_command('which sshpass; which expect; which ssh-keygen', timeout=10)
print("工具:", o.read().decode('utf-8','replace'))

# Step 4: 生成 SSH key (如果没有)
print(f"\n=== 4. 准备面板 SSH key ===")
_, o, _ = panel.exec_command('ls /root/.ssh/id_ed25519.pub 2>/dev/null || ls /root/.ssh/id_rsa.pub 2>/dev/null', timeout=10)
existing_key = o.read().decode('utf-8','replace').strip()
if existing_key:
    print(f"已有 key: {existing_key}")
    _, o, _ = panel.exec_command(f'cat {existing_key}', timeout=10)
    pub_key = o.read().decode('utf-8','replace').strip()
    print(f"公钥: {pub_key[:60]}...")
else:
    print("生成新 key...")
    _, o, e = panel.exec_command('ssh-keygen -t ed25519 -N "" -f /root/.ssh/id_ed25519 -q && echo OK', timeout=10)
    print(o.read().decode('utf-8','replace'))
    _, o, _ = panel.exec_command('cat /root/.ssh/id_ed25519.pub', timeout=10)
    pub_key = o.read().decode('utf-8','replace').strip()
    print(f"新公钥: {pub_key[:60]}...")

# Step 5: 从面板尝试 SSH 到节点 (试每个密码)
print(f"\n=== 5. 从面板 SSH 到节点 {NODE_HOST} ===")
# 安装 sshpass (如果没有)
panel.exec_command('apt-get install -y sshpass 2>&1 | tail -3', timeout=60)
time.sleep(2)

for i, pwd in enumerate(NODE_PWD_CANDIDATES):
    print(f"\n--- 尝试密码 #{i+1}: {repr(pwd)} ---")
    cmd = f'sshpass -p {repr(pwd)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 -o PreferredAuthentications=password -o PubkeyAuthentication=no {NODE_USER}@{NODE_HOST} "hostname && uptime && docker ps --format \'table {{{{.Names}}}}\\t{{{{.Status}}}}\'"'
    _, o, e = panel.exec_command(cmd, timeout=30)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print("STDOUT:", out)
    if err.strip():
        print("STDERR:", err[:300])
    if 'load average' in out or 'up ' in out:
        print(f"✓✓✓ 密码 #{i+1} 登录成功!")
        # 把面板的 pub_key 写到节点的 authorized_keys
        print("正在部署 SSH key 到节点...")
        cmd2 = f'sshpass -p {repr(pwd)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 -o PreferredAuthentications=password -o PubkeyAuthentication=no {NODE_USER}@{NODE_HOST} "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo {repr(pub_key)} >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && sort -u ~/.ssh/authorized_keys -o ~/.ssh/authorized_keys && echo KEY_DEPLOYED"'
        _, o, e = panel.exec_command(cmd2, timeout=20)
        print(o.read().decode('utf-8','replace'))
        err = e.read().decode('utf-8','replace')
        if err.strip():
            print("STDERR:", err[:300])
        # 验证免密
        print("\n验证 SSH 免密登录...")
        cmd3 = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=publickey {NODE_USER}@{NODE_HOST} "echo KEYLESS_OK && hostname"'
        _, o, _ = panel.exec_command(cmd3, timeout=20)
        print("结果:", o.read().decode('utf-8','replace'))
        panel.close()
        sys.exit(0)

print("\n所有密码尝试均失败")
panel.close()
