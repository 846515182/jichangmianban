#!/usr/bin/env python3
"""面板装 paramiko 后 SSH 节点排查 agent。"""
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
NODE_PASS = "Q63r8G60PnwyliZE2W"

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


def run(cmd, timeout=180):
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


# 1. 装 paramiko
print("=== 安装 paramiko ===")
run("pip3 install paramiko 2>&1 | tail -5", timeout=180)
run("python3 -c 'import paramiko; print(paramiko.__version__)' 2>&1")

# 2. 写节点 SSH 脚本
node_script = f'''import paramiko, sys
ssh2 = paramiko.SSHClient()
ssh2.set_missing_host_key_policy(paramiko.AutoAddPolicy())
try:
    ssh2.connect("{NODE_HOST}", port=22, username="root", password="{NODE_PASS}", timeout=15, look_for_keys=False, allow_agent=False)
    print("[+] 节点 SSH 登录成功")
except Exception as e:
    print(f"[-] 节点 SSH 登录失败: {{e}}")
    sys.exit(1)

def run2(cmd, t=60):
    print(f"\\n$ {{cmd}}")
    stdin, stdout, stderr = ssh2.exec_command(cmd, timeout=t)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    rc = stdout.channel.recv_exit_status()
    if out: print(out.rstrip())
    if err: print(f"[stderr] {{err.rstrip()}}")
    print(f"[exit={{rc}}]")
    return out, err, rc

print("=== 节点磁盘 ===")
run2("df -h /")
print("\\n=== 节点 docker 容器 ===")
run2("docker ps -a 2>&1")
print("\\n=== 节点进程(nexus/xray/agent) ===")
run2("ps aux | grep -E 'nexus|xray|agent' | grep -v grep")
print("\\n=== 节点监听端口 ===")
run2("ss -tlnp 2>&1 | head -25")
print("\\n=== 节点 agent 日志(docker) ===")
run2("docker logs --tail 40 nexus-node-agent 2>&1 || echo 'no docker agent'")
print("\\n=== 节点 agent 日志(systemd) ===")
run2("journalctl -u nexus-agent --no-pager -n 30 2>&1 || echo 'no systemd agent'")
print("\\n=== 节点 /root 和 /opt ===")
run2("ls -la /root/ 2>&1 | head -20")
run2("ls -la /opt/ 2>&1 | head -20")
print("\\n=== 节点 systemctl 服务 ===")
run2("systemctl list-units --type=service --state=running 2>&1 | grep -iE 'nexus|agent|xray|docker'")
ssh2.close()
print("\\n[+] 节点排查完成")
'''

run(f"cat > /tmp/node_ssh.py << 'PYEOF'\n{node_script}\nPYEOF\necho written")
print("\n=== 执行节点 SSH 排查 ===")
run("python3 /tmp/node_ssh.py 2>&1", timeout=120)

ssh.close()
print("\n[+] 完成")
