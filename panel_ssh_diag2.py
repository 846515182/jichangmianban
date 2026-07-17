#!/usr/bin/env python3
"""诊断2: gRPC 监听 + 完整错误日志 + 磁盘大头 + 节点连通性。"""
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
print("=== A. 面板 gRPC 监听状态(50051) ===")
print("=" * 60)
run("ss -tlnp | grep -E '50051|8080' 2>&1")
run("docker exec nexus-panel ss -tlnp 2>&1 | grep -E '50051|8080'")

print("\n=== B. 面板 gRPC 日志(agent 连接尝试) ===")
run("docker logs nexus-panel 2>&1 | grep -iE 'grpc|register|heartbeat|agent|节点.*注册|节点.*心跳' | tail -30")

print("\n=== C. 那个 LIKE 'node:%' SQL 的完整上下文 ===")
run("docker logs nexus-panel 2>&1 | grep -B2 -A2 \"LIKE 'node:\" | tail -30")

print("\n=== D. 面板所有 WARN/ERROR 日志 ===")
run("docker logs nexus-panel 2>&1 | grep -iE '\"level\":\"(warn|error)\"' | tail -30")

print("\n=== E. 磁盘大头排查 ===")
run("df -h / 2>&1")
run("ls -lh /swapfile 2>&1")
run("du -sh /* 2>/dev/null | sort -h | tail -15")
run("du -sh /var/* 2>/dev/null | sort -h | tail -10")
run("du -sh /var/lib/* 2>/dev/null | sort -h | tail -10")

print("\n=== F. 节点服务器连通性(从面板测) ===")
# 测试节点 gRPC 端口
run("timeout 5 bash -c 'echo > /dev/tcp/38.59.246.203/50052' 2>&1 && echo 'node:50052 OK' || echo 'node:50052 FAIL'")
run("timeout 5 bash -c 'echo > /dev/tcp/38.59.246.203/443' 2>&1 && echo 'node:443 OK' || echo 'node:443 FAIL'")
run("timeout 5 bash -c 'echo > /dev/tcp/38.59.246.203/22' 2>&1 && echo 'node:22 OK' || echo 'node:22 FAIL'")

print("\n=== G. 节点表完整信息(server_address/grpc_port) ===")
run("docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT name, server_address, port, grpc_port, online, is_enabled, version FROM nodes WHERE is_deleted=false;\" 2>&1")

print("\n=== H. 面板 gRPC 端口对外监听(0.0.0.0 vs 127.0.0.1) ===")
run("docker port nexus-panel 2>&1")
run("docker inspect nexus-panel --format '{{json .HostConfig.PortBindings}}' 2>&1")
run("iptables -t nat -L DOCKER -n 2>&1 | grep -E '50051|8080' | head -5")

ssh.close()
print("\n[+] 完成")
