#!/usr/bin/env python3
"""看两个 agent 容器日志, 找心跳失败原因。"""
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
    print(f"\n$ {cmd[:120]}...")
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


node_cmd = """set +e
echo === 1. AGENT 48699a6e LOGS last 60 ===
docker logs --tail 60 nexus-agent-48699a6e 2>&1
echo === 2. AGENT f2617f02 LOGS last 30 ===
docker logs --tail 30 nexus-agent-f2617f02 2>&1
echo === 3. AGENT 48699a6e INSPECT env ===
docker inspect nexus-agent-48699a6e --format '{{range .Config.Env}}{{println .}}{{end}}' 2>&1 | grep -iE 'PANEL|NODE_TOKEN|LISTEN|HEALTH'
echo === 4. AGENT 48699a6e restart policy ===
docker inspect nexus-agent-48699a6e --format '{{.HostConfig.RestartPolicy}}' 2>&1
echo === 5. AGENT 48699a6e startedAt ===
docker inspect nexus-agent-48699a6e --format '{{.State.StartedAt}} {{.State.Status}} {{.State.Health.Status}}' 2>&1
echo === 6. AGENT 48699a6e healthcheck log ===
docker inspect nexus-agent-48699a6e --format '{{range .State.Health.Log}}{{.ExitCode}} {{.Output}}{{println}}{{end}}' 2>&1 | tail -10
echo === 7. AGENT 48699a6e 资源 ===
docker stats --no-stream --format '{{.Name}} CPU={{.CPUPerc}} MEM={{.MemUsage}}' nexus-agent-48699a6e nexus-agent-f2617f02 2>&1
echo === 8. 测面板 gRPC 连通(从节点) ===
timeout 5 bash -c 'echo > /dev/tcp/192.129.242.242/50051' 2>&1 && echo 'panel:50051 OK' || echo 'panel:50051 FAIL'
echo === 9. agent 配置文件内容(48699a6e) ===
docker exec nexus-agent-48699a6e cat /app/config.json 2>&1 | head -30
echo === DONE ===
"""
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=90)

ssh.close()
print("\n[+] 完成")
