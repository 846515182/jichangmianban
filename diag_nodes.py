#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""节点掉线排查"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD = 'eH62M3CcaSep59J8lZ'
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=60, banner_timeout=60)
print("=== SSH 连接成功 ===\n")

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 所有节点状态
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, name, server_address, port, grpc_port, online, last_seen_at, is_enabled FROM nodes WHERE is_deleted=false ORDER BY name"', 10, "1. 所有节点状态")

# 2. panel 启动时间 vs 节点最后心跳时间
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, online, last_seen_at, NOW() - last_seen_at AS since_last_seen FROM nodes WHERE is_deleted=false ORDER BY name"', 10, "2. 节点最后心跳距今时长")

# 3. panel 启动时间
run('docker exec nexus-panel sh -c "stat /proc/1 | grep Modify"', 5, "3a. panel 容器启动时间")
run('curl -s http://127.0.0.1:8080/healthz', 5, "3b. panel boot_time")

# 4. gRPC 服务监听状态
run('docker exec nexus-panel sh -c "netstat -tlnp 2>/dev/null | grep 50051 || ss -tlnp | grep 50051"', 10, "4. gRPC 50051 监听状态")

# 5. gRPC 端口外部可达性(节点连过来的端口)
run('ss -tlnp | grep 50051', 5, "5. 宿主机 50051 监听")

# 6. panel 日志里 gRPC 相关
run('docker logs nexus-panel --since 30m 2>&1 | grep -E "gRPC|grpc|node|agent|heartbeat|心跳|节点" | tail -30', 10, "6. panel 日志 gRPC/节点相关")

# 7. panel 日志里最近的 ERROR/WARN
run('docker logs nexus-panel --since 30m 2>&1 | grep -E "ERROR|WARN|panic|FATAL" | tail -20', 10, "7. panel 日志错误")

# 8. 看 MarkStaleNodesOffline 的日志
run('docker logs nexus-panel 2>&1 | grep -E "MarkStale|stale|offline|离线" | tail -20', 10, "8. 节点离线标记日志")

# 9. 看 docker-compose.yml 里 gRPC 端口暴露
run('grep -A 3 "50051" /root/nexus-panel/docker-compose.yml', 5, "9. docker-compose gRPC 端口配置")

# 10. 防火墙规则
run('iptables -L -n 2>&1 | head -20', 10, "10. iptables 规则")
run('ufw status 2>&1 || echo "ufw not installed"', 5, "10b. ufw 状态")

# 11. 节点 agent 配置(如果有)
run('ls -la /root/nexus-panel/node_agent/ 2>&1 | head -10', 5, "11a. node_agent 目录")
run('find /root/nexus-panel -name "config*.yaml" -o -name "agent*.yaml" 2>/dev/null | head -5', 5, "11b. agent 配置文件")

c.close()
print("\n=== 完成 ===")
