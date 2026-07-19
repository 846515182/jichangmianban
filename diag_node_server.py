#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""登录节点服务器查 agent 状态 + 日志"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

# 节点服务器
HOST = '38.59.246.203'
PORT = 22
USER = 'root'
PWD = '3Cxeg14SKol9fp43LZ'
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
print("=== SSH 连接成功(节点服务器 38.59.246.203) ===\n")

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. agent 容器状态
run('docker ps -a --filter name=nexus --format "{{.Names}} | {{.Status}} | {{.Image}}"', 10, "1. agent 容器状态")

# 2. agent 最近 50 行日志
run('docker logs nexus-node-agent --tail 80 2>&1', 10, "2. agent 日志(最近 80 行)")

# 3. 看是否有 FATAL / fatalShutdown / 停服 字样
run('docker logs nexus-node-agent 2>&1 | grep -iE "FATAL|fatal|shutdown|停服|token|拒绝|未授权|NotFound|Unauthenticated" | tail -30', 10, "3. 致命错误相关日志")

# 4. agent 启动时间
run('docker inspect nexus-node-agent --format "{{.State.StartedAt}}" 2>&1', 5, "4. agent 容器启动时间")

# 5. agent 重启次数
run('docker inspect nexus-node-agent --format "RestartCount={{.RestartCount}} OOMKilled={{.State.OOMKilled}} ExitCode={{.State.ExitCode}}" 2>&1', 5, "5. agent 重启次数/OOM/退出码")

# 6. agent 配置环境变量(看连的是哪个 panel gRPC 地址)
run('docker inspect nexus-node-agent --format "{{range .Config.Env}}{{println .}}{{end}}" 2>&1 | grep -E "PANEL|NODE|XRAY|LISTEN|HEALTH"', 5, "6. agent 环境变量")

# 7. 当前能不能连到 panel gRPC(从节点服务器测)
run('nc -zv -w 5 192.129.242.242 50051 2>&1 || timeout 5 bash -c "echo > /dev/tcp/192.129.242.242/50051" 2>&1', 10, "7. 节点到 panel gRPC 50051 连通性")

# 8. Xray 进程状态
run('docker exec nexus-node-agent sh -c "pgrep -a xray || echo XRAY_NOT_RUNNING" 2>&1', 10, "8. Xray 进程状态")

# 9. agent 健康检查端点
run('curl -s http://127.0.0.1:50052/healthz 2>&1', 5, "9. agent /healthz")
run('curl -s http://127.0.0.1:50052/livez 2>&1', 5, "9b. agent /livez")

# 10. 节点服务器资源
run('free -h && echo "---" && df -h / && echo "---" && uptime', 10, "10. 节点服务器资源")

c.close()
print("\n=== 完成 ===")
