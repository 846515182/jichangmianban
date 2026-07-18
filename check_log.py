import paramiko

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('192.129.242.242', username='root', password='eH62M3CcaSep59J8lZ', timeout=15)

si, so, se = c.exec_command('docker logs nexus-panel --tail 80 2>&1', timeout=15)
out = so.read().decode(errors='replace')
err = se.read().decode(errors='replace')
print((out or err).strip())

c.close()
