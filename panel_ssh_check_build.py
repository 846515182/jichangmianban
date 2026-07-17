#!/usr/bin/env python3
"""登录面板服务器排查卡住的 docker compose build"""
import socket
import socks
import paramiko
import sys

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

def connect():
    sock = socks.socksocket()
    sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    sock.settimeout(30)
    sock.connect((PANEL_HOST, PANEL_PORT))
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS, sock=sock, timeout=30)
    return client

def run(client, cmd, timeout=60):
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    return out + ("\n[STDERR]" + err if err.strip() else "")

def main():
    try:
        client = connect()
    except Exception as e:
        print(f"[连接失败] {e}")
        sys.exit(1)

    print("=" * 50)
    print("1. 检查 nexus-panel 容器进程是否在 build")
    print("=" * 50)
    print(run(client, "ps aux | grep -E 'docker compose build|buildkit|git reset' | grep -v grep | head -20"))

    print("=" * 50)
    print("2. 当前运行的 docker 容器")
    print("=" * 50)
    print(run(client, "docker ps -a --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' 2>&1 | head -20"))

    print("=" * 50)
    print("3. buildkit 状态")
    print("=" * 50)
    print(run(client, "docker ps -a --filter name=buildx --format '{{.Names}} {{.Status}}' 2>&1; docker buildx ls 2>&1 | head -10"))

    print("=" * 50)
    print("4. 磁盘空间")
    print("=" * 50)
    print(run(client, "df -h / 2>&1; echo '---'; docker system df 2>&1"))

    print("=" * 50)
    print("5. 面板 nexus-panel 容器日志最后 30 行")
    print("=" * 50)
    print(run(client, "docker logs --tail 30 nexus-panel 2>&1 | tail -30"))

    print("=" * 50)
    print("6. 检查是否有残留的 git fetch/reset 进程")
    print("=" * 50)
    print(run(client, "ps aux | grep -E 'git|compose' | grep -v grep 2>&1 | head -10"))

    print("=" * 50)
    print("7. /root/nexus-panel 目录状态")
    print("=" * 50)
    print(run(client, "cd /root/nexus-panel 2>/dev/null && git log --oneline -3 2>&1; echo '---'; ls -la docker-compose*.yml 2>&1 | head -5"))

    client.close()

if __name__ == "__main__":
    main()
