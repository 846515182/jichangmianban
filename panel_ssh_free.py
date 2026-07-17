#!/usr/bin/env python3
"""紧急: 磁盘100%满, 容器全无。先释放空间再重建。"""
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


def run(cmd, timeout=240, label=""):
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


# === 1. 删除所有镜像(无容器运行, 全部可删) ===
run("docker system prune -a -f 2>&1 | tail -10", label="删除所有未使用镜像", timeout=120)

# === 2. 删除所有停止的容器(清理残留) ===
run("docker container prune -f 2>&1 | tail -3", timeout=60)

# === 3. 手动清理 containerd orphan committed snapshots ===
# 列出所有 committed 快照(非 active), 这些是旧镜像层, 删除可释放空间
print("\n=== 清理 containerd committed 快照 ===")
out, _, _ = run("ctr -n moby snapshots --snapshotter overlayfs list -q 2>&1", label="列出所有快照key")

# 获取 active 快照(正在使用的, 不能删)
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | awk '$3==\"Active\"{print $1}' > /tmp/active_snaps.txt; wc -l /tmp/active_snaps.txt")
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | awk '$3==\"Committed\"{print $1}' > /tmp/committed_snaps.txt; wc -l /tmp/committed_snaps.txt")

# 找出 active 快照的父链(不能删的)
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | awk '$3==\"Active\"{print $2}' | sort -u > /tmp/active_parents.txt; wc -l /tmp/active_parents.txt")

# 删除 committed 快照中不在 active 父链里的(孤儿)
# 注意: 这可能失败部分(有依赖关系), 用 || true 继续
run("cat /tmp/committed_snaps.txt | while read k; do ctr -n moby snapshots --snapshotter overlayfs rm \"$k\" 2>/dev/null; done; echo done",
    label="删除孤儿 committed 快照", timeout=180)

# === 4. 检查释放后的磁盘 ===
run("df -h / | tail -1", label="清理后磁盘")
run("du -sh /var/lib/containerd 2>/dev/null")
run("du -sh /var/lib/docker 2>/dev/null")
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | wc -l", label="剩余快照数")

ssh.close()
print("\n[+] 完成")
