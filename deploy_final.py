#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""部署彻底修复版到服务器"""
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

# 1. 拉新代码
run('cd /root/nexus-panel && git fetch origin main && git reset --hard origin/main && git rev-parse --short HEAD', 30, "1. git pull")

# 2. 验证 docker-compose.yml 的 restart policy 已改
run('grep "restart:" /root/nexus-panel/docker-compose.yml', 5, "2. 验证 restart policy")

# 3. 验证 admin_system.go 里有 helper 容器代码
run('grep -c "nexus-panel-restarter" /root/nexus-panel/backend/internal/handler/admin_system.go', 5, "3. 验证 helper 容器代码")

# 4. 检查 alpine:latest 镜像是否存在(helper 容器需要, 没有就拉)
run('docker image inspect alpine:latest --format "{{.RepoTags}}" 2>&1 || docker pull alpine:latest 2>&1 | tail -3', 30, "4. alpine 镜像检查")

# 5. build panel 镜像(注入新版本号)
print("\n=== 5. build panel (耗时 3-5 分钟) ===")
run('cd /root/nexus-panel && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel 2>&1 | tail -10', 600, "5. build panel")

# 6. 用 helper 容器方式部署(模拟 gitPull 流程的最后一步)
# 先 docker rm -f 旧 helper(如果有)
run('docker rm -f nexus-panel-restarter 2>&1; echo "cleaned"', 10, "6a. 清理旧 helper")

# 启动 helper 容器
helper_cmd = (
    'docker run -d --name nexus-panel-restarter '
    '-v /var/run/docker.sock:/var/run/docker.sock '
    '-v /root/nexus-panel:/repo '
    'alpine:latest '
    'sh -c "apk add --no-cache docker-cli docker-cli-compose >/dev/null 2>&1 && '
    'sleep 3 && cd /repo && '
    'docker compose up -d --no-deps panel && '
    'docker rm -f nexus-panel-restarter"'
)
run(helper_cmd, 60, "6b. 启动 helper 容器")

# 等 helper 完成 + panel 启动
print("\n=== 等 30 秒让 helper 跑完 + panel 启动 ===")
time.sleep(30)

# 7. 验证
run('docker ps --filter name=nexus --format "{{.Names}} | {{.Status}}"', 5, "7a. 所有容器状态")
run('docker ps -a --filter name=restarter --format "{{.Names}} | {{.Status}}"', 5, "7b. helper 容器(应该已自动 rm)")
run('curl -s -o /dev/null -w "panel healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "7c. panel healthz")
run('curl -s -o /dev/null -w "frontend: HTTP %{http_code}\\n" -k https://127.0.0.1/', 5, "7d. frontend")

# 8. 验证版本号已注入
run('docker exec nexus-panel sh -c "strings /app/nexus-panel 2>/dev/null | grep -E \\"^65a3c77$\\" || echo NOT_FOUND"', 10, "7e. 二进制版本验证")

# 9. 验证 restart policy 实际生效(看容器配置)
run('docker inspect nexus-panel --format "{{.HostConfig.RestartPolicy.Name}}"', 5, "7f. panel restart policy")
run('docker inspect nexus-frontend --format "{{.HostConfig.RestartPolicy.Name}}"', 5, "7g. frontend restart policy")

# 10. 测试自动重启: 模拟 panel 异常退出, 看 Docker 是否拉起来
print("\n=== 10. 测试自动重启机制 ===")
run('docker kill nexus-panel 2>&1; echo "killed, 等 15 秒看是否自动重启"', 5, "10a. kill panel")
time.sleep(15)
run('docker ps --filter name=nexus-panel --format "{{.Names}} | {{.Status}}"', 5, "10b. 等 15 秒后状态")
run('curl -s -o /dev/null -w "panel healthz: HTTP %{http_code}\\n" http://127.0.0.1:8080/healthz', 5, "10c. healthz")

c.close()
print("\n=== 部署完成 ===")
