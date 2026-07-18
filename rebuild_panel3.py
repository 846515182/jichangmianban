import paramiko, time, os

os.environ['PYTHONUNBUFFERED'] = '1'

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=10)

transport = c.get_transport()
transport.set_keepalive(30)

def run(cmd, timeout=15):
    channel = transport.open_session()
    channel.settimeout(float(timeout))
    channel.exec_command(cmd)
    out = channel.makefile('r', -1).read()
    err = channel.makefile_stderr('r', -1).read()
    channel.close()
    return out, err

# Step 1: Check current state
print("Step 1: Current panel state")
o, e = run('docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Image}}" 2>&1')
print(o)

print("\nStep 2: Starting docker compose build in background...")
o, e = run('cd /root/nexus-panel && nohup docker compose build --no-cache panel > /tmp/panel_build.log 2>&1 & echo "BUILD_PID=$!"')
print(o.strip())

# Wait and poll
build_pid = o.strip().split('=')[-1]
print(f"Build running with PID: {build_pid}")

for i in range(30):  # 30 * 10s = 5 min max
    time.sleep(10)
    o, e = run('wc -l /tmp/panel_build.log 2>/dev/null | awk "{print \$1}" && echo "---" && tail -3 /tmp/panel_build.log 2>/dev/null')
    print(f"[{i*10}s] Lines: {o.strip()}")
    
    o2, e2 = run('cat /tmp/panel_build.log 2>/dev/null | grep -c "exporting to image\|Successfully built\|ERROR\|FAILED"')
    if o2.strip().isdigit() and int(o2.strip()) > 0:
        o3, e3 = run('tail -10 /tmp/panel_build.log')
        print("Build appears to be done!")
        print(o3)
        break
    
    # Check if process still running
    o4, e4 = run(f'ps -p {build_pid} > /dev/null 2>&1 && echo "RUNNING" || echo "DONE"')
    if "DONE" in o4:
        print("Build process completed!")
        o5, e5 = run('tail -20 /tmp/panel_build.log')
        print(o5)
        break
else:
    print("Build timeout after 5 minutes")

print("\nStep 3: Restarting panel...")
o, e = run('cd /root/nexus-panel && docker compose up -d panel 2>&1')
print(o)

print("\nStep 4: Waiting for startup...")
time.sleep(12)
o, e = run('docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Image}}" 2>&1')
print(o)

o, e = run('curl -s http://127.0.0.1:8080/healthz 2>&1')
print(f"Health check: {o.strip()}")

c.close()
