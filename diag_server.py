#!/usr/bin/env python3
"""诊断服务器实际状态, 找到"资源不存在"和"订单UUID报错"的真正根因"""
import paramiko, sys, io, socket, socks
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
    s.settimeout(30)
    s.connect((HOST, PORT))
    return s

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, port=PORT, username=USER, password=PWD, sock=make_sock(), timeout=30, banner_timeout=30)
print("=== SSH 连接成功 ===\n")

def run(cmd, timeout=60, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:500])

# 1. 容器状态
run('docker ps --format "table {{.Names}}\\t{{.Status}}\\t{{.Image}}\\t{{.CreatedAt}}"', 10, "1. 容器状态")

# 2. 服务器仓库代码版本
run('cd /root/nexus-panel && git log --oneline -8 2>&1', 10, "2. 服务器仓库代码版本")

# 3. 当前未提交改动
run('cd /root/nexus-panel && git status --short 2>&1 | head -30', 10, "3. 服务器仓库未提交改动")

# 4. 运行二进制版本(从 strings 找 git HEAD short hash)
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^[a-f0-9]{7}$\\" | sort -u | head -5"', 30, "4. 运行二进制版本")

# 5. 启动日志最后 50 行
run('docker logs nexus-panel --tail 50 2>&1', 30, "5. 启动日志最后 50 行")

# 6. 找 404 / 资源不存在 / containers 相关请求
run('docker logs nexus-panel 2>&1 | grep -iE "404|资源不存在|/containers|not found" | tail -30', 30, "6. 404 / 资源不存在 请求")

# 7. plans 表实际数据
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id,name,is_trial,price_cents,is_enabled FROM plans WHERE is_deleted=false ORDER BY sort_order;"', 30, "7. plans 表实际数据")

# 8. schema_migrations 已执行的迁移
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT version FROM schema_migrations ORDER BY version;"', 30, "8. 已执行的迁移列表")

# 9. systemd unit 文件是否存在
run('systemctl status nexus-panel 2>&1 | head -10; echo "---"; ls -la /etc/systemd/system/nexus-panel.service 2>&1; ls -la /etc/systemd/system/multi-user.target.wants/nexus-panel.service 2>&1', 10, "9. systemd unit 是否存在")

# 10. 当前 panel 容器里的二进制修改时间
run('docker exec nexus-panel ls -la /app/nexus-panel /app/nexus-panel.new 2>&1', 10, "10. 容器内二进制文件时间")

# 11. 服务器磁盘上的旧二进制残留
run('ls -la /root/nexus-panel/nexus-panel* /root/nexus-panel/.last_build_version 2>&1 | head -20', 10, "11. 服务器磁盘上的二进制残留")

# 12. nexus-panel:latest 镜像创建时间
run('docker image inspect nexus-panel:latest --format "Created: {{.Created}}\\nID: {{.Id}}\\nSize: {{.Size}}" 2>&1', 10, "12. nexus-panel:latest 镜像创建时间")

# 13. orders 表最近 5 条订单(看 coupon_id 字段类型)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "\\d orders" 2>&1 | head -30', 30, "13. orders 表结构")

# 14. 最近 5 条订单
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT id,order_no,plan_id,coupon_id,amount_cents,status,created_at FROM orders ORDER BY created_at DESC LIMIT 5;"', 30, "14. 最近 5 条订单")

# 15. 看 frontend 容器版本
run('docker exec nexus-frontend ls -la /usr/share/nginx/html/ 2>&1 | head -10', 10, "15. frontend 容器静态文件")

c.close()
print("\n=== 诊断完成 ===")
