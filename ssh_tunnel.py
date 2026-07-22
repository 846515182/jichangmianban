#!/usr/bin/env python3
# SSH over HTTP-CONNECT proxy tunnel helper
import sys, socket, paramiko

PROXY_HOST = "127.0.0.1"
PROXY_PORT = 18080


def make_proxy_sock(target_host, target_port, timeout=20):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    sock.connect((PROXY_HOST, PROXY_PORT))
    req = "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n" % (target_host, target_port, target_host, target_port)
    sock.sendall(req.encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = sock.recv(4096)
        if not chunk:
            break
        buf += chunk
    first_line = buf.split(b"\r\n", 1)[0]
    if b" 200 " not in first_line:
        raise RuntimeError("Proxy CONNECT failed: " + first_line.decode(errors="replace"))
    return sock


def run(host, password, cmd, port=22, timeout=30):
    sock = make_proxy_sock(host, port)
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(host, port=port, username="root", password=password,
                   sock=sock, timeout=timeout, allow_agent=False, look_for_keys=False)
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout, get_pty=False)
    out = stdout.read().decode(errors="replace")
    err = stderr.read().decode(errors="replace")
    rc = stdout.channel.recv_exit_status()
    client.close()
    return rc, out, err


if __name__ == "__main__":
    host = sys.argv[1]
    password = sys.argv[2]
    cmd = sys.argv[3]
    timeout = int(sys.argv[4]) if len(sys.argv) > 4 else 30
    rc, out, err = run(host, password, cmd, timeout=timeout)
    sys.stdout.write(out)
    if err:
        sys.stderr.write(err)
    sys.exit(rc)
