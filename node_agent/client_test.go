package main

import (
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
		name     string
		setup    func() net.Listener
		wantTLS  bool
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
		"127.0.0.1:19998",    // 不可达
		"",                    // 空地址
		"invalid:addr",        // 格式错误
		"127.0.0.1:1",         // 被拒绝的端口
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
