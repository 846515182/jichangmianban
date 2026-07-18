import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('38.59.246.203', username='root', password='Ev687g8X1o4WmjhPZR', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=10)
    return o.read().decode(), e.read().decode()

print("=== SSHD Config ===")
o, e = run("cat /etc/ssh/sshd_config 2>&1 | grep -v '^#' | grep -v '^$'")
print(o.strip())

print("\n=== SSHD version ===")
o, e = run("sshd -v 2>&1 || sshd -V 2>&1")
print(o.strip()[:500])

print("\n=== MOTD ===")
o, e = run("cat /etc/motd 2>&1")
print(o.strip())

print("\n=== issue.net ===")
o, e = run("cat /etc/issue.net 2>&1")
print(o.strip())

print("\n=== Bashrc check (non-interactive) ===")
o, e = run("bash -c 'echo BEFORE_TEST && echo SSH_OK && echo AFTER_TEST'")
print(f"stdout: [{o.strip()}]")

print("\n=== Dash test ===")
o, e = run("dash -c \"echo 'SSH_OK in dash'\"")
print(f"stdout: [{o.strip()}]")

print("\n=== export cmd test ===")
o, e = run("bash -c \"export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; echo 'SSH_OK'\"")
print(f"stdout: [{o.strip()}]")
print(f"stderr: [{e.strip()}]")

c.close()
