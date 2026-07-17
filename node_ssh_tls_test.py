#!/usr/bin/env python3
"""测面板 gRPC TLS 握手 + agent 是否明文连。"""
import socks
import socket
import paramiko
import base64

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080
PANEL_HOST = "192.129.242.242"
PANEL_PORT = 22
PANEL_USER = "root"
PANEL_PASS = "eH62M3CcaSep59J8lZ"
NODE_HOST = "38.59.246.203"
NODE_PASS = "KplEZ90A72QvkFnz95"

sock = socks.socksocket()
sock.set_proxy(socks.HTTP, PROXY_HOST, PROXY_PORT)
sock.settimeout(30)
sock.connect((PANEL_HOST, PANEL_PORT))
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(
    PANEL_HOST, port=PANEL_PORT, username=PANEL_USER, password=PANEL_PASS,
    sock=sock, timeout=30, look_for_keys=False, allow_agent=False,
)
print("[+] 面板 SSH 登录成功\n")


def run(cmd, timeout=90):
    print(f"\n$ {cmd[:140]}")
    try:
        stdin, stdout, stderr = ssh.exec_command(cmd, timeout=timeout, get_pty=False)
        out = stdout.read().decode("utf-8", errors="replace")
        err = stderr.read().decode("utf-8", errors="replace")
        rc = stdout.channel.recv_exit_status()
        if out:
            print(out.rstrip())
        if err:
            print(f"[stderr] {err.rstrip()}")
        print(f"[exit={rc}]")
        return out, err, rc
    except Exception as e:
        print(f"[ERROR] {e}")
        return "", str(e), -1


# 1. 从面板本机测 gRPC TLS
print("=== A. 面板本机测 50051 TLS ===")
run("echo | timeout 5 openssl s_client -connect 127.0.0.1:50051 -servername nexus 2>&1 | head -15")
run("echo | timeout 5 openssl s_client -connect 127.0.0.1:50051 2>&1 | grep -iE 'subject|issuer|CONNECTED|verify' | head -10")

# 2. 面板 gRPC server 配置
print("\n=== B. 面板 gRPC server.go 配置 ===")
run("docker exec nexus-panel cat /app/tls/server.crt 2>&1 | openssl x509 -noout -subject -issuer -dates 2>&1 | head -5")
run("cd /root/nexus-panel && grep -n 'TLS\\|tls\\|InsecureSkipVerify\\|Credentials' backend/internal/grpc/server.go 2>&1 | head -20")

# 3. 节点上测面板 TLS 握手
print("\n=== C. 从节点测面板 50051 ===")
node_cmd = """set +e
echo === node openssl s_client to panel 50051 ===
echo | timeout 5 openssl s_client -connect 192.129.242.242:50051 2>&1 | head -15
echo === node GRPC_TLS_CA env in agent ===
docker inspect nexus-agent-48699a6e --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -i TLS
echo === node plain TCP test to panel 50051 ===
timeout 5 bash -c 'echo > /dev/tcp/192.129.242.242/50051' 2>&1 && echo TCP-OK || echo TCP-FAIL
echo === agent 48699a6e full env ===
docker inspect nexus-agent-48699a6e --format '{{range .Config.Env}}{{println .}}{{end}}'
echo === DONE ===
"""
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=60)

ssh.close()
print("\n[+] 完成")
