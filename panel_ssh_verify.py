#!/usr/bin/env python3
"""检查服务器代码是否已包含修复。"""
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
sock.connect((PANEL_HOST, PANEL_PORT))
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(
    PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS,
    sock=sock, timeout=30, look_for_keys=False, allow_agent=False,
)
print("[+] SSH 登录成功\n")


def run(cmd, timeout=60):
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


# 检查 cron_service.go 是否有 8 分钟阈值(我的修复)
run("cd /root/nexus-panel && grep -n '8.*time.Minute' backend/internal/service/cron_service.go 2>&1 | head -5")

# 检查 main.go 是否有 3 分钟启动延迟(我的修复)
run("cd /root/nexus-panel && grep -n '3.*time.Minute.*startupGrace\\|startupGrace' backend/cmd/server/main.go 2>&1 | head -5")

# 检查 node_agent main.go 是否有 10s 补发心跳(我的修复)
run("cd /root/nexus-panel && grep -n '10.*time.Second\\|doHeartbeat.*bool' node_agent/main.go 2>&1 | head -5")

# 检查 node_agent client.go 是否有 INTERNAL 重试(我的修复)
run("cd /root/nexus-panel && grep -n 'INTERNAL.*UNKNOWN\\|retryableStatusCodes' node_agent/client.go 2>&1 | head -5")

# 检查 CleanAggregateTrafficLogs 是否移除了 LIKE
run("cd /root/nexus-panel && grep -n \"LIKE.*node:\" backend/internal/service/cron_service.go 2>&1 | head -5")

# git fetch 看看 GitHub 是否有更新的 commit
run("cd /root/nexus-panel && git fetch origin 2>&1 | tail -5")
run("cd /root/nexus-panel && git log --oneline -5 origin/main 2>&1")
run("cd /root/nexus-panel && git log --oneline -5 HEAD 2>&1")

# 检查 HEAD 和 origin/main 是否一致
run("cd /root/nexus-panel && git rev-parse HEAD origin/main 2>&1")

ssh.close()
print("\n[+] 完成")
