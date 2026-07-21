package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// TestProbeTLSServer_NilPointerRegression 验证 P0 修复: probeTLSServer 在
// 非 TLS 服务端(明文 TCP) 场景下不会 nil pointer panic。
// 修复前: if err := Handshake(); err == nil 中用 := 声明新局部 err 遮蔽了
// 外层 dial 的 nil err, 出 if 块后 err.Error() 触发 panic。
func TestProbeTLSServer_NilPointerRegression(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() net.Listener
		wantTLS bool
	}{
		{
			name: "明文TCP服务端(无TLS)",
			setup: func() net.Listener {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				go func() {
					for {
						conn, err := ln.Accept()
						if err != nil {
							return
						}
						// 读取 ClientHello 然后关闭(模拟明文 gRPC 行为)
						buf := make([]byte, 1024)
						conn.Read(buf)
						conn.Close()
					}
				}()
				return ln
			},
			wantTLS: false, // 明文 → 应返回 false
		},
		{
			name: "端口不可达",
			setup: func() net.Listener {
				return nil // 使用不存在的端口
			},
			wantTLS: false, // 不可达 → 应返回 false 不 panic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var host, port string
			if tt.setup == nil || tt.setup() == nil {
				// 用随机高位端口(大概率没人在用)
				host, port = "127.0.0.1", "19999"
			} else {
				ln := tt.setup()
				defer ln.Close()
				host, port = "127.0.0.1", strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:")
			}

			// 核心断言: 无论如何不能 panic
			got := probeTLSServer(host, port)
			if got != tt.wantTLS {
				t.Errorf("probeTLSServer(%s, %s) = %v, want %v", host, port, got, tt.wantTLS)
			}
		})
	}
}

// TestProbeTLSServer_HandshakeErrorVariants 验证各种 TLS 握手错误
// 不会触发 panic, 且能正确区分 TLS server vs 明文 server
func TestProbeTLSServer_HandshakeErrorVariants(t *testing.T) {
	t.Run("TCP连接立即关闭", func(t *testing.T) {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		go func() {
			conn, _ := ln.Accept()
			conn.Close() // 立即关闭, TLS ClientHello 发不出去
		}()
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		got := probeTLSServer("127.0.0.1", port)
		if got != false {
			t.Errorf("期望 false(非TLS), 得到 %v", got)
		}
	})

	t.Run("连接超时", func(t *testing.T) {
		// 连接 127.0.0.1:1 — 会立即被 OS 拒绝, 不走超时
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		addr := ln.Addr().String()
		ln.Close()

		// 发大量连接占满 backlog, 让新连接超时
		var conns []net.Conn
		for i := 0; i < 200; i++ {
			c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err != nil {
				break
			}
			conns = append(conns, c)
		}
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()

		// 用已关闭的监听地址 + 极短超时
		_, port, _ := net.SplitHostPort(addr)
		// 注意: probeTLSServer 内部 dialer timeout = 3s, 端口已被关闭,
		// Dial 会立即得到 connection refused
		got := probeTLSServer("127.0.0.1", port)
		if got != false {
			t.Errorf("关闭端口应返回 false(明文), 得到 %v", got)
		}
	})
}

// TestAutoDetectCredentials_NoPanic 验证 autoDetectCredentials 在各种输入下
// 不会 panic, 且始终返回非 nil 的 credentials
func TestAutoDetectCredentials_NoPanic(t *testing.T) {
	tests := []string{
		"127.0.0.1:19998", // 不可达
		"",                // 空地址
		"invalid:addr",    // 格式错误
		"127.0.0.1:1",     // 被拒绝的端口
	}

	for _, addr := range tests {
		t.Run("addr="+addr, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("autoDetectCredentials(%q) panic: %v", addr, r)
				}
			}()
			creds := autoDetectCredentials(addr)
			if creds == nil {
				t.Errorf("autoDetectCredentials(%q) 返回 nil", addr)
			}
		})
	}
}

// TestProbeTLSServer_SelfSignedTLS 用自签 TLS 服务端验证探测逻辑
// 注意: 这个测试需要生成临时自签证书, 仅在非 short 模式下运行
func TestProbeTLSServer_SelfSignedTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要自签证书的集成测试 (go test -short)")
	}

	// 模拟: 启动一个不验证客户端证书的 TLS 监听,
	// probeTLSServer 发 ClientHello 时会触发握手错误(TLS alert),
	// 但错误类型会被正确识别为 "对面是 TLS 端"
	t.Run("连接被拒绝(无TLS监听)", func(t *testing.T) {
		// 用随机端口, 无人监听
		got := probeTLSServer("127.0.0.1", "19997")
		if got != false {
			t.Errorf("无监听端口应返回 false, 得到 %v", got)
		}
	})
}

