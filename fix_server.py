#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""修复服务器: 重建 panel 容器用新镜像 + 手动执行 is_trial 迁移"""
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

def run(cmd, timeout=600, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 用 heredoc 把 SQL 写到临时文件, 避免 shell 转义噩梦
SQL_TRIAL = """
ALTER TABLE plans ADD COLUMN IF NOT EXISTS is_trial BOOLEAN DEFAULT false;
UPDATE plans SET is_trial = true WHERE name LIKE '%试用%' AND is_deleted = false;
UPDATE plans SET is_trial = true WHERE price_cents = 0 AND duration_days = 30 AND device_limit = 2 AND is_deleted = false;
INSERT INTO schema_migrations (version) VALUES ('2026_07_19_add_plan_is_trial') ON CONFLICT DO NOTHING;
SELECT name, is_trial, price_cents FROM plans WHERE is_deleted=false ORDER BY sort_order;
"""

# 0. 修复前状态
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "0. 修复前 panel 容器状态")

# 1. 执行 is_trial 迁移(用 stdin 传 SQL, 避免转义)
print("\n=== 1. 执行 is_trial 迁移 ===")
stdin, stdout, stderr = c.exec_command('docker exec -i nexus-postgres psql -U nexus -d nexus_panel', timeout=30)
stdin.write(SQL_TRIAL)
stdin.channel.shutdown_write()
print(stdout.read().decode('utf-8','replace').rstrip())
err = stderr.read().decode('utf-8','replace').rstrip()
if err: print("[ERR]", err[:500])

# 2. 构建新 panel 镜像(用最新代码 372999c)
print("\n=== 2. 构建新 panel 镜像 (5-10 分钟, 请耐心等待) ===")
run('cd /root/nexus-panel && docker compose build panel 2>&1 | tail -15', 900, None)

# 3. 重建 panel 容器用新镜像
run('cd /root/nexus-panel && docker compose up -d --no-deps panel 2>&1', 60, "3. 重建 panel 容器")

# 4. 等 15 秒让容器启动
print("\n=== 等待 15 秒让容器启动 ===")
time.sleep(15)

# 5. 验证容器状态
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "5a. 修复后 panel 容器状态")
run('docker exec nexus-panel ls -la /app/nexus-panel 2>&1', 10, "5b. 容器内二进制文件时间")

# 6. 看新容器启动日志
run('docker logs nexus-panel --tail 30 2>&1', 30, "6. 新容器启动日志")

# 7. 测试 containers 接口
run('curl -s -o /dev/null -w "HTTP %{http_code}\\n" http://127.0.0.1:8080/api/v1/admin/system/containers 2>&1', 10, "7. containers 接口 HTTP 状态码 (401=正常需登录, 404=后端没这接口)")

# 8. 再次确认 is_trial 字段
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT name,is_trial,price_cents FROM plans WHERE is_deleted=false ORDER BY sort_order;"', 10, "8. plans 表最终状态")

# 9. 看下当前运行二进制版本
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^[a-f0-9]{7}$\\" | sort -u | head -5"', 30, "9. 运行二进制版本")

c.close()
print("\n=== 修复完成 ===")
