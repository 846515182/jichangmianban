#!/usr/bin/env python3
"""紧急恢复: 找到 docker-compose.yml 并启动容器。"""
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
print("[+] SSH 登录成功\n")


def run(cmd, timeout=180, label=""):
    if label:
        print(f"\n=== {label} ===")
    print(f"$ {cmd}")
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


# === 1. 紧急: 找 docker-compose.yml 位置 ===
run("find / -maxdepth 4 -name 'docker-compose*.yml' -not -path '*/node_modules/*' 2>/dev/null | head -20",
    label="查找 docker-compose.yml")
run("find / -maxdepth 4 -name 'docker-compose*.yaml' -not -path '*/node_modules/*' 2>/dev/null | head -20")
run("find / -maxdepth 3 -name '.git' -type d 2>/dev/null | head -10", label="查找 git 仓库")

ssh.close()
print("\n[+] 完成")
