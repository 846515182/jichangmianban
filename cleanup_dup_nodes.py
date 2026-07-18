#!/usr/bin/env python3
"""清理重复节点 + 确认结果"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

from ops_config import NODE_HOST as HOST
PORT = 22
USER = 'root'
from ops_config import NODE_SSH_PWD as PWD
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080
from ops_config import REDIS_PWD

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

# 0. 删除前: 看看哪些节点有绑定到套餐 (不能误删)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT n.id, n.name, n.online, n.is_enabled, COUNT(b.plan_id) AS plan_count FROM nodes n LEFT JOIN node_plan_bindings b ON b.node_id = n.id WHERE n.name=\'美国01\' GROUP BY n.id ORDER BY n.online DESC, n.created_at ASC;"', 15, "删除前: 节点 + 套餐绑定数")

# 1. 找到真正要保留的节点 (online=true 的那个)
KEEP_ID = '1e8b92e3-ff15-49d5-b6b8-b7db1f12aeb4'

# 2. 先把其他重复节点的套餐绑定迁移到保留节点 (如果有)
run(f'docker exec nexus-postgres psql -U nexus -d nexus_panel -c "UPDATE node_plan_bindings SET node_id=\'{KEEP_ID}\' WHERE node_id IN (SELECT id FROM nodes WHERE name=\'美国01\' AND id != \'{KEEP_ID}\') AND plan_id NOT IN (SELECT plan_id FROM node_plan_bindings WHERE node_id=\'{KEEP_ID}\');"', 10, "迁移套餐绑定到保留节点")

# 3. 删除其他重复节点 (硬删除, 因为它们都是误建的垃圾数据)
run(f'docker exec nexus-postgres psql -U nexus -d nexus_panel -c "DELETE FROM node_plan_bindings WHERE node_id IN (SELECT id FROM nodes WHERE name=\'美国01\' AND id != \'{KEEP_ID}\');"', 10, "清理孤儿绑定")
run(f'docker exec nexus-postgres psql -U nexus -d nexus_panel -c "DELETE FROM user_nodes WHERE node_id IN (SELECT id FROM nodes WHERE name=\'美国01\' AND id != \'{KEEP_ID}\');"', 10, "清理 user_nodes")
run(f'docker exec nexus-postgres psql -U nexus -d nexus_panel -c "DELETE FROM traffic_logs WHERE node_id IN (SELECT id FROM nodes WHERE name=\'美国01\' AND id != \'{KEEP_ID}\');"', 10, "清理 traffic_logs")
run(f'docker exec nexus-postgres psql -U nexus -d nexus_panel -c "DELETE FROM nodes WHERE name=\'美国01\' AND id != \'{KEEP_ID}\';"', 10, "删除重复节点")

# 4. 删除后: 确认只剩 1 个
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id, name, online, is_enabled, server_address, port FROM nodes WHERE name=\'美国01\';"', 10, "删除后: 剩余节点")

# 5. 清掉所有 node: 缓存 (让面板用新数据重建)
run(f'docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning KEYS "node:*" | xargs -I {{}} docker exec nexus-redis redis-cli -a "{REDIS_PWD}" --no-auth-warning DEL "{{}}"', 10, "清空 Redis node:* 缓存")

# 6. 最终节点总数
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT COUNT(*) FROM nodes WHERE is_deleted=false;"', 10, "最终节点总数")

c.close()
print("=== done ===")
