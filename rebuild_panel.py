import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=90)
    return o.read().decode(), e.read().decode()

print("=== 重建 panel 容器 ===")
o, e = run('cd /root/nexus-panel && docker compose build --no-cache panel 2>&1')
print(o)
if e: print("STDERR:", e[:2000])

print("\n=== 重启 panel ===")
o, e = run('cd /root/nexus-panel && docker compose up -d panel 2>&1')
print(o)
if e: print("STDERR:", e[:500])

print("\n=== 等待启动 ===")
import time
time.sleep(5)
o, e = run('docker ps --format "{{.Names}} {{.Status}}" | grep panel')
print(o.strip())

c.close()
