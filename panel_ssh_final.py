#!/usr/bin/env python3
"""重启 docker + 清 buildx 缓存 + 无缓存重建。"""
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


# === 1. 重启 docker(重启 buildkitd, 清理 stale 状态) ===
run("systemctl restart docker 2>&1; echo rc=$?", timeout=90)
print("[等待 10 秒...]")
time.sleep(10)

# === 2. 清除所有 buildx/buildkit 缓存 + 残留镜像 ===
run("docker buildx prune -a -f 2>&1 | tail -5", timeout=120)
run("docker builder prune -a -f 2>&1 | tail -3", timeout=120)
run("docker image prune -a -f 2>&1 | tail -3", timeout=120)

# === 3. 无缓存重建并启动 ===
run("cd /root/nexus-panel && DOCKER_BUILDKIT=1 docker compose build --no-cache 2>&1 | tail -40", timeout=1200)

# === 4. 启动容器 ===
run("cd /root/nexus-panel && docker compose up -d 2>&1 | tail -15", timeout=180)

# === 5. 等待容器就绪 ===
print("\n[等待 40 秒...]")
time.sleep(40)
run("docker ps --format '{{.Names}}|{{.Status}}|{{.Image}}'")

print("\n[再等 20 秒检查健康...]")
time.sleep(20)
run("docker ps --format '{{.Names}}|{{.Status}}'")

# === 6. 面板日志 ===
run("docker logs --tail 20 nexus-panel 2>&1 | tail -20", timeout=30)

# === 7. 最终状态 ===
run("df -h / | tail -1")
run("docker system df 2>&1")

ssh.close()
print("\n[+] 完成")
