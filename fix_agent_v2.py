#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""修复 agent gRPC TLS: /etc/hosts 强制 bbcdtv.top->面板IP + 域名连接 + 系统 CA 池"""
import paramiko, sys, io, socket, socks, time, base64, shlex
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

def write_file_via_panel(remote_path, content):
    """通过 base64 + ssh echo 写文件, 避免 heredoc 嵌套问题"""
    b64 = base64.b64encode(content.encode()).decode()
    cmd = f'echo {b64} | base64 -d > {remote_path} && echo OK'
    return run_via_panel(cmd, label=f"写入 {remote_path}")

# 1. 在节点 /etc/hosts 加一行强制 bbcdtv.top 解析到面板 IP (避免走 Cloudflare CDN)
print("\n=== 1. 检查/添加 /etc/hosts 映射 ===")
hosts_check = run_via_panel('grep bbcdtv.top /etc/hosts 2>&1', label="1a. 检查现有映射")
if '192.129.242.242' not in hosts_check or 'bbcdtv.top' not in hosts_check:
    run_via_panel('echo "192.129.242.242 bbcdtv.top" >> /etc/hosts && grep bbcdtv.top /etc/hosts', label="1b. 添加映射")
run_via_panel('getent hosts bbcdtv.top', label="1c. 验证解析")

# 2. 用 base64 写入新 .env.node
new_env = """CONTAINER_NAME=nexus-agent-1e8b92e3
PANEL_GRPC_ADDR=bbcdtv.top:50051
NODE_TOKEN=24d68a6187887379b0b890c23c9cd60affec88e02a6f443928902adaa8e52561
LISTEN_PORT=443
HEALTH_PORT=50052
XRAY_VERSION=v26.6.1
GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt
"""
write_file_via_panel('/root/node-agent-1e8b92e3/.env.node', new_env)
run_via_panel('cat /root/node-agent-1e8b92e3/.env.node', label="2b. 验证 .env.node")

# 3. 先 down 干净(之前的 nexus-node-agent 默认名)
run_via_panel('docker rm -f nexus-node-agent nexus-agent-1e8b92e3 2>/dev/null; docker network rm node-agent-1e8b92e3_default 2>/dev/null; true', label="3. 清理旧容器")

# 4. 用 docker compose 启动 agent
run_via_panel('cd /root/node-agent-1e8b92e3 && docker compose -f docker-compose.node.yml --env-file .env.node up -d 2>&1 | tail -10', timeout=120, label="4. 启动 agent 容器")

# 5. 等待 bootstrap (30s 内首次重试, Xray 下载等)
time.sleep(15)
run_via_panel('docker ps --format "table {{.Names}}\\t{{.Status}}\\t{{.Ports}}" 2>&1', label="5. 容器状态")

# 6. agent 日志
run_via_panel('docker logs --tail 60 nexus-agent-1e8b92e3 2>&1', label="6. agent 日志")

# 7. 等更多时间, bootstrap 内 30 次重试, 每 5s 间隔, 最多 2.5 分钟
time.sleep(20)
run_via_panel('docker logs --tail 30 nexus-agent-1e8b92e3 2>&1', label="7. 再看 agent 日志")

# 8. 检查面板数据库, 节点是否 online
print("\n=== 8. 检查面板节点状态 ===")
_, o, _ = panel.exec_command('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name, online, last_seen_at, version FROM nodes WHERE is_deleted=false;"', timeout=20)
print(o.read().decode('utf-8','replace'))

panel.close()
print("\n✓ 完成")
