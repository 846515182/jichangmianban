#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
诊断 START_FAIL 错误: 容器启动后 Process exited with status 1
排查方向:
  1. 容器日志 (docker logs)
  2. 端口冲突 (ss/netstat)
  3. Docker daemon 状态
  4. .env.node 配置
  5. agent 二进制是否能独立运行
"""

import paramiko
import socks
import sys
import socket
import time

# 节点服务器凭证(从前期会话得知)
NODE_HOST = "38.59.246.203"
NODE_PORT = 22
NODE_USER = "root"
NODE_PASS = "3Cxeg14SKoI9fp43LZ"

# HTTP 代理(本地 sandbox 走代理才能出网)
HTTP_PROXY_HOST = "127.0.0.1"
HTTP_PROXY_PORT = 18080


def make_ssh():
    sock = socks.socksocket()
    sock.set_proxy(socks.HTTP, HTTP_PROXY_HOST, HTTP_PROXY_PORT)
    sock.settimeout(15)
    sock.connect((NODE_HOST, NODE_PORT))
    t = paramiko.Transport(sock)
    t.connect(username=NODE_USER, password=NODE_PASS)
    return paramiko.SSHClient()
    # 用 transport 包装


def ssh_run(transport, cmd, timeout=30):
    """通过 transport 执行命令"""
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
    print(f"=== 连接节点服务器 {NODE_HOST} ===")
    try:
        sock = socks.socksocket()
        sock.set_proxy(socks.HTTP, HTTP_PROXY_HOST, HTTP_PROXY_PORT)
        sock.settimeout(15)
        sock.connect((NODE_HOST, NODE_PORT))
        t = paramiko.Transport(sock)
        t.connect(username=NODE_USER, password=NODE_PASS)
    except Exception as e:
        print(f"SSH 连接失败: {e}")
        sys.exit(1)
    print("SSH 连接成功\n")

    cmds = [
        ("1. 系统负载与内存", "uptime; echo '---'; free -m"),
        ("2. Docker daemon 状态", "systemctl is-active docker; systemctl status docker --no-pager 2>&1 | head -20"),
        ("3. 所有 nexus 相关容器", "docker ps -a --filter name=nexus 2>&1"),
        ("4. 443 / 50052 端口占用", "ss -tlnp 2>/dev/null | grep -E ':(443|50052|50051)' || netstat -tlnp 2>/dev/null | grep -E ':(443|50052|50051)'"),
        ("5. 部署目录文件清单", "ls -la /root/node-agent-1e8b92e3/ 2>&1"),
        ("6. .env.node 内容(脱敏)", "cat /root/node-agent-1e8b92e3/.env.node 2>&1 | sed 's/NODE_TOKEN=.*/NODE_TOKEN=***REDACTED***/'"),
        ("7. agent 容器最近日志(200 行)", "docker logs --tail 200 nexus-agent-1e8b92e3 2>&1"),
        ("8. agent 容器详情", "docker inspect nexus-agent-1e8b92e3 2>&1 | head -100"),
        ("9. /etc/hosts 是否有 bbcdtv.top 映射", "grep -E 'bbcdtv|nexus' /etc/hosts || echo '(无映射)'"),
        ("10. Xray 是否还在跑", "pgrep -af xray 2>&1"),
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
