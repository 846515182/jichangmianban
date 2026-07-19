#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""排查 user 登录失败的真正原因"""
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

def run(cmd, timeout=30, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:800])

# 1. 看 Redis 里登录失败计数 / 锁
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning KEYS "loginfail:*" 2>&1', 10, "1a. Redis 登录失败计数 keys")
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning KEYS "loginlock:*" 2>&1', 10, "1b. Redis 登录锁 keys")

# 2. 清掉所有登录锁/失败计数, 确保测试不受锁影响
run('docker exec nexus-redis redis-cli -a "$(grep REDIS_PASSWORD /root/nexus-panel/.env | cut -d= -f2)" --no-auth-warning FLUSHDB 2>&1', 10, "2. 清空 Redis 登录锁/失败计数")

# 3. 直接 curl 测试 user 登录(用 ceshi1 + 常见测试密码)
# 但我们不知道密码, 先让用户重试. 先看后端登录失败时返回什么
# 模拟一次 admin 登录看返回体
run('curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login -H "Content-Type: application/json" -d \'{"username":"ceshi1","password":"test123","target":"user"}\'', 10, "3. 模拟 user 登录(密码可能不对, 看返回体)")

# 4. 看前端是否真的发了 user target. 看最近 30s 内所有 /auth/login 请求
run('docker logs nexus-panel --since 2m 2>&1 | grep "/auth/login" | tail -10', 10, "4. 最近 2 分钟登录请求")

# 5. 看用户密码 hash 长度(确认 bcrypt hash 完整)
run('docker exec nexus-postgres psql -U nexus -d nexus_panel -c "SELECT username, LENGTH(password_hash) AS hash_len, LEFT(password_hash, 7) AS prefix FROM users WHERE username=\'ceshi1\'"', 10, "5. ceshi1 密码 hash 状态")

# 6. 看登录路由的 RateLimit 配置(ScopeAdmin/ScopeUser 区别)
run('docker logs nexus-panel --since 2m 2>&1 | grep -E "rate|limit|locked|429" | tail -10', 10, "6. 限流/锁定日志")

c.close()
print("\n=== 完成 ===")
