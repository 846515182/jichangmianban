import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=15)
    return o.read().decode(), e.read().decode()

o, e = run('docker ps --format "{{.Names}} {{.Status}} {{.Image}}" 2>&1 | head -20')
print("=== DOCKER CONTAINERS ===")
print(o.strip())

o, e = run('ls -la /root/nexus-panel/ 2>/dev/null || echo "NO /root/nexus-panel"')
print("=== NEXUS-PANEL DIR ===")
print(o.strip())

o, e = run('cd /root/nexus-panel 2>/dev/null && git log --oneline -5 || echo "NOT A GIT REPO"')
print("=== GIT LOG ===")
print(o.strip())

o, e = run('cd /root/nexus-panel 2>/dev/null && git status --short || echo "CANT_CD"')
print("=== GIT STATUS ===")
print(o.strip())

o, e = run('cd /root/nexus-panel/backend 2>/dev/null && ls -la auto_deploy.go 2>/dev/null || find /root/nexus-panel/backend -name "auto_deploy.go" 2>/dev/null | head -5')
print("=== AUTO_DEPLOY.GO ===")
print(o.strip())

o, e = run('docker inspect nexus-panel 2>/dev/null | grep -A5 "Mounts" | head -20 || echo "NO nexus-panel container"')
print("=== PANEL VOLUME MOUNTS ===")
print(o.strip())

c.close()
