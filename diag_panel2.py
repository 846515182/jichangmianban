import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=15)
    return o.read().decode()

o = run('cat /root/nexus-panel/docker-compose.yml')
print("=== DOCKER COMPOSE ===")
print(o)

o = run('docker inspect nexus-panel --format "{{json .Mounts}}" 2>/dev/null')
print("=== PANEL MOUNTS (FULL) ===")
print(o)

o = run('grep -rn "defaultPath\|export.*defaultPath" /root/nexus-panel/backend/internal/handler/auto_deploy.go 2>/dev/null | head -10')
print("=== PATH FIX IN SOURCE ===")
print(o.strip() or "NOT FOUND")

o = run('md5sum /root/nexus-panel/backend/internal/handler/auto_deploy.go 2>/dev/null')
print("=== SOURCE MD5 ===")
print(o.strip())

c.close()
