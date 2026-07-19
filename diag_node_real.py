#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""通过面板跳板 SSH 到节点服务器, 查看 agent 容器日志"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

NODE_HOST = '38.59.246.203'
NODE_USER = 'root'
NODE_PWD = '3Cxeg14SKoI9fp43LZ'  # 大写 I 不是 1

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

def panel_run(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    _, o, e = panel.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    if out: print(out)
    if err.strip(): print("STDERR:", err[:500])
    return out

# 1. 先把面板公钥写到节点上 (用 sshpass)
panel_run('cat /root/.ssh/id_ed25519.pub', label="1. 面板公钥")

# 2. 用正确密码 SSH 到节点
print("\n=== 2. SSH 到节点测试 ===")
test_cmd = f'sshpass -p {repr(NODE_PWD)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=password -o PubkeyAuthentication=no {NODE_USER}@{NODE_HOST} "hostname && uptime && docker ps -a --format \'table {{{{.Names}}}}\\t{{{{.Status}}}}\\t{{{{.Image}}}}\'" 2>&1'
panel_run(test_cmd, timeout=30, label="2. SSH 测试 + docker ps")

# 3. 部署 SSH key 到节点 (免密)
print("\n=== 3. 部署面板 SSH key 到节点 authorized_keys ===")
deploy_key_cmd = (
    f'sshpass -p {repr(NODE_PWD)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 '
    f'-o PreferredAuthentications=password -o PubkeyAuthentication=no {NODE_USER}@{NODE_HOST} '
    f'"mkdir -p ~/.ssh && chmod 700 ~/.ssh && '
    f'grep -qF \"$(cat <<\'PUBKEY\'\\n$(ssh-keygen -y -f /root/.ssh/id_ed25519)\\nPUBKEY)\" ~/.ssh/authorized_keys 2>/dev/null || '
    f'ssh-keygen -y -f /root/.ssh/id_ed25519 >> ~/.ssh/authorized_keys && '
    f'chmod 600 ~/.ssh/authorized_keys && echo KEY_DEPLOYED"'
)
# 简化版: 直接把面板公钥内容写过去
panel_pubkey = panel_run('cat /root/.ssh/id_ed25519.pub', label="3a. 读取面板公钥").strip()
if panel_pubkey:
    import shlex
    deploy_cmd = (
        f'sshpass -p {repr(NODE_PWD)} ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 '
        f'-o PreferredAuthentications=password -o PubkeyAuthentication=no {NODE_USER}@{NODE_HOST} '
        f'"mkdir -p ~/.ssh && chmod 700 ~/.ssh && '
        f'echo {shlex.quote(panel_pubkey)} >> ~/.ssh/authorized_keys && '
        f'sort -u ~/.ssh/authorized_keys -o ~/.ssh/authorized_keys && '
        f'chmod 600 ~/.ssh/authorized_keys && echo KEY_DEPLOYED"'
    )
    panel_run(deploy_cmd, timeout=20, label="3b. 部署 SSH key")

# 4. 用免密 SSH 验证
panel_run(f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=publickey -o PasswordAuthentication=no {NODE_USER}@{NODE_HOST} "echo KEYLESS_OK && hostname"', timeout=20, label="4. 验证免密 SSH")

# 5. 查看所有 nexus 容器
panel_run(f'ssh -o StrictHostKeyChecking=no {NODE_USER}@{NODE_HOST} "docker ps -a --format \'table {{{{.Names}}}}\\t{{{{.Status}}}}\\t{{{{.Image}}}}\' | grep -i nexus"', timeout=20, label="5. 节点上 nexus 容器列表")

# 6. 查看部署目录
panel_run(f'ssh -o StrictHostKeyChecking=no {NODE_USER}@{NODE_HOST} "ls -la /root/node-agent-* 2>/dev/null | head -20"', timeout=20, label="6. 部署目录")

panel.close()
print("\n✓ 完成")
