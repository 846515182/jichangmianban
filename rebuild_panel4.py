import paramiko, time

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=15)

def run(cmd, timeout=60):
    s, o, e = c.exec_command(cmd, timeout=timeout)
    return o.read().decode(), e.read().decode()

print("1. Start build in background...")
o, e = run('cd /root/nexus-panel && nohup bash -c "docker compose build --no-cache panel" > /tmp/build.log 2>&1 &', timeout=30)
print("Build started")

print("\n2. Polling build progress...")
for i in range(60):
    time.sleep(5)
    o, e = run('tail -5 /tmp/build.log 2>/dev/null; echo "---SIZE"; wc -l /tmp/build.log 2>/dev/null')
    lines = o.strip()
    
    if lines:
        print(f"[{i*5}s] {lines.split(chr(10))[-1]}")
    
    o2, e2 = run('grep -c "exporting to image\|Successfully built\|ERROR\|FAILED" /tmp/build.log 2>/dev/null || echo 0')
    if o2.strip().isdigit() and int(o2.strip()) > 0:
        print("\nBUILD COMPLETED. Final output:")
        o3, e3 = run('tail -20 /tmp/build.log')
        print(o3)
        break
else:
    print("\nTIMEOUT. Last log:")
    o4, e4 = run('tail -30 /tmp/build.log')
    print(o4)

print("\n3. Restart panel...")
o, e = run('cd /root/nexus-panel && docker compose up -d panel 2>&1', timeout=30)
print(o.strip())

print("\n4. Wait and verify...")
time.sleep(10)
o, e = run('docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Image}}"')
print(o)

o, e = run('curl -s http://127.0.0.1:8080/healthz; echo')
print(f"Health: {o.strip()}")

c.close()
print("\nDONE")
