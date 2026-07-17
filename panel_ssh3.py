#!/usr/bin/env python3
"""面板服务器: 重启 docker 清理 containerd + 验证日志报错。"""
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
print("[+] TCP 连接已建立")

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(
    PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS,
    sock=sock, timeout=30, look_for_keys=False, allow_agent=False,
)
print("[+] SSH 登录成功\n")


def run(cmd, timeout=120, label=""):
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


# === 0. 先确认 SQL 报错是否仍在(查最近1小时面板日志) ===
run("docker logs --since 1h nexus-panel 2>&1 | grep -iE 'aggregate|SQLSTATE|42883|clean.*traffic' | tail -20",
    label="SQL 报错检查(最近1小时)", timeout=60)
run("docker logs --since 1h nexus-panel 2>&1 | grep -iE 'ERROR|error' | tail -20",
    label="面板最近1小时所有 ERROR", timeout=60)

# === 1. 重启前的容器与磁盘状态 ===
run("docker ps --format '{{.Names}}\t{{.Status}}\t{{.Image}}'", label="重启前容器状态")
run("df -h / | tail -1")
run("du -sh /var/lib/containerd 2>/dev/null")

# === 2. 重启 docker 守护进程(触发 containerd GC, 容器自动重启) ===
print("\n" + "=" * 60)
print("=== 重启 docker 守护进程 ===")
print("=" * 60)
run("systemctl restart docker 2>&1; echo docker-restarted-rc=$?", timeout=120)

# === 3. 等待容器恢复 ===
print("\n[等待 25 秒让容器自动重启...]")
time.sleep(25)
run("docker ps --format '{{.Names}}\t{{.Status}}\t{{.Image}}'", label="重启后容器状态")

# === 4. 重启后再做一次镜像清理(此时旧容器已切到新镜像, 旧镜像可删) ===
run("docker image prune -a -f 2>&1 | tail -5", label="重启后镜像清理", timeout=120)
run("docker container prune -f 2>&1 | tail -3", timeout=60)

# === 5. 检查 containerd 是否缩减 ===
run("du -sh /var/lib/containerd 2>/dev/null", label="重启后 containerd 大小")
run("du -sh /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs 2>/dev/null")
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | wc -l", label="剩余快照数")
run("df -h / | tail -1", label="重启后磁盘")
run("docker system df 2>&1", label="docker 系统占用")

# === 6. 容器恢复后健康检查 ===
print("\n[等待 15 秒让容器完全就绪...]")
time.sleep(15)
run("docker ps --format '{{.Names}}\t{{.Status}}'", label="最终容器状态")
run("docker logs --tail 10 nexus-panel 2>&1 | tail -10", label="面板最新日志", timeout=30)
run("docker logs --tail 5 nexus-postgres 2>&1 | tail -5", label="postgres 最新日志", timeout=30)

ssh.close()
print("\n[+] 完成")
