#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""登录节点服务器 - 尝试多种认证方式"""
import paramiko, sys, io, socket, socks, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

HOST = '38.59.246.203'
PORT = 22
USER = 'root'
# 用户给的密码(可能复制时有空格或大小写问题, 试几种)
PWD_CANDIDATES = [
    '3Cxeg14SKol9fp43LZ',
    '3Cxeg14SKol9fp43LZ ',
    ' 3Cxeg14SKol9fp43LZ',
    '3Cxeg14SKol9fp43LZ\n',
]
PROXY_HOST = '127.0.0.1'
PROXY_PORT = 18080

def make_sock():
    s = socks.socksocket(socket.AF_INET, socket.SOCK_STREAM)
    s.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    s.settimeout(60)
    s.connect((HOST, PORT))
    return s

# 先用原始 socket 测试一下 SSH banner
print("=== 1. 测试 SSH 端口连通性 ===")
try:
    s = make_sock()
    banner = s.recv(256)
    print(f"SSH banner: {banner.decode('utf-8','replace').strip()}")
    s.close()
except Exception as e:
    print(f"连接失败: {e}")

# 试每个密码候选
for i, pwd in enumerate(PWD_CANDIDATES):
    print(f"\n=== 尝试密码 #{i+1}: {repr(pwd)} ===")
    try:
        c = paramiko.SSHClient()
        c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        c.connect(HOST, port=PORT, username=USER, password=pwd, sock=make_sock(), timeout=30, banner_timeout=30, auth_timeout=30)
        print("✓ 登录成功!")
        _, o, _ = c.exec_command('hostname && uptime', timeout=10)
        print(o.read().decode('utf-8','replace'))
        c.close()
        sys.exit(0)
    except Exception as e:
        print(f"✗ 失败: {e}")

# 也试一下键盘交互式
print("\n=== 尝试键盘交互式认证 ===")
try:
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    transport_sock = make_sock()
    transport = paramiko.Transport(transport_sock)
    transport.connect()
    
    def handler(title, instructions, prompt_list):
        resp = []
        for p in prompt_list:
            resp.append(PWD_CANDIDATES[0])
        return resp
    
    transport.auth_interactive(USER, handler)
    print("✓ 键盘交互式登录成功!")
    transport.close()
except Exception as e:
    print(f"✗ 键盘交互式失败: {e}")

print("\n所有认证方式都失败")
