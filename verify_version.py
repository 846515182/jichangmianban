#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""验证 app.Version 实际值"""
import paramiko, sys, io, socket, socks, time, json
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

def run(cmd, timeout=60, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 直接在二进制里 grep c4052bd(本次 ldflags 注入的版本)
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^c4052bd$\\" | head -3"', 30, "1. 在二进制里查找 c4052bd(本次 ldflags 注入的)")

# 2. 在二进制里 grep b1c5fec
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^b1c5fec$\\" | head -3"', 30, "2. 在二进制里查找 b1c5fec(可能是历史遗留)")

# 3. 登录 admin 拿 token, 调 GitStatus 接口读 binary_version 字段
print("\n=== 3. 登录 admin 拿 token ===")
# 先尝试默认密码 admin123 或 ADMIN_INIT_PASSWORD 环境变量
_, o, e = c.exec_command('docker exec nexus-panel env | grep ADMIN_INIT_PASSWORD', timeout=10)
env_out = o.read().decode('utf-8','replace').rstrip()
print("env:", env_out)

# 从 .env 读 ADMIN_INIT_PASSWORD
_, o, e = c.exec_command('grep ADMIN_INIT_PASSWORD /root/nexus-panel/.env 2>/dev/null', timeout=10)
env_out = o.read().decode('utf-8','replace').rstrip()
print(".env:", env_out)

# 尝试登录 - 用 .env 里的密码
import re
m = re.search(r'ADMIN_INIT_PASSWORD=(\S+)', env_out)
pwd = m.group(1) if m else 'admin123'
print(f"尝试用密码登录: {pwd}")

# 调登录接口
login_cmd = f'curl -s -X POST http://127.0.0.1:8080/api/v1/admin/auth/login -H "Content-Type: application/json" -d \'{{"username":"admin","password":"{pwd}"}}\''
_, o, e = c.exec_command(login_cmd, timeout=10)
login_out = o.read().decode('utf-8','replace').rstrip()
print(f"登录响应: {login_out[:300]}")

# 提取 token
try:
    j = json.loads(login_out)
    token = j.get('data', {}).get('token', '')
except:
    token = ''
print(f"token: {token[:30]}...")

if token:
    # 调 GitStatus 接口
    print("\n=== 4. 调 GitStatus 接口读 binary_version ===")
    status_cmd = f'curl -s http://127.0.0.1:8080/api/v1/admin/system/git-status -H "Authorization: Bearer {token}"'
    _, o, e = c.exec_command(status_cmd, timeout=15)
    status_out = o.read().decode('utf-8','replace').rstrip()
    try:
        j = json.loads(status_out)
        data = j.get('data', {})
        print(f"  local_head:      {data.get('local_head')}")
        print(f"  remote_head:     {data.get('remote_head')}")
        print(f"  running_version: {data.get('running_version')} (来自 .last_build_version 文件)")
        print(f"  binary_version:  {data.get('binary_version')} (来自 app.Version, ldflags 注入)")
        print(f"  needs_rebuild:   {data.get('needs_rebuild')}")
        print(f"  up_to_date:      {data.get('up_to_date')}")
    except Exception as ex:
        print(f"解析失败: {ex}")
        print(f"原始响应: {status_out[:500]}")

# 5. 看版本一致性 cron 在过去 5 分钟内是否有日志(应该有, 启动 3 分钟后第一次巡检)
run('docker logs nexus-panel --since 10m 2>&1 | grep -E "版本一致性|巡检|cron:lock:version_check"', 30, "5. 版本一致性 cron 日志(应该有巡检记录或静默一致)")

# 6. 容器当前状态
run('docker ps --filter name=nexus-panel --format "{{.Image}} | {{.CreatedAt}} | {{.Status}}"', 10, "6. 容器当前状态")

c.close()
print("\n=== 验证完成 ===")
