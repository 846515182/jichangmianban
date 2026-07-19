#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 docker compose up -d 重建 panel 容器(强制重新读取 .env)"""
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

def panel_run(cmd, timeout=120, label=""):
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

# 1. 验证 .env 真的没有 GRPC_TLS_CA (确认 sed 改对了)
panel_run('grep -E "^GRPC_TLS_CA" /root/nexus-panel/.env 2>&1 || echo "NO_GRPC_TLS_CA" 2>&1', label="1. 确认 .env 中没有生效的 GRPC_TLS_CA")

# 2. 用 docker compose up -d 重建 panel (强制重新读 env_file)
#    之前用 docker restart 不行, 因为 env_file 在容器创建时读取, restart 不重新读
panel_run('cd /root/nexus-panel && docker compose up -d panel 2>&1 | tail -15', label="2. docker compose up -d panel (重建)")

# 3. 等待 panel 起来
print("\n=== 3. 等待 panel 起来 ===")
for i in range(30):
    time.sleep(3)
    res = panel_run(f'curl -s -o /dev/null -w "%{{http_code}}" http://127.0.0.1:8080/healthz 2>&1', label=f"3-{i+1}. health check")
    if '200' in res:
        print("✓ Panel 已恢复")
        break

# 4. 验证 panel 容器内 env 没 GRPC_TLS_CA
panel_run('docker exec nexus-panel env | grep -i GRPC 2>&1', label="4. panel 容器内 GRPC env")

# 5. 重启 agent
run_via_panel('docker restart nexus-agent-1e8b92e3 2>&1', label="5. 重启 agent")

# 6. 等 30s 看日志
time.sleep(30)
run_via_panel('docker logs --tail 30 nexus-agent-1e8b92e3 2>&1', label="6. agent 日志")

# 7. 节点状态
panel_run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, online, last_seen_at, version FROM nodes WHERE is_deleted=false;"', label="7. 节点状态")

panel.close()
print("\n✓ 完成")
