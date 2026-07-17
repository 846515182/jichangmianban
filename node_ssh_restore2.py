#!/usr/bin/env python3
"""恢复节点: 重建 agent 容器加 GRPC_TLS_CA, 重启。纯英文标签。"""
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
print("[+] panel SSH ok\n")


def run(cmd, timeout=120):
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


# 重建 agent 容器, 加 GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt (镜像内自带, 信任 Let's Encrypt)
# 用 docker run 重建, 保留原配置
node_cmd = """set -e
echo STEP1 stop old conflicting agent f2617f02
docker stop nexus-agent-f2617f02 2>/dev/null || true
docker rm nexus-agent-f2617f02 2>/dev/null || true
echo STEP2 get current 48699a6e config
IMAGE=$(docker inspect nexus-agent-48699a6e --format '{{.Config.Image}}')
echo IMAGE=$IMAGE
echo STEP3 stop and remove 48699a6e
docker stop nexus-agent-48699a6e
docker rm nexus-agent-48699a6e
echo STEP4 recreate with GRPC_TLS_CA
docker run -d --name nexus-agent-48699a6e \
  --restart unless-stopped \
  -e PANEL_GRPC_ADDR=192.129.242.242:50051 \
  -e NODE_TOKEN=e42a9b34a067f9c82fd99993fc2fd9379a87166d1b4c742031369dee7e427f22 \
  -e LISTEN_PORT=8443 \
  -e HEALTH_PORT=50052 \
  -e XRAY_VERSION=v26.6.1 \
  -e GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt \
  -p 8443:8443 \
  $IMAGE
echo STEP5 wait 10s
sleep 10
echo STEP6 status
docker ps --format '{{.Names}}|{{.Status}}|{{.Image}}'
echo STEP7 agent logs
docker logs --tail 30 nexus-agent-48699a6e 2>&1
echo STEP8 DONE
"""
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=120)

ssh.close()
print("\n[+] done")
