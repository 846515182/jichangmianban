#!/usr/bin/env python3
"""彻底清理 docker + containerd 状态(保留 volumes), 用 legacy builder 重建。"""
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


# === 0. 先确认 volumes 路径(必须保护) ===
run("ls -la /var/lib/docker/volumes/ 2>&1")
run("du -sh /var/lib/docker/volumes/ 2>&1")

# === 1. 停止 docker + containerd ===
run("systemctl stop docker 2>&1; echo rc=$?", timeout=60)
run("systemctl stop containerd 2>&1; echo rc=$?", timeout=60)
time.sleep(3)

# === 2. 备份 volumes 到临时位置(双保险) ===
run("mkdir -p /root/docker-volumes-backup && cp -a /var/lib/docker/volumes/. /root/docker-volumes-backup/ 2>&1; echo rc=$?", timeout=120)
run("du -sh /root/docker-volumes-backup/ 2>&1")

# === 3. 彻底清空 docker + containerd 状态目录(保留 volumes) ===
# docker: 删除 image/overlay2/buildkit/containerd/ 等 metadata, 只留 volumes
run("ls /var/lib/docker/ 2>&1")
run("cd /var/lib/docker && for d in */; do [ \"$d\" != \"volumes/\" ] && rm -rf \"$d\"; done 2>&1; echo rc=$?", timeout=60)
run("ls /var/lib/docker/ 2>&1")
# containerd: 整个目录重建
run("rm -rf /var/lib/containerd/* 2>&1; echo rc=$?", timeout=60)
run("ls /var/lib/containerd/ 2>&1")

# === 4. 重启 containerd + docker(会自动重建空 metadata) ===
run("systemctl start containerd 2>&1; echo rc=$?", timeout=60)
time.sleep(3)
run("systemctl start docker 2>&1; echo rc=$?", timeout=90)
time.sleep(8)

# === 5. 恢复 volumes(若被清掉则从备份恢复) ===
run("ls -la /var/lib/docker/volumes/ 2>&1")
run("if [ ! -d /var/lib/docker/volumes/nexus-panel_pg-data ]; then cp -a /root/docker-volumes-backup/. /var/lib/docker/volumes/ 2>&1; echo restored; else echo 'volumes OK'; fi", timeout=120)
run("ls -la /var/lib/docker/volumes/ 2>&1")

# === 6. 确认 docker 干净状态 ===
run("docker system df 2>&1")
run("docker images 2>&1")
run("docker ps -a 2>&1")
run("df -h / | tail -1")

# === 7. 用 legacy builder 重建(DOCKER_BUILDKIT=0 绕过 buildkit 缓存问题) ===
print("\n[开始用 legacy builder 重建...]")
run("cd /root/nexus-panel && DOCKER_BUILDKIT=0 docker compose build --no-cache 2>&1 | tail -50", timeout=1800)

# === 8. 启动容器 ===
run("cd /root/nexus-panel && docker compose up -d 2>&1 | tail -15", timeout=180)

# === 9. 等待就绪 ===
print("\n[等待 40 秒...]")
time.sleep(40)
run("docker ps --format '{{.Names}}|{{.Status}}|{{.Image}}'")

print("\n[再等 20 秒检查健康...]")
time.sleep(20)
run("docker ps --format '{{.Names}}|{{.Status}}'")

# === 10. 面板日志 ===
run("docker logs --tail 25 nexus-panel 2>&1 | tail -25", timeout=30)

# === 11. 最终状态 ===
run("df -h / | tail -1")
run("docker system df 2>&1")

# 清理备份(确认 volumes 在 docker 里完好后再删)
run("ls /var/lib/docker/volumes/ 2>&1")

ssh.close()
print("\n[+] 完成")
