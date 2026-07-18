import paramiko, time

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=15)

# Use raw SSH exec with pty
t = c.get_transport()
ch = t.open_session()
ch.get_pty()
ch.exec_command('cd /root/nexus-panel && docker compose build --no-cache panel > /tmp/build.log 2>&1')
print("Build started, waiting...")

# Wait up to 5 min, read output as it comes
deadline = time.time() + 300
buf = b""
while time.time() < deadline:
    if ch.recv_ready():
        buf += ch.recv(8192)
    if ch.exit_status_ready():
        break
    time.sleep(0.5)

exit_code = ch.recv_exit_status()
ch.close()

print(f"Build exit code: {exit_code}")

print("=== BUILD LOG (last 20 lines) ===")
ch2 = t.open_session()
ch2.exec_command('tail -20 /tmp/build.log')
out = ch2.makefile('r', -1).read()
ch2.close()
print(out)

if exit_code != 0:
    print("BUILD FAILED, exiting")
    c.close()
    exit(1)

print("=== Restarting panel ===")
ch3 = t.open_session()
ch3.exec_command('cd /root/nexus-panel && docker compose up -d panel 2>&1')
out3 = ch3.makefile('r', -1).read()
ch3.close()
print(out3)

time.sleep(12)

print("=== Status ===")
ch4 = t.open_session()
ch4.exec_command('docker ps --format "table {{.Names}}\t{{.Status}}" 2>&1')
out4 = ch4.makefile('r', -1).read()
ch4.close()
print(out4)

ch5 = t.open_session()
ch5.exec_command('curl -s http://127.0.0.1:8080/healthz; echo')
out5 = ch5.makefile('r', -1).read()
ch5.close()
print(f"Health: {out5.strip()}")

c.close()
print("DONE")
