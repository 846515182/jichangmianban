#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
检查面板 .env 当前 mTLS / TLS 配置, 确认根因是否已彻底解决
"""

import paramiko
import socks
import sys

# 面板服务器凭证(从前期会话得知)
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

HTTP_PROXY_HOST = "127.0.0.1"
HTTP_PROXY_PORT = 18080


def ssh_run(transport, cmd, timeout=30):
    chan = transport.open_session()
    chan.settimeout(timeout)
    chan.exec_command(cmd)
    out = b""
    err = b""
    while True:
        if chan.recv_ready():
            out += chan.recv(65536)
        if chan.recv_stderr_ready():
            err += chan.recv_stderr(65536)
        if chan.exit_status_ready() and not chan.recv_ready() and not chan.recv_stderr_ready():
            break
    while chan.recv_ready():
        out += chan.recv(65536)
    while chan.recv_stderr_ready():
        err += chan.recv_stderr(65536)
    code = chan.recv_exit_status()
    chan.close()
    return out.decode("utf-8", errors="replace"), err.decode("utf-8", errors="replace"), code


def main():
    print(f"=== 连接面板服务器 {PANEL_HOST} ===")
    try:
        sock = socks.socksocket()
        sock.set_proxy(socks.HTTP, HTTP_PROXY_HOST, HTTP_PROXY_PORT)
        sock.settimeout(15)
        sock.connect((PANEL_HOST, PANEL_PORT))
        t = paramiko.Transport(sock)
        t.connect(username=PANEL_USER, password=PANEL_PASS)
    except Exception as e:
        print(f"SSH 连接失败: {e}")
        sys.exit(1)
    print("SSH 连接成功\n")

    cmds = [
        ("1. .env 中 GRPC_TLS 相关配置(脱敏)", "grep -nE 'GRPC_TLS|GRPC_LISTEN|PANEL_DOMAIN' /root/nexus-panel/.env 2>&1 | sed 's/=.*/=***/'"),
        ("2. .env GRPC_TLS_CA 原始行", "grep -nE 'GRPC_TLS_CA' /root/nexus-panel/.env 2>&1"),
        ("3. panel 容器内 GRPC_TLS 相关 env", "docker exec nexus-panel sh -c 'env | grep -E GRPC_TLS 2>&1 || echo NO_GRPC_TLS_ENV'"),
        ("4. deployments/tls 目录文件", "ls -la /root/nexus-panel/deployments/tls/ 2>&1"),
        ("5. ca.crt / ca.key 是否存在", "ls -la /root/nexus-panel/deployments/tls/ca.* 2>&1"),
        ("6. ca.crt 过期时间", "openssl x509 -in /root/nexus-panel/deployments/tls/ca.crt -noout -dates -subject 2>&1 || echo '(无 ca.crt)'"),
        ("7. panel 容器是否运行", "docker ps --filter name=nexus-panel 2>&1"),
        ("8. panel 容器启动时间", "docker inspect nexus-panel --format '{{.State.StartedAt}} restartCount={{.RestartCount}}' 2>&1"),
        ("9. gRPC 端口监听", "ss -tlnp 2>/dev/null | grep 50051 || netstat -tlnp 2>/dev/null | grep 50051"),
        ("10. 面板版本+健康检查", "docker exec nexus-panel wget -qO- http://127.0.0.1:8080/healthz 2>&1 | head -5; echo '---'; docker exec nexus-panel /app/nexus-panel --version 2>&1 | head -3 || echo 'no --version flag'"),
        ("11. .env 文件最后 30 行(确认格式正确)", "tail -30 /root/nexus-panel/.env | cat -A | head -40"),
        ("12. git log 最近 5 次提交", "cd /root/nexus-panel && git log --oneline -5 2>&1"),
        ("13. git status 看是否有未提交修改", "cd /root/nexus-panel && git status 2>&1 | head -30"),
        ("14. panel 启动日志(看是否启用了 mTLS)", "docker logs --tail 100 nexus-panel 2>&1 | grep -iE 'tls|grpc|mTLS|证书' || echo '(无相关日志)'"),
    ]

    for title, cmd in cmds:
        print(f"\n=== {title} ===")
        out, err, code = ssh_run(t, cmd, timeout=20)
        if err.strip():
            print(f"[stderr] {err}")
        print(out)
        if code != 0 and not out.strip():
            print(f"[exit={code}] (无 stdout)")

    t.close()


if __name__ == "__main__":
    main()
