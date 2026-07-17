#!/usr/bin/env python3
"""验证节点心跳是否恢复 + 面板显示在线。"""
import socks
import socket
import paramiko
import base64
import time

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
print("[+] panel SSH ok\n")


def run(cmd, timeout=60):
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


# 等 35s 让心跳发一次
print("[waiting 35s for first heartbeat...]")
time.sleep(35)

# 1. 节点 agent 日志
node_cmd = """set +e
echo === AGENT LOGS last 20 ===
docker logs --tail 20 nexus-agent-48699a6e 2>&1
echo === DONE ===
"""
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=60)

# 2. 面板 DB 节点在线状态
print("\n=== PANEL DB node online status ===")
run("docker exec nexus-postgres psql -U nexus -d nexus_panel -c \"SELECT name, online, last_seen_at, EXTRACT(EPOCH FROM (NOW() - last_seen_at))::int AS secs_since_seen FROM nodes WHERE is_deleted=false;\" 2>&1")

# 3. 面板 gRPC 心跳日志
print("\n=== PANEL grpc heartbeat logs ===")
run("docker logs nexus-panel --since 2m 2>&1 | grep -iE 'heartbeat|心跳|register|node' | tail -10")

ssh.close()
print("\n[+] done")
