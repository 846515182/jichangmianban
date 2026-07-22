#!/usr/bin/env python3
# SFTP upload over HTTP-CONNECT proxy tunnel
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


def upload(host, password, local_path, remote_path, port=22):
    sock = make_proxy_sock(host, port)
    transport = paramiko.Transport(sock)
    transport.connect(username="root", password=password)
    sftp = paramiko.SFTPClient.from_transport(transport)
    sftp.put(local_path, remote_path)
    sftp.close()
    transport.close()


if __name__ == "__main__":
    # usage: ssh_sftp.py <host> <password> <local_path> <remote_path>
    upload(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4])
    print("uploaded %s -> %s" % (sys.argv[3], sys.argv[4]))
