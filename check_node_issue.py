#!/usr/bin/env python3
"""深入诊断: 1) 新代码是否部署成功 2) proxy_reachable=0 是什么"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD  = 'eH62M3CcaSep59J8lZ'
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080
REDIS_PWD = 'n3xus_r3dis_2026'

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)

def run(cmd, timeout=20, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 1. 看面板二进制构建时间 (是否新代码已部署)
run('docker exec nexus-panel ls -la /app/nexus-panel 2>/dev/null || docker exec nexus-panel ls -la /app/ 2>/dev/null', 10, "面板二进制时间")

# 2. git-pull 日志
run('docker exec nexus-panel cat /root/nexus-panel/.update-state/git-pull.log 2>/dev/null | tail -50', 10, "git-pull 日志")

# 3. git-pull state
run('docker exec nexus-panel cat /root/nexus-panel/.update-state/git-pull.state 2>/dev/null', 5, "git-pull state")

# 4. 面板启动时间 (是否已重启)
run('docker exec nexus-panel stat /proc/1/cmdline 2>/dev/null | grep -E "Modify|Access"', 10, "panel 容器 PID1 启动时间")
run('docker inspect nexus-panel --format "{{.State.StartedAt}}"', 10, "panel 容器启动时间")

# 5. 面板版本号
run('docker exec nexus-panel /app/nexus-panel --version 2>&1 | head -3 || docker exec nexus-panel nexus-panel --version 2>&1 | head -3', 10, "面板版本")

# 6. 当前 git HEAD (确认是否拉到 96af7e8)
run('cd /root/nexus-panel && git log --oneline -3 2>&1', 10, "服务器仓库 HEAD")

# 7. 节点 agent 是不是检测到代理不通 (proxy_reachable=0)
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGET "node:heartbeat:1e8b92e3-ff15-49d5-b6b8-b7db1f12aeb4" proxy_reachable', 5, "代理可达性")
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGET "node:heartbeat:1e8b92e3-ff15-49d5-b6b8-b7db1f12aeb4" proxy_error', 5, "代理错误信息")
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning HGET "node:heartbeat:1e8b92e3-ff15-49d5-b6b8-b7db1f12aeb4" proxy_latency', 5, "代理延迟")

# 8. 美国01 节点的配置 (server_address 等)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, name, server_address, port, protocol, is_enabled, online FROM nodes WHERE name LIKE \'%美国01%\';"', 10, "美国01 配置")

# 9. 看面板 admin_node.go 处理 proxy_reachable 的代码
run('grep -n "proxy_reachable\\|ProxyReachable" /workspace/backend/internal/handler/admin_node.go 2>/dev/null | head -10', 5, "代码中 proxy_reachable 处理")

c.close()
print("=== done ===")