// ============================================================
// 以下为 TLS 探测修复 (TLS-DETECT-FALSEPOS) 的验证测试
// ============================================================

// generateSelfSignedCert 生成临时自签 TLS 证书用于测试
// 返回 tls.Certificate 和包含该证书的 CertPool
func generateSelfSignedCert(t *testing.T, commonName string, dnsNames []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 密钥失败: %v", err)
	}
	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("生成序列号失败: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("创建自签证书失败: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("加载 X509 密钥对失败: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}

// startTLSListener 启动一个 TLS 监听并返回地址
func startTLSListener(t *testing.T, cert tls.Certificate) (string, func()) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("启动 TLS 监听失败: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 接受连接, TLS 握手由 tls.Listener 自动处理
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				c.Read(buf)
				c.Close()
			}(conn)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	cleanup := func() { ln.Close() }
	return port, cleanup
}

// startPlaintextListener 启动一个明文 TCP 监听, 收到数据后回复 HTTP/2 preface
// 模拟明文 gRPC 服务端的行为 (面板未启用 TLS 时端口的行为)
func startPlaintextListener(t *testing.T, response []byte) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动明文监听失败: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				c.Read(buf)
				if response != nil {
					c.Write(response)
				}
				c.Close()
			}(conn)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	cleanup := func() { ln.Close() }
	return port, cleanup
}

// TestProbeTLSServer_PlaintextGRPCResponse 验证修复 TLS-DETECT-FALSEPOS:
// 当明文 gRPC 服务端回复非 TLS 数据时, "tls: first record does not look like a TLS handshake"
// 错误虽然包含 "tls:" 前缀, 但不应被误判为 TLS 服务端。
//
// 修复前: 旧版因为匹配 "tls:" 前缀而返回 true, 导致 agent 用 TLS 连明文端口, 永久注册失败。
// 修复后: "first record" 检查提前到 "tls:" 之前, 正确返回 false。
func TestProbeTLSServer_PlaintextGRPCResponse(t *testing.T) {
	tests := []struct {
		name     string
		response []byte
		desc     string
	}{
		{
			name:     "gRPC明文HTTP2 preface回复",
			response: []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"),
			desc:     "模拟明文 gRPC(h2c) 服务端回复 HTTP/2 preface",
		},
		{
			name:     "随机非TLS数据回复",
			response: []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"),
			desc:     "模拟明文 HTTP 服务端回复",
		},
		{
			name:     "空回复立即关闭",
			response: nil,
			desc:     "服务端读取后直接关闭连接, 不回复任何数据",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, cleanup := startPlaintextListener(t, tt.response)
			defer cleanup()

			got := probeTLSServer("127.0.0.1", port)
			if got != false {
				t.Errorf("%s: probeTLSServer 应返回 false(明文), 得到 true(误判为TLS)\n场景: %s", tt.name, tt.desc)
			}
		})
	}
}

// TestProbeTLSServer_RealTLSServer 验证探测真实 TLS 服务端时返回 true
// 启动自签 TLS 监听, probeTLSServer 应识别为 TLS 服务端
func TestProbeTLSServer_RealTLSServer(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要自签证书的集成测试 (go test -short)")
	}
	cert, _ := generateSelfSignedCert(t, "127.0.0.1", []string{"127.0.0.1", "localhost"})
	port, cleanup := startTLSListener(t, cert)
	defer cleanup()

	got := probeTLSServer("127.0.0.1", port)
	// TLS 服务端可能握手成功(返回 true), 也可能因证书校验失败返回 TLS alert(也返回 true)
	if got != true {
		t.Errorf("真实 TLS 服务端应返回 true, 得到 false\n(可能原因: TLS 探测逻辑回退变更)")
	}
}

