#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""检查面板 gRPC TLS 配置和证书, 修复 IP SAN 问题"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = '192.129.242.242'
PANEL_PORT = 22
PANEL_USER = 'root'
PANEL_PWD = 'eH62M3CcaSep59J8lZ'

NODE_HOST = '38.59.246.203'
NODE_USER = 'root'

PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock(host, port):
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((host, port))
    return s

panel = paramiko.SSHClient()
panel.set_missing_host_key_policy(paramiko.AutoAddPolicy())
panel.connect(PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PWD,
              sock=make_sock(PANEL_HOST, PANEL_PORT), timeout=30, banner_timeout=30, auth_timeout=30)

def panel_run(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    _, o, e = panel.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    if out: print(out)
    if err.strip(): print("STDERR:", err[:500])
    return out

# 1. 查看面板 .env 是否启用 GRPC_TLS
panel_run('grep -iE "GRPC|TLS" /root/nexus-panel/.env 2>&1', label="1. 面板 .env GRPC/TLS 配置")

# 2. 查看面板 gRPC 配置代码
panel_run('docker exec nexus-panel cat /app/tls/ 2>&1 | head; ls /root/nexus-panel/deployments/tls/ 2>&1', label="2. 面板 TLS 目录")

# 3. 看 gRPC server 启动配置
panel_run('docker exec nexus-panel grep -r "GRPC" /app/ 2>/dev/null | head -10; echo "---"; docker exec nexus-panel env | grep -iE "GRPC|TLS"', label="3. 面板容器内 gRPC/TLS env")

# 4. 看 grpc-ca.crt 内容
panel_run('ls -la /root/nexus-panel/node_agent/grpc-ca.crt 2>&1; openssl x509 -in /root/nexus-panel/node_agent/grpc-ca.crt -text -noout 2>&1 | head -25', label="4. grpc-ca.crt 证书内容")

# 5. 测试面板 50051 是否 TLS
panel_run('timeout 3 bash -c "echo > /dev/tcp/127.0.0.1/50051" 2>&1 && echo "50051 OK"; openssl s_client -connect 127.0.0.1:50051 -servername 192.129.242.242 < /dev/null 2>&1 | head -20', label="5. 测试 50051 是否 TLS")

# 6. 看 panel 是否真的用 TLS 监听 50051
panel_run('docker exec nexus-panel ss -tlnp 2>/dev/null | grep 50051; docker exec nexus-panel netstat -tlnp 2>/dev/null | grep 50051', label="6. panel 50051 监听情况")

panel.close()
print("\n✓ 完成")
