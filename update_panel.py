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

print("1. Git pull...")
out = exec_cmd('cd /root/nexus-panel && git pull origin main 2>&1', 30)
print(out)

print("2. Build...")
out = exec_cmd('cd /root/nexus-panel && docker compose build --no-cache panel 2>&1', 300)
lines = out.split('\n')
for l in lines[-15:]:
    if l.strip():
        print(l)

print("\n3. Restart...")
out = exec_cmd('cd /root/nexus-panel && docker compose up -d panel 2>&1', 30)
print(out)

time.sleep(12)
print("\n4. Status...")
out = exec_cmd('docker ps --format "table {{.Names}}\t{{.Status}}"', 15)
print(out)
out = exec_cmd('curl -s http://127.0.0.1:8080/healthz', 10)
print(f"Health: {out.strip()}")

c.close()
print("DONE")
