import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('38.59.246.203', username='root', password='Ev687g8X1o4WmjhPZR', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=15)
    return o.read().decode(), e.read().decode()

o, e = run('cat /etc/os-release | head -3')
print("=== OS ===")
print(o)

o, e = run('uname -r')
print("=== KERNEL ===")
print(o.strip())

o, e = run('ls -la /usr/local/bin/docker* /usr/bin/docker* 2>&1')
print("=== DOCKER BINARIES ===")
print(o.strip())

o, e = run('ls -la /var/log/dockerd.log 2>&1')
print("=== DOCKER LOG FILE ===")
print(o.strip())

o, e = run('ps aux 2>/dev/null | grep dockerd | grep -v grep')
print("=== DOCKERD PROCESS ===")
print(o.strip() or "NOT RUNNING")

o, e = run('tail -80 /var/log/dockerd.log 2>/dev/null || echo "NO_LOG_FILE"')
print("=== DOCKERD LOGS ===")
print(o.strip())

o, e = run('PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; command -v docker && docker --version && dockerd --version')
print("=== DOCKER VERSION (with PATH) ===")
print(o.strip())

o, e = run('ls -la /tmp/docker* 2>&1')
print("=== TEMP FILES ===")
print(o.strip())

o, e = run('dmesg | tail -30 2>/dev/null | head -20')
print("=== DMESG TAIL ===")
print(o.strip())

c.close()
