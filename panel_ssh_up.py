#!/usr/bin/env python3
"""恢复面板: docker compose up -d。"""
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


def run(cmd, timeout=600):
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


# === docker compose up -d (重建并启动所有容器) ===
run("cd /root/nexus-panel && docker compose up -d 2>&1 | tail -40", timeout=900)

# === 等待容器就绪 ===
print("\n[等待 35 秒让容器启动...]")
time.sleep(35)
run("docker ps --format '{{.Names}}|{{.Status}}|{{.Image}}'")

# === 再等一会检查健康状态 ===
print("\n[再等 20 秒检查健康...]")
time.sleep(20)
run("docker ps --format '{{.Names}}|{{.Status}}'")

# === 面板启动日志确认 ===
run("docker logs --tail 20 nexus-panel 2>&1 | tail -20", timeout=30)

# === 最终磁盘 ===
run("df -h / | tail -1")
run("du -sh /var/lib/containerd /var/lib/docker 2>/dev/null")
run("docker system df 2>&1")

ssh.close()
print("\n[+] 完成")
