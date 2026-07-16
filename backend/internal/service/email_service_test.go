package service

import (
	"bufio"
	"io"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"nexus-panel/internal/config"
)

// ============================================================
// resolveFrom 单测
// ============================================================

func TestResolveFrom(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *EmailConfig
		want    string
		wantErr bool
	}{
		{
			name: "From 为合法邮箱, 直接使用",
			cfg:  &EmailConfig{From: "noreply@example.com", User: "api"},
			want: "noreply@example.com",
		},
		{
			name: "From 为空但 User 是邮箱, 回退到 User",
			cfg:  &EmailConfig{From: "", User: "user@example.com"},
			want: "user@example.com",
		},
		{
			name: "From 不含 @ 且 User 不是邮箱 (Mailtrap api), 应报错",
			cfg:  &EmailConfig{From: "", User: "api"},
			wantErr: true,
		},
		{
			name: "From 含 @ 但 User 非 (Mailtrap 新版), 用 From",
			cfg:  &EmailConfig{From: "verified@domain.com", User: "api"},
			want: "verified@domain.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveFrom(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错, 实际 got=%q err=%v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("非预期错误: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

// ============================================================
// mergeConfig 单测: DB 优先 -> env 回退 -> 都没有
// ============================================================

func TestMergeConfig(t *testing.T) {
	dbComplete := &EmailConfig{Enabled: true, Host: "smtp.db.com", Port: 587, User: "dbu", Password: "dbp", From: "db@db.com"}
	envCfg := &config.Config{SMTPHost: "smtp.env.com", SMTPPort: 465, SMTPUser: "envu", SMTPPass: "envp", SMTPFrom: "env@env.com"}

	t.Run("DB 完整且启用, 优先 DB", func(t *testing.T) {
		got := mergeConfig(dbComplete, envCfg)
		if got.Host != "smtp.db.com" {
			t.Fatalf("应优先 DB 配置, got Host=%q", got.Host)
		}
		if !got.Enabled {
			t.Fatalf("DB 配置应为启用")
		}
	})

	t.Run("DB 未启用, 回退 env", func(t *testing.T) {
		dbDisabled := &EmailConfig{Enabled: false, Host: "smtp.db.com", Port: 587}
		got := mergeConfig(dbDisabled, envCfg)
		if got.Host != "smtp.env.com" {
			t.Fatalf("应回退 env 配置, got Host=%q", got.Host)
		}
		if got.Port != 465 {
			t.Fatalf("env Port 应为 465, got %d", got.Port)
		}
		if !got.Enabled {
			t.Fatalf("env 回退应为启用")
		}
	})

	t.Run("DB 启用但不完整(缺密码), 回退 env", func(t *testing.T) {
		dbIncomplete := &EmailConfig{Enabled: true, Host: "smtp.db.com", Port: 587, User: "dbu", Password: ""}
		got := mergeConfig(dbIncomplete, envCfg)
		if got.Host != "smtp.env.com" {
			t.Fatalf("DB 不完整应回退 env, got Host=%q", got.Host)
		}
	})

	t.Run("DB 与 env 均无, 返回 DB(未启用)", func(t *testing.T) {
		dbEmpty := &EmailConfig{Enabled: false, Port: 587}
		got := mergeConfig(dbEmpty, nil)
		if got.Enabled {
			t.Fatalf("应未启用")
		}
	})

	t.Run("env Port 为 0 时默认 587", func(t *testing.T) {
		envZero := &config.Config{SMTPHost: "smtp.env.com", SMTPPort: 0, SMTPUser: "u", SMTPPass: "p", SMTPFrom: "e@e.com"}
		dbEmpty := &EmailConfig{Enabled: false}
		got := mergeConfig(dbEmpty, envZero)
		if got.Port != 587 {
			t.Fatalf("Port=0 应默认 587, got %d", got.Port)
		}
	})
}

// ============================================================
// sendMailWithTLS 单测: 使用伪造 SMTP 服务器验证端到端发送
// ============================================================

// fakeSMTPServer 伪造一个最小 SMTP 服务器, 可控制是否在 EHLO 应答中宣告 STARTTLS。
type fakeSMTPServer struct {
	ln                net.Listener
	advertiseSTARTTLS bool
	received          []byte // DATA 阶段接收到的原始邮件内容
	done              chan struct{}
}

func newFakeSMTPServer(t *testing.T, advertiseSTARTTLS bool) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{ln: ln, advertiseSTARTTLS: advertiseSTARTTLS, done: make(chan struct{})}
	go s.serve()
	return s
}

func (s *fakeSMTPServer) addr() string { return s.ln.Addr().String() }

func (s *fakeSMTPServer) serve() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	writeLine := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	writeLine("220 fake.smtp ESMTP ready")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			if s.advertiseSTARTTLS {
				writeLine("250-fake.smtp")
				writeLine("250 STARTTLS")
				// 宣告 STARTTLS 后关闭连接, 触发客户端 StartTLS 握手失败
				return
			}
			writeLine("250-fake.smtp")
			writeLine("250 AUTH PLAIN")
		case strings.HasPrefix(verb, "AUTH PLAIN"):
			writeLine("235 2.7.0 Authentication successful")
		case strings.HasPrefix(verb, "MAIL FROM"):
			writeLine("250 2.1.0 Ok")
		case strings.HasPrefix(verb, "RCPT TO"):
			writeLine("250 2.1.5 Ok")
		case strings.HasPrefix(verb, "DATA"):
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			// 读取 DATA 内容直到单独一行的 "."
			for {
				dl, derr := r.ReadString('\n')
				if derr != nil {
					return
				}
				if strings.TrimSpace(dl) == "." {
					break
				}
				s.received = append(s.received, dl...)
			}
			writeLine("250 2.0.0 Ok: queued")
		case strings.HasPrefix(verb, "QUIT"):
			writeLine("221 2.0.0 Bye")
			return
		case strings.HasPrefix(verb, "RSET"), strings.HasPrefix(verb, "NOOP"):
			writeLine("250 Ok")
		default:
			writeLine("500 Command unrecognized")
		}
	}
}

