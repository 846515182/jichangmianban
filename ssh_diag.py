#!/usr/bin/env python3
# Diagnose SSH auth methods + try keyboard-interactive over HTTP proxy
import sys, socket, paramiko, traceback

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


def diag(host, password, port=22):
    sock = make_proxy_sock(host, port)
    t = paramiko.Transport(sock)
    t.connect()  # SSH banner/kex
    print("Server version:", t.remote_version)

    # 1. auth_none -> reveals allowed auth methods
    try:
        t.auth_none("root")
        print("auth_none succeeded (no auth required?!)")
    except paramiko.BadAuthenticationType as e:
        print("Allowed auth methods:", e.allowed_types)
        allowed = e.allowed_types
    except Exception as e:
        print("auth_none error:", repr(e))
        allowed = []

    # 2. try password
    try:
        t.auth_password("root", password)
        print("password auth: SUCCESS")
        t.close()
        return
    except Exception as e:
        print("password auth failed:", repr(e))

    # 3. try keyboard-interactive
    if "keyboard-interactive" in allowed or True:
        try:
            def handler(title, instructions, prompt_list):
                return [password] * len(prompt_list)
            t.auth_interactive("root", handler)
            print("keyboard-interactive auth: SUCCESS")
            t.close()
            return
        except Exception as e:
            print("keyboard-interactive auth failed:", repr(e))

    t.close()


if __name__ == "__main__":
    host = sys.argv[1]
    password = sys.argv[2]
    try:
        diag(host, password)
    except Exception:
        traceback.print_exc()
