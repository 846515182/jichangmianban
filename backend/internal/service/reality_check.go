package service

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// 已知与 REALITY 不兼容的 dest 服务器(按域名后缀匹配)
// 这些服务器的 TLS 实现会导致 REALITY handshakeStatus: false
var incompatibleRealityDestSuffixes = []string{
	"www.microsoft.com",      // AkamaiGHost，TLS 实现与 REALITY 嫁接不兼容
	"microsoft.com",          // 同上
	"www.apple.com",          // Apple 的某些 CDN 节点偶发不兼容
	"www.amazon.com",         // Amazon CloudFront TLS 扩展不兼容
	"aws.amazon.com",         // 同上
}

// CheckRealityDest 检查 dest 服务器是否与 REALITY 兼容
// 1. 域名黑名单检查(已知不兼容的 Akamai/CloudFront 等)
// 2. TLS 连通性检查(能否在 5s 内完成 TLS 握手)
// 返回 error 描述不兼容原因，nil 表示兼容
func CheckRealityDest(dest string) error {
	if dest == "" {
		return errors.New("dest 不能为空")
	}

	// dest 格式: host:port
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		// 没有端口，默认 443
		host = dest
		dest = net.JoinHostPort(dest, "443")
	}
	host = strings.ToLower(host)

	// 1. 黑名单检查
	for _, suffix := range incompatibleRealityDestSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return fmt.Errorf("dest %s 已知与 REALITY 不兼容(AkamaiGHost/CloudFront TLS 实现问题)，请使用 gateway.icloud.com 或 www.cloudflare.com", host)
		}
	}

	// 2. TLS 连通性检查(5s 超时)
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second},
		"tcp",
		dest,
		&tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		},
	)
	if err != nil {
		return fmt.Errorf("dest %s TLS 连接失败: %v", dest, err)
	}
	defer conn.Close()

	// 检查证书是否有效(tls.DialWithDialer 已做校验，到这里说明证书 OK)
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("dest %s 未返回证书", dest)
	}

	return nil
}

// extractRealityDest 从 server_config map 中提取 reality.dest
func extractRealityDest(cfgMap map[string]interface{}) string {
	if reality, ok := cfgMap["reality"].(map[string]interface{}); ok {
		if dest, ok := reality["dest"].(string); ok && dest != "" {
			return dest
		}
	}
	return ""
}
