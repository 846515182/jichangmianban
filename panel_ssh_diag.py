#!/usr/bin/env python3
"""诊断: 磁盘占用 + 节点在线状态 + 当前部署版本。"""
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


def run(cmd, timeout=120):
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


print("=" * 60)
print("=== A. 当前部署版本(确认一键更新是否生效) ===")
print("=" * 60)
run("cd /root/nexus-panel && git rev-parse HEAD 2>&1")
run("cd /root/nexus-panel && git log --oneline -3 2>&1")
# 检查修复是否在代码里
run("cd /root/nexus-panel && grep -n 'startupGrace\\|3.*time.Minute.*startup' backend/cmd/server/main.go 2>&1 | head -3")
run("cd /root/nexus-panel && grep -n '8.*time.Minute' backend/internal/service/cron_service.go 2>&1 | head -3")
# 容器启动时间(确认是否重建过)
run("docker ps --format '{{.Names}}|{{.RunningFor}}|{{.Status}}'")

print("\n" + "=" * 60)
print("=== B. 磁盘占用分布(35G 用在哪) ===")
print("=" * 60)
run("df -h / 2>&1")
run("du -sh /var/lib/docker/* 2>/dev/null | sort -h")
run("du -sh /var/lib/containerd 2>/dev/null")
run("du -sh /root/* 2>/dev/null | sort -h | tail -15")
run("du -sh /var/log/* 2>/dev/null | sort -h | tail -10")
run("du -sh /tmp/* 2>/dev/null | sort -h | tail -5")
run("docker system df 2>&1")
# 系统层占用
run("du -sh /usr/* 2>/dev/null | sort -h | tail -8")
# 找大文件
run("find / -type f -size +500M 2>/dev/null | head -20")

print("\n" + "=" * 60)
print("=== C. 节点在线状态 ===")
print("=" * 60)
# 查询数据库 nodes 表状态
run("docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT id, name, online, last_seen_at, EXTRACT(EPOCH FROM (NOW() - last_seen_at))::int AS secs_since_seen FROM nodes WHERE is_deleted=false;\" 2>&1")
# 面板日志最近的心跳相关
run("docker logs --tail 100 nexus-panel 2>&1 | grep -iE 'heartbeat|心跳|node|节点' | tail -20")
# 面板最近所有日志(看是否有报错)
run("docker logs --tail 50 nexus-panel 2>&1 | tail -50")

ssh.close()
print("\n[+] 完成")
