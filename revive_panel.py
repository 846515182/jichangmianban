#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急: 拉起 panel 容器 + 看更新日志找根因"""
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

def run(cmd, timeout=60, label=None):
    if label: print(f"\n=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:1500])

# 1. 看 panel 容器状态(Exited 的原因)
run('docker ps -a --filter name=nexus-panel --format "{{.Names}} | {{.Status}} | {{.RunningFor}}"', 10, "1. panel 容器状态")
run('docker inspect nexus-panel --format "{{.State.Status}} | ExitCode={{.State.ExitCode}} | Error={{.State.Error}} | StartedAt={{.State.StartedAt}} | FinishedAt={{.State.FinishedAt}}"', 10, "1b. 容器详情")

# 2. 看更新日志最后 60 行, 找出"成功"之后发生了什么
run('tail -60 /root/nexus-panel/.update-state/git-pull.log', 10, "2. 更新日志最后 60 行")

# 3. 立即启动 panel 容器
run('docker start nexus-panel 2>&1', 30, "3. 启动 panel 容器")
print("\n=== 等 10 秒 ===")
time.sleep(10)
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 10, "3b. 启动后状态")

# 4. 看 panel 启动日志
run('docker logs nexus-panel --tail 30 2>&1', 30, "4. panel 启动日志")

# 5. curl 测试
run('curl -s -o /dev/null -w "panel: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 10, "5. panel healthz")

# 6. 看上次更新过程中 panel 是否又被版本兜底 cron 杀掉
run('docker logs nexus-panel --since 30m 2>&1 | grep -E "版本一致性兜底|收到退出信号|Nexus-Panel 已退出|panic|FATAL|ERROR" | tail -30', 30, "6. 关键事件日志(版本兜底/退出/错误)")

c.close()
print("\n=== 完成 ===")
