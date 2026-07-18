import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=10)
    return o.read().decode(), e.read().decode()

print("=== Panel Dockerfile ===")
o, e = run("cat /root/nexus-panel/backend/Dockerfile")
print(o)

print("\n=== Go mod (ssh lib) ===")
o, e = run("grep -i 'crypto/ssh\|crypto\s' /root/nexus-panel/backend/go.mod")
print(o.strip())

print("\n=== Go mod full ===")
o, e = run("grep -E 'golang.org/x/crypto|ssh' /root/nexus-panel/backend/go.mod")
print(o.strip())

print("\n=== Test SSH from within container ===")
o, e = run("docker exec nexus-panel sh -c 'apk list --installed 2>/dev/null | grep -i libcrypto'")
print(o.strip())

print("\n=== Panel log (last 20 lines) ===")
o, e = run("docker logs nexus-panel --tail 20 2>&1")
print(o.strip())

c.close()
