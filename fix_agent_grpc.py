#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""修复 agent gRPC TLS: 改用域名 bbcdtv.top:50051 + 系统 CA 池, 重启 agent"""
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

def run_via_panel(cmd, timeout=60, label=""):
    print(f"\n=== {label or cmd[:60]} ===")
    full = f'ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 {NODE_USER}@{NODE_HOST} {repr(cmd)} 2>&1'
    _, o, _ = panel.exec_command(full, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    print(out)
    return out

# 1. 先确认节点的 bbcdtv.top 能解析到面板 IP
run_via_panel('getent hosts bbcdtv.top 2>&1; echo "---"; nslookup bbcdtv.top 2>&1 | head -10', label="1. DNS 解析 bbcdtv.top")

# 2. 备份当前 .env.node
run_via_panel('cp /root/node-agent-1e8b92e3/.env.node /root/node-agent-1e8b92e3/.env.node.bak.$(date +%s) && cat /root/node-agent-1e8b92e3/.env.node', label="2. 备份并查看 .env.node")

# 3. 修改 .env.node: PANEL_GRPC_ADDR 改为域名, GRPC_TLS_CA 改为系统 CA 池
#    - 域名 bbcdtv.top 让 TLS ServerName 校验通过(证书 SAN 含 bbcdtv.top)
#    - 系统 CA 池含 ISRG Root X1, 可信 Let's Encrypt 证书链
new_env = """CONTAINER_NAME=nexus-agent-1e8b92e3
PANEL_GRPC_ADDR=bbcdtv.top:50051
NODE_TOKEN=24d68a6187887379b0b890c23c9cd60affec88e02a6f443928902adaa8e52561
LISTEN_PORT=443
HEALTH_PORT=50052
XRAY_VERSION=v26.6.1
GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt
"""
import shlex
run_via_panel(f'cat > /root/node-agent-1e8b92e3/.env.node <<\'ENVEOF\'\n{new_env}ENVEOF', label="3. 写入新 .env.node")
run_via_panel('cat /root/node-agent-1e8b92e3/.env.node', label="3b. 验证 .env.node")

# 4. 重启 agent 容器
run_via_panel('cd /root/node-agent-1e8b92e3 && docker compose -f docker-compose.node.yml --env-file .env.node down 2>&1; docker compose -f docker-compose.node.yml --env-file .env.node up -d 2>&1 | tail -10', timeout=120, label="4. 重启 agent 容器")

# 5. 等待容器启动 + bootstrap (最多 30s 重试)
time.sleep(15)
run_via_panel('docker ps --format "table {{.Names}}\\t{{.Status}}\\t{{.Ports}}" 2>&1', label="5. 容器状态")

# 6. 查看 agent 日志
run_via_panel('docker logs --tail 60 nexus-agent-1e8b92e3 2>&1', label="6. agent 日志(看是否注册成功)")

# 7. 等待更多时间(bootstrap 30 次重试)
time.sleep(20)
run_via_panel('docker logs --tail 30 nexus-agent-1e8b92e3 2>&1', label="7. 再看 agent 日志")

panel.close()
print("\n✓ 完成")
