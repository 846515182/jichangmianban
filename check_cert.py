#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""快速确认面板 server.crt 证书类型(SAN/签发者), 顺便看下当前 panel/agent 都正常"""
import paramiko, socks, sys

PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080


def ssh_run(t, cmd, timeout=20):
    chan = t.open_session()
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
    chan.close()
    return out.decode("utf-8", "replace"), err.decode("utf-8", "replace")


def main():
    print(f"=== 连接面板 {PANEL_HOST} ===")
    sock = socks.socksocket()
    sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    sock.settimeout(15)
    sock.connect((PANEL_HOST, PANEL_PORT))
    t = paramiko.Transport(sock)
    t.connect(username=PANEL_USER, password=PANEL_PASS)

    cmds = [
        ("1. server.crt 颁发者/Subject/SAN", "openssl x509 -in /root/nexus-panel/deployments/tls/server.crt -noout -subject -issuer -ext subjectAltName 2>&1"),
        ("2. server.crt 有效期", "openssl x509 -in /root/nexus-panel/deployments/tls/server.crt -noout -dates 2>&1"),
        ("3. ca.crt 是否签发了 server.crt", "openssl verify -CAfile /root/nexus-panel/deployments/tls/ca.crt /root/nexus-panel/deployments/tls/server.crt 2>&1"),
        ("4. 节点列表(API)", "curl -sk https://127.0.0.1/api/admin/nodes 2>&1 | head -20 || echo '(需认证)'"),
        ("5. 直接查 DB 节点状态", "docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT name, online, last_seen_at, version FROM nodes WHERE is_deleted=false;\" 2>&1"),
        ("6. agent 容器最近心跳(面板侧视角)", "docker logs --tail 5 nexus-panel 2>&1 | grep -E 'heartbeat|Heartbeat' | head -5 || echo '(无心跳日志)'"),
        ("7. agent .env.node 中的 GRPC_TLS_CA", "ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@38.59.246.203 'cat /root/node-agent-1e8b92e3/.env.node | grep GRPC_TLS_CA' 2>&1"),
        ("8. server.crt 是否是 Let's Encrypt", "openssl x509 -in /root/nexus-panel/deployments/tls/server.crt -noout -issuer 2>&1 | grep -iE 'let.s encrypt|r3|e1|r10|r11' || echo 'NOT_LE'"),
    ]

    for title, cmd in cmds:
        print(f"\n=== {title} ===")
        out, err = ssh_run(t, cmd)
        if err.strip():
            print(f"[stderr] {err}")
        print(out)

    t.close()


if __name__ == "__main__":
    main()
