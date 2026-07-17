#!/usr/bin/env python3
"""清理 build cache 释放空间, 确认面板可访问。"""
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


def run(cmd, timeout=900):
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


# === 1. 清理 build cache(2.7G 可回收) ===
run("docker builder prune -a -f 2>&1 | tail -3", timeout=120)

# === 2. 清理悬空镜像 ===
run("docker image prune -f 2>&1 | tail -3", timeout=60)

# === 3. 最终磁盘状态 ===
run("df -h / | tail -1")
run("docker system df 2>&1")

# === 4. 确认容器状态 ===
run("docker ps --format '{{.Names}}|{{.Status}}'")

# === 5. 面板 HTTP 可达性检查 ===
run("curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz 2>&1; echo")

# === 6. 面板 git 状态(确认当前部署的版本) ===
run("cd /root/nexus-panel && git log --oneline -3 2>&1")
run("cd /root/nexus-panel && git remote -v 2>&1")

# === 7. 清理备份 ===
run("rm -rf /root/docker-volumes-backup 2>&1; echo rc=$?")
run("df -h / | tail -1")

ssh.close()
print("\n[+] 完成")
