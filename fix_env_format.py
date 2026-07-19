#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
修复面板 .env 第 51 行格式问题:
  当前: # GRPC_TLS_CA disabled for single-direction TLS (agent has no client cert)/app/tls/ca.crt
  目标: 拆成两行, 注释 + 注释掉的原配置(便于将来需要启用 mTLS 时取消注释)
"""
import paramiko, socks, sys, io

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080


def ssh_run(t, cmd, timeout=30):
    chan = t.open_session()
    chan.settimeout(timeout)
    chan.exec_command(cmd)
    out = b""
    err = b""
    while True:
        if chan.recv_ready():
            out += chan.recv(65536)
        if chan.recv_stderr_ready():
            err += chan.recv_stderr(65536)
        if chan.exit_status_ready() and not chan.recv_ready() and not chan.recv_stderr_ready():
            break
    while chan.recv_ready():
        out += chan.recv(65536)
    while chan.recv_stderr_ready():
        err += chan.recv_stderr(65536)
    chan.close()
    return out.decode("utf-8", "replace"), err.decode("utf-8", "replace"), chan.recv_exit_status()


def main():
    print(f"=== 连接面板 {PANEL_HOST} ===")
    sock = socks.socksocket()
    sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    sock.settimeout(15)
    sock.connect((PANEL_HOST, PANEL_PORT))
    t = paramiko.Transport(sock)
    t.connect(username=PANEL_USER, password=PANEL_PASS)

    # 1. 备份当前 .env
    print("\n=== 1. 备份当前 .env ===")
    out, _, _ = ssh_run(t, "cp /root/nexus-panel/.env /root/nexus-panel/.env.bak.$(date +%s) && ls -la /root/nexus-panel/.env*")
    print(out)

    # 2. 用 sed 把错误行替换为两行(注释 + 注释掉的原配置)
    # 错误行模式: 行首是 "# GRPC_TLS_CA disabled..." 但行尾粘连 "/app/tls/ca.crt"
    # 替换策略: 找到匹配的行, 替换为两行:
    #   # GRPC_TLS_CA disabled for single-direction TLS (agent has no client cert)
    #   # GRPC_TLS_CA=/app/tls/ca.crt
    # 用 | 作为 sed 分隔符避免 / 冲突
    print("\n=== 2. 修复 .env 第 51 行格式 ===")
    # 用 python 替换更安全
    fix_cmd = r'''python3 -c "
import re
with open('/root/nexus-panel/.env','r',encoding='utf-8') as f:
    content = f.read()
# 匹配错误行: 以 # GRPC_TLS_CA disabled 开头, 行尾有 /app/tls/ca.crt 残留
pattern = r'^# GRPC_TLS_CA disabled for single-direction TLS \(agent has no client cert\)/app/tls/ca\. crt$'
pattern = r'^# GRPC_TLS_CA disabled for single-direction TLS \(agent has no client cert\)/app/tls/ca\.crt$'
replacement = '# GRPC_TLS_CA disabled for single-direction TLS (agent has no client cert)\n# GRPC_TLS_CA=/app/tls/ca.crt'
new_content, n = re.subn(pattern, replacement, content, flags=re.MULTILINE)
print(f'replaced {n} occurrences')
with open('/root/nexus-panel/.env','w',encoding='utf-8') as f:
    f.write(new_content)
"'''
    out, err, code = ssh_run(t, fix_cmd)
    print(f"stdout: {out}")
    if err.strip():
        print(f"stderr: {err}")
    print(f"exit: {code}")

    # 3. 验证修改后 GRPC_TLS_CA 行
    print("\n=== 3. 验证修改后 .env 中 GRPC_TLS_CA 行 ===")
    out, _, _ = ssh_run(t, "grep -nE 'GRPC_TLS_CA' /root/nexus-panel/.env")
    print(out)

    # 4. 重新 docker compose up panel(让 .env 生效, 虽然 GRPC_TLS_CA 本就是注释不会变)
    #    但为了格式正确性, 还是要让 panel 容器读到最新 .env
    print("\n=== 4. 不重建 panel(本次只是改注释格式, 环境变量没变化, 无需重启) ===")
    print("(跳过 panel 重建, 避免节点短暂掉线)")

    # 5. 验证 panel 仍正常
    print("\n=== 5. 验证 panel 仍正常 ===")
    out, _, _ = ssh_run(t, "docker exec nexus-panel env | grep -E 'GRPC_TLS' 2>&1")
    print(out)
    out, _, _ = ssh_run(t, "curl -s http://127.0.0.1:8080/healthz 2>&1")
    print(out)

    t.close()
    print("\n✓ 完成")


if __name__ == "__main__":
    main()
