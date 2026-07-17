#!/usr/bin/env python3
"""从面板跳板 SSH 到节点服务器, 排查 agent + Xray + 磁盘 lsof。"""
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
print("=== A. 面板磁盘 lsof deleted(被占用的大文件) ===")
print("=" * 60)
run("lsof 2>/dev/null | grep -i deleted | awk '{print $7, $9}' | sort -rn | head -20")
run("df -h / 2>&1")

print("\n=== B. 清理面板 containerd 无用层 + build cache ===")
run("docker builder prune -a -f 2>&1 | tail -3", timeout=120)
run("docker image prune -a -f 2>&1 | tail -3", timeout=60)
# 清理 containerd 未被引用的快照
run("ctr -n moby containers delete -f \$(ctr -n moby containers list -q) 2>/dev/null; echo done")
run("df -h / 2>&1")

print("\n=== C. 从面板 SSH 到节点服务器 ===")
print("尝试 sshpass 方式登录节点...")
# 先看面板有没有 sshpass
run("which sshpass 2>&1 || apt-get install -y sshpass 2>&1 | tail -3")

# 用 sshpass SSH 到节点
node_cmds = """
echo '=== 节点磁盘 ===';
df -h /;
echo '=== 节点 docker 容器 ===';
docker ps -a 2>&1;
echo '=== 节点 agent 进程 ===';
ps aux | grep -E 'nexus|xray|agent' | grep -v grep;
echo '=== 节点监听端口 ===';
ss -tlnp 2>&1 | head -20;
echo '=== 节点 agent 日志 ===';
docker logs --tail 30 nexus-node-agent 2>&1 || journalctl -u nexus-agent --no-pager -n 30 2>&1 || echo 'no agent logs';
echo '=== 节点 Xray 进程 ===';
docker ps | grep -i xray 2>&1;
ps aux | grep xray | grep -v grep 2>&1;
echo '=== 节点完成 ===';
"""
# 用 sshpass 执行
ssh_cmd = f"sshpass -p '{NODE_PASS}' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 root@{NODE_HOST} \"{node_cmds}\" 2>&1"
run(ssh_cmd, timeout=60)

ssh.close()
print("\n[+] 完成")
