#!/usr/bin/env python3
"""清理 containerd 死快照(所有镜像/容器已删, overlayfs 快照全是孤儿) + 重建。"""
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


def run(cmd, timeout=300, label=""):
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


# === 0. 确认 docker volumes 位置(pg-data/redis-data 必须保留!) ===
run("ls -la /var/lib/docker/volumes/ 2>&1", label="docker volumes(必须保留)")
run("du -sh /var/lib/docker/volumes/* 2>/dev/null")

# === 1. 停止 docker + containerd ===
run("systemctl stop docker 2>&1; echo docker-stopped-rc=$?", label="停止 docker", timeout=60)
run("systemctl stop containerd 2>&1; echo containerd-stopped-rc=$?", timeout=60)

# === 2. 清空 containerd overlayfs 快照(全是死数据, 所有镜像/容器已删) ===
run("du -sh /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs 2>/dev/null", label="清理前 containerd overlayfs")
run("rm -rf /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/* 2>&1; echo snapshots-cleared-rc=$?", label="清空 overlayfs snapshots")
run("rm -f /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/metadata.db 2>&1; echo metadata-cleared-rc=$?", label="清除 overlayfs metadata")
run("du -sh /var/lib/containerd 2>/dev/null", label="清理后 containerd")
run("df -h / | tail -1", label="清理后磁盘")

# === 3. 启动 containerd + docker ===
run("systemctl start containerd 2>&1; echo containerd-started-rc=$?", label="启动 containerd", timeout=60)
run("systemctl start docker 2>&1; echo docker-started-rc=$?", label="启动 docker", timeout=60)

# === 4. 确认 volumes 仍在 ===
run("ls -la /var/lib/docker/volumes/ 2>&1", label="确认 volumes 仍在")

# === 5. 重建并启动容器 ===
run("docker compose up -d 2>&1 | tail -30", label="docker compose up -d", timeout=600, cwd="/root/nexus-panel")

# === 6. 检查容器状态 ===
print("\n[等待 30 秒...]")
time.sleep(30)
run("docker ps --format '{{.Names}}\t{{.Status}}\t{{.Image}}'", label="容器状态")
run("df -h / | tail -1", label="最终磁盘")
run("du -sh /var/lib/containerd 2>/dev/null")
run("docker system df 2>&1")

ssh.close()
print("\n[+] 完成")
