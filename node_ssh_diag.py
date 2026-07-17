#!/usr/bin/env python3
"""从面板跳板 SSH 节点, 排查 agent 为什么挂 + 部署方式。"""
import socks
import socket
import paramiko
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
print("[+] 面板 SSH 登录成功, 准备跳板节点\n")


def run(cmd, timeout=60):
    print(f"\n$ {cmd}")
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


# SSH 节点的诊断命令 - 用 heredoc 传给远程 bash, 避免引号嵌套问题
node_cmd = """set +e
echo === 1. NODE DISK ===
df -h /
echo === 2. DOCKER PS -A ===
docker ps -a
echo === 3. AGENT PROCS ===
ps aux | grep -iE 'nexus|agent' | grep -v grep
echo === 4. XRAY PROCS ===
ps aux | grep -i xray | grep -v grep
echo === 5. LISTEN PORTS ===
ss -tlnp | head -30
echo === 6. SYSTEMD SERVICES ===
systemctl list-units --type=service --all | grep -iE 'nexus|agent|xray'
echo === 7. STATUS nexus-agent ===
systemctl status nexus-agent 2>&1 | head -25
echo === 8. JOURNAL nexus-agent ===
journalctl -u nexus-agent --no-pager -n 50 2>&1
echo === 9. ROOT DIR ===
ls -la /root/ | head -25
echo === 10. OPT DIR ===
ls -la /opt/ | head -25
echo === 11. FIND nexus files ===
find / -maxdepth 4 -name '*nexus*' 2>/dev/null | grep -v proc | head -30
echo === 12. DOCKER LOGS nexus-node-agent ===
docker logs --tail 60 nexus-node-agent 2>&1 || echo no-docker-agent-container
echo === 13. CRONTAB ===
crontab -l 2>&1
echo === 14. MEM ===
free -h
echo === 15. UPTIME ===
uptime
echo === 16. DMESG OOM ===
dmesg -T 2>/dev/null | grep -iE 'oom|kill|nexus|xray' | tail -15
echo === DONE ===
"""
# 用 base64 传输避免引号问题
import base64
b64 = base64.b64encode(node_cmd.encode()).decode()
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} 'echo {b64} | base64 -d | bash' 2>&1"
run(ssh_cmd, timeout=90)

ssh.close()
print("\n[+] 完成")
