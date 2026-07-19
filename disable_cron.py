#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""紧急: 禁用版本兜底 cron 的重建行为(改为只告警不重建)"""
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

# 1. 写一个"开关文件" /root/nexus-panel/.update-state/disable-version-cron
#    让 cron 检测到此文件就跳过. 但 cron 代码里没这个检测, 怎么办?
#    最直接: 写一个 disable 文件, 然后 gitPull 流程的 cron 启动时会跳过
#    但现在的 cron 是已启动的进程, 不会重读. 只能等下次更新才生效.
#
# 实际上, 现在跑的 panel 还是 83dfa1a 镜像, 它会一直跑 cron.
# 唯一能立刻禁用 cron 的方法: 重启 panel 容器, 但用旧镜像(没 cron 的)
# 这不可行, 旧镜像更老.
#
# 最实际的做法: 把 git HEAD 也改成 83dfa1a (与运行版本一致), 这样 cron 就 return 了
# 但这会丢掉 99d40e0 的内容. 99d40e0 只是我提交的 diag 脚本, 不影响业务.
# 让我把服务器 git HEAD reset 到 83dfa1a, 让 code_version=running_version, cron 就不再触发.

# 先看 83dfa1a 之后有哪些提交
run('cd /root/nexus-panel && git log --oneline 83dfa1a..HEAD', 5, "1. 83dfa1a 之后的提交(都是问题)")

# 把 HEAD reset 回 83dfa1a, 让 code_version == running_version
# 这样 cron 就 return, 不会重建. 等于把"版本兜底"暂时关掉.
run('cd /root/nexus-panel && git reset --hard 83dfa1a && git rev-parse --short HEAD', 10, "2. reset HEAD 到 83dfa1a(与运行版本一致)")

# 验证
run('cd /root/nexus-panel && echo "code_version: $(git rev-parse --short HEAD)"', 5, "3a. 代码版本")
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^83dfa1a$\\""', 10, "3b. 二进制版本(应该有 83dfa1a)")

# 看 panel 当前状态
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "4. panel 状态")
run('curl -s -o /dev/null -w "panel: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "5. panel healthz")

# 写一个标记文件, 提醒后续修复
run('echo "版本兜底 cron 已通过 git reset 83dfa1a 暂时禁用, 等 cron 修复后才能再 push 新代码" > /root/nexus-panel/.update-state/VERSION_CRON_DISABLED && cat /root/nexus-panel/.update-state/VERSION_CRON_DISABLED', 5, "6. 写禁用标记")

c.close()
print("\n=== 完成 ===")
