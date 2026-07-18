import paramiko, time, sys, io
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=15)
t = c.get_transport()

def exec_cmd(cmd, timeout=120):
    ch = t.open_session()
    ch.get_pty()
    ch.exec_command(cmd)
    deadline = time.time() + timeout
    buf = b""
    while time.time() < deadline:
        if ch.recv_ready():
            buf += ch.recv(8192)
        if ch.exit_status_ready():
            break
        time.sleep(0.5)
    ch.close()
    return buf.decode('utf-8', errors='replace')

print("=== Git pull ===")
o = exec_cmd('cd /root/nexus-panel && git pull origin main 2>&1', 30)
print(o.strip())

print("\n=== Build ===")
o = exec_cmd('cd /root/nexus-panel && docker compose build --no-cache panel 2>&1', 300)
for l in o.split('\n')[-10:]:
    if l.strip(): print(l.strip())

print("\n=== Restart ===")
o = exec_cmd('cd /root/nexus-panel && docker compose up -d panel 2>&1', 30)
print(o.strip())

time.sleep(10)
o = exec_cmd('docker ps --format "table {{.Names}}\t{{.Status}}"', 10)
print(o.strip())

o = exec_cmd('curl -s http://127.0.0.1:8080/healthz', 8)
print(f"Health: {o.strip()}")

c.close()
