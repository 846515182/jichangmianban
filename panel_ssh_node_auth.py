#!/usr/bin/env python3
"""诊断节点 SSH 认证失败原因。"""
import socks
import socket
import paramiko
import time

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"
NODE_HOST = "38.59.246.203"
NODE_PASS = "Q63r8G60PnwyliZE2W"

sock = socks.socksocket()
sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
sock.settimeout(30)
sock.connect((PANEL_HOST, PANEL_PORT))
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(
    PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS,
    sock=sock, timeout=30, look_for_keys=False, allow_agent=False,
)
print("[+] 面板 SSH 登录成功\n")


def run(cmd, timeout=60):
    print(f"\n$ {cmd}")
    try:
        stdin, stdout, stderr = ssh.exec_command(cmd, timeout=timeout, get_pty=False)
        out = stdout.read().decode("utf-8", errors="replace")
        err = stderr.read().decode("utf-8", errors="replace")
        rc = stdout.channel.recv_exit_status()
        if out:
            print(out.rstrip())
        if err:
            print(f"[stderr] {err.rstrip()}")
        print(f"[exit={rc}]")
        return out, err, rc
    except Exception as e:
        print(f"[ERROR] {e}")
        return "", str(e), -1


# ssh -v 看认证方式
print("=== ssh -v 诊断节点认证 ===")
run(f"sshpass -p '{NODE_PASS}' ssh -v -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o PreferredAuthentications=password -o PubkeyAuthentication=no root@{NODE_HOST} 'echo OK' 2>&1 | grep -iE 'auth|password|denied|method|banner|publickey' | head -20", timeout=30)

# 测试不同密码变体(可能用户输错)
print("\n=== 测试 SSH 连接(详细) ===")
run(f"ssh -v -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o NumberOfPasswordPrompts=0 root@{NODE_HOST} 'echo' 2>&1 | grep -iE 'auth|method|banner' | head -15", timeout=20)

# 看节点 SSH banner
run(f"timeout 5 bash -c 'exec 3<>/dev/tcp/{NODE_HOST}/22; head -1 <&3' 2>&1", timeout=10)

# 检查面板是否被节点 fail2ban 封
print("\n=== 从面板多次 SSH 尝试(看是否被封) ===")
run(f"sshpass -p 'wrong' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@{NODE_HOST} 'echo' 2>&1; echo rc=$?", timeout=15)

ssh.close()
print("\n[+] 完成")
