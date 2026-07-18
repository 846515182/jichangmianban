#!/usr/bin/env python3
"""让服务器 git 也同步 reset 到干净的 4b61333, 删除所有调试脚本提交"""
import paramiko, sys, io, socket, socks
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '192.129.242.242'
PORT = 22
USER = 'root'
PWD  = 'eH62M3CcaSep59J8lZ'
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

def run(cmd, timeout=30, label=None):
    if label: print(f"=== {label} ===")
    _, o, e = c.exec_command(cmd, timeout=timeout)
    out = o.read().decode('utf-8','replace')
    err = e.read().decode('utf-8','replace')
    print(out.rstrip())
    if err.strip() and err != out:
        print("[ERR]", err.rstrip()[:300])
    print()

# 1. 服务器当前 HEAD
run("cd /root/nexus-panel && git log --oneline -3", 5, "1. 服务器当前 HEAD")

# 2. fetch origin
run("cd /root/nexus-panel && git fetch origin main 2>&1 | tail -3", 30, "2. fetch")

# 3. reset --hard origin/main (强同步)
run("cd /root/nexus-panel && git reset --hard origin/main 2>&1 | tail -3", 10, "3. reset --hard origin/main")

# 4. 验证
run("cd /root/nexus-panel && git log --oneline -5", 5, "4. reset 后的 HEAD")

# 5. 看仓库根目录还有没有 .py 调试脚本 (本来就不该有)
run("cd /root/nexus-panel && git ls-files | grep -E '\\.py$' 2>&1 || echo '(无 .py 文件, 干净)'", 5, "5. 仓库内 .py 文件")

# 6. 工作区状态
run("cd /root/nexus-panel && git status -s", 5, "6. git status")

# 7. 容器状态 (不应该重启, 只动 git)
run("docker ps --format 'table {{.Names}}\\t{{.Status}}'", 5, "7. 容器状态 (未受影响)")

c.close()
print("=== done ===")