func (s *fakeSMTPServer) close() {
	_ = s.ln.Close()
	<-s.done
}

// TestSendMailWithTLS_PlainEndToEnd 验证非 465、不宣告 STARTTLS 的标准发送流程,
// 并校验最终投递的邮件内容(From/To/Subject/Body)。
func TestSendMailWithTLS_PlainEndToEnd(t *testing.T) {
	srv := newFakeSMTPServer(t, false)
	defer srv.close()

	from := "noreply@example.com"
	to := []string{"user@example.com"}
	subject := "测试主题"
	body := "这是测试正文"
	msg := []byte("From: Nexus-Panel <" + from + ">\r\nTo: " + strings.Join(to, ",") +
		"\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	auth := smtp.PlainAuth("", "127.0.0.1-user", "pass", "127.0.0.1")

	if err := sendMailWithTLS(srv.addr(), auth, from, to, msg); err != nil {
		t.Fatalf("发送失败: %v", err)
	}

	got := string(srv.received)
	for _, want := range []string{from, to[0], subject, body} {
		if !strings.Contains(got, want) {
			t.Errorf("邮件内容缺失 %q, 收到:\n%s", want, got)
		}
	}
}

// TestSendMailWithTLS_STARTTLSBranch 验证服务器宣告 STARTTLS 时, 客户端会尝试升级,
// 因伪造服务器未真正完成 TLS 握手, 应返回 STARTTLS 相关错误。
func TestSendMailWithTLS_STARTTLSBranch(t *testing.T) {
	srv := newFakeSMTPServer(t, true)
	defer srv.close()

	auth := smtp.PlainAuth("", "u", "p", "127.0.0.1")
	err := sendMailWithTLS(srv.addr(), auth, "a@b.com", []string{"x@y.com"}, []byte("x"))
	if err == nil {
		t.Fatal("期望 STARTTLS 升级失败错误, 实际 nil")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("错误应包含 STARTTLS, got: %v", err)
	}
}

// TestSendMailWithTLS_Port465UsesTLS 验证 465 端口走隐式 TLS 拨号路径
// (错误信息应来自 TLS 拨号, 而非明文 "连接失败")。
func TestSendMailWithTLS_Port465UsesTLS(t *testing.T) {
	auth := smtp.PlainAuth("", "u", "p", "127.0.0.1")
	// 127.0.0.1:465 通常无服务/被拒, 关键是验证代码走了 TLS 拨号分支
	err := sendMailWithTLS("127.0.0.1:465", auth, "a@b.com", []string{"x@y.com"}, []byte("x"))
	if err == nil {
		t.Skip("127.0.0.1:465 意外存在可用 TLS 服务, 跳过")
	}
	if !strings.Contains(err.Error(), "TLS 连接失败") {
		t.Fatalf("465 应走 TLS 拨号路径, 错误应含 'TLS 连接失败', got: %v", err)
	}
}

// TestSendMailWithTLS_EmptyRecipients 受测对象 SendMail 的前置校验 (空收件人)。
func TestSendMailWithTLS_EmptyRecipients(t *testing.T) {
	s := &EmailService{}
	if err := s.SendMail(nil, "s", "b"); err == nil {
		t.Fatal("空收件人应报错")
	}
}

// 避免未使用导入告警 (io 供未来扩展)
var _ = io.EOF
var _ = time.Second
