#!/usr/bin/env python3
"""运维脚本共享配置。

安全说明：敏感凭据（SSH 密码、Redis 密码、节点 IP）一律从环境变量读取，
禁止硬编码入库。请在运行脚本前通过环境变量或 .env 文件提供。

示例:
    $env:OPS_NODE_HOST="1.2.3.4"; $env:OPS_NODE_SSH_PWD="xxx"; python check_node_state.py
"""
import os


def _read(name: str, default: str = "") -> str:
    val = os.environ.get(name, default)
    if not val:
        print(f"[ops_config] 警告: 环境变量 {name} 未设置，请先配置（参见 .env.example）", flush=True)
    return val


# 节点 SSH 连接
NODE_HOST = _read("OPS_NODE_HOST", "127.0.0.1")
NODE_SSH_PWD = _read("OPS_NODE_SSH_PWD", "")

# Redis 密码
REDIS_PWD = _read("OPS_REDIS_PWD", "")
