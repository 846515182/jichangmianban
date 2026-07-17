package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"net"
	"time"
)

// ProxyHealth 代理连通性检查结果
type ProxyHealth struct {
	ProxyReachable bool   `json:"proxy_reachable"`  // REALITY 代理是否可用
	ProxyLatencyMs int64  `json:"proxy_latency_ms"` // 自连延迟(毫秒)
	ProxyError     string `json:"proxy_error"`      // 失败时的错误描述
	LastCheckTime  int64  `json:"last_check_time"`  // 上次检查的 unix 时间戳
}

// CheckProxyHealth 对本地 Xray 代理端口做 REALITY 自连测试
// 检查项: TCP 连接 → TLS 握手(收到 Server Hello + Certificate)
// 注意: 这不是完整的 REALITY 认证(需要 X25519 密钥协商)，但能覆盖
// xray 进程崩溃、端口未监听、dest 服务器不可达等常见故障
func (a *Agent) CheckProxyHealth() ProxyHealth {
	health := ProxyHealth{
		LastCheckTime: time.Now().Unix(),
	}

	port := a.effectivePort
	if port == 0 {
		port = 443
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	start := time.Now()

	// 1. TCP 连接检查(3s 超时)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		health.ProxyError = fmt.Sprintf("TCP 连接 %s 失败: %v", addr, err)
		return health
	}
	defer conn.Close()

	// 2. TLS 握手检查(5s 超时) — 验证 xray 能响应 TLS Client Hello
	// REALITY 会代理 dest 服务器的 TLS 响应, 故 cert 是 dest 的 (动态, 不可信)
	// 通过环境变量 NODE_AGENT_TLS_VERIFY 控制:
	//   - "true"  : 严格校验 (要求 cert 由系统 CA 签发, 仅适合直连节点)
	//   - "false" : 跳过校验 (默认, 适用于 REALITY 反代目标)
	// 安全注意: 即使跳过证书校验, TLS 仍提供传输加密, 仅无法防中间人替换目标
	skipVerify := os.Getenv("NODE_AGENT_TLS_VERIFY") != "true"
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: skipVerify,
		ServerName:         "gateway.icloud.com",
	})
	if err := tlsConn.Handshake(); err != nil {
		health.ProxyLatencyMs = time.Since(start).Milliseconds()
		health.ProxyError = fmt.Sprintf("TLS 握手失败: %v", err)
		return health
	}

	// 3. 检查是否收到证书
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		health.ProxyLatencyMs = time.Since(start).Milliseconds()
		health.ProxyError = "TLS 握手成功但未收到证书"
		return health
	}

	health.ProxyReachable = true
	health.ProxyLatencyMs = time.Since(start).Milliseconds()
	return health
}

// healthJSON 返回 /healthz 端点的 JSON 响应
func (a *Agent) healthJSON() []byte {
	health := a.CheckProxyHealth()
	b, _ := json.Marshal(health)
	return b
}
