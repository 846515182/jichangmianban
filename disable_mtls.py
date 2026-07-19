#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""禁用面板 gRPC mTLS (去掉 GRPC_TLS_CA), 改为单向 TLS, 重启 panel"""
import paramiko, sys, io, socket, socks, time, base64
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

def run_via_panel(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    full = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 {NODE_USER}@{NODE_HOST} {repr(cmd)} 2>&1'
    _, o, _ = panel.exec_command(full, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    print(out)
    return out

# 1. 备份 .env
panel_run('cp /root/nexus-panel/.env /root/nexus-panel/.env.bak.$(date +%s)', label="1. 备份 .env")

# 2. 注释掉 GRPC_TLS_CA (保留 CERT/KEY 做单向 TLS, 去掉 CA 不再要求客户端证书)
#    用 sed 把所有 GRPC_TLS_CA 行注释掉
panel_run("sed -i 's|^GRPC_TLS_CA=|# GRPC_TLS_CA disabled for single-direction TLS (agent has no client cert)|g' /root/nexus-panel/.env", label="2. 注释掉 GRPC_TLS_CA")
panel_run('grep -E "GRPC_TLS|GRPC_LISTEN" /root/nexus-panel/.env', label="2b. 验证 .env")

# 3. 重启 panel 容器 (用 helper 容器方案, 避免自杀)
#    panel 是 nexus-panel, 用 docker restart 即可(docker compose up 会重建)
#    因为 panel 容器配置 entrypoint 加载 git/docker-cli, 直接 docker restart 会重新读取环境变量
print("\n=== 3. 重启 panel 容器 ===")
panel_run('docker restart nexus-panel 2>&1', label="3a. docker restart nexus-panel")

# 4. 等待 panel 起来
print("\n=== 4. 等待 panel 起来 ===")
for i in range(20):
    time.sleep(3)
    res = panel_run(f'curl -s -o /dev/null -w "%{{http_code}}" http://127.0.0.1:8080/healthz 2>&1', label=f"4-{i+1}. health check")
    if '200' in res:
        print("✓ Panel 已恢复")
        break

# 5. 验证 panel gRPC 50051 监听
panel_run('docker exec nexus-panel ss -tlnp 2>/dev/null | grep 50051 || docker exec nexus-panel netstat -tlnp 2>/dev/null | grep 50051', label="5. gRPC 50051 监听")

# 6. 重启节点 agent (重新发起 TLS 握手)
run_via_panel('docker restart nexus-agent-1e8b92e3 2>&1', label="6. 重启 agent")

# 7. 等 30s 看 agent 日志
time.sleep(30)
run_via_panel('docker logs --tail 40 nexus-agent-1e8b92e3 2>&1', label="7. agent 日志")

# 8. 检查面板节点 online 状态
print("\n=== 8. 检查面板节点状态 ===")
panel_run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, online, last_seen_at, version FROM nodes WHERE is_deleted=false;"', label="8. 节点状态")

panel.close()
print("\n✓ 完成")
