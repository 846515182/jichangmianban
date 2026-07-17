#!/usr/bin/env python3
"""恢复节点: 给 agent 容器注入 GRPC_TLS_CA(用系统 CA bundle), 重启 agent。"""
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


# 恢复节点: 把系统 CA bundle 挂载到 agent 容器, 设 GRPC_TLS_CA, 重启
# agent 镜像内 ca-certificates 已装(从 Dockerfile), 用容器内的 /etc/ssl/certs/ca-certificates.crt
node_cmd = """set +e
echo === 1. 检查 agent 镜像内 CA bundle ===
docker exec nexus-agent-48699a6e ls -la /etc/ssl/certs/ca-certificates.crt 2>&1
echo === 2. 停止旧的冲突 agent(f2617f02, unhealthy, 已删除节点) ===
docker stop nexus-agent-f2617f02 2>&1
docker rm nexus-agent-f2617f02 2>&1
echo === 3. 获取 48699a6e 容器完整启动配置 ===
docker inspect nexus-agent-48699a6e --format '{{json .Config.Env}}' 2>&1
echo === 4. 获取镜像名 ===
docker inspect nexus-agent-48699a6e --format '{{.Config.Image}}' 2>&1
echo === 5. 获取挂载 ===
docker inspect nexus-agent-48699a6e --format '{{json .Mounts}}' 2>&1
echo === 6. 获取端口绑定 ===
docker inspect nexus-agent-48699a6e --format '{{json .HostConfig.PortBindings}}' 2>&1
echo === 7. 获取 restart policy ===
docker inspect nexus-agent-48699a6e --format '{{json .HostConfig.RestartPolicy}}' 2>&1
echo === 8. 获取 network mode ===
docker inspect nexus-agent-48699a6e --format '{{.HostConfig.NetworkMode}}' 2>&1
echo === DONE ===
"""
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=90)

ssh.close()
print("\n[+] 完成")
