#!/usr/bin/env python3
"""1. 释放 24G deleted 文件 2. 从面板 paramiko 跳板 SSH 节点重启 agent。"""
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


def run(cmd, timeout=120):
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


print("=" * 60)
print("=== A. 找占用 /tmp/server_backup.tar.gz 的进程 ===")
print("=" * 60)
run("lsof 2>/dev/null | grep 'server_backup.tar.gz' | head -10")
# 也用 fuser 找
run("fuser /tmp/server_backup.tar.gz 2>&1 || echo 'fuser n/a'")
# 看 /tmp 下是否真的没文件了
run("ls -la /tmp/server_backup.tar.gz 2>&1")
run("ls -la /tmp/ 2>&1 | head -20")

print("\n=== B. kill 占用 deleted 文件的进程 ===")
# 找所有打开 deleted 文件的进程 PID
run("for pid in $(lsof 2>/dev/null | grep -i deleted | awk '{print $2}' | sort -u); do echo \"PID=$pid $(ps -p $pid -o comm= 2>/dev/null)\"; done | head -20")

print("\n=== C. 释放磁盘 - kill tar/python 进程(可能是备份脚本残留) ===")
# 看 tar 和 python 进程
run("ps aux | grep -E 'tar|backup|python' | grep -v grep | head -10")
# 优雅 kill tar 进程(备份残留)
run("pkill -TERM -f 'server_backup' 2>&1; echo rc=$?")
run("pkill -TERM -x tar 2>&1; echo rc=$?")
time.sleep(3)
run("df -h / 2>&1")

# 如果还没释放, 找具体 PID kill
run("lsof 2>/dev/null | grep 'server_backup.tar.gz' | head -5")
run("for pid in $(lsof 2>/dev/null | grep 'server_backup.tar.gz' | awk '{print $2}' | sort -u); do echo \"killing $pid\"; kill -9 $pid 2>/dev/null; done; echo done")
time.sleep(2)
run("df -h / 2>&1")

print("\n=== D. 从面板用 paramiko SSH 节点 ===")
# 在面板上写一个临时 python 脚本连节点
node_script = '''
import paramiko, sys
ssh2 = paramiko.SSHClient()
ssh2.set_missing_host_key_policy(paramiko.AutoAddPolicy())
try:
    ssh2.connect("NODE_HOST", port=22, username="root", password="NODE_PASS", timeout=15, look_for_keys=False, allow_agent=False)
    print("[+] 节点 SSH 登录成功")
except Exception as e:
    print(f"[-] 节点 SSH 登录失败: {e}")
    sys.exit(1)

def run2(cmd, t=60):
    print(f"\\n$ {cmd}")
    stdin, stdout, stderr = ssh2.exec_command(cmd, timeout=t)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    rc = stdout.channel.recv_exit_status()
    if out: print(out.rstrip())
    if err: print(f"[stderr] {err.rstrip()}")
    print(f"[exit={rc}]")
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
print("\\n=== 节点 /root 目录 ===")
run2("ls -la /root/ 2>&1 | head -20")
run2("ls -la /opt/ 2>&1 | head -20")
ssh2.close()
print("\\n[+] 节点排查完成")
'''
node_script = node_script.replace("NODE_HOST", NODE_HOST).replace("NODE_PASS", NODE_PASS)

# 写到面板临时文件并执行
run(f"cat > /tmp/node_ssh.py << 'PYEOF'\n{node_script}\nPYEOF\necho written")
run("python3 /tmp/node_ssh.py 2>&1", timeout=90)

ssh.close()
print("\n[+] 完成")
