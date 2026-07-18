import paramiko, time

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('38.59.246.203', username='root', password='Ev687g8X1o4WmjhPZR', timeout=10)

def run(cmd):
    s, o, e = c.exec_command(cmd, timeout=10)
    return o.read().decode(), e.read().decode()

print("=== Test basic command ===")
o, e = run("echo 'HELLO'")
print(f"stdout: [{o.strip()}]")
print(f"stderr: [{e.strip()}]")

print("\n=== Who am I ===")
o, e = run("whoami && pwd")
print(o.strip())

print("\n=== Shell check ===")
o, e = run("echo $SHELL && echo '---' && ls -la /bin/sh /bin/bash 2>&1")
print(o.strip())

print("\n=== Login files ===")
o, e = run("ls -la /root/.bashrc /root/.profile /root/.bash_profile 2>&1")
print(o.strip())

print("\n=== Test command with leading output ===")
o, e = run("echo 'BEFORE'; echo 'SSH_OK'; echo 'AFTER'")
print(f"stdout: [{o.strip()}]")

print("\n=== PATH ===")
o, e = run("echo PATH=$PATH")
print(o.strip())

print("\n=== sshRun simulation ===")
o, e = run("export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; echo 'SSH_OK'")
print(f"stdout: [{o.strip()}]")
print(f"stderr: [{e.strip()}]")

print("\n=== test sshRun with the actual test ===")
o, e = run("echo 'SSH_OK'")
print(f"stdout: [{o.strip()}]")
print(f"contains SSH_OK: {'SSH_OK' in o}")

c.close()
