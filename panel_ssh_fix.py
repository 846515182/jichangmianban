#!/usr/bin/env python3
"""修复: 创建 tmp 目录, 清残留容器, 重启, 重建。"""
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


# === 1. 停止 docker + containerd ===
run("systemctl stop docker 2>&1; echo rc=$?", timeout=60)
run("systemctl stop containerd 2>&1; echo rc=$?", timeout=60)
time.sleep(3)

# === 2. 彻底清空 containerd(残留容器引用在 containerd 侧) ===
run("rm -rf /var/lib/containerd/* 2>&1; echo rc=$?", timeout=60)

# === 3. 补回 docker 缺失目录(tmp 等) ===
run("mkdir -p /var/lib/docker/tmp /var/lib/docker/containers /var/lib/docker/image /var/lib/docker/network /var/lib/docker/plugins /var/lib/docker/runtimes /var/lib/docker/swarm /var/lib/docker/buildkit /var/lib/docker/overlay2 2>&1; echo rc=$?", timeout=30)
run("ls /var/lib/docker/ 2>&1")

# === 4. 重启 containerd + docker ===
run("systemctl start containerd 2>&1; echo rc=$?", timeout=60)
time.sleep(3)
run("systemctl start docker 2>&1; echo rc=$?", timeout=90)
time.sleep(8)

# === 5. 确认 docker 干净 ===
run("docker system df 2>&1")
run("docker images 2>&1")
run("docker ps -a 2>&1")
run("df -h / | tail -1")

# === 6. 确认 volumes 仍在 ===
run("ls /var/lib/docker/volumes/ 2>&1")

# === 7. 用 BuildKit 重建(现在 containerd 是干净的, 不再有 stale parent) ===
print("\n[开始重建...]")
run("cd /root/nexus-panel && DOCKER_BUILDKIT=1 docker compose build --no-cache 2>&1 | tail -60", timeout=1800)

# === 8. 启动容器 ===
run("cd /root/nexus-panel && docker compose up -d 2>&1 | tail -15", timeout=180)

# === 9. 等待就绪 ===
print("\n[等待 45 秒...]")
time.sleep(45)
run("docker ps --format '{{.Names}}|{{.Status}}|{{.Image}}'")

print("\n[再等 20 秒检查健康...]")
time.sleep(20)
run("docker ps --format '{{.Names}}|{{.Status}}'")

# === 10. 面板日志 ===
run("docker logs --tail 30 nexus-panel 2>&1 | tail -30", timeout=30)

# === 11. 最终状态 ===
run("df -h / | tail -1")
run("docker system df 2>&1")

ssh.close()
print("\n[+] 完成")
