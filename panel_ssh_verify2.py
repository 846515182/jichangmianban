#!/usr/bin/env python3
"""确认新容器(一键更新后)是否还报 SQL 错误 + 面板最终状态。"""
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
print("[+] 面板 SSH 登录成功\n")


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


print("=== 新容器启动时间 + 当前时间 ===")
run("date 2>&1")
run("docker ps --format '{{.Names}}|{{.RunningFor}}|{{.Status}}'")

print("\n=== 新容器启动后的所有 ERROR 日志 ===")
# 只看新容器启动后的日志(1784312513 之后)
run("docker logs nexus-panel 2>&1 | python3 -c \"import sys,json; [print(l.rstrip()) for l in sys.stdin if l.strip() and json.loads(l).get('ts',0) > 1784312513 and json.loads(l).get('level') in ('error','warn')]\" 2>&1 | tail -20")

print("\n=== 新容器启动后是否还有 SQL LIKE 报错 ===")
run("docker logs nexus-panel --since 20m 2>&1 | grep -iE 'LIKE.*node|SQLSTATE 42883|clean aggregate' | tail -10")

print("\n=== 新容器启动后 cron 执行情况 ===")
run("docker logs nexus-panel --since 20m 2>&1 | grep -iE 'cron|cleaned|清理|巡检|aggregate' | tail -15")

print("\n=== 面板最终磁盘 ===")
run("df -h / 2>&1")
run("docker system df 2>&1")

print("\n=== 节点状态(DB) ===")
run("docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT name, online, last_seen_at, EXTRACT(EPOCH FROM (NOW() - last_seen_at))::int AS secs_since_seen FROM nodes WHERE is_deleted=false;\" 2>&1")

print("\n=== 面板 gRPC 最近连接(新容器) ===")
run("docker logs nexus-panel --since 20m 2>&1 | grep -iE 'register|heartbeat|rpc' | tail -10")

ssh.close()
print("\n[+] 完成")