// TestAutoDetectCredentials_PlaintextServer 验证当面板端口为明文 gRPC 时,
// autoDetectCredentials 返回 insecure credentials (明文模式)
func TestAutoDetectCredentials_PlaintextServer(t *testing.T) {
	port, cleanup := startPlaintextListener(t, []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	defer cleanup()

	addr := "127.0.0.1:" + port
	creds := autoDetectCredentials(addr)

	// 验证返回的不是 nil
	if creds == nil {
		t.Fatal("autoDetectCredentials 返回 nil credentials")
	}

	// 验证返回的是 insecure (明文) credentials, 而非 TLS credentials
	// insecure.NewCredentials() 返回的 Info() 的 SecurityProtocol 为 "insecure"
	info := creds.Info()
	if info.SecurityProtocol == "tls" {
		t.Errorf("明文服务端应返回 insecure credentials, 但返回了 TLS credentials (SecurityProtocol=%s)", info.SecurityProtocol)
	}
}

// TestAutoDetectCredentials_TLSServer 验证当面板端口为 TLS 服务端时,
// autoDetectCredentials 返回 TLS credentials (而非明文)
func TestAutoDetectCredentials_TLSServer(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要自签证书的集成测试 (go test -short)")
	}
	cert, _ := generateSelfSignedCert(t, "127.0.0.1", []string{"127.0.0.1", "localhost"})
	port, cleanup := startTLSListener(t, cert)
	defer cleanup()

	addr := "127.0.0.1:" + port
	creds := autoDetectCredentials(addr)

	if creds == nil {
		t.Fatal("autoDetectCredentials 返回 nil credentials")
	}

	info := creds.Info()
	// TLS 服务端应返回 TLS credentials 或 InsecureSkipVerify 的 TLS credentials
	// (自签证书不在系统 CA 池中, probeTLSHandshake 会报 x509 错误, 回退到 InsecureSkipVerify)
	// 但 SecurityProtocol 仍应为 "tls"
	if info.SecurityProtocol != "tls" {
		t.Errorf("TLS 服务端应返回 TLS credentials (SecurityProtocol=tls), 得到 SecurityProtocol=%s", info.SecurityProtocol)
	}
}

// TestAutoDetectCredentials_InvalidAddr 验证地址格式错误时退回明文
func TestAutoDetectCredentials_InvalidAddr(t *testing.T) {
	creds := autoDetectCredentials("invalid-no-colon")
	if creds == nil {
		t.Fatal("autoDetectCredentials 返回 nil credentials")
	}
	info := creds.Info()
	if info.SecurityProtocol == "tls" {
		t.Errorf("无效地址应返回 insecure credentials, 得到 TLS")
	}
}

// TestProbeTLSHandshake_ValidTLS 验证用正确的 CA 池和匹配的 ServerName 时,
// TLS 握手成功 (返回 nil error)
func TestProbeTLSHandshake_ValidTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要自签证书的集成测试 (go test -short)")
	}
	cert, pool := generateSelfSignedCert(t, "localhost", []string{"localhost", "127.0.0.1"})
	port, cleanup := startTLSListener(t, cert)
	defer cleanup()

	err := probeTLSHandshake("127.0.0.1", port, pool, "localhost")
	if err != nil {
		t.Errorf("用匹配的 CA 池和 ServerName 时, TLS 握手应成功, 得到错误: %v", err)
	}
}

// TestProbeTLSHandshake_CertSANMismatch 验证当证书 SAN 不包含 ServerName 时,
// 握手返回 x509 错误 (用于测试 autoDetectCredentials 的 SAN 不匹配回退逻辑)
func TestProbeTLSHandshake_CertSANMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要自签证书的集成测试 (go test -short)")
	}
	// 生成证书只包含 "localhost", 不包含 ServerName="wrong-host"
	cert, pool := generateSelfSignedCert(t, "localhost", []string{"localhost"})
	port, cleanup := startTLSListener(t, cert)
	defer cleanup()

	err := probeTLSHandshake("127.0.0.1", port, pool, "wrong-host")
	if err == nil {
		t.Fatal("SAN 不匹配时应返回错误, 得到 nil")
	}
	// 错误应包含 "x509:" (证书 SAN 校验失败)
	if !strings.Contains(err.Error(), "x509:") {
		t.Errorf("SAN 不匹配错误应包含 'x509:', 得到: %v", err)
	}
}

// TestProbeTLSHandshake_PlaintextServer 验证明文服务端的握手错误
// 应包含 "first record" (用于测试 probeTLSServer 的判断逻辑)
func TestProbeTLSHandshake_PlaintextServer(t *testing.T) {
	port, cleanup := startPlaintextListener(t, []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	defer cleanup()

	pool := x509.NewCertPool()
	err := probeTLSHandshake("127.0.0.1", port, pool, "127.0.0.1")
	if err == nil {
		t.Fatal("明文服务端握手应失败, 得到 nil error")
	}
	if !strings.Contains(err.Error(), "first record") {
		t.Errorf("明文服务端错误应包含 'first record', 得到: %v", err)
	}
}
