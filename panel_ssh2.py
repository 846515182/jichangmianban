#!/usr/bin/env python3
"""面板服务器: 深入清理 containerd + 检查镜像/日志, 非交互式。"""
import socks
import socket
import paramiko

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

sock = socks.socksocket()
sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
sock.settimeout(30)
print(f"[*] 经代理连接 {PANEL_HOST}:{PANEL_PORT} ...")
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


# === 1. 镜像清单(找出未使用/可删的) ===
run("docker images --format '{{.Repository}}:{{.Tag}}\t{{.ID}}\t{{.Size}}\t{{.CreatedSince}}'", label="全部镜像")
run("docker ps --format '{{.Names}}\t{{.Image}}\t{{.Status}}'", label="运行中容器使用的镜像")

# === 2. 列出所有未被容器使用的镜像 ID ===
run("docker images -q | sort -u > /tmp/all_imgs.txt; docker ps -q | xargs -r docker inspect --format '{{.Image}}' | sort -u > /tmp/used_imgs.txt; comm -23 /tmp/all_imgs.txt /tmp/used_imgs.txt", label="未被容器使用的镜像ID")

# === 3. 删除未被容器使用的镜像(释放对应 containerd 层) ===
run("comm -23 /tmp/all_imgs.txt /tmp/used_imgs.txt | xargs -r docker rmi -f 2>&1 | tail -20", label="删除未使用镜像", timeout=120)

# === 4. containerd 快照状态 ===
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | head -30", label="containerd 快照(前30)")
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | awk '{print $1, $3}' | sort -k2 | head -30", label="快照按状态排序")

# === 5. journal + 日志清理(非交互) ===
run("journalctl --vacuum-time=1d 2>&1 | tail -3", label="journal 清理")
run("find /var/log -type f \\( -name '*.gz' -o -name '*.1' -o -name '*.old' \\) -delete 2>&1; echo done", label="旧日志清理")
run("rm -rf /var/cache/apt/archives/*.deb 2>&1; echo apt-cache-cleaned", label="apt 缓存")
run("rm -rf /root/.cache /tmp/* 2>&1; echo tmp-cleaned", label="缓存/临时文件")

# === 6. docker 容器日志截断(防止日志文件膨胀) ===
run("for f in /var/lib/docker/containers/*/*-json.log; do [ -f \"$f\" ] && echo \"$f: $(du -sh \"$f\" | awk '{print $1}')\"; done 2>&1 | head -20", label="容器日志文件大小")
run("for f in /var/lib/docker/containers/*/*-json.log; do [ -f \"$f\" ] && truncate -s 0 \"$f\"; done; echo logs-truncated", label="截断容器日志")

# === 7. 清理后磁盘 ===
run("df -h / 2>/dev/null", label="清理后磁盘")
run("du -sh /var/lib/containerd 2>/dev/null")
run("du -sh /var/lib/docker 2>/dev/null")
run("docker system df 2>&1", label="docker 系统占用")

# === 8. 检查面板日志报错 ===
run("docker logs --since 30m nexus-panel 2>&1 | grep -iE 'error|panic|fatal|SQLSTATE' | tail -30", label="面板最近30分钟报错", timeout=60)
run("docker logs --since 30m nexus-postgres 2>&1 | grep -iE 'error|fatal' | tail -20", label="postgres 最近30分钟报错", timeout=60)

ssh.close()
print("\n[+] 完成")
