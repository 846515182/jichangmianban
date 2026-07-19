#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""验证 precheckGRPCTLS 函数逻辑能正确识别当前 TLS 状态"""
import paramiko, socks, sys, io

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

NODE_HOST = "38.59.246.203"
NODE_PORT = 22
NODE_USER = "root"
NODE_PASS = "3Cxeg14SKoI9fp43LZ"

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080


def ssh_run(t, cmd, timeout=20):
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
    return out.decode("utf-8", "replace"), err.decode("utf-8", "replace")


def main():
    print(f"=== 连接节点 {NODE_HOST} ===")
    sock = socks.socksocket()
    sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
    sock.settimeout(15)
    sock.connect((NODE_HOST, NODE_PORT))
    t = paramiko.Transport(sock)
    t.connect(username=NODE_USER, password=NODE_PASS)

    # 模拟 precheckGRPCTLS 逻辑
    panel_addr = "bbcdtv.top:50051"
    host = "bbcdtv.top"
    cmd = f"echo | timeout 8 openssl s_client -connect {panel_addr} -servername {host} -brief 2>&1 | head -30 || true"
    print(f"\n=== 模拟 precheckGRPCTLS: {cmd} ===")
    out, err = ssh_run(t, cmd)
    print(f"stdout:\n{out}")
    if err.strip():
        print(f"stderr:\n{err}")

    # 判断结果
    print("\n=== 判断结果 ===")
    if "certificate required" in out:
        print("❌ 检测到 mTLS 错误: 面板要求客户端证书")
    elif "verify error" in out or "verification failed" in out or "self-signed" in out:
        print("❌ 检测到证书校验失败")
    elif "Connection refused" in out or "No route" in out or "Connection timed out" in out:
        print("❌ 检测到端口不通")
    elif "Verification: OK" in out or "Verify return code: 0" in out:
        print("✓ TLS 握手成功, 预检通过")
    else:
        print("⚠️ 输出未匹配已知模式")

    # 也测试一下用 IP 直连(模拟错误配置)
    print("\n=== 测试 IP 直连(应该 TLS 校验失败, 证书 SAN 无 IP) ===")
    cmd2 = "echo | timeout 8 openssl s_client -connect 192.129.242.242:50051 -servername 192.129.242.242 -brief 2>&1 | head -10 || true"
    out2, _ = ssh_run(t, cmd2)
    print(out2)

    t.close()


if __name__ == "__main__":
    main()
