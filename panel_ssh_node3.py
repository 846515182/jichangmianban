#!/usr/bin/env python3
"""装 sshpass, SSH 节点排查 agent。"""
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


def run(cmd, timeout=180):
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


# 装 sshpass
print("=== 装 sshpass ===")
run("apt-get install -y sshpass 2>&1 | tail -5", timeout=180)
run("which sshpass 2>&1")

# SSH 节点排查
print("\n=== SSH 节点排查 ===")
node_cmd = (
    "echo '=== 节点磁盘 ==='; df -h /; "
    "echo '=== 节点 docker 容器 ==='; docker ps -a 2>&1; "
    "echo '=== 节点进程 ==='; ps aux | grep -E 'nexus|xray|agent' | grep -v grep; "
    "echo '=== 节点端口 ==='; ss -tlnp 2>&1 | head -25; "
    "echo '=== 节点 agent docker 日志 ==='; docker logs --tail 40 nexus-node-agent 2>&1 || echo no-docker-agent; "
    "echo '=== 节点 systemd agent ==='; systemctl status nexus-agent 2>&1 | head -15 || echo no-systemd-agent; "
    "echo '=== 节点 /root ==='; ls -la /root/ 2>&1 | head -20; "
    "echo '=== 节点 /opt ==='; ls -la /opt/ 2>&1 | head -20; "
    "echo '=== 节点 systemctl docker ==='; systemctl status docker 2>&1 | head -10; "
    "echo '=== DONE ==='"
)
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} \"{node_cmd}\" 2>&1"
run(ssh_cmd, timeout=90)

ssh.close()
print("\n[+] 完成")
