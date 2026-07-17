#!/usr/bin/env python3
"""通过 HTTP 隧道代理 SSH 登录面板服务器, 执行存储清理与日志检查。
代码只读不改(代码修改已在 GitHub 完成), 仅做运维清理。
"""
import socks
import socket
import paramiko
import sys

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

sock = socks.socksocket()
sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
sock.settimeout(30)
print(f"[*] 经 {PROXY_HOST}:{PROXY_PORT} 代理连接 {PANEL_HOST}:{PANEL_PORT} ...")
sock.connect((PANEL_HOST, PANEL_PORT))
print("[+] TCP 连接已建立")

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(
    PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS,
    sock=sock, timeout=30, look_for_keys=False, allow_agent=False,
)
print("[+] SSH 登录成功\n")


def run(cmd, timeout=180):
    print(f"\n$ {cmd}")
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


run("df -h / /var 2>/dev/null | head -20")
run("du -sh /var/lib/containerd 2>/dev/null")
run("du -sh /var/lib/docker 2>/dev/null")
run("docker system df 2>&1 | head -20")

print("\n" + "=" * 60)
print("=== 阶段1: 清理悬空镜像/停止容器/构建缓存 ===")
print("=" * 60)
run("docker container prune -f 2>&1 | tail -5")
run("docker image prune -a -f 2>&1 | tail -10", timeout=240)
run("docker builder prune -a -f 2>&1 | tail -5", timeout=240)
run("docker network prune -f 2>&1 | tail -3")

print("\n" + "=" * 60)
print("=== 阶段2: containerd 残留检查 ===")
print("=" * 60)
run("du -sh /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs 2>/dev/null")
run("ctr -n moby snapshots --snapshotter overlayfs list 2>&1 | wc -l")

print("\n" + "=" * 60)
print("=== 阶段3: 系统级清理(apt/journal/临时文件) ===")
print("=" * 60)
run("apt-get clean 2>&1 | tail -3")
run("apt-get autoremove -y 2>&1 | tail -5", timeout=240)
run("journalctl --vacuum-time=2d 2>&1 | tail -5")
run("find /var/log -type f \\( -name '*.gz' -o -name '*.1' \\) -mtime +7 -delete 2>&1; true")

print("\n" + "=" * 60)
print("=== 清理结果 ===")
print("=" * 60)
run("df -h / /var 2>/dev/null | head -20")
run("du -sh /var/lib/containerd 2>/dev/null")
run("du -sh /var/lib/docker 2>/dev/null")
run("docker system df 2>&1 | head -20")

print("\n" + "=" * 60)
print("=== 面板容器日志(最近报错) ===")
print("=" * 60)
run("docker ps --format '{{.Names}}\t{{.Status}}' 2>&1 | head -20")
run("docker logs --tail 200 nexus-panel 2>&1 | grep -iE 'error|panic|fatal' | tail -30", timeout=60)
run("docker logs --tail 50 nexus-panel 2>&1 | tail -30", timeout=60)

ssh.close()
print("\n[+] 完成")
